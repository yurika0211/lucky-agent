package napcat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yurika0211/luckyharness/internal/gateway"
)

const (
	defaultNapCatAttachmentDownloadLimit = 1 << 30
	defaultNapCatAckEmojiID              = "76" // QQ /赞
)

type oneBotEvent struct {
	Time        int64           `json:"time"`
	SelfID      json.RawMessage `json:"self_id"`
	PostType    string          `json:"post_type"`
	MessageType string          `json:"message_type"`
	SubType     string          `json:"sub_type"`
	MessageID   json.RawMessage `json:"message_id"`
	UserID      json.RawMessage `json:"user_id"`
	GroupID     json.RawMessage `json:"group_id"`
	Message     json.RawMessage `json:"message"`
	RawMessage  string          `json:"raw_message"`
	Sender      oneBotSender    `json:"sender"`
}

type oneBotSender struct {
	UserID   json.RawMessage `json:"user_id"`
	Nickname string          `json:"nickname"`
	Card     string          `json:"card"`
	Role     string          `json:"role"`
}

type messageSegment struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type parsedMessage struct {
	Text        string
	AtQQs       []string
	ReplyID     string
	Attachments []gateway.Attachment
}

type actionRequest struct {
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
	Echo   string         `json:"echo,omitempty"`
}

type actionResponse struct {
	Status  string          `json:"status"`
	RetCode int             `json:"retcode"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
	Wording string          `json:"wording"`
	Echo    string          `json:"echo"`
}

// Adapter implements gateway.Gateway for NapCat's OneBot v11 reverse WebSocket.
type Adapter struct {
	cfg Config

	mu              sync.RWMutex
	writeMu         sync.Mutex
	handler         gateway.MessageHandler
	running         bool
	connected       bool
	cancel          context.CancelFunc
	server          *http.Server
	listener        net.Listener
	conn            *websocket.Conn
	selfID          string
	echoSeq         atomic.Uint64
	actionResponses map[string]chan actionResponse
}

func NewAdapter(cfg Config) *Adapter {
	def := DefaultConfig()
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = def.ListenAddr
	}
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = def.Path
	}
	if strings.TrimSpace(cfg.GroupTriggerMode) == "" {
		cfg.GroupTriggerMode = def.GroupTriggerMode
	}
	cfg.RemoveAt = cfg.RemoveAt || def.RemoveAt
	return &Adapter{cfg: cfg, actionResponses: make(map[string]chan actionResponse)}
}

func (a *Adapter) Name() string { return "napcat" }

func (a *Adapter) SetHandler(handler gateway.MessageHandler) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.handler = handler
}

func (a *Adapter) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	listenAddr := a.cfg.normalizedListenAddr()
	path := a.cfg.normalizedPath()
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("napcat: listen %s: %w", listenAddr, err)
	}

	startCtx, cancel := context.WithCancel(ctx)
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		a.handleWebSocket(startCtx, w, r)
	})
	server := &http.Server{Handler: mux}

	a.cancel = cancel
	a.server = server
	a.listener = ln
	a.running = true
	a.mu.Unlock()

	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Printf("[napcat] websocket server stopped with error: %v\n", err)
			a.mu.Lock()
			a.running = false
			a.connected = false
			a.mu.Unlock()
		}
	}()

	return nil
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	cancel := a.cancel
	server := a.server
	conn := a.conn
	a.cancel = nil
	a.server = nil
	a.listener = nil
	a.conn = nil
	a.running = false
	a.connected = false
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	if server != nil {
		ctx, cancelShutdown := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("napcat: shutdown websocket server: %w", err)
		}
	}
	return nil
}

func (a *Adapter) IsRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.running
}

func (a *Adapter) IsConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

func (a *Adapter) ListenAddr() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.listener != nil {
		return a.listener.Addr().String()
	}
	return a.cfg.normalizedListenAddr()
}

func (a *Adapter) Send(ctx context.Context, chatID string, message string) error {
	return a.sendTextMessage(ctx, chatID, "", message)
}

func (a *Adapter) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	return a.sendTextMessage(ctx, chatID, replyToMsgID, message)
}

func (a *Adapter) SendForwardedText(ctx context.Context, chatID string, title string, chunks []string) error {
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("napcat: invalid chat id %q", chatID)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "LuckyHarness"
	}

	a.mu.RLock()
	selfID := strings.TrimSpace(a.selfID)
	a.mu.RUnlock()
	nodes := make([]map[string]any, 0, len(chunks))
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		data := map[string]any{
			"name": title,
			"content": []map[string]any{
				{
					"type": "text",
					"data": map[string]any{"text": chunk},
				},
			},
		}
		if selfID != "" {
			data["uin"] = selfID
		}
		nodes = append(nodes, map[string]any{
			"type": "node",
			"data": data,
		})
	}
	if len(nodes) == 0 {
		return a.Send(ctx, chatID, "")
	}

	switch parts[0] {
	case "private", "c2c":
		return a.sendAction(ctx, "send_private_forward_msg", map[string]any{
			"user_id":  oneBotIDValue(parts[1]),
			"messages": nodes,
		})
	case "group":
		return a.sendAction(ctx, "send_group_forward_msg", map[string]any{
			"group_id": oneBotIDValue(parts[1]),
			"messages": nodes,
		})
	default:
		return fmt.Errorf("napcat: unsupported chat type %q", parts[0])
	}
}

func (a *Adapter) SendForwardedMedia(ctx context.Context, chatID string, title string, items []gateway.ForwardedMediaItem) error {
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("napcat: invalid chat id %q", chatID)
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "LuckyHarness"
	}

	a.mu.RLock()
	selfID := strings.TrimSpace(a.selfID)
	a.mu.RUnlock()

	nodes := make([]map[string]any, 0, len(items))
	for _, item := range items {
		node, err := a.forwardedMediaNode(title, selfID, item)
		if err != nil {
			return err
		}
		if node != nil {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		return fmt.Errorf("napcat: no media nodes to forward")
	}

	switch parts[0] {
	case "private", "c2c":
		return a.sendAction(ctx, "send_private_forward_msg", map[string]any{
			"user_id":  oneBotIDValue(parts[1]),
			"messages": nodes,
		})
	case "group":
		return a.sendAction(ctx, "send_group_forward_msg", map[string]any{
			"group_id": oneBotIDValue(parts[1]),
			"messages": nodes,
		})
	default:
		return fmt.Errorf("napcat: unsupported chat type %q", parts[0])
	}
}

func (a *Adapter) forwardedMediaNode(title string, selfID string, item gateway.ForwardedMediaItem) (map[string]any, error) {
	var content []map[string]any
	if caption := strings.TrimSpace(item.Caption); caption != "" {
		content = append(content, map[string]any{
			"type": "text",
			"data": map[string]any{"text": caption},
		})
	}

	switch item.Type {
	case gateway.AttachmentImage:
		cqSource, err := cqMediaSource(item.Source)
		if err != nil {
			return nil, err
		}
		content = append(content, map[string]any{
			"type": "image",
			"data": map[string]any{"file": cqSource},
		})
	default:
		source := strings.TrimSpace(item.Source)
		if source != "" {
			content = append(content, map[string]any{
				"type": "text",
				"data": map[string]any{"text": source},
			})
		}
	}
	if len(content) == 0 {
		return nil, nil
	}

	data := map[string]any{
		"name":    title,
		"content": content,
	}
	if selfID != "" {
		data["uin"] = selfID
	}
	return map[string]any{
		"type": "node",
		"data": data,
	}, nil
}

func (a *Adapter) SetTyping(ctx context.Context, chatID string, _ string) error {
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return nil
	}
	userID := ""
	switch parts[0] {
	case "private", "c2c":
		userID = strings.TrimSpace(parts[1])
	default:
		return nil
	}
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	return a.sendAction(ctx, "set_input_status", map[string]any{
		"user_id":    strings.TrimSpace(userID),
		"event_type": 1,
	})
}

func (a *Adapter) AcknowledgeMessage(ctx context.Context, chatID string, messageID string) error {
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 || parts[0] != "group" {
		return nil
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	return a.sendAction(ctx, "set_msg_emoji_like", map[string]any{
		"message_id": messageID,
		"emoji_id":   defaultNapCatAckEmojiID,
		"set":        true,
	})
}

func (a *Adapter) SendPhoto(ctx context.Context, chatID string, replyToMsgID string, source string, caption string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("napcat: empty photo source")
	}
	cqSource, err := cqMediaSource(source)
	if err != nil {
		return err
	}
	var body strings.Builder
	if strings.TrimSpace(caption) != "" {
		body.WriteString(escapeCQText(strings.TrimSpace(caption)))
		body.WriteString("\n")
	}
	body.WriteString("[CQ:image,file=")
	body.WriteString(escapeCQParam(cqSource))
	body.WriteString("]")
	return a.sendRawMessage(ctx, chatID, replyToMsgID, body.String())
}

func (a *Adapter) SendDocument(ctx context.Context, chatID string, replyToMsgID string, source string, caption string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return fmt.Errorf("napcat: empty document source")
	}
	if strings.TrimSpace(caption) != "" {
		if err := a.SendWithReply(ctx, chatID, replyToMsgID, caption); err != nil {
			return err
		}
	}
	if strings.HasPrefix(strings.ToLower(source), "http://") || strings.HasPrefix(strings.ToLower(source), "https://") {
		return a.SendWithReply(ctx, chatID, replyToMsgID, "文件："+source)
	}

	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("napcat: invalid chat id %q", chatID)
	}
	filePath, err := oneBotUploadFileSource(source)
	if err != nil {
		return err
	}
	params := map[string]any{
		"file": filePath,
		"name": mediaUploadFileName(source),
	}
	switch parts[0] {
	case "private", "c2c":
		params["user_id"] = oneBotIDValue(parts[1])
		return a.sendAction(ctx, "upload_private_file", params)
	case "group":
		params["group_id"] = oneBotIDValue(parts[1])
		return a.sendAction(ctx, "upload_group_file", params)
	default:
		return fmt.Errorf("napcat: unsupported chat type %q", parts[0])
	}
}

func (a *Adapter) SendStream(_ context.Context, chatID string, replyToMsgID string) (gateway.StreamSender, error) {
	if !a.IsRunning() {
		return nil, fmt.Errorf("napcat: adapter not running")
	}
	return &streamSender{
		adapter:      a,
		chatID:       chatID,
		replyToMsgID: strings.TrimSpace(replyToMsgID),
		messageID:    strings.TrimSpace(replyToMsgID),
	}, nil
}

func (a *Adapter) handleWebSocket(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[napcat] websocket upgrade failed: %v\n", err)
		return
	}

	a.mu.Lock()
	if a.conn != nil {
		_ = a.conn.Close()
	}
	a.conn = conn
	a.connected = true
	a.mu.Unlock()

	fmt.Printf("[napcat] reverse websocket connected from %s\n", r.RemoteAddr)
	defer func() {
		_ = conn.Close()
		a.mu.Lock()
		if a.conn == conn {
			a.conn = nil
			a.connected = false
		}
		a.mu.Unlock()
		fmt.Printf("[napcat] reverse websocket disconnected from %s\n", r.RemoteAddr)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if a.handleActionResponse(data) {
			continue
		}
		a.mu.RLock()
		handler := a.handler
		a.mu.RUnlock()
		if handler == nil {
			continue
		}
		payload := append([]byte(nil), data...)
		go func() {
			msg := a.convertEvent(payload)
			if msg == nil {
				return
			}
			if err := handler(ctx, msg); err != nil {
				fmt.Printf("[napcat] handler error: %v\n", err)
			}
		}()
	}
}

func (a *Adapter) handleActionResponse(data []byte) bool {
	var resp actionResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return false
	}
	echo := strings.TrimSpace(resp.Echo)
	if echo == "" {
		return false
	}
	a.mu.RLock()
	ch := a.actionResponses[echo]
	a.mu.RUnlock()
	if ch == nil {
		return true
	}
	select {
	case ch <- resp:
	default:
	}
	return true
}

func (a *Adapter) authorized(r *http.Request) bool {
	token := strings.TrimSpace(a.cfg.AccessToken)
	if token == "" {
		return true
	}
	if strings.TrimSpace(r.URL.Query().Get("access_token")) == token || strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true
	}
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.EqualFold(auth, "Bearer "+token) || strings.EqualFold(auth, token) {
		return true
	}
	return false
}

func (a *Adapter) convertEvent(data []byte) *gateway.Message {
	var evt oneBotEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(evt.PostType)) != "message" {
		return nil
	}

	selfID := rawIDString(evt.SelfID)
	if selfID != "" {
		a.mu.Lock()
		a.selfID = selfID
		a.mu.Unlock()
	} else {
		a.mu.RLock()
		selfID = a.selfID
		a.mu.RUnlock()
	}

	userID := rawIDString(evt.UserID)
	if userID == "" {
		userID = rawIDString(evt.Sender.UserID)
	}
	if userID == "" || !a.cfg.IsUserAllowed(userID) {
		return nil
	}

	parsed := parseOneBotMessage(evt.Message, evt.RawMessage)
	msg := &gateway.Message{
		ID:        rawIDString(evt.MessageID),
		Text:      strings.TrimSpace(parsed.Text),
		Timestamp: oneBotTimestamp(evt.Time),
		Sender: gateway.User{
			ID:        userID,
			Username:  strings.TrimSpace(evt.Sender.Nickname),
			FirstName: firstNonEmpty(evt.Sender.Card, evt.Sender.Nickname),
		},
	}
	msg.IsCommand, msg.Command, msg.Args = parseCommand(msg.Text)

	switch strings.ToLower(strings.TrimSpace(evt.MessageType)) {
	case "private":
		msg.Chat = gateway.Chat{ID: "private:" + userID, Type: gateway.ChatPrivate, Username: evt.Sender.Nickname}
		if !a.cfg.IsChatAllowed(msg.Chat.ID, userID) {
			return nil
		}
	case "group":
		groupID := rawIDString(evt.GroupID)
		if groupID == "" {
			return nil
		}
		msg.Chat = gateway.Chat{ID: "group:" + groupID, Type: gateway.ChatGroup, Title: groupID}
		if !a.cfg.IsChatAllowed(msg.Chat.ID, groupID) {
			return nil
		}
		triggered, triggerType := a.groupTriggered(parsed, selfID)
		if !triggered {
			return nil
		}
		msg.IsGroupTrigger = triggerType != ""
		msg.TriggerType = triggerType
	default:
		return nil
	}

	if len(parsed.Attachments) > 0 {
		for i := range parsed.Attachments {
			setAttachmentMetadata(&parsed.Attachments[i], "napcat_group_id", rawIDString(evt.GroupID))
			setAttachmentMetadata(&parsed.Attachments[i], "napcat_message_type", strings.ToLower(strings.TrimSpace(evt.MessageType)))
		}
		a.populateAttachments(parsed.Attachments)
		msg.Attachments = parsed.Attachments
	}
	return msg
}

func (a *Adapter) groupTriggered(parsed parsedMessage, selfID string) (bool, string) {
	switch a.cfg.normalizedGroupTriggerMode() {
	case "all":
		return true, ""
	case "none":
		return false, ""
	}
	if selfID != "" {
		for _, qq := range parsed.AtQQs {
			if strings.TrimSpace(qq) == selfID {
				return true, "mention"
			}
		}
	}
	if len(parsed.AtQQs) > 0 {
		return false, ""
	}
	if strings.TrimSpace(parsed.ReplyID) != "" {
		return true, "reply"
	}
	return false, ""
}

func (a *Adapter) sendTextMessage(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	return a.sendRawMessage(ctx, chatID, replyToMsgID, escapeCQText(strings.TrimSpace(message)))
}

func (a *Adapter) sendRawMessage(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	parts := strings.SplitN(chatID, ":", 2)
	if len(parts) != 2 {
		return fmt.Errorf("napcat: invalid chat id %q", chatID)
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = " "
	}
	if strings.TrimSpace(replyToMsgID) != "" {
		message = "[CQ:reply,id=" + escapeCQParam(replyToMsgID) + "]" + message
	}

	switch parts[0] {
	case "private", "c2c":
		return a.sendAction(ctx, "send_private_msg", map[string]any{
			"user_id": oneBotIDValue(parts[1]),
			"message": message,
		})
	case "group":
		return a.sendAction(ctx, "send_group_msg", map[string]any{
			"group_id": oneBotIDValue(parts[1]),
			"message":  message,
		})
	default:
		return fmt.Errorf("napcat: unsupported chat type %q", parts[0])
	}
}

func (a *Adapter) sendAction(ctx context.Context, action string, params map[string]any) error {
	_, err := a.sendActionWithResponse(ctx, action, params, false)
	return err
}

func (a *Adapter) sendActionWithResponse(ctx context.Context, action string, params map[string]any, wait bool) (actionResponse, error) {
	a.mu.RLock()
	conn := a.conn
	connected := a.connected
	a.mu.RUnlock()
	if conn == nil || !connected {
		return actionResponse{}, fmt.Errorf("napcat: reverse websocket is not connected")
	}

	echo := "lh-" + strconv.FormatUint(a.echoSeq.Add(1), 10)
	req := actionRequest{Action: action, Params: params, Echo: echo}
	var respCh chan actionResponse
	if wait {
		respCh = make(chan actionResponse, 1)
		a.mu.Lock()
		if a.actionResponses == nil {
			a.actionResponses = make(map[string]chan actionResponse)
		}
		a.actionResponses[echo] = respCh
		a.mu.Unlock()
		defer func() {
			a.mu.Lock()
			delete(a.actionResponses, echo)
			a.mu.Unlock()
		}()
	}

	a.writeMu.Lock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else {
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	}
	if err := conn.WriteJSON(req); err != nil {
		a.writeMu.Unlock()
		return actionResponse{}, fmt.Errorf("napcat: send action %s: %w", action, err)
	}
	a.writeMu.Unlock()

	if !wait {
		return actionResponse{}, nil
	}
	select {
	case resp := <-respCh:
		if resp.RetCode != 0 || strings.EqualFold(strings.TrimSpace(resp.Status), "failed") {
			msg := firstNonEmpty(resp.Message, resp.Wording, fmt.Sprintf("retcode %d", resp.RetCode))
			return resp, fmt.Errorf("napcat: action %s failed: %s", action, msg)
		}
		return resp, nil
	case <-ctx.Done():
		return actionResponse{}, fmt.Errorf("napcat: action %s response timeout: %w", action, ctx.Err())
	}
}

func parseOneBotMessage(raw json.RawMessage, fallback string) parsedMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" || strings.TrimSpace(string(raw)) == "null" {
		return parseCQMessage(fallback)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return parseCQMessage(text)
	}

	var segments []messageSegment
	if err := json.Unmarshal(raw, &segments); err != nil {
		return parseCQMessage(fallback)
	}

	var parsed parsedMessage
	var textParts []string
	for _, seg := range segments {
		switch strings.ToLower(strings.TrimSpace(seg.Type)) {
		case "text":
			if text := dataString(seg.Data, "text"); strings.TrimSpace(text) != "" {
				textParts = append(textParts, text)
			}
		case "at":
			if qq := dataString(seg.Data, "qq"); strings.TrimSpace(qq) != "" {
				parsed.AtQQs = append(parsed.AtQQs, strings.TrimSpace(qq))
			}
		case "reply":
			parsed.ReplyID = dataString(seg.Data, "id")
		case "image":
			parsed.Attachments = append(parsed.Attachments, attachmentFromSegment(gateway.AttachmentImage, seg.Data))
		case "record":
			parsed.Attachments = append(parsed.Attachments, attachmentFromSegment(gateway.AttachmentAudio, seg.Data))
		case "video":
			parsed.Attachments = append(parsed.Attachments, attachmentFromSegment(gateway.AttachmentVideo, seg.Data))
		case "file":
			parsed.Attachments = append(parsed.Attachments, attachmentFromSegment(gateway.AttachmentDocument, seg.Data))
		}
	}
	parsed.Text = strings.TrimSpace(strings.Join(textParts, " "))
	return parsed
}

func parseCQMessage(raw string) parsedMessage {
	var parsed parsedMessage
	var text strings.Builder
	for i := 0; i < len(raw); {
		if strings.HasPrefix(raw[i:], "[CQ:") {
			end := strings.Index(raw[i:], "]")
			if end >= 0 {
				code := raw[i+4 : i+end]
				typ, params := parseCQCode(code)
				switch typ {
				case "at":
					if qq := params["qq"]; qq != "" {
						parsed.AtQQs = append(parsed.AtQQs, qq)
					}
				case "reply":
					parsed.ReplyID = params["id"]
				case "image":
					parsed.Attachments = append(parsed.Attachments, attachmentFromCQ(gateway.AttachmentImage, params))
				case "record":
					parsed.Attachments = append(parsed.Attachments, attachmentFromCQ(gateway.AttachmentAudio, params))
				case "video":
					parsed.Attachments = append(parsed.Attachments, attachmentFromCQ(gateway.AttachmentVideo, params))
				case "file":
					parsed.Attachments = append(parsed.Attachments, attachmentFromCQ(gateway.AttachmentDocument, params))
				}
				i += end + 1
				continue
			}
		}
		text.WriteByte(raw[i])
		i++
	}
	parsed.Text = strings.TrimSpace(unescapeCQText(text.String()))
	return parsed
}

func parseCQCode(code string) (string, map[string]string) {
	parts := strings.Split(code, ",")
	typ := strings.ToLower(strings.TrimSpace(parts[0]))
	params := make(map[string]string)
	for _, part := range parts[1:] {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		params[strings.TrimSpace(k)] = unescapeCQText(strings.TrimSpace(v))
	}
	return typ, params
}

func attachmentFromSegment(kind gateway.AttachmentType, data map[string]any) gateway.Attachment {
	fileID := firstNonEmpty(dataString(data, "file_id"), dataString(data, "id"), dataString(data, "file"))
	fileURL := firstNonEmpty(dataString(data, "url"), dataString(data, "file_url"), dataString(data, "download_url"))
	filePath := firstNonEmpty(dataString(data, "path"), dataString(data, "file_path"), dataString(data, "local_path"))
	if filePath == "" {
		filePath = localPathCandidate(firstNonEmpty(dataString(data, "file"), fileURL))
	}
	if fileURL == "" && isHTTPURL(dataString(data, "file")) {
		fileURL = dataString(data, "file")
	}
	name := firstNonEmpty(dataString(data, "name"), dataString(data, "file_name"), dataString(data, "filename"), filepath.Base(fileID), filepath.Base(filePath), filepath.Base(fileURL))
	mimeType := firstNonEmpty(dataString(data, "mime"), dataString(data, "mime_type"), mimeForAttachment(kind, name, fileURL))
	att := gateway.Attachment{
		Type:     kind,
		FileID:   fileID,
		FileURL:  fileURL,
		FilePath: filePath,
		FileName: name,
		MimeType: mimeType,
		FileSize: attachmentSizeFromMap(data),
	}
	for _, key := range []string{"busid", "fid", "file_uuid", "folder_id"} {
		setAttachmentMetadata(&att, "napcat_"+key, dataString(data, key))
	}
	return att
}

func attachmentFromCQ(kind gateway.AttachmentType, params map[string]string) gateway.Attachment {
	fileID := firstNonEmpty(params["file_id"], params["id"], params["file"])
	fileURL := firstNonEmpty(params["url"], params["file_url"], params["download_url"])
	filePath := firstNonEmpty(params["path"], params["file_path"], params["local_path"])
	if filePath == "" {
		filePath = localPathCandidate(firstNonEmpty(params["file"], fileURL))
	}
	if fileURL == "" && isHTTPURL(params["file"]) {
		fileURL = params["file"]
	}
	name := firstNonEmpty(params["name"], params["file_name"], params["filename"], filepath.Base(fileID), filepath.Base(filePath), filepath.Base(fileURL))
	mimeType := firstNonEmpty(params["mime"], params["mime_type"], mimeForAttachment(kind, name, fileURL))
	att := gateway.Attachment{
		Type:     kind,
		FileID:   fileID,
		FileURL:  fileURL,
		FilePath: filePath,
		FileName: name,
		MimeType: mimeType,
		FileSize: attachmentSizeFromParams(params),
	}
	for _, key := range []string{"busid", "fid", "file_uuid", "folder_id"} {
		setAttachmentMetadata(&att, "napcat_"+key, params[key])
	}
	return att
}

func (a *Adapter) populateAttachments(attachments []gateway.Attachment) {
	for i := range attachments {
		a.populateAttachmentData(&attachments[i])
	}
}

func (a *Adapter) populateAttachmentData(att *gateway.Attachment) {
	if att == nil {
		return
	}
	if path := strings.TrimSpace(att.FilePath); path != "" {
		if attachmentFileAvailable(path) {
			att.FilePath = normalizeMediaSource(path)
			return
		}
		fmt.Printf("[napcat] attachment local path is not accessible, trying alternate source: %s\n", path)
		att.FilePath = ""
	}
	if att.FileSize > defaultNapCatAttachmentDownloadLimit {
		return
	}
	if strings.TrimSpace(att.FileURL) == "" && strings.TrimSpace(att.FileID) != "" {
		a.populateAttachmentFromGetFile(att)
		if strings.TrimSpace(att.FilePath) != "" || len(att.Data) > 0 {
			return
		}
	}
	if strings.TrimSpace(att.FileURL) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, att.FileURL, nil)
	if err != nil {
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return
	}
	if ct := strings.TrimSpace(resp.Header.Get("Content-Type")); ct != "" && (att.MimeType == "" || strings.HasSuffix(att.MimeType, "/*")) {
		att.MimeType = strings.Split(ct, ";")[0]
	}

	dir, err := napcatAttachmentStorageDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}

	fileName := napcatAttachmentFileName(att)
	tmpFile, err := os.CreateTemp(dir, fileName+".*.part")
	if err != nil {
		return
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
	}()

	reader := io.LimitReader(resp.Body, defaultNapCatAttachmentDownloadLimit+1)
	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	if written > defaultNapCatAttachmentDownloadLimit {
		_ = os.Remove(tmpPath)
		return
	}
	if att.FileSize == 0 {
		att.FileSize = written
	}

	finalPath := filepath.Join(dir, fileName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return
	}
	att.FilePath = finalPath
}

func (a *Adapter) populateAttachmentFromGetFile(att *gateway.Attachment) {
	if a == nil || att == nil || strings.TrimSpace(att.FileID) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := a.sendActionWithResponse(ctx, "get_file", map[string]any{"file_id": strings.TrimSpace(att.FileID)}, true)
	if err != nil {
		fmt.Printf("[napcat] get_file failed for attachment %s: %v\n", attachmentDebugName(att), err)
		a.populateAttachmentFromGroupFileURL(ctx, att)
		return
	}
	var data map[string]any
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		a.populateAttachmentFromGroupFileURL(ctx, att)
		return
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		fmt.Printf("[napcat] decode get_file response failed for attachment %s: %v\n", attachmentDebugName(att), err)
		a.populateAttachmentFromGroupFileURL(ctx, att)
		return
	}
	if strings.TrimSpace(att.FileName) == "" {
		att.FileName = firstNonEmpty(dataString(data, "file_name"), dataString(data, "name"), filepath.Base(dataString(data, "file")))
	}
	if att.FileSize == 0 {
		att.FileSize = attachmentSizeFromMap(data)
	}
	if strings.TrimSpace(att.MimeType) == "" || strings.HasSuffix(att.MimeType, "/*") || att.MimeType == "application/octet-stream" {
		att.MimeType = firstNonEmpty(dataString(data, "mime"), dataString(data, "mime_type"), mimeForAttachment(att.Type, att.FileName, dataString(data, "url"), dataString(data, "path"), dataString(data, "file")))
	}
	if local := localPathCandidate(firstNonEmpty(dataString(data, "path"), dataString(data, "file_path"), dataString(data, "local_path"), dataString(data, "file"))); local != "" {
		if attachmentFileAvailable(local) {
			att.FilePath = normalizeMediaSource(local)
			return
		}
		fmt.Printf("[napcat] get_file returned inaccessible local path, trying alternate source: %s\n", local)
	}
	if u := firstNonEmpty(dataString(data, "url"), dataString(data, "file_url"), dataString(data, "download_url")); isHTTPURL(u) {
		att.FileURL = u
		return
	}
	if b64 := strings.TrimSpace(firstNonEmpty(dataString(data, "base64"), dataString(data, "file_data"))); b64 != "" {
		b64 = strings.TrimPrefix(b64, "base64://")
		if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
			att.Data = decoded
			if att.FileSize == 0 {
				att.FileSize = int64(len(decoded))
			}
			if path, err := writeNapCatAttachmentBytes(att, decoded); err == nil {
				att.FilePath = path
			} else {
				fmt.Printf("[napcat] cache get_file base64 attachment failed: %v\n", err)
			}
		}
	}
	if strings.TrimSpace(att.FilePath) == "" && strings.TrimSpace(att.FileURL) == "" && len(att.Data) == 0 {
		a.populateAttachmentFromGroupFileURL(ctx, att)
	}
}

func (a *Adapter) populateAttachmentFromGroupFileURL(ctx context.Context, att *gateway.Attachment) {
	if a == nil || att == nil || strings.TrimSpace(att.FileID) == "" {
		return
	}
	groupID := attachmentMetadata(att, "napcat_group_id")
	if strings.TrimSpace(groupID) == "" {
		return
	}
	params := map[string]any{
		"group_id": oneBotIDValue(groupID),
		"file_id":  strings.TrimSpace(att.FileID),
	}
	if busid := attachmentMetadata(att, "napcat_busid"); strings.TrimSpace(busid) != "" {
		params["busid"] = oneBotIDValue(busid)
	}
	resp, err := a.sendActionWithResponse(ctx, "get_group_file_url", params, true)
	if err != nil {
		fmt.Printf("[napcat] get_group_file_url failed for attachment %s: %v\n", attachmentDebugName(att), err)
		return
	}
	if len(resp.Data) == 0 || string(resp.Data) == "null" {
		return
	}
	var data map[string]any
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		fmt.Printf("[napcat] decode get_group_file_url response failed for attachment %s: %v\n", attachmentDebugName(att), err)
		return
	}
	if u := firstNonEmpty(dataString(data, "url"), dataString(data, "file_url"), dataString(data, "download_url")); isHTTPURL(u) {
		att.FileURL = u
	}
}

func setAttachmentMetadata(att *gateway.Attachment, key, value string) {
	if att == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return
	}
	if att.Metadata == nil {
		att.Metadata = make(map[string]string)
	}
	att.Metadata[strings.TrimSpace(key)] = strings.TrimSpace(value)
}

func attachmentMetadata(att *gateway.Attachment, key string) string {
	if att == nil || att.Metadata == nil {
		return ""
	}
	return strings.TrimSpace(att.Metadata[key])
}

func attachmentDebugName(att *gateway.Attachment) string {
	if att == nil {
		return "<nil>"
	}
	return firstNonEmpty(att.FileName, att.FileID, string(att.Type), "attachment")
}

func attachmentFileAvailable(path string) bool {
	path = normalizeMediaSource(path)
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func writeNapCatAttachmentBytes(att *gateway.Attachment, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty attachment data")
	}
	dir, err := napcatAttachmentStorageDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	fileName := napcatAttachmentFileName(att)
	tmpFile, err := os.CreateTemp(dir, fileName+".*.part")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	finalPath := filepath.Join(dir, fileName)
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return finalPath, nil
}

func napcatAttachmentStorageDir() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".luckyharness", "data", "napcat", "attachments"), nil
	}
	return filepath.Join(os.TempDir(), "luckyharness", "napcat", "attachments"), nil
}

func napcatAttachmentFileName(att *gateway.Attachment) string {
	name := strings.TrimSpace(att.FileName)
	if name == "" {
		switch att.Type {
		case gateway.AttachmentImage:
			name = "image"
		case gateway.AttachmentAudio:
			name = "audio"
		case gateway.AttachmentVideo:
			name = "video"
		default:
			name = "document"
		}
	}

	name = sanitizeNapCatFileName(filepath.Base(name))
	if strings.TrimSpace(name) == "" || name == "." {
		name = "attachment"
	}
	if filepath.Ext(name) == "" {
		if ext := mimeExtensionForAttachment(att); ext != "" {
			name += ext
		}
	}

	prefix := strings.TrimSpace(att.FileID)
	if prefix == "" {
		prefix = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	prefix = sanitizeNapCatFileName(prefix)
	if len(prefix) > 48 {
		prefix = prefix[:48]
	}
	if strings.TrimSpace(prefix) == "" || prefix == "." {
		prefix = "attachment"
	}
	return prefix + "-" + name
}

func sanitizeNapCatFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '_'
		default:
			return r
		}
	}, strings.TrimSpace(name))
	return name
}

func mimeExtensionForAttachment(att *gateway.Attachment) string {
	if exts, err := mime.ExtensionsByType(strings.TrimSpace(att.MimeType)); err == nil && len(exts) > 0 {
		return exts[0]
	}
	switch att.Type {
	case gateway.AttachmentImage:
		return ".jpg"
	case gateway.AttachmentAudio:
		return ".ogg"
	case gateway.AttachmentVideo:
		return ".mp4"
	default:
		return ""
	}
}

func dataString(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	v, ok := data[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		return fmt.Sprint(x)
	}
}

func rawIDString(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.Trim(strings.TrimSpace(string(raw)), `"`)
}

func oneBotTimestamp(ts int64) time.Time {
	if ts <= 0 {
		return time.Now()
	}
	return time.Unix(ts, 0)
}

func parseCommand(text string) (bool, string, string) {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return false, "", ""
	}
	parts := strings.SplitN(text, " ", 2)
	if len(parts) == 1 {
		return true, parts[0], ""
	}
	return true, parts[0], strings.TrimSpace(parts[1])
}

func oneBotIDValue(id string) any {
	id = strings.TrimSpace(id)
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		return n
	}
	return id
}

func cqMediaSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	lower := strings.ToLower(source)
	if lower == "" {
		return "", fmt.Errorf("napcat: empty media source")
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "base64://") {
		return source, nil
	}
	local := normalizeMediaSource(source)
	data, err := os.ReadFile(local)
	if err != nil {
		return "", fmt.Errorf("napcat: read media file: %w", err)
	}
	return "base64://" + base64.StdEncoding.EncodeToString(data), nil
}

func oneBotUploadFileSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	lower := strings.ToLower(source)
	if lower == "" {
		return "", fmt.Errorf("napcat: empty file source")
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "base64://") {
		return source, nil
	}
	local := normalizeMediaSource(source)
	data, err := os.ReadFile(local)
	if err != nil {
		return "", fmt.Errorf("napcat: read upload file: %w", err)
	}
	return "base64://" + base64.StdEncoding.EncodeToString(data), nil
}

func mediaUploadFileName(source string) string {
	source = normalizeMediaSource(source)
	name := filepath.Base(source)
	if strings.TrimSpace(name) == "" || name == "." || strings.HasPrefix(strings.ToLower(source), "base64://") {
		return "file"
	}
	return name
}

func normalizeMediaSource(source string) string {
	source = strings.TrimSpace(source)
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		if u, err := url.Parse(source); err == nil && u.Path != "" {
			return u.Path
		}
	}
	if strings.HasPrefix(source, "~/") {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Join(home, strings.TrimPrefix(source, "~/"))
		}
	}
	return source
}

func mimeForAttachment(kind gateway.AttachmentType, values ...string) string {
	for _, value := range values {
		if mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(value))); mt != "" {
			return mt
		}
	}
	switch kind {
	case gateway.AttachmentImage:
		return "image/*"
	case gateway.AttachmentAudio:
		return "audio/*"
	case gateway.AttachmentVideo:
		return "video/*"
	case gateway.AttachmentDocument:
		return "application/octet-stream"
	default:
		return ""
	}
}

func isHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func localPathCandidate(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || isHTTPURL(value) || strings.HasPrefix(strings.ToLower(value), "base64://") {
		return ""
	}
	if filepath.IsAbs(value) || strings.HasPrefix(value, "~/") || strings.HasPrefix(strings.ToLower(value), "file://") {
		return normalizeMediaSource(value)
	}
	return ""
}

func attachmentSizeFromMap(data map[string]any) int64 {
	if data == nil {
		return 0
	}
	for _, key := range []string{"size", "file_size"} {
		if n := parseInt64Value(data[key]); n > 0 {
			return n
		}
	}
	return 0
}

func attachmentSizeFromParams(params map[string]string) int64 {
	if params == nil {
		return 0
	}
	for _, key := range []string{"size", "file_size"} {
		if n, err := strconv.ParseInt(strings.TrimSpace(params[key]), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func parseInt64Value(v any) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case int:
		if x > 0 {
			return int64(x)
		}
	case int64:
		if x > 0 {
			return x
		}
	case float64:
		if x > 0 {
			return int64(x)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil && n > 0 {
			return n
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" && strings.TrimSpace(v) != "." {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func escapeCQText(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "[", "&#91;", "]", "&#93;")
	return replacer.Replace(text)
}

func escapeCQParam(text string) string {
	replacer := strings.NewReplacer("&", "&amp;", "[", "&#91;", "]", "&#93;", ",", "&#44;")
	return replacer.Replace(strings.TrimSpace(text))
}

func unescapeCQText(text string) string {
	replacer := strings.NewReplacer("&#91;", "[", "&#93;", "]", "&#44;", ",", "&amp;", "&")
	return replacer.Replace(text)
}

type streamSender struct {
	adapter      *Adapter
	chatID       string
	replyToMsgID string
	messageID    string

	mu              sync.Mutex
	content         strings.Builder
	lastProgressMsg string
	finished        bool
}

func (s *streamSender) Append(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return fmt.Errorf("napcat: stream sender already finished")
	}
	s.content.WriteString(content)
	return nil
}

func (s *streamSender) SetThinking(label string) error {
	return s.sendProgress(strings.TrimSpace(label))
}

func (s *streamSender) SetToolCall(name, args string) error {
	name = strings.TrimSpace(name)
	args = strings.TrimSpace(args)
	if len(args) > 120 {
		args = args[:117] + "..."
	}
	if args == "" {
		return s.sendProgress(fmt.Sprintf("正在调用工具：%s", name))
	}
	return s.sendProgress(fmt.Sprintf("正在调用工具：%s\n参数：%s", name, args))
}

func (s *streamSender) SetResult(content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return nil
	}
	s.content.Reset()
	s.content.WriteString(content)
	return nil
}

func (s *streamSender) Finish() error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil
	}
	s.finished = true
	message := strings.TrimSpace(s.content.String())
	s.mu.Unlock()
	if message == "" {
		message = "我这边暂时还没有整理出可发送的结果。"
	}
	return s.sendMessage(message)
}

func (s *streamSender) MessageID() string {
	return s.messageID
}

func (s *streamSender) sendProgress(message string) error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" || message == s.lastProgressMsg {
		s.mu.Unlock()
		return nil
	}
	s.lastProgressMsg = message
	s.mu.Unlock()
	return s.sendMessage(message)
}

func (s *streamSender) sendMessage(message string) error {
	if s == nil || s.adapter == nil {
		return fmt.Errorf("napcat: stream sender not initialized")
	}
	sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if strings.TrimSpace(s.replyToMsgID) != "" {
		return s.adapter.SendWithReply(sendCtx, s.chatID, s.replyToMsgID, message)
	}
	return s.adapter.Send(sendCtx, s.chatID, message)
}

var (
	_ gateway.StreamGateway = (*Adapter)(nil)
	_ gateway.StreamSender  = (*streamSender)(nil)
)
