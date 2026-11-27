package agent

import (
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

type CompactSummaryValidation = compactlib.Validation

// validateCompactSummary runs the engine shape checks and then requires the
// summary to keep the identifiers of the messages it replaces. Section keywords
// alone can be satisfied by filler, so they are not enough to let a summary
// overwrite the messages it summarizes.
func validateCompactSummary(summary string, messages []provider.Message) CompactSummaryValidation {
	body, block := splitCompactRecord(summary)
	if strings.TrimSpace(block) == "" && strings.Contains(summary, "Source identifiers") {
		body, block = summary, summary
	}
	validation := compactlib.Validate(compactBodyForLength(body), toCompactMessages(messages))
	if !validation.Valid {
		if len([]rune(compactBodyForLength(body))) <= compactlib.MaxSummaryRunes {
			filtered := validation.Missing[:0]
			for _, item := range validation.Missing {
				if strings.HasPrefix(item, "summary length <= ") {
					continue
				}
				filtered = append(filtered, item)
			}
			validation.Missing = filtered
			if len(validation.Missing) == 0 {
				validation.Valid = true
				validation.Reason = ""
			}
		}
		if !validation.Valid {
			return validation
		}
	}
	// The body may omit source identifiers. A record that carries no evidence
	// block is not yet a persisted compact, so shape-valid prose still passes.
	// Once a block is attached it must be complete, and the body must not cite
	// an identifier the source does not contain.
	if strings.TrimSpace(block) != "" {
		missing := missingEvidenceIdentifiers(block, requiredCompactIdentifiers(messages))
		if len(missing) > 0 {
			validation.Valid = false
			validation.Missing = append(validation.Missing, missing...)
			validation.Reason = compactIdentifierReason(missing)
			return validation
		}
	}
	invented := inventedIdentifiers(body, messages)
	if len(invented) == 0 {
		return validation
	}
	validation.Valid = false
	validation.Missing = append(validation.Missing, invented...)
	validation.Reason = compactIdentifierReason(invented)
	return validation
}

func compactBodyForLength(body string) string {
	const marker = "Source identifiers (verbatim tool evidence"
	if index := strings.Index(body, marker); index >= 0 {
		return strings.TrimSpace(body[:index])
	}
	return body
}
