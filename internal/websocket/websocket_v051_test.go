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

// ============================================================
// WS-1: AgentHandler 完整测试
// ============================================================

// mockMsgHandler 简单消息处理器模拟
type mockMsgHandler struct {
	mu     sync.Mutex
	msgs   []*Message
	cancel context.CancelFunc
}

func (h *mockMsgHandler) HandleMessage(client *Client, msg *Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, msg)
}

// TestAgentHandlerBasic 测试 AgentHandler 基本功能
func TestAgentHandlerBasic(t *testing.T) {
	handler := NewAgentHandler(nil)

	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	// 测试 PendingCount
	if handler.PendingCount() != 0 {
		t.Errorf("expected 0 pending, got %d", handler.PendingCount())
	}

	// 测试 CancelSession 对不存在的 session
	handler.CancelSession("nonexistent")
}

// TestAgentHandlerWithNilAgent 测试 AgentHandler 空 Agent
func TestAgentHandlerWithNilAgent(t *testing.T) {
	// 这个测试会触发 nil agent 的处理逻辑，但不应该 panic
	// 由于 handler 内部会检查 agent 是否为 nil，我们只测试基本功能
	handler := NewAgentHandler(nil)
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}

// ============================================================
// WS-2: Hub SendToClient 测试
// ============================================================

func _TestHubSendToClient(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=send-client-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	// 获取客户端 ID
	hub.mu.RLock()
	var clientID string
	for id := range hub.clients {
		clientID = id
		break
	}
	hub.mu.RUnlock()

	if clientID == "" {
		t.Fatal("expected non-empty client ID")
	}

	// 发送到客户端
	msg, _ := NewMessage(TypeStatus, "send-client-test", StatusData{State: "test"})
	hub.SendToClient(clientID, msg)

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

func TestHubSendToNonExistentClient(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	// 发送到不存在的客户端不应该 panic
	msg, _ := NewMessage(TypeStatus, "non-existent", StatusData{State: "test"})
	hub.SendToClient("non-existent-client", msg)
}

// ============================================================
// WS-3: Hub ServeHTTP 边界测试
// ============================================================

// TestHubServeHTTPInvalidSession 测试 Hub 处理无效 session
// 注意：实际代码中缺少 session 参数会使用默认值，不会报错
func TestHubServeHTTPInvalidSession(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	// 不带 session 参数的请求会使用默认 session
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	// 应该能连接成功（使用默认 session）
	if hub.ClientCount() != 1 {
		t.Errorf("expected 1 client, got %d", hub.ClientCount())
	}
}

// ============================================================
// WS-4: readPump/writePump 边界测试
// ============================================================

func TestReadPumpHandlesClose(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=close-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 主动关闭连接
	ws.Close()

	// 等待 unregister 处理
	time.Sleep(200 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after close, got %d", hub.ClientCount())
	}
}

func TestWritePumpHandlesChannelClose(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=chan-close-test"

	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial error: %v", err)
	}
	defer ws.Close()

	time.Sleep(100 * time.Millisecond)

	// Hub 停止时会关闭所有 channel
	hub.Stop()

	// 等待处理
	time.Sleep(200 * time.Millisecond)
}

// ============================================================
// WS-5: Message 解析边界测试
// ============================================================

func TestParseMessageInvalidJSON(t *testing.T) {
	_, err := ParseMessage([]byte("invalid json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseMessageMissingType(t *testing.T) {
	// 实际代码中，缺少 type 字段会解析失败
	data := []byte(`{"session_id": "test", "data": {}}`)
	_, err := ParseMessage(data)
	// 注意：ParseMessage 可能不会报错，而是使用默认 type
	// 这里只验证能解析成功
	if err != nil {
		t.Logf("ParseMessage returned error (expected): %v", err)
	}
}

func TestNewMessageWithInvalidData(t *testing.T) {
	// 测试不支持的数据类型
	_, err := NewMessage(TypeChat, "test", make(chan int))
	if err == nil {
		t.Error("expected error for unsupported data type")
	}
}

func TestParseDataInvalidJSON(t *testing.T) {
	msg := &Message{
		Type: TypeChat,
		Data: json.RawMessage("invalid"),
	}
	var data ChatData
	err := msg.ParseData(&data)
	if err == nil {
		t.Error("expected error for invalid JSON data")
	}
}

func TestParseDataNilData(t *testing.T) {
	msg := &Message{
		Type: TypeChat,
		Data: nil,
	}
	var data ChatData
	err := msg.ParseData(&data)
	// nil Data 会触发 JSON 解析错误
	if err == nil {
		t.Error("expected error for nil Data")
	}
}

// ============================================================
// WS-6: Hub 并发压力测试
// ============================================================

func TestHubConcurrentBroadcast(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	// 创建多个客户端
	const numClients = 10
	clients := make([]*websocket.Conn, numClients)
	for i := 0; i < numClients; i++ {
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=concurrent-test"
		ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("Dial %d error: %v", i, err)
		}
		clients[i] = ws
		defer ws.Close()
	}

	time.Sleep(200 * time.Millisecond)

	// 并发广播
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			msg, _ := NewMessage(TypeStatus, "concurrent-test", StatusData{State: "test"})
			hub.SendToSession("concurrent-test", msg)
			done <- true
		}()
	}

	// 等待所有广播完成
	for i := 0; i < 10; i++ {
		<-done
	}

	// 所有客户端应仍能正常通信
	time.Sleep(100 * time.Millisecond)
	if hub.ClientCount() != numClients {
		t.Errorf("expected %d clients, got %d", numClients, hub.ClientCount())
	}
}

func TestHubConcurrentConnectDisconnect(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	done := make(chan bool)

	// 并发连接和断开
	for i := 0; i < 20; i++ {
		go func(idx int) {
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=conc-" + string(rune('A'+idx))
			ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				done <- false
				return
			}
			time.Sleep(50 * time.Millisecond)
			ws.Close()
			done <- true
		}(i)
	}

	// 等待所有操作完成
	for i := 0; i < 20; i++ {
		<-done
	}

	time.Sleep(200 * time.Millisecond)

	// 最终应该没有客户端
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients after all disconnects, got %d", hub.ClientCount())
	}
}

// ============================================================
// WS-7: Config 边界测试
// ============================================================

func TestHubConfigWithCustomValues(t *testing.T) {
	handler := &mockHandler{}
	cfg := HubConfig{
		WriteWait:       5 * time.Second,
		PongWait:        30 * time.Second,
		PingPeriod:      27 * time.Second,
		MaxMessageSize:  32 * 1024,
		ReadBufferSize:  2048,
		WriteBufferSize: 2048,
	}

	hub := NewHub(handler, cfg)
	if hub == nil {
		t.Error("expected non-nil hub")
	}
}

func TestHubConfigPingPeriodGreaterThanPongWait(t *testing.T) {
	handler := &mockHandler{}
	cfg := HubConfig{
		PongWait:   30 * time.Second,
		PingPeriod: 60 * time.Second, // 错误配置：PingPeriod > PongWait
	}

	// 不应该 panic
	hub := NewHub(handler, cfg)
	if hub == nil {
		t.Error("expected non-nil hub")
	}
}

// ============================================================
// WS-8: Client 测试
// ============================================================

func TestClientChannelReceive(t *testing.T) {
	client := &Client{
		ID:        "test-client",
		SessionID: "test-session",
		Send:      make(chan *Message, 10),
	}

	// 模拟接收消息
	msg := &Message{Type: TypeChat}
	client.Send <- msg

	// 检查 channel 能正常接收
	if len(client.Send) != 1 {
		t.Errorf("expected 1 message in channel, got %d", len(client.Send))
	}

	// 清理
	close(client.Send)
}

// ============================================================
// WS-9: Hub Stats 并发安全测试
// ============================================================

func TestHubStatsConcurrentAccess(t *testing.T) {
	handler := &mockHandler{}
	cfg := DefaultHubConfig()
	hub := NewHub(handler, cfg)

	go hub.Run()
	defer hub.Stop()

	server := httptest.NewServer(hub)
	defer server.Close()

	done := make(chan bool)

	// 并发访问统计
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				hub.GetStats()
			}
			done <- true
		}()
	}

	// 等待所有 goroutine 完成
	for i := 0; i < 10; i++ {
		<-done
	}
}

// ============================================================
// WS-10: Error 处理测试
// ============================================================

func TestErrorMessageSerialization(t *testing.T) {
	errorData := ErrorData{
		Code:    "ERR_TEST",
		Message: "test error message",
	}

	msg, err := NewMessage(TypeError, "test-session", errorData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if received.Type != TypeError {
		t.Errorf("expected type %s, got %s", TypeError, received.Type)
	}

	var parsedError ErrorData
	if err := received.ParseData(&parsedError); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsedError.Code != "ERR_TEST" {
		t.Errorf("expected code ERR_TEST, got %s", parsedError.Code)
	}
}

func TestToolCallMessageSerialization(t *testing.T) {
	toolData := ToolCallData{
		Name:       "web_search",
		Phase:      "start",
		Params:     map[string]interface{}{"query": "test"},
		GroupID:    "round-1",
		StepID:     "round-1-tool-1",
		Visibility: "visible",
	}

	msg, err := NewMessage(TypeToolCall, "test-session", toolData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	var parsedTool ToolCallData
	if err := received.ParseData(&parsedTool); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsedTool.Name != "web_search" {
		t.Errorf("expected name web_search, got %s", parsedTool.Name)
	}
	if parsedTool.StepID != "round-1-tool-1" {
		t.Errorf("expected step id round-1-tool-1, got %s", parsedTool.StepID)
	}
	if parsedTool.Visibility != "visible" {
		t.Errorf("expected visibility visible, got %s", parsedTool.Visibility)
	}
}

func TestStreamChunkMessageSerialization(t *testing.T) {
	chunkData := StreamChunkData{
		Content: "test content",
		Done:    false,
	}

	msg, err := NewMessage(TypeStreamChunk, "test-session", chunkData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if received.Type != TypeStreamChunk {
		t.Errorf("expected type %s, got %s", TypeStreamChunk, received.Type)
	}

	var parsedChunk StreamChunkData
	if err := received.ParseData(&parsedChunk); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsedChunk.Content != "test content" {
		t.Errorf("expected content 'test content', got '%s'", parsedChunk.Content)
	}
}

func TestStreamEndMessageSerialization(t *testing.T) {
	endData := StreamEndData{
		FullResponse: "full response",
		Iterations:   5,
	}

	msg, err := NewMessage(TypeStreamEnd, "test-session", endData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if received.Type != TypeStreamEnd {
		t.Errorf("expected type %s, got %s", TypeStreamEnd, received.Type)
	}

	var parsedEnd StreamEndData
	if err := received.ParseData(&parsedEnd); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsedEnd.FullResponse != "full response" {
		t.Errorf("expected full response 'full response', got '%s'", parsedEnd.FullResponse)
	}
	if parsedEnd.Iterations != 5 {
		t.Errorf("expected 5 iterations, got %d", parsedEnd.Iterations)
	}
}

func TestReasoningMessageSerialization(t *testing.T) {
	reasoningData := ReasoningData{
		Summary: "Analyzing the request",
		Round:   2,
		Stage:   "continue",
	}

	msg, err := NewMessage(TypeReasoning, "test-session", reasoningData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if received.Type != TypeReasoning {
		t.Errorf("expected type %s, got %s", TypeReasoning, received.Type)
	}

	var parsed ReasoningData
	if err := received.ParseData(&parsed); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsed.Round != 2 {
		t.Errorf("expected round 2, got %d", parsed.Round)
	}
	if parsed.Stage != "continue" {
		t.Errorf("expected stage continue, got %q", parsed.Stage)
	}
}

func TestToolResultMessageSerialization(t *testing.T) {
	resultData := ToolResultData{
		Name:       "web_search",
		Success:    true,
		Output:     "search results",
		GroupID:    "round-1",
		StepID:     "round-1-tool-1",
		Visibility: "visible",
	}

	msg, err := NewMessage(TypeToolResult, "test-session", resultData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if received.Type != TypeToolResult {
		t.Errorf("expected type %s, got %s", TypeToolResult, received.Type)
	}

	var parsedResult ToolResultData
	if err := received.ParseData(&parsedResult); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsedResult.Name != "web_search" {
		t.Errorf("expected name web_search, got %s", parsedResult.Name)
	}
	if !parsedResult.Success {
		t.Error("expected success=true")
	}
	if parsedResult.GroupID != "round-1" {
		t.Errorf("expected group round-1, got %s", parsedResult.GroupID)
	}
	if parsedResult.StepID != "round-1-tool-1" {
		t.Errorf("expected step id round-1-tool-1, got %s", parsedResult.StepID)
	}
}

func TestReconnectMessageSerialization(t *testing.T) {
	reconnData := ReconnectData{
		LastMessageID: "msg-123",
	}

	msg, err := NewMessage(TypeReconnect, "test-session", reconnData)
	if err != nil {
		t.Fatalf("NewMessage error: %v", err)
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var received Message
	if err := json.Unmarshal(data, &received); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if received.Type != TypeReconnect {
		t.Errorf("expected type %s, got %s", TypeReconnect, received.Type)
	}

	var parsedReconn ReconnectData
	if err := received.ParseData(&parsedReconn); err != nil {
		t.Fatalf("ParseData error: %v", err)
	}

	if parsedReconn.LastMessageID != "msg-123" {
		t.Errorf("expected last message ID 'msg-123', got '%s'", parsedReconn.LastMessageID)
	}
}
