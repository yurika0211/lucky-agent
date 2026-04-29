package utils

import (
	"regexp"
	"strings"
)

var (
	toolProtocolBlockRe = regexp.MustCompile(`(?is)<tool_call>.*?</tool_call>`)
	toolProtocolJSONRe  = regexp.MustCompile(`(?i)^\s*\{\s*"(name|tool|tool_name|command)"\s*:`)
	toolProtocolToRe    = regexp.MustCompile(`(?i)^\{?\s*to=[a-z0-9_\-]+`)
	toolProtocolPunctRe = regexp.MustCompile(`^[\{\}\[\]\(\)!",:\.\-\+\s]+$`)
	toolProtocolNameRe  = regexp.MustCompile(`(?i)^[a-z][a-z0-9_\-]{1,40}$`)
	toolMetaLeakRe      = regexp.MustCompile(`(?i)(commentary channel|analysis channel|tool call syntax|tool-call syntax|assistant to=|as chatgpt|let'?s attempt|hidden parser|function_call object|need infer|maybe the tool call)`)
)

const ToolProtocolFilteredFallback = "已完成处理。内部工具调用日志已自动隐藏。"

// SanitizeToolProtocolOutput removes leaked tool protocol text from assistant-visible content.
func SanitizeToolProtocolOutput(input string) string {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return ""
	}

	text := toolProtocolBlockRe.ReplaceAllString(raw, "")
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	removed := 0
	protocolSeen := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(kept) > 0 && kept[len(kept)-1] != "" {
				kept = append(kept, "")
			}
			continue
		}

		lower := strings.ToLower(trimmed)
		if idx := strings.Index(lower, "to="); idx > 0 {
			cleanedInline := strings.TrimSpace(line[:idx])
			protocolSeen = true
			removed++
			if cleanedInline != "" {
				kept = append(kept, cleanedInline)
			}
			continue
		}
		if toolProtocolToRe.MatchString(trimmed) ||
			toolProtocolJSONRe.MatchString(trimmed) ||
			strings.Contains(lower, "<tool_call>") ||
			strings.Contains(lower, "</tool_call>") ||
			trimmed == "```" ||
			strings.HasPrefix(lower, "```json") ||
			strings.HasPrefix(lower, "```tool") {
			protocolSeen = true
			removed++
			continue
		}

		if protocolSeen && (isLikelyProtocolFragment(trimmed) || toolMetaLeakRe.MatchString(trimmed)) {
			removed++
			continue
		}

		kept = append(kept, line)
	}

	out := strings.TrimSpace(strings.Join(kept, "\n"))
	out = collapseBlankLines(out)
	if out == "" && removed > 0 {
		return ToolProtocolFilteredFallback
	}
	return out
}

func isLikelyProtocolFragment(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if toolProtocolPunctRe.MatchString(lower) {
		return true
	}
	if lower == "tool" || lower == "tool_call" {
		return true
	}
	if toolProtocolNameRe.MatchString(lower) && strings.Contains(lower, "_") {
		return true
	}
	if strings.HasPrefix(lower, "{to=") || strings.HasPrefix(lower, "to=") {
		return true
	}
	return false
}

func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, line)
		prevBlank = blank
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
