//go:build !integration
// +build !integration

package websocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
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

type stubAgentRuntime struct {
	sessions       *session.Manager
	tools          *tool.Registry
	chatFn         func(ctx context.Context, userInput string) (string, error)
	chatSessFn     func(ctx context.Context, sessionID, userInput string) (string, error)
	chatStreamSess func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error)
	chatInputFn    func(ctx context.Context, sessionID string, input agent.UserTurnInput) (string, error)
	chatStreamInputFn func(ctx context.Context, sessionID string, input agent.UserTurnInput) (<-chan agent.ChatEvent, error)
}

func (s *stubAgentRuntime) Chat(ctx context.Context, userInput string) (string, error) {
	if s.chatFn != nil {
		return s.chatFn(ctx, userInput)
	}
	return "", nil
}

func (s *stubAgentRuntime) ChatWithSession(ctx context.Context, sessionID, userInput string) (string, error) {
	if s.chatSessFn != nil {
		return s.chatSessFn(ctx, sessionID, userInput)
	}
	return "", nil
}

func (s *stubAgentRuntime) ChatWithSessionStream(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
	if s.chatStreamSess != nil {
		return s.chatStreamSess(ctx, sessionID, userInput)
	}
	ch := make(chan agent.ChatEvent)
	close(ch)
	return ch, nil
}

func (s *stubAgentRuntime) ChatWithSessionInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (string, error) {
	if s.chatInputFn != nil {
		return s.chatInputFn(ctx, sessionID, input)
	}
	return "", nil
}

func (s *stubAgentRuntime) ChatWithSessionStreamInput(ctx context.Context, sessionID string, input agent.UserTurnInput) (<-chan agent.ChatEvent, error) {
	if s.chatStreamInputFn != nil {
		return s.chatStreamInputFn(ctx, sessionID, input)
	}
	return s.ChatWithSessionStream(ctx, sessionID, input.RoutingText)
}

func (s *stubAgentRuntime) Sessions() *session.Manager {
	return s.sessions
}

func (s *stubAgentRuntime) Tools() *tool.Registry {
	return s.tools
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

func TestStreamChatEmitsStructuredAgentEvents(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	h := NewAgentHandler(&stubAgentRuntime{
		sessions: mgr,
		tools:    tool.NewRegistry(),
		chatStreamSess: func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
			ch := make(chan agent.ChatEvent, 6)
			ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "Thinking... (round 1)"}
			ch <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "web_search", Args: `{"query":"luckyagent"}`}
			ch <- agent.ChatEvent{Type: agent.ChatEventToolResult, Name: "web_search", Result: "Found 3 results"}
			ch <- agent.ChatEvent{Type: agent.ChatEventContent, Content: "Final answer"}
			ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "Final answer"}
			close(ch)
			return ch, nil
		},
	})

	client := &Client{
		SessionID: "ws-structured",
		Send:      make(chan *Message, 16),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	h.streamChat(ctx, client, ChatData{Message: "Hello", Stream: true}, "parent-id")

	if _, ok := mgr.Get("ws-structured"); !ok {
		t.Fatal("expected websocket session to be ensured before streaming")
	}

	gotTypes := make([]MessageType, 0, len(client.Send))
	for len(client.Send) > 0 {
		gotTypes = append(gotTypes, (<-client.Send).Type)
	}

	want := []MessageType{
		TypeStatus,
		TypeReasoning,
		TypeToolCall,
		TypeToolResult,
		TypeStreamChunk,
		TypeStreamEnd,
		TypeStatus,
	}
	if len(gotTypes) != len(want) {
		t.Fatalf("expected %d websocket messages, got %d (%v)", len(want), len(gotTypes), gotTypes)
	}
	for i := range want {
		if gotTypes[i] != want[i] {
			t.Fatalf("message %d: expected %s, got %s", i, want[i], gotTypes[i])
		}
	}
}

func TestStreamChatAnnotatesToolGroupingAndVisibility(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true, Category: tool.CatBuiltin})

	h := NewAgentHandler(&stubAgentRuntime{
		sessions: mgr,
		tools:    reg,
		chatStreamSess: func(ctx context.Context, sessionID, userInput string) (<-chan agent.ChatEvent, error) {
			ch := make(chan agent.ChatEvent, 8)
			ch <- agent.ChatEvent{Type: agent.ChatEventThinking, Content: "Thinking... (round 1)"}
			ch <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "web_search", Args: `{"query":"agent ui"}`}
			ch <- agent.ChatEvent{Type: agent.ChatEventToolCall, Name: "skill_read", Args: `{"name":"obsidian"}`}
			ch <- agent.ChatEvent{Type: agent.ChatEventToolResult, Name: "web_search", Result: "ok"}
			ch <- agent.ChatEvent{Type: agent.ChatEventToolResult, Name: "skill_read", Result: "ok"}
			ch <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "done"}
			close(ch)
			return ch, nil
		},
	})

	client := &Client{
		SessionID: "ws-visibility",
		Send:      make(chan *Message, 16),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h.streamChat(ctx, client, ChatData{Message: "Hello", Stream: true}, "parent-id")

	var toolCalls []ToolCallData
	var toolResults []ToolResultData
	for len(client.Send) > 0 {
		msg := <-client.Send
		switch msg.Type {
		case TypeToolCall:
			var data ToolCallData
			if err := msg.ParseData(&data); err != nil {
				t.Fatalf("parse tool call: %v", err)
			}
			toolCalls = append(toolCalls, data)
		case TypeToolResult:
			var data ToolResultData
			if err := msg.ParseData(&data); err != nil {
				t.Fatalf("parse tool result: %v", err)
			}
			toolResults = append(toolResults, data)
		}
	}

	if len(toolCalls) != 2 || len(toolResults) != 2 {
		t.Fatalf("expected 2 tool calls and 2 tool results, got %d/%d", len(toolCalls), len(toolResults))
	}
	if toolCalls[0].GroupID != "round-1" || toolCalls[0].StepID == "" {
		t.Fatalf("expected grouped tool call metadata, got %+v", toolCalls[0])
	}
	if toolCalls[0].Visibility != "visible" {
		t.Fatalf("expected web_search visible, got %s", toolCalls[0].Visibility)
	}
	if toolCalls[1].Visibility != "compact" {
		t.Fatalf("expected skill_read compact, got %s", toolCalls[1].Visibility)
	}
	if toolResults[0].StepID != toolCalls[0].StepID {
		t.Fatalf("expected result step id %s, got %s", toolCalls[0].StepID, toolResults[0].StepID)
	}
	if toolResults[1].StepID != toolCalls[1].StepID {
		t.Fatalf("expected result step id %s, got %s", toolCalls[1].StepID, toolResults[1].StepID)
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
