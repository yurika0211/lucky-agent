package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

const (
	// compactSummaryTimeout is the hard cap for the LLM summarizer call.
	// Parent turn deadlines are not inherited when they would cut this short;
	// explicit parent cancellation still aborts before the call starts.
	compactSummaryTimeout = 45 * time.Second

	// compactTranscriptMaxRunes bounds the text sent to the summarizer.
	// Unbounded per-message dumps were the main cause of
	// "generate summary: context deadline exceeded" on long sessions.
	compactTranscriptMaxRunes = 48_000

	compactTranscriptUserRunes     = 420
	compactTranscriptAssistantRunes = 300
	compactTranscriptToolRunes     = 220
	compactTranscriptDefaultRunes  = 260
)

// compactSummaryContext builds the context used for the compaction LLM call.
// It keeps request-scoped values, drops an already-tight parent deadline so the
// summarizer still gets a full budget, and fails fast if the parent is already
// canceled (user abort / process shutdown).
func compactSummaryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		dead, cancel := context.WithCancel(ctx)
		cancel()
		return dead, func() {}
	}
	base := ctx
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < compactSummaryTimeout {
			base = context.WithoutCancel(ctx)
		}
	}
	return context.WithTimeout(base, compactSummaryTimeout)
}

// compactTranscript renders session messages into a budgeted plain-text
// transcript for the compaction summarizer. Tool bodies are summarized first;
// when the full transcript would exceed the rune budget, older lines are dropped
// and a short omission note is kept so the model still knows history was truncated.
func compactTranscript(messages []provider.Message) string {
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		if line := formatCompactTranscriptLine(msg); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}

	total := 0
	for _, line := range lines {
		total += utf8.RuneCountInString(line) + 1
	}
	if total <= compactTranscriptMaxRunes {
		return strings.Join(lines, "\n")
	}

	// Prefer recent evidence: walk from the end until the budget fills.
	var kept []string
	used := 0
	omitted := 0
	for i := len(lines) - 1; i >= 0; i-- {
		lineRunes := utf8.RuneCountInString(lines[i]) + 1
		if used+lineRunes > compactTranscriptMaxRunes && len(kept) > 0 {
			omitted = i + 1
			break
		}
		if used+lineRunes > compactTranscriptMaxRunes {
			// Single oversized line: hard-trim it.
			trimmed := truncateRunes(lines[i], compactTranscriptMaxRunes-16)
			kept = append(kept, trimmed)
			omitted = i
			break
		}
		kept = append(kept, lines[i])
		used += lineRunes
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	if omitted <= 0 {
		return strings.Join(kept, "\n")
	}
	note := fmt.Sprintf(
		"[compact transcript truncated: omitted %d older lines; prefer recent goals, files, commands, and unresolved items]",
		omitted,
	)
	return note + "\n" + strings.Join(kept, "\n")
}

func formatCompactTranscriptLine(msg provider.Message) string {
	if session.IsCompactBoundary(msg) {
		return ""
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" && len(msg.ToolCalls) == 0 {
		return ""
	}

	role := strings.ToUpper(strings.TrimSpace(msg.Role))
	if role == "" {
		role = "MESSAGE"
	}

	switch strings.ToLower(strings.TrimSpace(msg.Role)) {
	case "tool":
		if summary := summarizeToolResult(msg.Name, content); summary != "" {
			content = summary
		} else {
			content = truncateRunes(content, compactTranscriptToolRunes)
		}
		content = truncateRunes(content, compactTranscriptToolRunes)
	case "user":
		content = truncateRunes(content, compactTranscriptUserRunes)
	case "assistant":
		content = truncateRunes(content, compactTranscriptAssistantRunes)
	default:
		content = truncateRunes(content, compactTranscriptDefaultRunes)
	}

	var b strings.Builder
	b.WriteString(role)
	if name := strings.TrimSpace(msg.Name); name != "" {
		b.WriteString("(" + name + ")")
	}
	b.WriteString(": ")
	if content != "" {
		b.WriteString(content)
	}
	if n := len(msg.ToolCalls); n > 0 {
		names := make([]string, 0, n)
		for _, tc := range msg.ToolCalls {
			if name := strings.TrimSpace(tc.Name); name != "" {
				names = append(names, name)
			}
		}
		if len(names) > 0 {
			b.WriteString(fmt.Sprintf(" [tool_calls=%s]", strings.Join(names, ",")))
		} else {
			b.WriteString(fmt.Sprintf(" [tool_calls=%d]", n))
		}
	}
	return b.String()
}

func truncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	var b strings.Builder
	b.Grow(maxRunes * 4)
	n := 0
	for _, r := range s {
		if n >= maxRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
