package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	BoundaryID          string
	Trigger             string
	Summary             string
	FromMessage         int
	ToMessage           int
	ContentHash         string
	PolicyVersion       string
	PreTokenEstimate    int
	PostTokenEstimate   int
	SummaryTokens       int
	DroppedMessages     int
	RetainedMessages    int
	RestoredAttachments int
	SummarySource       string
	DryRun              bool
	Backup              *session.BackupInfo
}

const compactSegmentPolicyVersion = "checkpoint-segments-v1"

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
	sum := sha256.New()
	for _, message := range messages[from:to] {
		sum.Write([]byte(message.Role))
		sum.Write([]byte{0})
		sum.Write([]byte(message.Content))
		sum.Write([]byte{0})
		sum.Write([]byte(message.ToolCallID))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

type CompactSessionOptions struct {
	ForceLocal        bool
	DryRun            bool
	RetainRecentTurns int
	TargetTailTokens  int
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
	if len(compactInput) == 0 {
		return nil, fmt.Errorf("compact session: retained tail leaves no complete messages to compact")
	}
	preTokens := estimateProviderMessages(est, compactInput)
	emitCompactProgress(opts, CompactProgress{
		Phase:            "start",
		Trigger:          trigger,
		Message:          fmt.Sprintf("Compressing conversation context… (%d messages, ~%d tokens)", len(compactInput), preTokens),
		PreTokenEstimate: preTokens,
		DroppedMessages:  len(compactInput),
		RetainedMessages: len(retained),
	})
	summarySource := "llm"
	var summary string
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
		summary, summarySource, err = a.generateCompactSummaryWithProvider(ctx, compactInput, turnProvider, opts)
		if err != nil {
			emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error(), PreTokenEstimate: preTokens})
			return nil, err
		}
		if strings.TrimSpace(summarySource) == "" {
			summarySource = "llm"
		}
	}
	if validation := validateCompactSummary(summary, compactInput); !validation.Valid {
		err := fmt.Errorf("compact session: invalid summary: %s", validation.Reason)
		emitCompactProgress(opts, CompactProgress{Phase: "failed", Trigger: trigger, Message: err.Error(), Error: err.Error()})
		return nil, err
	}
	postTokens := est.Estimate(summary)
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
		PreTokenEstimate:    preTokens,
		PostTokenEstimate:   postTokens,
		SummaryTokens:       postTokens,
		DroppedMessages:     len(compactInput),
		RetainedMessages:    len(retained),
		RestoredAttachments: len(attachments),
		SummarySource:       summarySource,
		Attachments:         attachments,
	}
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
		if err := sess.Save(); err != nil {
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
		PreTokenEstimate:    preTokens,
		PostTokenEstimate:   postTokens,
		SummaryTokens:       postTokens,
		DroppedMessages:     len(compactInput),
		RetainedMessages:    len(retained),
		RestoredAttachments: len(attachments),
		SummarySource:       summarySource,
		DryRun:              opts.DryRun,
		Backup:              backup,
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
	return a.generateMapReduceCompactSummary(ctx, messages, turnProvider, opts)
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
