package multimodal

import "github.com/yurika0211/luckyagent/internal/utils"

func truncateString(s string, maxLen int) string {
	return utils.Truncate(s, maxLen)
}
