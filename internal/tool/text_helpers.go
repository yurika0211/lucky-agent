package tool

import "github.com/yurika0211/luckyagent/internal/utils"

func truncateStr(s string, maxLen int) string {
	return utils.Truncate(s, maxLen)
}

func stripHTMLTags(s string) string {
	return utils.StripHTMLTags(s)
}

func normalizeWhitespace(s string) string {
	return utils.NormalizeWhitespace(s)
}
