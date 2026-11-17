package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestFitStableHistoryTailKeepsLongMessageWhenBudgetFits(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(128000)}
	tail := "date 2026-04-17 amount 18420.65 LUM path /synthetic/ledger/kx4471/batch-19.ndjson"
	body := strings.Repeat("ledger note ", 200) + tail
	if len(body) <= 800 {
		t.Fatalf("fixture is not over 800 bytes: %d", len(body))
	}
	original := provider.Message{Role: "user", Content: body, ContentParts: []provider.ContentPart{{Type: "text", Text: "structured"}}}
	messages := []provider.Message{original}
	got := planner.fitStableHistoryTail(messages, 37500)
	if len(got) != 1 {
		t.Fatalf("messages = %d", len(got))
	}
	if !strings.Contains(got[0].Content, "2026-04-17") || !strings.Contains(got[0].Content, "18420.65") || !strings.Contains(got[0].Content, "/synthetic/ledger/kx4471/batch-19.ndjson") {
		t.Fatalf("budget-fit history dropped the tail: %s", got[0].Content[len(got[0].Content)-180:])
	}
	if messages[0].Content != body {
		t.Fatal("source message was mutated")
	}
	if len(got[0].ContentParts) != 1 || got[0].ContentParts[0].Text != "structured" {
		t.Fatalf("structured parts dropped: %+v", got[0].ContentParts)
	}
}

func TestFitStableHistoryTailDropsWholeTurnsBeforeCuttingTail(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	longTail := strings.Repeat("old filler ", 400) + "KEEP-TAIL-FACT 2026-09-03 6077.40 KIN"
	messages := []provider.Message{
		{Role: "user", Content: strings.Repeat("older turn ", 500)},
		{Role: "assistant", Content: "older answer"},
		{Role: "user", Content: longTail},
		{Role: "assistant", Content: "latest answer"},
	}
	budget := estimateProviderMessages(planner.est, messages[2:])
	got := planner.fitStableHistoryTail(messages, budget)
	text := messagesToTestText(got)
	if strings.Contains(text, "older turn") {
		t.Fatal("older turn kept after budget drop")
	}
	if !strings.Contains(text, "KEEP-TAIL-FACT") || !strings.Contains(text, "2026-09-03") || !strings.Contains(text, "6077.40") {
		t.Fatalf("latest turn tail lost: %s", text)
	}
}

func TestFitStableHistoryTailKeepsToolPair(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	messages := []provider.Message{
		{Role: "user", Content: strings.Repeat("drop this older turn ", 300)},
		{Role: "assistant", Content: "calling", ToolCalls: []provider.ToolCall{{ID: "call-1", Name: "file_read"}}},
		{Role: "tool", Name: "file_read", ToolCallID: "call-1", Content: "path /tmp/pair.txt"},
	}
	projectedTail := []provider.Message{
		messages[1],
		planner.projectHistoryMessage(messages[2]),
	}
	budget := estimateProviderMessages(planner.est, projectedTail)
	got := planner.fitStableHistoryTail(messages, budget)
	if len(got) < 2 || len(got[0].ToolCalls) == 0 || got[len(got)-1].Role != "tool" {
		t.Fatalf("tool pair broken: %+v", got)
	}
	if got[len(got)-1].ToolCallID != "call-1" {
		t.Fatalf("tool result detached: %+v", got[len(got)-1])
	}
}

func TestManualCompactNoBenefitWritesNothing(t *testing.T) {
	a, sess, prov := compactBenefitHarness(t, "tiny")
	before := len(sess.GetMessages())
	_, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{}, a.baseProviderSnapshot())
	if !errors.Is(err, ErrCompactNoBenefit) || !strings.Contains(err.Error(), "no-benefit: skipped") {
		t.Fatalf("error = %v", err)
	}
	if len(sess.GetMessages()) != before {
		t.Fatalf("messages changed: %d -> %d", before, len(sess.GetMessages()))
	}
	if _, ok := sess.LatestCompactTrace(); ok {
		t.Fatal("no-benefit wrote a boundary")
	}
	if prov.calls.Load() == 0 {
		t.Fatal("summary provider was not called")
	}
}

func TestManualCompactDryRunNoBenefitWritesNothing(t *testing.T) {
	a, sess, _ := compactBenefitHarness(t, "tiny")
	before := len(sess.GetMessages())
	_, err := a.compactSessionWithProvider(context.Background(), sess, "manual-api", CompactSessionOptions{DryRun: true}, a.baseProviderSnapshot())
	if !errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("dry-run error = %v", err)
	}
	if len(sess.GetMessages()) != before {
		t.Fatal("dry-run wrote messages")
	}
}

func TestAutoCompactNoBenefitDoesNotForceLocal(t *testing.T) {
	// Verifies the auto path: a real summary that does not shrink the frozen
	// candidate returns ErrCompactNoBenefit, writes nothing, does not
	// ForceLocal, and cools down. The window is the diagnosed 16000/512 pair.
	// Target 0.009 is legal under threshold 0.01, so auto does not fall back
	// to 0.50. Retain stays 6. The oldest turn is about 122 tokens and the
	// token tail is about 138, so the source range is the older turns and the
	// last complete turn stays. The summary is longer than that prefix.
	a, sess, prov := compactBenefitHarness(t, "tiny")
	loaded := a.cfg.Get()
	loaded.Context.AutoCompact = true
	loaded.Context.AutoCompactMinMessages = 1
	loaded.Context.AutoCompactThreshold = 0.01
	loaded.Context.AutoCompactTargetRatio = 0.009
	loaded.Context.AutoCompactCooldownTurns = 1
	loaded.Context.AutoCompactReservedSummaryTokens = 1
	loaded.Context.AutoCompactRetainTurns = 6
	loaded.ContextBudget.ReservedRatio = 512.0 / 16000.0
	a.cfg.SetConfig(loaded)
	a.contextWin = contextx.NewContextWindow(contextx.WindowConfig{MaxTokens: 16000, ReservedTokens: 512})
	a.contextEst = contextx.NewTokenEstimator(16000)
	sess = session.NewSession("compact-benefit-auto", t.TempDir())
	for i := 0; i < 7; i++ {
		sess.AddMessage("user", fmt.Sprintf("turn %d ledger note %s", i, strings.Repeat("turn-note ", 30)))
		sess.AddMessage("assistant", fmt.Sprintf("turn %d ack", i))
	}
	prov.summary = strings.Repeat(compactSemanticSummary()+"\n", 8)
	_, err := a.compactSessionWithProvider(context.Background(), sess, "auto", CompactSessionOptions{
		RetainRecentTurns: a.autoCompactConfig().AutoCompactRetainTurns,
		TargetTailTokens:  int(float64(a.contextInputLimit())*a.autoCompactConfig().AutoCompactTargetRatio) - a.autoCompactConfig().AutoCompactReservedSummaryTokens,
		Query:             "continue the ledger",
	}, a.baseProviderSnapshot())
	if !errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("auto candidate error = %v, want ErrCompactNoBenefit", err)
	}
	a.maybeAutoCompactSessionWithProvider(context.Background(), sess, "continue the ledger", false, a.baseProviderSnapshot(), nil)
	if _, ok := sess.LatestCompactTrace(); ok {
		t.Fatal("auto no-benefit wrote a boundary")
	}
	if a.autoCompactFailureCount(sess.ID) != 0 {
		t.Fatalf("no-benefit counted as a failure: %d", a.autoCompactFailureCount(sess.ID))
	}
	if !a.autoCompactNoBenefitCooling(sess.ID) {
		t.Fatal("no-benefit cooldown was not recorded")
	}
	if prov.calls.Load() == 0 {
		t.Fatal("auto did not attempt a summary")
	}
	calls := prov.calls.Load()
	a.maybeAutoCompactSessionWithProvider(context.Background(), sess, "continue the ledger", false, a.baseProviderSnapshot(), nil)
	if prov.calls.Load() != calls {
		t.Fatalf("no-benefit retried inside cooldown: %d -> %d", calls, prov.calls.Load())
	}
	if _, ok := sess.LatestCompactTrace(); ok {
		t.Fatal("cooldown retry wrote a boundary")
	}
}

func compactSessionAPIMapping(result *CompactSessionResult) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	return map[string]any{
		"boundary_id":          result.BoundaryID,
		"trigger":              result.Trigger,
		"summary":              result.Summary,
		"from_message":         result.FromMessage,
		"to_message":           result.ToMessage,
		"content_hash":         result.ContentHash,
		"policy_version":       result.PolicyVersion,
		"pre_token_estimate":   result.PreTokenEstimate,
		"post_token_estimate":  result.PostTokenEstimate,
		"summary_tokens":       result.SummaryTokens,
		"dropped_messages":     result.DroppedMessages,
		"retained_messages":    result.RetainedMessages,
		"restored_attachments": result.RestoredAttachments,
		"summary_source":       result.SummarySource,
		"dry_run":              result.DryRun,
		"display":              CompactDisplay(result),
	}
}

func TestCompactAPIMappingUsesFullRequestEstimate(t *testing.T) {
	a, sess, prov := compactBenefitHarness(t, strings.Repeat("unique ledger fact ", 800))
	prov.summary = compactSemanticSummary()
	var progress []CompactProgress
	result, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{
		OnProgress: func(p CompactProgress) { progress = append(progress, p) },
	}, a.baseProviderSnapshot())
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	mapped := compactSessionAPIMapping(result)
	pre, _ := mapped["pre_token_estimate"].(int)
	post, _ := mapped["post_token_estimate"].(int)
	summaryTokens, _ := mapped["summary_tokens"].(int)
	if pre == 0 || post == 0 || post >= pre {
		t.Fatalf("API pre/post = %d/%d", pre, post)
	}
	if summaryTokens == 0 || summaryTokens == post {
		t.Fatalf("API summary_tokens = %d, post = %d", summaryTokens, post)
	}
	display, _ := mapped["display"].(map[string]string)
	if !strings.Contains(display["message"], "full request estimate") || !strings.Contains(display["message"], fmt.Sprintf("%d → %d", pre, post)) {
		t.Fatalf("display = %#v", display)
	}
	if strings.Contains(display["message"], "provider usage") {
		t.Fatalf("display claims provider usage: %s", display["message"])
	}
	trace, ok := sess.LatestCompactTrace()
	if !ok || trace.PreTokenEstimate != pre || trace.PostTokenEstimate != post || trace.SummaryTokens != summaryTokens || trace.PolicyVersion != "checkpoint-segments-v2" {
		t.Fatalf("trace = %+v", trace)
	}
	var sawStart, sawDone bool
	for _, p := range progress {
		switch p.Phase {
		case "start":
			sawStart = true
			if !strings.Contains(p.Message, "summary-input tokens") {
				t.Fatalf("start message = %s", p.Message)
			}
			if p.PreTokenEstimate == pre {
				t.Fatalf("start reused the full-request pre %d", pre)
			}
		case "done":
			sawDone = true
			if p.PreTokenEstimate != pre || p.PostTokenEstimate != post || !strings.Contains(p.Message, "full request estimate") {
				t.Fatalf("done progress = %+v", p)
			}
		}
	}
	if !sawStart || !sawDone {
		t.Fatalf("progress phases missing: %+v", progress)
	}

	drySess := session.NewSession("compact-benefit-dry", t.TempDir())
	drySess.AddMessage("user", strings.Repeat("unique ledger fact ", 800))
	drySess.AddMessage("assistant", "ACK")
	dry, err := a.compactSessionWithProvider(context.Background(), drySess, "manual", CompactSessionOptions{DryRun: true}, a.baseProviderSnapshot())
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	dryDisplay := CompactDisplay(dry)
	if !strings.Contains(dryDisplay["message"], "full request estimate") || dry.PreTokenEstimate == 0 || dry.PostTokenEstimate == 0 {
		t.Fatalf("dry-run display = %#v result=%+v", dryDisplay, dry)
	}
}

func TestCompactBenefitWritesUsageWhenSmaller(t *testing.T) {
	a, sess, prov := compactBenefitHarness(t, strings.Repeat("unique ledger fact ", 800))
	prov.usage = &provider.UsageDetails{PromptTokens: 1200, CompletionTokens: 80, TotalTokens: 1280, CachedPromptTokens: 100}
	result, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{}, a.baseProviderSnapshot())
	if err != nil {
		if errors.Is(err, ErrCompactNoBenefit) {
			t.Skipf("candidate request did not shrink under this estimator: %v", err)
		}
		t.Fatalf("compact: %v", err)
	}
	trace, ok := sess.LatestCompactTrace()
	if !ok {
		t.Fatal("missing trace")
	}
	if trace.SummaryCalls < 1 || trace.SummaryUsage == nil || trace.SummaryUsageRecords < 1 || !trace.SummaryUsageComplete {
		t.Fatalf("usage observation: calls=%d records=%d complete=%v usage=%+v", trace.SummaryCalls, trace.SummaryUsageRecords, trace.SummaryUsageComplete, trace.SummaryUsage)
	}
	if trace.SummaryUsage.InputTokens != 1200 || trace.SummaryUsage.OutputTokens != 80 {
		t.Fatalf("usage = %+v", trace.SummaryUsage)
	}
	if trace.SummaryDurationMS <= 0 {
		t.Fatalf("duration = %d", trace.SummaryDurationMS)
	}
	if result.PostTokenEstimate >= result.PreTokenEstimate || result.PreTokenEstimate == 0 {
		t.Fatalf("full request estimate before=%d after=%d", result.PreTokenEstimate, result.PostTokenEstimate)
	}
	if result.SummaryTokens == 0 || result.SummaryTokens == result.PostTokenEstimate {
		t.Fatalf("summary tokens = %d, post = %d; summary body must stay separate from the full request", result.SummaryTokens, result.PostTokenEstimate)
	}
	if trace.PreTokenEstimate != result.PreTokenEstimate || trace.PostTokenEstimate != result.PostTokenEstimate || trace.SummaryTokens != result.SummaryTokens {
		t.Fatalf("trace estimate %+v result %+v", trace, result)
	}
	if trace.PolicyVersion != "checkpoint-segments-v2" {
		t.Fatalf("policy = %s", trace.PolicyVersion)
	}
	if result.Backup == nil {
		t.Fatal("beneficial compact skipped backup")
	}
}

func TestSecondCompactKeepsPriorBoundaryAndAbsoluteRange(t *testing.T) {
	a, sess, prov := compactBenefitHarness(t, strings.Repeat("first range fact ", 400))
	prov.summary = compactSemanticSummary()
	first, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{RetainRecentTurns: 0}, a.baseProviderSnapshot())
	if err != nil {
		t.Fatalf("first compact: %v", err)
	}
	if first.FromMessage != 0 || first.ToMessage <= first.FromMessage {
		t.Fatalf("first range = [%d,%d)", first.FromMessage, first.ToMessage)
	}
	sess.AddMessage("user", strings.Repeat("second range fact /synthetic/ledger/nl2208/slip-44.ndjson ", 80))
	sess.AddMessage("assistant", "second ack")
	sess.AddMessage("user", "retained latest turn")
	sess.AddMessage("assistant", "retained ack")
	prov.summary = strings.ReplaceAll(compactSemanticSummary(), "/synthetic/ledger/kx4471/batch-19.ndjson", "/synthetic/ledger/nl2208/slip-44.ndjson")
	second, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{RetainRecentTurns: 1}, a.baseProviderSnapshot())
	if errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("second compact skipped: %v", err)
	}
	if err != nil {
		t.Fatalf("second compact: %v", err)
	}
	if second.FromMessage != first.ToMessage {
		t.Fatalf("second range [%d,%d) does not continue after %d", second.FromMessage, second.ToMessage, first.ToMessage)
	}
	if second.RetainedMessages < 1 {
		t.Fatalf("retained tail dropped: %+v", second)
	}
	segments, raw, covered := session.CompactSegments(sess.GetMessages())
	if len(segments) != 2 {
		t.Fatalf("segments = %d", len(segments))
	}
	if segments[0].ID != first.BoundaryID || segments[1].ID != second.BoundaryID {
		t.Fatalf("boundary order = %s then %s", segments[0].ID, segments[1].ID)
	}
	if covered != second.ToMessage {
		t.Fatalf("covered %d != second end %d", covered, second.ToMessage)
	}
	if len(raw) != second.RetainedMessages {
		t.Fatalf("raw tail %d != retained %d", len(raw), second.RetainedMessages)
	}
	text := messagesToTestText(segmentsToPlannerHistory(t, a, sess.GetMessages()))
	if strings.Count(text, "[Compact Summary]") < 2 {
		t.Fatalf("planner dropped an existing segment:\n%s", text)
	}
	if strings.Count(text, first.BoundaryID) != 1 || strings.Count(text, second.BoundaryID) != 1 {
		t.Fatalf("segment ids repeated or missing:\n%s", text)
	}
}

func TestCompareCompactBenefitIncludesExistingSegments(t *testing.T) {
	a, sess, _ := compactBenefitHarness(t, "current raw tail")
	prior := session.CompactMetadata{
		ID:            "prior-segment",
		Trigger:       "manual",
		Summary:       strings.Repeat("PRIOR-SEGMENT-FACT ", 80),
		FromMessage:   0,
		ToMessage:     2,
		PolicyVersion: compactSegmentPolicyVersion,
		CreatedAt:     time.Now(),
		SummarySource: "llm",
		Attachments:   []session.CompactAttachment{{Kind: "file_state", Source: "ledger", Content: "/synthetic/ledger/prior.ndjson"}},
	}
	sess.AddCompactBoundary(prior)
	sess.AddMessage("user", strings.Repeat("new raw body ", 200)+" /synthetic/ledger/new.ndjson")
	sess.AddMessage("assistant", "new ack")
	all := sess.GetMessages()
	_, raw, covered := session.CompactSegments(all)
	if covered != 2 || len(raw) != 2 {
		t.Fatalf("covered=%d raw=%d", covered, len(raw))
	}
	meta := session.CompactMetadata{
		ID:            "next-segment",
		Trigger:       "manual",
		Summary:       "short next",
		FromMessage:   covered,
		ToMessage:     covered + len(raw),
		PolicyVersion: compactSegmentPolicyVersion,
		CreatedAt:     time.Now(),
		SummarySource: "llm",
	}
	before, after, _, _, beforeTokens, afterTokens, err := a.compareCompactBenefit(context.Background(), sess, all, raw, raw, nil, meta, compactBenefitBaseline{Manual: true}, a.baseProviderSnapshot())
	if err != nil && !errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("compare: %v", err)
	}
	beforeText := messagesToTestText(before)
	afterText := messagesToTestText(after)
	if !strings.Contains(beforeText, "PRIOR-SEGMENT-FACT") || !strings.Contains(afterText, "PRIOR-SEGMENT-FACT") {
		t.Fatalf("existing segment missing\nbefore=%s\nafter=%s", beforeText, afterText)
	}
	if !strings.Contains(afterText, "/synthetic/ledger/prior.ndjson") {
		t.Fatal("existing attachment missing from after")
	}
	if strings.Count(afterText, "prior-segment") != 1 {
		t.Fatalf("prior segment wrapped again:\n%s", afterText)
	}
	if beforeTokens == 0 || afterTokens == 0 {
		t.Fatalf("tokens before=%d after=%d", beforeTokens, afterTokens)
	}
}

func TestRetainedTailBeforeOlderBoundaryMatchesPersistedProjection(t *testing.T) {
	// First compact keeps one turn. New raw is then appended after the
	// boundary, so the next logical tail is the retained turn plus the new
	// raw. Those messages are not a contiguous slice. The after candidate
	// must match the planner projection of a real AddCompactBoundary append.
	a, sess, prov := compactBenefitHarness(t, strings.Repeat("same body ", 80))
	prov.summary = compactSemanticSummary()
	sess.AddMessage("user", strings.Repeat("same body ", 80))
	sess.AddMessage("assistant", "same ack")
	sess.AddMessage("user", "kept turn "+strings.Repeat("same body ", 40))
	sess.AddMessage("assistant", "kept ack")
	first, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{RetainRecentTurns: 1}, a.baseProviderSnapshot())
	if err != nil {
		t.Fatalf("first compact: %v", err)
	}
	if first.RetainedMessages < 1 {
		t.Fatalf("first compact retained nothing: %+v", first)
	}
	sess.AddMessage("user", strings.Repeat("new raw /synthetic/ledger/nl2208/slip-44.ndjson ", 80))
	sess.AddMessage("assistant", "new ack")
	beforeWrite := append([]provider.Message(nil), sess.GetMessages()...)
	_, raw, covered := session.CompactSegments(beforeWrite)
	if covered != first.ToMessage || len(raw) < 4 {
		t.Fatalf("logical tail covered=%d raw=%d", covered, len(raw))
	}
	compactInput := raw[:len(raw)-2]
	retained := raw[len(raw)-2:]
	meta := session.CompactMetadata{
		ID:            "second-appended",
		Trigger:       "manual",
		Summary:       strings.ReplaceAll(compactSemanticSummary(), "/synthetic/ledger/kx4471/batch-19.ndjson", "/synthetic/ledger/nl2208/slip-44.ndjson"),
		FromMessage:   covered,
		ToMessage:     covered + len(compactInput),
		PolicyVersion: compactSegmentPolicyVersion,
		CreatedAt:     time.Now(),
		SummarySource: "llm",
	}
	_, after, _, _, _, _, err := a.compareCompactBenefit(context.Background(), sess, beforeWrite, raw, compactInput, retained, meta, compactBenefitBaseline{Manual: true}, a.baseProviderSnapshot())
	if err != nil && !errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("compare: %v", err)
	}
	persisted := append([]provider.Message(nil), beforeWrite...)
	persisted = append(persisted, session.CompactBoundaryMessage(meta))
	want := segmentsToPlannerHistory(t, a, persisted)
	got := segmentsToPlannerHistory(t, a, transcriptWithAppendedBoundary(t, beforeWrite, raw, compactInput, retained, meta))
	if messagesToTestText(got) != messagesToTestText(want) {
		t.Fatalf("candidate projection differs from persisted append\nwant:\n%s\ngot:\n%s", messagesToTestText(want), messagesToTestText(got))
	}
	afterText := messagesToTestText(after)
	if afterText == "" || !strings.Contains(afterText, "/synthetic/ledger/nl2208/slip-44.ndjson") {
		t.Fatalf("after candidate lost the new raw:\n%s", afterText)
	}
	if strings.Count(messagesToTestText(after), first.BoundaryID) != 1 {
		t.Fatalf("first boundary was wrapped or dropped:\n%s", messagesToTestText(after))
	}
}

func transcriptWithAppendedBoundary(t *testing.T, all, raw, compactInput, retained []provider.Message, meta session.CompactMetadata) []provider.Message {
	t.Helper()
	out, err := transcriptAfterProposedCompact(all, raw, compactInput, retained, meta)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func segmentsToPlannerHistory(t *testing.T, a *Agent, messages []provider.Message) []provider.Message {
	t.Helper()
	planner := newContextPlannerWithProvider(a, contextBuildOptions{IncludeHistory: true, ReadOnly: true}, a.baseProviderSnapshot())
	return projectCompactCandidateHistory(planner, messages)
}

func TestSemanticSampleDateAndRevokeAreNotProvenBySubstringGate(t *testing.T) {
	source := []provider.Message{{
		Role: "user",
		Content: strings.Join([]string{
			"batch KX-4471-MOTH",
			"path /synthetic/ledger/kx4471/batch-19.ndjson",
			"date 2026-04-17",
			"amount 18420.65 LUM",
			"old carrier revoked",
			"current carrier is IVY-2208",
		}, "\n"),
	}}
	required := requiredCompactIdentifiers(source)
	for _, fact := range []string{"2026-04-17", "18420.65", "old carrier revoked", "IVY-2208"} {
		for _, id := range required {
			if strings.Contains(id, fact) {
				t.Fatalf("identifier gate unexpectedly requires %q via %q", fact, id)
			}
		}
	}
	joined := strings.Join(required, "\n")
	if !strings.Contains(joined, "/synthetic/ledger/kx4471/batch-19.ndjson") {
		t.Fatalf("path dropped from required identifiers: %v", required)
	}
	summary := compactSemanticSummary()
	summary = strings.ReplaceAll(summary, "2026-04-17", "2026-01-01")
	summary = strings.ReplaceAll(summary, "old carrier revoked", "old carrier is current")
	if !validateCompactSummary(summary, source).Valid {
		t.Fatal("changing the date and the revoke sentence failed the substring gate; this sample can no longer show the residual semantic gap")
	}
}

func TestCompactSummaryKeepsSixFieldSampleWhenProviderReturnsIt(t *testing.T) {
	body := strings.Join([]string{
		"field-1 batch KX-4471-MOTH",
		"field-2 path /synthetic/ledger/kx4471/batch-19.ndjson",
		"field-3 date 2026-04-17",
		"field-4 amount 18420.65 LUM",
		"field-5 old carrier revoked",
		"field-6 current carrier IVY-2208",
	}, "\n")
	padded := strings.Repeat("context line before the facts.\n", 40) + body
	a, sess, prov := compactBenefitHarness(t, padded)
	prov.summary = compactSemanticSummary()
	result, err := a.compactSessionWithProvider(context.Background(), sess, "manual", CompactSessionOptions{}, a.baseProviderSnapshot())
	if errors.Is(err, ErrCompactNoBenefit) {
		t.Fatalf("six-field sample was skipped as no-benefit: %v", err)
	}
	if err != nil {
		t.Fatalf("compact rejected a summary that kept the path: %v", err)
	}
	if !strings.Contains(result.Summary, "/synthetic/ledger/kx4471/batch-19.ndjson") {
		t.Fatalf("stored summary lost the path: %s", result.Summary)
	}
	if !strings.Contains(result.Summary, "2026-04-17") || !strings.Contains(result.Summary, "18420.65") || !strings.Contains(result.Summary, "old carrier revoked") || !strings.Contains(result.Summary, "IVY-2208") {
		t.Fatalf("semantic sample was not stored as returned: %s", result.Summary)
	}
}

func compactSemanticSummary() string {
	return strings.Join([]string{
		"Current user goal:",
		"Keep the ledger facts for batch KX-4471-MOTH.",
		"Completed work:",
		"Read /synthetic/ledger/kx4471/batch-19.ndjson. date 2026-04-17. amount 18420.65 LUM. old carrier revoked. current carrier IVY-2208.",
		"Pending work:",
		"Do not treat the revoked carrier as current.",
		"Key files and functions:",
		"/synthetic/ledger/kx4471/batch-19.ndjson",
		"Commands and test results:",
		"No command was run.",
		"User constraints:",
		"Keep field-1 through field-6.",
		"Uncertain facts:",
		"None.",
	}, "\n")
}

type compactBenefitProvider struct {
	countingCompactProvider
	summary string
	usage   *provider.UsageDetails
}

func (p *compactBenefitProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	resp, err := p.countingCompactProvider.Chat(ctx, messages)
	if err != nil || resp == nil {
		return resp, err
	}
	if p.summary != "" {
		resp.Content = p.summary
	}
	if p.usage != nil {
		resp.Usage = p.usage
		resp.Model = "example-model"
	}
	return resp, nil
}

func compactBenefitHarness(t *testing.T, userBody string) (*Agent, *session.Session, *compactBenefitProvider) {
	t.Helper()
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	prov := &compactBenefitProvider{countingCompactProvider: countingCompactProvider{name: "compact-benefit"}}
	window := contextx.NewContextWindow(contextx.WindowConfig{MaxTokens: 128000, ReservedTokens: 2048})
	a := &Agent{
		cfg:        cfg,
		tools:      tool.NewRegistry(),
		contextWin: window,
		contextEst: contextx.NewTokenEstimator(128000),
		provider:   prov,
	}
	sess := session.NewSession("compact-benefit", t.TempDir())
	sess.AddMessage("user", userBody)
	sess.AddMessage("assistant", "ACK")
	return a, sess, prov
}
