package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/collab"
)

func TestHandleAgentsList(t *testing.T) {
	reg := collab.NewRegistry()
	_ = reg.Register(&collab.AgentProfile{ID: "agent-1", Name: "Agent 1", Status: collab.StatusOnline})
	_ = reg.Register(&collab.AgentProfile{ID: "agent-2", Name: "Agent 2", Status: collab.StatusOffline})

	dm := collab.NewDelegateManager(reg, nil)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	w := httptest.NewRecorder()

	s.handleAgentsList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Agents []collab.AgentProfile `json:"agents"`
		Count  int                   `json:"count"`
		Online int                   `json:"online"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count != 2 {
		t.Errorf("count: got %d, want 2", resp.Count)
	}
	if resp.Online != 1 {
		t.Errorf("online: got %d, want 1", resp.Online)
	}
}

func TestHandleAgentsRegister(t *testing.T) {
	reg := collab.NewRegistry()
	dm := collab.NewDelegateManager(reg, nil)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	body := map[string]any{
		"id":           "test-agent",
		"name":         "Test Agent",
		"description":  "A test agent",
		"capabilities": []string{"chat", "code"},
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAgentsRegister(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusCreated)
	}

	// 验证注册成功
	profile, ok := reg.Get("test-agent")
	if !ok {
		t.Fatal("agent not registered")
	}
	if profile.Name != "Test Agent" {
		t.Errorf("name: got %s, want Test Agent", profile.Name)
	}
}

func TestHandleAgentsDeregister(t *testing.T) {
	reg := collab.NewRegistry()
	_ = reg.Register(&collab.AgentProfile{ID: "agent-1", Name: "Agent 1"})
	dm := collab.NewDelegateManager(reg, nil)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/agents/deregister?id=agent-1", nil)
	w := httptest.NewRecorder()

	s.handleAgentsDeregister(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	// 验证已注销
	_, ok := reg.Get("agent-1")
	if ok {
		t.Error("agent should be deregistered")
	}
}

func TestHandleAgentsDelegate(t *testing.T) {
	reg := collab.NewRegistry()
	_ = reg.Register(&collab.AgentProfile{ID: "agent-1", Name: "Agent 1"})

	// Mock handler
	handler := collab.TaskHandlerFunc(func(ctx context.Context, task *collab.SubTask) (string, error) {
		return "processed: " + task.Input, nil
	})
	dm := collab.NewDelegateManager(reg, handler)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	body := map[string]any{
		"mode":         "parallel",
		"description":  "test task",
		"input":        "hello",
		"agent_ids":    []string{"agent-1"},
		"timeout":      10,
	}
	bodyBytes, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/delegate", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleAgentsDelegate(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusAccepted)
	}

	var task collab.CollabTask
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if task.Mode != collab.ModeParallel {
		t.Errorf("mode: got %s, want parallel", task.Mode)
	}
}

func TestHandleAgentsTasks(t *testing.T) {
	reg := collab.NewRegistry()
	_ = reg.Register(&collab.AgentProfile{ID: "agent-1", Name: "Agent 1"})

	handler := collab.TaskHandlerFunc(func(ctx context.Context, task *collab.SubTask) (string, error) {
		return "done", nil
	})
	dm := collab.NewDelegateManager(reg, handler)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	// 创建一个任务
	_, _ = dm.Delegate(context.Background(), collab.ModeParallel, "test", "input", []string{"agent-1"}, 10*time.Second)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/tasks", nil)
	w := httptest.NewRecorder()

	s.handleAgentsTasks(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Tasks []*collab.CollabTask `json:"tasks"`
		Count int                  `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Count < 1 {
		t.Errorf("count: got %d, want at least 1", resp.Count)
	}
}

func TestHandleAgentsCancel(t *testing.T) {
	reg := collab.NewRegistry()
	_ = reg.Register(&collab.AgentProfile{ID: "agent-1", Name: "Agent 1"})

	// Slow handler
	handler := collab.TaskHandlerFunc(func(ctx context.Context, task *collab.SubTask) (string, error) {
		time.Sleep(5 * time.Second)
		return "done", nil
	})
	dm := collab.NewDelegateManager(reg, handler)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	// 创建一个任务
	task, _ := dm.Delegate(context.Background(), collab.ModeParallel, "test", "input", []string{"agent-1"}, 10*time.Second)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/cancel?id="+task.ID, nil)
	w := httptest.NewRecorder()

	s.handleAgentsCancel(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestHandleAgentsRegister_Errors 测试 handleAgentsRegister 错误分支
func TestHandleAgentsRegister_Errors(t *testing.T) {
	reg := collab.NewRegistry()
	dm := collab.NewDelegateManager(reg, nil)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	// 测试 1: 方法不允许 (GET instead of POST)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/register", nil)
	w := httptest.NewRecorder()
	s.handleAgentsRegister(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("method not allowed: got %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	// 测试 2: 无效 JSON
	body := []byte(`{invalid json}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	w = httptest.NewRecorder()
	s.handleAgentsRegister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid json: got %d, want %d", w.Code, http.StatusBadRequest)
	}

	// 测试 3: 缺少 agent id
	body = []byte(`{"name": "No ID Agent"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	w = httptest.NewRecorder()
	s.handleAgentsRegister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing id: got %d, want %d", w.Code, http.StatusBadRequest)
	}

	// 测试 4: 重复注册
	_ = reg.Register(&collab.AgentProfile{ID: "duplicate-agent", Name: "Duplicate"})
	body = []byte(`{"id": "duplicate-agent", "name": "Another Duplicate"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
	w = httptest.NewRecorder()
	s.handleAgentsRegister(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate registration: got %d, want %d", w.Code, http.StatusConflict)
	}
}

// TestHandleAgentsDeregister_Errors 测试 handleAgentsDeregister 错误分支
func TestHandleAgentsDeregister_Errors(t *testing.T) {
	reg := collab.NewRegistry()
	dm := collab.NewDelegateManager(reg, nil)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	// 测试 1: 方法不允许 (GET instead of DELETE)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/deregister?id=agent-1", nil)
	w := httptest.NewRecorder()
	s.handleAgentsDeregister(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("method not allowed: got %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	// 测试 2: 缺少 agent id
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/agents/deregister", nil)
	w = httptest.NewRecorder()
	s.handleAgentsDeregister(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing id: got %d, want %d", w.Code, http.StatusBadRequest)
	}

	// 测试 3: 注销不存在的 agent
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/agents/deregister?id=nonexistent", nil)
	w = httptest.NewRecorder()
	s.handleAgentsDeregister(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("not found: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestHandleAgentsDelegate_Errors 测试 handleAgentsDelegate 错误分支
func TestHandleAgentsDelegate_Errors(t *testing.T) {
	reg := collab.NewRegistry()
	_ = reg.Register(&collab.AgentProfile{ID: "agent-1", Name: "Agent 1"})
	dm := collab.NewDelegateManager(reg, nil)

	s := &Server{
		collabRegistry:  reg,
		delegateManager: dm,
	}

	// 测试 1: 方法不允许 (GET instead of POST)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/delegate", nil)
	w := httptest.NewRecorder()
	s.handleAgentsDelegate(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("method not allowed: got %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}

	// 测试 2: 无效 JSON
	body := []byte(`{invalid json}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/delegate", bytes.NewReader(body))
	w = httptest.NewRecorder()
	s.handleAgentsDelegate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid json: got %d, want %d", w.Code, http.StatusBadRequest)
	}

	// 测试 3: 缺少 agent_ids
	body = []byte(`{"mode": "parallel", "description": "test", "input": "hello"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/delegate", bytes.NewReader(body))
	w = httptest.NewRecorder()
	s.handleAgentsDelegate(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing agent_ids: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}