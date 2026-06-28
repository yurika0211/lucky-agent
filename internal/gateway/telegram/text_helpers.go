package telegram

import "github.com/yurika0211/luckyagent/internal/utils"

func truncateString(s string, maxLen int) string {
	return utils.TruncateKeepLength(s, maxLen)
}
