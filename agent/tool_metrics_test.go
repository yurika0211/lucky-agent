package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/tool"
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

func TestToolDurationSplitsBackgroundAndLongTerminal(t *testing.T) {
	a := &Agent{toolMetrics: newToolDurationMetrics()}
	a.recordExecutedToolMetrics([]toolCallLog{
		{Name: "terminal", Arguments: `{"command":"ls"}`, Result: "ok", Duration: 40 * time.Millisecond},
		{Name: "terminal", Arguments: `{"command":"sleep 30","background":true}`, Result: "task_id=shell-1\nbackground terminal started\ncommand: sleep 30", Duration: 8 * time.Millisecond},
		{Name: "terminal", Arguments: `{"command":"make test"}`, Result: "ok", Duration: 12 * time.Second},
	})
	snap := a.ToolDurationSnapshot()
	if snap["terminal"].Calls != 1 {
		t.Fatalf("interactive terminal = %#v", snap["terminal"])
	}
	if snap["terminal_background"].Calls != 1 {
		t.Fatalf("background terminal = %#v", snap["terminal_background"])
	}
	if snap["terminal_long"].Calls != 1 {
		t.Fatalf("long terminal = %#v", snap["terminal_long"])
	}
	prom := a.ToolMetricsPrometheus()
	for _, want := range []string{
		`lh_tool_calls_by_name_total{tool="terminal",result="success"} 1`,
		`lh_tool_calls_by_name_total{tool="terminal_background",result="success"} 1`,
		`lh_tool_calls_by_name_total{tool="terminal_long",result="success"} 1`,
	} {
		if !strings.Contains(prom, want) {
			t.Fatalf("missing %q in:\n%s", want, prom)
		}
	}
}

func TestExecuteToolRecordsMetricsOnce(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "web_search",
		Description: "test search",
		Enabled:     true,
		Handler: func(map[string]any) (string, error) {
			return "ok", nil
		},
	})
	a := &Agent{
		metrics:     metrics.NewMetrics(),
		toolMetrics: newToolDurationMetrics(),
		tools:       registry,
		gateway:     tool.NewGateway(registry),
	}
	out, err := a.executeToolWithSessionDetailedContext(context.Background(), "web_search", `{"q":"x"}`, true, nil, "cli")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out.Output != "ok" {
		t.Fatalf("output = %q", out.Output)
	}
	if got := a.metrics.Snapshot().ToolCalls; got != 1 {
		t.Fatalf("RecordToolCall count = %d, want 1", got)
	}
	snap := a.ToolDurationSnapshot()["web_search"]
	if snap.Calls != 1 || snap.Failures != 0 {
		t.Fatalf("web_search snapshot = %#v", snap)
	}

	a.recordExecutedToolMetrics(nil)
	if got := a.metrics.Snapshot().ToolCalls; got != 1 {
		t.Fatalf("empty batch changed tool count to %d", got)
	}
}

func TestChatWithSessionInputDoesNotDoubleCountExecutedTools(t *testing.T) {
	a := &Agent{
		metrics:     metrics.NewMetrics(),
		toolMetrics: newToolDurationMetrics(),
	}
	a.recordExecutedToolMetrics([]toolCallLog{{
		Name: "web_search", Arguments: `{"q":"x"}`, Result: "ok", Duration: 20 * time.Millisecond,
	}})
	before := a.metrics.Snapshot().ToolCalls
	a.metrics.RecordChatRequest()
	if got := a.metrics.Snapshot().ToolCalls; got != before {
		t.Fatalf("chat request recording changed tool calls from %d to %d", before, got)
	}
	if a.ToolDurationSnapshot()["web_search"].Calls != 1 {
		t.Fatal("named histogram must stay at the execution record")
	}
}

func TestToolMetricsNameSplitsBackgroundAndLong(t *testing.T) {
	if got := toolMetricsName("terminal", `{"command":"sleep 1","background":true}`, "task_id=shell-1\nbackground terminal started\n", 8*time.Millisecond); got != "terminal_background" {
		t.Fatalf("background name = %q", got)
	}
	if got := toolMetricsName("terminal", `{"command":"make test"}`, "ok", 12*time.Second); got != "terminal_long" {
		t.Fatalf("long name = %q", got)
	}
	if got := toolMetricsName("terminal", `{"command":"ls"}`, "ok", 40*time.Millisecond); got != "terminal" {
		t.Fatalf("interactive name = %q", got)
	}
	if got := toolMetricsName("web_search", `{"background":true}`, "", 12*time.Second); got != "web_search" {
		t.Fatalf("non-terminal name = %q", got)
	}
}
