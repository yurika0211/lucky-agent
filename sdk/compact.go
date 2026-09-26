package sdk

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
)

// CompactResult summarizes a manual session compaction.
type CompactResult struct {
	BoundaryID          string
	Trigger             string
	Summary             string
	FromMessage         int
	ToMessage           int
	PreTokenEstimate    int
	PostTokenEstimate   int
	SummaryTokens       int
	DroppedMessages     int
	RetainedMessages    int
	RestoredAttachments int
	SummarySource       string
	DryRun              bool
}

// CompactOptions tunes CompactSession. Zero values use runtime defaults.
type CompactOptions struct {
	// ForceLocal skips the provider summarizer and builds a local extractive summary.
	ForceLocal bool
	// DryRun computes the compact plan without mutating the session.
	DryRun bool
	// RetainRecentTurns keeps the newest N turns outside the compacted range when > 0.
	RetainRecentTurns int
	// TargetTailTokens hints how many recent tokens to keep when > 0.
	TargetTailTokens int
}

// CompactSession summarizes older turns in a session to free context window space.
func (a *Agent) CompactSession(ctx context.Context, sessionID, trigger string, opts *CompactOptions) (*CompactResult, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("sdk: empty session id")
	}
	sess, ok := a.inner.Sessions().Get(sessionID)
	if !ok || sess == nil {
		return nil, fmt.Errorf("sdk: session %q not found", sessionID)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	trigger = strings.TrimSpace(trigger)
	if trigger == "" {
		trigger = "embed-sdk"
	}

	innerOpts := agent.CompactSessionOptions{}
	if opts != nil {
		innerOpts = agent.CompactSessionOptions{
			ForceLocal:        opts.ForceLocal,
			DryRun:            opts.DryRun,
			RetainRecentTurns: opts.RetainRecentTurns,
			TargetTailTokens:  opts.TargetTailTokens,
		}
	}
	result, err := a.inner.CompactSessionWithOptions(ctx, sess, trigger, innerOpts)
	if err != nil {
		return nil, fmt.Errorf("sdk: compact session: %w", err)
	}
	return mapCompactResult(result), nil
}

func mapCompactResult(result *agent.CompactSessionResult) *CompactResult {
	if result == nil {
		return nil
	}
	return &CompactResult{
		BoundaryID:          result.BoundaryID,
		Trigger:             result.Trigger,
		Summary:             result.Summary,
		FromMessage:         result.FromMessage,
		ToMessage:           result.ToMessage,
		PreTokenEstimate:    result.PreTokenEstimate,
		PostTokenEstimate:   result.PostTokenEstimate,
		SummaryTokens:       result.SummaryTokens,
		DroppedMessages:     result.DroppedMessages,
		RetainedMessages:    result.RetainedMessages,
		RestoredAttachments: result.RestoredAttachments,
		SummarySource:       result.SummarySource,
		DryRun:              result.DryRun,
	}
}
