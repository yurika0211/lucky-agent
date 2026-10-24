package agent

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Tool output is evidence. The next turn may need a path, an error string, a
// commit, or one numeric value that happens to sit in the middle of a long
// result, and a positional head/tail cut drops exactly those lines. Compaction
// therefore drops the most redundant lines and never drops a line that carries
// an identifier.
//
// The redundancy score is a deterministic stand-in for the conditional-entropy
// ranking in #337: the agent loop has no frozen proxy model, and the rule below
// can only remove blank, repeated, and progress lines, so it cannot remove
// evidence by mistake.
type toolLineKind int

const (
	toolLineRedundant toolLineKind = iota
	toolLineNormal
	toolLineProtected
)

type toolLine struct {
	index int
	text  string
	kind  toolLineKind
	score float64
	runes int
}

var (
	diffHunkPattern = regexp.MustCompile(`^(?:@@ -\d|\+\+\+ |--- |diff --git )`)
	commitPattern   = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)
	// A bare 4-digit run matches years, ports, and line numbers on almost every
	// log line, which made compression a no-op. Named measurements stay.
	valuePattern    = regexp.MustCompile(`(?i)(?:\b0x[0-9a-fA-F]{4,}|\b\d+\.\d+%?|\b\d{4,}(?:ms|ns|us|µs|s|kb|mb|gb|kib|mib|gib)\b|(?:elapsed|duration|latency|timeout|size|bytes|count|total|limit|offset|status|code|exit)\s*[=:]\s*\d+)`)
	pathPattern     = regexp.MustCompile(`(?:^|[\s"'(=:\[,])(?:/[\w.\-]+/[^\s"')\]]*|(?:\.{1,2}/)[^\s"')\]]+|~/[^\s"')\]]+|[A-Za-z]:\\[^\s"')\]]+)`)
	urlPattern      = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s"')\]]+`)
	errorPattern    = regexp.MustCompile(`(?i)\b(?:error|panic|exception|traceback|fatal|failed|failure|denied|not found|exit (?:status|code))\b`)
	progressPattern = regexp.MustCompile(`(?i)^\s*(?:\[[^\]]{0,40}\]\s*)?(?:\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?\s*)?(?:\d+%|\.{3,}|…|\[[#=>. -]{3,}\]|loading|fetching|processing|working|thinking|done\.?)?\s*$`)
)

// isRepetitionLine reports whether a line only repeats one drawing character, as
// progress bars and separators do.
func isRepetitionLine(line string) bool {
	if len(line) < 3 {
		return false
	}
	first := line[0]
	if !strings.ContainsRune("#=*-_+", rune(first)) {
		return false
	}
	for i := 1; i < len(line); i++ {
		if line[i] != first {
			return false
		}
	}
	return true
}

// toolCommandVerbs are first tokens that mark a line as a command. A command is
// reproducible evidence: it records what was run.
var toolCommandVerbs = map[string]bool{
	"go": true, "git": true, "npm": true, "npx": true, "pnpm": true, "yarn": true,
	"docker": true, "make": true, "curl": true, "wget": true, "ssh": true, "scp": true,
	"python": true, "python3": true, "pip": true, "node": true, "cargo": true, "rustc": true,
	"bash": true, "sh": true, "zsh": true, "pwsh": true, "powershell": true,
	"kubectl": true, "systemctl": true, "journalctl": true, "rg": true, "grep": true,
	"sed": true, "awk": true, "find": true, "ls": true, "cat": true, "rm": true,
	"mv": true, "cp": true, "mkdir": true, "chmod": true, "chown": true, "tar": true,
	"psql": true, "sqlite3": true, "redis-cli": true, "terraform": true, "helm": true,
}

// classifyToolLine decides whether a line may be dropped. Redundancy wins over
// identifier detection so a timestamped progress line stays droppable even
// though it contains digits.
func classifyToolLine(line string) toolLineKind {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || isRepetitionLine(trimmed) {
		return toolLineRedundant
	}
	if progressPattern.MatchString(trimmed) {
		return toolLineRedundant
	}
	if toolLineHasIdentifier(line) {
		return toolLineProtected
	}
	return toolLineNormal
}

// toolLineHasIdentifier reports whether the line carries something the next turn
// cannot reconstruct from the remaining context.
func toolLineHasIdentifier(line string) bool {
	if urlPattern.MatchString(line) || pathPattern.MatchString(line) {
		return true
	}
	if diffHunkPattern.MatchString(strings.TrimSpace(line)) {
		return true
	}
	if errorPattern.MatchString(line) {
		return true
	}
	if valuePattern.MatchString(line) {
		return true
	}
	if commitPattern.MatchString(strings.ToLower(line)) {
		return true
	}
	return toolLineIsCommand(line)
}

// toolLineIsCommand reports whether the line starts with a shell command.
func toolLineIsCommand(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "$ ")
	trimmed = strings.TrimPrefix(trimmed, "> ")
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return false
	}
	return toolCommandVerbs[strings.ToLower(fields[0])]
}

// toolLineRedundancy scores a droppable line; lower scores are dropped first. A
// repeated line keeps its first occurrence and makes later copies cheap to drop,
// which is what removes repeated table headers and echoed progress.
func toolLineRedundancy(line string, seen map[string]int, position, total int) float64 {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return 0
	}
	seen[trimmed]++
	if seen[trimmed] > 1 {
		return 0.1
	}
	if progressPattern.MatchString(trimmed) || isRepetitionLine(trimmed) {
		return 0.2
	}
	score := 1.0
	if length := float64(len([]rune(trimmed))); length > 0 {
		score += math.Min(length/240.0, 1.0)
	}
	if position == 0 || position == total-1 {
		score += 0.5
	}
	return score
}

// compressToolResultLines shortens out to at most limit runes. Lines that carry
// identifiers are never dropped, redundant lines are dropped first, at least one
// original line survives, and retained lines keep their original order.
func compressToolResultLines(out string, limit int) string {
	if limit <= 0 || len([]rune(out)) <= limit {
		return out
	}
	rawLines := strings.Split(out, "\n")
	lines := make([]toolLine, len(rawLines))
	seen := make(map[string]int, len(rawLines))
	for i, line := range rawLines {
		kind := classifyToolLine(line)
		score := 0.0
		if kind != toolLineProtected {
			score = toolLineRedundancy(line, seen, i, len(rawLines))
		}
		lines[i] = toolLine{index: i, text: line, kind: kind, score: score, runes: len([]rune(line)) + 1}
	}

	kept := make([]bool, len(lines))
	nonProtected := 0
	for _, line := range lines {
		kept[line.index] = true
		if line.kind != toolLineProtected {
			nonProtected++
		}
	}

	// Reserve room for the omission record so adding it cannot overshoot the
	// budget and force a positional cut through a protected line.
	reserve := len([]rune(omissionRecord(len(lines)))) + 1
	dropped := 0
	blockRunes := func() int {
		total, first := 0, true
		for _, line := range lines {
			if !kept[line.index] {
				continue
			}
			if !first {
				total++
			}
			first = false
			total += line.runes - 1
		}
		if dropped > 0 {
			total += 1 + len([]rune(omissionRecord(dropped)))
		}
		return total
	}

	drop := func(index int) {
		if !kept[index] {
			return
		}
		kept[index] = false
		if lines[index].kind != toolLineProtected {
			nonProtected--
		}
		dropped++
	}

	// 1. Redundant lines go first: blank, repeated, and progress lines carry no
	// evidence, so dropping them costs nothing the next turn can use.
	for _, line := range cheapestFirst(lines) {
		if blockRunes()+reserve <= limit {
			break
		}
		if line.kind != toolLineRedundant || nonProtected <= 1 {
			continue
		}
		drop(line.index)
	}

	// 2. A single long line can still dominate the budget. Shrink it in place so
	// the shorter lines around it, which usually name the target and conclusion,
	// survive intact.
	for blockRunes() > limit {
		longest, index := -1, -1
		for _, line := range lines {
			if !kept[line.index] || line.kind == toolLineProtected {
				continue
			}
			if line.runes > longest {
				longest, index = line.runes, line.index
			}
		}
		if index < 0 || longest <= minShrinkableLine {
			break
		}
		shrunk := truncateToolResultMiddle(lines[index].text, longest/2)
		lines[index].text = shrunk
		lines[index].runes = len([]rune(shrunk)) + 1
	}

	// 3. Only then drop ordinary content lines, cheapest first, keeping one.
	for _, line := range cheapestFirst(lines) {
		if blockRunes() <= limit {
			break
		}
		if line.kind == toolLineProtected || nonProtected <= 1 {
			continue
		}
		drop(line.index)
	}

	parts := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		if kept[line.index] {
			parts = append(parts, line.text)
		}
	}
	compacted := strings.Join(parts, "\n")
	if dropped > 0 {
		compacted += "\n" + omissionRecord(dropped)
	}
	// Callers treat limit as a hard cap: a binary search and a shrinking
	// evidence window both assume a smaller limit never returns a longer
	// string. Protected lines are preferred, but a line that alone exceeds
	// the budget is cut in place instead of returning the whole block.
	if len([]rune(compacted)) > limit {
		return fitToolResultToLimit(lines, kept, limit)
	}
	return compacted
}

// fitToolResultToLimit returns a result of at most limit runes. Protected
// lines are shortened from the middle before any of them is dropped, so a
// path or error head still has both ends. The omission record is appended
// only when it still fits; the evidence comes first.
func fitToolResultToLimit(lines []toolLine, kept []bool, limit int) string {
	if limit <= 0 {
		return ""
	}
	selected := make([]int, 0, len(lines))
	for _, line := range lines {
		if kept[line.index] {
			selected = append(selected, line.index)
		}
	}
	if len(selected) == 0 && len(lines) > 0 {
		selected = append(selected, 0)
	}
	texts := make([]string, len(selected))
	costs := make([]int, len(selected))
	for i, index := range selected {
		texts[i] = lines[index].text
		costs[i] = len([]rune(texts[i]))
	}
	separators := len(selected) - 1
	if separators < 0 {
		separators = 0
	}
	for sumRunes(costs)+separators > limit {
		longest, index := -1, -1
		for i, cost := range costs {
			if cost > longest {
				longest, index = cost, i
			}
		}
		if index < 0 || longest <= 1 {
			break
		}
		next := longest / 2
		if next < 1 {
			next = 1
		}
		if next > limit {
			next = limit
		}
		texts[index] = truncateToolResultMiddle(texts[index], next)
		costs[index] = len([]rune(texts[index]))
		if costs[index] >= longest {
			texts[index] = truncateToolResultMiddle(texts[index], limit)
			costs[index] = len([]rune(texts[index]))
			break
		}
	}
	for sumRunes(costs)+separators > limit && len(texts) > 1 {
		texts = texts[:len(texts)-1]
		costs = costs[:len(costs)-1]
		separators = len(texts) - 1
	}
	if len(texts) == 1 && costs[0] > limit {
		texts[0] = truncateToolResultMiddle(texts[0], limit)
	}
	compacted := strings.Join(texts, "\n")
	record := omissionRecord(len(lines))
	if len([]rune(compacted))+1+len([]rune(record)) <= limit {
		compacted += "\n" + record
	}
	runes := []rune(compacted)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return compacted
}

func sumRunes(costs []int) int {
	total := 0
	for _, cost := range costs {
		total += cost
	}
	return total
}

// minShrinkableLine is the size below which a line is not worth cutting again.
const minShrinkableLine = 64

// omissionRecord is the pointer a folded result keeps to its original text.
func omissionRecord(dropped int) string {
	return fmt.Sprintf("... (%d lines omitted for context; the full tool output stays in the session transcript) ...", dropped)
}

// cheapestFirst orders lines by redundancy score, then by original position so
// the result is deterministic.
func cheapestFirst(lines []toolLine) []toolLine {
	ordered := make([]toolLine, len(lines))
	copy(ordered, lines)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score < ordered[j].score
		}
		return ordered[i].index < ordered[j].index
	})
	return ordered
}

// truncateToolResultMiddle keeps the head and tail of an oversized block. It is
// only reached when line-level dropping cannot meet the budget, so the marker
// stays explicit about what was cut.
func truncateToolResultMiddle(out string, limit int) string {
	runes := []rune(out)
	if limit <= 0 || len(runes) <= limit {
		return out
	}
	marker := fmt.Sprintf(
		"\n\n... (middle omitted for context: %d chars; do not rely on omitted details without another tool check) ...\n\n",
		len(runes)-limit,
	)
	markerRunes := []rune(marker)
	if len(markerRunes) >= limit {
		return string(runes[:limit])
	}
	room := limit - len(markerRunes)
	head := room * 2 / 3
	tail := room - head
	return string(runes[:head]) + marker + string(runes[len(runes)-tail:])
}
