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
	// Tool status rows such as "- status error dur 11 sid 324151" match
	// errorPattern but name no file, command, or stable error text.
	ephemeralToolStatusPattern = regexp.MustCompile(`(?i)^-?\s*status(?:\s*[:=]\s*|\s+)\w+(?:\s+(?:dur|sid|attempt)(?:\s*[:=]\s*|\s+)[\d.]+[a-z]*)*\s*$`)
	// file_read emits the path and range before numbered file contents.
	sourceFileReadHeader = regexp.MustCompile(`(?i)^File: [^\r\n]+\.(?:[cm]?[jt]sx?|go|py|rs|java|kt|swift|c|cc|cpp|h|hpp|cs|rb|sh|bash|php)\r?\nSize: \d+ bytes\r?\nShowing from line \d+, max \d+ lines\b`)
	metricDeclaration    = regexp.MustCompile(`^# (?:HELP|TYPE) [a-zA-Z_:][a-zA-Z0-9_:]*\s+`)
	zeroFailedCount      = regexp.MustCompile(`(?i)(?:^|[,;:(])\s*0\s+failed\s*(?:$|[,;.)])`)
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
		if ephemeralToolStatusLine(identifier) {
			continue
		}
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

func ephemeralToolStatusLine(identifier string) bool {
	text := strings.TrimSpace(identifier)
	if text == "" || toolLineIsCommand(text) || identifierURLPattern.MatchString(text) || identifierPathPattern.MatchString(" "+text) {
		return false
	}
	return ephemeralToolStatusPattern.MatchString(text)
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
		sourceRead := message.Role == "tool" && message.Name == "file_read" && sourceFileReadHeader.MatchString(message.Content)
		for _, line := range strings.Split(message.Content, "\n") {
			if toolLineIsCommand(line) {
				command := strings.TrimSpace(line)
				command = strings.TrimPrefix(strings.TrimPrefix(command, "$ "), "> ")
				add(command)
			}
			// The first line of an error is what a follow-up turn re-reads, so it
			// counts as an identifier even without a path or a value on it.
			trimmed := strings.TrimSpace(line)
			if !errorPattern.MatchString(trimmed) {
				continue
			}
			if message.Role == "tool" {
				// A source listing or a metric declaration documents possible
				// failures. Its paths and URLs are still extracted below.
				if (sourceRead && shapeNumberedLine.MatchString(trimmed)) || metricDeclaration.MatchString(trimmed) {
					continue
				}
				// A successful count can share a line with an actual diagnostic.
				if zeroFailedCount.MatchString(trimmed) && !errorPattern.MatchString(zeroFailedCount.ReplaceAllString(trimmed, " ")) {
					continue
				}
			}
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
	kept := make([]string, 0, len(identifiers))
	for _, identifier := range identifiers {
		if ephemeralToolStatusLine(identifier) {
			continue
		}
		kept = append(kept, identifier)
	}
	identifiers = kept
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

// Identifiers are exact evidence: changing path or command-flag case can name
// a different file or operation even if surrounding prose is reworded.
// The summary body does not have to repeat them. The evidence block does.
func missingCompactIdentifiers(summary string, identifiers []string) []string {
	return missingEvidenceIdentifiers(evidenceBlockText(summary), identifiers)
}

func evidenceBlockText(record string) string {
	_, block := splitCompactRecord(record)
	if strings.TrimSpace(block) != "" {
		return block
	}
	if strings.Contains(record, "Source identifiers") {
		return record
	}
	return ""
}

func missingEvidenceIdentifiers(block string, identifiers []string) []string {
	if len(identifiers) == 0 {
		return nil
	}
	if strings.TrimSpace(block) == "" {
		return append([]string(nil), identifiers...)
	}
	var missing []string
	for _, identifier := range identifiers {
		if !strings.Contains(block, identifier) {
			missing = append(missing, identifier)
		}
	}
	return missing
}

func compactEvidenceBlock(messages []provider.Message) string {
	identifiers := requiredCompactIdentifiers(messages)
	if len(identifiers) == 0 {
		return compactEvidenceBlockHeader + "\n- none"
	}
	return compactEvidenceBlockHeader + "\n- " + strings.Join(identifiers, "\n- ")
}

func joinCompactRecord(summary, evidence string) string {
	summary = strings.TrimSpace(summary)
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return summary
	}
	return summary + "\n\n" + evidence
}

func splitCompactRecord(record string) (body, evidence string) {
	index := strings.Index(record, "\n"+compactEvidenceBlockHeader)
	if index < 0 && strings.HasPrefix(record, compactEvidenceBlockHeader) {
		return "", record
	}
	if index < 0 {
		return record, ""
	}
	return record[:index], record[index+1:]
}

func inventedIdentifiers(body string, known []provider.Message) []string {
	seen := map[string]struct{}{}
	for _, message := range known {
		seen[message.Content] = struct{}{}
		for _, call := range message.ToolCalls {
			seen[call.Name] = struct{}{}
			seen[call.Arguments] = struct{}{}
		}
	}
	var invented []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		name := ""
		switch {
		case strings.HasPrefix(line, "tool:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "tool:"))
		case strings.HasPrefix(line, "cited:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "cited:"))
		default:
			continue
		}
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok || strings.Contains(sourceText(known), name) {
			continue
		}
		invented = append(invented, name)
	}
	return invented
}

func sourceText(messages []provider.Message) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString(message.Content)
		b.WriteByte('\n')
		for _, call := range message.ToolCalls {
			b.WriteString(call.Name)
			b.WriteByte('\n')
			b.WriteString(call.Arguments)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// compactCutIsExchange reports whether a retained tail starts on a user or
// assistant message. A tool result is not a legal cut.
func compactRecordRebuildable(summary, evidence string, retained []provider.Message) bool {
	return strings.TrimSpace(summary) != "" && strings.TrimSpace(evidence) != "" && compactCutIsExchange(retained)
}

func compactCutIsExchange(retained []provider.Message) bool {
	if len(retained) == 0 {
		return true
	}
	role := retained[0].Role
	return role == "user" || role == "assistant"
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
