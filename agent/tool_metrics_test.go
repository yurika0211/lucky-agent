package agent

import (
	"strings"
	"testing"
	"time"
)

func TestToolDurationExportKeepsNamedLatency(t *testing.T) {
	m := newToolDurationMetrics()
	m.record("terminal", 40*time.Millisecond, false)
	m.record("terminal", 80*time.Millisecond, false)
	m.record("web_fetch", 2*time.Second, true)

	snap := m.snapshot()
	terminal := snap["terminal"]
	if terminal.Calls != 2 || terminal.Failures != 0 {
		t.Fatalf("terminal calls=%d failures=%d", terminal.Calls, terminal.Failures)
	}
	if terminal.TotalDuration != 120*time.Millisecond {
		t.Fatalf("terminal total = %s", terminal.TotalDuration)
	}
	if terminal.LatencyMs < 59 || terminal.LatencyMs > 61 {
		t.Fatalf("terminal avg latency = %v", terminal.LatencyMs)
	}
	web := snap["web_fetch"]
	if web.Calls != 0 || web.Failures != 1 {
		t.Fatalf("web_fetch calls=%d failures=%d", web.Calls, web.Failures)
	}
	var le50, le100 int64
	for _, bucket := range terminal.Buckets {
		switch bucket.UpperBound {
		case "50ms":
			le50 = bucket.Count
		case "100ms":
			le100 = bucket.Count
		}
	}
	if le50 != 1 || le100 != 2 {
		t.Fatalf("terminal buckets 50ms=%d 100ms=%d", le50, le100)
	}

	prom := m.ExportPrometheus()
	for _, want := range []string{
		`lh_tool_calls_by_name_total{tool="terminal",result="success"} 2`,
		`lh_tool_calls_failed_total{tool="web_fetch",result="failed"} 1`,
		`lh_tool_call_duration_seconds_bucket{tool="terminal",le="50ms"} 1`,
		`lh_tool_call_duration_seconds_bucket{tool="terminal",le="100ms"} 2`,
		`lh_tool_call_duration_seconds_count{tool="web_fetch"} 1`,
		`lh_tool_call_duration_seconds_sum{tool="terminal"} 0`,
	} {
		if !strings.Contains(prom, want) {
			t.Fatalf("missing %q in:\n%s", want, prom)
		}
	}
	if strings.Contains(prom, "lh_function_calls_total") || strings.Contains(prom, "lh_tool_calls_total ") {
		t.Fatalf("tool histogram must not redefine the runtime totals:\n%s", prom)
	}

	agent := &Agent{toolMetrics: m}
	if agent.MetricsPrometheus() != prom || agent.ToolMetricsPrometheus() != prom {
		t.Fatal("MetricsPrometheus must match ToolMetricsPrometheus")
	}
}
