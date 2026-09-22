package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/autonomy"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type durableScriptProvider struct {
	chat func(context.Context, []provider.Message) (*provider.Response, error)
}

func (p *durableScriptProvider) Name() string    { return "durable-test" }
func (p *durableScriptProvider) Validate() error { return nil }
func (p *durableScriptProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return p.chat(ctx, messages)
}
func (p *durableScriptProvider) ChatStream(context.Context, []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("not used")
}

const verifiedAssessment = `{"status":"complete","reason":"All requested work has recorded evidence","evidence":["The recorded tool result and final answer satisfy the task"]}`

func durableHarness(t *testing.T, cp durableCheckpoint) (*Agent, *session.Session, *autonomy.TaskQueue, *autonomy.QueueTask, LoopConfig) {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.NewManagerWithDir(root)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewManager(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	sess := mgr.New()
	r := tool.NewRegistry()
	a := &Agent{cfg: cfg, tools: r, gateway: tool.NewGateway(r), sessions: mgr}
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		return &provider.Response{Content: "The requested operation is complete."}, nil
	}}
	q := autonomy.NewTaskQueue(8)
	if _, err := q.EnablePersistence(filepath.Join(root, "queue.json")); err != nil {
		t.Fatal(err)
	}
	task := q.Add("perform requested operation", "", autonomy.PriorityNormal, nil)
	task = q.Pull("worker")
	exec := autonomy.NewExecution(q, task)
	if err := exec.BindSession(sess.ID); err != nil {
		t.Fatal(err)
	}
	cp.Version = 1
	if len(cp.Messages) == 0 {
		cp.Messages = []provider.Message{{Role: "user", Content: "perform requested operation"}}
	}
	data, _ := json.Marshal(cp)
	if err := exec.SaveCheckpoint(data); err != nil {
		t.Fatal(err)
	}
	loopCfg := DefaultLoopConfig()
	loopCfg.Execution, loopCfg.AutoApprove = exec, true
	return a, sess, q, task, loopCfg
}

func pendingCheckpoint() durableCheckpoint {
	call := provider.ToolCall{ID: "call-1", Name: "perform_operation", Arguments: "{}"}
	return durableCheckpoint{
		Messages: []provider.Message{{Role: "user", Content: "perform requested operation"}, {Role: "assistant", ToolCalls: []provider.ToolCall{call}}},
		Pending:  []durablePendingCall{{ID: "step-1-1", Call: call}},
		Batch:    1,
	}
}

func resumeDurableQueue(t *testing.T, q *autonomy.TaskQueue, cfg *LoopConfig) *autonomy.TaskQueue {
	t.Helper()
	reloaded := autonomy.NewTaskQueue(8)
	if _, err := reloaded.EnablePersistence(q.PersistencePath()); err != nil {
		t.Fatal(err)
	}
	task := reloaded.Pull("restarted-worker")
	if task == nil {
		t.Fatal("task was not restored")
	}
	cfg.Execution = autonomy.NewExecution(reloaded, task)
	return reloaded
}

func TestDurableReplayCompletedOperationAfterCrash(t *testing.T) {
	a, sess, q, _, cfg := durableHarness(t, pendingCheckpoint())
	calls := 0
	a.tools.Register(&tool.Tool{Name: "perform_operation", Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { calls++; return "effect", nil }})
	_, err := cfg.Execution.BeginOperation(autonomy.Operation{ID: "step-1-1", Name: "perform_operation", Arguments: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Execution.FinishOperation("step-1-1", "effect already committed", false, nil); err != nil {
		t.Fatal(err)
	}
	q = resumeDurableQueue(t, q, &cfg)
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || calls != 0 {
		t.Fatalf("completed effect replayed or lost: calls=%d result=%+v err=%v", calls, result, err)
	}
	// Crash after the verified checkpoint but before queue completion.
	resumeDurableQueue(t, q, &cfg)
	a.provider = &durableScriptProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		t.Error("verified checkpoint called model again")
		return nil, errors.New("unexpected")
	}}
	result, err = a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || calls != 0 {
		t.Fatalf("verified resume failed: %+v %v", result, err)
	}
}

func TestDurableAmbiguousOperationBlocksUntilReconciled(t *testing.T) {
	a, sess, q, task, cfg := durableHarness(t, pendingCheckpoint())
	calls := 0
	a.tools.Register(&tool.Tool{Name: "perform_operation", Handler: func(map[string]any) (string, error) { calls++; return "effect", nil }})
	_, err := cfg.Execution.BeginOperation(autonomy.Operation{ID: "step-1-1", Name: "perform_operation", Arguments: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	q = resumeDurableQueue(t, q, &cfg)
	_, err = a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if !errors.Is(err, autonomy.ErrBlocked) || calls != 0 {
		t.Fatalf("ambiguous operation repeated: calls=%d err=%v", calls, err)
	}
	if err := q.Block(task.ID, "ambiguous"); err != nil {
		t.Fatal(err)
	}
	if err := q.ResolveOperation(task.ID, "step-1-1", "retry", ""); err != nil {
		t.Fatal(err)
	}
	if err := q.Unblock(task.ID); err != nil {
		t.Fatal(err)
	}
	cfg.Execution = autonomy.NewExecution(q, q.Pull("approved-worker"))
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || calls != 1 {
		t.Fatalf("approved retry failed: calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestDurableBudgetYieldsAndContinuesPendingTools(t *testing.T) {
	a, sess, _, _, cfg := durableHarness(t, durableCheckpoint{})
	calls := 0
	a.tools.Register(&tool.Tool{Name: "perform_operation", Handler: func(map[string]any) (string, error) { calls++; return "evidence", nil }})
	modelCalls := 0
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		modelCalls++
		if modelCalls == 1 {
			return &provider.Response{ToolCalls: pendingCheckpoint().Messages[1].ToolCalls}, nil
		}
		return &provider.Response{Content: "Completed with evidence"}, nil
	}}
	cfg.MaxIterations = 1
	_, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if !errors.Is(err, autonomy.ErrYield) || calls != 0 {
		t.Fatalf("first slice did not checkpoint tool intent: %d %v", calls, err)
	}
	cfg.MaxIterations = 5
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || calls != 1 {
		t.Fatalf("continuation failed: %+v calls=%d err=%v", result, calls, err)
	}
}

func TestDurableCompletionReviewRejectsPrematureAnswer(t *testing.T) {
	a, sess, _, _, cfg := durableHarness(t, durableCheckpoint{Candidate: "I will do it later"})
	reviews := 0
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			reviews++
			if reviews == 1 {
				return &provider.Response{Content: `{"status":"continue","reason":"Deliver the actual answer","evidence":["Only a promise was supplied"]}`}, nil
			}
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		if !strings.Contains(fmt.Sprint(messages), "Deliver the actual answer") {
			t.Error("review feedback not supplied")
		}
		return &provider.Response{Content: "Here is the actual completed answer"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || reviews != 2 || strings.Contains(result.Response, "later") {
		t.Fatalf("review bypassed: %+v reviews=%d err=%v", result, reviews, err)
	}
}

func TestDurableInvalidReviewCannotComplete(t *testing.T) {
	a, sess, _, _, cfg := durableHarness(t, durableCheckpoint{Candidate: "finished"})
	a.provider = &durableScriptProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "looks fine"}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err == nil || result.Verified {
		t.Fatalf("invalid review accepted: %+v %v", result, err)
	}
}

func TestDurableInterruptedToolRemainsAmbiguous(t *testing.T) {
	a, sess, q, task, cfg := durableHarness(t, pendingCheckpoint())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.tools.Register(&tool.Tool{Name: "perform_operation", ContextDetailedHandler: func(exec tool.ExecutionContext, _ map[string]any) (tool.ToolCallResult, error) {
		cancel()
		<-exec.Context.Done()
		return tool.ToolCallResult{}, exec.Context.Err()
	}})
	_, err := a.RunLoopWithSession(ctx, sess, "perform requested operation", cfg)
	if !errors.Is(err, autonomy.ErrBlocked) {
		t.Fatalf("interrupted effect did not block: %v", err)
	}
	got, _ := q.Get(task.ID)
	if len(got.Operations) != 1 || got.Operations[0].State != "started" {
		t.Fatalf("interrupted effect marked complete: %+v", got.Operations)
	}
}

func TestProgressingToolCallsDoNotTriggerConvergence(t *testing.T) {
	state := &streamConvergenceState{repeatToolCallLimit: 3, toolOnlyIterationLimit: 3}
	for i := 0; i < 8; i++ {
		call := provider.ToolCall{Name: "status", Arguments: "{}"}
		if stopped, _ := state.trackToolCallPattern([]provider.ToolCall{call}, ""); stopped {
			t.Fatalf("progressing task stopped on round %d", i)
		}
		state.rememberToolCallResult("status", "{}", fmt.Sprintf("progress=%d", i), 0)
	}
	stopped := false
	for i := 0; i < 5; i++ {
		stopped, _ = state.trackToolCallPattern([]provider.ToolCall{{Name: "status", Arguments: "{}"}}, "")
		if stopped {
			break
		}
		state.rememberToolCallResult("status", "{}", "progress=7", 0)
	}
	if !stopped {
		t.Fatal("stalled loop was not stopped")
	}
}

func TestDurableContextKeepsGoalAndCompleteToolPairs(t *testing.T) {
	a, _, _, _, _ := durableHarness(t, durableCheckpoint{})
	if err := a.cfg.Set("context.max_context_tokens", "1200"); err != nil {
		t.Fatal(err)
	}
	history := []provider.Message{{Role: "system", Content: "Task instructions"}}
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("tool-%d", i)
		history = append(history,
			provider.Message{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: id, Name: "read", Arguments: "{}"}}},
			provider.Message{Role: "tool", ToolCallID: id, Content: strings.Repeat("evidence ", 100)})
	}
	messages, err := a.durableContext(history, "original goal", []string{"tests pass"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messages[1].Content, "original goal") || !strings.Contains(messages[1].Content, "tests pass") {
		t.Fatal("goal or acceptance criteria trimmed")
	}
	ids := make(map[string]bool)
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			ids[call.ID] = true
		}
		if message.Role == "tool" && !ids[message.ToolCallID] {
			t.Fatalf("orphaned tool result %s", message.ToolCallID)
		}
	}
	if len(messages) >= len(history) || !ids["tool-29"] {
		t.Fatal("history was not bounded or latest exchange was dropped")
	}
}

func TestDurableSummaryIsPersistedWithoutDeletingEvidence(t *testing.T) {
	a, _, _, _, cfg := durableHarness(t, durableCheckpoint{})
	if err := a.cfg.Set("context.max_context_tokens", "2000"); err != nil {
		t.Fatal(err)
	}
	cp := durableCheckpoint{Version: 1}
	for i := 0; i < 24; i++ {
		cp.Messages = append(cp.Messages, provider.Message{Role: "assistant", Content: strings.Repeat(fmt.Sprintf("step %d done ", i), 60)})
	}
	originalCount := len(cp.Messages)
	a.provider = &durableScriptProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "Steps are recorded; preserve the remaining work."}, nil
	}}
	saved := false
	err := a.compactDurableCheckpoint(context.Background(), "original goal", &cp, cfg, a.baseProviderSnapshot(), func() error { saved = true; return nil })
	if err != nil || !saved || cp.SummarizedThrough == 0 || cp.Summary == "" || len(cp.Messages) != originalCount {
		t.Fatalf("summary/checkpoint failure: saved=%v cp=%+v err=%v", saved, cp, err)
	}
	if durableMessageTokens(durableHistory(&cp)) >= durableMessageTokens(cp.Messages) {
		t.Fatal("summary did not reduce active context")
	}
}

func TestDurableStartupResumesQueuedWork(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []provider.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		content := "The requested short answer is complete."
		if len(req.Messages) > 0 && strings.HasPrefix(req.Messages[0].Content, "Review whether") {
			content = verifiedAssessment
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}})
	}))
	defer upstream.Close()
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprint(resume), func(t *testing.T) {
			root := t.TempDir()
			cfg, err := config.NewManagerWithDir(root)
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"provider": "openai", "api_key": "test", "api_base": upstream.URL, "model": "gpt-test", "autonomy.recovery.resume_on_start": fmt.Sprint(resume)} {
				if err := cfg.Set(key, value); err != nil {
					t.Fatal(err)
				}
			}
			q := autonomy.NewTaskQueue(8)
			if _, err := q.EnablePersistence(filepath.Join(root, "runtime", "autonomy_queue.json")); err != nil {
				t.Fatal(err)
			}
			task, err := q.AddWithError("Return a short answer", "", autonomy.PriorityNormal, nil)
			if err != nil {
				t.Fatal(err)
			}
			a, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			if !resume {
				if a.Autonomy().Status().Started {
					t.Fatal("resume_on_start=false was ignored")
				}
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				got, _ := a.Autonomy().Queue().Get(task.ID)
				if got.State == autonomy.TaskDone {
					if !got.Verified || got.SessionID == "" || len(got.Checkpoint) == 0 {
						t.Fatalf("incomplete durable result: %+v", got)
					}
					return
				}
				if got.State == autonomy.TaskBlocked {
					t.Fatalf("restored task blocked: %s", got.BlockReason)
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("restored task did not complete")
		})
	}
}
