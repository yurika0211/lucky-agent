package websocket

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockHandler 用于测试的简单消息处理器
type mockHandler struct {
	mu      sync.Mutex
	msgs    []*Message
	clients []*Client
}

func (h *mockHandler) HandleMessage(client *Client, msg *Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, msg)
	h.clients = append(h.clients, client)
}

// TestMessageMarshal 测试消息序列化/反序列化
func TestMessageMarshal(t *testing.T) {
	msg, err := NewMessage(TypeChat, "session-1", ChatData{
		Message: "hello",
		Stream:  true,
	})
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}
	if msg.Type != TypeChat {
		t.Errorf("expected type %s, got %s", TypeChat, msg.Type)
	}
	if msg.SessionID != "session-1" {
		t.Errorf("expected session session-1, got %s", msg.SessionID)
	}
	if msg.ID == "" {
		t.Error("expected non-empty message ID")
	}

	// 序列化
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// 反序列化
	parsed, err := ParseMessage(data)
	if err != nil {
		t.Fatalf("ParseMessage error: %v", err)
	}
	if parsed.Type != TypeChat {
		t.Errorf("expected type %s, got %s", TypeChat, parsed.Type)
	}
	if parsed.SessionID != "session-1" {
		t.Errorf("expected session session-1, got %s", parsed.SessionID)
	}

	// 解析 Data
	var chatData ChatData
	if err := parsed.ParseData(&chatData); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}
	if chatData.Message != "hello" {
		t.Errorf("expected message 'hello', got '%s'", chatData.Message)
	}
	if !chatData.Stream {
		t.Error("expected stream=true")
	}
}

// TestMessageTypes 测试各种消息类型
func TestMessageTypes(t *testing.T) {
	types := []struct {
		msgType MessageType
		data    interface{}
	}{
		{TypeStreamChunk, StreamChunkData{Content: "hello", Done: false}},
		{TypeStreamEnd, StreamEndData{FullResponse: "hello world", Iterations: 1}},
		{TypeReasoning, ReasoningData{Summary: "Analyzing the request", Round: 1, Stage: "start"}},
		{TypeToolCall, ToolCallData{Name: "search", Phase: "start"}},
		{TypeToolResult, ToolResultData{Name: "search", Success: true, Output: "result"}},
		{TypeStatus, StatusData{State: "thinking", Message: "processing"}},
		{TypeError, ErrorData{Code: "ERR001", Message: "something went wrong"}},
		{TypePong, nil},
	}

	for _, tc := range types {
		msg, err := NewMessage(tc.msgType, "test-session", tc.data)
		if err != nil {
			t.Errorf("NewMessage(%s) error: %v", tc.msgType, err)
			continue
		}
		if msg.Type != tc.msgType {
			t.Errorf("expected type %s, got %s", tc.msgType, msg.Type)
		}
	}
}

// TestHubCreate 测试 Hub 创建
func TestHubCreate(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	if hub == nil {
		t.Fatal("expected non-nil hub")
	}
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", hub.ClientCount())
	}
	if hub.SessionCount() != 0 {
		t.Errorf("expected 0 sessions, got %d", hub.SessionCount())
	}
}

// TestHubRegisterUnregister 测试客户端注册和注销
func TestHubRegisterUnregister(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	// 创建测试服务器
	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=test-session"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	// 等待注册
	time.Sleep(100 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}
	if hub.SessionCount() != 1 {
		t.Errorf("expected 1 session, got %d", hub.SessionCount())
	}

	// 关闭连接
	ws.Close()
	time.Sleep(200 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", hub.ClientCount())
	}
}

// TestHubBroadcast 测试广播消息
func TestHubBroadcast(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=broadcast-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	// 发送广播消息
	msg, _ := NewMessage(TypeStatus, "broadcast-test", StatusData{
		State:   "idle",
		Message: "test broadcast",
	})
	hub.SendToSession("broadcast-test", msg)

	// 读取消息
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if received.Type != TypeStatus {
		t.Errorf("expected type %s, got %s", TypeStatus, received.Type)
	}
}

// TestPingPong 测试心跳
func TestPingPong(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=ping-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	// 发送 ping
	pingMsg, _ := NewMessage(TypePing, "ping-test", nil)
	data, _ := json.Marshal(pingMsg)
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WriteMessage error: %v", err)
	}

	// 读取 pong
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if received.Type != TypePong {
		t.Errorf("expected pong, got %s", received.Type)
	}
}

// TestHubStats 测试统计信息
func TestHubStats(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=stats-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	stats := hub.GetStats()
	if stats.TotalConns < 1 {
		t.Errorf("expected TotalConns >= 1, got %d", stats.TotalConns)
	}
	if stats.ActiveConns != 1 {
		t.Errorf("expected ActiveConns=1, got %d", stats.ActiveConns)
	}
}

// TestMultipleClients 测试多客户端
func TestMultipleClients(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	// 同一 session 连接两个客户端
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=multi-test"

	ws1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial 1 error: %v", err)
	}
	defer ws1.Close()

	ws2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial 2 error: %v", err)
	}
	defer ws2.Close()

	time.Sleep(200 * time.Millisecond)

	if hub.ClientCount() != 2 {
		t.Errorf("expected 2 clients, got %d", hub.ClientCount())
	}
	if hub.SessionCount() != 1 {
		t.Errorf("expected 1 session, got %d", hub.SessionCount())
	}

	// 广播到 session，两个客户端都应该收到
	msg, _ := NewMessage(TypeStatus, "multi-test", StatusData{State: "test"})
	hub.SendToSession("multi-test", msg)

	time.Sleep(200 * time.Millisecond)

	// 两个客户端都应收到消息
	ws1.SetReadDeadline(time.Now().Add(2 * time.Second))
	ws2.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, raw1, err := ws1.ReadMessage()
	if err != nil {
		t.Errorf("ws1 ReadMessage error: %v", err)
	}
	_, raw2, err := ws2.ReadMessage()
	if err != nil {
		t.Errorf("ws2 ReadMessage error: %v", err)
	}

	var msg1, msg2 Message
	json.Unmarshal(raw1, &msg1)
	json.Unmarshal(raw2, &msg2)

	if msg1.Type != TypeStatus || msg2.Type != TypeStatus {
		t.Error("both clients should receive status message")
	}
}

// TestReconnect 测试断线重连消息
func TestReconnect(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=reconnect-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	// 发送重连消息
	reconnMsg, _ := NewMessage(TypeReconnect, "reconnect-test", ReconnectData{
		LastMessageID: "msg-123",
	})
	data, _ := json.Marshal(reconnMsg)
	if err := ws.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("WriteMessage error: %v", err)
	}

	// 应该收到 connected 状态
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, raw, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(raw, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if received.Type != TypeStatus {
		t.Errorf("expected status message, got %s", received.Type)
	}
}

// TestGenerateID 测试 ID 生成
func TestGenerateID(t *testing.T) {
	id1 := generateID()
	id2 := generateID()
	if id1 == id2 {
		t.Error("expected different IDs")
	}
	if !strings.HasPrefix(id1, "ws-") {
		t.Errorf("expected ID prefix 'ws-', got %s", id1)
	}
}

// TestDefaultHubConfig 测试默认配置
func TestDefaultHubConfig(t *testing.T) {
	cfg := DefaultHubConfig()
	if cfg.WriteWait != 10*time.Second {
		t.Errorf("expected WriteWait 10s, got %v", cfg.WriteWait)
	}
	if cfg.PongWait != 60*time.Second {
		t.Errorf("expected PongWait 60s, got %v", cfg.PongWait)
	}
	if cfg.PingPeriod != 54*time.Second {
		t.Errorf("expected PingPeriod 54s, got %v", cfg.PingPeriod)
	}
	if cfg.MaxMessageSize != 64*1024 {
		t.Errorf("expected MaxMessageSize 64KB, got %d", cfg.MaxMessageSize)
	}
}

// TestAgentHandler 测试 AgentHandler
func TestAgentHandler(t *testing.T) {
	handler := &AgentHandler{
		pending: make(map[string]context.CancelFunc),
	}

	// 测试 CancelSession
	if handler.PendingCount() != 0 {
		t.Errorf("expected 0 pending, got %d", handler.PendingCount())
	}

	// CancelSession 对不存在的 session 应该不 panic
	handler.CancelSession("nonexistent")
}

// TestHubStop 测试 Hub 停止
func TestHubStop(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()

	// 停止 Hub
	hub.Stop()
	time.Sleep(100 * time.Millisecond)

	// Hub 应该已停止，不再接受新连接
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after stop, got %d", hub.ClientCount())
	}
}
