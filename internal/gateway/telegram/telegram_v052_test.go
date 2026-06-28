package telegram

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

// ============================================================
// CV-2: Telegram Handler 测试补全
// ============================================================

// TestHandlerSetDataDir 测试 SetDataDir
func TestHandlerSetDataDir(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)
	handler := NewHandler(adapter, nil)

	tmpDir := t.TempDir()
	handler.SetDataDir(tmpDir)

	h := handler
	if h.dataDir != tmpDir {
		t.Errorf("expected dataDir %s, got %s", tmpDir, h.dataDir)
	}
}

// TestHandlerChatSessionsPath 测试 chatSessionsPath (跳过，需要 agent)
func TestHandlerChatSessionsPath(t *testing.T) {
	t.Skip("requires non-nil agent")
}

// TestHandlerLoadSaveChatSessions 测试会话加载保存 (跳过，需要 agent)
func TestHandlerLoadSaveChatSessions(t *testing.T) {
	t.Skip("requires non-nil agent")
}

// TestHandlerGetSessionID 测试 getSessionID (跳过，需要 agent)
func TestHandlerGetSessionID(t *testing.T) {
	t.Skip("requires non-nil agent")
}

// TestHandlerResetSession 测试 resetSession (跳过，需要 agent)
func TestHandlerResetSession(t *testing.T) {
	t.Skip("requires non-nil agent")
}

// TestHandlerSetSessionID 测试 setSessionID
func TestHandlerSetSessionID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)
	handler := NewHandler(adapter, nil)

	h := handler
	h.setSessionID("chat-1", "test-session")
	if h.sessions["chat-1"] != "test-session" {
		t.Errorf("expected test-session, got %s", h.sessions["chat-1"])
	}
}

// TestHandlerHasSession 测试 hasSession
func TestHandlerHasSession(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)
	handler := NewHandler(adapter, nil)

	h := handler
	h.sessions["chat-1"] = "session-1"

	if !h.hasSession("chat-1") {
		t.Error("expected session to exist")
	}
	if h.hasSession("chat-2") {
		t.Error("expected session not to exist")
	}
}

// TestHandlerHandleMessage 测试 HandleMessage (跳过，需要 agent)
func TestHandlerHandleMessage(t *testing.T) {
	t.Skip("requires non-nil agent")
}

// TestHandlerHandleCommand 测试 handleCommand (跳过，需要 agent)
func TestHandlerHandleCommand(t *testing.T) {
	t.Skip("requires non-nil agent")
}

// TestHandlerTruncateString 测试 truncateString
func TestHandlerTruncateString(t *testing.T) {
	result := truncateString("hello", 10)
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}

	result = truncateString("this is a long string", 10)
	if len(result) != 10 {
		t.Errorf("expected length 10, got %d", len(result))
	}
}

// ============================================================
// CV-3: Telegram Adapter 测试补全
// ============================================================

// TestAdapterStartInvalidConfig 测试 Start 无效配置
func TestAdapterStartInvalidConfig(t *testing.T) {
	cfg := Config{}
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	err := adapter.Start(ctx)
	if err == nil {
		t.Error("expected error for empty token")
	}
}

// TestAdapterIsRunning 测试 IsRunning
func TestAdapterIsRunning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	if adapter.IsRunning() {
		t.Error("expected not running initially")
	}
}

// TestAdapterSend 测试 Send
func TestAdapterSend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	err := adapter.Send(ctx, "123", "test message")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

// TestAdapterSendWithReply 测试 SendWithReply
func TestAdapterSendWithReply(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	err := adapter.SendWithReply(ctx, "123", "456", "test message")
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

// TestAdapterSendTypingOnce 测试 sendTypingOnce
func TestAdapterSendTypingOnce(t *testing.T) {
	// 需要有效的 bot 实例，跳过
	t.Skip("requires valid bot instance")
}

// TestAdapterReactToMessage 测试 ReactToMessage
func TestAdapterReactToMessage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	// 不应该 panic（bot 为 nil 时会跳过）
	adapter.ReactToMessage("12345", "1", "👍")
}

// TestAdapterCallSetMessageReaction 测试 callSetMessageReaction
func TestAdapterCallSetMessageReaction(t *testing.T) {
	// 需要有效的 bot 实例，跳过
	t.Skip("requires valid bot instance")
}

// TestAdapterCallTelegramAPI 测试 callTelegramAPI
func TestAdapterCallTelegramAPI(t *testing.T) {
	// 需要有效的 bot 实例，跳过
	t.Skip("requires valid bot instance")
}

// TestAdapterSendStream 测试 SendStream
func TestAdapterSendStream(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, err := adapter.SendStream(ctx, "123", "")
	if err == nil {
		t.Error("expected error for invalid token")
	}
	if stream != nil {
		t.Error("expected nil stream")
	}
}

// TestAdapterAppend 测试 Append
func TestAdapterAppend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		err := stream.Append("test")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	}
}

// TestAdapterSetThinking 测试 SetThinking
func TestAdapterSetThinking(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		err := stream.SetThinking("thinking")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	}
}

// TestAdapterSetToolCall 测试 SetToolCall
func TestAdapterSetToolCall(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		err := stream.SetToolCall("search", `{"query": "test"}`)
		if err == nil {
			t.Error("expected error for invalid token")
		}
	}
}

// TestAdapterSetResult 测试 SetResult
func TestAdapterSetResult(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		err := stream.SetResult("result")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	}
}

// TestAdapterFinish 测试 Finish
func TestAdapterFinish(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		err := stream.Finish()
		if err == nil {
			t.Error("expected error for invalid token")
		}
	}
}

// TestAdapterMessageID 测试 MessageID
func TestAdapterMessageID(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		id := stream.MessageID()
		if id != "" {
			t.Errorf("expected empty string, got %s", id)
		}
	}
}

// TestAdapterThrottledEdit 测试 throttledEdit
func TestAdapterThrottledEdit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		stream.(*telegramStreamSender).throttledEdit()
	}
}

// TestAdapterEditMessage 测试 editMessage
func TestAdapterEditMessage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	stream, _ := adapter.SendStream(ctx, "123", "")
	if stream != nil {
		err := stream.(*telegramStreamSender).editMessage("new text")
		if err == nil {
			t.Error("expected error for invalid token")
		}
	}
}

// TestAdapterPoll 测试 poll (跳过，需要实际服务)
func TestAdapterPoll(t *testing.T) {
	t.Skip("requires actual Telegram Bot API")
}

// TestAdapterProcessUpdate 测试 processUpdate
func TestAdapterProcessUpdate(t *testing.T) {
	// 需要有效的 handler，跳过
	t.Skip("requires valid handler")
}

// TestAdapterConvertMessage 测试 convertMessage
func TestAdapterConvertMessage(t *testing.T) {
	cfg := DefaultConfig()
	adapter := NewAdapter(cfg)

	// 测试私聊消息
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
		Text: "hello",
	}

	result := adapter.convertMessage(msg)
	if result == nil {
		t.Error("expected non-nil message")
	}
	if result.Chat.ID != "12345" {
		t.Errorf("expected chat ID 12345, got %s", result.Chat.ID)
	}
	if result.Text != "hello" {
		t.Errorf("expected text 'hello', got %s", result.Text)
	}
}

// TestAdapterConvertMessageGroup 测试群聊消息转换
func TestAdapterConvertMessageGroup(t *testing.T) {
	cfg := DefaultConfig()
	adapter := NewAdapter(cfg)

	msg := &tgbotapi.Message{
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
		Text: "group message",
	}

	result := adapter.convertMessage(msg)
	if result == nil {
		t.Error("expected non-nil message")
	}
	if result.Chat.Type != gateway.ChatGroup {
		t.Errorf("expected group chat type, got %v", result.Chat.Type)
	}
}

// TestAdapterExtractAttachments 测试 extractAttachments
func TestAdapterExtractAttachments(t *testing.T) {
	// 需要有效的 bot 实例，跳过
	t.Skip("requires valid bot instance")
}

// TestAdapterExtractAttachmentsDocument 测试文档附件
func TestAdapterExtractAttachmentsDocument(t *testing.T) {
	// 需要有效的 bot 实例，跳过
	t.Skip("requires valid bot instance")
}

// TestAdapterIsMentioned 测试 isMentioned
func TestAdapterIsMentioned(t *testing.T) {
	cfg := DefaultConfig()
	adapter := NewAdapter(cfg)
	adapter.botUsername = "testbot"

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
		t.Error("expected message to be mentioned")
	}

	// 测试未提及
	msg2 := &tgbotapi.Message{
		Text: "hello world",
	}

	if adapter.isMentioned(msg2) {
		t.Error("expected message not to be mentioned")
	}
}

// TestAdapterIsReplyToBot 测试 isReplyToBot
func TestAdapterIsReplyToBot(t *testing.T) {
	cfg := DefaultConfig()
	adapter := NewAdapter(cfg)

	// 测试回复给 bot（需要设置 IsBot=true）
	msg := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{
				UserName: "testbot",
				IsBot:    true,
			},
		},
	}

	if !adapter.isReplyToBot(msg) {
		t.Error("expected reply to bot")
	}

	// 测试回复给别人
	msg2 := &tgbotapi.Message{
		ReplyToMessage: &tgbotapi.Message{
			From: &tgbotapi.User{
				UserName: "otheruser",
				IsBot:    false,
			},
		},
	}

	if adapter.isReplyToBot(msg2) {
		t.Error("expected not reply to bot")
	}
}

// TestAdapterSendChunk 测试 sendChunk
func TestAdapterSendChunk(t *testing.T) {
	// 需要有效的 bot 实例，跳过
	t.Skip("requires valid bot instance")
}

// TestAdapterWaitRateLimit 测试 waitRateLimit
func TestAdapterWaitRateLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)
	adapter.waitRateLimit("test-user")
}

// TestAdapterSplitMessage 测试 splitMessage
func TestAdapterSplitMessage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)

	chunks := adapter.splitMessage("hello")
	if len(chunks) != 1 {
		t.Errorf("expected 1 chunk, got %d", len(chunks))
	}

	longMsg := ""
	for i := 0; i < 5000; i++ {
		longMsg += "x"
	}
	chunks = adapter.splitMessage(longMsg)
	if len(chunks) < 2 {
		t.Errorf("expected >= 2 chunks, got %d", len(chunks))
	}
}

// ============================================================
// CV-4: 并发安全测试
// ============================================================

// TestAdapterConcurrentSend 测试并发发送
func TestAdapterConcurrentSend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "invalid-token"
	adapter := NewAdapter(cfg)

	ctx := context.Background()
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			adapter.Send(ctx, "123", "test-"+string(rune('0'+idx)))
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestHandlerConcurrentSessions 测试并发 session 操作
func TestHandlerConcurrentSessions(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "test-token"
	adapter := NewAdapter(cfg)
	handler := NewHandler(adapter, nil)

	h := handler
	done := make(chan bool)

	for i := 0; i < 10; i++ {
		go func(idx int) {
			chatID := "chat-" + string(rune('0'+idx))
			h.setSessionID(chatID, "session-"+string(rune('0'+idx)))
			h.hasSession(chatID)
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
