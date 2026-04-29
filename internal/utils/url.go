package utils

import (
	"net/url"
	"strings"
)

/*
NormalizeURL 对 URL 做标准化处理，以便后续去重和稳定比较。
*/
func NormalizeURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
