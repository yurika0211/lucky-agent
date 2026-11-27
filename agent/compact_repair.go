package agent

import (
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
)

const compactTrimMarker = "[Summary details omitted to stay within the summary length limit.]"

// compactSummarySections is the trim order for an over-long model summary.
// Completed work goes first, then the evidence lists. The current goal and
// pending work stay until nothing else can be shortened.
var compactSummarySections = []string{
	"Completed work:",
	"Key files and functions:",
	"Commands and test results:",
	"User constraints:",
	"Uncertain facts:",
	"Current user goal:",
	"Pending work:",
}

// repairCompactSummary keeps a model summary that failed shape or identifier
// checks. It trims to the rune cap, then appends the same verbatim identifier
// appendix local summaries already use. It does not call the model again and
// does not weaken the identifier check. A summary that is still invalid, or
// that was not a recognizable summary to begin with, comes back unchanged so
// the caller rejects it.
func repairCompactSummary(summary string, messages []provider.Message) (string, bool) {
	original := summary
	if validation := validateCompactSummary(summary, messages); validation.Valid {
		return summary, false
	} else if !compactSummaryRepairable(validation, messages) {
		return summary, false
	}

	identifiers := requiredCompactIdentifiers(messages)
	body, _ := splitCompactRecord(summary)
	summary = trimCompactSummary(body, compactlib.MaxSummaryRunes)
	summary = joinCompactRecord(summary, compactEvidenceBlock(messages))
	summary = appendMissingCompactIdentifiers(compactBodyForLength(summary), identifiers)
	if !strings.Contains(summary, compactEvidenceBlockHeader) {
		summary = joinCompactRecord(summary, compactEvidenceBlock(messages))
	}

	if validation := validateCompactSummary(summary, messages); !validation.Valid {
		return original, false
	}
	if summary == original {
		return summary, false
	}
	return summary, true
}

// compactSummaryRepairable reports whether a failed validation is only too
// long or missing source identifiers. A summary below the minimum length, or
// one that never had the required sections, is not repaired into a passing
// summary. Identifier misses are the raw identifier text, so they are matched
// against this summary's required set instead of being inferred by exclusion.
func compactSummaryRepairable(validation CompactSummaryValidation, messages []provider.Message) bool {
	if len(validation.Missing) == 0 {
		return false
	}
	required := requiredCompactIdentifiers(messages)
	for _, item := range validation.Missing {
		if strings.HasPrefix(item, "summary length <= ") {
			continue
		}
		if !compactMissingIsIdentifier(item, required) {
			return false
		}
	}
	return true
}

func compactMissingIsIdentifier(item string, required []string) bool {
	for _, identifier := range required {
		if item == identifier {
			return true
		}
	}
	return false
}

// trimCompactSummary shortens summary to maxRunes by dropping later bullets
// of the lower-priority sections. Sections that are not in the known shape
// are left untouched. A summary that is already within the cap is returned
// as-is. Goal and pending work are last, so an ordinary over-long completed
// section is cut before either of them moves.
func trimCompactSummary(summary string, maxRunes int) string {
	if maxRunes <= 0 || len([]rune(summary)) <= maxRunes {
		return summary
	}
	for _, heading := range compactSummarySections {
		bullets := compactSectionBullets(summary, heading)
		if len(bullets) == 0 {
			continue
		}
		for len(bullets) > 0 && len([]rune(summary)) > maxRunes {
			bullets = bullets[:len(bullets)-1]
			summary = replaceCompactSection(summary, heading, bullets)
		}
		if len([]rune(summary)) <= maxRunes {
			return summary
		}
	}
	return summary
}

// compactSectionBullets returns the non-empty lines under heading, without a
// trim marker left by an earlier pass.
func compactSectionBullets(summary, heading string) []string {
	start, end, ok := compactSectionSpan(summary, heading)
	if !ok {
		return nil
	}
	var bullets []string
	for _, line := range strings.Split(summary[start:end], "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || trim == compactTrimMarker {
			continue
		}
		bullets = append(bullets, trim)
	}
	return bullets
}

// replaceCompactSection rewrites one section body. A cut section keeps a
// single omission marker so the summary still says material was removed.
func replaceCompactSection(summary, heading string, bullets []string) string {
	start, end, ok := compactSectionSpan(summary, heading)
	if !ok {
		return summary
	}
	var b strings.Builder
	for _, bullet := range bullets {
		b.WriteString(bullet)
		b.WriteByte('\n')
	}
	b.WriteString(compactTrimMarker)
	b.WriteByte('\n')
	return summary[:start] + b.String() + summary[end:]
}

// compactHeadingIndex finds heading only as its own line. A substring match
// would treat "Completed work" as the start of "Pending work".
func compactHeadingIndex(summary, heading string) int {
	if summary == "" || heading == "" {
		return -1
	}
	target := strings.ToLower(heading)
	offset := 0
	for _, line := range strings.Split(summary, "\n") {
		if strings.ToLower(strings.TrimSpace(line)) == target {
			return offset + strings.Index(strings.ToLower(line), target)
		}
		offset += len(line) + 1
	}
	return -1
}

// compactSectionHeading reports a section title. The check uses runes, not
// bytes: a multi-byte heading such as "当前用户目标:" is still one short line.
func compactSectionHeading(trim string) bool {
	if !strings.HasSuffix(trim, ":") || strings.HasPrefix(trim, "-") {
		return false
	}
	for _, heading := range compactSummarySections {
		if strings.EqualFold(trim, heading) {
			return true
		}
	}
	return false
}

// compactSectionSpan returns the byte range of the body under heading, from
// the first character after the heading line through the character before the
// next section heading.
func compactSectionSpan(summary, heading string) (start, end int, ok bool) {
	if summary == "" || heading == "" {
		return 0, 0, false
	}
	idx := compactHeadingIndex(summary, heading)
	if idx < 0 {
		return 0, 0, false
	}
	rest := summary[idx:]
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		return 0, 0, false
	}
	start = idx + nl + 1
	end = len(summary)
	offset := start
	body := summary[start:]
	for _, line := range strings.Split(body, "\n") {
		trim := strings.TrimSpace(line)
		if trim != "" && compactSectionHeading(trim) {
			end = offset
			break
		}
		offset += len(line) + 1
	}
	if end < start {
		return 0, 0, false
	}
	return start, end, true
}

// appendMissingCompactIdentifiers adds the verbatim identifier appendix used
// by local summaries. Identifiers already present are not repeated. When the
// appendix would pass the rune cap, the body is trimmed first; if it still
// cannot fit, the summary is returned unchanged.
func appendMissingCompactIdentifiers(summary string, identifiers []string) string {
	missing := missingEvidenceIdentifiers(summary, identifiers)
	if len(missing) == 0 {
		return summary
	}
	appendix := compactIdentifierAppendix(missing)
	appendixRunes := len([]rune(appendix))
	if appendixRunes >= compactlib.MaxSummaryRunes {
		return summary
	}
	room := compactlib.MaxSummaryRunes - appendixRunes
	body := summary
	if len([]rune(body)) > room {
		body = trimCompactSummary(body, room)
	}
	if len([]rune(body))+appendixRunes > compactlib.MaxSummaryRunes {
		body = trimCompactSummaryRunes(body, room)
	}
	if len([]rune(body))+appendixRunes > compactlib.MaxSummaryRunes {
		return summary
	}
	return body + appendix
}

func compactIdentifierAppendix(identifiers []string) string {
	return "\n\nSource identifiers (verbatim tool evidence; consult the original transcript):\n- " + strings.Join(identifiers, "\n- ")
}

// trimCompactSummaryRunes is the last cut. Section trimming already failed to
// free enough room, so the tail of the body is dropped and marked instead of
// letting the appendix push the summary back over the cap.
func trimCompactSummaryRunes(summary string, maxRunes int) string {
	runes := []rune(summary)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return summary
	}
	marker := []rune("\n" + compactTrimMarker)
	if len(marker) >= maxRunes {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-len(marker)]) + "\n" + compactTrimMarker
}

// fitCompactSummaryAppendix trims a local summary so its identifier appendix
// fits under the rune cap. It uses the same section order as a repaired model
// summary, then a rune tail cut only when a section has no bullet left.
func fitCompactSummaryAppendix(summary string, identifiers []string) string {
	if len(missingEvidenceIdentifiers(summary, identifiers)) == 0 {
		if len([]rune(summary)) > compactlib.MaxSummaryRunes {
			return trimCompactSummary(summary, compactlib.MaxSummaryRunes)
		}
		return summary
	}
	return appendMissingCompactIdentifiers(summary, identifiers)
}
