package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
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
	t.Logf("SAMPLE before=%d full_request_pre=%d full_request_post=%d summary_tokens=%d stored_after=%d raw_tail=%d retained_messages=%d dropped=%d projected=%d ratio_vs_before=%.4f tail_ratio=%.4f summary=%q",
		before, result.PreTokenEstimate, result.PostTokenEstimate, result.SummaryTokens, afterAll, tail, result.RetainedMessages, result.DroppedMessages, projected.TotalTokens,
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
	a, sess := contextTestAgent(t)
	a.tools.Register(&tool.Tool{
		Name: "file_read", Enabled: true, Permission: tool.PermAuto,
		Handler: func(map[string]any) (string, error) { return raw, nil },
	})
	resp := &provider.Response{ToolCalls: []provider.ToolCall{{
		ID: "call-live", Name: "file_read", Arguments: `{"path":"docs/compact-rebuildability.md"}`,
	}}}
	cfg := DefaultLoopConfig()
	cfg.AutoApprove = true

	without, finalized, _ := a.processToolCallBatch(resp, cfg, &LoopResult{}, nil, nil, newLoopRuntimeState(), nil, "read the compact note")
	if finalized {
		t.Fatal("tool batch finalized")
	}
	prompt := toolMessageByID(t, without, "call-live")
	if strings.Contains(prompt.Content, "context_history_read") {
		t.Fatalf("nil session invented a readback ref: %s", truncate(prompt.Content, 240))
	}
	if !strings.Contains(prompt.Content, goal) || !strings.Contains(prompt.Content, failing) || !strings.Contains(prompt.Content, "noise line without an identifier") {
		t.Fatalf("nil session dropped standalone evidence: %s", truncate(prompt.Content, 240))
	}

	with, _, _ := a.processToolCallBatch(resp, cfg, &LoopResult{}, sess, nil, newLoopRuntimeState(), nil, "read the compact note")
	stored := toolMessageByID(t, with, "call-live")
	if !strings.Contains(stored.Content, "context_history_read action=result tool_call_id=call-live") {
		t.Fatalf("session prompt missing readback ref: %s", truncate(stored.Content, 240))
	}
	if !strings.Contains(stored.Content, goal) || !strings.Contains(stored.Content, failing) {
		t.Fatalf("session projection dropped mustkeep: %s", truncate(stored.Content, 240))
	}
	var recovered strings.Builder
	offset := 0
	for page := 0; page < 20; page++ {
		body, err := readContextHistory(sess, map[string]any{"action": "result", "tool_call_id": "call-live", "offset": offset, "limit": 6000})
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Content    string `json:"content"`
			NextOffset int    `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(body), &payload); err != nil {
			t.Fatal(err)
		}
		recovered.WriteString(payload.Content)
		if payload.NextOffset < 0 {
			break
		}
		offset = payload.NextOffset
	}
	if !strings.Contains(recovered.String(), goal) || !strings.Contains(recovered.String(), failing) || len(recovered.String()) < len(raw)/2 {
		t.Fatalf("stored body not readable: %s", truncate(recovered.String(), 240))
	}
	est := contextx.NewTokenEstimator(8000)
	before := est.Estimate(raw)
	after := est.Estimate(stored.Content)
	t.Logf("LIVE_TURN before_tokens=%d after_tokens=%d ratio=%.4f", before, after, float64(after)/float64(before))
	if after >= before/5 {
		t.Fatalf("live turn still carries most of the dump: before=%d after=%d", before, after)
	}
}

func TestFitDropsUnreadToolRefWithoutMutatingInput(t *testing.T) {
	a, _ := contextTestAgent(t)
	a.contextWin = contextx.NewContextWindow(contextx.WindowConfig{
		MaxTokens: 400, ReservedTokens: 80, Strategy: contextx.TrimLowPriority,
	})
	raw := strings.Repeat("noise line without an identifier\n", 400) + "docs/compact-rebuildability.md\n"
	original := []provider.Message{{
		Role: "tool", ToolCallID: "call-fit", Name: "file_read", Content: raw,
	}}
	before := original[0].Content
	fitted := a.fitContextWindow(original)
	cleaned := dropUnreadToolResultRefs(fitted)
	if strings.Contains(cleaned[0].Content, "context_history_read") {
		t.Fatalf("unread ref survived fit: %s", truncate(cleaned[0].Content, 200))
	}
	if original[0].Content != before {
		t.Fatal("fit or drop rewrote the caller slice")
	}
	if len(fitted) == 0 || fitted[0].Content == before {
		t.Fatal("fit did not shrink the oversized tool body")
	}
}

func TestOfflineFixtureRequestShrinksProjectedToolBody(t *testing.T) {
	est := contextx.NewTokenEstimator(8000)
	system := provider.Message{Role: "system", Content: "You are LuckyAgent. Keep docs/compact-rebuildability.md."}
	tail := provider.Message{Role: "user", Content: "continue the compact ratio fix"}
	schema := []map[string]any{{
		"type": "function",
		"function": map[string]any{"name": "file_read", "description": "Read one workspace file."},
	}}
	raw := strings.Repeat("noise line without an identifier\n", 2000) + "docs/compact-rebuildability.md\ngo test ./agent -run TestCompactRatio -count=1\n"
	projected := buildContextToolResult("file_read", raw, nil, nil)
	full := []provider.Message{system, {Role: "tool", ToolCallID: "call-live", Name: "file_read", Content: raw}, tail}
	shrunk := []provider.Message{system, {Role: "tool", ToolCallID: "call-live", Name: "file_read", Content: projected}, tail}
	before := estimateProviderRequest(est, full, schema)
	after := estimateProviderRequest(est, shrunk, schema)
	t.Logf("OFFLINE_FIXTURE before=%d after=%d ratio=%.4f system_and_schema_fixed=true", before, after, float64(after)/float64(before))
	if after >= before {
		t.Fatalf("fixture did not shrink: before=%d after=%d", before, after)
	}
}

func toolMessageByID(t *testing.T, messages []provider.Message, id string) provider.Message {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == "tool" && msg.ToolCallID == id {
			return msg
		}
	}
	t.Fatalf("tool message %s missing", id)
	return provider.Message{}
}
