package agent

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

func TestCompactTranscriptCapsSizeAndSummarizesTools(t *testing.T) {
	var messages []provider.Message
	for i := 0; i < 400; i++ {
		messages = append(messages,
			provider.Message{Role: "user", Content: strings.Repeat("user-goal-path/internal/agent/foo.go ", 80)},
			provider.Message{Role: "assistant", Content: strings.Repeat("working on the change ", 80)},
			provider.Message{
				Role:    "tool",
				Name:    "terminal",
				Content: strings.Repeat("go test ./internal/agent failed with stack dump line ", 200),
			},
		)
	}
	// Include a boundary that must be skipped.
	messages = append(messages, session.CompactBoundaryMessage(session.CompactMetadata{
		ID:      "skip-me",
		Summary: "should not appear",
	}))

	out := compactTranscript(messages)
	if out == "" {
		t.Fatal("expected non-empty transcript")
	}
	if got := utf8.RuneCountInString(out); got > compactTranscriptMaxRunes+200 {
		t.Fatalf("transcript too large: %d runes (budget %d)", got, compactTranscriptMaxRunes)
	}
	if !strings.Contains(out, "compact transcript truncated") {
		t.Fatalf("expected truncation note in oversized transcript, got prefix:\n%s", out[:min(len(out), 240)])
	}
	if strings.Contains(out, "should not appear") {
		t.Fatal("compact boundary content must not enter the transcript")
	}
	// Each tool line must be short; full multi-KB dumps must not appear.
	if strings.Contains(out, strings.Repeat("stack dump line ", 40)) {
		t.Fatal("raw multi-kilobyte tool dumps must not enter the transcript")
	}
	foundTool := false
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "TOOL(") {
			continue
		}
		foundTool = true
		if got := utf8.RuneCountInString(line); got > 400 {
			t.Fatalf("tool transcript line too long: %d runes: %s", got, truncate(line, 120))
		}
	}
	if !foundTool {
		t.Fatalf("expected tool lines in transcript, sample:\n%s", out[max(0, len(out)-500):])
	}
}

func TestCompactTranscriptKeepsSmallSessionsIntact(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "Fix internal/agent/context_planner.go"},
		{Role: "assistant", Content: "Updating planner compact boundary handling."},
		{Role: "tool", Name: "terminal", Content: "go test ./internal/agent passed"},
	}
	out := compactTranscript(messages)
	if strings.Contains(out, "compact transcript truncated") {
		t.Fatalf("small transcript should not truncate: %s", out)
	}
	if !strings.Contains(out, "USER:") || !strings.Contains(out, "context_planner.go") {
		t.Fatalf("expected user content preserved: %s", out)
	}
	if !strings.Contains(out, "TOOL(terminal):") {
		t.Fatalf("expected tool line: %s", out)
	}
}

func TestCompactSummaryContextDropsTightParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sumCtx, sumCancel := compactSummaryContext(parent)
	defer sumCancel()

	deadline, ok := sumCtx.Deadline()
	if !ok {
		t.Fatal("expected summarizer deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 30*time.Second {
		t.Fatalf("expected near full compact budget, remaining=%s", remaining)
	}
	if sumCtx.Err() != nil {
		t.Fatalf("summarizer context should be live: %v", sumCtx.Err())
	}
}

func TestCompactSummaryContextHonorsAlreadyCanceledParent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	sumCtx, sumCancel := compactSummaryContext(parent)
	defer sumCancel()
	if sumCtx.Err() == nil {
		t.Fatal("canceled parent must keep summarizer context canceled")
	}
}
