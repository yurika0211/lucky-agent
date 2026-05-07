package qqofficial

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

const (
	dispatchEventOp = 0
	heartbeatOp     = 1
	identifyOp      = 2
	resumeOp        = 6
	reconnectOp     = 7
	invalidSessOp   = 9
	helloOp         = 10
	heartbeatAckOp  = 11
)

type accessTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type gatewayFrame struct {
	Op int             `json:"op"`
	S  *int64          `json:"s,omitempty"`
	T  string          `json:"t,omitempty"`
	D  json.RawMessage `json:"d,omitempty"`
}

type helloPayload struct {
	HeartbeatInterval int `json:"heartbeat_interval"`
}

type identifyPayload struct {
	Token      string         `json:"token"`
	Intents    int            `json:"intents"`
	Shard      [2]int         `json:"shard"`
	Properties map[string]any `json:"properties"`
}

type messageAuthor struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
	Bot      bool   `json:"bot"`
}

type incomingMessageEvent struct {
	ID          string        `json:"id"`
	Content     string        `json:"content"`
	MsgID       string        `json:"msg_id"`
	GroupOpenID string        `json:"group_openid"`
	GuildID     string        `json:"guild_id"`
	ChannelID   string        `json:"channel_id"`
	Author      messageAuthor `json:"author"`
}

type outgoingMessagePayload struct {
	Content string `json:"content,omitempty"`
	MsgType int    `json:"msg_type"`
	MsgID   string `json:"msg_id,omitempty"`
}

// Adapter implements gateway.Gateway for QQ official bot.
type Adapter struct {
	cfg Config

	mu         sync.RWMutex
	handler    gateway.MessageHandler
	running    bool
	cancel     context.CancelFunc
	conn       *websocket.Conn
	httpClient *http.Client

	accessToken string
	tokenExpiry time.Time
	seq         int64
	sessionID   string
}

func NewAdapter(cfg Config) *Adapter {
	def := DefaultConfig()
	if cfg.HeartbeatSec <= 0 {
		cfg.HeartbeatSec = def.HeartbeatSec
	}
	if cfg.ReconnectWait <= 0 {
		cfg.ReconnectWait = def.ReconnectWait
	}
	if len(cfg.Intents) == 0 {
		cfg.Intents = append([]string(nil), def.Intents...)
	}
	cfg.RemoveAt = cfg.RemoveAt || def.RemoveAt

	return &Adapter{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (a *Adapter) Name() string { return "qqofficial" }

func (a *Adapter) SetHandler(handler gateway.MessageHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = handler
}

func (a *Adapter) Start(ctx context.Context) error {
	if strings.TrimSpace(a.cfg.AppID) == "" || strings.TrimSpace(a.cfg.AppSecret) == "" {
		return fmt.Errorf("qqofficial: app_id and app_secret are required")
	}
	startCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel

	if _, err := a.ensureAccessToken(startCtx); err != nil {
		cancel()
		return err
	}
	if err := a.connectGateway(startCtx); err != nil {
		cancel()
		return err
	}
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()

	go a.readLoop(startCtx)
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
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("qqofficial: invalid chat id %q", chatID)
	}
	switch parts[0] {
	case "c2c":
		return a.sendC2CMessage(ctx, parts[1], "", message)
	case "group":
		return a.sendGroupMessage(ctx, parts[1], "", message)
	default:
		return fmt.Errorf("qqofficial: unsupported chat type %q", parts[0])
	}
}

func (a *Adapter) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("qqofficial: invalid chat id %q", chatID)
	}
	switch parts[0] {
	case "c2c":
		return a.sendC2CMessage(ctx, parts[1], replyToMsgID, message)
	case "group":
		return a.sendGroupMessage(ctx, parts[1], replyToMsgID, message)
	default:
		return fmt.Errorf("qqofficial: unsupported chat type %q", parts[0])
	}
}

func (a *Adapter) ensureAccessToken(ctx context.Context) (string, error) {
	a.mu.RLock()
	if strings.TrimSpace(a.accessToken) != "" && time.Now().Before(a.tokenExpiry.Add(-1*time.Minute)) {
		token := a.accessToken
		a.mu.RUnlock()
		return token, nil
	}
	a.mu.RUnlock()

	body, _ := json.Marshal(map[string]string{
		"appId":        a.cfg.AppID,
		"clientSecret": a.cfg.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://bots.qq.com/app/getAppAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("qqofficial: create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("qqofficial: request access token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qqofficial: token endpoint status %d", resp.StatusCode)
	}
	var tokenResp accessTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("qqofficial: decode token response: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return "", fmt.Errorf("qqofficial: empty access token")
	}
	expiry := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	a.mu.Lock()
	a.accessToken = tokenResp.AccessToken
	a.tokenExpiry = expiry
	a.mu.Unlock()
	return tokenResp.AccessToken, nil
}

func (a *Adapter) connectGateway(ctx context.Context) error {
	token, err := a.ensureAccessToken(ctx)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, a.cfg.normalizedGatewayURL(), http.Header{
		"Authorization": []string{"QQBot " + token},
		"X-Union-Appid": []string{a.cfg.AppID},
	})
	if err != nil {
		return fmt.Errorf("qqofficial: connect websocket: %w", err)
	}

	var hello gatewayFrame
	if err := conn.ReadJSON(&hello); err != nil {
		_ = conn.Close()
		return fmt.Errorf("qqofficial: read hello: %w", err)
	}
	if hello.Op != helloOp {
		_ = conn.Close()
		return fmt.Errorf("qqofficial: unexpected hello op %d", hello.Op)
	}

	var payload helloPayload
	if err := json.Unmarshal(hello.D, &payload); err != nil {
		_ = conn.Close()
		return fmt.Errorf("qqofficial: decode hello: %w", err)
	}

	intents := buildIntentBits(a.cfg.Intents)
	identify := gatewayFrame{
		Op: identifyOp,
		D: mustJSON(identifyPayload{
			Token:   "QQBot " + token,
			Intents: intents,
			Shard:   [2]int{0, 1},
			Properties: map[string]any{
				"$os":      "linux",
				"$browser": "luckyharness",
				"$device":  "luckyharness",
			},
		}),
	}
	if err := conn.WriteJSON(identify); err != nil {
		_ = conn.Close()
		return fmt.Errorf("qqofficial: identify: %w", err)
	}

	a.mu.Lock()
	a.conn = conn
	a.mu.Unlock()
	go a.heartbeatLoop(ctx, conn, time.Duration(payload.HeartbeatInterval)*time.Millisecond)
	return nil
}

func (a *Adapter) heartbeatLoop(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	if interval <= 0 {
		interval = time.Duration(a.cfg.HeartbeatSec) * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.mu.RLock()
			seq := a.seq
			a.mu.RUnlock()
			_ = conn.WriteJSON(gatewayFrame{Op: heartbeatOp, D: mustJSON(seq)})
		}
	}
}

func (a *Adapter) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.mu.RLock()
		conn := a.conn
		handler := a.handler
		a.mu.RUnlock()
		if conn == nil {
			return
		}
		var frame gatewayFrame
		if err := conn.ReadJSON(&frame); err != nil {
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			return
		}
		if frame.S != nil {
			a.mu.Lock()
			a.seq = *frame.S
			a.mu.Unlock()
		}
		switch frame.Op {
		case dispatchEventOp:
			msg := a.convertDispatch(frame.T, frame.D)
			if msg != nil && handler != nil {
				_ = handler(ctx, msg)
			}
		case reconnectOp, invalidSessOp:
			a.mu.Lock()
			a.running = false
			a.mu.Unlock()
			return
		case heartbeatAckOp:
		}
	}
}

func (a *Adapter) convertDispatch(eventType string, raw json.RawMessage) *gateway.Message {
	switch eventType {
	case "C2C_MESSAGE_CREATE", "GROUP_AT_MESSAGE_CREATE":
	default:
		return nil
	}

	var evt incomingMessageEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return nil
	}
	text := strings.TrimSpace(evt.Content)
	if a.cfg.RemoveAt {
		text = stripLeadingQQMention(text)
	}
	chat := gateway.Chat{
		Title: evt.GuildID,
	}
	msg := &gateway.Message{
		ID:        evt.ID,
		Text:      text,
		Timestamp: time.Now(),
		Sender: gateway.User{
			ID:       evt.Author.ID,
			Username: evt.Author.Username,
		},
	}
	if !a.cfg.IsUserAllowed(msg.Sender.ID) {
		return nil
	}

	switch eventType {
	case "C2C_MESSAGE_CREATE":
		chat.ID = "c2c:" + evt.Author.ID
		chat.Type = gateway.ChatPrivate
	case "GROUP_AT_MESSAGE_CREATE":
		if !a.cfg.IsChatAllowed(evt.GroupOpenID) {
			return nil
		}
		chat.ID = "group:" + evt.GroupOpenID
		chat.Type = gateway.ChatGroup
		msg.IsGroupTrigger = true
		msg.TriggerType = "mention"
	}
	msg.Chat = chat
	msg.IsCommand, msg.Command, msg.Args = parseCommand(text)
	return msg
}

func (a *Adapter) sendC2CMessage(ctx context.Context, openID, replyMsgID, message string) error {
	return a.sendMessage(ctx, a.cfg.normalizedAPIBaseURL()+"/v2/users/"+openID+"/messages", replyMsgID, message)
}

func (a *Adapter) sendGroupMessage(ctx context.Context, groupOpenID, replyMsgID, message string) error {
	return a.sendMessage(ctx, a.cfg.normalizedAPIBaseURL()+"/v2/groups/"+groupOpenID+"/messages", replyMsgID, message)
}

func (a *Adapter) sendMessage(ctx context.Context, endpoint, replyMsgID, message string) error {
	token, err := a.ensureAccessToken(ctx)
	if err != nil {
		return err
	}
	payload := outgoingMessagePayload{
		Content: strings.TrimSpace(message),
		MsgType: 0,
		MsgID:   strings.TrimSpace(replyMsgID),
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("qqofficial: create send request: %w", err)
	}
	req.Header.Set("Authorization", "QQBot "+token)
	req.Header.Set("X-Union-Appid", a.cfg.AppID)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qqofficial: send message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		a.mu.Lock()
		a.accessToken = ""
		a.tokenExpiry = time.Time{}
		a.mu.Unlock()
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qqofficial: send message status %d", resp.StatusCode)
	}
	return nil
}

func buildIntentBits(names []string) int {
	var bits int
	for _, name := range names {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "public_guild_messages":
			bits |= 1 << 9
		case "guild_messages":
			bits |= 1 << 9
		case "group_and_c2c_messages":
			bits |= 1 << 25
			bits |= 1 << 26
		case "group_messages":
			bits |= 1 << 25
		case "c2c_messages":
			bits |= 1 << 26
		}
	}
	return bits
}

func parseCommand(text string) (bool, string, string) {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return false, "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return true, cmd, args
}

func stripLeadingQQMention(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "<@!")
	if idx := strings.Index(text, ">"); idx >= 0 {
		text = strings.TrimSpace(text[idx+1:])
	}
	return strings.TrimSpace(text)
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func debugID(n int64) string {
	return strconv.FormatInt(n, 10)
}
