package utils

import (
	"regexp"
	"strings"
)

var (
	htmlTagPattern    = regexp.MustCompile(`<[^>]*>`)
	whitespacePattern = regexp.MustCompile(`\s+`)
)

/*
StripHTMLTags 使用轻量级正则去除字符串中的 HTML 标签。
*/
func StripHTMLTags(s string) string {
	return htmlTagPattern.ReplaceAllString(s, "")
}

/*
NormalizeWhitespace 会将连续空白折叠为单个空格，并去掉首尾空白。
*/
func NormalizeWhitespace(s string) string {
	s = whitespacePattern.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
