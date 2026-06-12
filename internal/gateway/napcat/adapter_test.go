package napcat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

func TestParseOneBotCQMessage(t *testing.T) {
	parsed := parseOneBotMessage(json.RawMessage(`"[CQ:reply,id=12][CQ:at,qq=10001] 看图 [CQ:image,file=abc.jpg,url=http://example.test/a.jpg]"`), "")
	if parsed.Text != "看图" {
		t.Fatalf("unexpected text: %q", parsed.Text)
	}
	if parsed.ReplyID != "12" {
		t.Fatalf("unexpected reply id: %q", parsed.ReplyID)
	}
	if len(parsed.AtQQs) != 1 || parsed.AtQQs[0] != "10001" {
		t.Fatalf("unexpected at list: %#v", parsed.AtQQs)
	}
	if len(parsed.Attachments) != 1 || parsed.Attachments[0].Type != gateway.AttachmentImage {
		t.Fatalf("unexpected attachments: %#v", parsed.Attachments)
	}
}

func TestAdapterReceivesGroupMention(t *testing.T) {
	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	got := make(chan *gateway.Message, 1)
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		got <- msg
		return nil
	})

	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"time":         time.Now().Unix(),
		"self_id":      10001,
		"post_type":    "message",
		"message_type": "group",
		"message_id":   12,
		"group_id":     345,
		"user_id":      678,
		"message":      "[CQ:at,qq=10001] /help now",
		"raw_message":  "[CQ:at,qq=10001] /help now",
		"sender": map[string]any{
			"user_id":  678,
			"nickname": "tester",
		},
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}

	select {
	case msg := <-got:
		if msg.Chat.ID != "group:345" || msg.Chat.Type != gateway.ChatGroup {
			t.Fatalf("unexpected chat: %#v", msg.Chat)
		}
		if msg.Text != "/help now" || !msg.IsCommand || msg.Command != "/help" || msg.Args != "now" {
			t.Fatalf("unexpected command message: %#v", msg)
		}
		if !msg.IsGroupTrigger || msg.TriggerType != "mention" {
			t.Fatalf("expected mention trigger, got %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestAdapterIgnoresMentionOfOtherUser(t *testing.T) {
	adapter := NewAdapter(Config{})
	msg := adapter.convertEvent([]byte(`{
		"time": 123,
		"self_id": 10001,
		"post_type": "message",
		"message_type": "group",
		"message_id": 12,
		"group_id": 345,
		"user_id": 678,
		"message": "[CQ:at,qq=20002] hello",
		"raw_message": "[CQ:at,qq=20002] hello",
		"sender": {"user_id": 678, "nickname": "tester"}
	}`))
	if msg != nil {
		t.Fatalf("expected mention of another user to be ignored, got %#v", msg)
	}
}

func TestAdapterIgnoresReplyMentionOfOtherUser(t *testing.T) {
	adapter := NewAdapter(Config{})
	msg := adapter.convertEvent([]byte(`{
		"time": 123,
		"self_id": 10001,
		"post_type": "message",
		"message_type": "group",
		"message_id": 12,
		"group_id": 345,
		"user_id": 678,
		"message": "[CQ:reply,id=99][CQ:at,qq=20002] hello",
		"raw_message": "[CQ:reply,id=99][CQ:at,qq=20002] hello",
		"sender": {"user_id": 678, "nickname": "tester"}
	}`))
	if msg != nil {
		t.Fatalf("expected reply mention of another user to be ignored, got %#v", msg)
	}
}

func TestAdapterReceivesBareGroupReply(t *testing.T) {
	adapter := NewAdapter(Config{})
	msg := adapter.convertEvent([]byte(`{
		"time": 123,
		"self_id": 10001,
		"post_type": "message",
		"message_type": "group",
		"message_id": 12,
		"group_id": 345,
		"user_id": 678,
		"message": "[CQ:reply,id=99] hello",
		"raw_message": "[CQ:reply,id=99] hello",
		"sender": {"user_id": 678, "nickname": "tester"}
	}`))
	if msg == nil {
		t.Fatal("expected bare group reply to trigger")
	}
	if !msg.IsGroupTrigger || msg.TriggerType != "reply" {
		t.Fatalf("expected reply trigger, got %#v", msg)
	}
}

func TestAdapterDownloadsIncomingAttachment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fake png data"))
	}))
	defer server.Close()

	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	got := make(chan *gateway.Message, 1)
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		got <- msg
		return nil
	})

	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{
		"time":         time.Now().Unix(),
		"self_id":      10001,
		"post_type":    "message",
		"message_type": "group",
		"message_id":   12,
		"group_id":     345,
		"user_id":      678,
		"message": []map[string]any{
			{"type": "at", "data": map[string]any{"qq": "10001"}},
			{"type": "text", "data": map[string]any{"text": " 看图"}},
			{"type": "image", "data": map[string]any{"file": "photo.png", "url": server.URL + "/photo.png", "size": 13}},
		},
		"sender": map[string]any{
			"user_id":  678,
			"nickname": "tester",
		},
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}

	select {
	case msg := <-got:
		if len(msg.Attachments) != 1 {
			t.Fatalf("expected one attachment, got %#v", msg.Attachments)
		}
		att := msg.Attachments[0]
		if att.Type != gateway.AttachmentImage || att.FilePath == "" {
			t.Fatalf("expected downloaded image attachment, got %#v", att)
		}
		data, err := os.ReadFile(att.FilePath)
		if err != nil {
			t.Fatalf("read downloaded file: %v", err)
		}
		if string(data) != "fake png data" {
			t.Fatalf("unexpected downloaded data: %q", string(data))
		}
		if att.MimeType != "image/png" {
			t.Fatalf("expected image/png mime, got %q", att.MimeType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestAdapterSendWithReplyWritesOneBotAction(t *testing.T) {
	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	if err := adapter.SendWithReply(context.Background(), "group:345", "12", "hello [qq]"); err != nil {
		t.Fatalf("send with reply: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if req.Action != "send_group_msg" {
		t.Fatalf("unexpected action: %s", req.Action)
	}
	if req.Params["group_id"].(float64) != 345 {
		t.Fatalf("unexpected group id: %#v", req.Params["group_id"])
	}
	message, _ := req.Params["message"].(string)
	if !strings.HasPrefix(message, "[CQ:reply,id=12]") || !strings.Contains(message, "hello &#91;qq&#93;") {
		t.Fatalf("unexpected message payload: %q", message)
	}
}

func TestAdapterSendPhotoLocalFileUsesBase64CQSource(t *testing.T) {
	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	img := filepath.Join(t.TempDir(), "photo.png")
	if err := os.WriteFile(img, []byte("fake image"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	if err := adapter.SendPhoto(context.Background(), "group:345", "12", img, "caption"); err != nil {
		t.Fatalf("send photo: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	message, _ := req.Params["message"].(string)
	want := "base64://" + base64.StdEncoding.EncodeToString([]byte("fake image"))
	if !strings.Contains(message, "[CQ:image,file="+want+"]") {
		t.Fatalf("expected base64 image CQ source, got %q", message)
	}
	if strings.Contains(message, img) {
		t.Fatalf("message should not expose local host path: %q", message)
	}
}

func TestAdapterSendDocumentLocalFileUsesBase64UploadSource(t *testing.T) {
	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	doc := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(doc, []byte("hello report"), 0o600); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	if err := adapter.SendDocument(context.Background(), "group:345", "", doc, ""); err != nil {
		t.Fatalf("send document: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if req.Action != "upload_group_file" {
		t.Fatalf("unexpected action: %s", req.Action)
	}
	want := "base64://" + base64.StdEncoding.EncodeToString([]byte("hello report"))
	if req.Params["file"] != want {
		t.Fatalf("unexpected file source: %#v", req.Params["file"])
	}
	if req.Params["name"] != "report.txt" {
		t.Fatalf("unexpected upload name: %#v", req.Params["name"])
	}
}
