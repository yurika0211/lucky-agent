package agent

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/metrics"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestBackgroundTerminalStartReturnsTaskIDWithoutKill(t *testing.T) {
	a, sess := newBackgroundTerminalAgent(t)

	start := time.Now()
	started := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command":    "sleep 2",
		"background": true,
		"timeout":    1,
	})
	if time.Since(start) > time.Second {
		t.Fatalf("background start took %s", time.Since(start))
	}
	if started.Result == "" || strings.HasPrefix(started.Result, "Error:") {
		t.Fatalf("start = %s", started.Result)
	}
	id := backgroundTerminalTaskID(started.Result)
	if id == "" || !strings.Contains(started.Result, "background terminal started") {
		t.Fatalf("missing task_id in %q", started.Result)
	}
	t.Cleanup(func() { killBackgroundTask(t, a, sess, id) })

	time.Sleep(1200 * time.Millisecond)
	snap := runNamedTool(t, a, sess, "terminal_output", map[string]any{"task_id": id, "timeout": 0})
	if !strings.Contains(snap.Result, "state=running") {
		t.Fatalf("background sleep was killed inside the foreground timeout window: %s", snap.Result)
	}
}

func TestBackgroundTerminalOutputWaitDoesNotKill(t *testing.T) {
	a, sess := newBackgroundTerminalAgent(t)
	started := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command":    "sleep 2",
		"background": true,
	})
	id := backgroundTerminalTaskID(started.Result)
	if id == "" {
		t.Fatalf("missing task_id in %q", started.Result)
	}
	t.Cleanup(func() { killBackgroundTask(t, a, sess, id) })

	start := time.Now()
	snap := runNamedTool(t, a, sess, "terminal_output", map[string]any{"task_id": id, "timeout": 1})
	if time.Since(start) > 3*time.Second {
		t.Fatalf("wait overran: %s", time.Since(start))
	}
	if !strings.Contains(snap.Result, "state=running") {
		t.Fatalf("wait killed or finished early: %s", snap.Result)
	}
}

func TestBackgroundTerminalKillStopsTask(t *testing.T) {
	a, sess := newBackgroundTerminalAgent(t)
	started := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command":    "sleep 30",
		"background": true,
	})
	id := backgroundTerminalTaskID(started.Result)
	if id == "" {
		t.Fatalf("missing task_id in %q", started.Result)
	}
	killed := runNamedTool(t, a, sess, "terminal_kill", map[string]any{"task_id": id})
	if !strings.Contains(killed.Result, "state=killed") && !strings.Contains(killed.Result, "state=exited") {
		t.Fatalf("kill = %s", killed.Result)
	}
	snap := runNamedTool(t, a, sess, "terminal_output", map[string]any{"task_id": id, "timeout": 0})
	if strings.Contains(snap.Result, "state=running") {
		t.Fatalf("task still running after kill: %s", snap.Result)
	}
}

func TestForegroundTerminalStillTimesOut(t *testing.T) {
	a, sess := newBackgroundTerminalAgent(t)
	start := time.Now()
	fg := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command": "sleep 30",
		"timeout": 1,
	})
	if !strings.Contains(fg.Result, "timed out after 1 seconds") {
		t.Fatalf("foreground timeout = %s", fg.Result)
	}
	if time.Since(start) > 8*time.Second {
		t.Fatalf("foreground kill returned too slowly: %s", time.Since(start))
	}
}

func TestBackgroundTerminalCompleteNotifiesSession(t *testing.T) {
	a, sess := newBackgroundTerminalAgent(t)
	var pushedSession, pushed string
	a.SetSessionEventSink(func(sessionID, content string) {
		pushedSession = sessionID
		pushed = content
	})

	started := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command":    "printf bg-done\\n",
		"background": true,
	})
	id := backgroundTerminalTaskID(started.Result)
	if id == "" {
		t.Fatalf("missing task_id in %q", started.Result)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(pushed, id) && (strings.Contains(pushed, "exited") || strings.Contains(pushed, "killed")) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pushedSession != sess.ID || !strings.Contains(pushed, id) {
		t.Fatalf("push = session %q content %q", pushedSession, pushed)
	}
	reloaded, ok := a.sessions.Get(sess.ID)
	if !ok {
		t.Fatal("session missing")
	}
	messages := reloaded.GetMessages()
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || !strings.Contains(messages[len(messages)-1].Content, id) {
		t.Fatalf("messages = %#v", messages)
	}
}

func TestBackgroundTerminalMetricsSplitFromForeground(t *testing.T) {
	a, sess := newBackgroundTerminalAgent(t)
	bg := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command":    "printf bg\\n",
		"background": true,
	})
	id := backgroundTerminalTaskID(bg.Result)
	if id != "" {
		t.Cleanup(func() { killBackgroundTask(t, a, sess, id) })
	}
	fg := runNamedTool(t, a, sess, "terminal", map[string]any{
		"command": "printf fg\\n",
		"timeout": 5,
	})
	a.recordExecutedToolMetrics([]toolCallLog{
		{Name: bg.ToolCall.Name, Arguments: bg.ToolCall.Arguments, Result: bg.Result, Duration: bg.Duration},
		{Name: fg.ToolCall.Name, Arguments: fg.ToolCall.Arguments, Result: fg.Result, Duration: fg.Duration},
	})
	snap := a.ToolDurationSnapshot()
	if snap["terminal_background"].Calls != 1 {
		t.Fatalf("background calls = %#v", snap["terminal_background"])
	}
	if snap["terminal"].Calls != 1 {
		t.Fatalf("foreground calls = %#v", snap["terminal"])
	}
	prom := a.ToolMetricsPrometheus()
	if !strings.Contains(prom, `lh_tool_calls_by_name_total{tool="terminal_background",result="success"} 1`) {
		t.Fatalf("missing background series in:\n%s", prom)
	}
	if !strings.Contains(prom, `lh_tool_calls_by_name_total{tool="terminal",result="success"} 1`) {
		t.Fatalf("missing foreground series in:\n%s", prom)
	}
}

func TestToolMetricsNameSplitsBackgroundTerminal(t *testing.T) {
	if got := toolMetricsName("terminal", `{"command":"sleep 1","background":true}`, "task_id=shell-1\nbackground terminal started\n", 8*time.Millisecond); got != "terminal_background" {
		t.Fatalf("background name = %q", got)
	}
	if got := toolMetricsName("terminal", `{"command":"ls"}`, "ok", 40*time.Millisecond); got != "terminal" {
		t.Fatalf("foreground name = %q", got)
	}
	if got := toolMetricsName("web_search", `{"background":true}`, "", 12*time.Second); got != "web_search" {
		t.Fatalf("non-terminal name = %q", got)
	}
}

func newBackgroundTerminalAgent(t *testing.T) (*Agent, *session.Session) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("process-group signals are unix-only")
	}
	registry := tool.NewRegistry()
	tool.RegisterBuiltinTools(registry)
	sessions, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess := sessions.New()
	a := &Agent{
		tools:       registry,
		gateway:     tool.NewGateway(registry),
		sessions:    sessions,
		toolMetrics: newToolDurationMetrics(),
		metrics:     metrics.NewMetrics(),
	}
	a.installBackgroundTerminalNotify()
	t.Cleanup(func() { _ = a.Close() })
	return a, sess
}

func runNamedTool(t *testing.T, a *Agent, sess *session.Session, name string, args map[string]any) executedToolCall {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	results := a.executeToolCallsOrdered([]provider.ToolCall{{
		ID:        name + "-1",
		Name:      name,
		Arguments: string(raw),
	}}, true, sess, map[string]int{}, map[string]string{}, 0, false)
	if len(results) != 1 {
		t.Fatalf("%s results = %d", name, len(results))
	}
	return results[0]
}

func killBackgroundTask(t *testing.T, a *Agent, sess *session.Session, id string) {
	t.Helper()
	if id == "" {
		return
	}
	_ = runNamedTool(t, a, sess, "terminal_kill", map[string]any{"task_id": id})
}
