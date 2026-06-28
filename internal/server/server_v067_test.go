//go:build !integration
// +build !integration

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
)

// v0.67.0: server 包测试补全 - 覆盖 0% 函数

// TestDoChatSyncHelp 测试 doChatSync 处理 /help 命令
func TestDoChatSyncHelp(t *testing.T) {
	// 创建测试服务器
	s := &Server{}

	// 创建请求
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "/help"}`))
	w := httptest.NewRecorder()

	// 调用 doChatSync
	s.doChatSync(w, req, ChatRequest{Message: "/help"}, agent.LoopConfig{}, context.Background(), time.Now())

	// 验证响应
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("doChatSync /help: expected status 200, got %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if !strings.Contains(chatResp.Response, "LuckyAgent") {
		t.Errorf("doChatSync /help: expected response to contain 'LuckyAgent', got '%s'", chatResp.Response)
	}
}

// TestDoChatSyncNew 测试 doChatSync 处理 /new 命令
func TestDoChatSyncNew(t *testing.T) {
	// 创建带 mock agent 的服务器
	a := createTestAgent(t)
	s := &Server{
		agent: a,
	}

	// 创建请求
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "/new"}`))
	w := httptest.NewRecorder()

	// 调用 doChatSync
	s.doChatSync(w, req, ChatRequest{Message: "/new"}, agent.LoopConfig{}, context.Background(), time.Now())

	// 验证响应
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("doChatSync /new: expected status 200, got %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if !strings.Contains(chatResp.Response, "session") {
		t.Errorf("doChatSync /new: expected response to contain 'session', got '%s'", chatResp.Response)
	}
}

// TestDoChatSyncStatus 测试 doChatSync 处理 /status 命令
func TestDoChatSyncStatus(t *testing.T) {
	a := createTestAgent(t)
	s := &Server{
		agent: a,
	}

	// 创建请求
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "/status"}`))
	w := httptest.NewRecorder()

	// 调用 doChatSync
	s.doChatSync(w, req, ChatRequest{Message: "/status"}, agent.LoopConfig{}, context.Background(), time.Now())

	// 验证响应
	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("doChatSync /status: expected status 200, got %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if !strings.Contains(chatResp.Response, "Status") {
		t.Errorf("doChatSync /status: expected response to contain 'Status', got '%s'", chatResp.Response)
	}
}

// TestSendSSEError 测试 sendSSEError 函数
func TestSendSSEError(t *testing.T) {
	// 创建 buffer 和 flusher
	var buf bytes.Buffer
	flusher := &testFlusher{}

	s := &Server{}
	s.sendSSEError(&buf, flusher, "test error message")

	output := buf.String()

	// 验证 SSE 格式
	if !strings.Contains(output, "data:") {
		t.Errorf("sendSSEError: expected SSE format with 'data:', got '%s'", output)
	}

	if !strings.Contains(output, "error") {
		t.Errorf("sendSSEError: expected response to contain 'error', got '%s'", output)
	}

	if !strings.Contains(output, "test error message") {
		t.Errorf("sendSSEError: expected response to contain error message, got '%s'", output)
	}

	// 验证 flusher 被调用
	if !flusher.flushed {
		t.Error("sendSSEError: expected flusher to be called")
	}
}

// TestCleanup 测试 rateLimiter.cleanup 函数
func TestCleanup(t *testing.T) {
	rl := &rateLimiter{
		clients: make(map[string]*clientBucket),
		limit:   10,
	}

	// 添加一个已过期的客户端
	expiredIP := "192.168.1.1"
	rl.clients[expiredIP] = &clientBucket{
		count:   5,
		resetAt: time.Now().Add(-2 * time.Minute), // 已过期
	}

	// 添加一个未过期的客户端
	validIP := "192.168.1.2"
	rl.clients[validIP] = &clientBucket{
		count:   3,
		resetAt: time.Now().Add(2 * time.Minute), // 未过期
	}

	// 执行清理
	rl.cleanup()

	// 验证过期的客户端被删除
	if _, exists := rl.clients[expiredIP]; exists {
		t.Error("cleanup: expected expired client to be removed")
	}

	// 验证未过期的客户端保留
	if _, exists := rl.clients[validIP]; !exists {
		t.Error("cleanup: expected valid client to be retained")
	}
}

// TestRateLimiterAllow 测试 rateLimiter.Allow 函数
func TestRateLimiterAllow(t *testing.T) {
	rl := &rateLimiter{
		clients: make(map[string]*clientBucket),
		limit:   5,
	}

	ip := "192.168.1.1"

	// 测试限流逻辑
	for i := 0; i < 10; i++ {
		allowed := rl.Allow(ip)
		if i < 5 {
			if !allowed {
				t.Errorf("Allow: request %d should be allowed", i+1)
			}
		} else {
			if allowed {
				t.Errorf("Allow: request %d should be rate limited", i+1)
			}
		}
	}
}

// 辅助类型

// testFlusher 用于测试的 mock flusher
type testFlusher struct {
	flushed bool
}

func (f *testFlusher) Flush() {
	f.flushed = true
}

// 确保 testFlusher 实现 http.Flusher
var _ http.Flusher = (*testFlusher)(nil)
