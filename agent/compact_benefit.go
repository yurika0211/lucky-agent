package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

// ErrCompactNoBenefit is returned when a valid summary does not shrink the
// reconstructed candidate request. Manual callers surface it on the existing
// error channel. The text includes "no-benefit: skipped" so API 400 and CLI
// failure output can name the reason. That 400 is compatibility with the
// existing endpoint, not a model failure. Automatic compact recognizes this
// sentinel and skips without counting a failure or writing a local boundary.
var ErrCompactNoBenefit = errors.New("compact session: no-benefit: skipped")

// compactBenefitBaseline is the next-turn context the before/after comparison
// must share. Manual compact has no future user text, so Query stays empty.
// Auto keeps the routing text of the turn that triggered compaction.
type compactBenefitBaseline struct {
	Query          string
	Manual         bool
	DisabledTools  []string
	Loop           LoopConfig
	RetainTurns    int
	TargetTail     int
	HasAttachments bool
}

func compactNoBenefitError(before, after int) error {
	return fmt.Errorf("%w; candidate request %d tokens is not smaller than %d", ErrCompactNoBenefit, after, before)
}

// localCompactFits reports whether a local candidate is smaller than the
// source it replaces. A larger candidate must not become a boundary.
func localCompactFits(before, after int) bool {
	return after > 0 && after < before
}

// compactSummaryObservation aggregates host Chat callbacks for one compact.
// Map/reduce may call Chat concurrently. Missing usage stays nil; a zero
// TokenUsage is recorded only when a callback actually carried usage.
type compactSummaryObservation struct {
	mu       sync.Mutex
	calls    int
	records  int
	usage    *provider.TokenUsage
	started  time.Time
	duration time.Duration
	timed    bool
}

func (o *compactSummaryObservation) start() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.started.IsZero() {
		o.started = time.Now()
		o.timed = true
	}
}

func (o *compactSummaryObservation) finish() {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.timed || o.started.IsZero() || o.duration > 0 {
		return
	}
	o.duration = time.Since(o.started)
}

func (o *compactSummaryObservation) noteCall(resp *provider.Response) {
	o.add(resp)
}

func (o *compactSummaryObservation) add(resp *provider.Response) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls++
	if resp == nil || !responseHasVisibleUsage(resp) {
		return
	}
	if o.usage == nil {
		o.usage = &provider.TokenUsage{}
	}
	o.usage.AddResponse(resp)
	o.records++
}

func responseHasVisibleUsage(resp *provider.Response) bool {
	if resp == nil {
		return false
	}
	if resp.Usage != nil {
		return true
	}
	return resp.TokensUsed > 0
}

func (o *compactSummaryObservation) apply(meta *session.CompactMetadata, segments int) {
	if o == nil || meta == nil {
		return
	}
	o.finish()
	o.mu.Lock()
	defer o.mu.Unlock()
	meta.SummaryCalls = o.calls
	if o.records > 0 && o.usage != nil {
		copied := *o.usage
		meta.SummaryUsage = &copied
		meta.SummaryUsageRecords = o.records
		meta.SummaryUsageComplete = o.calls > 0 && o.records == o.calls
	}
	if o.timed && o.duration > 0 {
		meta.SummaryDurationMS = o.duration.Milliseconds()
		if meta.SummaryDurationMS == 0 {
			meta.SummaryDurationMS = 1
		}
	}
	if segments > 0 {
		meta.SummarySegments = segments
	}
}

func (o *compactSummaryObservation) snapshot() (calls, records int, usage *provider.TokenUsage, durationMS int64) {
	if o == nil {
		return 0, 0, nil, 0
	}
	o.finish()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.records > 0 && o.usage != nil {
		copied := *o.usage
		usage = &copied
	}
	if o.timed && o.duration > 0 {
		durationMS = o.duration.Milliseconds()
		if durationMS == 0 {
			durationMS = 1
		}
	}
	return o.calls, o.records, usage, durationMS
}

func compactSegmentCount(messages []provider.Message) int {
	segments, _, _ := session.CompactSegments(messages)
	if len(segments) == 0 {
		return 1
	}
	return len(segments) + 1
}

// compareCompactBenefit rebuilds the candidate request before and after the
// proposed boundary. Non-history context is frozen once. The after transcript
// is the full history plus one appended boundary, matching AddCompactBoundary.
// CompactSegments then covers the raw range. Existing boundaries stay.
//
// Snapshot boundary: this includes system, project, memory, RAG, skill hint,
// history, the current-turn query, tool schemas, and prepareModelRequest.
// It does not include turn attachments. Compact comparison has no new
// attachment bytes to attach, and re-analyzing old ones would call the
// media processor. ReadOnly skips memory hygiene and the context cache.
func (a *Agent) compareCompactBenefit(ctx context.Context, sess *session.Session, all, raw []provider.Message, compactInput, retained []provider.Message, meta session.CompactMetadata, baseline compactBenefitBaseline, turnProvider providerSnapshot) (beforeMessages, afterMessages []provider.Message, beforeTools, afterTools []map[string]any, beforeTokens, afterTokens int, err error) {
	est := a.contextEst
	if est == nil {
		est = contextx.NewTokenEstimator(4096)
	}
	planner := newContextPlannerWithProvider(a, contextBuildOptions{
		IncludeHistory:  true,
		IncludeRAG:      true,
		ReadOnly:        true,
		OmitUserMessage: strings.TrimSpace(baseline.Query) == "",
		DisabledTools:   baseline.DisabledTools,
	}, turnProvider)
	if planner.est == nil {
		planner.est = est
	}

	beforeTranscript := append([]provider.Message(nil), all...)
	afterTranscript, err := transcriptAfterProposedCompact(all, raw, compactInput, retained, meta)
	if err != nil {
		return nil, nil, nil, nil, 0, 0, err
	}
	beforeHistory := projectCompactCandidateHistory(planner, beforeTranscript)
	afterHistory := projectCompactCandidateHistory(planner, afterTranscript)
	stable, evidence, skillHint := planner.frozenNonHistoryContext(ctx, sess, baseline.Query, TurnScope{})

	beforeMessages = assembleFrozenCandidate(stable, beforeHistory, evidence, skillHint, baseline.Query)
	afterMessages = assembleFrozenCandidate(stable, afterHistory, evidence, skillHint, baseline.Query)
	beforeMessages = plannerFitCandidate(planner, beforeMessages, "")
	afterMessages = plannerFitCandidate(planner, afterMessages, "")

	opts := provider.CallOptions{}
	if a != nil && a.tools != nil {
		opts = a.buildLoopCallOptions(baseline.Query, baseline.Loop)
	}
	snap := turnProvider
	if snap.budget == nil && a != nil {
		snap.budget = a.requestBudget()
	}
	if snap.budget != nil && snap.budget.est == nil {
		snap.budget.est = est
	}
	fittedBefore, optsBefore, fitErr := a.prepareModelRequest(beforeMessages, opts, snap)
	if fitErr != nil {
		return fittedBefore, nil, optsBefore.Tools, nil, 0, 0, fmt.Errorf("compact session: before-candidate fit: %w", fitErr)
	}
	fittedAfter, optsAfter, fitErrAfter := a.prepareModelRequest(afterMessages, cloneCallOptions(opts), snap)
	if fitErrAfter != nil {
		return fittedBefore, fittedAfter, optsBefore.Tools, optsAfter.Tools, 0, 0, fmt.Errorf("compact session: after-candidate fit: %w", fitErrAfter)
	}
	beforeMessages, afterMessages = fittedBefore, fittedAfter
	beforeTools, afterTools = optsBefore.Tools, optsAfter.Tools
	beforeTokens = estimateProviderRequest(est, beforeMessages, beforeTools)
	afterTokens = estimateProviderRequest(est, afterMessages, afterTools)
	if afterTokens >= beforeTokens {
		return beforeMessages, afterMessages, beforeTools, afterTools, beforeTokens, afterTokens, compactNoBenefitError(beforeTokens, afterTokens)
	}
	return beforeMessages, afterMessages, beforeTools, afterTools, beforeTokens, afterTokens, nil
}

func assembleFrozenCandidate(stable, history, evidence []provider.Message, skillHint provider.Message, query string) []provider.Message {
	out := make([]provider.Message, 0, len(stable)+len(history)+len(evidence)+3)
	out = append(out, stable...)
	out = append(out, history...)
	out = append(out, evidence...)
	if strings.TrimSpace(skillHint.Content) != "" {
		out = append(out, skillHint)
	}
	if strings.TrimSpace(query) != "" {
		out = append(out, provider.Message{Role: "user", Content: query})
	}
	return out
}

func cloneCallOptions(opts provider.CallOptions) provider.CallOptions {
	if len(opts.Tools) == 0 {
		return opts
	}
	opts.Tools = append([]map[string]any(nil), opts.Tools...)
	return opts
}

func plannerFitCandidate(planner *contextPlanner, messages []provider.Message, query string) []provider.Message {
	if planner == nil || planner.agent == nil {
		return messages
	}
	provisional := append([]provider.Message(nil), messages...)
	if strings.TrimSpace(query) != "" {
		provisional = append(provisional, provider.Message{Role: "user", Content: query})
	}
	fitted := planner.agent.fitContextWindow(provisional)
	if strings.TrimSpace(query) == "" {
		return fitted
	}
	if n := len(fitted); n > 0 {
		last := fitted[n-1]
		if last.Role == "user" && strings.TrimSpace(last.Content) == query {
			return fitted[:n-1]
		}
	}
	return fitted
}

// projectCompactCandidateHistory renders history with the same planner path
// used by a later turn. The messages are a copy; the live session is not written.
func projectCompactCandidateHistory(planner *contextPlanner, messages []provider.Message) []provider.Message {
	if planner == nil {
		return append([]provider.Message(nil), messages...)
	}
	summaries, raw := planner.compactBoundaryContext(messages)
	summaries = planner.projectCompactSummaries(summaries)
	rawBudget := planner.budget.History - estimateProviderMessages(planner.est, summaries)
	if rawBudget < 128 {
		rawBudget = 128
	}
	out := append([]provider.Message(nil), summaries...)
	return append(out, planner.fitStableHistoryTail(raw, rawBudget)...)
}

// transcriptAfterProposedCompact copies the full transcript and appends one
// boundary. That is the same write as Session.AddCompactBoundary: raw messages
// stay where they are, including a retained tail that physically sits before
// an older boundary. CompactSegments reads the absolute [From, To) range.
// The helper does not search message text or delete the covered prefix.
func transcriptAfterProposedCompact(all, raw, compactInput, retained []provider.Message, meta session.CompactMetadata) ([]provider.Message, error) {
	if len(compactInput) == 0 {
		return nil, fmt.Errorf("compact session: empty covered range")
	}
	if len(compactInput)+len(retained) != len(raw) {
		return nil, fmt.Errorf("compact session: covered range %d plus retained %d does not match raw %d", len(compactInput), len(retained), len(raw))
	}
	_, _, covered := session.CompactSegments(all)
	if meta.FromMessage != covered || meta.ToMessage != covered+len(compactInput) {
		return nil, fmt.Errorf("compact session: boundary range [%d,%d) does not match covered %d plus %d messages", meta.FromMessage, meta.ToMessage, covered, len(compactInput))
	}
	out := make([]provider.Message, len(all), len(all)+1)
	copy(out, all)
	out = append(out, session.CompactBoundaryMessage(meta))
	return out, nil
}

func logCompactNoBenefit(trigger, sessionID string, before, after int, dryRun bool, calls, records int) {
	logger.Info("compact skipped: no-benefit",
		"session_id", sessionID,
		"trigger", trigger,
		"dry_run", dryRun,
		"before_tokens", before,
		"after_tokens", after,
		"summary_calls", calls,
		"summary_usage_records", records,
	)
}

// noteCompactNoBenefit records a short cooldown so the next turns do not
// repeat the provider call. It does not increment the ordinary failure
// counter, so it cannot by itself reach ForceLocal or the hard stop.
// The existing failure counter still stops automatic compact after real
// failures. Emergency overflow bypasses this cooldown in shouldAutoCompact.
func (a *Agent) noteCompactNoBenefit(sessionID, trigger string) {
	if a == nil || !strings.Contains(trigger, "auto") {
		return
	}
	a.autoCompactMu.Lock()
	if a.autoCompactNoBenefitUntil == nil {
		a.autoCompactNoBenefitUntil = make(map[string]time.Time)
	}
	turns := 8
	if a.cfg != nil {
		if n := a.cfg.Get().Context.AutoCompactCooldownTurns; n > 0 {
			turns = n
		}
	}
	a.autoCompactNoBenefitUntil[sessionID] = time.Now().Add(time.Duration(turns) * time.Minute)
	a.autoCompactMu.Unlock()
	logger.Info("auto compact no-benefit cooldown",
		"session_id", sessionID,
		"trigger", trigger,
		"cooldown_turns", turns,
	)
}

func (a *Agent) autoCompactNoBenefitCooling(sessionID string) bool {
	if a == nil {
		return false
	}
	a.autoCompactMu.Lock()
	defer a.autoCompactMu.Unlock()
	if a.autoCompactNoBenefitUntil == nil {
		return false
	}
	until, ok := a.autoCompactNoBenefitUntil[sessionID]
	if !ok {
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	delete(a.autoCompactNoBenefitUntil, sessionID)
	return false
}

