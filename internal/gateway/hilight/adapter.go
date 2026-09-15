package hilight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

// Envelope is the unified HiLight WebSocket message shape.
type Envelope struct {
	Context string          `json:"context"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

type msgPayload struct {
	UserID   string `json:"userId"`
	UserName string `json:"userName"`
	Text     string `json:"text"`
}

type replyPayload struct {
	UserID string `json:"userId"`
	Text   string `json:"text"`
	Done   bool   `json:"done"`
}

type typingPayload struct {
	UserID string `json:"userId"`
}

type errorPayload struct {
	UserID  string `json:"userId"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type connectedPayload struct {
	PluginID  string `json:"pluginId"`
	AccountID string `json:"accountId"`
}

type pingPayload struct {
	TS int64 `json:"ts"`
}

// Adapter implements gateway.Gateway for the HiLight WebSocket bridge.
// Phase 1: text DM only. Inbound action=msg, outbound action=reply/typing/error.
type Adapter struct {
	cfg Config

	mu      sync.RWMutex
	writeMu sync.Mutex
	handler gateway.MessageHandler
	running bool
	cancel  context.CancelFunc
	conn    *websocket.Conn

	// chatContexts remembers the latest HiLight context string per chat/user.
	chatContexts map[string]string
}

func NewAdapter(cfg Config) *Adapter {
	def := DefaultConfig()
	if strings.TrimSpace(cfg.WSURL) == "" {
		cfg.WSURL = def.WSURL
	}
	if strings.TrimSpace(cfg.AccountID) == "" {
		cfg.AccountID = def.AccountID
	}
	if cfg.ReconnectIntervalMS <= 0 {
		cfg.ReconnectIntervalMS = def.ReconnectIntervalMS
	}
	if cfg.MaxReconnectIntervalMS <= 0 {
		cfg.MaxReconnectIntervalMS = def.MaxReconnectIntervalMS
	}
	if cfg.HeartbeatIntervalMS <= 0 {
		cfg.HeartbeatIntervalMS = def.HeartbeatIntervalMS
	}
	if strings.TrimSpace(cfg.DMPolicy) == "" {
		cfg.DMPolicy = def.DMPolicy
	}
	if cfg.AllowFrom == nil {
		cfg.AllowFrom = append([]string(nil), def.AllowFrom...)
	}
	return &Adapter{
		cfg:          cfg,
		chatContexts: make(map[string]string),
	}
}

func (a *Adapter) Name() string { return "hilight" }

func (a *Adapter) SetHandler(handler gateway.MessageHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = handler
}

func (a *Adapter) Start(ctx context.Context) error {
	if strings.TrimSpace(a.cfg.AuthToken) == "" {
		return fmt.Errorf("hilight: auth_token is required")
	}
	if strings.TrimSpace(a.cfg.WSURL) == "" {
		return fmt.Errorf("hilight: ws_url is required")
	}

	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.running = true
	a.mu.Unlock()

	go a.run(runCtx)
	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
	if a.conn != nil {
		_ = a.conn.Close()
		a.conn = nil
	}
	a.running = false
	return nil
}

func (a *Adapter) IsRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

func (a *Adapter) Send(ctx context.Context, chatID string, message string) error {
	return a.SendWithReply(ctx, chatID, "", message)
}

func (a *Adapter) SendWithReply(ctx context.Context, chatID string, _ string, message string) error {
	chatID = strings.TrimSpace(chatID)
	message = strings.TrimSpace(message)
	if chatID == "" {
		return fmt.Errorf("hilight: chatID is required")
	}
	if message == "" {
		return nil
	}

	a.mu.RLock()
	contextID := a.chatContexts[chatID]
	a.mu.RUnlock()
	if contextID == "" {
		contextID = "default"
	}

	payload, err := json.Marshal(replyPayload{
		UserID: chatID,
		Text:   message,
		Done:   true,
	})
	if err != nil {
		return err
	}
	return a.writeEnvelope(ctx, Envelope{
		Context: contextID,
		Action:  "reply",
		Payload: payload,
	})
}

func (a *Adapter) sendTyping(ctx context.Context, chatID, contextID string) {
	payload, err := json.Marshal(typingPayload{UserID: chatID})
	if err != nil {
		return
	}
	_ = a.writeEnvelope(ctx, Envelope{
		Context: contextID,
		Action:  "typing",
		Payload: payload,
	})
}

func (a *Adapter) sendError(ctx context.Context, chatID, contextID, code, message string) {
	payload, err := json.Marshal(errorPayload{
		UserID:  chatID,
		Code:    code,
		Message: message,
	})
	if err != nil {
		return
	}
	_ = a.writeEnvelope(ctx, Envelope{
		Context: contextID,
		Action:  "error",
		Payload: payload,
	})
}

func (a *Adapter) run(ctx context.Context) {
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	attempts := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := a.connectOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if isAuthFailure(err) {
				fmt.Printf("[hilight] auth failed: %v; stop reconnecting\n", err)
				return
			}
			fmt.Printf("[hilight] connection ended: %v\n", err)
		}
		attempts++
		delay := time.Duration(minInt(a.cfg.reconnectBase()<<minInt(attempts-1, 5), a.cfg.reconnectMax())) * time.Millisecond
		fmt.Printf("[hilight] reconnecting in %s (attempt %d)\n", delay, attempts)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (a *Adapter) connectOnce(ctx context.Context) error {
	connectURL, err := resolveConnectWSURL(a.cfg.normalizedWSURL())
	if err != nil {
		return err
	}

	headers := http.Header{}
	token := strings.TrimSpace(a.cfg.AuthToken)
	if token != "" {
		// HiLight plugin sends the raw token as Authorization value.
		headers.Set("Authorization", token)
	}

	fmt.Printf("[hilight] connecting to %s\n", connectURL)
	dialer := websocket.DefaultDialer
	conn, resp, err := dialer.DialContext(ctx, connectURL, headers)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("hilight: unauthorized (401)")
		}
		return err
	}
	defer conn.Close()

	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.conn == conn {
			a.conn = nil
		}
		a.mu.Unlock()
	}()

	if err := a.sendConnected(ctx); err != nil {
		return err
	}

	missedPongs := 0
	const maxMissedPongs = 2
	heartbeat := time.NewTicker(time.Duration(a.cfg.heartbeatInterval()) * time.Millisecond)
	defer heartbeat.Stop()

	errCh := make(chan error, 1)
	go func() {
		for {
			_, data, readErr := conn.ReadMessage()
			if readErr != nil {
				errCh <- readErr
				return
			}
			a.handleRawMessage(ctx, conn, string(data), &missedPongs)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"), time.Now().Add(2*time.Second))
			return ctx.Err()
		case err := <-errCh:
			return err
		case <-heartbeat.C:
			missedPongs++
			if missedPongs > maxMissedPongs {
				return fmt.Errorf("hilight: missed %d pongs", missedPongs)
			}
			payload, _ := json.Marshal(pingPayload{TS: time.Now().UnixMilli()})
			if err := a.writeEnvelope(ctx, Envelope{Action: "ping", Payload: payload}); err != nil {
				return err
			}
		}
	}
}

func (a *Adapter) sendConnected(ctx context.Context) error {
	payload, err := json.Marshal(connectedPayload{
		PluginID:  "luckyagent-hilight",
		AccountID: a.cfg.normalizedAccountID(),
	})
	if err != nil {
		return err
	}
	return a.writeEnvelope(ctx, Envelope{
		Action:  "connected",
		Payload: payload,
	})
}

func (a *Adapter) handleRawMessage(ctx context.Context, _ *websocket.Conn, raw string, missedPongs *int) {
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		fmt.Printf("[hilight] failed to parse message: %s\n", truncate(raw, 200))
		return
	}

	switch strings.TrimSpace(env.Action) {
	case "pong":
		if missedPongs != nil {
			*missedPongs = 0
		}
		return
	case "msg":
		a.handleInboundMsg(ctx, env)
	default:
		// Ignore unknown actions in Phase 1.
	}
}

func (a *Adapter) handleInboundMsg(ctx context.Context, env Envelope) {
	var payload msgPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		fmt.Printf("[hilight] invalid msg payload: %v\n", err)
		return
	}
	userID := strings.TrimSpace(payload.UserID)
	text := strings.TrimSpace(payload.Text)
	if userID == "" || text == "" {
		fmt.Printf("[hilight] msg payload missing userId or text\n")
		return
	}
	if !a.cfg.isDMAllowed(userID) {
		fmt.Printf("[hilight] drop msg from user=%s (dm policy)\n", userID)
		return
	}

	contextID := strings.TrimSpace(env.Context)
	if contextID == "" {
		contextID = "default"
	}
	a.mu.Lock()
	a.chatContexts[userID] = contextID
	a.mu.Unlock()

	senderName := strings.TrimSpace(payload.UserName)
	if senderName == "" {
		senderName = userID
	}

	msg := &gateway.Message{
		ID: fmt.Sprintf("hilight-%s-%d", userID, time.Now().UnixNano()),
		Chat: gateway.Chat{
			ID:   userID,
			Type: gateway.ChatPrivate,
		},
		ThreadID: contextID,
		Sender: gateway.User{
			ID:        userID,
			FirstName: senderName,
		},
		Text:      text,
		Timestamp: time.Now(),
	}
	msg.IsCommand, msg.Command, msg.Args = parseCommand(text)

	a.sendTyping(ctx, userID, contextID)

	a.mu.RLock()
	handler := a.handler
	a.mu.RUnlock()
	if handler == nil {
		return
	}
	if err := handler(ctx, msg); err != nil {
		fmt.Printf("[hilight] handler failed: %v\n", err)
		a.sendError(ctx, userID, contextID, "DISPATCH_FAILED", err.Error())
	}
}

func (a *Adapter) writeEnvelope(ctx context.Context, env Envelope) error {
	a.mu.RLock()
	conn := a.conn
	a.mu.RUnlock()
	if conn == nil {
		return fmt.Errorf("hilight: websocket not connected")
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}

	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	deadline := time.Now().Add(10 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func resolveConnectWSURL(wsURL string) (string, error) {
	id := uuid.NewString()
	if strings.Contains(wsURL, wsUUIDPlaceholder) {
		return strings.ReplaceAll(wsURL, wsUUIDPlaceholder, id), nil
	}
	parsed, err := url.Parse(wsURL)
	if err != nil {
		trimmed := strings.TrimRight(wsURL, "/")
		return trimmed + "/" + id, nil
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + id
	return parsed.String(), nil
}

func parseCommand(text string) (bool, string, string) {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return false, "", ""
	}
	text = strings.TrimPrefix(text, "/")
	parts := strings.SplitN(text, " ", 2)
	cmd := strings.ToLower(strings.TrimSpace(parts[0]))
	args := ""
	if len(parts) == 2 {
		args = strings.TrimSpace(parts[1])
	}
	return cmd != "", cmd, args
}

func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") || strings.Contains(msg, "unauthorized")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
