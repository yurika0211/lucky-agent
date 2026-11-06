package agent

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// defaultLatencyBuckets matches runtime provider latency: 50ms through 10s.
var defaultLatencyBuckets = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// latencyBucket is one cumulative histogram bucket. UpperBound 0 is +Inf.
type latencyBucket struct {
	upperBound time.Duration
	count      atomic.Int64
}

// latencyHistogram counts observations into cumulative buckets.
type latencyHistogram struct {
	buckets []latencyBucket
	total   atomic.Int64
	sum     atomic.Int64 // nanoseconds
}

func newLatencyHistogram(bounds []time.Duration) *latencyHistogram {
	h := &latencyHistogram{}
	for _, bound := range bounds {
		h.buckets = append(h.buckets, latencyBucket{upperBound: bound})
	}
	h.buckets = append(h.buckets, latencyBucket{upperBound: 0})
	return h
}

func (h *latencyHistogram) observe(d time.Duration) {
	if h == nil {
		return
	}
	h.total.Add(1)
	h.sum.Add(int64(d))
	for i := range h.buckets {
		if h.buckets[i].upperBound == 0 || d <= h.buckets[i].upperBound {
			h.buckets[i].count.Add(1)
		}
	}
}

type toolDurationStat struct {
	success  atomic.Int64
	failures atomic.Int64
	latency  *latencyHistogram
}

// toolDurationMetrics is in-process only. Restart clears it.
// It stores tool name and wall clock, never arguments or results.
// lh_tool_calls_total on runtime Metrics counts every tool call via
// RecordToolCall(). This map splits success and failure and keeps latency.
// lh_function_calls_total is RecordFunctionCall, not tool wall clock, so
// this map does not touch it.
type toolDurationMetrics struct {
	mu    sync.Mutex
	tools map[string]*toolDurationStat
}

func newToolDurationMetrics() *toolDurationMetrics {
	return &toolDurationMetrics{tools: map[string]*toolDurationStat{}}
}

func (m *toolDurationMetrics) record(name string, d time.Duration, failed bool) {
	if m == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "unknown"
	}
	if d < 0 {
		d = 0
	}
	m.mu.Lock()
	stats := m.tools[name]
	if stats == nil {
		stats = &toolDurationStat{latency: newLatencyHistogram(defaultLatencyBuckets)}
		m.tools[name] = stats
	}
	m.mu.Unlock()
	stats.latency.observe(d)
	if failed {
		stats.failures.Add(1)
		return
	}
	stats.success.Add(1)
}

// ToolDurationSnapshot is one tool's in-process latency.
// Calls counts successes. Failures are separate. Buckets are cumulative.
type ToolDurationSnapshot struct {
	Calls         int64
	Failures      int64
	TotalDuration time.Duration
	LatencyMs     float64
	Buckets       []ToolLatencyBucket
}

// ToolLatencyBucket is one cumulative bucket. UpperBound "+Inf" has no bound.
type ToolLatencyBucket struct {
	UpperBound string
	Count      int64
}

func (m *toolDurationMetrics) snapshot() map[string]ToolDurationSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	names := make([]string, 0, len(m.tools))
	copied := make(map[string]*toolDurationStat, len(m.tools))
	for name, stats := range m.tools {
		names = append(names, name)
		copied[name] = stats
	}
	m.mu.Unlock()
	sort.Strings(names)
	out := make(map[string]ToolDurationSnapshot, len(names))
	for _, name := range names {
		stats := copied[name]
		total := stats.latency.total.Load()
		sum := time.Duration(stats.latency.sum.Load())
		avg := float64(0)
		if total > 0 {
			avg = float64(sum) / float64(total) / 1e6
		}
		buckets := make([]ToolLatencyBucket, 0, len(stats.latency.buckets))
		for i := range stats.latency.buckets {
			le := stats.latency.buckets[i].upperBound.String()
			if stats.latency.buckets[i].upperBound == 0 {
				le = "+Inf"
			}
			buckets = append(buckets, ToolLatencyBucket{
				UpperBound: le,
				Count:      stats.latency.buckets[i].count.Load(),
			})
		}
		out[name] = ToolDurationSnapshot{
			Calls:         stats.success.Load(),
			Failures:      stats.failures.Load(),
			TotalDuration: sum,
			LatencyMs:     avg,
			Buckets:       buckets,
		}
	}
	return out
}

// ExportPrometheus writes per-tool counts and latency.
// lh_tool_calls_total stays on the runtime counter and is not repeated here.
func (m *toolDurationMetrics) ExportPrometheus() string {
	tools := m.snapshot()
	if len(tools) == 0 {
		return ""
	}
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# HELP lh_tool_calls_by_name_total Successful tool executions by name.\n")
	b.WriteString("# TYPE lh_tool_calls_by_name_total counter\n")
	b.WriteString("# HELP lh_tool_calls_failed_total Failed tool executions by name. Not included in the success counter.\n")
	b.WriteString("# TYPE lh_tool_calls_failed_total counter\n")
	b.WriteString("# HELP lh_tool_call_duration_seconds Wall clock of one tool execution, success and failure.\n")
	b.WriteString("# TYPE lh_tool_call_duration_seconds histogram\n")
	for _, name := range names {
		stats := tools[name]
		b.WriteString(`lh_tool_calls_by_name_total{tool="` + promLabel(name) + `",result="success"} ` + formatMetricInt(stats.Calls) + "\n")
		b.WriteString(`lh_tool_calls_failed_total{tool="` + promLabel(name) + `",result="failed"} ` + formatMetricInt(stats.Failures) + "\n")
		count := int64(0)
		for _, bucket := range stats.Buckets {
			b.WriteString(`lh_tool_call_duration_seconds_bucket{tool="` + promLabel(name) + `",le="` + bucket.UpperBound + `"} ` + formatMetricInt(bucket.Count) + "\n")
			if bucket.UpperBound == "+Inf" {
				count = bucket.Count
			}
		}
		b.WriteString(`lh_tool_call_duration_seconds_sum{tool="` + promLabel(name) + `"} ` + formatMetricInt(int64(stats.TotalDuration.Seconds()+0.5)) + "\n")
		b.WriteString(`lh_tool_call_duration_seconds_count{tool="` + promLabel(name) + `"} ` + formatMetricInt(count) + "\n")
	}
	return b.String()
}

func promLabel(value string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", "").Replace(value)
}

func formatMetricInt(v int64) string {
	if v == 0 {
		return "0"
	}
	negative := v < 0
	if negative {
		v = -v
	}
	var digits [20]byte
	i := len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}
