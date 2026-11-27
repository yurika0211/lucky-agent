package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

func TestPrecomputedCompactHitDoesNotCallModelAgain(t *testing.T) {
	agent := &Agent{}
	messages := []provider.Message{{Role: "user", Content: "read /tmp/notes/alpha.go:12"}}
	anchor := compactAnchor(messages)
	agent.compactPrecompute()
	agent.precompute.current["s"] = &precomputedCompact{
		anchor: anchor, model: "grok", summary: validCompactSummaryText(),
		evidence: compactEvidenceBlock(messages), source: "llm", tailLimit: 15000, ready: true, done: closedChan(),
	}
	summary, evidence, source, ok := agent.takePrecomputedCompact("s", anchor, "grok", 10, 15000)
	if !ok || !strings.Contains(source, "llm") || summary == "" || !strings.Contains(evidence, "/tmp/notes/alpha.go") {
		t.Fatalf("hit summary=%q evidence=%q source=%q ok=%v", summary, evidence, source, ok)
	}
}

func TestPrecomputedCompactMissFallsThrough(t *testing.T) {
	agent := &Agent{}
	_, _, _, ok := agent.takePrecomputedCompact("missing", "anchor", "grok", 1, 15000)
	if ok {
		t.Fatal("missing precompute was treated as a hit")
	}
}

func TestPrecomputeSkipsWhenMeasuredPromptIsOlderThanCache(t *testing.T) {
	agent := &Agent{}
	agent.recordMeasuredPromptUsage("s", &provider.TokenUsage{InputTokens: 10})
	agent.precompute.mu.Lock()
	agent.precompute.measuredAt["s"] = time.Now().Add(-6 * time.Minute)
	agent.precompute.mu.Unlock()
	agent.maybeStartCompactPrecompute(context.Background(), "s", "anchor", "grok", 100, 100, []provider.Message{{Role: "user", Content: "stay"}}, providerSnapshot{})
	if agent.precompute.current["s"] != nil {
		t.Fatal("stale measured prompt started a precompute")
	}
}

func TestPrecomputeMeasurementGatesStart(t *testing.T) {
	messages := []provider.Message{{Role: "user", Content: "stay"}}
	start := func(agent *Agent, id string) {
		agent.maybeStartCompactPrecompute(context.Background(), id, "anchor", "grok", 100, 100, messages, providerSnapshot{provider: &staticChatProvider{content: validCompactSummaryText()}, model: "grok"})
	}

	missing := &Agent{}
	start(missing, "missing")
	if missing.precompute != nil && missing.precompute.current["missing"] != nil {
		t.Fatal("missing measurement started a precompute")
	}

	stale := &Agent{}
	stale.recordMeasuredPromptUsage("stale", &provider.TokenUsage{InputTokens: 12})
	stale.precompute.mu.Lock()
	stale.precompute.measuredAt["stale"] = time.Now().Add(-6 * time.Minute)
	stale.precompute.mu.Unlock()
	start(stale, "stale")
	if stale.precompute.current["stale"] != nil {
		t.Fatal("expired measurement started a precompute")
	}

	fresh := &Agent{provider: &staticChatProvider{content: validCompactSummaryText()}, contextEst: contextx.NewTokenEstimator(4096)}
	fresh.recordMeasuredPromptUsage("fresh", &provider.TokenUsage{InputTokens: 20})
	start(fresh, "fresh")
	if fresh.precompute == nil || fresh.precompute.current["fresh"] == nil {
		t.Fatal("fresh measurement did not start a precompute")
	}
	fresh.waitPrecompute("fresh")
}

func TestPrecomputeMeasurementWithoutUsage(t *testing.T) {
	messages := []provider.Message{{Role: "user", Content: "stay"}}
	agent := &Agent{provider: &staticChatProvider{content: validCompactSummaryText()}, contextEst: contextx.NewTokenEstimator(4096)}
	agent.recordUsageAndTTFTForSession("s", nil, 0)
	agent.maybeStartCompactPrecompute(context.Background(), "s", "anchor", "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
	if agent.precompute == nil || agent.precompute.current["s"] == nil {
		t.Fatal("a finished round with nil usage was skipped as no measurement")
	}
	agent.waitPrecompute("s")
}

func TestPrecomputeMeasurementIsPerSession(t *testing.T) {
	messages := []provider.Message{{Role: "user", Content: "stay"}}
	agent := &Agent{provider: &staticChatProvider{content: validCompactSummaryText()}, contextEst: contextx.NewTokenEstimator(4096)}
	agent.recordUsageAndTTFTForSession("A", nil, 0)
	agent.setMeasuredSession("B")
	agent.recordUsageAndTTFT(nil, 0)
	agent.maybeStartCompactPrecompute(context.Background(), "B", "anchor", "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
	if agent.precompute != nil && agent.precompute.current["B"] != nil {
		t.Fatal("session B started from session A's measurement")
	}
	agent.maybeStartCompactPrecompute(context.Background(), "A", "anchor", "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
	if agent.precompute == nil || agent.precompute.current["A"] == nil {
		t.Fatal("session B replaced session A's measurement")
	}
	agent.waitPrecompute("A")
}

func TestLoopRuntimeRecordsMeasurementWithoutUsage(t *testing.T) {
	agent := &Agent{provider: &staticChatProvider{content: validCompactSummaryText()}, contextEst: contextx.NewTokenEstimator(4096)}
	runtime := newAgentLoopRuntime(agent)
	recordRuntimeUsage(runtime, "loop-session", nil, 0)
	messages := []provider.Message{{Role: "user", Content: "stay"}}
	agent.maybeStartCompactPrecompute(context.Background(), "loop-session", "anchor", "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
	if agent.precompute == nil || agent.precompute.current["loop-session"] == nil {
		t.Fatal("loop end with nil usage did not record this session")
	}
	agent.maybeStartCompactPrecompute(context.Background(), "other", "anchor", "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
	if agent.precompute.current["other"] != nil {
		t.Fatal("loop measurement was stored on another session")
	}
	agent.waitPrecompute("loop-session")
}

func TestPrecomputeStopsAfterTwoFailures(t *testing.T) {
	agent := &Agent{
		provider:   &staticChatProvider{name: "fail", err: context.DeadlineExceeded},
		contextEst: contextx.NewTokenEstimator(4096),
		precompute: &compactPrecomputeState{current: map[string]*precomputedCompact{}, failures: map[string]int{}, stopped: map[string]bool{}, generation: map[string]uint64{}, measuredAt: map[string]time.Time{}},
	}
	messages := []provider.Message{{Role: "user", Content: "again"}}
	for i := 0; i < 2; i++ {
		agent.maybeStartCompactPrecompute(context.Background(), "s", compactAnchor(messages)+string(rune('a'+i)), "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
		agent.waitPrecompute("s")
	}
	before := agent.precompute.generation["s"]
	agent.maybeStartCompactPrecompute(context.Background(), "s", "later", "grok", 100, 100, messages, providerSnapshot{provider: agent.provider, model: "grok"})
	if agent.precompute.current["s"] != nil || agent.precompute.generation["s"] != before {
		t.Fatal("precompute continued after two failures")
	}
}

func (a *Agent) waitPrecompute(sessionID string) {
	if a == nil || a.precompute == nil {
		return
	}
	a.precompute.mu.Lock()
	job := a.precompute.current[sessionID]
	a.precompute.mu.Unlock()
	if job != nil {
		<-job.done
	}
}

func TestCompactCutDoesNotLandOnToolResult(t *testing.T) {
	raw := []provider.Message{
		{Role: "user", Content: "inspect"},
		{Role: "assistant", Content: "reading", ToolCalls: []provider.ToolCall{{ID: "1", Name: "file_read"}}},
		{Role: "tool", Name: "file_read", Content: "File: /tmp/notes/alpha.go\nline 12"},
		{Role: "assistant", Content: "done"},
		{Role: "user", Content: "next"},
	}
	input, retained := selectCompactSegmentInput(raw, contextx.NewTokenEstimator(4096), 0, 20)
	if len(retained) == 0 || !compactCutIsExchange(retained) {
		t.Fatalf("cut retained=%#v input=%d", retained, len(input))
	}
	if retained[0].Role == "tool" {
		t.Fatal("cut landed on a tool result")
	}
}

func TestLocalCandidateNoBenefitSkipsCooldownAfterBenefitCheck(t *testing.T) {
	est := contextx.NewTokenEstimator(4096)
	sess := session.NewSession("local-benefit-cooldown", t.TempDir())
	sess.AddMessage("user", "note")
	sess.AddMessage("assistant", "ack")
	beforeMessages := sess.MessageCount()
	agent := &Agent{
		contextEst:          est,
		autoCompactFailures: map[string]int{},
		provider:            &staticChatProvider{name: "static", content: validCompactSummaryText()},
	}
	raw := sess.GetMessages()
	keep := agent.compactSettings().KeepTokens
	historyTokens := estimateProviderMessages(est, raw)
	if keep >= historyTokens && historyTokens > 1 {
		keep = historyTokens / 2
		if keep < 1 {
			keep = 1
		}
	}
	compactInput, retained := selectCompactSegmentInput(raw, est, 0, keep)
	if len(compactInput) == 0 {
		t.Fatal("fixture left nothing to compact")
	}
	summary := validCompactSummaryText() + "\n" + strings.Repeat("expanded local candidate ", 40)
	anchor := compactAnchor(compactInput)
	agent.compactPrecompute()
	agent.precompute.current[sess.ID] = &precomputedCompact{
		anchor:    anchor,
		model:     "grok",
		summary:   summary,
		evidence:  compactEvidenceBlock(compactInput),
		source:    "local",
		tailLimit: keep,
		ready:     true,
		done:      closedChan(),
	}
	failures := agent.autoCompactFailureCount(sess.ID)
	_, err := agent.compactSessionWithProvider(context.Background(), sess, "auto", CompactSessionOptions{}, providerSnapshot{provider: agent.provider, model: "grok"})
	if err == nil || !strings.Contains(err.Error(), "no-benefit") {
		t.Fatalf("local candidate after benefit check = %v", err)
	}
	if agent.autoCompactNoBenefitCooling(sess.ID) {
		t.Fatal("local candidate entered no-benefit cooldown after the benefit check")
	}
	if got := agent.autoCompactFailureCount(sess.ID); got != failures {
		t.Fatalf("failure count changed: %d -> %d", failures, got)
	}
	if _, ok := sess.LatestCompactTrace(); ok || sess.MessageCount() != beforeMessages {
		t.Fatalf("boundary written: messages %d -> %d", beforeMessages, sess.MessageCount())
	}
	if calls := agent.provider.(*staticChatProvider).calls; calls != 0 {
		t.Fatalf("benefit-check path called the model %d times", calls)
	}
	if estimateProviderMessages(est, retained) > keep {
		t.Fatalf("tail %d exceeds keep %d", estimateProviderMessages(est, retained), keep)
	}
}

func TestLocalExpansionDoesNotEnterCooldown(t *testing.T) {
	est := contextx.NewTokenEstimator(4096)
	messages := []provider.Message{{Role: "user", Content: "x"}}
	source := estimateProviderMessages(est, messages)
	summary := strings.Repeat("expanded local summary ", 40)
	if est.Estimate(summary) < source {
		t.Fatalf("fixture did not expand: source=%d summary=%d", source, est.Estimate(summary))
	}
	agent := &Agent{contextEst: est, autoCompactFailures: map[string]int{}}
	beforeCount := sessMessageCount(t)
	sess := beforeCount.sess
	sess.AddMessage("user", "x")
	failures := agent.autoCompactFailureCount(sess.ID)
	_, err := agent.compactSessionWithProvider(context.Background(), sess, "auto-degraded", CompactSessionOptions{ForceLocal: true}, providerSnapshot{})
	if err == nil || !strings.Contains(err.Error(), "no-benefit") {
		t.Fatalf("expanded local candidate error = %v", err)
	}
	if agent.autoCompactNoBenefitCooling(sess.ID) {
		t.Fatal("expanded local candidate entered no-benefit cooldown")
	}
	if got := agent.autoCompactFailureCount(sess.ID); got != failures {
		t.Fatalf("failure count changed: %d -> %d", failures, got)
	}
	if _, ok := sess.LatestCompactTrace(); ok || sess.MessageCount() != beforeCount.n+1 {
		t.Fatalf("boundary written: messages %d -> %d", beforeCount.n, sess.MessageCount())
	}
}

type countedSession struct {
	sess *session.Session
	n    int
}

func sessMessageCount(t *testing.T) countedSession {
	t.Helper()
	sess := session.NewSession("local-expand", t.TempDir())
	return countedSession{sess: sess, n: sess.MessageCount()}
}

func TestLocalExpansionIsRejected(t *testing.T) {
	if localCompactFits(100, 120) {
		t.Fatal("larger local summary was accepted")
	}
	if !localCompactFits(120, 40) {
		t.Fatal("smaller local summary was rejected")
	}
	err := compactNoBenefitError(56317, 58157)
	if err == nil || !strings.Contains(err.Error(), "no-benefit") {
		t.Fatalf("error = %v", err)
	}
}

func TestEvidenceBlockDoesNotRequireBodyToRepeatIdentifiers(t *testing.T) {
	messages := []provider.Message{{Role: "tool", Name: "file_read", Content: "File: /tmp/notes/alpha.go\nShowing from line 12, max 20 lines"}}
	evidence := compactEvidenceBlock(messages)
	summary := validCompactSummaryText()
	record := joinCompactRecord(summary, evidence)
	if missing := missingCompactIdentifiers(record, requiredCompactIdentifiers(messages)); len(missing) != 0 {
		t.Fatalf("evidence block still failed: %v", missing)
	}
	if missing := missingCompactIdentifiers(summary, requiredCompactIdentifiers(messages)); len(missing) == 0 {
		t.Fatal("summary without an evidence block was accepted")
	}
}

func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
