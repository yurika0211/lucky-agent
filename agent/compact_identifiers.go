package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

// A compact summary replaces the messages it covers, so an identifier that only
// appeared in that range has to survive in the summary or it is gone. Keyword
// checks cannot tell a summary that kept the evidence from one that only sounds
// like a summary, so summaries are checked against the identifiers of their
// source.
var (
	identifierPathPattern   = regexp.MustCompile(`(?:^|[\s"'(=:\[,])((?:/[\w.\-]+){2,}/?|(?:\.{1,2}/)[^\s"')\]]+|~/[^\s"')\]]+|[A-Za-z]:\\[^\s"')\]]+)`)
	identifierURLPattern    = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"')\]]+`)
	identifierCommitPattern = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	identifierValuePattern  = valuePattern
)

// maxCompactIdentifiers bounds the check so a large tool dump cannot turn one
// summary into thousands of required substrings.
const maxCompactIdentifiers = 40

// maxCompactErrorHeadRunes bounds one captured error line.
const maxCompactErrorHeadRunes = 160

// requiredCompactIdentifiers is the gate for a summary that may replace
// messages. Measurements are kept when they fit, but a rewritten duration
// must not fail automatic compaction. Paths, commands, and error heads are
// the identifiers a follow-up turn cannot rebuild.
func requiredCompactIdentifiers(messages []provider.Message) []string {
	required := make([]string, 0, maxCompactIdentifiers)
	for _, identifier := range compactIdentifiers(messages) {
		if identifierValuePattern.MatchString(identifier) &&
			!errorPattern.MatchString(identifier) &&
			!identifierPathPattern.MatchString(" "+identifier) &&
			!identifierURLPattern.MatchString(identifier) &&
			!toolLineIsCommand(identifier) {
			continue
		}
		required = append(required, identifier)
	}
	return required
}

// compactIdentifiers returns the identifiers of the messages a summary covers.
// Paths, URLs, commits, and values are mutually exclusive shapes, and the result
// is deduplicated and ordered so a failure can name a stable identifier.
func compactIdentifiers(messages []provider.Message) []string {
	seen := make(map[string]struct{}, maxCompactIdentifiers)
	out := make([]string, 0, maxCompactIdentifiers)
	add := func(candidate string) {
		candidate = strings.TrimSpace(strings.Trim(candidate, "\"'(),[]<>."))
		if len(candidate) < 3 || len([]rune(candidate)) > 200 {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	for _, message := range messages {
		for _, line := range strings.Split(message.Content, "\n") {
			// The first line of an error is what a follow-up turn re-reads, so it
			// counts as an identifier even without a path or a value on it.
			if !errorPattern.MatchString(line) {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if runes := []rune(trimmed); len(runes) > maxCompactErrorHeadRunes {
				trimmed = string(runes[:maxCompactErrorHeadRunes])
			}
			add(trimmed)
		}
		for _, match := range identifierURLPattern.FindAllString(message.Content, -1) {
			add(match)
		}
		for _, match := range identifierPathPattern.FindAllStringSubmatch(message.Content, -1) {
			if len(match) > 1 {
				add(match[1])
			}
		}
		for _, match := range identifierCommitPattern.FindAllString(strings.ToLower(message.Content), -1) {
			// A bare hex word is only an identifier when it is not part of prose.
			if len(match) >= 7 && len(match) <= 40 {
				add(match)
			}
		}
		for _, match := range identifierValuePattern.FindAllString(message.Content, -1) {
			add(match)
		}
	}
	return preferCompactIdentifiers(out)
}

// preferCompactIdentifiers keeps the check explainable. Alphabetical order
// let a timestamp crowd out a path. Error heads, paths, URLs, commits, and
// commands outrank bare measurements when the set is capped.
func preferCompactIdentifiers(identifiers []string) []string {
	rank := func(identifier string) int {
		switch {
		case errorPattern.MatchString(identifier):
			return 0
		case identifierURLPattern.MatchString(identifier) || identifierPathPattern.MatchString(" "+identifier):
			return 1
		case toolLineIsCommand(identifier):
			return 2
		case identifierCommitPattern.MatchString(identifier):
			return 3
		default:
			return 4
		}
	}
	sort.SliceStable(identifiers, func(i, j int) bool {
		left, right := rank(identifiers[i]), rank(identifiers[j])
		if left != right {
			return left < right
		}
		return identifiers[i] < identifiers[j]
	})
	if len(identifiers) > maxCompactIdentifiers {
		identifiers = identifiers[:maxCompactIdentifiers]
	}
	return identifiers
}

// missingCompactIdentifiers reports which identifiers the summary does not
// mention. Matching is case-insensitive because a summary may reflow casing.
func missingCompactIdentifiers(summary string, identifiers []string) []string {
	if len(identifiers) == 0 {
		return nil
	}
	lower := strings.ToLower(summary)
	missing := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		if !strings.Contains(lower, strings.ToLower(identifier)) {
			missing = append(missing, identifier)
		}
	}
	return missing
}

// compactIdentifierReason names the first few missing identifiers so a failed
// validation says what the summary dropped instead of only that it was short.
func compactIdentifierReason(missing []string) string {
	const shown = 3
	if len(missing) <= shown {
		return fmt.Sprintf("summary drops source identifiers: %s", strings.Join(missing, ", "))
	}
	return fmt.Sprintf("summary drops source identifiers: %s, and %d more", strings.Join(missing[:shown], ", "), len(missing)-shown)
}
