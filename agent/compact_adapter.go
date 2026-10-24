package agent

import (
	compactpkg "github.com/yurika0211/lucky-agent/agent/compact"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

const compactSummaryTimeout = compactpkg.SummaryTimeout

func toCompactMessages(messages []provider.Message) []compactlib.Message {
	return compactpkg.Messages(messages)
}

func compactEstimator(est *contextx.TokenEstimator) compactlib.Estimator {
	return compactpkg.Estimator(est)
}

func compactTextOptions() compactlib.TextOptions {
	return compactpkg.TextOptions(transcriptToolSummary)
}
