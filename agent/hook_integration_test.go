package agent

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/hook"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
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
		Name:       "demo.echo",
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
				Match:   []string{"demo.echo"},
				Command: `echo '{"decision":"block","reason":"demo policy"}'`,
			}},
		}),
	}

	results := runGuarded(a, []provider.ToolCall{{Name: "demo_echo", Arguments: "{}"}})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].Result, "demo policy") {
		t.Errorf("expected canonical-name hook to block, got %q", results[0].Result)
	}
	if strings.Contains(results[0].Result, "RAN") {
		t.Error("tool handler should not have executed when aliased hook blocked")
	}
}

func TestExecuteGuardedPolicyResolvesOpenAICompatibleName(t *testing.T) {
	tools := tool.NewRegistry()
	tools.Register(&tool.Tool{
		Name:       "file_write",
		Permission: tool.PermAuto,
		Handler:    func(map[string]any) (string, error) { return "RAN", nil },
	})
	a := &Agent{tools: tools, gateway: tool.NewGateway(tools)}

	results := a.executeToolCallsOrderedGuarded(
		[]provider.ToolCall{{Name: "file_write", Arguments: `{"path":"note.txt","content":"x"}`}},
		true, nil, map[string]int{}, map[string]string{}, 1, false,
		newTurnToolGuard("不要修改文件", nil, nil),
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

func TestExecuteGuardedPreModifyCannotRestoreBlockedAction(t *testing.T) {
	requireShForHooks(t)
	ran := false
	a := newHookTestAgent(hook.Config{
		Enabled: true,
		Timeout: time.Second,
		PreToolUse: []hook.Spec{{
			Command: `echo '{"decision":"modify","modified_arguments":"{\"action\":\"write\"}"}'`,
		}},
	}, func(map[string]any) (string, error) {
		ran = true
		return "RAN", nil
	})
	results := a.executeToolCallsOrderedGuarded(
		[]provider.ToolCall{{Name: "echo_tool", Arguments: `{"action":"read"}`}},
		true, nil, map[string]int{}, map[string]string{}, 1, false,
		newTurnToolGuard("inspect", nil, map[string][]string{"echo_tool": {"write"}}),
	)
	if ran || len(results) != 1 || !strings.Contains(results[0].Result, "write action is disabled") {
		t.Fatalf("hook restored a blocked action: ran=%t results=%+v", ran, results)
	}
}

func TestDurablePreModifyCannotRestoreBlockedAction(t *testing.T) {
	requireShForHooks(t)
	cp := pendingCheckpoint()
	cp.Pending[0].Call.Arguments = `{"action":"read"}`
	a, sess, _, _, cfg := durableHarness(t, cp)
	ran := false
	a.tools.Register(&tool.Tool{Name: "perform_operation", Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) {
		ran = true
		return "RAN", nil
	}})
	a.hooks = hook.NewRunner(hook.Config{Enabled: true, Timeout: time.Second, PreToolUse: []hook.Spec{{
		Command: `echo '{"decision":"modify","modified_arguments":"{\"action\":\"write\"}"}'`,
	}}})
	cfg.BlockedActions = map[string][]string{"perform_operation": {"write"}}
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: `{"status":"complete","reason":"The forbidden write was prevented","evidence":["The blocked action did not run"],"failures":[{"operation_id":"step-1-1","status":"irrelevant","reason":"The modified write was forbidden and is not required for the inspection goal"}]}`}, nil
		}
		return &provider.Response{Content: "The forbidden write was prevented."}, nil
	}}
	if _, err := a.RunLoopWithSession(context.Background(), sess, "inspect", cfg); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("durable hook restored a blocked action")
	}
}
