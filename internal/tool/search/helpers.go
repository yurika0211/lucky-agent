package search

import "github.com/yurika0211/luckyharness/internal/utils"

func normalizeURL(rawURL string) string {
	return utils.NormalizeURL(rawURL)
}

func stripHTMLTags(s string) string {
	return utils.StripHTMLTags(s)
}

func normalizeWhitespace(s string) string {
	return utils.NormalizeWhitespace(s)
}
