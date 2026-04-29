package utils

import (
	"strings"
	"testing"
)

func testEstimate(s string) int {
	return len([]rune(s))
}

func TestCompactMarkdownForPrompt_NoTrimNeeded(t *testing.T) {
	in := "# Title\n\nShort text."
	got := CompactMarkdownForPrompt(in, 1000, testEstimate, MarkdownBudgetOptions{})
	if got != in {
		t.Fatalf("expected unchanged text, got %q", got)
	}
}

func TestCompactMarkdownForPrompt_PrefersRuleSection(t *testing.T) {
	in := strings.Join([]string{
		"# Intro",
		"",
		"small intro",
		"",
		"## Rules",
		"",
		"You must verify facts. Never print secrets.",
		"",
		"## Filler",
		"",
		strings.Repeat("filler ", 80),
	}, "\n")

	got := CompactMarkdownForPrompt(in, 120, testEstimate, MarkdownBudgetOptions{})
	if !strings.Contains(got, "## Rules") {
		t.Fatalf("expected rules section kept, got %q", got)
	}
	if testEstimate(got) > 120 {
		t.Fatalf("expected output to fit budget, got len=%d", testEstimate(got))
	}
}

func TestCompactMarkdownForPrompt_PreservesCodeBlock(t *testing.T) {
	in := strings.Join([]string{
		"# Manual",
		"",
		"## Example",
		"",
		"```go",
		"func main() {",
		`    println("ok")`,
		"}",
		"```",
		"",
		"## Notes",
		"",
		strings.Repeat("notes ", 100),
	}, "\n")

	got := CompactMarkdownForPrompt(in, 140, testEstimate, MarkdownBudgetOptions{})
	if !strings.Contains(got, "```go") || !strings.Contains(got, `println("ok")`) {
		t.Fatalf("expected code block preserved, got %q", got)
	}
}

func TestCompactMarkdownForPrompt_FallsBackToTruncation(t *testing.T) {
	in := strings.Repeat("abc", 200)
	got := CompactMarkdownForPrompt(in, 30, testEstimate, MarkdownBudgetOptions{})
	if got == "" {
		t.Fatal("expected non-empty truncated output")
	}
	if !strings.Contains(got, "[... omitted ...]") {
		t.Fatalf("expected omission marker in fallback, got %q", got)
	}
}
