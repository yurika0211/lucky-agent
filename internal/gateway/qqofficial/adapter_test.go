package qqofficial

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/gateway"
)

type qqHandlerTestSender struct {
	mu            sync.Mutex
	messages      []string
	acks          []string
	forwards      []qqHandlerForwardedText
	mediaForwards []qqHandlerForwardedMedia
}

type qqHandlerForwardedText struct {
	ChatID string
	Title  string
	Chunks []string
}

type qqHandlerForwardedMedia struct {
	ChatID string
	Title  string
	Items  []gateway.ForwardedMediaItem
}

func (s *qqHandlerTestSender) Name() string                { return "test" }
func (s *qqHandlerTestSender) Start(context.Context) error { return nil }
func (s *qqHandlerTestSender) Stop() error                 { return nil }
func (s *qqHandlerTestSender) IsRunning() bool             { return true }

func (s *qqHandlerTestSender) Send(_ context.Context, _ string, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}

func (s *qqHandlerTestSender) SendWithReply(_ context.Context, _ string, _ string, message string) error {
	return s.Send(context.Background(), "", message)
}

func (s *qqHandlerTestSender) SendPhoto(_ context.Context, _ string, _ string, source string, caption string) error {
	return s.Send(context.Background(), "", strings.TrimSpace(source+" "+caption))
}

func (s *qqHandlerTestSender) SendDocument(_ context.Context, _ string, _ string, source string, caption string) error {
	return s.Send(context.Background(), "", strings.TrimSpace(source+" "+caption))
}

func (s *qqHandlerTestSender) AcknowledgeMessage(_ context.Context, chatID string, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acks = append(s.acks, chatID+"|"+messageID)
	return nil
}

func (s *qqHandlerTestSender) SendForwardedText(_ context.Context, chatID string, title string, chunks []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forwards = append(s.forwards, qqHandlerForwardedText{
		ChatID: chatID,
		Title:  title,
		Chunks: append([]string(nil), chunks...),
	})
	return nil
}

func (s *qqHandlerTestSender) SendForwardedMedia(_ context.Context, chatID string, title string, items []gateway.ForwardedMediaItem) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mediaForwards = append(s.mediaForwards, qqHandlerForwardedMedia{
		ChatID: chatID,
		Title:  title,
		Items:  append([]gateway.ForwardedMediaItem(nil), items...),
	})
	return nil
}

func (s *qqHandlerTestSender) Messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
}

func (s *qqHandlerTestSender) Acks() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.acks...)
}

func (s *qqHandlerTestSender) Forwards() []qqHandlerForwardedText {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]qqHandlerForwardedText(nil), s.forwards...)
}

func (s *qqHandlerTestSender) MediaForwards() []qqHandlerForwardedMedia {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]qqHandlerForwardedMedia(nil), s.mediaForwards...)
}

func TestBuildIntentBits(t *testing.T) {
	bits := buildIntentBits([]string{"group_messages", "c2c_messages"})
	if bits&(1<<25) == 0 {
		t.Fatal("expected group message intent bit")
	}
	if bits&(1<<26) == 0 {
		t.Fatal("expected c2c message intent bit")
	}
}

func TestParseCommand(t *testing.T) {
	isCommand, cmd, args := parseCommand("/hello world")
	if !isCommand || cmd != "/hello" || args != "world" {
		t.Fatalf("unexpected parse result: %v %q %q", isCommand, cmd, args)
	}
}

func TestStripLeadingQQMention(t *testing.T) {
	got := stripLeadingQQMention("<@!123456> 你好")
	if got != "你好" {
		t.Fatalf("stripLeadingQQMention() = %q", got)
	}
}

func TestConvertDispatchGroupAtMessage(t *testing.T) {
	a := NewAdapter(DefaultConfig())
	raw, _ := json.Marshal(incomingMessageEvent{
		ID:          "msg-1",
		Content:     "<@!123456> /ping now",
		GroupOpenID: "group-1",
		Author: messageAuthor{
			ID:       "user-1",
			Username: "tester",
		},
	})

	msg := a.convertDispatch("GROUP_AT_MESSAGE_CREATE", raw)
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Chat.Type != gateway.ChatGroup || msg.Chat.ID != "group:group-1" {
		t.Fatalf("unexpected chat: %+v", msg.Chat)
	}
	if !msg.IsGroupTrigger || msg.TriggerType != "mention" {
		t.Fatalf("expected group trigger metadata: %+v", msg)
	}
	if !msg.IsCommand || msg.Command != "/ping" || msg.Args != "now" {
		t.Fatalf("unexpected command parsing: %+v", msg)
	}
}

func TestConvertDispatchC2CMessage(t *testing.T) {
	a := NewAdapter(DefaultConfig())
	raw, _ := json.Marshal(incomingMessageEvent{
		ID:      "msg-2",
		Content: "hello there",
		Author: messageAuthor{
			ID:       "user-2",
			Username: "tester2",
		},
	})

	msg := a.convertDispatch("C2C_MESSAGE_CREATE", raw)
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Chat.Type != gateway.ChatPrivate || msg.Chat.ID != "c2c:user-2" {
		t.Fatalf("unexpected chat: %+v", msg.Chat)
	}
	if msg.Text != "hello there" {
		t.Fatalf("unexpected text: %q", msg.Text)
	}
}

func TestAccessTokenResponseUnmarshalExpiresInString(t *testing.T) {
	var resp accessTokenResponse
	if err := json.Unmarshal([]byte(`{"access_token":"abc","expires_in":"7200"}`), &resp); err != nil {
		t.Fatalf("unmarshal string expires_in: %v", err)
	}
	if resp.AccessToken != "abc" || resp.ExpiresIn != 7200 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAccessTokenResponseUnmarshalExpiresInNumber(t *testing.T) {
	var resp accessTokenResponse
	if err := json.Unmarshal([]byte(`{"access_token":"abc","expires_in":7200}`), &resp); err != nil {
		t.Fatalf("unmarshal numeric expires_in: %v", err)
	}
	if resp.AccessToken != "abc" || resp.ExpiresIn != 7200 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAdapterReconnectsAfterGatewayReconnectOp(t *testing.T) {
	var connections atomic.Int32
	messageReceived := make(chan *gateway.Message, 1)

	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.Error(w, "unexpected token path "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(accessTokenResponse{AccessToken: "token", ExpiresIn: 3600})
	}))
	defer tokenServer.Close()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		n := connections.Add(1)
		if err := conn.WriteJSON(gatewayFrame{Op: helloOp, D: mustJSON(helloPayload{HeartbeatInterval: 60_000})}); err != nil {
			t.Errorf("write hello: %v", err)
			return
		}
		var identify gatewayFrame
		if err := conn.ReadJSON(&identify); err != nil {
			t.Errorf("read identify: %v", err)
			return
		}
		if identify.Op != identifyOp {
			t.Errorf("expected identify op, got %d", identify.Op)
			return
		}
		if n == 1 {
			_ = conn.WriteJSON(gatewayFrame{Op: reconnectOp})
			return
		}
		raw, _ := json.Marshal(incomingMessageEvent{
			ID:      "msg-reconnected",
			Content: "hello after reconnect",
			Author:  messageAuthor{ID: "user-1", Username: "tester"},
		})
		_ = conn.WriteJSON(gatewayFrame{
			Op: dispatchEventOp,
			T:  "C2C_MESSAGE_CREATE",
			D:  raw,
		})
		<-r.Context().Done()
	}))
	defer wsServer.Close()

	a := NewAdapter(Config{
		AppID:         "app-id",
		AppSecret:     "secret",
		GatewayURL:    wsToHTTPTestURL(t, wsServer.URL),
		ReconnectWait: 1,
	})
	a.accessTokenURL = tokenServer.URL + "/token"
	a.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		messageReceived <- msg
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer a.Stop()

	select {
	case msg := <-messageReceived:
		if msg.Text != "hello after reconnect" {
			t.Fatalf("unexpected message after reconnect: %+v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for reconnected message; connections=%d", connections.Load())
	}
	if got := connections.Load(); got < 2 {
		t.Fatalf("expected at least 2 websocket connections, got %d", got)
	}
	if !a.IsRunning() {
		t.Fatal("adapter should remain running after reconnect")
	}
}

func wsToHTTPTestURL(t *testing.T, raw string) string {
	t.Helper()
	addr := strings.TrimPrefix(raw, "http://")
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatalf("unexpected test server URL %q: %v", raw, err)
	}
	return "ws://" + addr
}

func TestSendWithReplyUsesIncrementingMsgSeq(t *testing.T) {
	var mu sync.Mutex
	var payloads []outgoingMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users/user-1/messages" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		var payload outgoingMessagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "decode payload: "+err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	a := NewAdapter(Config{
		AppID:      "app-id",
		AppSecret:  "app-secret",
		APIBaseURL: server.URL,
	})
	a.accessToken = "token"
	a.tokenExpiry = time.Now().Add(time.Hour)

	if err := a.SendWithReply(context.Background(), "c2c:user-1", "msg-1", "first"); err != nil {
		t.Fatalf("first SendWithReply error = %v", err)
	}
	if err := a.SendWithReply(context.Background(), "c2c:user-1", "msg-1", "second"); err != nil {
		t.Fatalf("second SendWithReply error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(payloads) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(payloads))
	}
	if payloads[0].MsgID != "msg-1" || payloads[1].MsgID != "msg-1" {
		t.Fatalf("unexpected msg_id values: %#v", payloads)
	}
	if payloads[0].MsgSeq == 0 || payloads[1].MsgSeq == 0 {
		t.Fatalf("expected msg_seq to be set: %#v", payloads)
	}
	if payloads[0].MsgSeq == payloads[1].MsgSeq {
		t.Fatalf("expected msg_seq to increment, got %#v", payloads)
	}
}

func TestSendForwardedTextSendsChunksSequentially(t *testing.T) {
	var mu sync.Mutex
	var payloads []outgoingMessagePayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/groups/group-1/messages" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusInternalServerError)
			return
		}
		var payload outgoingMessagePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "decode payload: "+err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	a := NewAdapter(Config{
		AppID:      "app-id",
		AppSecret:  "app-secret",
		APIBaseURL: server.URL,
	})
	a.accessToken = "token"
	a.tokenExpiry = time.Now().Add(time.Hour)

	if err := a.SendForwardedText(context.Background(), "group:group-1", "LuckyHarness", []string{"first", "second"}); err != nil {
		t.Fatalf("SendForwardedText error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(payloads))
	}
	if payloads[0].Content != "first" || payloads[1].Content != "second" {
		t.Fatalf("unexpected forwarded text payloads: %#v", payloads)
	}
}

func TestSendDocumentUploadsSandboxPath(t *testing.T) {
	tmpDir := t.TempDir()
	doc := tmpDir + "/report.pdf"
	content := []byte("fake pdf")
	if err := os.WriteFile(doc, content, 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}

	var uploadSeen bool
	var sendSeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/users/user-1/files":
			var payload uploadFilePayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "decode upload payload: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if payload.FileType != 4 {
				http.Error(w, "unexpected file_type", http.StatusInternalServerError)
				return
			}
			if payload.FileName != "report.pdf" {
				http.Error(w, "unexpected file_name "+payload.FileName, http.StatusInternalServerError)
				return
			}
			if payload.FileData != base64.StdEncoding.EncodeToString(content) {
				http.Error(w, "unexpected file_data", http.StatusInternalServerError)
				return
			}
			uploadSeen = true
			_, _ = w.Write([]byte(`{"file_info":"file-info-1"}`))
		case "/v2/users/user-1/messages":
			var payload outgoingMessagePayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, "decode send payload: "+err.Error(), http.StatusInternalServerError)
				return
			}
			if payload.MsgType != 7 || payload.Media == nil || payload.Media.FileInfo != "file-info-1" {
				http.Error(w, "unexpected rich media payload", http.StatusInternalServerError)
				return
			}
			sendSeen = true
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	a := NewAdapter(Config{
		AppID:      "app-id",
		AppSecret:  "app-secret",
		APIBaseURL: server.URL,
	})
	a.accessToken = "token"
	a.tokenExpiry = time.Now().Add(time.Hour)

	if err := a.SendDocument(context.Background(), "c2c:user-1", "msg-1", "sandbox:"+doc, ""); err != nil {
		t.Fatalf("SendDocument error = %v", err)
	}
	if !uploadSeen || !sendSeen {
		t.Fatalf("expected upload and send requests, upload=%t send=%t", uploadSeen, sendSeen)
	}
}

func TestBuildUserTurnInputPreservesAttachments(t *testing.T) {
	h := NewHandler(NewAdapter(DefaultConfig()), nil)
	input := h.buildUserTurnInput(context.Background(), "看一下附件", []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FilePath: "/tmp/example.jpg",
			FileName: "example.jpg",
			MimeType: "image/jpeg",
		},
	})

	if input.Message.Role != "user" {
		t.Fatalf("expected user role, got %q", input.Message.Role)
	}
	if len(input.Attachments) != 1 {
		t.Fatalf("expected attachment to be preserved, got %#v", input.Attachments)
	}
	if input.RoutingText == "" {
		t.Fatal("expected non-empty routing text")
	}
	if got := input.RoutingText; got == "看一下附件" {
		t.Fatalf("expected attachment description to be appended, got %q", got)
	}
	normalized := input.Normalize()
	if len(normalized.Message.ContentParts) != 2 {
		t.Fatalf("expected text plus image content parts, got %#v", normalized.Message.ContentParts)
	}
	if normalized.Message.ContentParts[1].Image == nil || normalized.Message.ContentParts[1].Image.FilePath != "/tmp/example.jpg" {
		t.Fatalf("expected image content part to keep file path, got %#v", normalized.Message.ContentParts[1])
	}
}

func TestNapcatSenderContextTextIncludesQQAndOriginalText(t *testing.T) {
	got := napcatSenderContextText("hello", gateway.User{
		ID:       "678",
		Username: "tester",
	})

	for _, want := range []string{
		"[NapCat message sender]",
		"QQ: 678",
		"Name: @tester",
		"hello",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in sender context, got %q", want, got)
		}
	}
}

func TestInputWithMessageScopeInjectsNapcatSenderQQ(t *testing.T) {
	h := NewHandlerWithOptions(&qqHandlerTestSender{}, nil, HandlerOptions{PlatformName: "napcat"})
	input := agent.MultimodalUserTurnInput("看一下附件", []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FilePath: "/tmp/example.jpg",
			MimeType: "image/jpeg",
		},
	})
	got := h.inputWithMessageScope(input, &gateway.Message{
		ID:   "msg-1",
		Chat: gateway.Chat{ID: "group:123", Type: gateway.ChatGroup},
		Sender: gateway.User{
			ID:       "3256247459",
			Username: "shiokou",
		},
	})

	if got.Scope.Platform != "napcat" || got.Scope.SenderID != "3256247459" {
		t.Fatalf("expected napcat scoped sender, got %#v", got.Scope)
	}
	for _, want := range []string{"QQ: 3256247459", "Name: @shiokou", "看一下附件"} {
		if !strings.Contains(got.RoutingText, want) {
			t.Fatalf("expected %q in routing text, got %q", want, got.RoutingText)
		}
		if !strings.Contains(got.Message.Content, want) {
			t.Fatalf("expected %q in message content, got %q", want, got.Message.Content)
		}
	}
	if len(got.Message.ContentParts) != 2 {
		t.Fatalf("expected text plus one image part, got %#v", got.Message.ContentParts)
	}
	if got.Message.ContentParts[0].Type != "text" || !strings.Contains(got.Message.ContentParts[0].Text, "QQ: 3256247459") {
		t.Fatalf("expected first content part to include sender context, got %#v", got.Message.ContentParts[0])
	}
	if got.Message.ContentParts[1].Image == nil || got.Message.ContentParts[1].Image.FilePath != "/tmp/example.jpg" {
		t.Fatalf("expected image content part to be preserved once, got %#v", got.Message.ContentParts[1])
	}
}

func TestInputWithMessageScopeDoesNotInjectQQOfficialSenderQQ(t *testing.T) {
	h := NewHandlerWithOptions(&qqHandlerTestSender{}, nil, HandlerOptions{PlatformName: "qqofficial"})
	got := h.inputWithMessageScope(agent.TextUserTurnInput("hello"), &gateway.Message{
		ID:   "msg-1",
		Chat: gateway.Chat{ID: "c2c:user-1", Type: gateway.ChatPrivate},
		Sender: gateway.User{
			ID:       "user-1",
			Username: "tester",
		},
	})

	if strings.Contains(got.RoutingText, "[NapCat message sender]") || strings.Contains(got.Message.Content, "[NapCat message sender]") {
		t.Fatalf("qqofficial input should not include napcat sender context, got %#v", got)
	}
	if got.RoutingText != "hello" || got.Message.Content != "hello" {
		t.Fatalf("expected original text to be unchanged, got routing=%q content=%q", got.RoutingText, got.Message.Content)
	}
}

func TestQQProgressHelpers(t *testing.T) {
	if got := qqThinkingMessage("Thinking... (round 2)", 2); got == "" {
		t.Fatal("expected non-empty thinking message")
	}
	if got := qqToolCallMessage("web_search", `{"query":"abc"}`); got == "" {
		t.Fatal("expected non-empty tool call message")
	}
	if got := qqToolResultMessage("web_search", "found results"); got == "" {
		t.Fatal("expected non-empty tool result message")
	}
}

func TestQQProgressTraceBuildsPublicSummary(t *testing.T) {
	trace := newQQProgressTrace()
	trace.AddThinking("Thinking... (round 1)", 1)
	trace.AddToolCall("web_search", `{"query":"abc"}`)
	trace.AddToolResult("web_search", strings.Repeat("result ", 80))

	got := trace.Message()
	if !strings.Contains(got, "本轮公开执行轨迹") {
		t.Fatalf("expected public trace title, got %q", got)
	}
	if !strings.Contains(got, "web_search") {
		t.Fatalf("expected tool name in trace, got %q", got)
	}
	if strings.Contains(got, "Thinking...") {
		t.Fatalf("trace should not expose raw thinking marker, got %q", got)
	}
	if !strings.Contains(got, "不包含模型隐藏推理") {
		t.Fatalf("expected hidden reasoning disclaimer, got %q", got)
	}
}

func TestHandlerFinalAnswerOnlySuppressesProgressMessages(t *testing.T) {
	sender := &qqHandlerTestSender{}
	handler := NewHandlerWithOptions(sender, nil, HandlerOptions{
		PlatformName:    "napcat",
		FinalAnswerOnly: true,
	})
	events := make(chan agent.ChatEvent, 5)
	events <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "Thinking... (round 1)"}
	events <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "web_search", Args: `{"query":"test"}`}
	events <- agent.ChatEvent{Type: agent.ChatEventToolResult, Name: "web_search", Result: "ok"}
	events <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "中间内容"}
	events <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "最终答案"}
	close(events)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	msg := &gateway.Message{
		ID:   "msg-1",
		Chat: gateway.Chat{ID: "group:123", Type: gateway.ChatGroup},
	}
	if err := handler.handleChatEventStream(ctx, msg, events); err != nil {
		t.Fatalf("handleChatEventStream() error = %v", err)
	}

	messages := sender.Messages()
	if len(messages) != 1 {
		t.Fatalf("expected exactly one outbound message, got %d: %#v", len(messages), messages)
	}
	if messages[0] != "最终答案" {
		t.Fatalf("expected final answer only, got %q", messages[0])
	}
}

func TestHandlerForwardsLongAssistantText(t *testing.T) {
	sender := &qqHandlerTestSender{}
	handler := NewHandlerWithOptions(sender, nil, HandlerOptions{
		PlatformName: "napcat",
		DisplayName:  "QQ Bot",
	})
	msg := &gateway.Message{
		ID:   "msg-1",
		Chat: gateway.Chat{ID: "group:123", Type: gateway.ChatGroup},
	}
	long := strings.Repeat("你", qqLongMessageForwardThreshold+1)

	if err := handler.sendAssistantResponse(context.Background(), msg, long); err != nil {
		t.Fatalf("sendAssistantResponse() error = %v", err)
	}
	if got := sender.Messages(); len(got) != 0 {
		t.Fatalf("expected long response to skip normal messages, got %#v", got)
	}
	forwards := sender.Forwards()
	if len(forwards) != 1 {
		t.Fatalf("expected one forwarded response, got %#v", forwards)
	}
	if forwards[0].ChatID != "group:123" || forwards[0].Title != "QQ Bot" {
		t.Fatalf("unexpected forward metadata: %#v", forwards[0])
	}
	if len(forwards[0].Chunks) != 2 {
		t.Fatalf("expected long text to be split into 2 nodes, got %#v", forwards[0].Chunks)
	}
	for _, chunk := range forwards[0].Chunks {
		if qqRuneLen(chunk) > qqForwardNodeChunkLimit {
			t.Fatalf("forward chunk exceeds limit: %d", qqRuneLen(chunk))
		}
	}
}

func TestHandlerWrapsMultiplePhotosInForwardedMedia(t *testing.T) {
	tmpDir := t.TempDir()
	img1 := tmpDir + "/one.png"
	img2 := tmpDir + "/two.jpg"
	if err := os.WriteFile(img1, []byte("one"), 0o600); err != nil {
		t.Fatalf("write first image: %v", err)
	}
	if err := os.WriteFile(img2, []byte("two"), 0o600); err != nil {
		t.Fatalf("write second image: %v", err)
	}

	sender := &qqHandlerTestSender{}
	handler := NewHandlerWithOptions(sender, nil, HandlerOptions{
		PlatformName: "napcat",
		DisplayName:  "QQ Bot",
	})
	msg := &gateway.Message{
		ID:   "msg-1",
		Chat: gateway.Chat{ID: "group:123", Type: gateway.ChatGroup},
	}
	response := "![第一张](" + img1 + ")\n![第二张](" + img2 + ")"

	if err := handler.sendAssistantResponse(context.Background(), msg, response); err != nil {
		t.Fatalf("sendAssistantResponse() error = %v", err)
	}
	if got := sender.Messages(); len(got) != 0 {
		t.Fatalf("expected forwarded media to avoid individual sends, got %#v", got)
	}
	forwards := sender.MediaForwards()
	if len(forwards) != 1 {
		t.Fatalf("expected one forwarded media payload, got %#v", forwards)
	}
	if forwards[0].ChatID != "group:123" || forwards[0].Title != "QQ Bot" {
		t.Fatalf("unexpected forward metadata: %#v", forwards[0])
	}
	if len(forwards[0].Items) != 2 {
		t.Fatalf("expected two forwarded media items, got %#v", forwards[0].Items)
	}
	if forwards[0].Items[0].Type != gateway.AttachmentImage || forwards[0].Items[0].Source != img1 || forwards[0].Items[0].Caption != "第一张" {
		t.Fatalf("unexpected first item: %#v", forwards[0].Items[0])
	}
	if forwards[0].Items[1].Type != gateway.AttachmentImage || forwards[0].Items[1].Source != img2 || forwards[0].Items[1].Caption != "第二张" {
		t.Fatalf("unexpected second item: %#v", forwards[0].Items[1])
	}
}

func TestHandlerAcknowledgesGroupMessagesOnly(t *testing.T) {
	sender := &qqHandlerTestSender{}
	handler := NewHandlerWithOptions(sender, nil, HandlerOptions{PlatformName: "napcat"})

	handler.acknowledgeIncomingMessage(&gateway.Message{
		ID:   "42",
		Chat: gateway.Chat{ID: "group:123", Type: gateway.ChatGroup},
	})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := sender.Acks(); len(got) == 1 {
			if got[0] != "group:123|42" {
				t.Fatalf("unexpected ack: %#v", got)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := sender.Acks(); len(got) != 1 {
		t.Fatalf("expected one group ack, got %#v", got)
	}

	handler.acknowledgeIncomingMessage(&gateway.Message{
		ID:   "43",
		Chat: gateway.Chat{ID: "private:3256247459", Type: gateway.ChatPrivate},
	})
	time.Sleep(50 * time.Millisecond)
	if got := sender.Acks(); len(got) != 1 {
		t.Fatalf("expected private message to skip ack, got %#v", got)
	}
}

func TestQQCommandNamesMatchTelegramCommandSet(t *testing.T) {
	expected := []string{
		"start", "help", "chat", "lucky",
		"review", "init", "config", "version", "model", "models", "soul", "tools", "skills", "mcp", "approve", "deny", "cron", "watch", "dashboard", "msg_gateway", "rag", "context", "fc", "embedder", "metrics", "health",
		"learn", "learn_start", "learn_current", "learn_lab", "learn_submit", "learn_progress",
		"remember", "remember_long", "recall", "memstats", "memdecay", "promote", "profile", "reset", "history", "session", "sessions", "resume", "rename", "new", "stop", "status", "restart",
	}
	got := qqCommandNames()
	if len(got) != len(expected) {
		t.Fatalf("expected %d commands, got %d: %#v", len(expected), len(got), got)
	}
	for i, want := range expected {
		if got[i] != want {
			t.Fatalf("command %d = %q, want %q", i, got[i], want)
		}
	}
}

func TestQQCommandRegistryCoversAllCommands(t *testing.T) {
	h := NewHandlerWithOptions(&qqHandlerTestSender{}, nil, HandlerOptions{PlatformName: "napcat"})
	for _, name := range qqCommandNames() {
		if h.commands[name] == nil {
			t.Fatalf("command %q is missing a handler", name)
		}
	}
}

func TestQQHelpIncludesAdvancedCommands(t *testing.T) {
	help := qqHelpMessage()
	for _, want := range []string{"/rag", "/sessions", "/remember", "/embedder", "/learn_start", "/msg_gateway"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help is missing %s:\n%s", want, help)
		}
	}
	for _, forbidden := range []string{"`", "**", "```"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help should be plain text, found %q in:\n%s", forbidden, help)
		}
	}
}

func TestQQProgressTraceOmitsTrivialThinkingOnlyTrace(t *testing.T) {
	trace := newQQProgressTrace()
	trace.AddThinking("Thinking... (round 1)", 1)

	if got := trace.Message(); got != "" {
		t.Fatalf("expected empty trace for trivial thinking-only turn, got %q", got)
	}
}

func TestSplitQQMessageChunks(t *testing.T) {
	long := strings.Repeat("你", qqProgressTraceChunkLimit+20)
	chunks := splitQQMessageChunks(long, qqProgressTraceChunkLimit)

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if qqRuneLen(chunk) > qqProgressTraceChunkLimit {
			t.Fatalf("chunk exceeds limit: %d", qqRuneLen(chunk))
		}
	}
}

func TestSendStreamReturnsSenderWhenRunning(t *testing.T) {
	a := NewAdapter(DefaultConfig())
	a.running = true

	sender, err := a.SendStream(context.Background(), "c2c:user-1", "msg-1")
	if err != nil {
		t.Fatalf("SendStream error = %v", err)
	}
	if sender == nil {
		t.Fatal("expected non-nil sender")
	}
	if sender.MessageID() != "msg-1" {
		t.Fatalf("expected message id msg-1, got %q", sender.MessageID())
	}
}

func TestResolveOutboundMediaResponse(t *testing.T) {
	tmpDir := t.TempDir()
	img := tmpDir + "/a.png"
	doc := tmpDir + "/b.pdf"
	if err := os.WriteFile(img, []byte("img"), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := os.WriteFile(doc, []byte("doc"), 0o644); err != nil {
		t.Fatalf("write doc: %v", err)
	}

	text, media, err := resolveOutboundMediaResponse("图片如下\nMEDIA:" + img + "\nMEDIA:" + doc)
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse error = %v", err)
	}
	if text != "图片如下" {
		t.Fatalf("unexpected text %q", text)
	}
	if len(media) != 2 {
		t.Fatalf("expected 2 media items, got %d", len(media))
	}
}

func TestResolveOutboundMediaResponseSandboxPath(t *testing.T) {
	tmpDir := t.TempDir()
	doc := tmpDir + "/report.pdf"
	if err := os.WriteFile(doc, []byte("doc"), 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}

	text, media, err := resolveOutboundMediaResponse("文件已生成\nMEDIA:sandbox:" + doc)
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse error = %v", err)
	}
	if text != "文件已生成" {
		t.Fatalf("unexpected text %q", text)
	}
	if len(media) != 1 || media[0].Source != "sandbox:"+doc || media[0].Kind != outboundMediaDocument {
		t.Fatalf("unexpected media %#v", media)
	}
}

func TestResolveOutboundMediaResponseMarkdownSandboxLink(t *testing.T) {
	tmpDir := t.TempDir()
	img := tmpDir + "/chart.png"
	if err := os.WriteFile(img, []byte("img"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	text, media, err := resolveOutboundMediaResponse("PNG 在这里：[chart](sandbox:" + img + ")")
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse error = %v", err)
	}
	if text != "PNG 在这里：[chart](sandbox:"+img+")" {
		t.Fatalf("unexpected text %q", text)
	}
	if len(media) != 0 {
		t.Fatalf("markdown sandbox link should not auto-send media, got %#v", media)
	}
}

func TestResolveOutboundMediaResponseBareLocalFile(t *testing.T) {
	tmpDir := t.TempDir()
	doc := tmpDir + "/result.pdf"
	if err := os.WriteFile(doc, []byte("doc"), 0o600); err != nil {
		t.Fatalf("write document: %v", err)
	}

	text, media, err := resolveOutboundMediaResponse("文件在这里 " + doc)
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse error = %v", err)
	}
	if text != "文件在这里 "+doc {
		t.Fatalf("unexpected text %q", text)
	}
	if len(media) != 0 {
		t.Fatalf("bare local file should not auto-send media, got %#v", media)
	}
}

func TestResolveOutboundMediaResponseDoesNotSendConfigJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"api_key":"secret"}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	text, media, err := resolveOutboundMediaResponse("配置在这里 " + configPath)
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse error = %v", err)
	}
	if text != "配置在这里 "+configPath {
		t.Fatalf("unexpected text %q", text)
	}
	if len(media) != 0 {
		t.Fatalf("config.json should not be sent as media, got %#v", media)
	}

	text, media, err = resolveOutboundMediaResponse("MEDIA:" + configPath)
	if err != nil {
		t.Fatalf("resolveOutboundMediaResponse MEDIA config error = %v", err)
	}
	if text != "MEDIA:"+configPath {
		t.Fatalf("expected sensitive MEDIA tag to remain text, got %q", text)
	}
	if len(media) != 0 {
		t.Fatalf("explicit config.json MEDIA tag should be blocked, got %#v", media)
	}
}

func TestQQMediaDeliveryGuidance(t *testing.T) {
	got := qqMediaDeliveryGuidance("生成报告")
	if !strings.Contains(got, "MEDIA:/absolute/path/to/file.ext") {
		t.Fatalf("guidance should mention MEDIA path, got %q", got)
	}
	again := qqMediaDeliveryGuidance(got)
	if strings.Count(again, "[QQ delivery rule]") != 1 {
		t.Fatalf("guidance should not be duplicated, got %q", again)
	}
}
