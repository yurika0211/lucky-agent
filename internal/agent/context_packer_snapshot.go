package agent

import (
	"context"

	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
)

// ContextPackerSnapshot captures the messages and token distribution produced
// by the current context planner for a single user turn.
type ContextPackerSnapshot struct {
	Messages     []provider.Message `json:"messages"`
	TotalTokens  int                `json:"total_tokens"`
	BucketTokens map[string]int     `json:"bucket_tokens"`
	BucketCounts map[string]int     `json:"bucket_counts"`
}

// BuildContextPackerSnapshot builds context with the same planner used by the
// agent loop, without calling the model. It is intended for benchmarks and
// diagnostics.
func (a *Agent) BuildContextPackerSnapshot(ctx context.Context, sess *session.Session, input UserTurnInput) ContextPackerSnapshot {
	planner := newContextPlanner(a, defaultContextBuildOptions())
	messages := planner.BuildInput(ctx, sess, input)
	report := planner.buildContextReport(messages)

	bucketTokens := make(map[string]int, len(report.bucketTokens))
	for k, v := range report.bucketTokens {
		bucketTokens[k] = v
	}
	bucketCounts := make(map[string]int, len(report.bucketCounts))
	for k, v := range report.bucketCounts {
		bucketCounts[k] = v
	}

	return ContextPackerSnapshot{
		Messages:     messages,
		TotalTokens:  report.totalTokens,
		BucketTokens: bucketTokens,
		BucketCounts: bucketCounts,
	}
}
