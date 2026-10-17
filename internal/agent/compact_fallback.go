package agent

import (
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/provider"
)

func generateLocalCompactSummary(messages []provider.Message, _ *contextx.TokenEstimator) string {
	return compactlib.LocalSummary(toCompactMessages(messages), compactTextOptions())
}
