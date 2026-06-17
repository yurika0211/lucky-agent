package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/contextx"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/provider"
)

func TestBuildTypedMemoryBodyIncludesTypedSections(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	route := memory.RouteAnalysis{
		RequiredTools:     []string{"current_time", "web_search"},
		SuggestedSearches: []string{"Shanghai pollen forecast"},
		RiskFlags:         []string{"pollen_allergy"},
		Constraints:       []string{"Check air quality before final answer."},
		TemporalNotes:     []string{"Superseded memory ignored: old-ref."},
		SupersededRefs:    []string{"old-ref"},
		EvidenceRefs:      []string{"memory.md#block"},
	}
	entries := []memory.Entry{
		{Content: "User's daughter has active pollen allergy.", Category: "health", Tier: memory.TierLong},
	}

	body := planner.buildTypedMemoryBody(route, entries, 2048)
	for _, want := range []string{
		"[Memory Router]",
		"[Must Use Facts]",
		"[Required Tools]",
		"[Answer Constraints]",
		"[Temporal Warnings]",
		"[Evidence Refs]",
		"[Suggested web_search queries]",
		"current_time",
		"web_search",
		"Check air quality",
		"Temporal resolution:",
		"Superseded refs:",
		"User's daughter has active pollen allergy",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected typed memory body to contain %q, got:\n%s", want, body)
		}
	}
}

func TestSelectIntentAwareRecentHistoryDropsUnrelatedFiller(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	messages := []provider.Message{
		{Role: "user", Content: "recipe filler user chatter"},
		{Role: "assistant", Content: "recipe filler assistant chatter with no benchmark relevance"},
		{Role: "user", Content: "Context packer benchmark acceptance gates: CMR >= 0.95 and P95PackerMS <= 10."},
		{Role: "assistant", Content: "Keep Quality >= baseline and track context noise."},
		{Role: "tool", Name: "terminal", Content: "go test ./cmd/lh-context-packer-bench ./internal/agent"},
		{Role: "assistant", Content: "Next summarize prompt tokens and bucket tokens."},
	}

	terms := historyIntentTerms("context-packer-bench long history", messages)
	selected := planner.selectIntentAwareRecentHistory(messages, terms)
	text := strings.ToLower(messagesToTestText(selected))
	if strings.Contains(text, "recipe filler") {
		t.Fatalf("expected unrelated filler to be dropped, got:\n%s", text)
	}
	for _, want := range []string{"cmr", "quality", "go test", "bucket tokens"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected selected history to contain %q, got:\n%s", want, text)
		}
	}
}

func TestHistoryMessageRelevantHonorsExplicitIrrelevance(t *testing.T) {
	msg := provider.Message{
		Role:    "assistant",
		Content: "recipe filler assistant chatter with no benchmark relevance",
	}
	if historyMessageRelevant(msg, []string{"benchmark", "quality"}) {
		t.Fatalf("expected explicit irrelevant history to be dropped")
	}
}

func TestSelectIntentAwareRecentHistoryDropsExplicitlyIrrelevantTail(t *testing.T) {
	planner := &contextPlanner{est: contextx.NewTokenEstimator(4096)}
	messages := []provider.Message{
		{Role: "user", Content: "Context Packer trace replay acceptance: strict clean=true and CMR >= 0.95."},
		{Role: "assistant", Content: "Evidence file should be docs/reports/context-packer-hardcases-v4-trace-capable-20260607.jsonl."},
		{Role: "user", Content: "travel filler unrelated note"},
		{Role: "assistant", Content: "travel filler unrelated response with no current task relevance"},
	}

	terms := historyIntentTerms("Context Packer trace replay", messages)
	selected := planner.selectIntentAwareRecentHistory(messages, terms)
	text := strings.ToLower(messagesToTestText(selected))
	if strings.Contains(text, "travel filler") {
		t.Fatalf("expected explicitly irrelevant tail to be dropped, got:\n%s", text)
	}
	for _, want := range []string{"strict clean=true", "context-packer-hardcases"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected selected history to contain %q, got:\n%s", want, text)
		}
	}
}

func messagesToTestText(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}
