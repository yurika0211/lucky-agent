package memory

import "github.com/yurika0211/luckyagent/internal/utils"

func truncateField(s string, maxLen int) string {
	return utils.TrimToRunes(s, maxLen)
}

func dedupSlice(items []string) []string {
	return utils.DedupNonEmptyStrings(items)
}
