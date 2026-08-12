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
	// Protocol cleanup must not inspect lines inside a normal Markdown code
	// block. A standalone `}` (or even `tool_call`) is valid source code and
	// must not be treated as protocol residue.
	inCodeFence := false
	inProtocolFence := false
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			kept = append(kept, line)
			continue
		}
		if isMarkdownFenceLine(trimmed) {
			if inProtocolFence {
				protocolSeen = true
				removed++
				inProtocolFence = false
				continue
			}
			if inCodeFence {
				kept = append(kept, line)
				inCodeFence = false
				continue
			}
			// Only classify an opening fence from the payload immediately
			// following it. Looking at the previous line makes a normal code
			// block look like a protocol block when a tool call came before it.
			if isProtocolFenceOpening(lines, idx) {
				protocolSeen = true
				removed++
				inProtocolFence = true
				continue
			}
			kept = append(kept, line)
			inCodeFence = true
			continue
		}
		if inCodeFence {
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
		if isProtocolFenceLine(lines, idx) {
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

func isProtocolFenceOpening(lines []string, idx int) bool {
	if idx < 0 || idx >= len(lines) || !isMarkdownFenceLine(strings.TrimSpace(lines[idx])) {
		return false
	}
	return neighborLooksLikeProtocol(lines, idx, 1)
}

func isProtocolFenceLine(lines []string, idx int) bool {
	if idx < 0 || idx >= len(lines) {
		return false
	}
	trimmed := strings.TrimSpace(lines[idx])
	if !isMarkdownFenceLine(trimmed) {
		return false
	}
	if neighborLooksLikeProtocol(lines, idx, -1) || neighborLooksLikeProtocol(lines, idx, 1) {
		return true
	}
	return false
}

func neighborLooksLikeProtocol(lines []string, idx int, step int) bool {
	for j := idx + step; j >= 0 && j < len(lines); j += step {
		trimmed := strings.TrimSpace(lines[j])
		if trimmed == "" {
			continue
		}
		return isLikelyProtocolFencePayload(trimmed)
	}
	return false
}

func isLikelyProtocolFencePayload(trimmed string) bool {
	lower := strings.ToLower(trimmed)
	if channelTagRe.MatchString(trimmed) ||
		jsonToolCallRe.MatchString(trimmed) ||
		jsonCommandRe.MatchString(trimmed) ||
		jsonToolRe.MatchString(trimmed) ||
		strings.Contains(lower, "<tool_call>") ||
		strings.Contains(lower, "</tool_call>") {
		return true
	}
	if lower == "tool" || lower == "tool_call" {
		return true
	}
	if toolNameLikeRe.MatchString(lower) && strings.Contains(lower, "_") {
		return true
	}
	if strings.HasPrefix(lower, "{to=") || strings.HasPrefix(lower, "to=") {
		return true
	}
	return false
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
