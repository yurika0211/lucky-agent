package agent

import (
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

func generateLocalCompactSummary(messages []provider.Message, _ *contextx.TokenEstimator) string {
	summary := compactlib.LocalSummary(toCompactMessages(messages), compactTextOptions())
	// LocalSummary limits its file/command lists. Preserve the required set in
	// an extractive appendix instead of weakening the validator for local mode.
	// Over-long bodies are cut by section before the appendix is attached, so
	// the rune cap is not met by slicing the goal or pending work in half.
	return fitCompactSummaryAppendix(summary, requiredCompactIdentifiers(messages))
}
