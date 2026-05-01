//go:build !integration
// +build !integration

package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
)

// v0.72.0: websocket 包测试补全 - 覆盖 syncChat 和 streamChat

// createTestAgentForWS 创建测试用 Agent
func createTestAgentForWS(t *testing.T) *agent.Agent {
	t.Helper()
	tmpDir := t.TempDir()
	mgr, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Fatalf("close agent: %v", err)
		}
	})
	return a
}

func cleanupPendingSession(t *testing.T, h *AgentHandler, sessionID string) {
	t.Helper()
	h.CancelSession(sessionID)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.PendingCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Logf("warning: pending websocket chat did not finish for session %s", sessionID)
}

// TestSyncChat 测试 syncChat 函数
func TestSyncChat(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)

	// 创建测试 client
	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	data := ChatData{
		Message: "Hello",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 调用 syncChat
	h.syncChat(ctx, client, data, "test-parent-id")

	// 验证消息推送
	select {
	case msg := <-client.Send:
		if msg.Type != TypeStatus {
			t.Errorf("expected TypeStatus, got %v", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("syncChat timed out")
	}
}

// TestSyncChatError 测试 syncChat 错误处理
func TestSyncChatError(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)

	// 创建测试 client
	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 使用空消息触发错误
	data := ChatData{
		Message: "",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h.syncChat(ctx, client, data, "test-parent-id")

	// 验证错误消息推送
	select {
	case msg := <-client.Send:
		// 应该收到 error 或 executing
		if msg.Type != TypeStatus && msg.Type != TypeError {
			t.Errorf("expected TypeStatus or TypeError, got %v", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("syncChat error test timed out")
	}
}

// TestStreamChat 测试 streamChat 函数
func TestStreamChat(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)

	// 创建测试 client
	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	data := ChatData{
		Message: "Hello",
		Stream:  true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 调用 streamChat
	h.streamChat(ctx, client, data, "test-parent-id")

	// 验证消息推送
	select {
	case msg := <-client.Send:
		if msg.Type != TypeStatus {
			t.Errorf("expected TypeStatus, got %v", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("streamChat timed out")
	}
}

// TestStreamChatError 测试 streamChat 错误处理
func TestStreamChatError(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)

	// 创建测试 client
	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 使用空消息触发错误
	data := ChatData{
		Message: "",
		Stream:  true,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h.streamChat(ctx, client, data, "test-parent-id")

	// 验证错误消息推送
	select {
	case msg := <-client.Send:
		if msg.Type != TypeStatus && msg.Type != TypeError {
			t.Errorf("expected TypeStatus or TypeError, got %v", msg.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("streamChat error test timed out")
	}
}

// TestHandleChat 测试 handleChat 函数（同步模式）
func TestHandleChat(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)
	t.Cleanup(func() {
		cleanupPendingSession(t, h, "test-session")
	})

	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 构造 chat 消息
	dataBytes, _ := json.Marshal(ChatData{Message: "Hello", Stream: false})
	msg := &Message{
		Type:      TypeChat,
		SessionID: "test-session",
		Data:      dataBytes,
	}

	h.handleChat(client, msg)

	// 验证收到 thinking 状态
	select {
	case m := <-client.Send:
		if m.Type != TypeStatus {
			t.Errorf("expected TypeStatus, got %v", m.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("handleChat timed out")
	}
}

// TestHandleChatStream 测试 handleChat 函数（流式模式）
func TestHandleChatStream(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)
	t.Cleanup(func() {
		cleanupPendingSession(t, h, "test-session")
	})

	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	dataBytes, _ := json.Marshal(ChatData{Message: "Hello", Stream: true})
	msg := &Message{
		Type:      TypeChat,
		SessionID: "test-session",
		Data:      dataBytes,
	}

	h.handleChat(client, msg)

	// 验证收到 thinking 状态
	select {
	case m := <-client.Send:
		if m.Type != TypeStatus {
			t.Errorf("expected TypeStatus, got %v", m.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("handleChat stream timed out")
	}
}

// TestHandleChatInvalidData 测试 handleChat 错误数据处理
func TestHandleChatInvalidData(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)

	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 构造无效 JSON 数据
	msg := &Message{
		Type:      TypeChat,
		SessionID: "test-session",
		Data:      []byte("invalid json{{{"),
	}

	h.handleChat(client, msg)

	// 验证收到错误消息
	select {
	case m := <-client.Send:
		if m.Type != TypeError {
			t.Errorf("expected TypeError, got %v", m.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("handleChat invalid data test timed out")
	}
}

// TestHandleChatCancelPending 测试取消 pending 请求
func TestHandleChatCancelPending(t *testing.T) {
	a := createTestAgentForWS(t)
	h := NewAgentHandler(a)
	t.Cleanup(func() {
		cleanupPendingSession(t, h, "test-session")
	})

	client := &Client{
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 第一次请求
	data1, _ := json.Marshal(ChatData{Message: "First", Stream: false})
	msg1 := &Message{
		Type:      TypeChat,
		SessionID: "test-session",
		Data:      data1,
	}
	h.handleChat(client, msg1)

	// 等待一下让 goroutine 启动
	time.Sleep(100 * time.Millisecond)

	// 第二次请求（应该取消第一次）
	data2, _ := json.Marshal(ChatData{Message: "Second", Stream: false})
	msg2 := &Message{
		Type:      TypeChat,
		SessionID: "test-session",
		Data:      data2,
	}
	h.handleChat(client, msg2)

	// 验证收到两条 thinking 状态
	count := 0
	timeout := time.After(3 * time.Second)
	for count < 2 {
		select {
		case <-client.Send:
			count++
		case <-timeout:
			t.Errorf("expected 2 status messages, got %d", count)
			return
		}
	}
}
