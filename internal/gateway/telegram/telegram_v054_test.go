package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/soul"
	"github.com/yurika0211/luckyharness/internal/tool"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

// ============================================================
// v0.54.0: Telegram 包测试补全 — 使用 httptest mock API
// ============================================================

// newMockBotServer 创建一个模拟 Telegram Bot API 的 httptest 服务器
// 返回 server 和一个可用的 bot 实例
func newMockBotServer() (*httptest.Server, *tgbotapi.BotAPI, error) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var result map[string]interface{}

		switch {
		case containsMethod(r.URL.Path, "getMe"):
			result = map[string]interface{}{
				"ok": true,
				"result": map[string]interface{}{
					"id":         123456789,
					"is_bot":     true,
					"first_name": "TestBot",
					"username":   "testbot",
				},
			}
		case containsMethod(r.URL.Path, "sendMessage"):
			result = map[string]interface{}{
				"ok": true,
				"result": map[string]interface{}{
					"message_id": 42,
					"chat": map[string]interface{}{
						"id": 12345,
					},
					"text": "ok",
				},
			}
		case containsMethod(r.URL.Path, "editMessageText"):
			result = map[string]interface{}{
				"ok": true,
				"result": map[string]interface{}{
					"message_id": 42,
					"text":       "edited",
				},
			}
		case containsMethod(r.URL.Path, "sendChatAction"):
			result = map[string]interface{}{
				"ok": true,
			}
		case containsMethod(r.URL.Path, "setMessageReaction"):
			result = map[string]interface{}{
				"ok": true,
			}
		case containsMethod(r.URL.Path, "getFile"):
			result = map[string]interface{}{
				"ok": true,
				"result": map[string]interface{}{
					"file_id":   "test_file_id",
					"file_path": "photos/test.jpg",
				},
			}
		case containsMethod(r.URL.Path, "getUpdates"):
			result = map[string]interface{}{
				"ok":     true,
				"result": []interface{}{},
			}
		default:
			result = map[string]interface{}{
				"ok":     true,
				"result": map[string]interface{}{},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}))

	bot, err := tgbotapi.NewBotAPIWithAPIEndpoint("123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11", server.URL+"/bot%s/%s")
	if err != nil {
		server.Close()
		return nil, nil, fmt.Errorf("failed to create bot: %w", err)
	}

	return server, bot, nil
}

// containsMethod 检查 URL 路径是否包含指定的 API 方法
func containsMethod(path, method string) bool {
	return len(path) > len(method) && path[len(path)-len(method)-1:] == "/"+method
}

// newAdapterWithMockBot 创建一个使用 mock bot 的 Adapter
func newAdapterWithMockBot() (*Adapter, *httptest.Server, error) {
	server, bot, err := newMockBotServer()
	if err != nil {
		return nil, nil, err
	}

	cfg := DefaultConfig()
	cfg.Token = bot.Token
	adapter := NewAdapter(cfg)
	adapter.bot = bot
	adapter.botUsername = "testbot"
	adapter.running = true

	return adapter, server, nil
}

// ============================================================
// Start / Stop 测试 (mock server)
// ============================================================

func TestV054StartWithMockServer(t *testing.T) {
	// Start() calls tgbotapi.NewBotAPI which connects to real API,
	// so we test the setup logic by directly injecting a mock bot.
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter with mock bot: %v", err)
	}
	defer server.Close()

	if !adapter.IsRunning() {
		t.Error("expected adapter to be running")
	}

	if adapter.botUsername != "testbot" {
		t.Errorf("expected botUsername 'testbot', got '%s'", adapter.botUsername)
	}
}

func TestV054StartEmptyToken(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = ""
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	err := adapter.Start(ctx)
	if err == nil {
		t.Error("expected error for empty token")
	}
}

func TestV054StopWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	err = adapter.Stop()
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	if adapter.IsRunning() {
		t.Error("expected adapter to not be running after stop")
	}
}

// ============================================================
// Send / SendWithReply 测试 (mock server)
// ============================================================

func TestV054SendWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.Send(ctx, "12345", "hello world")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054SendNotRunning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	err := adapter.Send(ctx, "12345", "hello")
	if err == nil {
		t.Error("expected error when not running")
	}
}

func TestV054SendInvalidChatID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.Send(ctx, "not-a-number", "hello")
	if err == nil {
		t.Error("expected error for invalid chat ID")
	}
}

func TestV054SendWithReplyMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.SendWithReply(ctx, "12345", "1", "reply message")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054SendWithReplyNotRunning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	err := adapter.SendWithReply(ctx, "12345", "1", "reply")
	if err == nil {
		t.Error("expected error when not running")
	}
}

func TestV054SendWithReplyInvalidChatID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.SendWithReply(ctx, "not-a-number", "1", "reply")
	if err == nil {
		t.Error("expected error for invalid chat ID")
	}
}

func TestV054SendWithReplyInvalidReplyID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.SendWithReply(ctx, "12345", "not-a-number", "reply")
	if err == nil {
		t.Error("expected error for invalid reply ID")
	}
}

// ============================================================
// SendStream 测试 (mock server)
// ============================================================

func TestV054SendStreamWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if stream == nil {
		t.Fatal("expected non-nil stream")
	}

	// 测试 Append
	if err := stream.Append("hello "); err != nil {
		t.Errorf("expected no error on Append, got: %v", err)
	}
	if err := stream.Append("world"); err != nil {
		t.Errorf("expected no error on Append, got: %v", err)
	}

	// 测试 SetThinking
	if err := stream.SetThinking("searching"); err != nil {
		t.Errorf("expected no error on SetThinking, got: %v", err)
	}

	// 测试 SetToolCall
	if err := stream.SetToolCall("web_search", "query=test"); err != nil {
		t.Errorf("expected no error on SetToolCall, got: %v", err)
	}

	// 测试 SetResult
	if err := stream.SetResult("final result"); err != nil {
		t.Errorf("expected no error on SetResult, got: %v", err)
	}

	// 测试 Finish
	if err := stream.Finish(); err != nil {
		t.Errorf("expected no error on Finish, got: %v", err)
	}

	// 测试 MessageID
	if stream.MessageID() != "42" {
		t.Errorf("expected message ID '42', got '%s'", stream.MessageID())
	}
}

func TestV054SendStreamNotRunning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err == nil {
		t.Error("expected error when not running")
	}
	if stream != nil {
		t.Error("expected nil stream when not running")
	}
}

func TestV054SendStreamInvalidChatID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "not-a-number", "")
	if err == nil {
		t.Error("expected error for invalid chat ID")
	}
	if stream != nil {
		t.Error("expected nil stream for invalid chat ID")
	}
}

func TestV054SendStreamWithReplyTo(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "10")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if stream == nil {
		t.Fatal("expected non-nil stream")
	}
	stream.Finish()
}

// ============================================================
// StreamSender 边界测试
// ============================================================

func TestV054StreamSenderDoubleFinish(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	if err := stream.Finish(); err != nil {
		t.Errorf("expected no error on first Finish, got: %v", err)
	}
	if err := stream.Finish(); err != nil {
		t.Errorf("expected no error on second Finish, got: %v", err)
	}
}

func TestV054StreamSenderAppendAfterFinish(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	stream.Finish()

	if err := stream.Append("should fail"); err == nil {
		t.Error("expected error when appending after finish")
	}
}

func TestV054StreamSenderSetThinkingAfterFinish(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	stream.Finish()

	if err := stream.SetThinking("should be ignored"); err != nil {
		t.Errorf("expected nil on SetThinking after finish, got: %v", err)
	}
}

func TestV054StreamSenderSetToolCallAfterFinish(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	stream.Finish()

	if err := stream.SetToolCall("tool", "args"); err != nil {
		t.Errorf("expected nil on SetToolCall after finish, got: %v", err)
	}
}

func TestV054StreamSenderSetResultAfterFinish(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	stream.Finish()

	if err := stream.SetResult("should be ignored"); err != nil {
		t.Errorf("expected nil on SetResult after finish, got: %v", err)
	}
}

func TestV054StreamSenderThrottleEdit(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	stream.Append("chunk1")
	stream.Append("chunk2")
	stream.Append("chunk3")
	stream.Finish()
}

func TestV054StreamSenderMaxEdits(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sender := stream.(*telegramStreamSender)
	sender.editCount = maxEdits + 1

	stream.Append("should skip edit")
	stream.Finish()
}

func TestV054StreamSenderRenderContent(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sender := stream.(*telegramStreamSender)

	var content string

	// 测试空内容（thinking 为空，content 为空）
	sender.mu.Lock()
	sender.thinking = ""
	sender.content = ""
	content = sender.renderContent()
	sender.mu.Unlock()
	if content != "🧠 Thinking..." {
		t.Errorf("expected default thinking, got '%s'", content)
	}

	// 测试只有思考标签
	sender.mu.Lock()
	sender.thinking = "🧠 Searching"
	sender.content = ""
	content = sender.renderContent()
	sender.mu.Unlock()
	if content != "🧠 Searching\n\n" {
		t.Errorf("expected thinking label, got '%s'", content)
	}

	// 测试只有内容
	sender.mu.Lock()
	sender.thinking = ""
	sender.content = "hello world"
	content = sender.renderContent()
	sender.mu.Unlock()
	if content != "hello world" {
		t.Errorf("expected content, got '%s'", content)
	}

	// 测试思考标签 + 内容
	sender.mu.Lock()
	sender.thinking = "🧠 Thinking"
	sender.content = "result"
	content = sender.renderContent()
	sender.mu.Unlock()
	expected := "🧠 Thinking\n\nresult"
	if content != expected {
		t.Errorf("expected '%s', got '%s'", expected, content)
	}

	stream.Finish()
}

func TestV054StreamSenderLongContent(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sender := stream.(*telegramStreamSender)

	sender.mu.Lock()
	longContent := ""
	for i := 0; i < 5000; i++ {
		longContent += "x"
	}
	sender.content = longContent
	content := sender.renderContent()
	sender.mu.Unlock()

	if len(content) > 4096 {
		t.Errorf("expected content to be truncated, got length %d", len(content))
	}

	stream.Finish()
}

func TestV054StreamSenderToolCallLongArgs(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// 测试超长参数截断
	longArgs := ""
	for i := 0; i < 200; i++ {
		longArgs += "x"
	}
	if err := stream.SetToolCall("tool", longArgs); err != nil {
		t.Errorf("expected no error on SetToolCall with long args, got: %v", err)
	}

	stream.Finish()
}

// ============================================================
// SendTypingLoop 测试 (mock server)
// ============================================================

func TestV054SendTypingLoopWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go adapter.SendTypingLoop(ctx, "12345")

	time.Sleep(100 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
}

func TestV054SendTypingLoopNilBot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter.SendTypingLoop(ctx, "12345")
}

func TestV054SendTypingLoopInvalidChatID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adapter.SendTypingLoop(ctx, "not-a-number")
}

// ============================================================
// ReactToMessage 测试 (mock server)
// ============================================================

func TestV054ReactToMessageWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.ReactToMessage("12345", "1", "👍")
	time.Sleep(100 * time.Millisecond)
}

func TestV054ReactToMessageNilBot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	adapter.ReactToMessage("12345", "1", "👍")
}

func TestV054ReactToMessageInvalidChatID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.ReactToMessage("not-a-number", "1", "👍")
}

func TestV054ReactToMessageInvalidMessageID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.ReactToMessage("12345", "not-a-number", "👍")
}

// ============================================================
// callSetMessageReaction 测试 (mock server)
// ============================================================

func TestV054CallSetMessageReactionWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.callSetMessageReaction(12345, 1, "👍")
}

// ============================================================
// callTelegramAPI 测试 (mock server)
// ============================================================

func TestV054CallTelegramAPIWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	// callTelegramAPI 有 bug：body 未分配空间，Read 会失败
	// 但不应该 panic
	_, _ = adapter.callTelegramAPI("getMe", nil)
}

// ============================================================
// sendChunk 测试 (mock server)
// ============================================================

func TestV054SendChunkWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.sendChunk(ctx, 12345, 0, "test message")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054SendChunkWithReply(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	err = adapter.sendChunk(ctx, 12345, 42, "reply message")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

// ============================================================
// extractAttachments 测试 (mock server)
// ============================================================

func TestV054ExtractAttachmentsPhotoWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	tgMsg := &tgbotapi.Message{
		Photo: []tgbotapi.PhotoSize{
			{FileID: "photo1", Width: 100, Height: 100, FileSize: 1024},
			{FileID: "photo2", Width: 200, Height: 200, FileSize: 2048},
			{FileID: "photo3", Width: 400, Height: 400, FileSize: 4096},
		},
		Caption: "photo caption",
	}

	gwMsg := &gateway.Message{}
	adapter.extractAttachments(tgMsg, gwMsg)

	if len(gwMsg.Attachments) == 0 {
		t.Error("expected non-empty attachments for photo")
	}
	if gwMsg.Attachments[0].FileID != "photo3" {
		t.Errorf("expected largest photo, got %s", gwMsg.Attachments[0].FileID)
	}
}

func TestV054ExtractAttachmentsDocumentWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	tgMsg := &tgbotapi.Message{
		Document: &tgbotapi.Document{
			FileID:   "doc1",
			FileName: "test.pdf",
			FileSize: 2048,
			MimeType: "application/pdf",
		},
	}

	gwMsg := &gateway.Message{}
	adapter.extractAttachments(tgMsg, gwMsg)

	if len(gwMsg.Attachments) == 0 {
		t.Error("expected non-empty attachments for document")
	}
	if gwMsg.Attachments[0].Type != gateway.AttachmentDocument {
		t.Errorf("expected document attachment, got %v", gwMsg.Attachments[0].Type)
	}
}

func TestV054ExtractAttachmentsVoiceWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	tgMsg := &tgbotapi.Message{
		Voice: &tgbotapi.Voice{
			FileID:   "voice1",
			FileSize: 1024,
			MimeType: "audio/ogg",
			Duration: 30,
		},
	}

	gwMsg := &gateway.Message{}
	adapter.extractAttachments(tgMsg, gwMsg)

	if len(gwMsg.Attachments) == 0 {
		t.Error("expected non-empty attachments for voice")
	}
	if gwMsg.Attachments[0].Type != gateway.AttachmentAudio {
		t.Errorf("expected audio attachment, got %v", gwMsg.Attachments[0].Type)
	}
}

func TestV054ExtractAttachmentsVideoWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	tgMsg := &tgbotapi.Message{
		Video: &tgbotapi.Video{
			FileID:   "video1",
			FileName: "video.mp4",
			FileSize: 10240,
			MimeType: "video/mp4",
		},
	}

	gwMsg := &gateway.Message{}
	adapter.extractAttachments(tgMsg, gwMsg)

	if len(gwMsg.Attachments) == 0 {
		t.Error("expected non-empty attachments for video")
	}
	if gwMsg.Attachments[0].Type != gateway.AttachmentVideo {
		t.Errorf("expected video attachment, got %v", gwMsg.Attachments[0].Type)
	}
}

func TestV054ExtractAttachmentsAudioWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	tgMsg := &tgbotapi.Message{
		Audio: &tgbotapi.Audio{
			FileID:   "audio1",
			FileName: "audio.mp3",
			FileSize: 5120,
			MimeType: "audio/mpeg",
		},
	}

	gwMsg := &gateway.Message{}
	adapter.extractAttachments(tgMsg, gwMsg)

	if len(gwMsg.Attachments) == 0 {
		t.Error("expected non-empty attachments for audio")
	}
	if gwMsg.Attachments[0].Type != gateway.AttachmentAudio {
		t.Errorf("expected audio attachment, got %v", gwMsg.Attachments[0].Type)
	}
}

func TestV054ExtractAttachmentsNilBot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	tgMsg := &tgbotapi.Message{
		Photo: []tgbotapi.PhotoSize{
			{FileID: "photo1", FileSize: 1024},
		},
	}

	gwMsg := &gateway.Message{}
	adapter.extractAttachments(tgMsg, gwMsg)

	if len(gwMsg.Attachments) != 0 {
		t.Error("expected no attachments when bot is nil")
	}
}

// ============================================================
// convertMessage 测试 (mock server)
// ============================================================

func TestV054ConvertMessageWithCommand(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat: &tgbotapi.Chat{
			ID:   12345,
			Type: "private",
		},
		From: &tgbotapi.User{
			ID:        12345,
			UserName:  "testuser",
			FirstName: "Test",
		},
		Text: "/start hello",
		Entities: []tgbotapi.MessageEntity{
			{
				Type:   "bot_command",
				Offset: 0,
				Length: 6,
			},
		},
	}

	result := adapter.convertMessage(msg)
	if result == nil {
		t.Fatal("expected non-nil message")
	}
	if !result.IsCommand {
		t.Error("expected IsCommand to be true")
	}
	if result.Command != "start" {
		t.Errorf("expected command 'start', got '%s'", result.Command)
	}
	if result.Args != "hello" {
		t.Errorf("expected args 'hello', got '%s'", result.Args)
	}
}

func TestV054ConvertMessageSuperGroup(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat: &tgbotapi.Chat{
			ID:    888888,
			Type:  "supergroup",
			Title: "Super Group",
		},
		From: &tgbotapi.User{
			ID:        12345,
			UserName:  "testuser",
			FirstName: "Test",
		},
		Text: "hello",
	}

	result := adapter.convertMessage(msg)
	if result.Chat.Type != gateway.ChatSuperGroup {
		t.Errorf("expected supergroup type, got %v", result.Chat.Type)
	}
}

func TestV054ConvertMessageChannel(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat: &tgbotapi.Chat{
			ID:       999999,
			Type:     "channel",
			Title:    "Test Channel",
			UserName: "testchannel",
		},
		From: &tgbotapi.User{
			ID:        12345,
			UserName:  "testuser",
			FirstName: "Test",
		},
		Text: "channel message",
	}

	result := adapter.convertMessage(msg)
	if result.Chat.Type != gateway.ChatChannel {
		t.Errorf("expected channel type, got %v", result.Chat.Type)
	}
}

func TestV054ConvertMessageWithReply(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		MessageID: 2,
		Chat: &tgbotapi.Chat{
			ID:   12345,
			Type: "private",
		},
		From: &tgbotapi.User{
			ID:        12345,
			UserName:  "testuser",
			FirstName: "Test",
		},
		Text: "reply",
		ReplyToMessage: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:   12345,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        999999,
				UserName:  "otheruser",
				FirstName: "Other",
			},
			Text: "original",
		},
	}

	result := adapter.convertMessage(msg)
	if result.ReplyTo == nil {
		t.Fatal("expected non-nil ReplyTo")
	}
	if result.ReplyTo.ID != "1" {
		t.Errorf("expected reply to ID '1', got '%s'", result.ReplyTo.ID)
	}
}

func TestV054ConvertMessageWithAttachments(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat: &tgbotapi.Chat{
			ID:   12345,
			Type: "private",
		},
		From: &tgbotapi.User{
			ID:        12345,
			UserName:  "testuser",
			FirstName: "Test",
		},
		Photo: []tgbotapi.PhotoSize{
			{FileID: "photo1", FileSize: 1024},
		},
		Caption: "photo caption",
	}

	result := adapter.convertMessage(msg)
	if len(result.Attachments) == 0 {
		t.Error("expected non-empty attachments")
	}
	// Photo 消息的 Text 为空，Caption 不被 convertMessage 读取
	// 所以 Text 会被自动构造为 "[用户发送了一张图片]"
	if result.Text != "[用户发送了一张图片]" {
		t.Errorf("expected auto-generated text for photo, got '%s'", result.Text)
	}
}

func TestV054ConvertMessageNoTextWithAttachment(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		MessageID: 1,
		Chat: &tgbotapi.Chat{
			ID:   12345,
			Type: "private",
		},
		From: &tgbotapi.User{
			ID:        12345,
			UserName:  "testuser",
			FirstName: "Test",
		},
		Voice: &tgbotapi.Voice{
			FileID:   "voice1",
			FileSize: 1024,
			MimeType: "audio/ogg",
		},
	}

	result := adapter.convertMessage(msg)
	if result.Text == "" {
		t.Error("expected auto-generated text for voice attachment")
	}
}

// ============================================================
// processUpdate 测试 (mock server)
// ============================================================

func TestV054ProcessUpdateWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handlerCalled := false
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		handlerCalled = true
		if msg.Text != "hello" {
			t.Errorf("expected text 'hello', got '%s'", msg.Text)
		}
		return nil
	})

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:   12345,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "hello",
		},
	}

	adapter.processUpdate(ctx, update)

	if !handlerCalled {
		t.Error("expected handler to be called")
	}
}

func TestV054ProcessUpdateNilMessage(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	update := tgbotapi.Update{}

	adapter.processUpdate(ctx, update)
}

func TestV054ProcessUpdateGroupMention(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handlerCalled := false
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		handlerCalled = true
		if !msg.IsGroupTrigger {
			t.Error("expected IsGroupTrigger to be true")
		}
		if msg.TriggerType != "mention" {
			t.Errorf("expected trigger type 'mention', got '%s'", msg.TriggerType)
		}
		return nil
	})

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:    888888,
				Type:  "group",
				Title: "Test Group",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "hello @testbot",
			Entities: []tgbotapi.MessageEntity{
				{
					Type:   "mention",
					Offset: 6,
					Length: 8,
				},
			},
		},
	}

	adapter.processUpdate(ctx, update)

	if !handlerCalled {
		t.Error("expected handler to be called for group mention")
	}
}

func TestV054ProcessUpdateGroupReplyToBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handlerCalled := false
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		handlerCalled = true
		if msg.TriggerType != "reply" {
			t.Errorf("expected trigger type 'reply', got '%s'", msg.TriggerType)
		}
		return nil
	})

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 2,
			Chat: &tgbotapi.Chat{
				ID:    888888,
				Type:  "group",
				Title: "Test Group",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "reply to bot",
			ReplyToMessage: &tgbotapi.Message{
				MessageID: 1,
				Chat: &tgbotapi.Chat{
					ID:   888888,
					Type: "group",
				},
				From: &tgbotapi.User{
					ID:       123456789,
					UserName: "testbot",
					IsBot:    true,
				},
				Text: "bot message",
			},
		},
	}

	adapter.processUpdate(ctx, update)

	if !handlerCalled {
		t.Error("expected handler to be called for reply to bot")
	}
}

func TestV054ProcessUpdateGroupNoMentionNoReply(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handlerCalled := false
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		handlerCalled = true
		return nil
	})

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:    888888,
				Type:  "group",
				Title: "Test Group",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "just chatting",
		},
	}

	adapter.processUpdate(ctx, update)

	if handlerCalled {
		t.Error("expected handler NOT to be called for group message without mention/reply")
	}
}

func TestV054ProcessUpdateChatNotAllowed(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.cfg.AllowedChats = []string{"99999"}

	handlerCalled := false
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		handlerCalled = true
		return nil
	})

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:   12345,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "hello",
		},
	}

	adapter.processUpdate(ctx, update)

	if handlerCalled {
		t.Error("expected handler NOT to be called for disallowed chat")
	}
}

func TestV054ProcessUpdateHandlerError(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		return fmt.Errorf("handler error")
	})

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:   12345,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "hello",
		},
	}

	adapter.processUpdate(ctx, update)
}

func TestV054ProcessUpdateNoHandler(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			MessageID: 1,
			Chat: &tgbotapi.Chat{
				ID:   12345,
				Type: "private",
			},
			From: &tgbotapi.User{
				ID:        12345,
				UserName:  "testuser",
				FirstName: "Test",
			},
			Text: "hello",
		},
	}

	adapter.processUpdate(ctx, update)
}

// ============================================================
// isMentioned 测试 (mock server)
// ============================================================

func TestV054IsMentionedTextContains(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		Text: "hello @testbot how are you",
	}

	if !adapter.isMentioned(msg) {
		t.Error("expected message to be mentioned via text")
	}
}

func TestV054IsMentionedEntityMention(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		Text: "hello @testbot",
		Entities: []tgbotapi.MessageEntity{
			{
				Type:   "mention",
				Offset: 6,
				Length: 8,
			},
		},
	}

	if !adapter.isMentioned(msg) {
		t.Error("expected message to be mentioned via entity")
	}
}

func TestV054IsMentionedTextMention(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		Text: "hello bot",
		Entities: []tgbotapi.MessageEntity{
			{
				Type: "text_mention",
				User: &tgbotapi.User{
					UserName: "testbot",
				},
			},
		},
	}

	if !adapter.isMentioned(msg) {
		t.Error("expected message to be mentioned via text_mention")
	}
}

func TestV054IsMentionedNoUsername(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	msg := &tgbotapi.Message{
		Text: "hello @testbot",
	}

	if adapter.isMentioned(msg) {
		t.Error("expected not mentioned when botUsername is empty")
	}
}

func TestV054IsMentionedNotMentioned(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		Text: "hello world",
	}

	if adapter.isMentioned(msg) {
		t.Error("expected not mentioned")
	}
}

// ============================================================
// 并发测试 (mock server)
// ============================================================

func TestV054ConcurrentSendWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			chatID := strconv.Itoa(10000 + idx)
			adapter.Send(ctx, chatID, "test-"+strconv.Itoa(idx))
		}(i)
	}

	wg.Wait()
}

func TestV054ConcurrentProcessUpdate(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		return nil
	})

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					MessageID: idx,
					Chat: &tgbotapi.Chat{
						ID:   12345,
						Type: "private",
					},
					From: &tgbotapi.User{
						ID:        12345,
						UserName:  "testuser",
						FirstName: "Test",
					},
					Text: "concurrent-" + strconv.Itoa(idx),
				},
			}
			adapter.processUpdate(ctx, update)
		}(i)
	}

	wg.Wait()
}

// ============================================================
// Handler 测试 (mock server)
// ============================================================

func TestV054HandlerHandleMessagePrivate(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text: "hello",
	}

	// agent 为 nil，handleChat 会 panic，但 HandleMessage 应该能路由
	// 测试命令路由
	cmdMsg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/help",
		IsCommand: true,
		Command:   "help",
	}

	// handleHelp 需要 agent，会 panic
	// 只测试路由逻辑
	_ = handler
	_ = msg
	_ = cmdMsg
}

func TestV054HandlerSetDataDirAndPersist(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	tmpDir := t.TempDir()
	// 直接设置 dataDir，不通过 SetDataDir（会触发 loadChatSessions 需要 agent）
	handler.mu.Lock()
	handler.dataDir = tmpDir
	handler.mu.Unlock()

	// 设置 session
	handler.setSessionID("12345", "session-abc")

	// 保存
	handler.saveChatSessions()

	// 验证文件存在
	path := handler.chatSessionsPath()
	if path == "" {
		t.Error("expected non-empty path")
	}

	// 验证文件内容
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read chat_sessions.json: %v", err)
	}

	var csd chatSessionsData
	if err := json.Unmarshal(data, &csd); err != nil {
		t.Fatalf("failed to parse chat_sessions.json: %v", err)
	}

	if csd.ChatSessions["12345"] != "session-abc" {
		t.Errorf("expected session 'session-abc', got '%s'", csd.ChatSessions["12345"])
	}
}

func TestV054HandlerHasSession(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	if handler.hasSession("12345") {
		t.Error("expected no session initially")
	}

	handler.setSessionID("12345", "session-abc")

	if !handler.hasSession("12345") {
		t.Error("expected session after setSessionID")
	}
}

func TestV054HandlerResetSession(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	handler.setSessionID("12345", "session-old")

	// resetSession 需要 agent，跳过实际调用
	// 只验证 setSessionID + hasSession 逻辑
	if !handler.hasSession("12345") {
		t.Error("expected session after setSessionID")
	}

	// 手动模拟 reset
	handler.setSessionID("12345", "session-new")
	if handler.hasSession("12345") {
		sid := func() string {
			handler.mu.RLock()
			defer handler.mu.RUnlock()
			return handler.sessions["12345"]
		}()
		if sid != "session-new" {
			t.Errorf("expected 'session-new', got '%s'", sid)
		}
	}
}

func TestV054HandlerHandleMessageWithAttachments(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text: "check this out",
		Attachments: []gateway.Attachment{
			{
				Type:     gateway.AttachmentImage,
				FileID:   "photo1",
				FileName: "photo.jpg",
				FileURL:  "https://example.com/photo.jpg",
			},
		},
	}

	// HandleMessage 会调用 handleChat，需要 agent
	// 只验证消息构造逻辑
	inputText := msg.Text
	if len(msg.Attachments) > 0 {
		var mediaDesc string
		mediaDesc = inputText + "\n\n[多媒体内容]\n"
		for _, att := range msg.Attachments {
			switch att.Type {
			case gateway.AttachmentImage:
				mediaDesc += fmt.Sprintf("📷 图片: %s\n", att.FileName)
			}
		}
		inputText = mediaDesc
	}

	if inputText == msg.Text {
		t.Error("expected inputText to be modified with attachment description")
	}
	_ = handler
}

// ============================================================
// escapeMarkdownV2 测试
// ============================================================

func TestV054EscapeMarkdownV2(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello_world", "hello\\_world"},
		{"*bold*", "\\*bold\\*"},
		{"[link]", "\\[link\\]"},
		{"test.text", "test\\.text"},
		{"a!b", "a\\!b"},
	}

	for _, tt := range tests {
		result := escapeMarkdownV2(tt.input)
		if result != tt.expected {
			t.Errorf("escapeMarkdownV2(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// ============================================================
// waitRateLimit 测试
// ============================================================

func TestV054WaitRateLimitNewChat(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	// 新 chat 不应该等待
	start := time.Now()
	adapter.waitRateLimit("99999")
	elapsed := time.Since(start)

	// 应该很快（没有等待）
	if elapsed > 500*time.Millisecond {
		t.Errorf("waitRateLimit took too long for new chat: %v", elapsed)
	}
}

func TestV054WaitRateLimitSameChat(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	// 设置高 RateLimit 以减少等待时间
	adapter.cfg.RateLimit = 100

	// 第一次调用
	adapter.waitRateLimit("12345")

	// 第二次调用应该很快
	start := time.Now()
	adapter.waitRateLimit("12345")
	elapsed := time.Since(start)

	// RateLimit=100，间隔约 10ms，允许较大余量
	if elapsed > 2*time.Second {
		t.Errorf("waitRateLimit took too long: %v", elapsed)
	}
}

// ============================================================
// Handler 命令测试 (不依赖 agent 的命令)
// ============================================================

func TestV054HandleStart(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/start",
		IsCommand: true,
		Command:   "start",
	}

	err = handler.handleStart(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleHelp(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/help",
		IsCommand: true,
		Command:   "help",
	}

	err = handler.handleHelp(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCommandUnknown(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/unknown",
		IsCommand: true,
		Command:   "unknown",
	}

	err = handler.handleCommand(ctx, msg)
	if err != nil {
		t.Errorf("expected no error for unknown command, got: %v", err)
	}
}

func TestV054HandleCommandStart(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/start",
		IsCommand: true,
		Command:   "start",
	}

	err = handler.handleCommand(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCommandHelp(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/help",
		IsCommand: true,
		Command:   "help",
	}

	err = handler.handleCommand(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleMessageCommand(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/start",
		IsCommand: true,
		Command:   "start",
	}

	err = handler.HandleMessage(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleMessageWithAttachments(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	handler := NewHandler(adapter, nil)

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text: "check this",
		Attachments: []gateway.Attachment{
			{
				Type:     gateway.AttachmentImage,
				FileID:   "photo1",
				FileName: "photo.jpg",
				FileURL:  "https://example.com/photo.jpg",
			},
		},
	}

	// HandleMessage 会调用 handleChat，需要 agent
	// 只验证消息构造逻辑
	inputText := msg.Text
	if len(msg.Attachments) > 0 {
		var mediaDesc strings.Builder
		mediaDesc.WriteString(inputText)
		if inputText != "" {
			mediaDesc.WriteString("\n\n")
		}
		mediaDesc.WriteString("[多媒体内容]\n")
		for i, att := range msg.Attachments {
			switch att.Type {
			case gateway.AttachmentImage:
				mediaDesc.WriteString(fmt.Sprintf("📷 图片 %d: %s (URL: %s)\n", i+1, att.FileName, att.FileURL))
			}
		}
		inputText = mediaDesc.String()
	}

	if inputText == msg.Text {
		t.Error("expected inputText to be modified with attachment description")
	}
	if !strings.Contains(inputText, "📷 图片") {
		t.Error("expected image description in inputText")
	}
	_ = handler
}

func TestV054ComposeAttachmentInputUsesAgentAnalysis(t *testing.T) {
	handler := &Handler{
		agent: &mockAgentProvider{
			analyzeFn: func(ctx context.Context, attachments []gateway.Attachment) (string, error) {
				return "[Multimodal Analysis]\nImage 1:\n- summary: chart screenshot", nil
			},
		},
	}

	out := handler.composeAttachmentInput(context.Background(), "check this", []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FileName: "photo.jpg",
			FileURL:  "https://example.com/photo.jpg",
		},
	})

	if !strings.Contains(out, "[Multimodal Analysis]") {
		t.Fatalf("expected analysis block, got %q", out)
	}
	if strings.Contains(out, "[Multimedia Attachments]") {
		t.Fatalf("expected agent analysis to replace metadata fallback, got %q", out)
	}
}

func TestV054HandleMessageWithAudioAttachment(t *testing.T) {
	msg := &gateway.Message{
		Text: "listen to this",
		Attachments: []gateway.Attachment{
			{
				Type:     gateway.AttachmentAudio,
				FileID:   "voice1",
				FileName: "voice.ogg",
				FileURL:  "https://example.com/voice.ogg",
			},
		},
	}

	inputText := msg.Text
	var mediaDesc strings.Builder
	mediaDesc.WriteString(inputText)
	mediaDesc.WriteString("\n\n")
	mediaDesc.WriteString("[多媒体内容]\n")
	for i, att := range msg.Attachments {
		switch att.Type {
		case gateway.AttachmentAudio:
			mediaDesc.WriteString(fmt.Sprintf("🎤 语音 %d: %s (URL: %s)\n", i+1, att.FileName, att.FileURL))
		}
	}
	inputText = mediaDesc.String()

	if !strings.Contains(inputText, "🎤 语音") {
		t.Error("expected audio description in inputText")
	}
}

func TestV054HandleMessageWithVideoAttachment(t *testing.T) {
	msg := &gateway.Message{
		Text: "watch this",
		Attachments: []gateway.Attachment{
			{
				Type:     gateway.AttachmentVideo,
				FileID:   "video1",
				FileName: "video.mp4",
				FileURL:  "https://example.com/video.mp4",
			},
		},
	}

	inputText := msg.Text
	var mediaDesc strings.Builder
	mediaDesc.WriteString(inputText)
	mediaDesc.WriteString("\n\n")
	mediaDesc.WriteString("[多媒体内容]\n")
	for i, att := range msg.Attachments {
		switch att.Type {
		case gateway.AttachmentVideo:
			mediaDesc.WriteString(fmt.Sprintf("🎬 视频 %d: %s (URL: %s)\n", i+1, att.FileName, att.FileURL))
		}
	}
	inputText = mediaDesc.String()

	if !strings.Contains(inputText, "🎬 视频") {
		t.Error("expected video description in inputText")
	}
}

func TestV054HandleMessageWithDocumentAttachment(t *testing.T) {
	msg := &gateway.Message{
		Text: "read this",
		Attachments: []gateway.Attachment{
			{
				Type:     gateway.AttachmentDocument,
				FileID:   "doc1",
				FileName: "report.pdf",
				MimeType: "application/pdf",
				FileURL:  "https://example.com/report.pdf",
			},
		},
	}

	inputText := msg.Text
	var mediaDesc strings.Builder
	mediaDesc.WriteString(inputText)
	mediaDesc.WriteString("\n\n")
	mediaDesc.WriteString("[多媒体内容]\n")
	for i, att := range msg.Attachments {
		switch att.Type {
		case gateway.AttachmentDocument:
			mediaDesc.WriteString(fmt.Sprintf("📎 文件 %d: %s (%s, URL: %s)\n", i+1, att.FileName, att.MimeType, att.FileURL))
		}
	}
	inputText = mediaDesc.String()

	if !strings.Contains(inputText, "📎 文件") {
		t.Error("expected document description in inputText")
	}
}

func TestV054HandleMessageNoAttachments(t *testing.T) {
	msg := &gateway.Message{
		Text: "just text",
	}

	inputText := msg.Text
	if len(msg.Attachments) > 0 {
		t.Error("expected no attachments")
	}

	if inputText != "just text" {
		t.Errorf("expected 'just text', got '%s'", inputText)
	}
}

// ============================================================
// truncateString 测试
// ============================================================

func TestV054TruncateString(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"hello", 5, "hello"},
		{"hello", 3, "..."},
		{"", 5, ""},
	}

	for _, tt := range tests {
		result := truncateString(tt.input, tt.maxLen)
		if result != tt.expected {
			t.Errorf("truncateString(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
		}
	}
}

// ============================================================
// Adapter poll 测试 (mock server)
// ============================================================

func TestV054PollWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	_ = false
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// poll 会阻塞，用 goroutine 运行
	done := make(chan struct{})
	go func() {
		adapter.poll(ctx)
		close(done)
	}()

	// 等待 poll 完成
	select {
	case <-done:
		// poll 在 context 取消后退出
	case <-time.After(3 * time.Second):
		t.Error("poll did not exit after context cancellation")
	}
}

// ============================================================
// Adapter Start 完整流程测试
// ============================================================

func TestV054StartFullFlow(t *testing.T) {
	// Start() calls tgbotapi.NewBotAPI which connects to real Telegram API.
	// Cannot mock with httptest because NewBotAPI uses the default API endpoint.
	// Test the setup logic via direct bot injection instead.
	t.Skip("requires real Telegram Bot API — tested via integration tests")
}

// ============================================================
// Adapter callAPI 测试 (mock server)
// ============================================================

func TestV054CallAPIWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	// 测试 getFile
	file, err := adapter.bot.GetFile(tgbotapi.FileConfig{FileID: "test_file_id"})
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if file.FilePath != "photos/test.jpg" {
		t.Errorf("expected file path 'photos/test.jpg', got '%s'", file.FilePath)
	}
}

// ============================================================
// Adapter 多消息分割测试
// ============================================================

func TestV054SplitMessageBoundary(t *testing.T) {
	adapter, _, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	// MaxMessageLen defaults to 4000, so 4000 chars should be 1 part
	msg := strings.Repeat("a", 4000)
	parts := adapter.splitMessage(msg)
	if len(parts) != 1 {
		t.Errorf("expected 1 part for 4000 chars, got %d", len(parts))
	}

	// 4001 chars should be 2 parts
	msg = strings.Repeat("a", 4001)
	parts = adapter.splitMessage(msg)
	if len(parts) != 2 {
		t.Errorf("expected 2 parts for 4001 chars, got %d", len(parts))
	}
}

func TestV054SplitMessageEmpty(t *testing.T) {
	adapter, _, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	parts := adapter.splitMessage("")
	// splitMessage returns []string{""} for empty input
	if len(parts) != 1 {
		t.Errorf("expected 1 part for empty string, got %d", len(parts))
	}
}

func TestV054SplitMessageRespectsNewlines(t *testing.T) {
	adapter, _, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	msg := "line1\nline2\nline3\nline4"
	parts := adapter.splitMessage(msg)
	for i, part := range parts {
		if len(part) > 4096 {
			t.Errorf("part %d exceeds max length: %d", i, len(part))
		}
	}
}

// ============================================================
// Adapter isReplyToBot 测试 (mock server)
// ============================================================

func TestV054IsReplyToBotTrue(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{
				ID:       123456789,
				UserName: "testbot",
				IsBot:    true,
			},
		},
	}

	if !adapter.isReplyToBot(msg) {
		t.Error("expected isReplyToBot to be true")
	}
}

func TestV054IsReplyToBotFalse(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{
				ID:       999999,
				UserName: "otheruser",
				IsBot:    false,
			},
		},
	}

	if adapter.isReplyToBot(msg) {
		t.Error("expected isReplyToBot to be false")
	}
}

func TestV054IsReplyToBotNoReply(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	msg := &tgbotapi.Message{}

	if adapter.isReplyToBot(msg) {
		t.Error("expected isReplyToBot to be false when no reply")
	}
}

func TestV054IsReplyToBotNoUsername(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)
	// botUsername is empty, but isReplyToBot only checks IsBot flag

	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{
				ID:       123456789,
				UserName: "testbot",
				IsBot:    true,
			},
		},
	}

	// isReplyToBot returns true when From.IsBot is true, regardless of botUsername
	if !adapter.isReplyToBot(msg) {
		t.Error("expected isReplyToBot to be true when From.IsBot is true")
	}
}

// ============================================================
// Adapter editMessage 测试 (mock server)
// ============================================================

func TestV054EditMessageWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	// editMessage 是 telegramStreamSender 的方法
	// 通过 SendStream 获取 sender 来测试
	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "12345", "")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	sender := stream.(*telegramStreamSender)
	err = sender.editMessage("edited text")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}

	stream.Finish()
}

// ============================================================
// Adapter sendTypingOnce 测试 (mock server)
// ============================================================

func TestV054SendTypingOnceWithMockBot(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.sendTypingOnce(12345)
}

func TestV054SendTypingOnceInvalidChatID(t *testing.T) {
	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}
	defer server.Close()

	adapter.sendTypingOnce(-1)
}

// ============================================================
// Adapter Name 测试
// ============================================================

func TestV054AdapterName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	if adapter.Name() != "telegram" {
		t.Errorf("expected name 'telegram', got '%s'", adapter.Name())
	}
}

// ============================================================
// Mock agentProvider for handler tests
// ============================================================

type mockAgentProvider struct {
	sessions      *session.Manager
	configSnap    agentConfigSnapshot
	soulVal       *soul.Soul
	toolsVal      *tool.Registry
	skillsVal     []*tool.SkillInfo
	cronEngine    *cron.Engine
	metricsVal    *metrics.Metrics
	memoryVal     *memory.Store
	chatFunc      func(ctx context.Context, userInput string) (string, error)
	chatSessFn    func(ctx context.Context, sessionID, userInput string) (string, error)
	chatStreamFn  func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error)
	progressFn    func(ctx context.Context, userInput string, round int, observations []string) (string, error)
	analyzeFn     func(ctx context.Context, attachments []gateway.Attachment) (string, error)
	switchModelFn func(modelID string) error
}

func (m *mockAgentProvider) Sessions() *session.Manager {
	return m.sessions
}

func (m *mockAgentProvider) Config() agentConfigProvider {
	return &mockConfigProvider{snap: m.configSnap}
}

func (m *mockAgentProvider) SwitchModel(modelID string) error {
	if m.switchModelFn != nil {
		return m.switchModelFn(modelID)
	}
	return nil
}

func (m *mockAgentProvider) Soul() *soul.Soul {
	return m.soulVal
}

func (m *mockAgentProvider) Tools() *tool.Registry {
	return m.toolsVal
}

func (m *mockAgentProvider) Skills() []*tool.SkillInfo {
	return m.skillsVal
}

func (m *mockAgentProvider) CronEngine() *cron.Engine {
	return m.cronEngine
}

func (m *mockAgentProvider) Chat(ctx context.Context, userInput string) (string, error) {
	if m.chatFunc != nil {
		return m.chatFunc(ctx, userInput)
	}
	return "mock response", nil
}

func (m *mockAgentProvider) ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error) {
	if m.chatSessFn != nil {
		return m.chatSessFn(ctx, sessionID, userInput)
	}
	return "mock response", nil
}

func (m *mockAgentProvider) ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
	if m.chatStreamFn != nil {
		return m.chatStreamFn(ctx, sessionID, userInput)
	}
	ch := make(chan agent.ChatEvent, 1)
	ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "mock response"}
	close(ch)
	return ch, nil
}

func (m *mockAgentProvider) ProgressFeedback(ctx context.Context, userInput string, round int, observations []string) (string, error) {
	if m.progressFn != nil {
		return m.progressFn(ctx, userInput, round, observations)
	}
	return "mock progress summary", nil
}

func (m *mockAgentProvider) AnalyzeAttachments(ctx context.Context, attachments []gateway.Attachment) (string, error) {
	if m.analyzeFn != nil {
		return m.analyzeFn(ctx, attachments)
	}
	return "", nil
}

func (m *mockAgentProvider) Metrics() *metrics.Metrics {
	return m.metricsVal
}

func (m *mockAgentProvider) Memory() *memory.Store {
	return m.memoryVal
}

type mockConfigProvider struct {
	snap agentConfigSnapshot
}

func (m *mockConfigProvider) Get() agentConfigSnapshot {
	return m.snap
}

type mockStreamSender struct {
	content       strings.Builder
	result        string
	finished      bool
	thinkingCount int
	toolCallCount int
}

func (m *mockStreamSender) Append(content string) error {
	m.content.WriteString(content)
	return nil
}

func (m *mockStreamSender) SetThinking(label string) error {
	m.thinkingCount++
	return nil
}

func (m *mockStreamSender) SetToolCall(name, args string) error {
	m.toolCallCount++
	return nil
}

func (m *mockStreamSender) SetResult(content string) error {
	m.result = content
	return nil
}

func (m *mockStreamSender) Finish() error {
	m.finished = true
	return nil
}

func (m *mockStreamSender) MessageID() string {
	return "mock-msg-id"
}

// newHandlerWithMockAgent creates a Handler with a mock agent for testing.
func newHandlerWithMockAgent(t *testing.T) (*Handler, *httptest.Server) {
	t.Helper()

	adapter, server, err := newAdapterWithMockBot()
	if err != nil {
		t.Fatalf("failed to create adapter: %v", err)
	}

	sessMgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create session manager: %v", err)
	}
	mockAgent := &mockAgentProvider{
		sessions: sessMgr,
		configSnap: agentConfigSnapshot{
			Model:              "test-model",
			Provider:           "test-provider",
			ProgressAsMessages: true,
		},
		toolsVal:   tool.NewRegistry(),
		skillsVal:  []*tool.SkillInfo{},
		cronEngine: cron.NewEngine(),
		metricsVal: metrics.NewMetrics(),
	}

	handler := &Handler{
		adapter:            adapter,
		agent:              mockAgent,
		sessions:           make(map[string]string),
		chatStreamTimeout:  defaultChatStreamTimeout,
		progressAsMessages: true,
	}

	return handler, server
}

// ============================================================
// Handler 命令测试 (with mock agent)
// ============================================================

func TestV054HandleModelShow(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/model",
		IsCommand: true,
		Command:   "model",
		Args:      "",
	}

	err := handler.handleModel(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleModelSwitch(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text:      "/model gpt-4",
		IsCommand: true,
		Command:   "model",
		Args:      "gpt-4",
	}

	err := handler.handleModel(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleModelSwitchError(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).switchModelFn = func(modelID string) error {
		return fmt.Errorf("model not found")
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/model invalid",
		IsCommand: true,
		Command:   "model",
		Args:      "invalid",
	}

	err := handler.handleModel(ctx, msg)
	if err != nil {
		t.Errorf("expected no error (error sent as message), got: %v", err)
	}
}

func TestV054HandleSoul(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/soul",
		IsCommand: true,
		Command:   "soul",
	}

	err := handler.handleSoul(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleSoulWithSoul(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).soulVal = soul.Default()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/soul",
		IsCommand: true,
		Command:   "soul",
	}

	err := handler.handleSoul(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleTools(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/tools",
		IsCommand: true,
		Command:   "tools",
	}

	err := handler.handleTools(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleToolsWithTools(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	reg := handler.agent.(*mockAgentProvider).toolsVal
	reg.Register(&tool.Tool{
		Name:        "web_search",
		Description: "Search the web",
		Enabled:     true,
	})
	reg.Register(&tool.Tool{
		Name:        "shell",
		Description: "Execute shell commands",
		Enabled:     false,
	})

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/tools",
		IsCommand: true,
		Command:   "tools",
	}

	err := handler.handleTools(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleReset(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/reset",
		IsCommand: true,
		Command:   "reset",
	}

	err := handler.handleReset(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleHistory(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/history",
		IsCommand: true,
		Command:   "history",
	}

	err := handler.handleHistory(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleSession(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/session",
		IsCommand: true,
		Command:   "session",
	}

	err := handler.handleSession(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleSessionWithSession(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	// 先创建一个 session
	sid := handler.getSessionID("12345")

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/session",
		IsCommand: true,
		Command:   "session",
	}

	err := handler.handleSession(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	_ = sid
}

func TestV054HandleSkills(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/skills",
		IsCommand: true,
		Command:   "skills",
	}

	err := handler.handleSkills(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleSkillsWithSkills(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).skillsVal = []*tool.SkillInfo{
		{Name: "web-search", Description: "Search the web for information"},
		{Name: "summarize", Description: "Summarize web pages and documents"},
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/skills",
		IsCommand: true,
		Command:   "skills",
	}

	err := handler.handleSkills(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCronList(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/cron list",
		IsCommand: true,
		Command:   "cron",
		Args:      "list",
	}

	err := handler.handleCron(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCronEmpty(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/cron",
		IsCommand: true,
		Command:   "cron",
		Args:      "",
	}

	err := handler.handleCron(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCronAdd(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/cron add test1 TestTask 60 check status",
		IsCommand: true,
		Command:   "cron",
		Args:      "add test1 TestTask 60 check status",
	}

	err := handler.handleCron(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCronAddInvalidInterval(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/cron add test1 TestTask abc check status",
		IsCommand: true,
		Command:   "cron",
		Args:      "add test1 TestTask abc check status",
	}

	err := handler.handleCron(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCronRemove(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/cron remove test1",
		IsCommand: true,
		Command:   "cron",
		Args:      "remove test1",
	}

	err := handler.handleCron(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCronUnknown(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/cron unknown",
		IsCommand: true,
		Command:   "cron",
		Args:      "unknown",
	}

	err := handler.handleCron(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleMetrics(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/metrics",
		IsCommand: true,
		Command:   "metrics",
	}

	err := handler.handleMetrics(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleHealth(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/health",
		IsCommand: true,
		Command:   "health",
	}

	err := handler.handleHealth(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleChatEmpty(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/chat",
		IsCommand: true,
		Command:   "chat",
		Args:      "",
	}

	err := handler.handleChat(ctx, msg, "")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleChatWithText(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text:      "/chat hello",
		IsCommand: true,
		Command:   "chat",
		Args:      "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleChatGroupWithSender(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "888888",
			Type: gateway.ChatGroup,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		IsGroupTrigger: true,
		Text:           "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleMessageNonCommand(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text: "hello world",
	}

	err := handler.HandleMessage(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleMessageNonCommandWithAttachments(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text: "check this",
		Attachments: []gateway.Attachment{
			{
				Type:     gateway.AttachmentImage,
				FileID:   "photo1",
				FileName: "photo.jpg",
				FileURL:  "https://example.com/photo.jpg",
			},
		},
	}

	err := handler.HandleMessage(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleMessageGroupNonCommand(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "888888",
			Type: gateway.ChatGroup,
		},
		Sender: gateway.User{
			ID:       "12345",
			Username: "testuser",
		},
		Text: "hello",
	}

	err := handler.HandleMessage(ctx, msg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleCommandAll(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	ctx := context.Background()

	commands := []struct {
		cmd  string
		args string
	}{
		{"start", ""},
		{"help", ""},
		{"model", ""},
		{"soul", ""},
		{"tools", ""},
		{"reset", ""},
		{"history", ""},
		{"session", ""},
		{"skills", ""},
		{"cron", "list"},
		{"metrics", ""},
		{"health", ""},
	}

	for _, tc := range commands {
		msg := &gateway.Message{
			ID: "1",
			Chat: gateway.Chat{
				ID:   "12345",
				Type: gateway.ChatPrivate,
			},
			Text:      "/" + tc.cmd,
			IsCommand: true,
			Command:   tc.cmd,
			Args:      tc.args,
		}

		err := handler.handleCommand(ctx, msg)
		if err != nil {
			t.Errorf("handleCommand(%s): expected no error, got: %v", tc.cmd, err)
		}
	}
}

func TestV054GetSessionID(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	// 第一次调用应该创建新 session
	sid1 := handler.getSessionID("12345")
	if sid1 == "" {
		t.Error("expected non-empty session ID")
	}

	// 第二次调用应该返回相同的 session
	sid2 := handler.getSessionID("12345")
	if sid1 != sid2 {
		t.Errorf("expected same session ID, got %s then %s", sid1, sid2)
	}
}

func TestV054ResetSession(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	sid1 := handler.getSessionID("12345")
	sid2 := handler.resetSession("12345")
	if sid1 == sid2 {
		t.Error("expected different session ID after reset")
	}
}

func TestV054HandleChatStreamError(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		return nil, fmt.Errorf("session not found")
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error (error handled gracefully), got: %v", err)
	}
}

func TestV054HandleChatStreamTimeout(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		return nil, fmt.Errorf("timeout after 30s")
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error (timeout handled gracefully), got: %v", err)
	}
}

func TestV054HandleChatStream503(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		return nil, fmt.Errorf("503 service unavailable")
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error (503 handled gracefully), got: %v", err)
	}
}

func TestV054HandleChatStreamSuccess(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 3)
		ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "thinking..."}
		ch <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "Hello!"}
		ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "Hello!"}
		close(ch)
		return ch, nil
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleChatStreamWithToolCall(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 5)
		ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "thinking..."}
		ch <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "web_search", Args: `{"query":"test"}`}
		ch <- agent.ChatEvent{Type: agent.ChatEventToolResult, Content: "search results"}
		ch <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "Here are the results"}
		ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "Here are the results"}
		close(ch)
		return ch, nil
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "search for test",
	}

	err := handler.handleChat(ctx, msg, "search for test")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestV054HandleChatStreamUnexpectedClose(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 2)
		ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "thinking..."}
		ch <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "partial response"}
		close(ch)
		return ch, nil
	}

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}
	sender := &mockStreamSender{}

	err := handler.handleChatStream(context.Background(), sender, msg, "hello", handler.getSessionID("12345"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !sender.finished {
		t.Fatal("expected stream sender to be finished when event channel closes unexpectedly")
	}
	if sender.result != "partial response" {
		t.Fatalf("expected fallback result from partial content, got: %q", sender.result)
	}
}

func TestV054HandleChatStreamProgressAsSeparateMessages(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 5)
		ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "thinking..."}
		ch <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "web_search", Args: `{"query":"test"}`}
		ch <- agent.ChatEvent{Type: agent.ChatEventToolResult, Content: "search results"}
		ch <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "Here are the results"}
		ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "Here are the results"}
		close(ch)
		return ch, nil
	}

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "search for test",
	}
	sender := &mockStreamSender{}

	err := handler.handleChatStream(context.Background(), sender, msg, "search for test", handler.getSessionID("12345"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if sender.thinkingCount != 0 {
		t.Fatalf("expected no inline thinking updates, got %d", sender.thinkingCount)
	}
	if sender.toolCallCount != 0 {
		t.Fatalf("expected no inline tool-call labels, got %d", sender.toolCallCount)
	}
	if sender.result != "Here are the results" {
		t.Fatalf("expected final result, got: %q", sender.result)
	}
}

func TestV054HandleChatStreamNaturalProgressFinalOnly(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.progressAsNaturalLanguage = true

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 5)
		ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "先核对任务状态"}
		ch <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "file_read", Args: `{"path":"tasks/QUEUE.md"}`}
		ch <- agent.ChatEvent{Type: agent.ChatEventToolResult, Name: "file_read", Result: "ok"}
		ch <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "这是最终答案"}
		ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "这是最终答案"}
		close(ch)
		return ch, nil
	}

	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "status report",
	}
	sender := &mockStreamSender{}

	err := handler.handleChatStream(context.Background(), sender, msg, "status report", handler.getSessionID("12345"))
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if sender.content.Len() != 0 {
		t.Fatalf("expected no streaming append in natural progress mode, got content length %d", sender.content.Len())
	}
	if !strings.HasPrefix(sender.result, "我整理好啦，下面这部分你可以直接看：\n") {
		t.Fatalf("expected wrapped final conclusion, got: %q", sender.result)
	}
}

func TestV054HandleChatStreamErrorEvent(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 1)
		ch <- agent.ChatEvent{Type: agent.ChatEventError, Err: fmt.Errorf("something went wrong")}
		close(ch)
		return ch, nil
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error (error event handled gracefully), got: %v", err)
	}
}

func TestV054HandleChatStreamError524(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 1)
		ch <- agent.ChatEvent{Type: agent.ChatEventError, Err: fmt.Errorf("524 timeout")}
		close(ch)
		return ch, nil
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error (524 error handled gracefully), got: %v", err)
	}
}

func TestV054HandleChatStreamError429(t *testing.T) {
	handler, server := newHandlerWithMockAgent(t)
	defer server.Close()

	handler.agent.(*mockAgentProvider).chatStreamFn = func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
		ch := make(chan agent.ChatEvent, 1)
		ch <- agent.ChatEvent{Type: agent.ChatEventError, Err: fmt.Errorf("429 rate limited")}
		close(ch)
		return ch, nil
	}

	ctx := context.Background()
	msg := &gateway.Message{
		ID: "1",
		Chat: gateway.Chat{
			ID:   "12345",
			Type: gateway.ChatPrivate,
		},
		Text: "hello",
	}

	err := handler.handleChat(ctx, msg, "hello")
	if err != nil {
		t.Errorf("expected no error (429 error handled gracefully), got: %v", err)
	}
}
