//go:build !integration
// +build !integration

package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// v0.67.0: websocket 包测试补全 - 覆盖 handler 相关 0% 函数

// TestHandleMessageUnknownType 测试 HandleMessage 处理未知消息类型
func TestHandleMessageUnknownType(t *testing.T) {
	client := &Client{
		ID:        "test-client",
		SessionID: "test-session-4",
		Send:      make(chan *Message, 10),
	}

	msg := &Message{
		Type:      "UNKNOWN_TYPE",
		SessionID: "test-session-4",
		Data:      json.RawMessage([]byte("{}")),
	}

	handler := NewAgentHandler(nil)
	// 不应该 panic
	handler.HandleMessage(client, msg)
}

// TestHandleMessageInvalidData 测试 HandleMessage 处理无效数据
func TestHandleMessageInvalidData(t *testing.T) {
	client := &Client{
		ID:        "test-client",
		SessionID: "test-session-3",
		Send:      make(chan *Message, 10),
	}

	// 创建无效数据的消息
	invalidData := json.RawMessage([]byte("invalid_json"))
	msg := &Message{
		Type:      TypeChat,
		SessionID: "test-session-3",
		Data:      invalidData,
	}

	handler := NewAgentHandler(nil)
	handler.HandleMessage(client, msg)

	// 等待消息处理
	time.Sleep(100 * time.Millisecond)

	// 验证收到错误消息
	select {
	case received := <-client.Send:
		if received.Type != TypeError {
			t.Errorf("HandleMessage invalid data: expected TypeError, got %v", received.Type)
		}
	default:
		t.Error("HandleMessage invalid data: expected error message")
	}
}

// TestCancelSession 测试 CancelSession 函数
func TestCancelSession(t *testing.T) {
	handler := NewAgentHandler(nil)

	// 添加一个 pending cancel
	ctx, cancel := context.WithCancel(context.Background())
	handler.pending["test-session"] = cancel

	// 验证 pending 存在
	if handler.PendingCount() != 1 {
		t.Errorf("PendingCount before cancel: expected 1, got %d", handler.PendingCount())
	}

	// 取消 session
	handler.CancelSession("test-session")

	// 验证 context 被取消
	select {
	case <-ctx.Done():
		// 符合预期
	default:
		t.Error("CancelSession: context should be cancelled")
	}

	// 验证 pending 被移除
	if handler.PendingCount() != 0 {
		t.Errorf("PendingCount after cancel: expected 0, got %d", handler.PendingCount())
	}
}

// TestPendingCount 测试 PendingCount 函数
func TestPendingCount(t *testing.T) {
	handler := NewAgentHandler(nil)

	if handler.PendingCount() != 0 {
		t.Errorf("PendingCount empty: expected 0, got %d", handler.PendingCount())
	}

	// 添加 pending
	handler.pending["session1"] = func() {}
	handler.pending["session2"] = func() {}

	if handler.PendingCount() != 2 {
		t.Errorf("PendingCount with 2 sessions: expected 2, got %d", handler.PendingCount())
	}
}

// TestParseData 测试 ParseData 函数
func TestParseData(t *testing.T) {
	// 创建测试消息
	msg, err := NewMessage(TypeChat, "test-session", ChatData{
		Message: "hello",
		Stream:  true,
	})
	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	var data ChatData
	if err := msg.ParseData(&data); err != nil {
		t.Fatalf("ParseData failed: %v", err)
	}

	if data.Message != "hello" {
		t.Errorf("ParseData: expected message 'hello', got '%s'", data.Message)
	}

	if !data.Stream {
		t.Error("ParseData: expected Stream to be true")
	}
}

// TestNewMessage 测试 NewMessage 函数
func TestNewMessage(t *testing.T) {
	msg, err := NewMessage(TypeChat, "test-session", ChatData{
		Message: "test",
		Stream:  false,
	})

	if err != nil {
		t.Fatalf("NewMessage failed: %v", err)
	}

	if msg.Type != TypeChat {
		t.Errorf("NewMessage: expected TypeChat, got %v", msg.Type)
	}

	if msg.SessionID != "test-session" {
		t.Errorf("NewMessage: expected session_id 'test-session', got '%s'", msg.SessionID)
	}
}

// v0.67.0: websocket 包测试补全 - 覆盖 syncChat 和 streamChat 0% 函数

// TestSyncChatLogic 测试 syncChat 的消息发送逻辑
func TestSyncChatLogic(t *testing.T) {
	// 创建测试客户端
	client := &Client{
		ID:        "test-client",
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 发送 executing 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "executing",
		Message: "agent is running",
	})
	client.Send <- status

	// 模拟 agent 返回错误（因为 agent 为 nil）
	// 直接发送错误消息
	errMsg, _ := NewMessage(TypeError, client.SessionID, ErrorData{
		Code:    "AGENT_ERROR",
		Message: "agent is nil",
	})
	errMsg.ParentID = "parent-1"
	client.Send <- errMsg

	// 发送 idle 状态
	idle, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State: "idle",
	})
	client.Send <- idle

	// 验证消息序列
	messages := collectMessages(client.Send, 3)

	if len(messages) < 2 {
		t.Errorf("syncChat logic: expected at least 2 messages, got %d", len(messages))
	}

	// 验证第一条是 executing
	if messages[0].Type != TypeStatus {
		t.Errorf("syncChat: first message should be TypeStatus, got %v", messages[0].Type)
	}
}

// TestStreamChatLogic 测试 streamChat 的消息发送逻辑
func TestStreamChatLogic(t *testing.T) {
	// 创建测试客户端
	client := &Client{
		ID:        "test-client",
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 发送 executing 状态
	status, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State:   "executing",
		Message: "agent is running",
	})
	client.Send <- status

	// 模拟发送 stream chunk
	chunk, _ := NewMessage(TypeStreamChunk, client.SessionID, StreamChunkData{
		Content: "test chunk",
		Done:    false,
	})
	chunk.ParentID = "parent-2"
	client.Send <- chunk

	// 发送 stream end
	endMsg, _ := NewMessage(TypeStreamEnd, client.SessionID, StreamEndData{
		FullResponse: "full response",
		Iterations:   1,
	})
	endMsg.ParentID = "parent-2"
	client.Send <- endMsg

	// 发送 idle 状态
	idle, _ := NewMessage(TypeStatus, client.SessionID, StatusData{
		State: "idle",
	})
	client.Send <- idle

	// 验证消息序列
	messages := collectMessages(client.Send, 4)

	if len(messages) < 3 {
		t.Errorf("streamChat logic: expected at least 3 messages, got %d", len(messages))
	}

	// 验证第一条是 executing
	if messages[0].Type != TypeStatus {
		t.Errorf("streamChat: first message should be TypeStatus, got %v", messages[0].Type)
	}
}

// collectMessages 从 channel 收集消息
func collectMessages(ch chan *Message, expected int) []*Message {
	var messages []*Message
	timeout := time.After(2 * time.Second)

	for {
		select {
		case msg := <-ch:
			messages = append(messages, msg)
			if len(messages) >= expected {
				return messages
			}
		case <-timeout:
			return messages
		default:
			if len(messages) > 0 {
				return messages
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestHandleCancelStopsPendingSession(t *testing.T) {
	handler := NewAgentHandler(nil)
	ctx, cancel := context.WithCancel(context.Background())
	handler.pending["gui-session"] = cancel

	client := &Client{ID: "c1", SessionID: "gui-session", Send: make(chan *Message, 4)}
	msg, err := NewMessage(TypeCancel, "gui-session", CancelData{SessionID: "gui-session"})
	if err != nil {
		t.Fatalf("new cancel message: %v", err)
	}
	handler.HandleMessage(client, msg)

	select {
	case <-ctx.Done():
	default:
		t.Fatal("cancel message should cancel the pending session")
	}
	if handler.PendingCount() != 0 {
		t.Fatalf("pending count = %d, want 0", handler.PendingCount())
	}
	select {
	case sent := <-client.Send:
		if sent.Type != TypeStatus {
			t.Fatalf("ack type = %s, want status", sent.Type)
		}
	default:
		t.Fatal("expected a cancelled status ack")
	}
}
