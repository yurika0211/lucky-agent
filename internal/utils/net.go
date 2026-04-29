package utils

import (
	"net/url"
	"strings"
)

/*
URLEncode 对查询文本执行 URL 编码，并将空格编码为 RFC3986 友好的 %20。
*/
func URLEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
