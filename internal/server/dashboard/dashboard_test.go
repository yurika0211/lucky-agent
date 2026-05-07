package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// mockDataProvider 用于测试
type mockDataProvider struct {
	data map[string]interface{}
}

func (m *mockDataProvider) DashboardData() map[string]interface{} {
	return m.data
}

func TestNew(t *testing.T) {
	d := New(DefaultConfig())
	if d == nil {
		t.Fatal("New returned nil")
	}
	if d.addr != ":8765" {
		t.Errorf("expected addr :8765, got %s", d.addr)
	}
}

func TestNewCustomAddr(t *testing.T) {
	cfg := Config{Addr: ":9999"}
	d := New(cfg)
	if d.addr != ":9999" {
		t.Errorf("expected addr :9999, got %s", d.addr)
	}
}

func TestAddProvider(t *testing.T) {
	d := New(DefaultConfig())
	provider := &mockDataProvider{data: map[string]interface{}{"test": "value"}}
	d.AddProvider(provider)

	if len(d.providers) != 1 {
		t.Errorf("expected 1 provider, got %d", len(d.providers))
	}
}

func TestHandleHealth(t *testing.T) {
	d := New(DefaultConfig())
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	d.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]string
	json.NewDecoder(w.Body).Decode(&result)
	if result["status"] != "ok" {
		t.Errorf("expected status ok, got %s", result["status"])
	}
}

func TestHandleStatus(t *testing.T) {
	d := New(DefaultConfig())
	d.AddProvider(&mockDataProvider{data: map[string]interface{}{
		"provider": "openai",
		"model":    "gpt-4o",
	}})

	req := httptest.NewRequest("GET", "/api/status", nil)
	w := httptest.NewRecorder()

	d.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["provider"] != "openai" {
		t.Errorf("expected provider openai, got %v", result["provider"])
	}
	if result["model"] != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %v", result["model"])
	}
}

func TestHandleData(t *testing.T) {
	d := New(DefaultConfig())
	d.AddProvider(&mockDataProvider{data: map[string]interface{}{
		"memory_short": 5,
		"memory_long":  2,
	}})

	req := httptest.NewRequest("GET", "/api/data", nil)
	w := httptest.NewRecorder()

	d.handleData(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["memory_short"] != float64(5) {
		t.Errorf("expected memory_short 5, got %v", result["memory_short"])
	}
}

func TestHandleSPA(t *testing.T) {
	d := New(DefaultConfig())

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	d.handleSPA(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if len(body) < 100 {
		t.Error("SPA response too short")
	}
	// 应该包含标题
	if !contains(body, "LuckyHarness Dashboard") {
		t.Error("SPA missing title")
	}
}

func TestStartAndStop(t *testing.T) {
	cfg := Config{Addr: ":0"} // 随机端口
	d := New(cfg)

	if err := d.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !d.IsRunning() {
		t.Error("Dashboard should be running")
	}

	// 等待服务启动
	time.Sleep(100 * time.Millisecond)

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if d.IsRunning() {
		t.Error("Dashboard should not be running after stop")
	}
}

func TestDoubleStart(t *testing.T) {
	d := New(Config{Addr: ":0"})

	if err := d.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	time.Sleep(100 * time.Millisecond)

	if err := d.Start(); err == nil {
		t.Error("expected error for double start")
	}
}

func TestMultipleProviders(t *testing.T) {
	d := New(DefaultConfig())
	d.AddProvider(&mockDataProvider{data: map[string]interface{}{"a": 1}})
	d.AddProvider(&mockDataProvider{data: map[string]interface{}{"b": 2}})

	req := httptest.NewRequest("GET", "/api/data", nil)
	w := httptest.NewRecorder()
	d.handleData(w, req)

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	if result["a"] != float64(1) {
		t.Errorf("expected a=1, got %v", result["a"])
	}
	if result["b"] != float64(2) {
		t.Errorf("expected b=2, got %v", result["b"])
	}
}

func TestEnsureDir(t *testing.T) {
	tmpDir := os.TempDir() + "/lh_test_dir_" + time.Now().Format("150405")
	defer os.RemoveAll(tmpDir)

	if err := EnsureDir(tmpDir); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		t.Error("directory not created")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
