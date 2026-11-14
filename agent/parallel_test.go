package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestParallelToolExecution(t *testing.T) {
	slowSearch := &tool.Tool{
		Name:         "web_search",
		Description:  "Search the web",
		Permission:   tool.PermAuto,
		Category:     tool.CatBuiltin,
		ParallelSafe: true,
		Handler: func(args map[string]any) (string, error) {
			return "search results", nil
		},
	}
	slowFetch := &tool.Tool{
		Name:         "web_fetch",
		Description:  "Fetch a URL",
		Permission:   tool.PermAuto,
		Category:     tool.CatBuiltin,
		ParallelSafe: true,
		Handler: func(args map[string]any) (string, error) {
			return "page content", nil
		},
	}
	serialShell := &tool.Tool{
		Name:         "terminal",
		Description:  "Run terminal command",
		Permission:   tool.PermApprove,
		Category:     tool.CatBuiltin,
		ParallelSafe: false,
		Handler: func(args map[string]any) (string, error) {
			return "command output", nil
		},
	}

	reg := tool.NewRegistry()
	reg.Register(slowSearch)
	reg.Register(slowFetch)
	reg.Register(serialShell)

	s, _ := reg.Get("web_search")
	if !s.ParallelSafe {
		t.Error("web_search should be ParallelSafe")
	}
	f, _ := reg.Get("web_fetch")
	if !f.ParallelSafe {
		t.Error("web_fetch should be ParallelSafe")
	}
	sh, _ := reg.Get("terminal")
	if sh.ParallelSafe {
		t.Error("terminal should NOT be ParallelSafe")
	}
}

func newParallelDispatchAgent(tools ...*tool.Tool) *Agent {
	reg := tool.NewRegistry()
	for _, item := range tools {
		reg.Register(item)
	}
	return &Agent{tools: reg, gateway: tool.NewGateway(reg)}
}

func waitStarted(t *testing.T, started <-chan string, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	seen := map[string]bool{}
	for len(seen) < want {
		select {
		case name := <-started:
			seen[name] = true
		case <-deadline:
			t.Fatalf("timed out waiting for %d overlapping starts, seen=%v", want, seen)
		}
	}
}

func TestExecuteToolCallsOrderedRunsParallelSafeTogether(t *testing.T) {
	started := make(chan string, 3)
	release := make(chan struct{})
	handler := func(name string) func(map[string]any) (string, error) {
		return func(map[string]any) (string, error) {
			started <- name
			<-release
			return name + "-ok", nil
		}
	}
	a := newParallelDispatchAgent(
		&tool.Tool{Name: "tool_a", Permission: tool.PermAuto, ParallelSafe: true, Handler: handler("tool_a")},
		&tool.Tool{Name: "tool_b", Permission: tool.PermAuto, ParallelSafe: true, Handler: handler("tool_b")},
		&tool.Tool{Name: "tool_c", Permission: tool.PermAuto, ParallelSafe: true, Handler: handler("tool_c")},
	)
	done := make(chan []executedToolCall, 1)
	go func() {
		done <- a.executeToolCallsOrdered([]provider.ToolCall{
			{Name: "tool_a", Arguments: "{}"},
			{Name: "tool_b", Arguments: "{}"},
			{Name: "tool_c", Arguments: "{}"},
		}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	}()
	waitStarted(t, started, 3)
	close(release)
	select {
	case results := <-done:
		if len(results) != 3 {
			t.Fatalf("results = %d, want 3", len(results))
		}
		for i, result := range results {
			if result.Index != i {
				t.Fatalf("result order[%d] = %d", i, result.Index)
			}
			if result.Result != result.ToolCall.Name+"-ok" {
				t.Fatalf("result[%d] = %q", i, result.Result)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("production dispatch did not return")
	}
}

func TestExecuteToolCallsOrderedSerializesNonParallelSafe(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	a := newParallelDispatchAgent(
		&tool.Tool{Name: "web_search", Permission: tool.PermAuto, ParallelSafe: true, Handler: func(map[string]any) (string, error) {
			close(firstStarted)
			<-releaseFirst
			return "web_search-ok", nil
		}},
		&tool.Tool{Name: "terminal", Permission: tool.PermAuto, ParallelSafe: false, Handler: func(map[string]any) (string, error) {
			close(secondStarted)
			return "terminal-ok", nil
		}},
	)
	done := make(chan []executedToolCall, 1)
	go func() {
		done <- a.executeToolCallsOrdered([]provider.ToolCall{
			{Name: "web_search", Arguments: "{}"},
			{Name: "terminal", Arguments: "{}"},
		}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	}()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("web_search did not start")
	}
	select {
	case <-secondStarted:
		t.Fatal("terminal overlapped web_search")
	default:
	}
	close(releaseFirst)
	select {
	case results := <-done:
		if len(results) != 2 || results[0].ToolCall.Name != "web_search" || results[1].ToolCall.Name != "terminal" {
			t.Fatalf("results = %#v", results)
		}
		if results[0].Result != "web_search-ok" || results[1].Result != "terminal-ok" {
			t.Fatalf("result text = %#v", results)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serialized dispatch did not return")
	}
}

func TestExecuteToolCallsOrderedKeepsResultsAfterOneFailure(t *testing.T) {
	a := newParallelDispatchAgent(
		&tool.Tool{Name: "tool_a", Permission: tool.PermAuto, ParallelSafe: true, Handler: func(map[string]any) (string, error) {
			return "a-ok", nil
		}},
		&tool.Tool{Name: "tool_b", Permission: tool.PermAuto, ParallelSafe: true, Handler: func(map[string]any) (string, error) {
			return "", errors.New("b exploded")
		}},
		&tool.Tool{Name: "tool_c", Permission: tool.PermAuto, ParallelSafe: true, Handler: func(map[string]any) (string, error) {
			return "c-ok", nil
		}},
	)
	results := a.executeToolCallsOrdered([]provider.ToolCall{
		{Name: "tool_a", Arguments: "{}"},
		{Name: "tool_b", Arguments: "{}"},
		{Name: "tool_c", Arguments: "{}"},
	}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3", len(results))
	}
	if results[0].Index != 0 || results[0].Result != "a-ok" {
		t.Fatalf("first result = %+v", results[0])
	}
	if results[1].Index != 1 || !strings.Contains(results[1].Result, "b exploded") {
		t.Fatalf("failed result = %+v", results[1])
	}
	if results[2].Index != 2 || results[2].Result != "c-ok" {
		t.Fatalf("third result = %+v", results[2])
	}
}

func TestExecuteToolCallsOrderedCancelStopsContextHandler(t *testing.T) {
	entered := make(chan struct{})
	a := newParallelDispatchAgent(&tool.Tool{
		Name:         "slow_wait",
		Permission:   tool.PermAuto,
		ParallelSafe: true,
		ContextDetailedHandler: func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			close(entered)
			select {
			case <-exec.Context.Done():
				return tool.ToolCallResult{}, exec.Context.Err()
			case <-time.After(2 * time.Second):
				return tool.ToolCallResult{Output: "late"}, nil
			}
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []executedToolCall, 1)
	go func() {
		done <- a.executeToolCallsOrderedContext(ctx, []provider.ToolCall{{Name: "slow_wait", Arguments: "{}"}}, true, nil, map[string]int{}, map[string]string{}, 1, false)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case results := <-done:
		if len(results) != 1 {
			t.Fatalf("results = %d", len(results))
		}
		if !strings.Contains(results[0].Result, "Error:") {
			t.Fatalf("canceled result = %q", results[0].Result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled dispatch did not return")
	}
}
