//go:build !integration
// +build !integration

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/metrics"
)

// v0.69.0: server 包测试补全 - 覆盖 handleChat 0% 函数

// TestHandleChatMethodNotAllowed 测试 handleChat 拒绝非 POST 请求
func TestHandleChatMethodNotAllowed(t *testing.T) {
	s := &Server{}

	// GET 请求
	req := httptest.NewRequest(http.MethodGet, "/chat", nil)
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("handleChat GET: expected status 405, got %d", resp.StatusCode)
	}
}

// TestHandleChatInvalidJSON 测试 handleChat 处理无效 JSON
func TestHandleChatInvalidJSON(t *testing.T) {
	s := &Server{
		metrics: metrics.NewMetrics(),
		stats:   ServerStats{StartTime: time.Now()},
	}

	// 无效 JSON
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{invalid}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("handleChat invalid JSON: expected status 400, got %d", resp.StatusCode)
	}
}

// TestHandleChatEmptyMessage 测试 handleChat 拒绝空消息
func TestHandleChatEmptyMessage(t *testing.T) {
	s := &Server{
		metrics: metrics.NewMetrics(),
		stats:   ServerStats{StartTime: time.Now()},
	}

	// 空消息
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": ""}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("handleChat empty message: expected status 400, got %d", resp.StatusCode)
	}
}

// TestHandleChatSyncFallback 测试 handleChat 降级为同步响应 (不支持 SSE)
func TestHandleChatSyncFallback(t *testing.T) {
	// 创建 mock agent
	a := createTestAgent(t)
	s := &Server{
		agent:   a,
		metrics: metrics.NewMetrics(),
		stats:   ServerStats{StartTime: time.Now()},
	}

	// 普通消息触发同步响应
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "hello"}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("handleChat hello: expected status 200, got %d", resp.StatusCode)
	}

	// 验证响应非空
	body := w.Body.String()
	if len(body) == 0 {
		t.Errorf("handleChat hello: expected non-empty response")
	}
}

// TestHandleChatWithMaxIter 测试 handleChat 处理 max_iter 参数
func TestHandleChatWithMaxIter(t *testing.T) {
	// 创建 mock agent
	a := createTestAgent(t)
	s := &Server{
		agent:   a,
		metrics: metrics.NewMetrics(),
		stats:   ServerStats{StartTime: time.Now()},
	}

	// 带 max_iter 参数
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "test", "max_iter": 5}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("handleChat with max_iter: expected status 200, got %d", resp.StatusCode)
	}
}

// TestHandleChatWithAutoApprove 测试 handleChat 处理 auto_approve 参数
func TestHandleChatWithAutoApprove(t *testing.T) {
	// 创建 mock agent
	a := createTestAgent(t)
	s := &Server{
		agent:   a,
		metrics: metrics.NewMetrics(),
		stats:   ServerStats{StartTime: time.Now()},
	}

	// 带 auto_approve 参数
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "test", "auto_approve": true}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("handleChat with auto_approve: expected status 200, got %d", resp.StatusCode)
	}
}

// TestHandleChatMetrics 测试 handleChat 记录 metrics
func TestHandleChatMetrics(t *testing.T) {
	// 创建 mock agent
	a := createTestAgent(t)
	m := metrics.NewMetrics()
	s := &Server{
		agent:   a,
		metrics: m,
		stats:   ServerStats{StartTime: time.Now()},
	}

	// 发送请求
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "/help"}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	// 验证 metrics 被记录
	snapshot := m.Snapshot()
	if snapshot.ChatRequests == 0 {
		t.Errorf("handleChat: expected metrics.ChatRequests > 0, got %d", snapshot.ChatRequests)
	}
}

// TestHandleChatServerStats 测试 handleChat 更新 server stats
func TestHandleChatServerStats(t *testing.T) {
	// 创建 mock agent
	a := createTestAgent(t)
	s := &Server{
		agent: a,
		metrics: metrics.NewMetrics(),
		stats:   ServerStats{StartTime: time.Now()},
	}

	initialReqs := s.stats.ChatReqs

	// 发送请求
	req := httptest.NewRequest(http.MethodPost, "/chat", strings.NewReader(`{"message": "/help"}`))
	w := httptest.NewRecorder()

	s.handleChat(w, req)

	// 验证 stats 被更新
	if s.stats.ChatReqs <= initialReqs {
		t.Errorf("handleChat: expected stats.ChatReqs to increase, got %d", s.stats.ChatReqs)
	}
}
