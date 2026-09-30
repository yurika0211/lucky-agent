// Package textutil contains the small text operations used by Aestus.
package textutil

import "strings"

// TrimToRunes truncates s to at most maxLen Unicode code points. A non-positive
// limit produces an empty string.
func TrimToRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return strings.TrimSpace(string(runes[:maxLen]))
}

// DedupNonEmptyStrings removes blank strings and preserves first-seen order.
func DedupNonEmptyStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
