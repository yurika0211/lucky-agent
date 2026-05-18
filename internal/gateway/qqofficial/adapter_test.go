package qqofficial

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

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

func TestBuildUserTurnInputRoutesAttachmentsThroughTextPath(t *testing.T) {
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
	if len(input.Message.ContentParts) != 0 {
		t.Fatalf("expected no content parts, got %#v", input.Message.ContentParts)
	}
	if input.RoutingText == "" {
		t.Fatal("expected non-empty routing text")
	}
	if got := input.RoutingText; got == "看一下附件" {
		t.Fatalf("expected attachment description to be appended, got %q", got)
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
