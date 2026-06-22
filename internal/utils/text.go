package utils

import "strings"

const ellipsis = "..."

/*
Truncate 按字节长度截断字符串，并在超出长度时追加 "..."。

由于会附加省略号，返回结果的总长度可能大于 maxLen。
*/
func Truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + ellipsis
}

/*
TruncateKeepLength 按字节长度截断字符串，并保证最终长度不超过 maxLen。
*/
func TruncateKeepLength(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= len(ellipsis) {
		return ellipsis[:maxLen]
	}
	return s[:maxLen-len(ellipsis)] + ellipsis
}

// TrimToRunes trims text to at most maxLen runes without appending a display
// marker. Use this for data that can be persisted or summarized later.
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
