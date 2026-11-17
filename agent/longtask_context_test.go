package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/soul"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func contextTestAgent(t *testing.T) (*Agent, *session.Session) {
	t.Helper()
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := config.DefaultConfig()
	c.Context.AutoCompact = true
	mgr.SetConfig(c)
	sessions, err := session.NewManager(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{cfg: mgr, tools: tool.NewRegistry(), sessions: sessions, soul: &soul.Soul{}, contextWin: contextx.NewContextWindow(contextx.WindowConfig{MaxTokens: 8000, ReservedTokens: 2000, Strategy: contextx.TrimLowPriority}), contextEst: contextx.NewTokenEstimator(8000)}
	a.gateway = tool.NewGateway(a.tools)
	a.registerContextHistoryTool()
	sess := sessions.New()
	sess.SetCwd(t.TempDir())
	return a, sess
}

func TestLongtaskProjectRulesSurviveLongSoul(t *testing.T) {
	a, sess := contextTestAgent(t)
	a.soul = &soul.Soul{Content: strings.Repeat("Runtime identity background. ", 2000)}
	const rule = "PROJECT_ACCEPTANCE_SENTINEL"
	if err := os.WriteFile(filepath.Join(sess.GetCwd(), "AGENTS.md"), []byte("Keep source evidence.\n"+strings.Repeat("project detail\n", 400)+rule), 0600); err != nil {
		t.Fatal(err)
	}
	got := a.BuildContextPackerSnapshot(context.Background(), sess, TextUserTurnInput("CURRENT_REQUEST_SENTINEL"))
	text := messagesToTestText(got.Messages)
	if !strings.Contains(text, rule) || !strings.Contains(text, "CURRENT_REQUEST_SENTINEL") {
		t.Fatal("project constraint or current request lost")
	}
	if got.TotalTokens > a.contextInputLimit() {
		t.Fatalf("over budget: %d", got.TotalTokens)
	}
}

func TestLongtaskProjectRulesUseDedicatedTextClip(t *testing.T) {
	a, _ := contextTestAgent(t)
	p := newContextPlanner(a, defaultContextBuildOptions())
	const firstRule = "PROJECT_RULE_FIRST: preserve the current request"
	const lastRule = "PROJECT_RULE_LAST: verify before claiming completion"
	project := firstRule + "\n" + strings.Repeat("ordinary project guidance\n", 2000) + lastRule

	const budget = 64
	got := p.fitProjectContextToBudget(project, budget)
	if !strings.Contains(got, firstRule) || !strings.Contains(got, lastRule) {
		t.Fatalf("dedicated project clip lost a rule: %q", got)
	}
	if p.est.Estimate(got) > budget {
		t.Fatalf("project clip exceeded budget: %d > %d", p.est.Estimate(got), budget)
	}
	if strings.Contains(got, "lines omitted for context") {
		t.Fatalf("project context used tool-output omission metadata: %q", got)
	}
}

func TestLongtaskEmergencyCompactUsesActualInputBudget(t *testing.T) {
	a, sess := contextTestAgent(t)
	sess.AddProviderMessage(provider.Message{Role: "user", Content: strings.Repeat("large current task ", 1500)})
	sess.AddProviderMessage(provider.Message{Role: "assistant", Content: "Work remains incomplete."})
	if !a.shouldAutoCompactSession(sess, "continue", false) {
		t.Fatal("few huge messages bypassed emergency compaction")
	}
}

func TestLongtaskTenCompactionsStayBoundedAndRecoverable(t *testing.T) {
	// Originally verified that ten successive local compacts stay inside the
	// input budget, keep every segment, and still recover stage 0. It still
	// does that. Each stage keeps retain 1, then adds new raw. The candidate
	// appends the new boundary to the full transcript, the same way
	// AddCompactBoundary persists it. A local fallback rewrites and truncates,
	// so the full request can grow even when the archive stays recoverable.
	// This fixture uses a short summary that keeps the stage label, so the
	// net-benefit gate sees a smaller full request. Validation stays on.
	a, sess := contextTestAgent(t)
	loaded := a.cfg.Get()
	loaded.ContextBudget.HistoryRatio = 0.70
	loaded.ContextBudget.SystemRatio = 0.05
	loaded.ContextBudget.MemoryRatio = 0.05
	loaded.ContextBudget.RAGRatio = 0.05
	loaded.ContextBudget.ToolResultRatio = 0.15
	a.cfg.SetConfig(loaded)
	a.provider = &staticChatProvider{name: "longtask", content: "Current user goal:\nContinue the original goal.\nCompleted work:\nStage work recorded.\nPending work:\nFinish remaining work.\nKey files and functions:\nNo file path.\nCommands and test results:\nNo command was run.\nUser constraints:\nKeep the stage label.\nUncertain facts:\nNone."}
	for stage := 0; stage < 10; stage++ {
		for round := 0; round < 3; round++ {
			sess.AddProviderMessage(provider.Message{Role: "user", Content: fmt.Sprintf("Stage %d round %d: keep the original goal and finish remaining work %s", stage, round, strings.Repeat("stage-note ", 80))})
			sess.AddProviderMessage(provider.Message{Role: "assistant", Content: strings.Repeat("Inspected source, pending verification. ", 40)})
		}
		a.provider = &staticChatProvider{name: "longtask", content: fmt.Sprintf("Current user goal:\nContinue stage %d.\nCompleted work:\nStage %d recorded.\nPending work:\nFinish remaining work.\nKey files and functions:\nNo file path.\nCommands and test results:\nNo command was run.\nUser constraints:\nKeep the stage label.\nUncertain facts:\nNone.", stage, stage)}
		if _, err := a.CompactSessionWithOptions(context.Background(), sess, "test", CompactSessionOptions{RetainRecentTurns: 1}); err != nil {
			t.Fatal(err)
		}
	}
	segments, _, _ := session.CompactSegments(sess.GetMessages())
	if len(segments) != 10 {
		t.Fatalf("archive lost: %d", len(segments))
	}
	snapshot := a.BuildContextPackerSnapshot(context.Background(), sess, TextUserTurnInput("LATEST_TASK_CONSTRAINT: do not repeat completed effects"))
	if snapshot.TotalTokens > a.contextInputLimit() || !strings.Contains(messagesToTestText(snapshot.Messages), "LATEST_TASK_CONSTRAINT") {
		t.Fatal("history projection grew without bound or lost latest task")
	}
	body, err := readContextHistory(sess, map[string]any{"action": "segment", "segment_id": segments[0].ID, "limit": 6000})
	if err != nil || !strings.Contains(body, "Stage 0") {
		t.Fatalf("old segment not recoverable: %v %s", err, body)
	}
	if _, err := a.durableContext(snapshot.Messages, "finish the remaining original task", nil); err != nil {
		t.Fatalf("compacted session cannot continue: %v", err)
	}
}

func TestLongtaskHistoryReaderIsBoundToCallerAndResolvesBlob(t *testing.T) {
	a, owner := contextTestAgent(t)
	other := a.sessions.New()
	large := strings.Repeat("large log line\n", 5000) + "FINAL_FAILURE_SENTINEL"
	owner.AddProviderMessage(provider.Message{Role: "tool", Name: "terminal", ToolCallID: "big-result", Content: large})
	args := fmt.Sprintf(`{"action":"result","tool_call_id":"big-result","offset":%d,"limit":100,"session_id":%q}`, len([]rune(large))-25, other.ID)
	got, err := a.executeToolWithSessionDetailedContext(context.Background(), "context_history_read", args, false, owner)
	if err != nil || !strings.Contains(got.Output, "FINAL_FAILURE_SENTINEL") {
		t.Fatalf("blob not read: %v %s", err, got.Output)
	}
	if _, err := a.executeToolWithSessionDetailedContext(context.Background(), "context_history_read", args, false, other); err == nil {
		t.Fatal("reader crossed caller session")
	}
	if _, err := a.tools.Call("context_history_read", map[string]any{"action": "list"}); err == nil {
		t.Fatal("unbound low-level call was allowed")
	}
}

func TestLongtaskDurableProjectionPreservesFailureAndToolPair(t *testing.T) {
	a, _ := contextTestAgent(t)
	const failure = "FINAL_TEST_FAILURE"
	input := []provider.Message{{Role: "system", Content: "Rules"}, {Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "run-test", Name: "terminal", Arguments: `{"command":"go test ./..."}`}}}, {Role: "tool", ToolCallID: "run-test", Name: "terminal", Content: strings.Repeat("progress ", 2000) + failure}}
	got, err := a.durableContext(input, "Fix code and pass tests", nil)
	if err != nil {
		t.Fatal(err)
	}
	text := messagesToTestText(got)
	if !strings.Contains(text, failure) || !strings.Contains(text, "context_history_read") {
		t.Fatal("final status or original-result reference lost")
	}
	calls, results := 0, 0
	for _, m := range got {
		calls += len(m.ToolCalls)
		if m.Role == "tool" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatal("tool round split")
	}
}

func TestLongtaskFreshToolEvidenceWinsOverRetrievedEvidence(t *testing.T) {
	a, _ := contextTestAgent(t)
	const failure = "LATEST_EXECUTION_FAILED"
	input := []provider.Message{
		{Role: "system", Content: "[Project Context]\n" + strings.Repeat("project rule ", 600)},
		{Role: "system", Content: "[Core Memory]\n" + strings.Repeat("prior memory ", 900)},
		{Role: "system", Content: "## Retrieved Knowledge\n" + strings.Repeat("old document ", 900)},
		{Role: "user", Content: "CURRENT_REQUEST: fix and test this project"},
		{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "fresh", Name: "terminal", Arguments: `{"command":"go test ./..."}`}}},
		{Role: "tool", ToolCallID: "fresh", Name: "terminal", Content: strings.Repeat("progress ", 3000) + failure},
	}
	got := a.fitContextWindow(input)
	text := messagesToTestText(got)
	if !strings.Contains(text, failure) || !strings.Contains(text, "CURRENT_REQUEST") {
		t.Fatal("fresh execution evidence or current request lost to old retrieval")
	}
	if err := a.ensureContextFits(got); err != nil {
		t.Fatal(err)
	}
	calls, results := 0, 0
	for _, m := range got {
		calls += len(m.ToolCalls)
		if m.Role == "tool" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatal("fresh tool exchange was split")
	}
	got = a.projectToolEvidence(got)
	if n := strings.Count(messagesToTestText(got), "[Full result:"); n != 1 {
		t.Fatalf("full-result reference duplicated or lost: %d", n)
	}
}

func TestLongtaskImpossiblePinnedInputStopsBeforeProvider(t *testing.T) {
	a, _ := contextTestAgent(t)
	started := make(chan string, 1)
	snapshot := providerSnapshot{provider: &snapshotTestProvider{name: "must-not-call", started: started}}
	input := []provider.Message{{Role: "user", Content: strings.Repeat("unavoidably large current request ", 3000)}}
	fitted := a.fitContextWindow(input)
	if len(fitted) != 1 {
		t.Fatal("current request silently dropped")
	}
	if _, err := a.chatLoopIteration(context.Background(), fitted, provider.CallOptions{}, false, snapshot); err == nil || !strings.Contains(err.Error(), "context input budget") {
		t.Fatalf("sync call did not reject impossible input: %v", err)
	}
	if _, err := a.streamLoopIteration(context.Background(), fitted, provider.CallOptions{}, false, snapshot); err == nil || !strings.Contains(err.Error(), "context input budget") {
		t.Fatalf("stream call did not reject impossible input: %v", err)
	}
	if len(started) != 0 {
		t.Fatal("provider called with impossible input")
	}
}

func TestLongtaskPlanCannotSkipForegroundVerification(t *testing.T) {
	a, sess, _, _, cfg := durableHarness(t, durableCheckpoint{})
	cfg.Foreground = true
	steps, reviews := 0, 0
	a.tools.Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { steps++; return "Actual operation completed and tested", nil }})
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			reviews++
			if steps == 0 {
				return &provider.Response{Content: `{"status":"continue","reason":"Only a plan was returned; execute and verify the change","evidence":["zero recorded operations"]}`}, nil
			}
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		if reviews == 0 {
			return &provider.Response{Content: "I will edit src/main.go and run go test next."}, nil
		}
		if steps == 0 {
			return &provider.Response{ToolCalls: []provider.ToolCall{{ID: "actual-work", Name: "perform_operation", Arguments: "{}"}}}, nil
		}
		return &provider.Response{Content: "The change and verification are complete."}, nil
	}}
	result, err := a.RunLoopWithSession(context.Background(), sess, "Edit src/main.go and run go test", cfg)
	if err != nil || !result.Verified || steps != 1 || reviews != 2 {
		t.Fatalf("plan treated as completion: %+v %v steps=%d reviews=%d", result, err, steps, reviews)
	}
}

func TestLongtaskHundredOperationsAcrossSlices(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	cfg.MaxIterations = 12
	executed, summaries := 0, 0
	sawState := false
	a.tools.Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(args map[string]any) (string, error) {
		if int(args["step"].(float64)) != executed {
			t.Fatalf("operation replayed or skipped: %+v executed=%d", args, executed)
		}
		executed++
		return fmt.Sprintf("Operation %d succeeded", executed), nil
	}})
	a.provider = &foregroundProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		for _, msg := range messages {
			if strings.HasPrefix(msg.Content, "[Task State]") {
				var state map[string]any
				start := strings.Index(msg.Content, "{")
				if start < 0 || json.Unmarshal([]byte(msg.Content[start:]), &state) != nil {
					t.Fatal("invalid task state")
				}
				if int(state["successful_operations"].(float64)) != executed {
					t.Fatal("task state disagrees with durable operations")
				}
				sawState = true
			}
		}
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		if strings.HasPrefix(messages[0].Content, "Summarize task progress") {
			summaries++
			return &provider.Response{Content: fmt.Sprintf("Current user goal: complete 100 operations. Completed work: %d persisted operations with evidence. Pending work: finish remaining operations, never replay completed effects. Evidence is in the task operation ledger.", executed)}, nil
		}
		if executed < 100 {
			return &provider.Response{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("operation-%d", executed), Name: "perform_operation", Arguments: fmt.Sprintf(`{"step":%d}`, executed)}}}, nil
		}
		return &provider.Response{Content: "All 100 operations are complete."}, nil
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := a.RunLoopWithSession(ctx, sess, "执行100个操作并验证，不能重复已完成的副作用", cfg)
	if err != nil || !result.Verified || executed != 100 || summaries < 1 || !sawState {
		t.Fatalf("long run failed: %+v %v executed=%d summaries=%d", result, err, executed, summaries)
	}
	q, err := a.openForeground(sess)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range q.ListAll() {
		if !task.Verified || task.Result != "All 100 operations are complete." {
			t.Fatalf("verified completion was not persisted: %+v", task)
		}
	}
}
