package agent

import (
	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

type CompactSummaryValidation = compactlib.Validation

// validateCompactSummary runs the engine shape checks and then requires the
// summary to keep the identifiers of the messages it replaces. Section keywords
// alone can be satisfied by filler, so they are not enough to let a summary
// overwrite the messages it summarizes.
func validateCompactSummary(summary string, messages []provider.Message) CompactSummaryValidation {
	validation := compactlib.Validate(summary, toCompactMessages(messages))
	if !validation.Valid {
		return validation
	}
	missing := missingCompactIdentifiers(summary, requiredCompactIdentifiers(messages))
	if len(missing) == 0 {
		return validation
	}
	validation.Valid = false
	validation.Missing = append(validation.Missing, missing...)
	validation.Reason = compactIdentifierReason(missing)
	return validation
}
