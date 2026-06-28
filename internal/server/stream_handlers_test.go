package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
)

func TestHandleRAGStreamStatus(t *testing.T) {
	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	mgr.Load()

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag/stream/status", nil)
	w := httptest.NewRecorder()

	s.handleRAGStreamStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if initialized, ok := resp["initialized"].(bool); !ok || !initialized {
		t.Error("expected initialized=true")
	}
}

func TestHandleRAGStreamWatch(t *testing.T) {
	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	mgr.Load()

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	s := New(a, DefaultServerConfig())

	// Test POST - add watch
	body := bytes.NewBufferString(`{"dir":"/tmp"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/stream/watch", body)
	w := httptest.NewRecorder()

	s.handleRAGStreamWatch(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp["action"] != "add_watch" {
		t.Errorf("expected action=add_watch, got %v", resp["action"])
	}

	// Test DELETE - remove watch
	body = bytes.NewBufferString(`{"dir":"/tmp"}`)
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/rag/stream/watch", body)
	w = httptest.NewRecorder()

	s.handleRAGStreamWatch(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Test invalid method
	req = httptest.NewRequest(http.MethodGet, "/api/v1/rag/stream/watch", nil)
	w = httptest.NewRecorder()

	s.handleRAGStreamWatch(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleRAGStreamScan(t *testing.T) {
	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	mgr.Load()

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	s := New(a, DefaultServerConfig())

	// Test POST
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/stream/scan", nil)
	w := httptest.NewRecorder()

	s.handleRAGStreamScan(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if _, ok := resp["changes"]; !ok {
		t.Error("expected changes field in response")
	}

	// Test invalid method
	req = httptest.NewRequest(http.MethodGet, "/api/v1/rag/stream/scan", nil)
	w = httptest.NewRecorder()

	s.handleRAGStreamScan(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleRAGStreamStartStop(t *testing.T) {
	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	mgr.Load()

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	s := New(a, DefaultServerConfig())

	// Test start
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/stream/start", nil)
	w := httptest.NewRecorder()

	s.handleRAGStreamStart(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp["status"] != "started" && resp["status"] != "already_running" {
		t.Errorf("expected status=started or already_running, got %v", resp["status"])
	}

	// Test stop
	req = httptest.NewRequest(http.MethodPost, "/api/v1/rag/stream/stop", nil)
	w = httptest.NewRecorder()

	s.handleRAGStreamStop(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Test invalid method
	req = httptest.NewRequest(http.MethodGet, "/api/v1/rag/stream/start", nil)
	w = httptest.NewRecorder()

	s.handleRAGStreamStart(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleRAGStreamQueue(t *testing.T) {
	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	mgr.Load()

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	s := New(a, DefaultServerConfig())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/rag/stream/queue", nil)
	w := httptest.NewRecorder()

	s.handleRAGStreamQueue(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if _, ok := resp["total"]; !ok {
		t.Error("expected total field in response")
	}
	if _, ok := resp["jobs"]; !ok {
		t.Error("expected jobs field in response")
	}
}

func TestHandleRAGStreamProcess(t *testing.T) {
	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("create config manager: %v", err)
	}
	mgr.Load()

	a, err := agent.New(mgr)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}

	s := New(a, DefaultServerConfig())

	// Test POST with empty queue
	req := httptest.NewRequest(http.MethodPost, "/api/v1/rag/stream/process", nil)
	w := httptest.NewRecorder()

	s.handleRAGStreamProcess(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if processed, ok := resp["processed"].(float64); !ok || int(processed) != 0 {
		t.Errorf("expected processed=0, got %v", resp["processed"])
	}

	// Test invalid method
	req = httptest.NewRequest(http.MethodGet, "/api/v1/rag/stream/process", nil)
	w = httptest.NewRecorder()

	s.handleRAGStreamProcess(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}