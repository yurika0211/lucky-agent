package agent

import (
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/tool"
)

func TestParallelToolExecution(t *testing.T) {
	slowSearch := &tool.Tool{
		Name:         "web_search",
		Description:  "Search the web",
		Permission:   tool.PermAuto,
		Category:     tool.CatBuiltin,
		ParallelSafe: true,
		Handler: func(args map[string]any) (string, error) {
			time.Sleep(100 * time.Millisecond)
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
			time.Sleep(100 * time.Millisecond)
			return "page content", nil
		},
	}
	serialShell := &tool.Tool{
		Name:         "shell",
		Description:  "Run shell command",
		Permission:   tool.PermApprove,
		Category:     tool.CatBuiltin,
		ParallelSafe: false,
		Handler: func(args map[string]any) (string, error) {
			time.Sleep(100 * time.Millisecond)
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
	sh, _ := reg.Get("shell")
	if sh.ParallelSafe {
		t.Error("shell should NOT be ParallelSafe")
	}
}

func TestParallelExecutionTiming(t *testing.T) {
	// 验证并发执行比串行快
	reg := tool.NewRegistry()

	for i := 0; i < 3; i++ {
		name := []string{"tool_a", "tool_b", "tool_c"}[i]
		reg.Register(&tool.Tool{
			Name:         name,
			Description:  "Test tool",
			Permission:   tool.PermAuto,
			Category:     tool.CatBuiltin,
			ParallelSafe: true,
			Handler: func(args map[string]any) (string, error) {
				time.Sleep(50 * time.Millisecond)
				return "ok", nil
			},
		})
	}

	// 并发执行 3 个 50ms 的工具
	start := time.Now()
	type result struct {
		idx    int
		output string
	}
	ch := make(chan result, 3)

	for i := 0; i < 3; i++ {
		go func(idx int) {
			output, _ := reg.Call([]string{"tool_a", "tool_b", "tool_c"}[idx], nil)
			ch <- result{idx, output}
		}(i)
	}

	for i := 0; i < 3; i++ {
		<-ch
	}
	elapsed := time.Since(start)

	// 并发应该 < 150ms（串行需要 150ms）
	if elapsed >= 150*time.Millisecond {
		t.Errorf("Parallel execution took %v, expected < 150ms", elapsed)
	}
}
