package agent

import "github.com/yurika0211/luckyharness/internal/utils"

/**
 * truncate 是对 utils.Truncate 的本地封装
 */
func truncate(s string, maxLen int) string {
	return utils.Truncate(s, maxLen)
}
