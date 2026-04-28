package telegram

import (
	"regexp"
	"strings"
)

var (
	toolCallBlockRe = regexp.MustCompile(`(?is)<tool_call>.*?</tool_call>`)
	jsonToolCallRe  = regexp.MustCompile(`(?i)^\s*\{\s*"name"\s*:\s*"[a-z0-9_\-]+"\s*,\s*"arguments"\s*:`)
	jsonCommandRe   = regexp.MustCompile(`(?i)^\s*\{\s*"command"\s*:`)
	jsonToolRe      = regexp.MustCompile(`(?i)^\s*\{\s*"tool"\s*:`)
	channelTagRe    = regexp.MustCompile(`(?i)^\{?\s*to=[a-z0-9_\-]+`)
	punctOnlyRe     = regexp.MustCompile(`^[\{\}\[\]\(\)!",:\.\-\+\s]+$`)
	toolNameLikeRe  = regexp.MustCompile(`(?i)^[a-z][a-z0-9_\-]{1,40}$`)
)

func sanitizeOutgoingText(input string) string {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return ""
	}

	text := toolCallBlockRe.ReplaceAllString(raw, "")
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	removed := 0

	protocolSeen := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			kept = append(kept, line)
			continue
		}
		if channelTagRe.MatchString(trimmed) || strings.Contains(strings.ToLower(trimmed), "<tool_call>") || strings.Contains(strings.ToLower(trimmed), "</tool_call>") {
			protocolSeen = true
			removed++
			continue
		}
		if jsonToolCallRe.MatchString(trimmed) || jsonCommandRe.MatchString(trimmed) || jsonToolRe.MatchString(trimmed) {
			protocolSeen = true
			removed++
			continue
		}
		if trimmed == "```" || strings.HasPrefix(trimmed, "```json") {
			protocolSeen = true
			removed++
			continue
		}
		if protocolSeen && isLikelyProtocolFragment(trimmed) {
			removed++
			continue
		}
		kept = append(kept, line)
	}

	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if out == "" && removed > 0 {
		return internalOutputFilteredFallback
	}
	return out
}

func isLikelyProtocolFragment(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if punctOnlyRe.MatchString(lower) {
		return true
	}
	if lower == "tool" || lower == "tool_call" {
		return true
	}
	// e.g. cron_status / current_time
	if toolNameLikeRe.MatchString(lower) && strings.Contains(lower, "_") {
		return true
	}
	if strings.HasPrefix(lower, "{to=") || strings.HasPrefix(lower, "to=") {
		return true
	}
	return false
}
