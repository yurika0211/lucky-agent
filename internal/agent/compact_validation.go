package agent

import (
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
	"github.com/yurika0211/luckyagent/internal/provider"
)

type CompactSummaryValidation = compactlib.Validation

func validateCompactSummary(summary string, messages []provider.Message) CompactSummaryValidation {
	return compactlib.Validate(summary, toCompactMessages(messages))
}
