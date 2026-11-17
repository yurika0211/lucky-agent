package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

// representativeLongSession builds one coding session whose tool dumps dominate
// the next prompt. Paths, the user goal, and a failing command stay verbatim so
// a later compact must keep them.
func representativeLongSession(sess *session.Session) {
	const goal = "GOAL keep docs/compact-rebuildability.md and finish the compact ratio fix"
	const failing = "go test ./agent -run TestCompactRatio -count=1"
	for turn := 0; turn < 18; turn++ {
		sess.AddProviderMessage(provider.Message{
			Role:    "user",
			Content: fmt.Sprintf("turn %d: %s. Inspect agent/compact_session.go and keep %s.", turn, goal, failing),
		})
		var calls []provider.ToolCall
		for toolN := 0; toolN < 3; toolN++ {
			calls = append(calls, provider.ToolCall{
				ID:        fmt.Sprintf("call-%d-%d", turn, toolN),
				Name:      "file_read",
				Arguments: fmt.Sprintf(`{"path":"agent/compact_session.go","turn":%d}`, turn),
			})
		}
		sess.AddProviderMessage(provider.Message{
			Role:      "assistant",
			Content:   "I will read the compact path and keep the failing command.",
			ToolCalls: calls,
		})
		for toolN := 0; toolN < 3; toolN++ {
			body := strings.Repeat(fmt.Sprintf("agent/compact_session.go line evidence turn=%d tool=%d %s\n", turn, toolN, failing), 80)
			sess.AddProviderMessage(provider.Message{
				Role:       "tool",
				Name:       "file_read",
				ToolCallID: fmt.Sprintf("call-%d-%d", turn, toolN),
				Content:    body,
			})
		}
	}
}

func TestCompactRatioSampleMeasuresRetainedTail(t *testing.T) {
	a, sess := contextTestAgent(t)
	representativeLongSession(sess)
	beforeMessages := sess.GetMessages()
	before := estimateProviderMessages(a.contextEst, beforeMessages)
	cfg := a.autoCompactConfig()
	targetTail := int(float64(a.contextInputLimit()) * cfg.AutoCompactTargetRatio)
	targetTail -= cfg.AutoCompactReservedSummaryTokens
	result, err := a.CompactSessionWithOptions(context.Background(), sess, "sample", CompactSessionOptions{
		ForceLocal:        true,
		RetainRecentTurns: cfg.AutoCompactRetainTurns,
		TargetTailTokens:  targetTail,
	})
	if err != nil {
		t.Fatal(err)
	}
	afterAll := estimateProviderMessages(a.contextEst, sess.GetMessages())
	_, raw, _ := session.CompactSegments(sess.GetMessages())
	tail := estimateProviderMessages(a.contextEst, raw)
	projected := a.BuildContextPackerSnapshot(context.Background(), sess, TextUserTurnInput("continue the compact ratio fix"))
	t.Logf("SAMPLE before=%d compact_input_pre=%d summary_post=%d stored_after=%d raw_tail=%d retained_messages=%d dropped=%d projected=%d ratio_vs_before=%.4f tail_ratio=%.4f summary=%q",
		before, result.PreTokenEstimate, result.PostTokenEstimate, afterAll, tail, result.RetainedMessages, result.DroppedMessages, projected.TotalTokens,
		float64(result.PostTokenEstimate)/float64(before), float64(tail)/float64(before), truncate(result.Summary, 180))
	if !strings.Contains(result.Summary, "docs/compact-rebuildability.md") || !strings.Contains(result.Summary, "go test ./agent -run TestCompactRatio -count=1") {
		t.Fatalf("mustkeep missing from summary: %s", truncate(result.Summary, 400))
	}

	// One more heavy turn after the boundary. Auto-compact will not fire again
	// until the cooldown, so this tail is what the next model call still sees.
	for turn := 18; turn < 24; turn++ {
		sess.AddProviderMessage(provider.Message{
			Role:    "user",
			Content: fmt.Sprintf("turn %d follow-up: keep docs/compact-rebuildability.md and %s", turn, "go test ./agent -run TestCompactRatio -count=1"),
		})
		sess.AddProviderMessage(provider.Message{
			Role:    "assistant",
			Content: "Reading the next dump.",
			ToolCalls: []provider.ToolCall{{
				ID: fmt.Sprintf("post-%d", turn), Name: "file_read",
				Arguments: `{"path":"agent/compact_session.go"}`,
			}},
		})
		sess.AddProviderMessage(provider.Message{
			Role: "tool", Name: "file_read", ToolCallID: fmt.Sprintf("post-%d", turn),
			Content: strings.Repeat("post-compact dump agent/compact_session.go go test ./agent -run TestCompactRatio -count=1\n", 400),
		})
	}
	postTail := estimateProviderMessages(a.contextEst, func() []provider.Message {
		_, raw, _ := session.CompactSegments(sess.GetMessages())
		return raw
	}())
	postProjected := a.BuildContextPackerSnapshot(context.Background(), sess, TextUserTurnInput("continue without dropping the goal"))
	t.Logf("POST_TURN raw_tail=%d projected=%d input_limit=%d should_compact=%v",
		postTail, postProjected.TotalTokens, a.contextInputLimit(),
		a.shouldAutoCompactSession(sess, "continue without dropping the goal", false))
}

func TestLiveToolResultUsesBoundedProjection(t *testing.T) {
	const goal = "docs/compact-rebuildability.md"
	const failing = "go test ./agent -run TestCompactRatio -count=1"
	raw := strings.Repeat("noise line without an identifier\n", 2000) + goal + "\n" + failing + "\n"
	projected := buildContextToolResult("file_read", raw, nil, nil)
	if !strings.Contains(projected, goal) || !strings.Contains(projected, failing) {
		t.Fatalf("live projection dropped mustkeep evidence: %s", truncate(projected, 300))
	}
	if len(projected) >= len(raw)/2 {
		t.Fatalf("live projection did not shrink: raw=%d projected=%d", len(raw), len(projected))
	}
	msg := attachFullToolResultRef(provider.Message{Role: "tool", Content: projected}, executedToolCall{
		ToolCall: provider.ToolCall{ID: "call-live"},
		Result:   raw,
	})
	if !strings.Contains(msg.Content, "context_history_read action=result tool_call_id=call-live") {
		t.Fatalf("missing readback ref: %s", truncate(msg.Content, 200))
	}
	est := contextx.NewTokenEstimator(8000)
	before := est.Estimate(raw)
	after := est.Estimate(projected)
	t.Logf("LIVE_TURN before_tokens=%d after_tokens=%d ratio=%.4f", before, after, float64(after)/float64(before))
	// Measured 2026-11-17 on this fixture: before=16520 after=1975 ratio=0.1196.
	// The session still stores the full body. The readback ref is on the prompt copy.
	if after >= before/5 {
		t.Fatalf("live turn still carries most of the dump: before=%d after=%d", before, after)
	}
}
