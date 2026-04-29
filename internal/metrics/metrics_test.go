package metrics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()
	if m == nil {
		t.Fatal("expected non-nil metrics")
	}
	if m.StartTime.IsZero() {
		t.Fatal("expected non-zero start time")
	}
}

func TestRecordChatRequest(t *testing.T) {
	m := NewMetrics()
	m.RecordChatRequest()
	m.RecordChatRequest()
	m.RecordChatRequest()

	if m.TotalRequests.Load() != 3 {
		t.Errorf("expected 3 total requests, got %d", m.TotalRequests.Load())
	}
	if m.ChatRequests.Load() != 3 {
		t.Errorf("expected 3 chat requests, got %d", m.ChatRequests.Load())
	}
}

func TestRecordProviderCall(t *testing.T) {
	m := NewMetrics()
	m.RecordProviderCall("openai", 100*time.Millisecond, false)
	m.RecordProviderCall("openai", 200*time.Millisecond, false)
	m.RecordProviderCall("anthropic", 150*time.Millisecond, true)

	if m.ProviderCalls["openai"].Load() != 2 {
		t.Errorf("expected 2 openai calls, got %d", m.ProviderCalls["openai"].Load())
	}
	if m.ProviderCalls["anthropic"].Load() != 1 {
		t.Errorf("expected 1 anthropic call, got %d", m.ProviderCalls["anthropic"].Load())
	}
	if m.ProviderErrors["anthropic"].Load() != 1 {
		t.Errorf("expected 1 anthropic error, got %d", m.ProviderErrors["anthropic"].Load())
	}
	if m.ErrorRequests.Load() != 1 {
		t.Errorf("expected 1 error request, got %d", m.ErrorRequests.Load())
	}
}

func TestRecordSession(t *testing.T) {
	m := NewMetrics()
	m.RecordSessionOpen()
	m.RecordSessionOpen()
	m.RecordSessionOpen()

	if m.ActiveSessions.Load() != 3 {
		t.Errorf("expected 3 active sessions, got %d", m.ActiveSessions.Load())
	}
	if m.TotalSessions.Load() != 3 {
		t.Errorf("expected 3 total sessions, got %d", m.TotalSessions.Load())
	}

	m.RecordSessionClose()
	if m.ActiveSessions.Load() != 2 {
		t.Errorf("expected 2 active sessions, got %d", m.ActiveSessions.Load())
	}
}

func TestRecordToolCall(t *testing.T) {
	m := NewMetrics()
	m.RecordToolCall()
	m.RecordToolCall()

	if m.ToolCalls.Load() != 2 {
		t.Errorf("expected 2 tool calls, got %d", m.ToolCalls.Load())
	}
}

func TestRecordFunctionCall(t *testing.T) {
	m := NewMetrics()
	m.RecordFunctionCall()

	if m.FunctionCalls.Load() != 1 {
		t.Errorf("expected 1 function call, got %d", m.FunctionCalls.Load())
	}
}

func TestRecordMemoryOps(t *testing.T) {
	m := NewMetrics()
	m.RecordMemoryStore()
	m.RecordMemoryStore()
	m.RecordMemoryRecall()

	if m.MemoryStores.Load() != 2 {
		t.Errorf("expected 2 memory stores, got %d", m.MemoryStores.Load())
	}
	if m.MemoryRecalls.Load() != 1 {
		t.Errorf("expected 1 memory recall, got %d", m.MemoryRecalls.Load())
	}
}

func TestRecordRAGOps(t *testing.T) {
	m := NewMetrics()
	m.RecordRAGIndex()
	m.RecordRAGSearch()

	if m.RAGIndexOps.Load() != 1 {
		t.Errorf("expected 1 rag index op, got %d", m.RAGIndexOps.Load())
	}
	if m.RAGSearchOps.Load() != 1 {
		t.Errorf("expected 1 rag search op, got %d", m.RAGSearchOps.Load())
	}
}

func TestSnapshot(t *testing.T) {
	m := NewMetrics()
	m.RecordChatRequest()
	m.RecordProviderCall("openai", 100*time.Millisecond, false)
	m.RecordSessionOpen()
	m.RecordMemoryStore()
	m.RecordRAGIndex()

	snap := m.Snapshot()
	if snap.TotalRequests != 1 {
		t.Errorf("expected 1 total request, got %d", snap.TotalRequests)
	}
	if snap.ActiveSessions != 1 {
		t.Errorf("expected 1 active session, got %d", snap.ActiveSessions)
	}
	if _, ok := snap.Providers["openai"]; !ok {
		t.Error("expected openai provider stats")
	}
	if snap.Uptime == "" {
		t.Error("expected non-empty uptime")
	}
}

func TestSnapshotJSON(t *testing.T) {
	m := NewMetrics()
	m.RecordChatRequest()
	m.RecordProviderCall("openai", 50*time.Millisecond, false)

	snap := m.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "total_requests") {
		t.Error("expected total_requests in JSON")
	}
	if !strings.Contains(string(data), "openai") {
		t.Error("expected openai in JSON")
	}
}

func TestExportPrometheus(t *testing.T) {
	m := NewMetrics()
	m.RecordChatRequest()
	m.RecordChatRequest()
	m.RecordProviderCall("openai", 100*time.Millisecond, false)
	m.RecordProviderCall("anthropic", 200*time.Millisecond, true)

	prom := m.ExportPrometheus()
	if !strings.Contains(prom, "lh_requests_total 2") {
		t.Errorf("expected requests_total 2 in prometheus output, got:\n%s", prom)
	}
	if !strings.Contains(prom, `lh_provider_calls_total{provider="openai"}`) {
		t.Errorf("expected openai provider calls in prometheus output")
	}
	if !strings.Contains(prom, `lh_provider_errors_total{provider="anthropic"}`) {
		t.Errorf("expected anthropic provider errors in prometheus output")
	}
}

func TestLatencyHistogram(t *testing.T) {
	h := NewLatencyHistogram(DefaultBuckets)
	h.Observe(50 * time.Millisecond)
	h.Observe(200 * time.Millisecond)
	h.Observe(3 * time.Second)

	if h.total.Load() != 3 {
		t.Errorf("expected 3 observations, got %d", h.total.Load())
	}
}

func TestLatencyHistogramBuckets(t *testing.T) {
	h := NewLatencyHistogram([]time.Duration{100 * time.Millisecond, 500 * time.Millisecond, time.Second})
	h.Observe(50 * time.Millisecond)  // <= 100ms
	h.Observe(200 * time.Millisecond) // <= 500ms
	h.Observe(2 * time.Second)        // +Inf

	// Prometheus 累积桶: <=100ms 包含 50ms, <=500ms 包含 50ms+200ms, +Inf 包含全部
	if h.buckets[0].Count.Load() != 1 {
		t.Errorf("expected 1 in <=100ms bucket, got %d", h.buckets[0].Count.Load())
	}
	// 200ms <= 500ms, 所以 <=500ms 桶包含 50ms 和 200ms
	if h.buckets[1].Count.Load() != 2 {
		t.Errorf("expected 2 in <=500ms bucket (cumulative), got %d", h.buckets[1].Count.Load())
	}
	// +Inf 桶包含全部 3 个观测
	if h.buckets[3].Count.Load() != 3 {
		t.Errorf("expected 3 in +Inf bucket, got %d", h.buckets[3].Count.Load())
	}
}

func TestRegisterProviderIdempotent(t *testing.T) {
	m := NewMetrics()
	m.RegisterProvider("openai")
	m.RegisterProvider("openai") // 不应 panic

	if m.ProviderCalls["openai"].Load() != 0 {
		t.Error("expected 0 calls after registration")
	}
}

func TestConcurrentMetrics(t *testing.T) {
	m := NewMetrics()
	done := make(chan bool, 100)

	for i := 0; i < 50; i++ {
		go func() {
			m.RecordChatRequest()
			m.RecordProviderCall("openai", 10*time.Millisecond, false)
			m.RecordSessionOpen()
			done <- true
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}

	if m.TotalRequests.Load() != 50 {
		t.Errorf("expected 50 total requests, got %d", m.TotalRequests.Load())
	}
	if m.ChatRequests.Load() != 50 {
		t.Errorf("expected 50 chat requests, got %d", m.ChatRequests.Load())
	}
}
