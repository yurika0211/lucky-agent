package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/tool"
)

// createTestAgent 创建测试用 Agent（使用临时目录，不污染生产数据）
func createTestAgent(t *testing.T) *agent.Agent {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Stream bool `json:"stream"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n")
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	t.Cleanup(upstream.Close)

	tmpDir := t.TempDir()
	mgr, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	if err := mgr.Set("provider", "openai"); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	if err := mgr.Set("api_key", "sk-test"); err != nil {
		t.Fatalf("set api_key: %v", err)
	}
	if err := mgr.Set("api_base", upstream.URL); err != nil {
		t.Fatalf("set api_base: %v", err)
	}
	if err := mgr.Set("model", "gpt-test"); err != nil {
		t.Fatalf("set model: %v", err)
	}
	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestNew(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	s := New(a, cfg)

	if s == nil {
		t.Fatal("server should not be nil")
	}
	if s.config.Addr != "127.0.0.1:9090" {
		t.Errorf("expected addr 127.0.0.1:9090, got %s", s.config.Addr)
	}
	if s.config.RateLimit != 60 {
		t.Errorf("expected rate limit 60, got %d", s.config.RateLimit)
	}
	if s.rateLimiter == nil {
		t.Error("rate limiter should not be nil")
	}
}

func TestDefaultServerConfig(t *testing.T) {
	cfg := DefaultServerConfig()
	if cfg.Addr != "127.0.0.1:9090" {
		t.Errorf("expected 127.0.0.1:9090, got %s", cfg.Addr)
	}
	if cfg.EnableCORS {
		t.Error("CORS should be disabled by default (secure default)")
	}
	if cfg.RateLimit != 60 {
		t.Errorf("expected 60, got %d", cfg.RateLimit)
	}
}

func TestHandleHealth(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("expected ok, got %v", resp["status"])
	}
	if resp["version"] != "v0.21.0" {
		t.Errorf("expected v0.21.0, got %v", resp["version"])
	}
}

func TestHandleHealthMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleRoot(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.handleRoot(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["name"] != "LuckyHarness API" {
		t.Errorf("expected LuckyHarness API, got %v", resp["name"])
	}
}

func TestHandleMemoryStats(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/stats", nil)
	w := httptest.NewRecorder()
	s.handleMemoryStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleMemorySave(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]interface{}{
		"content":  "test memory content",
		"category": "test",
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleMemory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleMemorySaveLongTerm(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]interface{}{
		"content":   "long term memory",
		"category":  "identity",
		"long_term": true,
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleMemory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleMemoryEmptyContent(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]interface{}{
		"content": "",
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleMemory(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleMemoryRecall(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/recall?q=test", nil)
	w := httptest.NewRecorder()
	s.handleMemoryRecall(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleMemoryRecallNoQuery(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/memory/recall", nil)
	w := httptest.NewRecorder()
	s.handleMemoryRecall(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleTools(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil)
	w := httptest.NewRecorder()
	s.handleTools(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	count, ok := resp["count"].(float64)
	if !ok || count < 1 {
		t.Errorf("expected at least 1 tool, got %v", resp["count"])
	}
}

func TestHandleStats(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	s.handleStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["version"] != "v0.21.0" {
		t.Errorf("expected v0.21.0, got %v", resp["version"])
	}
	if _, ok := resp["context_cache"]; !ok {
		t.Error("expected context_cache in response")
	}
}

func TestHandleSoul(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/soul", nil)
	w := httptest.NewRecorder()
	s.handleSoul(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleSessions(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	a.Sessions().Ensure("session-one").SetTitle("Session One")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	w := httptest.NewRecorder()
	s.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	sessions, ok := resp["sessions"].([]any)
	if !ok {
		t.Fatalf("expected sessions array, got %T", resp["sessions"])
	}
	if len(sessions) == 0 {
		t.Fatal("expected at least one session")
	}
	first, ok := sessions[0].(map[string]any)
	if !ok {
		t.Fatalf("expected session object, got %T", sessions[0])
	}
	if _, ok := first["title"]; !ok {
		t.Fatal("expected title in session payload")
	}
}

func TestHandleSessionsSearch(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	sess := a.Sessions().Ensure("resume-target")
	sess.SetTitle("Resume Target")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?q=resume", nil)
	w := httptest.NewRecorder()
	s.handleSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	sessions, ok := resp["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Fatalf("expected one matched session, got %v", resp["sessions"])
	}
	first, ok := sessions[0].(map[string]any)
	if !ok {
		t.Fatalf("expected session object, got %T", sessions[0])
	}
	if first["title"] != "Resume Target" {
		t.Fatalf("expected title to match, got %v", first["title"])
	}
}

func TestHandleChatSyncEmptyMessage(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]interface{}{
		"message": "",
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sync", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChatSync(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleChatSyncInvalidBody(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sync", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChatSync(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleChatSyncRunsLoopOncePerRequest(t *testing.T) {
	var callCount atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	mgr, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	if err := mgr.Set("provider", "openai"); err != nil {
		t.Fatalf("set provider: %v", err)
	}
	if err := mgr.Set("api_key", "sk-test"); err != nil {
		t.Fatalf("set api_key: %v", err)
	}
	if err := mgr.Set("api_base", upstream.URL); err != nil {
		t.Fatalf("set api_base: %v", err)
	}
	if err := mgr.Set("model", "gpt-test"); err != nil {
		t.Fatalf("set model: %v", err)
	}

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	s := New(a, DefaultServerConfig())

	body := map[string]any{
		"message": "hello",
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sync", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChatSync(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", w.Code, w.Body.String())
	}
	if got := callCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 upstream call, got %d", got)
	}
}

// ===== 中间件测试 =====

func TestCORSMiddleware(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	cfg.EnableCORS = true
	cfg.CORSOrigins = []string{"http://localhost:3000"} // 显式配置允许的源
	s := New(a, cfg)

	handler := s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// OPTIONS 预检
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Error("CORS origin header should be set")
	}
}

func TestCORSMiddlewareDisabled(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	cfg.EnableCORS = false
	s := New(a, cfg)

	handler := s.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// CORS 禁用时，OPTIONS 不应返回 204（无预检响应）
	if w.Code == http.StatusNoContent {
		t.Error("CORS disabled, OPTIONS should not return 204")
	}
	// 应该直接传给 next handler，返回 200
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (passthrough), got %d", w.Code)
	}
	// 不应有 CORS headers
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS headers should not be set when CORS is disabled")
	}
}

func TestAuthMiddlewareNoKeys(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	cfg.APIKeys = nil // 无 API Key
	s := New(a, cfg)

	called := false
	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// 来自 localhost 的请求应被允许
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("handler should be called for localhost when no API keys configured")
	}

	// 来自外部 IP 的请求应被拒绝
	called = false
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if called {
		t.Error("handler should NOT be called for external IP when no API keys configured")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for external IP, got %d", w.Code)
	}
}

func TestAuthMiddlewareWithKeys(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	cfg.APIKeys = []string{"test-key-123"}
	s := New(a, cfg)

	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 无 API Key
	req := httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	// 错误 API Key
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("X-API-Key", "wrong-key")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}

	// 正确 API Key (Header)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("X-API-Key", "test-key-123")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// 正确 API Key (Bearer)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer test-key-123")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Query string API Key 不再支持（安全原因）
	req = httptest.NewRequest(http.MethodGet, "/api/v1/stats?api_key=test-key-123", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for query string api_key (no longer supported), got %d", w.Code)
	}
}

func TestAuthMiddlewareHealthBypass(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	cfg.APIKeys = []string{"test-key-123"}
	s := New(a, cfg)

	handler := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 健康检查不需要认证
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("health endpoint should bypass auth, got %d", w.Code)
	}

	// 根路由不需要认证
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("root endpoint should bypass auth, got %d", w.Code)
	}
}

func TestRateLimiter(t *testing.T) {
	rl := newRateLimiter(3)

	for i := 0; i < 3; i++ {
		if !rl.Allow("127.0.0.1") {
			t.Errorf("request %d should be allowed", i+1)
		}
	}

	if rl.Allow("127.0.0.1") {
		t.Error("4th request should be rate limited")
	}

	// 不同 IP 应该独立计数
	if !rl.Allow("192.168.1.1") {
		t.Error("different IP should be allowed")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	a := createTestAgent(t)
	cfg := DefaultServerConfig()
	cfg.RateLimit = 2
	s := New(a, cfg)

	handler := s.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// 前两个请求应该通过
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d should be allowed, got %d", i+1, w.Code)
		}
	}

	// 第三个应该被限流
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	handler := s.recoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestServerStartStop(t *testing.T) {
	a := createTestAgent(t)
	cfg := ServerConfig{
		Addr:       ":0", // 随机端口
		EnableCORS: true,
		RateLimit:  60,
	}
	s := New(a, cfg)

	if err := s.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}

	if !s.IsRunning() {
		t.Error("server should be running")
	}

	// 重复启动应报错
	if err := s.Start(); err == nil {
		t.Error("starting already running server should fail")
	}

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	if err := s.Stop(); err != nil {
		t.Fatalf("stop server: %v", err)
	}

	if s.IsRunning() {
		t.Error("server should not be running after stop")
	}
}

func TestServerStats(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	stats := s.Stats()
	if stats.TotalReqs != 0 {
		t.Errorf("initial total requests should be 0, got %d", stats.TotalReqs)
	}
	if stats.StartTime.IsZero() {
		t.Error("start time should not be zero")
	}
}

func TestChatEventTypeString(t *testing.T) {
	tests := []struct {
		input    agent.ChatEventType
		expected string
	}{
		{agent.ChatEventThinking, "reason"},
		{agent.ChatEventToolCall, "act"},
		{agent.ChatEventToolResult, "observe"},
		{agent.ChatEventContent, "content"},
		{agent.ChatEventDone, "done"},
		{agent.ChatEventError, "error"},
		{99, "unknown"}, // Unknown
	}

	for _, tt := range tests {
		result := chatEventTypeString(tt.input)
		if result != tt.expected {
			t.Errorf("chatEventTypeString(%d) = %s, want %s", tt.input, result, tt.expected)
		}
	}
}

func TestPermString(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected string
	}{
		{"auto", 0, "auto"},
		{"approve", 1, "approve"},
		{"deny", 2, "deny"},
		{"unknown", 99, "unknown"},
	}

	for _, tt := range tests {
		result := permString(tool.PermissionLevel(tt.input))
		if result != tt.expected {
			t.Errorf("permString(%s) = %s, want %s", tt.name, result, tt.expected)
		}
	}
}

func TestSendJSON(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	w := httptest.NewRecorder()
	s.sendJSON(w, http.StatusOK, map[string]string{"hello": "world"})

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("expected json content type, got %s", ct)
	}
}

func TestSendError(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	w := httptest.NewRecorder()
	s.sendError(w, "test error", http.StatusBadRequest, "details")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp ErrorResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error != "test error" {
		t.Errorf("expected 'test error', got %s", resp.Error)
	}
	if resp.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.Code)
	}
	if resp.Details != "details" {
		t.Errorf("expected 'details', got %s", resp.Details)
	}
}

// --- RAG Handler Tests ---

func TestHandleRAGIndexText(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]string{
		"source":  "test-doc",
		"title":   "Test Document",
		"content": "This is a test document for RAG indexing. It contains some text to be chunked and embedded.",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleRAGIndex(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["action"] != "index_text" {
		t.Errorf("expected action=index_text, got %v", resp["action"])
	}
	if resp["doc_id"] == nil {
		t.Error("expected doc_id to be set")
	}
}

func TestHandleRAGIndexMissingFields(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]string{}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleRAGIndex(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRAGIndexMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag/index", nil)
	w := httptest.NewRecorder()

	s.handleRAGIndex(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleRAGIndexDelete(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	// First index a document
	body := map[string]string{
		"source":  "delete-test",
		"title":   "Delete Test",
		"content": "Document to be deleted from the index.",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRAGIndex(w, req)

	var indexResp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&indexResp)
	docID, _ := indexResp["doc_id"].(string)

	// Now delete it
	delBody := map[string]string{"doc_id": docID}
	b, _ = json.Marshal(delBody)

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/rag/index", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.handleRAGIndex(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if removed, ok := resp["removed"].(bool); !ok || !removed {
		t.Errorf("expected removed=true, got %v", resp["removed"])
	}
}

func TestHandleRAGSearch(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	// Index a document first
	body := map[string]string{
		"source":  "search-test",
		"title":   "Search Test",
		"content": "Go is a statically typed compiled programming language designed at Google.",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRAGIndex(w, req)

	// Now search
	searchBody := map[string]interface{}{
		"query": "programming language",
		"top_k": 3,
	}
	b, _ = json.Marshal(searchBody)

	req = httptest.NewRequest(http.MethodPost, "/api/v1/rag/search", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.handleRAGSearch(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["query"] != "programming language" {
		t.Errorf("expected query=programming language, got %v", resp["query"])
	}
}

func TestHandleRAGSearchNoQuery(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]string{}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/search", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleRAGSearch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRAGSearchMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag/search", nil)
	w := httptest.NewRecorder()

	s.handleRAGSearch(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleRAGStats(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	// Index a document first
	body := map[string]string{
		"source":  "stats-test",
		"title":   "Stats Test",
		"content": "Some content for stats testing.",
	}
	b, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/index", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRAGIndex(w, req)

	// Get stats
	req = httptest.NewRequest(http.MethodGet, "/api/v1/rag/stats", nil)
	w = httptest.NewRecorder()
	s.handleRAGStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["document_count"] == nil {
		t.Error("expected document_count to be set")
	}
	if resp["chunk_count"] == nil {
		t.Error("expected chunk_count to be set")
	}
}

func TestHandleRAGStatsMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/stats", nil)
	w := httptest.NewRecorder()

	s.handleRAGStats(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
