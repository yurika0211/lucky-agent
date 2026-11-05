package contextx

import (
	"strings"
	"testing"
)

func TestFitKeepsCompleteRecentToolExchange(t *testing.T) {
	w := NewContextWindow(WindowConfig{MaxTokens: 500, Strategy: TrimLowPriority})
	messages := []Message{
		{Role: "system", Content: "Project constraint", Priority: PriorityCritical},
		{Role: "assistant", Content: strings.Repeat("old context ", 300), Priority: PriorityNormal},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "one", Name: "read"}, {ID: "two", Name: "read"}}, Priority: PriorityNormal},
		{Role: "tool", ToolCallID: "one", Content: "first evidence", Priority: PriorityNormal},
		{Role: "tool", ToolCallID: "two", Content: "latest failure evidence", Priority: PriorityNormal},
	}
	got, fit := w.Fit(messages)
	if len(got) != 4 || len(got[1].ToolCalls) != 2 || got[3].ToolCallID != "two" || fit.FinalTokens > fit.AvailableTokens {
		t.Fatalf("exchange lost or split: %+v %+v", got, fit)
	}
}

func TestFitNeverKeepsOrphanOrIncompleteToolRound(t *testing.T) {
	w := NewContextWindow(WindowConfig{MaxTokens: 150, Strategy: TrimLowPriority})
	messages := []Message{
		{Role: "system", Content: "Pinned", Priority: PriorityCritical},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "large", Name: "write", Arguments: strings.Repeat("large ", 400)}}, Priority: PriorityNormal},
		{Role: "tool", ToolCallID: "large", Content: "cannot keep alone", Priority: PriorityNormal},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "missing", Name: "read"}}, Priority: PriorityNormal},
		{Role: "user", Content: "Latest request", Priority: PriorityCritical},
	}
	got, _ := w.Fit(messages)
	for _, m := range got {
		if m.Role == "tool" || len(m.ToolCalls) > 0 {
			t.Fatalf("incomplete round entered context: %+v", got)
		}
	}
	if got[len(got)-1].Content != "Latest request" {
		t.Fatal("latest request lost")
	}
}

func TestCriticalOverflowIsVisible(t *testing.T) {
	w := NewContextWindow(WindowConfig{MaxTokens: 80, Strategy: TrimLowPriority})
	_, fit := w.Fit([]Message{{Role: "system", Content: strings.Repeat("constraint ", 100), Priority: PriorityCritical}})
	if fit.FinalTokens <= fit.AvailableTokens {
		t.Fatalf("critical overflow was hidden: %+v", fit)
	}
}
