package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-runtime/turn"
	"github.com/yurika0211/luckyagent-tools/autonomy"
)

func TestDurableRecoveryDoesNotReplayParallelMutations(t *testing.T) {
	for _, tt := range []struct {
		name, args string
		permission tool.PermissionLevel
	}{
		{"memory", `{"action":"remember"}`, tool.PermAuto},
		{"memory", `{"action":"hygiene","operation":"delete"}`, tool.PermAuto},
		{"http_request", `{"method":"POST","allow_mutation":true}`, tool.PermApprove},
		{"file_read", `{}`, tool.PermApprove},
		{"custom_side_effect", `{}`, tool.PermAuto},
	} {
		t.Run(tt.name+"/"+tt.args, func(t *testing.T) {
			cp := pendingCheckpoint()
			call := provider.ToolCall{ID: "call-1", Name: tt.name, Arguments: tt.args}
			cp.Pending[0].Call = call
			cp.Messages[1].ToolCalls = []provider.ToolCall{call}
			a, sess, q, _, cfg := durableHarness(t, cp)
			calls := 0
			a.tools.Register(&tool.Tool{
				Name: tt.name, ParallelSafe: true, Permission: tt.permission,
				Handler: func(map[string]any) (string, error) { calls++; return "side effect", nil },
			})
			if _, err := cfg.Execution.BeginOperation(turn.DurableOperation{ID: cp.Pending[0].ID, Name: call.Name, Arguments: call.Arguments}); err != nil {
				t.Fatal(err)
			}
			resumeDurableQueue(t, q, &cfg)
			_, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
			if !errors.Is(err, autonomy.ErrBlocked) || calls != 0 {
				t.Fatalf("ambiguous effect was replayed: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestDurableRecoveryEmptyReplyDoesNotFinishTruncatedCandidate(t *testing.T) {
	a, sess, _, _, cfg := durableHarness(t, durableCheckpoint{})
	calls := 0
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			t.Error("truncated text reached the completion verifier")
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		calls++
		if calls == 1 {
			return &provider.Response{Content: "unfinished partial", FinishReason: "length"}, nil
		}
		return &provider.Response{}, nil
	}}
	cfg.MaxIterations = 3
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if !errors.Is(err, autonomy.ErrYield) || result.Verified {
		t.Fatalf("incomplete answer completed: result=%+v err=%v", result, err)
	}
	task, err := cfg.Execution.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var cp durableCheckpoint
	if err := json.Unmarshal(task.Checkpoint, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Candidate != "" || cp.Continuation != "unfinished partial" {
		t.Fatalf("partial text lost or promoted: candidate=%q continuation=%q", cp.Candidate, cp.Continuation)
	}
}

func TestDurableProviderCallDeadlineYields(t *testing.T) {
	if err := durableProviderError(context.Background(), context.DeadlineExceeded); !errors.Is(err, autonomy.ErrYield) {
		t.Fatalf("per-call deadline exhausted retry budget: %v", err)
	}
}

func TestDurableReplayChecksDynamicAndOverridePermissions(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{Name: "file_read", ParallelSafe: true, Permission: tool.PermAuto})
	a := &Agent{tools: registry}
	if !a.durableReplaySafeCall("file_read", `{}`) {
		t.Fatal("known read-only call was blocked")
	}
	if err := registry.SetPermissionOverride("file_read", tool.PermApprove); err != nil {
		t.Fatal(err)
	}
	if a.durableReplaySafeCall("file_read", `{}`) {
		t.Fatal("approval override was ignored")
	}
	registry.Register(&tool.Tool{Name: "memory", ParallelSafe: true, Permission: tool.PermAuto,
		PermissionFor: func(map[string]any) tool.PermissionLevel { return tool.PermApprove }})
	if a.durableReplaySafeCall("memory", `{"action":"recall"}`) {
		t.Fatal("per-action approval was ignored")
	}
}

func TestDurableRecoveryStallCounterStartsNewSlice(t *testing.T) {
	a, sess, _, _, cfg := durableHarness(t, durableCheckpoint{ModelStalls: 3})
	calls := 0
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		calls++
		if calls <= 2 {
			return nil, nil
		}
		return &provider.Response{Content: "usable answer"}, nil
	}}
	cfg.MaxIterations = 8
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || calls != 3 {
		t.Fatalf("new slice did not get its retry allowance: calls=%d result=%+v err=%v", calls, result, err)
	}
}
