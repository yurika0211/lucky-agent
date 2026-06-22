package memory

import (
	"regexp"
	"strings"
)

var generatedTruncationMarkerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?im)^\s*\.\.\.\s*\[(?:summary|earlier summary)?\s*truncated\]\s*$`),
	regexp.MustCompile(`(?im)^\s*\.\.\.\s*(?:\(|（)?\s*truncated(?:\s+by\s+sandbox|\s+for\s+context)?\s*(?:\)|）)?\s*$`),
	regexp.MustCompile(`(?im)^\s*\[\s*\.\.\.\s*(?:output\s+)?truncated\s*\.\.\.\s*\]\s*$`),
	regexp.MustCompile(`(?im)^\s*\[Output may be truncated after multiple continuation attempts\.\]\s*$`),
}

// sanitizeDurableMemoryContent removes LuckyHarness-generated truncation
// markers before Markdown notes become durable memory. It intentionally keeps
// ordinary prose ellipses because users may type them as meaningful content.
func sanitizeDurableMemoryContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	for _, pattern := range generatedTruncationMarkerPatterns {
		content = pattern.ReplaceAllString(content, "")
	}
	return strings.TrimSpace(collapseBlankLines(content))
}

func collapseBlankLines(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
