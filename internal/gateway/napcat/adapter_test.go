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
	"github.com/yurika0211/luckyagent/internal/gateway"
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

func TestAdapterDownloadsIncomingDocumentFileSegment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF fake"))
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
			{"type": "text", "data": map[string]any{"text": " 看文档"}},
			{"type": "file", "data": map[string]any{"file_id": "file-1", "name": "document.pdf", "url": server.URL + "/document.pdf", "size": 9}},
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
		if att.Type != gateway.AttachmentDocument || att.FileName != "document.pdf" || att.FilePath == "" {
			t.Fatalf("expected downloaded document attachment, got %#v", att)
		}
		data, err := os.ReadFile(att.FilePath)
		if err != nil {
			t.Fatalf("read downloaded document: %v", err)
		}
		if string(data) != "%PDF fake" {
			t.Fatalf("unexpected downloaded data: %q", string(data))
		}
		if att.MimeType != "application/pdf" {
			t.Fatalf("expected application/pdf mime, got %q", att.MimeType)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestAdapterDownloadsWhenIncomingLocalPathIsInaccessible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF remote"))
	}))
	defer server.Close()

	adapter := NewAdapter(Config{})
	parsed := parseOneBotMessage(mustRawMessage(t, []map[string]any{
		{"type": "file", "data": map[string]any{
			"file_id":   "file-1",
			"path":      filepath.Join(t.TempDir(), "missing.pdf"),
			"name":      "document.pdf",
			"url":       server.URL + "/document.pdf",
			"mime_type": "application/pdf",
		}},
	}), "")

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %#v", parsed.Attachments)
	}
	adapter.populateAttachments(parsed.Attachments)
	att := parsed.Attachments[0]
	if att.FilePath == "" || strings.Contains(att.FilePath, "missing.pdf") {
		t.Fatalf("expected downloaded replacement path, got %#v", att)
	}
	data, err := os.ReadFile(att.FilePath)
	if err != nil {
		t.Fatalf("read downloaded replacement: %v", err)
	}
	if string(data) != "%PDF remote" {
		t.Fatalf("unexpected downloaded data: %q", string(data))
	}
}

func TestParseOneBotFileSegmentWithLocalPath(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "document.pdf")
	if err := os.WriteFile(tmp, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write local document: %v", err)
	}

	parsed := parseOneBotMessage(mustRawMessage(t, []map[string]any{
		{"type": "file", "data": map[string]any{"file": tmp, "file_name": "document.pdf"}},
	}), "")

	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected one attachment, got %#v", parsed.Attachments)
	}
	att := parsed.Attachments[0]
	if att.Type != gateway.AttachmentDocument || att.FilePath != tmp || att.FileName != "document.pdf" {
		t.Fatalf("unexpected document attachment: %#v", att)
	}
}

func TestAdapterResolvesIncomingFileSegmentWithGetFile(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "document.pdf")
	if err := os.WriteFile(tmp, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write local document: %v", err)
	}

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
			{"type": "text", "data": map[string]any{"text": " 看文档"}},
			{"type": "file", "data": map[string]any{"file_id": "file-1", "name": "document.pdf", "size": 3}},
		},
		"sender": map[string]any{
			"user_id":  678,
			"nickname": "tester",
		},
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read get_file action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode get_file action: %v", err)
	}
	if req.Action != "get_file" {
		t.Fatalf("expected get_file action, got %s", req.Action)
	}
	if req.Params["file_id"] != "file-1" {
		t.Fatalf("unexpected file id: %#v", req.Params["file_id"])
	}
	if err := conn.WriteJSON(map[string]any{
		"status":  "ok",
		"retcode": 0,
		"echo":    req.Echo,
		"data": map[string]any{
			"file":      tmp,
			"file_name": "document.pdf",
		},
	}); err != nil {
		t.Fatalf("write get_file response: %v", err)
	}

	select {
	case msg := <-got:
		if len(msg.Attachments) != 1 {
			t.Fatalf("expected one attachment, got %#v", msg.Attachments)
		}
		att := msg.Attachments[0]
		if att.Type != gateway.AttachmentDocument || att.FilePath != tmp {
			t.Fatalf("expected resolved document path, got %#v", att)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestAdapterFallsBackToGetFileURLWhenLocalPathIsInaccessible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF remote from get_file"))
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
			{"type": "text", "data": map[string]any{"text": " 看文档"}},
			{"type": "file", "data": map[string]any{"file_id": "file-1", "name": "document.pdf", "size": 25}},
		},
		"sender": map[string]any{
			"user_id":  678,
			"nickname": "tester",
		},
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read get_file action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode get_file action: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"status":  "ok",
		"retcode": 0,
		"echo":    req.Echo,
		"data": map[string]any{
			"file":      filepath.Join(t.TempDir(), "missing.pdf"),
			"file_name": "document.pdf",
			"url":       server.URL + "/document.pdf",
		},
	}); err != nil {
		t.Fatalf("write get_file response: %v", err)
	}

	select {
	case msg := <-got:
		if len(msg.Attachments) != 1 {
			t.Fatalf("expected one attachment, got %#v", msg.Attachments)
		}
		att := msg.Attachments[0]
		if att.Type != gateway.AttachmentDocument || att.FilePath == "" {
			t.Fatalf("expected downloaded document path, got %#v", att)
		}
		data, err := os.ReadFile(att.FilePath)
		if err != nil {
			t.Fatalf("read downloaded document: %v", err)
		}
		if string(data) != "%PDF remote from get_file" {
			t.Fatalf("unexpected downloaded data: %q", string(data))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestAdapterResolvesGroupFileURLWhenGetFileFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF remote from group file url"))
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
			{"type": "file", "data": map[string]any{
				"file_id": "file-1",
				"name":    "document.pdf",
				"busid":   102,
				"size":    31,
			}},
		},
		"sender": map[string]any{
			"user_id":  678,
			"nickname": "tester",
		},
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read get_file action: %v", err)
	}
	var getFileReq actionRequest
	if err := json.Unmarshal(data, &getFileReq); err != nil {
		t.Fatalf("decode get_file action: %v", err)
	}
	if getFileReq.Action != "get_file" {
		t.Fatalf("expected get_file action, got %s", getFileReq.Action)
	}
	if err := conn.WriteJSON(map[string]any{
		"status":  "failed",
		"retcode": 100,
		"echo":    getFileReq.Echo,
		"message": "file not found by get_file",
	}); err != nil {
		t.Fatalf("write get_file failure: %v", err)
	}

	_, data, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read get_group_file_url action: %v", err)
	}
	var groupFileReq actionRequest
	if err := json.Unmarshal(data, &groupFileReq); err != nil {
		t.Fatalf("decode get_group_file_url action: %v", err)
	}
	if groupFileReq.Action != "get_group_file_url" {
		t.Fatalf("expected get_group_file_url action, got %s", groupFileReq.Action)
	}
	if groupFileReq.Params["file_id"] != "file-1" {
		t.Fatalf("unexpected file id: %#v", groupFileReq.Params["file_id"])
	}
	if groupFileReq.Params["group_id"] != float64(345) && groupFileReq.Params["group_id"] != int64(345) {
		t.Fatalf("unexpected group id: %#v", groupFileReq.Params["group_id"])
	}
	if groupFileReq.Params["busid"] != float64(102) && groupFileReq.Params["busid"] != int64(102) {
		t.Fatalf("unexpected busid: %#v", groupFileReq.Params["busid"])
	}
	if err := conn.WriteJSON(map[string]any{
		"status":  "ok",
		"retcode": 0,
		"echo":    groupFileReq.Echo,
		"data": map[string]any{
			"url": server.URL + "/document.pdf",
		},
	}); err != nil {
		t.Fatalf("write get_group_file_url response: %v", err)
	}

	select {
	case msg := <-got:
		if len(msg.Attachments) != 1 {
			t.Fatalf("expected one attachment, got %#v", msg.Attachments)
		}
		att := msg.Attachments[0]
		if att.FilePath == "" {
			t.Fatalf("expected downloaded group file path, got %#v", att)
		}
		data, err := os.ReadFile(att.FilePath)
		if err != nil {
			t.Fatalf("read downloaded group file: %v", err)
		}
		if string(data) != "%PDF remote from group file url" {
			t.Fatalf("unexpected downloaded data: %q", string(data))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

func TestAdapterCachesGetFileBase64Attachment(t *testing.T) {
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
			{"type": "file", "data": map[string]any{"file_id": "file-1", "name": "document.pdf"}},
		},
		"sender": map[string]any{
			"user_id":  678,
			"nickname": "tester",
		},
	}); err != nil {
		t.Fatalf("write event: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read get_file action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode get_file action: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"status":  "ok",
		"retcode": 0,
		"echo":    req.Echo,
		"data": map[string]any{
			"file_name": "document.pdf",
			"base64":    base64.StdEncoding.EncodeToString([]byte("%PDF from base64")),
		},
	}); err != nil {
		t.Fatalf("write get_file response: %v", err)
	}

	select {
	case msg := <-got:
		if len(msg.Attachments) != 1 {
			t.Fatalf("expected one attachment, got %#v", msg.Attachments)
		}
		att := msg.Attachments[0]
		if att.FilePath == "" || len(att.Data) == 0 {
			t.Fatalf("expected cached base64 attachment, got %#v", att)
		}
		data, err := os.ReadFile(att.FilePath)
		if err != nil {
			t.Fatalf("read cached base64 document: %v", err)
		}
		if string(data) != "%PDF from base64" {
			t.Fatalf("unexpected cached data: %q", string(data))
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

func TestAdapterSetTypingWritesInputStatusAction(t *testing.T) {
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

	if err := adapter.SetTyping(context.Background(), "private:3256247459", ""); err != nil {
		t.Fatalf("set typing: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if req.Action != "set_input_status" {
		t.Fatalf("unexpected action: %s", req.Action)
	}
	if req.Params["user_id"] != "3256247459" {
		t.Fatalf("unexpected user id: %#v", req.Params["user_id"])
	}
	if req.Params["event_type"].(float64) != 1 {
		t.Fatalf("unexpected event type: %#v", req.Params["event_type"])
	}
}

func TestAdapterAcknowledgeMessageWritesEmojiLikeAction(t *testing.T) {
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

	if err := adapter.AcknowledgeMessage(context.Background(), "group:345", "42"); err != nil {
		t.Fatalf("acknowledge message: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if req.Action != "set_msg_emoji_like" {
		t.Fatalf("unexpected action: %s", req.Action)
	}
	if req.Params["message_id"] != "42" {
		t.Fatalf("unexpected message id: %#v", req.Params["message_id"])
	}
	if req.Params["emoji_id"] != defaultNapCatAckEmojiID {
		t.Fatalf("unexpected emoji id: %#v", req.Params["emoji_id"])
	}
	if req.Params["set"] != true {
		t.Fatalf("unexpected set value: %#v", req.Params["set"])
	}
}

func TestAdapterSendForwardedTextWritesForwardAction(t *testing.T) {
	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	adapter.selfID = "10001"
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	if err := adapter.SendForwardedText(context.Background(), "group:345", "LuckyAgent", []string{"hello [qq]", "world"}); err != nil {
		t.Fatalf("send forwarded text: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if req.Action != "send_group_forward_msg" {
		t.Fatalf("unexpected action: %s", req.Action)
	}
	if req.Params["group_id"].(float64) != 345 {
		t.Fatalf("unexpected group id: %#v", req.Params["group_id"])
	}
	messages, ok := req.Params["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("unexpected messages payload: %#v", req.Params["messages"])
	}
	node, ok := messages[0].(map[string]any)
	if !ok || node["type"] != "node" {
		t.Fatalf("unexpected node payload: %#v", messages[0])
	}
	nodeData, ok := node["data"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected node data: %#v", node["data"])
	}
	if nodeData["name"] != "LuckyAgent" || nodeData["uin"] != "10001" {
		t.Fatalf("unexpected node identity: %#v", nodeData)
	}
	content, ok := nodeData["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("unexpected node content: %#v", nodeData["content"])
	}
	textSegment, ok := content[0].(map[string]any)
	if !ok || textSegment["type"] != "text" {
		t.Fatalf("unexpected text segment: %#v", content[0])
	}
	textData, ok := textSegment["data"].(map[string]any)
	if !ok || textData["text"] != "hello [qq]" {
		t.Fatalf("unexpected text data: %#v", textSegment["data"])
	}
}

func TestAdapterSendForwardedMediaWritesForwardAction(t *testing.T) {
	adapter := NewAdapter(Config{ListenAddr: "127.0.0.1:0", Path: "/ws"})
	adapter.selfID = "10001"
	if err := adapter.Start(context.Background()); err != nil {
		t.Fatalf("start adapter: %v", err)
	}
	defer adapter.Stop()

	conn, _, err := websocket.DefaultDialer.Dial("ws://"+adapter.ListenAddr()+"/ws", nil)
	if err != nil {
		t.Fatalf("dial reverse websocket: %v", err)
	}
	defer conn.Close()

	tmpDir := t.TempDir()
	img1 := filepath.Join(tmpDir, "one.png")
	img2 := filepath.Join(tmpDir, "two.jpg")
	if err := os.WriteFile(img1, []byte("fake image one"), 0o600); err != nil {
		t.Fatalf("write first image: %v", err)
	}
	if err := os.WriteFile(img2, []byte("fake image two"), 0o600); err != nil {
		t.Fatalf("write second image: %v", err)
	}

	if err := adapter.SendForwardedMedia(context.Background(), "group:345", "LuckyAgent", []gateway.ForwardedMediaItem{
		{Type: gateway.AttachmentImage, Source: img1, Caption: "one"},
		{Type: gateway.AttachmentImage, Source: img2, Caption: "two"},
	}); err != nil {
		t.Fatalf("send forwarded media: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read action: %v", err)
	}
	var req actionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if req.Action != "send_group_forward_msg" {
		t.Fatalf("unexpected action: %s", req.Action)
	}
	if req.Params["group_id"].(float64) != 345 {
		t.Fatalf("unexpected group id: %#v", req.Params["group_id"])
	}
	messages, ok := req.Params["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("unexpected messages payload: %#v", req.Params["messages"])
	}
	node, ok := messages[0].(map[string]any)
	if !ok || node["type"] != "node" {
		t.Fatalf("unexpected node payload: %#v", messages[0])
	}
	nodeData, ok := node["data"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected node data: %#v", node["data"])
	}
	if nodeData["name"] != "LuckyAgent" || nodeData["uin"] != "10001" {
		t.Fatalf("unexpected node identity: %#v", nodeData)
	}
	content, ok := nodeData["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatalf("unexpected node content: %#v", nodeData["content"])
	}
	textSegment, ok := content[0].(map[string]any)
	if !ok || textSegment["type"] != "text" {
		t.Fatalf("unexpected text segment: %#v", content[0])
	}
	textData, ok := textSegment["data"].(map[string]any)
	if !ok || textData["text"] != "one" {
		t.Fatalf("unexpected text data: %#v", textSegment["data"])
	}
	imageSegment, ok := content[1].(map[string]any)
	if !ok || imageSegment["type"] != "image" {
		t.Fatalf("unexpected image segment: %#v", content[1])
	}
	imageData, ok := imageSegment["data"].(map[string]any)
	wantImage := "base64://" + base64.StdEncoding.EncodeToString([]byte("fake image one"))
	if !ok || imageData["file"] != wantImage {
		t.Fatalf("unexpected image data: %#v", imageSegment["data"])
	}
	if strings.Contains(string(data), img1) || strings.Contains(string(data), img2) {
		t.Fatalf("forwarded media payload should not expose local host paths: %s", string(data))
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

func mustRawMessage(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal raw message: %v", err)
	}
	return data
}
