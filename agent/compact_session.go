package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

type CompactSessionResult struct {
	BoundaryID    string
	Trigger       string
	Summary       string
	FromMessage   int
	ToMessage     int
	ContentHash   string
	PolicyVersion string
	// PreTokenEstimate and PostTokenEstimate are the same full candidate
	// request estimates written to CompactMetadata. They include the frozen
	// non-history snapshot, history, schema, and prepareModelRequest fit.
	// They are local estimates, not provider usage. SummaryTokens is only
	// the summary body. A recorded zero is a real estimate, not "missing".
	PreTokenEstimate    int
	PostTokenEstimate   int
	SummaryTokens       int
	DroppedMessages     int
	RetainedMessages    int
	RestoredAttachments int
	SummarySource       string
	DryRun              bool
	Backup              *session.BackupInfo
	SummaryCalls        int
	SummaryUsageRecords int
	SummaryUsage        *provider.TokenUsage
}

// compactSegmentPolicyVersion marks boundaries whose pre/post estimates are
// the full candidate request, not the covered-message and summary-body pair.
// Older sessions keep checkpoint-segments-v1 and must be read as that older
// pair. Readers do not rewrite those stored numbers.
const compactSegmentPolicyVersion = "checkpoint-segments-v2"

// compactRangeFingerprint identifies the messages a summary is about to cover.
// A count check misses a rewrite that keeps the same number of messages.
func compactRangeFingerprint(messages []provider.Message, from, to int) string {
	if from < 0 {
		from = 0
	}
	if to > len(messages) {
		to = len(messages)
	}
	if from > to {
		from = to
	}
	data, err := json.Marshal(messages[from:to])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type CompactSessionOptions struct {
	ForceLocal        bool
	DryRun            bool
	RetainRecentTurns int
	TargetTailTokens  int
	// Query is the next-turn text included in the before/after request.
	// Manual compact leaves it empty. Auto passes the routing text so the
	// comparison uses the same current-turn baseline as the live loop.
	Query string
	// Loop carries the turn's tool allowlist into the benefit comparison.
	// Manual compact leaves it empty and uses the default visible tool set.
	Loop LoopConfig
	// OnProgress reports compact phases to streaming clients (SSE/WebSocket).
	OnProgress func(CompactProgress)
}

// CompactSession summarizes raw session history since the latest compact
// boundary and appends a compact_boundary marker. The compaction call does not
// expose tools; it is a plain text-only provider request.
func (a *Agent) CompactSession(ctx context.Context, sess *session.Session, trigger string) (*CompactSessionResult, error) {
	return a.CompactSessionWithOptions(ctx, sess, trigger, CompactSessionOptions{})
}

func (a *Agent) CompactSessionWithOptions(ctx context.Context, sess *session.Session, trigger string, opts CompactSessionOptions) (*CompactSessionResult, error) {
	return a.compactSessionWithProvider(ctx, sess, trigger, opts, a.providerSnapshotForTurn(""))
}

func (a *Agent) compactSessionWithProvider(ctx context.Context, sess *session.Session, trigger string, opts CompactSessionOptions, turnProvider providerSnapshot) (*CompactSessionResult, error) {
	if sess == nil {
		return nil, fmt.Errorf("compact session: session is nil")
	}
	if !opts.ForceLocal && !turnProvider.valid() {
		return nil, fmt.Errorf("compact session: provider is not initialized")
	}
	if strings.TrimSpace(trigger) == "" {
		trigger = "manual"
	}
	// Shrink giant inline tool dumps before estimating or summarizing so the
	// compact request itself does not re-send multi-megabyte bodies.
	if err := sess.ExternalizeLargeMessages(); err != nil {
		logger.Warn("compact session: externalize large messages", "session_id", sess.ID, "error", err)
	}
	all := sess.GetMessages()
	if len(all) == 0 {
		return nil, fmt.Errorf("compact session: no messages to compact")
	}
	_, raw, covered := session.CompactSegments(all)
	if len(raw) == 0 {
		return nil, fmt.Errorf("compact session: nothing to compact after latest boundary")
	}

	est := a.contextEst
	if est == nil {
		est = contextx.NewTokenEstimator(4096)
	}
	compactInput, retained := selectCompactSegmentInput(raw, est, opts.RetainRecentTurns, opts.TargetTailTokens)
	coveredFingerprint := compactRangeFingerprint(raw, 0, len(compactInput))
	if coveredFingerprint == "" {
		return nil, fmt.Errorf("compact session: cannot fingerprint source messages")
	}
	if len(compactInput) == 0 {
		return nil, fmt.Errorf("compact session: retained tail leaves no complete messages to compact")
	}
	sourceTokens := estimateProviderMessages(est, compactInput)
	emitCompactProgress(opts, CompactProgress{
		Phase:            "start",
		Trigger:          trigger,
		Message:          fmt.Sprintf("Compressing conversation context… (%d messages, ~%d summary-input tokens)", len(compactInput), sourceTokens),
		PreTokenEstimate: sourceTokens,
		DroppedMessages:  len(compactInput),
		RetainedMessages: len(retained),
	})
	summarySource := "llm"
	var summary string
	var observed *compactSummaryObservation
	if opts.ForceLocal {
		summarySource = "local"
		summary = generateLocalCompactSummary(compactInput, est)
		if strings.TrimSpace(summary) == "" {
			err := fmt.Errorf("compact session: local summary is empty")
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error()})
			return nil, err
		}
	} else {
		var err error
		summary, summarySource, observed, err = a.generateCompactSummaryObserved(ctx, compactInput, turnProvider, opts)
		if err != nil {
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error(), PreTokenEstimate: sourceTokens})
			a.logCompactAttempt(sess.ID, trigger, "failed", observed, len(sessionSegments(all)))
			return nil, err
		}
		if strings.TrimSpace(summarySource) == "" {
			summarySource = "llm"
		}
	}
	if validation := validateCompactSummary(summary, compactInput); !validation.Valid {
		repaired, ok := repairCompactSummary(summary, compactInput)
		if !ok {
			err := fmt.Errorf("compact session: invalid summary: %s", validation.Reason)
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error()})
			a.logCompactAttempt(sess.ID, trigger, "invalid-summary", observed, len(sessionSegments(all)))
			return nil, err
		}
		summary = repaired
		if !strings.Contains(summarySource, "repaired") {
			summarySource = strings.TrimSpace(summarySource) + "+repaired"
		}
	}
	summaryTokens := est.Estimate(summary)
	attachments := buildPostCompactAttachments(sess, raw, est)
	boundaryID := fmt.Sprintf("compact-%d", time.Now().UnixNano())
	meta := session.CompactMetadata{
		ID:                  boundaryID,
		Trigger:             trigger,
		Summary:             summary,
		FromMessage:         covered,
		ToMessage:           covered + len(compactInput),
		PolicyVersion:       compactSegmentPolicyVersion,
		CreatedAt:           time.Now(),
		SummaryTokens:       summaryTokens,
		DroppedMessages:     len(compactInput),
		RetainedMessages:    len(retained),
		RestoredAttachments: len(attachments),
		SummarySource:       summarySource,
		Attachments:         attachments,
	}
	if observed != nil {
		observed.apply(&meta, compactSegmentCount(all))
	}
	baseline := compactBenefitBaseline{
		Query:         opts.Query,
		Manual:        !strings.Contains(trigger, "auto"),
		DisabledTools: opts.Loop.DisabledTools,
		Loop:          opts.Loop,
		RetainTurns:   opts.RetainRecentTurns,
		TargetTail:    opts.TargetTailTokens,
	}
	_, _, _, _, beforeTokens, afterTokens, benefitErr := a.compareCompactBenefit(ctx, sess, all, raw, compactInput, retained, meta, baseline, turnProvider)
	if errors.Is(benefitErr, ErrCompactNoBenefit) {
		calls, records, _, _ := observed.snapshot()
		logCompactNoBenefit(trigger, sess.ID, beforeTokens, afterTokens, opts.DryRun, calls, records)
		if strings.Contains(trigger, "auto") && !strings.Contains(trigger, "degraded") {
			a.noteCompactNoBenefit(sess.ID, trigger)
		}
		emitCompactProgress(opts, CompactProgress{
			Phase:   "skipped",
			Trigger: trigger,
			Message: fmt.Sprintf("full request estimate %d → %d tokens, not provider usage; %s", beforeTokens, afterTokens, benefitErr.Error()),
			Error:   benefitErr.Error(),
			// Same full-request estimate as a written boundary. This skip
			// writes no metadata, so the numbers exist only on this event.
			PreTokenEstimate:  beforeTokens,
			PostTokenEstimate: afterTokens,
			DroppedMessages:   len(compactInput),
			RetainedMessages:  len(retained),
			SummarySource:     summarySource,
		})
		return nil, benefitErr
	}
	if benefitErr != nil {
		emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: benefitErr.Error(), Error: benefitErr.Error()})
		return nil, benefitErr
	}
	meta.PreTokenEstimate = beforeTokens
	meta.PostTokenEstimate = afterTokens
	meta.ContentHash = fmt.Sprintf("%x", makeContextCacheKey(map[string]any{
		"from":        meta.FromMessage,
		"to":          meta.ToMessage,
		"summary":     meta.Summary,
		"attachments": meta.Attachments,
		"policy":      meta.PolicyVersion,
	}))
	// Backup only after a valid summary exists so timed-out LLM calls do not
	// copy multi-megabyte sessions for a boundary that will never be written.
	var backup *session.BackupInfo
	if !opts.DryRun {
		var err error
		backup, err = sess.CreateBackup(trigger)
		if err != nil {
			err = fmt.Errorf("compact session: create pre-compaction backup: %w", err)
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error()})
			return nil, err
		}
		// A summary may only replace messages that are still recoverable. Re-read
		// the session after the backup and refuse to write a boundary when the
		// covered range moved underneath this compaction, so a boundary never
		// replaces messages that now exist only as an earlier summary.
		if _, reread, coveredAgain := session.CompactSegments(sess.GetMessages()); coveredAgain != covered || len(reread) < len(raw) || compactRangeFingerprint(reread, 0, len(compactInput)) != coveredFingerprint {
			err := fmt.Errorf("compact session: raw messages changed during compaction; refusing to overwrite them with a summary")
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error()})
			return nil, err
		}
		sess.AddCompactBoundary(meta)
		if err := a.saveSession(sess); err != nil {
			err = fmt.Errorf("compact session: save boundary: %w", err)
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error()})
			return nil, err
		}
	}
	result := &CompactSessionResult{
		BoundaryID:          boundaryID,
		Trigger:             trigger,
		Summary:             summary,
		FromMessage:         meta.FromMessage,
		ToMessage:           meta.ToMessage,
		ContentHash:         meta.ContentHash,
		PolicyVersion:       meta.PolicyVersion,
		PreTokenEstimate:    beforeTokens,
		PostTokenEstimate:   afterTokens,
		SummaryTokens:       summaryTokens,
		DroppedMessages:     len(compactInput),
		RetainedMessages:    len(retained),
		RestoredAttachments: len(attachments),
		SummarySource:       summarySource,
		DryRun:              opts.DryRun,
		Backup:              backup,
		SummaryCalls:        meta.SummaryCalls,
		SummaryUsageRecords: meta.SummaryUsageRecords,
		SummaryUsage:        meta.SummaryUsage,
	}
	phase := "done"
	if strings.Contains(summarySource, "local") && strings.Contains(trigger, "degraded") {
		phase = "degraded"
	}
	emitCompactProgress(opts, compactProgressFromResult(phase, "", result))
	return result, nil
}

func selectCompactSegmentInput(raw []provider.Message, est *contextx.TokenEstimator, retainTurns, targetTailTokens int) ([]provider.Message, []provider.Message) {
	input, tail := compactlib.SelectInput(toCompactMessages(raw), compactEstimator(est), retainTurns, targetTailTokens)
	if len(raw) == 0 {
		return nil, nil
	}
	return append([]provider.Message(nil), raw[:len(input)]...), append([]provider.Message(nil), raw[len(raw)-len(tail):]...)
}

func (a *Agent) generateCompactSummary(ctx context.Context, messages []provider.Message) (string, string, error) {
	return a.generateCompactSummaryWithProvider(ctx, messages, a.providerSnapshotForTurn(""), CompactSessionOptions{})
}

// generateCompactSummaryWithProvider summarizes compactInput. Long ranges are
// sliced on user-turn boundaries, summarized concurrently, then merged.
// context.compact_model selects the summarizer model when set.
func (a *Agent) generateCompactSummaryWithProvider(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (string, string, error) {
	summary, source, _, err := a.generateCompactSummaryObserved(ctx, messages, turnProvider, opts)
	return summary, source, err
}

func (a *Agent) generateCompactSummaryObserved(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (string, string, *compactSummaryObservation, error) {
	observed := &compactSummaryObservation{}
	observed.start()
	summary, source, err := a.generateMapReduceCompactSummaryObserved(ctx, messages, turnProvider, opts, observed)
	observed.finish()
	return summary, source, observed, err
}

func sessionSegments(messages []provider.Message) []session.CompactMetadata {
	segments, _, _ := session.CompactSegments(messages)
	return segments
}

func (a *Agent) logCompactAttempt(sessionID, trigger, phase string, observed *compactSummaryObservation, segments int) {
	calls, records, durationMS := 0, 0, int64(0)
	var usage *provider.TokenUsage
	if observed != nil {
		calls, records, usage, durationMS = observed.snapshot()
	}
	logger.Info("compact attempt",
		"session_id", sessionID,
		"trigger", trigger,
		"phase", phase,
		"summary_calls", calls,
		"summary_usage_records", records,
		"summary_usage_present", usage != nil,
		"summary_duration_ms", durationMS,
		"summary_segments", segments,
	)
}

func estimateProviderMessages(est *contextx.TokenEstimator, messages []provider.Message) int {
	if est == nil {
		return 0
	}
	total := 0
	for _, message := range messages {
		total += compactlib.EstimateMessage(est, message.Content, message.ReasoningContent)
		if message.Name != "" {
			total += est.Estimate(message.Name) + 1
		}
		for _, call := range message.ToolCalls {
			total += est.Estimate(call.Name) + est.Estimate(call.Arguments) + 4
		}
	}
	return total
}
