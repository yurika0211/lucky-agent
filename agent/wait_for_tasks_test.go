package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestParentWaitForTasksCollectsMultipleChildren(t *testing.T) {
	dm := tool.NewDelegateManager(tool.DefaultDelegateConfig())
	dm.SetAgentExecutor(func(_ context.Context, description, _ string) (string, error) {
		return description + " done", nil
	})
	reg := tool.NewRegistry()
	reg.Register(tool.DelegateTaskTool(dm))
	reg.Register(tool.WaitForTasksTool(dm))
	a := &Agent{tools: reg, gateway: tool.NewGateway(reg), delegate: dm}

	cfg := DefaultLoopConfig()
	schema := childToolSchemaNames([]string{"delegate_task", "wait_for_tasks", "file_read"}, cfg)
	if missing := missingNames(schema, []string{"wait_for_tasks"}); len(missing) > 0 {
		t.Fatalf("parent schema hid wait_for_tasks: %v", schema)
	}

	first, err := reg.Call("delegate_task", map[string]any{"description": "one"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := reg.Call("delegate_task", map[string]any{"description": "two"})
	if err != nil {
		t.Fatal(err)
	}
	id1 := waitTaskID(t, first)
	id2 := waitTaskID(t, second)

	results := a.executeToolCallsOrdered([]provider.ToolCall{{
		Name:      "wait_for_tasks",
		Arguments: `{"task_ids":["` + id1 + `","` + id2 + `"],"timeout":2,"poll_interval_ms":10,"include_result":true}`,
	}}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	if len(results) != 1 {
		t.Fatalf("results = %d", len(results))
	}
	var wait map[string]any
	if err := json.Unmarshal([]byte(results[0].Result), &wait); err != nil {
		t.Fatalf("decode wait %q: %v", results[0].Result, err)
	}
	if wait["all_terminal"] != true {
		t.Fatalf("wait = %#v", wait)
	}
	body := results[0].Result
	if !strings.Contains(body, "one done") || !strings.Contains(body, "two done") {
		t.Fatalf("missing child results: %s", body)
	}
}

func TestParentWaitForTasksTimesOutAndCancelStopsWait(t *testing.T) {
	dm := tool.NewDelegateManager(tool.DefaultDelegateConfig())
	started := make(chan struct{})
	dm.SetAgentExecutor(func(ctx context.Context, _, _ string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})
	reg := tool.NewRegistry()
	reg.Register(tool.DelegateTaskTool(dm))
	reg.Register(tool.WaitForTasksTool(dm))
	a := &Agent{tools: reg, gateway: tool.NewGateway(reg), delegate: dm}

	raw, err := reg.Call("delegate_task", map[string]any{"description": "hang"})
	if err != nil {
		t.Fatal(err)
	}
	id := waitTaskID(t, raw)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("child did not start")
	}

	timed := a.executeToolCallsOrdered([]provider.ToolCall{{
		Name:      "wait_for_tasks",
		Arguments: `{"task_ids":["` + id + `"],"timeout":0.2,"poll_interval_ms":20,"include_result":true}`,
	}}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	if len(timed) != 1 {
		t.Fatalf("timeout results = %d", len(timed))
	}
	if !strings.Contains(timed[0].Result, `"timed_out":true`) || !strings.Contains(timed[0].Result, `"all_terminal":false`) {
		t.Fatalf("timeout wait = %s", timed[0].Result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan []executedToolCall, 1)
	go func() {
		close(entered)
		done <- a.executeToolCallsOrderedContext(ctx, []provider.ToolCall{{
			Name:      "wait_for_tasks",
			Arguments: `{"task_ids":["` + id + `"],"timeout":5,"poll_interval_ms":20}`,
		}}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cancel wait did not start")
	}
	cancel()
	select {
	case results := <-done:
		if len(results) != 1 {
			t.Fatalf("cancel results = %d", len(results))
		}
		if !strings.Contains(results[0].Result, "Error:") && !strings.Contains(strings.ToLower(results[0].Result), "cancel") {
			t.Fatalf("canceled wait = %s", results[0].Result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled wait_for_tasks did not return")
	}
}

func waitTaskID(t *testing.T, raw string) string {
	t.Helper()
	var payload struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.TaskID == "" {
		t.Fatalf("task id from %q: %v", raw, err)
	}
	return payload.TaskID
}
