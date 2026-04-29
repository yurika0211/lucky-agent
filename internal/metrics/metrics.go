package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Metrics 全局指标收集器（无第三方依赖，纯 Go 实现）
// 可选导出为 Prometheus 格式或 JSON 格式

// Metrics 指标收集器
type Metrics struct {
	mu sync.RWMutex // 保护 ProviderCalls/ProviderErrors/ProviderLatency map

	// 请求计数
	TotalRequests atomic.Int64
	ChatRequests  atomic.Int64
	ErrorRequests atomic.Int64
	ToolCalls     atomic.Int64
	FunctionCalls atomic.Int64

	// Provider 调用统计
	ProviderCalls   map[string]*atomic.Int64     // provider -> count
	ProviderErrors  map[string]*atomic.Int64     // provider -> error count
	ProviderLatency map[string]*LatencyHistogram // provider -> latency

	// 会话统计
	ActiveSessions atomic.Int64
	TotalSessions  atomic.Int64

	// 记忆统计
	MemoryStores  atomic.Int64
	MemoryRecalls atomic.Int64

	// RAG 统计
	RAGIndexOps  atomic.Int64
	RAGSearchOps atomic.Int64

	// 系统统计
	StartTime time.Time
	Uptime    time.Duration
}

// LatencyHistogram 简易延迟直方图
type LatencyHistogram struct {
	buckets []LatencyBucket
	total   atomic.Int64
	sum     atomic.Int64 // 纳秒总和
}

// LatencyBucket 延迟桶
type LatencyBucket struct {
	UpperBound time.Duration // 上界
	Count      atomic.Int64
}

// NewLatencyHistogram 创建延迟直方图
// buckets 为上界值，如 100ms, 500ms, 1s, 5s, 10s
func NewLatencyHistogram(buckets []time.Duration) *LatencyHistogram {
	h := &LatencyHistogram{}
	for _, b := range buckets {
		h.buckets = append(h.buckets, LatencyBucket{UpperBound: b})
	}
	// +Inf 桶
	h.buckets = append(h.buckets, LatencyBucket{UpperBound: 0}) // 0 表示 +Inf
	return h
}

// Observe 记录一次延迟观测
func (h *LatencyHistogram) Observe(d time.Duration) {
	h.total.Add(1)
	h.sum.Add(int64(d))

	for i := range h.buckets {
		if h.buckets[i].UpperBound == 0 || d <= h.buckets[i].UpperBound {
			h.buckets[i].Count.Add(1)
		}
	}
}

// DefaultBuckets 默认延迟桶
var DefaultBuckets = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// NewMetrics 创建指标收集器
func NewMetrics() *Metrics {
	m := &Metrics{
		ProviderCalls:   make(map[string]*atomic.Int64),
		ProviderErrors:  make(map[string]*atomic.Int64),
		ProviderLatency: make(map[string]*LatencyHistogram),
		StartTime:       time.Now(),
	}
	return m
}

// RegisterProvider 注册一个 Provider 的指标
func (m *Metrics) RegisterProvider(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ProviderCalls[name]; !ok {
		m.ProviderCalls[name] = &atomic.Int64{}
		m.ProviderErrors[name] = &atomic.Int64{}
		m.ProviderLatency[name] = NewLatencyHistogram(DefaultBuckets)
	}
}

// RecordProviderCall 记录一次 Provider 调用
func (m *Metrics) RecordProviderCall(provider string, latency time.Duration, err bool) {
	m.RegisterProvider(provider)
	m.mu.RLock()
	calls := m.ProviderCalls[provider]
	lat := m.ProviderLatency[provider]
	m.mu.RUnlock()
	calls.Add(1)
	lat.Observe(latency)
	if err {
		m.mu.RLock()
		errors := m.ProviderErrors[provider]
		m.mu.RUnlock()
		errors.Add(1)
		m.ErrorRequests.Add(1)
	}
}

// RecordChatRequest 记录一次聊天请求
func (m *Metrics) RecordChatRequest() {
	m.TotalRequests.Add(1)
	m.ChatRequests.Add(1)
}

// RecordToolCall 记录一次工具调用
func (m *Metrics) RecordToolCall() {
	m.TotalRequests.Add(1)
	m.ToolCalls.Add(1)
}

// RecordFunctionCall 记录一次 Function Call
func (m *Metrics) RecordFunctionCall() {
	m.TotalRequests.Add(1)
	m.FunctionCalls.Add(1)
}

// RecordSessionOpen 记录会话开启
func (m *Metrics) RecordSessionOpen() {
	m.ActiveSessions.Add(1)
	m.TotalSessions.Add(1)
}

// RecordSessionClose 记录会话关闭
func (m *Metrics) RecordSessionClose() {
	m.ActiveSessions.Add(-1)
}

// RecordMemoryStore 记录记忆存储
func (m *Metrics) RecordMemoryStore() {
	m.MemoryStores.Add(1)
}

// RecordMemoryRecall 记录记忆召回
func (m *Metrics) RecordMemoryRecall() {
	m.MemoryRecalls.Add(1)
}

// RecordRAGIndex 记录 RAG 索引操作
func (m *Metrics) RecordRAGIndex() {
	m.RAGIndexOps.Add(1)
}

// RecordRAGSearch 记录 RAG 搜索操作
func (m *Metrics) RecordRAGSearch() {
	m.RAGSearchOps.Add(1)
}

// Snapshot 获取当前指标快照
func (m *Metrics) Snapshot() *MetricsSnapshot {
	m.Uptime = time.Since(m.StartTime)

	snap := &MetricsSnapshot{
		TotalRequests:  m.TotalRequests.Load(),
		ChatRequests:   m.ChatRequests.Load(),
		ErrorRequests:  m.ErrorRequests.Load(),
		ToolCalls:      m.ToolCalls.Load(),
		FunctionCalls:  m.FunctionCalls.Load(),
		ActiveSessions: m.ActiveSessions.Load(),
		TotalSessions:  m.TotalSessions.Load(),
		MemoryStores:   m.MemoryStores.Load(),
		MemoryRecalls:  m.MemoryRecalls.Load(),
		RAGIndexOps:    m.RAGIndexOps.Load(),
		RAGSearchOps:   m.RAGSearchOps.Load(),
		StartTime:      m.StartTime,
		Uptime:         m.Uptime.String(),
		Providers:      make(map[string]ProviderStats),
	}

	m.mu.RLock()
	for name, calls := range m.ProviderCalls {
		errors := int64(0)
		if e, ok := m.ProviderErrors[name]; ok {
			errors = e.Load()
		}
		latencyAvg := float64(0)
		if h, ok := m.ProviderLatency[name]; ok {
			total := h.total.Load()
			if total > 0 {
				latencyAvg = float64(h.sum.Load()) / float64(total) / 1e6 // ns -> ms
			}
		}
		snap.Providers[name] = ProviderStats{
			Calls:     calls.Load(),
			Errors:    errors,
			LatencyMs: latencyAvg,
		}
	}
	m.mu.RUnlock()

	return snap
}

// MetricsSnapshot 指标快照（可序列化）
type MetricsSnapshot struct {
	TotalRequests  int64                    `json:"total_requests"`
	ChatRequests   int64                    `json:"chat_requests"`
	ErrorRequests  int64                    `json:"error_requests"`
	ToolCalls      int64                    `json:"tool_calls"`
	FunctionCalls  int64                    `json:"function_calls"`
	ActiveSessions int64                    `json:"active_sessions"`
	TotalSessions  int64                    `json:"total_sessions"`
	MemoryStores   int64                    `json:"memory_stores"`
	MemoryRecalls  int64                    `json:"memory_recalls"`
	RAGIndexOps    int64                    `json:"rag_index_ops"`
	RAGSearchOps   int64                    `json:"rag_search_ops"`
	StartTime      time.Time                `json:"start_time"`
	Uptime         string                   `json:"uptime"`
	Providers      map[string]ProviderStats `json:"providers"`
}

// ProviderStats Provider 统计
type ProviderStats struct {
	Calls     int64   `json:"calls"`
	Errors    int64   `json:"errors"`
	LatencyMs float64 `json:"latency_ms"`
}

// ExportPrometheus 导出 Prometheus 格式文本
func (m *Metrics) ExportPrometheus() string {
	snap := m.Snapshot()
	var out string

	out += promLine("lh_requests_total", snap.TotalRequests)
	out += promLine("lh_chat_requests_total", snap.ChatRequests)
	out += promLine("lh_error_requests_total", snap.ErrorRequests)
	out += promLine("lh_tool_calls_total", snap.ToolCalls)
	out += promLine("lh_function_calls_total", snap.FunctionCalls)
	out += promLine("lh_active_sessions", snap.ActiveSessions)
	out += promLine("lh_sessions_total", snap.TotalSessions)
	out += promLine("lh_memory_stores_total", snap.MemoryStores)
	out += promLine("lh_memory_recalls_total", snap.MemoryRecalls)
	out += promLine("lh_rag_index_ops_total", snap.RAGIndexOps)
	out += promLine("lh_rag_search_ops_total", snap.RAGSearchOps)

	for name, stats := range snap.Providers {
		out += promLabelLine("lh_provider_calls_total", name, stats.Calls)
		out += promLabelLine("lh_provider_errors_total", name, stats.Errors)
		out += promLabelLine("lh_provider_latency_ms", name, int64(stats.LatencyMs))
	}

	return out
}

func promLine(name string, value int64) string {
	return name + " " + formatInt(value) + "\n"
}

func promLabelLine(name, label string, value int64) string {
	return name + `{provider="` + label + `"} ` + formatInt(value) + "\n"
}

func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	return string(rune('0'+v%10)) + formatInt(v/10)
}
