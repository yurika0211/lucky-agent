package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yurika0211/luckyagent/internal/workflow"
)

// ===== Health Handlers =====

func TestHandleHealthLiveness(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/liveness", nil)
	w := httptest.NewRecorder()
	s.handleHealthLiveness(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleHealthLivenessMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/health/liveness", nil)
	w := httptest.NewRecorder()
	s.handleHealthLiveness(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleHealthReadiness(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/readiness", nil)
	w := httptest.NewRecorder()
	s.handleHealthReadiness(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleHealthReadinessMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/health/readiness", nil)
	w := httptest.NewRecorder()
	s.handleHealthReadiness(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleHealthDetail(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health/detail", nil)
	w := httptest.NewRecorder()
	s.handleHealthDetail(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleHealthDetailMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/health/detail", nil)
	w := httptest.NewRecorder()
	s.handleHealthDetail(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ===== Metrics Handler =====

func TestHandleMetrics(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/metrics", nil)
	w := httptest.NewRecorder()
	s.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4" {
		t.Errorf("expected prometheus content type, got %s", ct)
	}
}

func TestHandleMetricsMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/metrics", nil)
	w := httptest.NewRecorder()
	s.handleMetrics(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ===== Context Handlers =====

func TestHandleContext(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/context", nil)
	w := httptest.NewRecorder()
	s.handleContext(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["max_tokens"] == nil {
		t.Error("expected max_tokens to be set")
	}
	if resp["strategy"] == nil {
		t.Error("expected strategy to be set")
	}
}

func TestHandleContextMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/context", nil)
	w := httptest.NewRecorder()
	s.handleContext(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleContextFit(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "system", "content": "You are helpful", "priority": 3},
			{"role": "user", "content": "Hello"},
			{"role": "assistant", "content": "Hi there"},
		},
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/context/fit", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleContextFit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["trimmed"] == nil {
		t.Error("expected trimmed field")
	}
	if resp["strategy"] == nil {
		t.Error("expected strategy field")
	}
}

func TestHandleContextFitWithStrategy(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "user", "content": "test"},
		},
		"strategy": "oldest_first",
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/context/fit", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleContextFit(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleContextFitInvalidBody(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/context/fit", bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleContextFit(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleContextFitMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/context/fit", nil)
	w := httptest.NewRecorder()
	s.handleContextFit(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ===== Function Calling Handlers =====

func TestHandleFunctionCallingGet(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fc", nil)
	w := httptest.NewRecorder()
	s.handleFunctionCalling(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["version"] == nil {
		t.Error("expected version field")
	}
}

func TestHandleFunctionCallingMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/fc", nil)
	w := httptest.NewRecorder()
	s.handleFunctionCalling(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleFCTools(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fc/tools", nil)
	w := httptest.NewRecorder()
	s.handleFCTools(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"] == nil {
		t.Error("expected count field")
	}
}

func TestHandleFCToolsMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/fc/tools", nil)
	w := httptest.NewRecorder()
	s.handleFCTools(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleFCHistory(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/fc/history", nil)
	w := httptest.NewRecorder()
	s.handleFCHistory(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleFCHistoryMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/fc/history", nil)
	w := httptest.NewRecorder()
	s.handleFCHistory(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ===== RAG Store Handler =====

func TestHandleRAGStoreGet(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag/store", nil)
	w := httptest.NewRecorder()
	s.handleRAGStore(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["backend"] == nil {
		t.Error("expected backend field")
	}
}

func TestHandleRAGStorePost(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]string{"db_path": "/tmp/test.db"}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/store", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRAGStore(w, req)

	// If already using SQLite, returns 409; otherwise returns migration hint
	if w.Code != http.StatusOK && w.Code != http.StatusConflict {
		t.Errorf("expected 200 or 409, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleRAGStoreMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/rag/store", nil)
	w := httptest.NewRecorder()
	s.handleRAGStore(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ===== Workflow Handlers =====

func TestHandleWorkflowsList(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	w := httptest.NewRecorder()
	s.handleWorkflows(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleWorkflowsCreate(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	tasks := []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "shell", Params: map[string]interface{}{"command": "echo hello"}},
	}
	body := map[string]interface{}{
		"name":        "test-workflow",
		"description": "A test workflow",
		"tasks":       tasks,
	}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleWorkflows(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleWorkflowsInvalidBody(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleWorkflows(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleWorkflowsMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workflows", nil)
	w := httptest.NewRecorder()
	s.handleWorkflows(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleWorkflowByIDGet(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	// Create a workflow first
	tasks := []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "shell", Params: map[string]interface{}{"command": "echo hi"}},
	}
	wf := workflow.NewWorkflow("get-test", tasks)
	s.workflowEngine.RegisterWorkflow(wf)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/"+wf.ID, nil)
	w := httptest.NewRecorder()
	s.handleWorkflowByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleWorkflowByIDNotFound(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflows/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleWorkflowByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleWorkflowByIDDelete(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	tasks := []*workflow.Task{
		{ID: "t1", Name: "Task 1", Action: "shell", Params: map[string]interface{}{"command": "echo bye"}},
	}
	wf := workflow.NewWorkflow("delete-test", tasks)
	s.workflowEngine.RegisterWorkflow(wf)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workflows/"+wf.ID, nil)
	w := httptest.NewRecorder()
	s.handleWorkflowByID(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleWorkflowByIDMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPut, "/api/v1/workflows/test", nil)
	w := httptest.NewRecorder()
	s.handleWorkflowByID(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleWorkflowInstancesList(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-instances", nil)
	w := httptest.NewRecorder()
	s.handleWorkflowInstances(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleWorkflowInstancesInvalidBody(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/workflow-instances", bytes.NewReader([]byte("bad")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleWorkflowInstances(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleWorkflowInstancesMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workflow-instances", nil)
	w := httptest.NewRecorder()
	s.handleWorkflowInstances(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleWorkflowInstanceByIDNotFound(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workflow-instances/nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleWorkflowInstanceByID(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleWorkflowInstanceByIDMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workflow-instances/test", nil)
	w := httptest.NewRecorder()
	s.handleWorkflowInstanceByID(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// ===== Gateway Handlers =====

func TestHandleGatewaysList(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways", nil)
	w := httptest.NewRecorder()
	s.handleGatewaysList(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandleGatewaysListMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateways", nil)
	w := httptest.NewRecorder()
	s.handleGatewaysList(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleGatewayTelegramStartNoToken(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	body := map[string]string{"token": ""}
	data, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/telegram/start", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleGatewayTelegramStart(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGatewayTelegramStartInvalidBody(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gateways/telegram/start", bytes.NewReader([]byte("bad")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleGatewayTelegramStart(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGatewayTelegramStartMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/telegram/start", nil)
	w := httptest.NewRecorder()
	s.handleGatewayTelegramStart(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleGatewayByNameNotFound(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/nonexistent/status", nil)
	w := httptest.NewRecorder()
	s.handleGatewayByName(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestHandleGatewayByNameInvalidAction(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gateways/test/invalid", nil)
	w := httptest.NewRecorder()
	s.handleGatewayByName(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ===== WebSocket Stats =====

func TestHandleWSStats(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ws/stats", nil)
	w := httptest.NewRecorder()
	s.handleWSStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// ===== Server Metrics/HealthCheck accessors =====

func TestServerMetrics(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	m := s.Metrics()
	if m == nil {
		t.Error("Metrics() should not return nil")
	}
}

func TestServerHealthCheck(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	hc := s.HealthCheck()
	if hc == nil {
		t.Error("HealthCheck() should not return nil")
	}
}

// ===== Collab Handlers =====

func TestHandleAgentsGetMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents", nil)
	w := httptest.NewRecorder()
	s.handleAgentsGet(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleAgentsGet(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	// Without id parameter → 400
	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents", nil)
	w := httptest.NewRecorder()
	s.handleAgentsGet(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing id, got %d", w.Code)
	}

	// With non-existent id → 404
	req = httptest.NewRequest(http.MethodGet, "/api/v1/agents?id=nonexistent", nil)
	w = httptest.NewRecorder()
	s.handleAgentsGet(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent agent, got %d", w.Code)
	}
}

func TestHandleAgentsTaskMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/task", nil)
	w := httptest.NewRecorder()
	s.handleAgentsTask(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleAgentsTaskNoID(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/task", nil)
	w := httptest.NewRecorder()
	s.handleAgentsTask(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing task id, got %d", w.Code)
	}
}

func TestHandleAgentsTaskNotFound(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/task?id=nonexistent", nil)
	w := httptest.NewRecorder()
	s.handleAgentsTask(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent task, got %d", w.Code)
	}
}

func TestHandleAgentsCancelMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/cancel", nil)
	w := httptest.NewRecorder()
	s.handleAgentsCancel(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleAgentsCancelInvalidBody(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	// Missing task id → 400
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/cancel", nil)
	w := httptest.NewRecorder()
	s.handleAgentsCancel(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	// Non-existent task id → 404
	req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/cancel?id=nonexistent", nil)
	w = httptest.NewRecorder()
	s.handleAgentsCancel(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// ===== Embedder Handlers =====

func TestHandleEmbedderRoutesMethodNotAllowed(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/embedders", nil)
	w := httptest.NewRecorder()
	s.handleEmbedderRoutes(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}
