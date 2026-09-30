// Package grok contains the small JSON-RPC bridge used by LuckyAgent to
// drive a local `grok agent stdio` process. Grok owns the coding loop.
// LuckyAgent tracks sessions, turns, compact events, and approval decisions.
package grok

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
	Command      []string
	ApprovalMode string // gateway, auto, deny
	CWDAllowlist []string
	MaxEvents    int
	Model        string
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
	onExit         func(error)
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
		return errors.New("grok agent client is closed")
	}
	if c.cmd != nil {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()
	if len(c.command) == 0 || strings.TrimSpace(c.command[0]) == "" {
		return errors.New("grok agent command is empty")
	}
	cmd := exec.Command(c.command[0], c.command[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("grok agent stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("grok agent stdout: %w", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start grok agent: %w", err)
	}
	c.mu.Lock()
	c.cmd, c.stdin, c.done = cmd, stdin, make(chan struct{})
	c.mu.Unlock()
	go c.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		c.finish(err)
	}()

	if _, err := c.callStarted(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientInfo": map[string]any{
			"name":    "luckyagent",
			"title":   "LuckyAgent Grok bridge",
			"version": "0.1.0",
		},
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": true, "writeTextFile": true},
			"terminal": true,
		},
	}); err != nil {
		_ = c.closeProcess()
		return fmt.Errorf("initialize grok agent: %w", err)
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
		return nil, errors.New("grok agent client is closed")
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
		return nil, errors.New("grok agent exited")
	}
}

func (c *client) respond(id json.RawMessage, result any) error {
	if len(id) == 0 {
		return errors.New("grok approval request has no id")
	}
	return c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
}

func (c *client) write(message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode grok JSON-RPC message: %w", err)
	}
	payload = append(payload, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	stdin := c.stdin
	closed := c.closed
	c.mu.Unlock()
	if closed || stdin == nil {
		return errors.New("grok agent is not running")
	}
	if _, err := stdin.Write(payload); err != nil {
		return fmt.Errorf("write grok agent: %w", err)
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
				c.onNotification("grok/protocol/error", map[string]any{"error": err.Error()})
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
			ch <- rpcResult{err: fmt.Errorf("grok RPC %d: %s", message.Error.Code, message.Error.Message)}
		} else {
			ch <- rpcResult{result: message.Result}
		}
	}
	if err := scanner.Err(); err != nil && c.onNotification != nil {
		c.onNotification("grok/protocol/error", map[string]any{"error": err.Error()})
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
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- rpcResult{err: fmt.Errorf("grok agent exited: %v", waitErr)}
	}
	c.cmd, c.stdin = nil, nil
	c.mu.Unlock()
	select {
	case <-done:
	default:
		close(done)
	}
	if c.onExit != nil {
		c.onExit(waitErr)
	}
	if c.onNotification != nil {
		c.onNotification("grok/process/exited", map[string]any{"error": errorString(waitErr)})
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

type Session struct {
	ID        string    `json:"id"`
	CWD       string    `json:"cwd,omitempty"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Turn struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	Status      string    `json:"status,omitempty"`
	StopReason  string    `json:"stop_reason,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	Summary     string    `json:"summary,omitempty"`
	Output      string    `json:"output,omitempty"`
	EventCursor int64     `json:"event_cursor"`
}

type Approval struct {
	ID        string             `json:"id"`
	Method    string             `json:"method"`
	SessionID string             `json:"session_id,omitempty"`
	TurnID    string             `json:"turn_id,omitempty"`
	Reason    string             `json:"reason,omitempty"`
	Summary   string             `json:"summary,omitempty"`
	Params    map[string]any     `json:"params,omitempty"`
	Options   []PermissionOption `json:"options,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	rpcID     json.RawMessage
}

// PermissionOption is an ACP option supplied by the agent for one approval.
// The option ID must be echoed back exactly as received.
type PermissionOption struct {
	OptionID string `json:"option_id"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

type Event struct {
	Cursor    int64          `json:"cursor"`
	At        time.Time      `json:"at"`
	Method    string         `json:"method"`
	SessionID string         `json:"session_id,omitempty"`
	TurnID    string         `json:"turn_id,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

type Manager struct {
	cfg       Config
	mu        sync.RWMutex
	client    *client
	seq       int64
	turnSeq   int64
	sessions  map[string]*Session
	turns     map[string]*Turn
	approvals map[string]*Approval
	events    []Event
}

func NewManager(cfg Config) *Manager {
	cfg.ApprovalMode = normalizeApprovalMode(cfg.ApprovalMode)
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = 256
	}
	return &Manager{
		cfg:       cfg,
		sessions:  make(map[string]*Session),
		turns:     make(map[string]*Turn),
		approvals: make(map[string]*Approval),
	}
}

func normalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto", "deny", "gateway":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "gateway"
	}
}

// ApprovalMode reports how ACP permission requests are handled.
func (m *Manager) ApprovalMode() string {
	if m == nil {
		return "gateway"
	}
	return normalizeApprovalMode(m.cfg.ApprovalMode)
}

func (m *Manager) ensureClient(ctx context.Context) (*client, error) {
	m.mu.Lock()
	if m.client != nil {
		c := m.client
		m.mu.Unlock()
		return c, nil
	}
	command := append([]string(nil), m.cfg.Command...)
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		command = []string{"grok", "agent", "stdio"}
	}
	c := newClient(command, m.handleRequest, m.handleNotification)
	c.onExit = func(waitErr error) { m.handleClientExit(c, waitErr) }
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

func (m *Manager) handleClientExit(c *client, waitErr error) {
	m.mu.Lock()
	if m.client != c {
		m.mu.Unlock()
		return
	}
	m.client = nil
	now := time.Now().UTC()
	for _, turn := range m.turns {
		if turn.Status == "inProgress" || turn.Status == "waiting_approval" {
			turn.Status = "failed"
			turn.CompletedAt = now
			turn.Summary = truncate("grok agent exited: "+errorString(waitErr), 500)
		}
	}
	for _, session := range m.sessions {
		if session.Status == "running" {
			session.Status = "failed"
		}
	}
	m.approvals = make(map[string]*Approval)
	m.mu.Unlock()
}

func (m *Manager) StartSession(ctx context.Context, cwd, model string) (Session, error) {
	cwd, err := m.validateCWD(cwd)
	if err != nil {
		return Session{}, err
	}
	params := map[string]any{
		"cwd":        cwd,
		"mcpServers": []any{},
	}
	meta := map[string]any{}
	if model = strings.TrimSpace(model); model == "" {
		model = strings.TrimSpace(m.cfg.Model)
	}
	if model != "" {
		meta["model"] = model
	}
	if len(meta) > 0 {
		params["_meta"] = meta
	}
	c, err := m.ensureClient(ctx)
	if err != nil {
		return Session{}, err
	}
	raw, err := c.call(ctx, "session/new", params)
	if err != nil {
		return Session{}, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		return Session{}, fmt.Errorf("decode session/new response: %w", err)
	}
	id := firstString(response, "sessionId", "session_id")
	if id == "" {
		return Session{}, errors.New("session/new response did not contain sessionId")
	}
	session := Session{ID: id, CWD: cwd, Status: "idle", CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	m.sessions[id] = &session
	m.mu.Unlock()
	m.recordEvent("session/started", id, "", "Session started", map[string]any{"sessionId": id, "cwd": cwd})
	return session, nil
}

func (m *Manager) ResumeSession(ctx context.Context, sessionID, cwd string) (Session, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Session{}, errors.New("session_id is required")
	}
	params := map[string]any{"sessionId": sessionID, "mcpServers": []any{}}
	if strings.TrimSpace(cwd) != "" {
		var err error
		cwd, err = m.validateCWD(cwd)
		if err != nil {
			return Session{}, err
		}
		params["cwd"] = cwd
	}
	c, err := m.ensureClient(ctx)
	if err != nil {
		return Session{}, err
	}
	raw, err := c.call(ctx, "session/load", params)
	if err != nil {
		return Session{}, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		return Session{}, fmt.Errorf("decode session/load response: %w", err)
	}
	if id := firstString(response, "sessionId", "session_id"); id != "" {
		sessionID = id
	}
	session := Session{ID: sessionID, CWD: cwd, Status: "idle", CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	if existing := m.sessions[sessionID]; existing != nil && session.CWD == "" {
		session.CWD = existing.CWD
	}
	m.sessions[sessionID] = &session
	m.mu.Unlock()
	m.recordEvent("session/resumed", sessionID, "", "Session resumed", nil)
	return session, nil
}

// StartTurn sends session/prompt and waits for that prompt to finish.
func (m *Manager) StartTurn(ctx context.Context, sessionID, input, model string) (Turn, error) {
	turn, c, err := m.beginTurn(ctx, sessionID, input, model)
	if err != nil {
		return Turn{}, err
	}
	return m.runTurn(ctx, c, turn.SessionID, turn.ID, input, model)
}

// StartTurnAsync starts a turn and returns immediately. It is used by the
// gateway approval mode so the caller can inspect and resolve approvals while
// the ACP prompt is still running.
func (m *Manager) StartTurnAsync(ctx context.Context, sessionID, input, model string) (Turn, error) {
	turn, c, err := m.beginTurn(ctx, sessionID, input, model)
	if err != nil {
		return Turn{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx := context.WithoutCancel(ctx)
	go func() {
		_, _ = m.runTurn(runCtx, c, turn.SessionID, turn.ID, input, model)
	}()
	return turn, nil
}

func (m *Manager) beginTurn(ctx context.Context, sessionID, input, model string) (Turn, *client, error) {
	sessionID = strings.TrimSpace(sessionID)
	input = strings.TrimSpace(input)
	if sessionID == "" || input == "" {
		return Turn{}, nil, errors.New("session_id and input are required")
	}
	m.mu.Lock()
	session := m.sessions[sessionID]
	if session == nil {
		m.mu.Unlock()
		return Turn{}, nil, fmt.Errorf("session %q is not open", sessionID)
	}
	if session.Status == "running" {
		m.mu.Unlock()
		return Turn{}, nil, fmt.Errorf("session %q already has a running turn", sessionID)
	}
	if session.Status == "failed" {
		m.mu.Unlock()
		return Turn{}, nil, fmt.Errorf("session %q is failed; resume it before starting a turn", sessionID)
	}
	m.turnSeq++
	turnID := fmt.Sprintf("turn-%d", m.turnSeq)
	turn := &Turn{ID: turnID, SessionID: sessionID, Status: "inProgress", StartedAt: time.Now().UTC()}
	m.turns[turnKey(sessionID, turnID)] = turn
	if session := m.sessions[sessionID]; session != nil {
		session.Status = "running"
	}
	m.mu.Unlock()
	m.recordEvent("turn/started", sessionID, turnID, "Turn started", map[string]any{"input": truncate(input, 500)})
	c, err := m.ensureClient(ctx)
	if err != nil {
		m.failTurn(sessionID, turnID, err)
		return Turn{}, nil, err
	}
	return *turn, c, nil
}

func (m *Manager) runTurn(ctx context.Context, c *client, sessionID, turnID, input, model string) (Turn, error) {
	params := map[string]any{
		"sessionId": sessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": input}},
	}
	if model = strings.TrimSpace(model); model != "" {
		params["_meta"] = map[string]any{"model": model}
	}
	raw, err := c.call(ctx, "session/prompt", params)
	if err != nil {
		m.failTurn(sessionID, turnID, err)
		return Turn{}, err
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		m.failTurn(sessionID, turnID, err)
		return Turn{}, fmt.Errorf("decode session/prompt response: %w", err)
	}
	stopReason := firstString(response, "stopReason", "stop_reason")
	summary := firstString(response, "text", "result", "message")
	m.mu.Lock()
	stored := m.turns[turnKey(sessionID, turnID)]
	if stored == nil {
		stored = &Turn{ID: turnID, SessionID: sessionID, StartedAt: time.Now().UTC()}
		m.turns[turnKey(sessionID, turnID)] = stored
	}
	stored.Status = "completed"
	stored.StopReason = stopReason
	stored.CompletedAt = time.Now().UTC()
	if summary != "" {
		stored.Summary = truncate(summary, 500)
		if stored.Output == "" {
			stored.Output = trimOutput(summary)
		}
	}
	if session := m.sessions[sessionID]; session != nil && session.Status != "failed" {
		session.Status = "idle"
	}
	completed := *stored
	m.mu.Unlock()
	m.recordEvent("turn/completed", sessionID, turnID, "Turn completed", map[string]any{"stopReason": stopReason})
	return completed, nil
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
	outcome, err := approvalOutcome(decision, approval.Options)
	if err != nil {
		return Approval{}, err
	}
	if c == nil {
		return Approval{}, errors.New("grok agent is not running")
	}
	if err := c.respond(approval.rpcID, outcome); err != nil {
		return Approval{}, err
	}
	m.mu.Lock()
	delete(m.approvals, approvalID)
	if turn := m.turns[turnKey(approval.SessionID, approval.TurnID)]; turn != nil && turn.Status == "waiting_approval" {
		turn.Status = "inProgress"
	}
	m.mu.Unlock()
	m.recordEvent("session/request_permission/resolved", approval.SessionID, approval.TurnID, "Approval resolved", map[string]any{"approvalId": approvalID, "decision": decision})
	return *approval, nil
}

func (m *Manager) Summary(sessionID, turnID string) (map[string]any, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := map[string]any{"session_id": sessionID, "turn_id": turnID}
	if sessionID != "" {
		if session := m.sessions[sessionID]; session != nil {
			result["session"] = *session
		}
	}
	if turnID != "" {
		if turn := m.turns[turnKey(sessionID, turnID)]; turn != nil {
			result["turn"] = *turn
		}
	}
	var pending []Approval
	for _, approval := range m.approvals {
		if sessionID != "" && approval.SessionID != sessionID {
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

func (m *Manager) Events(sessionID, turnID string, cursor int64, limit int) (map[string]any, error) {
	if limit <= 0 || limit > m.cfg.MaxEvents {
		limit = m.cfg.MaxEvents
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	events := make([]Event, 0, limit)
	for _, event := range m.events {
		if event.Cursor <= cursor || (sessionID != "" && event.SessionID != sessionID) || (turnID != "" && event.TurnID != turnID) {
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

func (m *Manager) failTurn(sessionID, turnID string, cause error) {
	m.mu.Lock()
	if turn := m.turns[turnKey(sessionID, turnID)]; turn != nil && turn.Status != "completed" {
		turn.Status = "failed"
		turn.CompletedAt = time.Now().UTC()
		turn.Summary = truncate(cause.Error(), 500)
	}
	if session := m.sessions[sessionID]; session != nil {
		session.Status = "idle"
	}
	m.mu.Unlock()
	m.recordEvent("turn/failed", sessionID, turnID, "Turn failed", map[string]any{"error": cause.Error()})
}

func (m *Manager) handleRequest(request RPCRequest) {
	sessionID, turnID := sessionAndTurn(request.Params)
	if sessionID == "" {
		sessionID = m.activeSessionID()
	}
	if turnID == "" {
		turnID = m.activeTurnID(sessionID)
	}
	approvalID := strings.Trim(string(request.ID), "\"")
	approval := &Approval{
		ID:        approvalID,
		Method:    request.Method,
		SessionID: sessionID,
		TurnID:    turnID,
		Reason:    stringValue(request.Params, "reason"),
		Summary:   approvalSummary(request),
		Params:    request.Params,
		Options:   permissionOptions(request.Params),
		CreatedAt: time.Now().UTC(),
		rpcID:     append(json.RawMessage(nil), request.ID...),
	}
	m.mu.Lock()
	m.approvals[approvalID] = approval
	if turn := m.turns[turnKey(sessionID, turnID)]; turn != nil && turn.Status == "inProgress" {
		turn.Status = "waiting_approval"
	}
	m.mu.Unlock()
	m.recordEvent(request.Method, sessionID, turnID, "Approval requested: "+approval.Summary, map[string]any{"approvalId": approvalID, "reason": approval.Reason, "method": request.Method})

	mode := m.ApprovalMode()
	if mode != "auto" && mode != "deny" {
		return
	}
	decision := "allow"
	if mode == "deny" {
		decision = "deny"
	}
	outcome, err := approvalOutcome(decision, approval.Options)
	if err != nil {
		return
	}
	m.mu.RLock()
	c := m.client
	m.mu.RUnlock()
	if c != nil {
		if err := c.respond(request.ID, outcome); err != nil {
			m.recordEvent("grok/protocol/error", sessionID, turnID, "Approval response failed", map[string]any{"error": err.Error()})
			return
		}
	}
	m.mu.Lock()
	delete(m.approvals, approvalID)
	if turn := m.turns[turnKey(sessionID, turnID)]; turn != nil && turn.Status == "waiting_approval" {
		turn.Status = "inProgress"
	}
	m.mu.Unlock()
	m.recordEvent("session/request_permission/resolved", sessionID, turnID, "Approval resolved", map[string]any{"approvalId": approvalID, "decision": decision})
}

func (m *Manager) handleNotification(method string, params map[string]any) {
	sessionID, turnID := sessionAndTurn(params)
	if sessionID == "" {
		sessionID = m.activeSessionID()
	}
	if turnID == "" {
		turnID = m.activeTurnID(sessionID)
	}
	update := nestedMap(params, "update")
	if update == nil {
		update = params
	}
	kind := firstString(update, "sessionUpdate", "session_update")
	summary := notificationSummary(method, kind, update)
	if text := contentText(update); text != "" && (kind == "agent_message_chunk" || kind == "agent_message") {
		m.mu.Lock()
		if turn := m.turns[turnKey(sessionID, turnID)]; turn != nil {
			turn.Output = trimOutput(turn.Output + text)
		}
		m.mu.Unlock()
	}
	m.recordEvent(eventMethod(method, kind), sessionID, turnID, summary, compactData(update))
}

func (m *Manager) recordEvent(method, sessionID, turnID, summary string, data map[string]any) {
	if summary == "" {
		summary = method
	}
	m.mu.Lock()
	m.seq++
	event := Event{Cursor: m.seq, At: time.Now().UTC(), Method: method, SessionID: sessionID, TurnID: turnID, Summary: summary, Data: data}
	m.events = append(m.events, event)
	if len(m.events) > m.cfg.MaxEvents {
		m.events = append([]Event(nil), m.events[len(m.events)-m.cfg.MaxEvents:]...)
	}
	if turn := m.turns[turnKey(sessionID, turnID)]; turn != nil {
		turn.EventCursor = m.seq
	}
	m.mu.Unlock()
}

func (m *Manager) activeSessionID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var id string
	var latest time.Time
	for _, session := range m.sessions {
		if session.Status == "running" {
			return session.ID
		}
		if id == "" || session.CreatedAt.After(latest) {
			id = session.ID
			latest = session.CreatedAt
		}
	}
	return id
}

func (m *Manager) activeTurnID(sessionID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var id string
	var latest time.Time
	for _, turn := range m.turns {
		if sessionID != "" && turn.SessionID != sessionID {
			continue
		}
		if turn.Status == "inProgress" || turn.Status == "waiting_approval" {
			return turn.ID
		}
		if id == "" || turn.StartedAt.After(latest) {
			id = turn.ID
			latest = turn.StartedAt
		}
	}
	return id
}

func (m *Manager) validateCWD(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", errors.New("cwd is required")
	}
	clean, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	clean = filepath.Clean(clean)
	if len(m.cfg.CWDAllowlist) == 0 {
		return clean, nil
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("resolve grok cwd %q: %w", clean, err)
	}
	resolved = filepath.Clean(resolved)
	for _, root := range m.cfg.CWDAllowlist {
		allowed, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		allowed, err = filepath.EvalSymlinks(filepath.Clean(allowed))
		if err != nil {
			continue
		}
		allowed = filepath.Clean(allowed)
		if grokPathWithin(allowed, resolved) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("cwd %q is outside grok.cwd_allowlist", clean)
}

func grokPathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func approvalOutcome(decision string, options []PermissionOption) (map[string]any, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision == "cancel" {
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	if decision == "deny" || decision == "decline" || decision == "reject" {
		if optionID := findPermissionOption(options, "reject_once", "reject_always"); optionID != "" {
			return selectedPermissionOutcome(optionID), nil
		}
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	}
	if optionID := findPermissionOptionByID(options, decision); optionID != "" {
		return selectedPermissionOutcome(optionID), nil
	}
	var kinds []string
	switch decision {
	case "allow", "accept", "allow_once", "approve":
		kinds = []string{"allow_once", "allow_always"}
	case "allow_always", "allow-always", "acceptforsession", "accept_for_session":
		kinds = []string{"allow_always", "allow_once"}
	default:
		return nil, fmt.Errorf("unsupported approval decision %q", decision)
	}
	if optionID := findPermissionOption(options, kinds...); optionID != "" {
		return selectedPermissionOutcome(optionID), nil
	}
	// Keep compatibility with older fake agents that omitted options. Real ACP
	// agents provide options, and their exact ID is selected above.
	if len(options) == 0 {
		return selectedPermissionOutcome("allow_once"), nil
	}
	return nil, fmt.Errorf("approval has no option for decision %q", decision)
}

func selectedPermissionOutcome(optionID string) map[string]any {
	return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}}
}

func findPermissionOption(options []PermissionOption, kinds ...string) string {
	for _, kind := range kinds {
		for _, option := range options {
			if strings.EqualFold(strings.TrimSpace(option.Kind), kind) && strings.TrimSpace(option.OptionID) != "" {
				return option.OptionID
			}
		}
	}
	return ""
}

func findPermissionOptionByID(options []PermissionOption, optionID string) string {
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option.OptionID), optionID) && strings.TrimSpace(option.OptionID) != "" {
			return option.OptionID
		}
	}
	return ""
}

func permissionOptions(params map[string]any) []PermissionOption {
	values, ok := params["options"].([]any)
	if !ok {
		return nil
	}
	options := make([]PermissionOption, 0, len(values))
	for _, value := range values {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		option := PermissionOption{
			OptionID: firstString(item, "optionId", "option_id"),
			Name:     firstString(item, "name"),
			Kind:     firstString(item, "kind"),
		}
		if option.OptionID != "" {
			options = append(options, option)
		}
	}
	return options
}

func turnKey(sessionID, turnID string) string { return sessionID + "\x00" + turnID }

func sessionAndTurn(values map[string]any) (string, string) {
	sessionID := firstString(values, "sessionId", "session_id")
	turnID := firstString(values, "turnId", "turn_id")
	if update := nestedMap(values, "update"); update != nil {
		if sessionID == "" {
			sessionID = firstString(update, "sessionId", "session_id")
		}
		if turnID == "" {
			turnID = firstString(update, "turnId", "turn_id")
		}
	}
	return sessionID, turnID
}

func nestedMap(values map[string]any, key string) map[string]any {
	if values == nil {
		return nil
	}
	raw, ok := values[key].(map[string]any)
	if !ok {
		return nil
	}
	return raw
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values, key); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func contentText(update map[string]any) string {
	if text := stringValue(update, "text"); text != "" {
		return text
	}
	content := nestedMap(update, "content")
	if content == nil {
		if raw, ok := update["content"].(string); ok {
			return raw
		}
		return ""
	}
	if text := stringValue(content, "text"); text != "" {
		return text
	}
	return ""
}

func approvalSummary(request RPCRequest) string {
	if title := stringValue(request.Params, "title"); title != "" {
		return truncate(title, 500)
	}
	if tool := nestedMap(request.Params, "toolCall"); tool != nil {
		if title := firstString(tool, "title", "toolName", "name"); title != "" {
			return truncate(title, 500)
		}
	}
	if command := stringValue(request.Params, "command"); command != "" {
		return truncate(command, 500)
	}
	if reason := stringValue(request.Params, "reason"); reason != "" {
		return truncate(reason, 500)
	}
	return request.Method
}

func notificationSummary(method, kind string, update map[string]any) string {
	if text := contentText(update); text != "" {
		return truncate(text, 500)
	}
	for _, key := range []string{"title", "message", "summary", "status", "reason"} {
		if value := stringValue(update, key); value != "" {
			return truncate(value, 500)
		}
	}
	if kind != "" {
		return kind
	}
	return method
}

func eventMethod(method, kind string) string {
	if kind == "" {
		return method
	}
	if method == "" || method == "session/update" {
		return "session/update:" + kind
	}
	return method + ":" + kind
}

func compactData(values map[string]any) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any)
	for key, value := range values {
		switch key {
		case "sessionId", "session_id", "turnId", "turn_id", "toolCallId", "status", "reason", "title", "sessionUpdate", "kind", "cwd":
			if text, ok := value.(string); ok {
				result[key] = truncate(text, 1000)
			} else {
				result[key] = value
			}
		case "text", "message", "summary":
			if text, ok := value.(string); ok {
				result[key] = truncate(text, 1000)
			}
		}
	}
	if text := contentText(values); text != "" {
		result["text"] = truncate(text, 1000)
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
