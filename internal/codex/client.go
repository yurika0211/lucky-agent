// Package codex contains the small JSON-RPC bridge used by LuckyAgent to
// drive a local `codex app-server` process. It intentionally keeps Codex's
// wire objects opaque: the app-server owns coding policy and LuckyAgent only
// tracks lifecycle, events, and approval decisions.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	Command        []string
	ApprovalMode   string
	DefaultSandbox string
	CWDAllowlist   []string
	MaxEvents      int
}

type RPCRequest struct {
	ID     json.RawMessage
	Method string
	Params map[string]any
}

type rpcEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params map[string]any  `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

type client struct {
	command []string

	startMu sync.Mutex
	mu      sync.Mutex
	writeMu sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{}
	closed  bool
	nextID  atomic.Int64
	pending map[string]chan rpcResult

	onRequest      func(RPCRequest)
	onNotification func(string, map[string]any)
}

func newClient(command []string, onRequest func(RPCRequest), onNotification func(string, map[string]any)) *client {
	return &client{
		command:        append([]string(nil), command...),
		pending:        make(map[string]chan rpcResult),
		onRequest:      onRequest,
		onNotification: onNotification,
	}
}

func (c *client) ensure(ctx context.Context) error {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("codex app-server client is closed")
	}
	if c.cmd != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	if len(c.command) == 0 || strings.TrimSpace(c.command[0]) == "" {
		return errors.New("codex app-server command is empty")
	}
	cmd := exec.Command(c.command[0], c.command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("codex app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("codex app-server stdout: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start codex app-server: %w", err)
	}
	c.mu.Lock()
	c.cmd, c.stdin, c.done = cmd, stdin, make(chan struct{})
	c.mu.Unlock()
	go c.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		c.finish(err)
	}()

	// initialize is the one mandatory handshake before normal requests. Keep
	// the payload small and advertise experimental APIs used by modern Codex.
	if _, err := c.callStarted(ctx, "initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    "luckyagent",
			"title":   "LuckyAgent Codex bridge",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		_ = c.closeProcess()
		return fmt.Errorf("initialize codex app-server: %w", err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		_ = c.closeProcess()
		return fmt.Errorf("initialize codex app-server notification: %w", err)
	}
	return nil
}

func (c *client) callStarted(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	return c.callInternal(ctx, method, params)
}

func (c *client) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	return c.callInternal(ctx, method, params)
}

func (c *client) callInternal(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := c.nextID.Add(1)
	idKey := strconv.FormatInt(id, 10)
	ch := make(chan rpcResult, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("codex app-server client is closed")
	}
	c.pending[idKey] = ch
	c.mu.Unlock()
	message := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := c.write(message); err != nil {
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case result := <-ch:
		return result.result, result.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, idKey)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.doneChan():
		return nil, errors.New("codex app-server exited")
	}
}

func (c *client) notify(method string, params map[string]any) error {
	message := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		message["params"] = params
	}
	return c.write(message)
}

func (c *client) respond(id json.RawMessage, result any) error {
	if len(id) == 0 {
		return errors.New("codex approval request has no id")
	}
	return c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func (c *client) write(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode codex JSON-RPC message: %w", err)
	}
	payload = append(payload, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	closed := c.closed
	c.mu.Unlock()
	if closed || stdin == nil {
		return errors.New("codex app-server is not running")
	}
	if _, err := stdin.Write(payload); err != nil {
		return fmt.Errorf("write codex app-server: %w", err)
	}
	return nil
}

func (c *client) readLoop(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var message rpcEnvelope
		if err := json.Unmarshal(line, &message); err != nil {
			if c.onNotification != nil {
				c.onNotification("codex/protocol/error", map[string]any{"error": err.Error()})
			}
			continue
		}
		if message.Method != "" {
			request := RPCRequest{ID: append(json.RawMessage(nil), message.ID...), Method: message.Method, Params: message.Params}
			if len(message.ID) > 0 && c.onRequest != nil {
				c.onRequest(request)
			} else if c.onNotification != nil {
				c.onNotification(message.Method, message.Params)
			}
			continue
		}
		if len(message.ID) == 0 {
			continue
		}
		idKey := strings.Trim(string(message.ID), "\"")
		c.mu.Lock()
		ch := c.pending[idKey]
		if ch != nil {
			delete(c.pending, idKey)
		}
		c.mu.Unlock()
		if ch == nil {
			continue
		}
		if message.Error != nil {
			ch <- rpcResult{err: fmt.Errorf("codex RPC %d: %s", message.Error.Code, message.Error.Message)}
		} else {
			ch <- rpcResult{result: message.Result}
		}
	}
	if err := scanner.Err(); err != nil && c.onNotification != nil {
		c.onNotification("codex/protocol/error", map[string]any{"error": err.Error()})
	}
}

func (c *client) doneChan() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return c.done
}

func (c *client) finish(waitErr error) {
	c.mu.Lock()
	if c.done == nil {
		c.mu.Unlock()
		return
	}
	done := c.done
	c.done = nil
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- rpcResult{err: fmt.Errorf("codex app-server exited: %v", waitErr)}
	}
	c.cmd, c.stdin = nil, nil
	c.mu.Unlock()
	select {
	case <-done:
	default:
		close(done)
	}
	if c.onNotification != nil {
		c.onNotification("codex/process/exited", map[string]any{"error": errorString(waitErr)})
	}
}

func (c *client) closeProcess() error {
	c.mu.Lock()
	cmd, stdin := c.cmd, c.stdin
	c.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	return nil
}

func (c *client) close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	return c.closeProcess()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type Thread struct {
	ID        string    `json:"id"`
	CWD       string    `json:"cwd,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Turn struct {
	ID          string    `json:"id"`
	ThreadID    string    `json:"thread_id"`
	Status      string    `json:"status,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Output      string    `json:"output,omitempty"`
	EventCursor int64     `json:"event_cursor"`
}

type Approval struct {
	ID        string         `json:"id"`
	Method    string         `json:"method"`
	ThreadID  string         `json:"thread_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	Reason    string         `json:"reason,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	rpcID     json.RawMessage
}

type Event struct {
	Cursor   int64          `json:"cursor"`
	At       time.Time      `json:"at"`
	Method   string         `json:"method"`
	ThreadID string         `json:"thread_id,omitempty"`
	TurnID   string         `json:"turn_id,omitempty"`
	Summary  string         `json:"summary,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

type Manager struct {
	cfg       Config
	mu        sync.RWMutex
	client    *client
	seq       int64
	threads   map[string]*Thread
	turns     map[string]*Turn
	approvals map[string]*Approval
	events    []Event
}

func NewManager(cfg Config) *Manager {
	if cfg.ApprovalMode == "" {
		cfg.ApprovalMode = "gateway"
	}
	if cfg.DefaultSandbox == "" {
		cfg.DefaultSandbox = "workspace-write"
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 256
	}
	return &Manager{cfg: cfg, threads: make(map[string]*Thread), turns: make(map[string]*Turn), approvals: make(map[string]*Approval)}
}

func (m *Manager) ensureClient(ctx context.Context) (*client, error) {
	m.mu.Lock()
	if m.client != nil {
		c := m.client
		m.mu.Unlock()
		return c, nil
	}
	command := append([]string(nil), m.cfg.Command...)
	if len(command) == 0 {
		command = []string{"codex", "app-server"}
	}
	c := newClient(command, m.handleRequest, m.handleNotification)
	m.client = c
	m.mu.Unlock()
	if err := c.ensure(ctx); err != nil {
		m.mu.Lock()
		if m.client == c {
			m.client = nil
		}
		m.mu.Unlock()
		return nil, err
	}
	return c, nil
}

func (m *Manager) StartThread(ctx context.Context, cwd, sandbox string, approvalPolicy string, extra map[string]any) (Thread, error) {
	cwd, err := m.validateCWD(cwd)
	if err != nil {
		return Thread{}, err
	}
	if sandbox == "" {
		sandbox = m.cfg.DefaultSandbox
	}
	if approvalPolicy == "" {
		approvalPolicy = "on-request"
	}
	params := map[string]any{"cwd": cwd, "sandbox": sandbox, "approvalPolicy": approvalPolicy}
	for key, value := range extra {
		if value != nil {
			params[key] = value
		}
	}
	c, err := m.ensureClient(ctx)
	if err != nil {
		return Thread{}, err
	}
	raw, err := c.call(ctx, "thread/start", params)
	if err != nil {
		return Thread{}, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		return Thread{}, fmt.Errorf("decode thread/start response: %w", err)
	}
	threadMap, _ := response["thread"].(map[string]any)
	id := stringValue(threadMap, "id")
	if id == "" {
		return Thread{}, errors.New("thread/start response did not contain thread.id")
	}
	thread := Thread{ID: id, CWD: cwd, Status: "idle", CreatedAt: time.Now().UTC()}
	if status := stringValue(threadMap, "status"); status != "" {
		thread.Status = status
	}
	m.mu.Lock()
	m.threads[id] = &thread
	m.mu.Unlock()
	m.recordEvent("thread/started", id, "", "Thread started", map[string]any{"threadId": id, "cwd": cwd})
	return thread, nil
}

func (m *Manager) ResumeThread(ctx context.Context, threadID string, cwd string) (Thread, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return Thread{}, errors.New("thread_id is required")
	}
	params := map[string]any{"threadId": threadID}
	if strings.TrimSpace(cwd) != "" {
		var err error
		cwd, err = m.validateCWD(cwd)
		if err != nil {
			return Thread{}, err
		}
		params["cwd"] = cwd
	}
	c, err := m.ensureClient(ctx)
	if err != nil {
		return Thread{}, err
	}
	raw, err := c.call(ctx, "thread/resume", params)
	if err != nil {
		return Thread{}, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		return Thread{}, fmt.Errorf("decode thread/resume response: %w", err)
	}
	threadMap, _ := response["thread"].(map[string]any)
	if id := stringValue(threadMap, "id"); id != "" {
		threadID = id
	}
	thread := Thread{ID: threadID, CWD: cwd, Status: "idle", CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	m.threads[threadID] = &thread
	m.mu.Unlock()
	m.recordEvent("thread/resumed", threadID, "", "Thread resumed", nil)
	return thread, nil
}

func (m *Manager) StartTurn(ctx context.Context, threadID, input, model string) (Turn, error) {
	threadID = strings.TrimSpace(threadID)
	input = strings.TrimSpace(input)
	if threadID == "" || input == "" {
		return Turn{}, errors.New("thread_id and input are required")
	}
	params := map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": input}}}
	if strings.TrimSpace(model) != "" {
		params["model"] = strings.TrimSpace(model)
	}
	c, err := m.ensureClient(ctx)
	if err != nil {
		return Turn{}, err
	}
	raw, err := c.call(ctx, "turn/start", params)
	if err != nil {
		return Turn{}, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		return Turn{}, fmt.Errorf("decode turn/start response: %w", err)
	}
	turnMap, _ := response["turn"].(map[string]any)
	turnID := stringValue(turnMap, "id")
	if turnID == "" {
		return Turn{}, errors.New("turn/start response did not contain turn.id")
	}
	turn := Turn{ID: turnID, ThreadID: threadID, Status: "inProgress", StartedAt: time.Now().UTC()}
	m.mu.Lock()
	m.turns[turnKey(threadID, turnID)] = &turn
	m.mu.Unlock()
	m.recordEvent("turn/started", threadID, turnID, "Turn started", map[string]any{"input": input})
	return turn, nil
}

func (m *Manager) SteerTurn(ctx context.Context, threadID, turnID, input string) error {
	if strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" || strings.TrimSpace(input) == "" {
		return errors.New("thread_id, turn_id and input are required")
	}
	c, err := m.ensureClient(ctx)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, "turn/steer", map[string]any{
		"threadId": threadID, "expectedTurnId": turnID,
		"input": []any{map[string]any{"type": "text", "text": input}},
	})
	if err == nil {
		m.recordEvent("turn/steered", threadID, turnID, "Turn steered", map[string]any{"input": input})
	}
	return err
}

func (m *Manager) RespondApproval(ctx context.Context, approvalID, decision string) (Approval, error) {
	approvalID = strings.TrimSpace(approvalID)
	decision = strings.TrimSpace(decision)
	if approvalID == "" || decision == "" {
		return Approval{}, errors.New("approval_id and decision are required")
	}
	m.mu.RLock()
	approval := m.approvals[approvalID]
	c := m.client
	if approval != nil {
		copyApproval := *approval
		approval = &copyApproval
	}
	m.mu.RUnlock()
	if approval == nil {
		return Approval{}, fmt.Errorf("approval %q is not pending", approvalID)
	}
	if c == nil {
		return Approval{}, errors.New("codex app-server is not running")
	}
	if err := c.respond(approval.rpcID, map[string]any{"decision": decision}); err != nil {
		return Approval{}, err
	}
	m.mu.Lock()
	delete(m.approvals, approvalID)
	if turn := m.turns[turnKey(approval.ThreadID, approval.TurnID)]; turn != nil {
		turn.Status = "inProgress"
	}
	m.mu.Unlock()
	m.recordEvent("serverRequest/resolved", approval.ThreadID, approval.TurnID, "Approval resolved", map[string]any{"approvalId": approvalID, "decision": decision})
	return *approval, nil
}

func (m *Manager) Summary(threadID, turnID string) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := map[string]any{"thread_id": threadID, "turn_id": turnID}
	if threadID != "" {
		if thread := m.threads[threadID]; thread != nil {
			result["thread"] = *thread
		}
	}
	if turnID != "" {
		if turn := m.turns[turnKey(threadID, turnID)]; turn != nil {
			result["turn"] = *turn
		}
	}
	var pending []Approval
	for _, approval := range m.approvals {
		if threadID != "" && approval.ThreadID != threadID {
			continue
		}
		if turnID != "" && approval.TurnID != turnID {
			continue
		}
		pending = append(pending, *approval)
	}
	if len(pending) > 0 {
		result["pending_approvals"] = pending
	}
	return result, nil
}

func (m *Manager) Events(threadID, turnID string, cursor int64, limit int) (map[string]any, error) {
	if limit <= 0 || limit > m.cfg.MaxEvents {
		limit = m.cfg.MaxEvents
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	events := make([]Event, 0, limit)
	for _, event := range m.events {
		if event.Cursor <= cursor || (threadID != "" && event.ThreadID != threadID) || (turnID != "" && event.TurnID != turnID) {
			continue
		}
		events = append(events, event)
		if len(events) >= limit {
			break
		}
	}
	next := cursor
	if len(events) > 0 {
		next = events[len(events)-1].Cursor
	}
	return map[string]any{"events": events, "next_cursor": next}, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	c := m.client
	m.client = nil
	m.mu.Unlock()
	if c == nil {
		return nil
	}
	return c.close()
}

func (m *Manager) handleRequest(request RPCRequest) {
	threadID := stringValue(request.Params, "threadId")
	turnID := stringValue(request.Params, "turnId")
	approvalID := string(request.ID)
	approvalID = strings.Trim(approvalID, "\"")
	approval := &Approval{ID: approvalID, Method: request.Method, ThreadID: threadID, TurnID: turnID, Reason: stringValue(request.Params, "reason"), Summary: approvalSummary(request), Params: request.Params, CreatedAt: time.Now().UTC(), rpcID: append(json.RawMessage(nil), request.ID...)}
	m.mu.Lock()
	m.approvals[approvalID] = approval
	if turn := m.turns[turnKey(threadID, turnID)]; turn != nil {
		turn.Status = "waiting_approval"
	}
	m.mu.Unlock()
	m.recordEvent(request.Method, threadID, turnID, "Approval requested: "+approval.Summary, map[string]any{"approvalId": approvalID, "reason": approval.Reason, "method": request.Method})
	mode := strings.ToLower(strings.TrimSpace(m.cfg.ApprovalMode))
	if mode == "auto" || mode == "deny" {
		decision := "accept"
		if mode == "deny" {
			decision = "decline"
		}
		m.mu.RLock()
		c := m.client
		m.mu.RUnlock()
		if c != nil {
			if err := c.respond(request.ID, map[string]any{"decision": decision}); err != nil {
				m.recordEvent("codex/protocol/error", threadID, turnID, "Approval response failed", map[string]any{"error": err.Error()})
			}
		}
		m.mu.Lock()
		delete(m.approvals, approvalID)
		if turn := m.turns[turnKey(threadID, turnID)]; turn != nil {
			turn.Status = "inProgress"
		}
		m.mu.Unlock()
		m.recordEvent("serverRequest/resolved", threadID, turnID, "Approval resolved", map[string]any{"approvalId": approvalID, "decision": decision})
	}
}

func (m *Manager) handleNotification(method string, params map[string]any) {
	threadID := stringValue(params, "threadId")
	turnID := stringValue(params, "turnId")
	if turnID == "" {
		if turn, ok := params["turn"].(map[string]any); ok {
			turnID = stringValue(turn, "id")
		}
	}
	summary := notificationSummary(method, params)
	if method == "turn/completed" {
		m.mu.Lock()
		if turn := m.turns[turnKey(threadID, turnID)]; turn != nil {
			turn.Status = stringValue(params, "status")
			if turn.Status == "" {
				if raw, ok := params["turn"].(map[string]any); ok {
					turn.Status = stringValue(raw, "status")
				}
			}
			if turn.Status == "" {
				turn.Status = "completed"
			}
			turn.CompletedAt = time.Now().UTC()
			turn.Summary = summary
		}
		m.mu.Unlock()
	}
	if method == "item/agentMessage/delta" {
		m.mu.Lock()
		if turn := m.turns[turnKey(threadID, turnID)]; turn != nil {
			turn.Output = trimOutput(turn.Output + stringValue(params, "delta"))
		}
		m.mu.Unlock()
	}
	if method == "thread/status/changed" {
		if status := stringValue(params, "status"); status != "" {
			m.mu.Lock()
			if thread := m.threads[threadID]; thread != nil {
				thread.Status = status
			}
			m.mu.Unlock()
		}
	}
	m.recordEvent(method, threadID, turnID, summary, compactData(params))
}

func (m *Manager) recordEvent(method, threadID, turnID, summary string, data map[string]any) {
	if summary == "" {
		summary = method
	}
	m.mu.Lock()
	m.seq++
	event := Event{Cursor: m.seq, At: time.Now().UTC(), Method: method, ThreadID: threadID, TurnID: turnID, Summary: summary, Data: data}
	m.events = append(m.events, event)
	if len(m.events) > m.cfg.MaxEvents {
		m.events = append([]Event(nil), m.events[len(m.events)-m.cfg.MaxEvents:]...)
	}
	if turn := m.turns[turnKey(threadID, turnID)]; turn != nil {
		turn.EventCursor = m.seq
	}
	m.mu.Unlock()
}

func (m *Manager) validateCWD(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("cwd is required")
	}
	clean, err := filepathAbs(cwd)
	if err != nil {
		return "", err
	}
	if len(m.cfg.CWDAllowlist) == 0 {
		return clean, nil
	}
	for _, root := range m.cfg.CWDAllowlist {
		allowed, err := filepathAbs(root)
		if err != nil {
			continue
		}
		if clean == allowed || strings.HasPrefix(clean, strings.TrimRight(allowed, string('/'))+string('/')) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside codex.cwd_allowlist", clean)
}

func filepathAbs(path string) (string, error) {
	return filepath.Abs(path)
}

func turnKey(threadID, turnID string) string { return threadID + "\x00" + turnID }

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func approvalSummary(request RPCRequest) string {
	if command := stringValue(request.Params, "command"); command != "" {
		return truncate(command, 500)
	}
	if reason := stringValue(request.Params, "reason"); reason != "" {
		return truncate(reason, 500)
	}
	return request.Method
}

func notificationSummary(method string, params map[string]any) string {
	for _, key := range []string{"message", "text", "delta", "summary", "status", "reason"} {
		if value := stringValue(params, key); value != "" {
			return truncate(value, 500)
		}
	}
	if method == "turn/completed" {
		return "Turn completed"
	}
	return method
}

func compactData(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any)
	for key, value := range values {
		switch key {
		case "threadId", "turnId", "itemId", "status", "reason", "command", "cwd", "delta", "text", "message", "summary", "approvalId":
			if text, ok := value.(string); ok {
				result[key] = truncate(text, 1000)
			} else {
				result[key] = value
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func trimOutput(value string) string {
	return truncate(value, 12000)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
