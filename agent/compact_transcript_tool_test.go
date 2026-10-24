package agent

import (
	"strings"
	"testing"
)

// criterion: the summarizer input keeps evidence. The engine replaces a tool
// body with whatever SummarizeTool returns, so the hook must return the
// compressed body, not a description of it.
func TestTranscriptToolSummaryKeepsIdentifiers(t *testing.T) {
	body := strings.Join([]string{
		"Resolving module graph",
		strings.Repeat("loading...\n", 60),
		"config: /srv/lucky/config.example.json",
		"error: module not found: example.com/x@v9",
		"elapsed=5231ms",
		strings.Repeat("nothing here\n", 60),
	}, "\n")

	got := transcriptToolSummary("terminal", body)
	if len([]rune(got)) > transcriptToolLimit {
		t.Fatalf("summary exceeds the engine's per-tool cap: %d runes", len([]rune(got)))
	}
	for _, want := range []string{
		"/srv/lucky/config.example.json",
		"error: module not found",
		"5231",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("summarizer input lost %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "loading...") {
		t.Fatalf("redundant lines survived the budget:\n%s", got)
	}
}

// criterion: the hook replaces the generic one-line description. A web_search
// body keeps its identifiers instead of becoming "Searched for X".
func TestTranscriptToolSummaryReplacesOneLineDescription(t *testing.T) {
	body := "Results for: lucky-agent compact\nhttps://example.com/compact\nretry budget is 3 attempts"
	got := transcriptToolSummary("web_search", body)
	if strings.Contains(got, "collected source candidates") {
		t.Fatalf("hook still returns the generic description:\n%s", got)
	}
	for _, want := range []string{"https://example.com/compact", "3 attempts"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summarizer input lost %q:\n%s", want, got)
		}
	}
}
