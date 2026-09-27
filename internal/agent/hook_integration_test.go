package agent

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/hook"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/tool"
)

func requireShForHooks(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available on this platform")
	}
}

// newHookTestAgent builds a minimal Agent wired with just a tool registry,
// gateway, and hook runner — enough to exercise the tool-execution boundary
// without the full New() constructor.
func newHookTestAgent(hookCfg hook.Config, handler func(map[string]any) (string, error)) *Agent {
	tools := tool.NewRegistry()
	tools.Register(&tool.Tool{
		Name:        "echo_tool",
		Description: "echo",
		Handler:     handler,
		Permission:  tool.PermAuto,
		Category:    tool.CatBuiltin,
	})
	return &Agent{
		tools:   tools,
		gateway: tool.NewGateway(tools),
		hooks:   hook.NewRunner(hookCfg),
	}
}

func runGuarded(a *Agent, calls []provider.ToolCall) []executedToolCall {
	return a.executeToolCallsOrderedGuarded(calls, true, nil, map[string]int{}, map[string]string{}, 1, false, nil)
}

func TestExecuteGuardedPreToolUseBlocks(t *testing.T) {
	requireShForHooks(t)
	a := newHookTestAgent(hook.Config{
		Enabled: true,
		Timeout: 2 * time.Second,
		PreToolUse: []hook.Spec{{
			Match:   []string{"echo_tool"},
			Command: `echo '{"decision":"block","reason":"nope"}'`,
		}},
	}, func(map[string]any) (string, error) { return "RAN", nil })

	results := runGuarded(a, []provider.ToolCall{{Name: "echo_tool", Arguments: "{}"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Result, "nope") {
		t.Errorf("expected block reason in result, got %q", results[0].Result)
	}
	if strings.Contains(results[0].Result, "RAN") {
		t.Error("tool handler should not have executed when blocked")
	}
}

func TestExecuteGuardedPreToolUseResolvesOpenAICompatibleName(t *testing.T) {
	requireShForHooks(t)
	tools := tool.NewRegistry()
	tools.Register(&tool.Tool{
		Name:       "codex.start_turn",
		Permission: tool.PermAuto,
		Handler:    func(map[string]any) (string, error) { return "RAN", nil },
	})
	a := &Agent{
		tools:   tools,
		gateway: tool.NewGateway(tools),
		hooks: hook.NewRunner(hook.Config{
			Enabled: true,
			Timeout: 2 * time.Second,
			PreToolUse: []hook.Spec{{
				Match:   []string{"codex.start_turn"},
				Command: `echo '{"decision":"block","reason":"codex policy"}'`,
			}},
		}),
	}

	results := runGuarded(a, []provider.ToolCall{{Name: "codex_start_turn", Arguments: "{}"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Result, "codex policy") {
		t.Errorf("expected canonical-name hook to block, got %q", results[0].Result)
	}
	if strings.Contains(results[0].Result, "RAN") {
		t.Error("tool handler should not have executed when aliased hook blocked")
	}
}

func TestExecuteGuardedPolicyResolvesOpenAICompatibleName(t *testing.T) {
	tools := tool.NewRegistry()
	tools.Register(&tool.Tool{
		Name:       "codex.start_turn",
		Permission: tool.PermAuto,
		Handler:    func(map[string]any) (string, error) { return "RAN", nil },
	})
	a := &Agent{tools: tools, gateway: tool.NewGateway(tools)}

	results := a.executeToolCallsOrderedGuarded(
		[]provider.ToolCall{{Name: "codex_start_turn", Arguments: "{}"}},
		true, nil, map[string]int{}, map[string]string{}, 1, false,
		newTurnToolGuard("不要修改文件", nil),
	)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Result, "read-only/no-file-modification") {
		t.Errorf("expected canonical-name policy to block, got %q", results[0].Result)
	}
	if strings.Contains(results[0].Result, "RAN") {
		t.Error("tool handler should not have executed when aliased policy blocked")
	}
}

func TestExecuteGuardedPostToolUseRewritesOutput(t *testing.T) {
	requireShForHooks(t)
	a := newHookTestAgent(hook.Config{
		Enabled: true,
		Timeout: 2 * time.Second,
		PostToolUse: []hook.Spec{{
			Command: `echo '{"decision":"modify","modified_output":"[REDACTED]"}'`,
		}},
	}, func(map[string]any) (string, error) { return "secret token=abc", nil })

	results := runGuarded(a, []provider.ToolCall{{Name: "echo_tool", Arguments: "{}"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Result != "[REDACTED]" {
		t.Errorf("expected post-hook to rewrite output, got %q", results[0].Result)
	}
}

func TestExecuteGuardedDisabledHooksUnchanged(t *testing.T) {
	a := newHookTestAgent(hook.Config{Enabled: false}, func(map[string]any) (string, error) { return "RAN", nil })
	results := runGuarded(a, []provider.ToolCall{{Name: "echo_tool", Arguments: "{}"}})
	if len(results) != 1 || results[0].Result != "RAN" {
		t.Errorf("disabled hooks must not alter execution, got %+v", results)
	}
}

func TestExecuteGuardedPreModifyThenExecute(t *testing.T) {
	requireShForHooks(t)
	var gotArgs string
	a := newHookTestAgent(hook.Config{
		Enabled: true,
		Timeout: 2 * time.Second,
		PreToolUse: []hook.Spec{{
			Command: `echo '{"decision":"modify","modified_arguments":"{\"path\":\"safe\"}"}'`,
		}},
	}, func(args map[string]any) (string, error) {
		if p, ok := args["path"].(string); ok {
			gotArgs = p
		}
		return "ok", nil
	})

	results := runGuarded(a, []provider.ToolCall{{Name: "echo_tool", Arguments: `{"path":"danger"}`}})
	if len(results) != 1 || results[0].Result != "ok" {
		t.Fatalf("expected tool to run, got %+v", results)
	}
	if gotArgs != "safe" {
		t.Errorf("expected rewritten args to reach the tool, got path=%q", gotArgs)
	}
}
