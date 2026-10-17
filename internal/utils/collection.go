package utils

import (
	"strings"

	"github.com/yurika0211/luckyagent-tools/textutil"
)

/*
DedupStringsLimit 在保留原始顺序的前提下移除重复字符串，并限制输出数量。

当 limit <= 0 时，函数会按“不过滤数量上限”的方式处理全部输入。
*/
var DedupStringsLimit = textutil.DedupStringsLimit

/*
DedupNonEmptyStrings 会过滤空字符串，并在保留原始顺序的前提下去重。
*/
func DedupNonEmptyStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
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

/*
FilterNonEmptyTrimmed 会先对每个元素执行 TrimSpace，再保留非空结果。
*/
func FilterNonEmptyTrimmed(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
