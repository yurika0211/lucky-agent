package qqofficial

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/gateway"
)

type qqHandlerTestSender struct {
	mu       sync.Mutex
	messages []string
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

func (s *qqHandlerTestSender) Messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.messages...)
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
