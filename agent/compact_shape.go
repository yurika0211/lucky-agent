package agent

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Shape compaction rewrites a tool result whose redundancy lives in the line
// format, not between lines. A diff repeats "+" on every change, grep repeats
// the full path on every hit, and ls -l repeats permissions and dates. Line
// scoring in compressToolResultLines keeps those lines because each one looks
// like evidence. Rewriting the shape first keeps the evidence and drops the
// repeated format.
//
// The rules follow the RTK filters in Ebony-Vinyl/dsh-our-free-model
// (src/token-saver/rtk). This is a deterministic rewrite, not a summary and
// not a conditional-entropy rank. Failure stays open: a rewrite that is empty,
// longer, or unrecognised returns the original text.

const (
	shapeMinBytes       = 500
	shapeMaxBytes       = 10 * 1024 * 1024
	shapeDetectWindow   = 1024
	shapeDiffMaxLines   = 500
	shapeDiffHunkMax    = 100
	shapeLogMaxLines    = 200
	shapeDedupMaxLines  = 2000
	shapeGrepPerFile    = 10
	shapeFindPerDir     = 10
	shapeFindDirMax     = 20
	shapeStatusMaxFiles = 10
	shapeTreeMaxLines   = 200
	shapeReadMinLines   = 250
	shapeReadHead       = 120
	shapeReadTail       = 60
	shapeReadHitRatio   = 0.70
	shapePorcelainRatio = 0.60
	shapeExtSummaryTop  = 5
	shapeWarnKeep       = 5
	shapeDeprecKeep     = 3
)

const (
	shapeNone          = ""
	shapeGitLog        = "git-log"
	shapeGitDiff       = "git-diff"
	shapeGitStatus     = "git-status"
	shapeBuild         = "build"
	shapePorcelainName = "git-porcelain"
	shapeGrep          = "grep"
	shapeFind          = "find"
	shapeTree          = "tree"
	shapeLs            = "ls"
	shapeReadNumber    = "read-numbered"
	shapeDedup         = "dedup"
	shapeTruncate      = "truncate"
)

// shapeSkip records why a result was not rewritten. Empty means a rewrite ran.
const (
	shapeSkipSmall      = "too-small"
	shapeSkipLarge      = "too-large"
	shapeSkipError      = "tool-error"
	shapeSkipUnknown    = "unrecognised"
	shapeSkipNotShorter = "not-shorter"
	shapeSkipEmpty      = "empty"
)

var (
	shapeCommitHeader = regexp.MustCompile(`(?i)^(?:[*|/\\ ]+)?commit [0-9a-f]{7,40}\b`)
	shapeDiffHeader   = regexp.MustCompile(`(?m)^diff --git |^@@ `)
	shapeStatusLong   = regexp.MustCompile(`(?m)^On branch |^nothing to commit|^Changes `)
	shapeBuildHeader  = regexp.MustCompile(`(?im)^[ \t]*(?:npm (?:warn|error)|yarn (?:warn|error)|Compiling |Downloading |added \d+ package|\[ERROR\]|BUILD (?:SUCCESS|FAILED)|Finished |Successfully (?:installed|built)|ERROR:)`)
	shapePorcelain    = regexp.MustCompile(`^[ MADRCU?!][ MADRCU?!] `)
	shapeGrepLine     = regexp.MustCompile(`^([^:\n]+):(\d+):(.*)$`)
	shapePathLine     = regexp.MustCompile(`^(?:\.{1,2}/|/(?:[^\s:]|$)|[A-Za-z]:\\)`)
	shapeLsTotal      = regexp.MustCompile(`(?m)^total \d+`)
	shapeLsPerm       = regexp.MustCompile(`^[dlcbps-][rwxstST-]{9}\s`)
	shapeLsDate       = regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(\d{4}|\d{2}:\d{2})\s+`)
	shapeNumberedLine = regexp.MustCompile(`^\s*\d+\|`)
	shapeTreeMark     = regexp.MustCompile(`[├└│]`)
	shapeCargoCont    = regexp.MustCompile(`^\s*(?:-->|\||\d+\s*\||=)`)
	shapeErrorLine    = regexp.MustCompile(`(?i)^(?:npm (?:ERR!|error)|yarn error|error(?:\[|:)|error -->|ERROR:|\[ERROR\]|BUILD FAILED)`)
	shapeWarnLine     = regexp.MustCompile(`(?i)^(?:npm warn|yarn warn|warning(?:\[|:)|warning -->|\[WARNING\])`)
	shapeDeprecLine   = regexp.MustCompile(`(?i)^npm warn deprecated`)
)

var shapeNoiseDirs = map[string]bool{
	"node_modules": true, ".git": true, "target": true, "__pycache__": true,
	".next": true, "dist": true, "build": true, ".cache": true, ".turbo": true,
	".vercel": true, ".pytest_cache": true, ".mypy_cache": true, ".tox": true,
	".venv": true, "venv": true, "env": true, "coverage": true, ".nyc_output": true,
	".DS_Store": true, "Thumbs.db": true, ".idea": true, ".vscode": true, ".vs": true,
}

// shapeRewriteResult is one attempt to rewrite a tool result.
type shapeRewriteResult struct {
	Text       string
	Filter     string
	Skip       string
	BytesIn    int
	BytesOut   int
	Idempotent bool
}

// rewriteToolResultShape recognises a tool-output shape and rewrites it.
// Error results, tiny results, and huge dumps pass through. A rewrite that is
// not strictly shorter is discarded so the caller keeps the original evidence.
func rewriteToolResultShape(text string, toolError bool) shapeRewriteResult {
	in := len(text)
	out := shapeRewriteResult{Text: text, BytesIn: in, BytesOut: in}
	if toolError || strings.HasPrefix(strings.TrimSpace(text), "[Tool Error:") {
		out.Skip = shapeSkipError
		return out
	}
	if in < shapeMinBytes {
		out.Skip = shapeSkipSmall
		return out
	}
	if in > shapeMaxBytes {
		out.Skip = shapeSkipLarge
		return out
	}
	filter := detectToolResultShape(text)
	if filter == shapeNone {
		out.Skip = shapeSkipUnknown
		return out
	}
	rewritten := applyToolResultShape(filter, text)
	if strings.TrimSpace(rewritten) == "" {
		out.Skip = shapeSkipEmpty
		return out
	}
	if len(rewritten) >= in {
		out.Skip = shapeSkipNotShorter
		return out
	}
	out.Text = rewritten
	out.Filter = filter
	out.BytesOut = len(rewritten)
	again := applyToolResultShape(detectToolResultShape(rewritten), rewritten)
	out.Idempotent = again == rewritten || len(again) >= len(rewritten)
	return out
}

// detectToolResultShape classifies the first 1024 characters. Order is load
// bearing: a build log contains lines a porcelain detector would claim, and a
// path list must not steal a grep listing.
func detectToolResultShape(text string) string {
	window := text
	if len(window) > shapeDetectWindow {
		window = window[:shapeDetectWindow]
	}
	switch {
	case shapeCommitHeader.MatchString(window):
		return shapeGitLog
	case shapeDiffHeader.MatchString(window):
		return shapeGitDiff
	case shapeStatusLong.MatchString(window):
		return shapeGitStatus
	case shapeBuildHeader.MatchString(window):
		return shapeBuild
	}
	// A build log can contain two-column-looking lines. Only an actual git
	// header may take the porcelain path; otherwise cargo output is counted as
	// a clean status.
	if strings.Contains(window, "## ") && porcelainRatio(window) >= shapePorcelainRatio {
		return shapePorcelainName
	}
	if looksLikeGrep(text) {
		return shapeGrep
	}
	if looksLikeFind(text) {
		return shapeFind
	}
	// A rewrite labels itself. Running dedup or truncation over that label would
	// make the second pass shorter than the first, so the result would not be
	// idempotent.
	if strings.HasPrefix(text, "[git ") || strings.HasPrefix(text, "[build]") || strings.HasPrefix(text, "[grep]") || strings.HasPrefix(text, "[find]") || strings.HasPrefix(text, "[ls]") {
		return shapeNone
	}
	if shapeTreeMark.MatchString(window) {
		return shapeTree
	}
	if looksLikeLs(window) {
		return shapeLs
	}
	if looksLikeNumberedRead(text) {
		return shapeReadNumber
	}
	nonEmpty := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			nonEmpty++
		}
	}
	if nonEmpty >= 5 {
		return shapeDedup
	}
	if strings.Count(text, "\n")+1 >= shapeReadMinLines {
		return shapeTruncate
	}
	return shapeNone
}

func applyToolResultShape(filter, text string) string {
	switch filter {
	case shapeGitLog:
		return shapeGitLogRewrite(text)
	case shapeGitDiff:
		return shapeGitDiffRewrite(text)
	case shapeGitStatus, shapePorcelainName:
		return shapeGitStatusRewrite(text)
	case shapeBuild:
		return shapeBuildRewrite(text)
	case shapeGrep:
		return shapeGrepRewrite(text)
	case shapeFind:
		return shapeFindRewrite(text)
	case shapeTree:
		return shapeCapLines(text, shapeTreeMaxLines, "... +%d tree lines truncated")
	case shapeLs:
		return shapeLsRewrite(text)
	case shapeReadNumber:
		return shapeNumberedRewrite(text)
	case shapeDedup:
		return shapeDedupRewrite(text)
	case shapeTruncate:
		return shapeHeadTail(text, "... +%d lines truncated")
	default:
		return text
	}
}

func porcelainRatio(window string) float64 {
	lines := strings.Split(window, "\n")
	nonEmpty := 0
	hits := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		nonEmpty++
		if strings.HasPrefix(line, "## ") || shapePorcelain.MatchString(line) {
			hits++
		}
	}
	if nonEmpty == 0 {
		return 0
	}
	return float64(hits) / float64(nonEmpty)
}

func looksLikeGrep(text string) bool {
	lines := nonEmptyLines(text)
	if len(lines) < 3 {
		return false
	}
	probed := 0
	for _, line := range lines {
		if probed >= 5 {
			break
		}
		probed++
		if !shapeGrepLine.MatchString(line) {
			return false
		}
	}
	for _, line := range lines {
		if !shapeGrepLine.MatchString(line) {
			return false
		}
	}
	return true
}

func looksLikeFind(text string) bool {
	lines := nonEmptyLines(text)
	if len(lines) < 3 {
		return false
	}
	for _, line := range lines {
		if strings.Contains(line, ":") || !shapePathLine.MatchString(line) {
			return false
		}
	}
	return true
}

func looksLikeLs(window string) bool {
	if shapeLsTotal.MatchString(window) {
		return true
	}
	hits := 0
	for _, line := range strings.Split(window, "\n") {
		if shapeLsPerm.MatchString(line) {
			hits++
		}
	}
	return hits >= 3
}

func looksLikeNumberedRead(text string) bool {
	lines := strings.Split(text, "\n")
	if len(lines) < shapeReadMinLines {
		return false
	}
	checked := 0
	hits := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		checked++
		if shapeNumberedLine.MatchString(line) {
			hits++
		}
		if checked >= 100 {
			break
		}
	}
	if checked == 0 {
		return false
	}
	return float64(hits)/float64(checked) >= shapeReadHitRatio
}

func shapeGitDiffRewrite(diff string) string {
	lines := strings.Split(diff, "\n")
	result := make([]string, 0, 64)
	current := ""
	added, removed := 0, 0
	inHunk := false
	hunkShown, hunkSkipped := 0, 0
	truncated := false
	flushSkipped := func() {
		if hunkSkipped > 0 {
			result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
			truncated = true
			hunkSkipped = 0
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git") {
			flushSkipped()
			if current != "" && (added > 0 || removed > 0) {
				result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
			}
			parts := strings.Split(line, " b/")
			if len(parts) > 1 {
				current = strings.Join(parts[1:], " b/")
			} else {
				current = "unknown"
			}
			result = append(result, "\n"+current)
			added, removed, hunkShown = 0, 0, 0
			inHunk = false
		} else if strings.HasPrefix(line, "@@") {
			flushSkipped()
			inHunk = true
			hunkShown = 0
			result = append(result, "  "+line)
		} else if inHunk {
			change := false
			if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				added++
				change = true
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				removed++
				change = true
			}
			if change || (hunkShown > 0 && hunkShown < shapeDiffHunkMax && !strings.HasPrefix(line, "\\")) {
				if change && hunkShown >= shapeDiffHunkMax {
					hunkSkipped++
				} else if hunkShown < shapeDiffHunkMax && (change || hunkShown > 0) {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}
		if len(result) >= shapeDiffMaxLines {
			result = append(result, "\n... (more changes truncated)")
			truncated = true
			break
		}
	}
	flushSkipped()
	if current != "" && (added > 0 || removed > 0) {
		result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
	}
	if truncated {
		result = append(result, "[full diff stays in the session transcript]")
	}
	rewritten := strings.TrimRight(strings.Join(result, "\n"), "\n")
	// The rewrite is not itself a diff. Prefixing it keeps the second pass from
	// detecting the same shape and wrapping the counts again.
	if !strings.HasPrefix(rewritten, "[git diff]") {
		rewritten = "[git diff]\n" + rewritten
	}
	return rewritten
}

func shapeGitStatusRewrite(text string) string {
	branch := ""
	var staged, modified, untracked []string
	conflicts := 0
	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		if name, ok := strings.CutPrefix(trimmed, "On branch "); ok {
			branch = strings.Fields(name)[0]
			continue
		}
		if strings.HasPrefix(raw, "## ") {
			branch = strings.TrimPrefix(raw, "## ")
			continue
		}
		if len(raw) >= 3 && shapePorcelain.MatchString(raw) {
			x, y, file := raw[0], raw[1], raw[3:]
			switch raw[:2] {
			case "??":
				untracked = append(untracked, file)
				continue
			}
			if strings.ContainsRune("MADRC", rune(x)) {
				staged = append(staged, file)
			} else if x == 'U' || y == 'U' {
				conflicts++
			}
			if y == 'M' || y == 'D' {
				modified = append(modified, file)
			}
			continue
		}
		if kind, path, ok := cutStatusLong(trimmed); ok {
			switch kind {
			case "both modified":
				conflicts++
			case "modified", "deleted":
				modified = append(modified, path)
			case "new file", "renamed":
				staged = append(staged, path)
			}
		}
	}
	var b strings.Builder
	b.WriteString("[git status]\n")
	if branch != "" {
		fmt.Fprintf(&b, "* %s\n", branch)
	}
	writeStatusSection(&b, "+ Staged", staged, shapeStatusMaxFiles)
	writeStatusSection(&b, "~ Modified", modified, shapeStatusMaxFiles)
	writeStatusSection(&b, "? Untracked", untracked, shapeStatusMaxFiles)
	if conflicts > 0 {
		fmt.Fprintf(&b, "conflicts: %d files\n", conflicts)
	}
	if len(staged)+len(modified)+len(untracked)+conflicts == 0 {
		b.WriteString("clean — nothing to commit\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func cutStatusLong(line string) (string, string, bool) {
	for _, kind := range []string{"both modified", "new file", "modified", "deleted", "renamed"} {
		prefix := kind + ":"
		if strings.HasPrefix(line, prefix) {
			return kind, strings.TrimSpace(strings.TrimPrefix(line, prefix)), true
		}
	}
	return "", "", false
}

func writeStatusSection(b *strings.Builder, label string, files []string, limit int) {
	if len(files) == 0 {
		return
	}
	fmt.Fprintf(b, "%s: %d files\n", label, len(files))
	shown := files
	if len(shown) > limit {
		shown = shown[:limit]
	}
	for _, file := range shown {
		fmt.Fprintf(b, "   %s\n", file)
	}
	if len(files) > limit {
		fmt.Fprintf(b, "   ... +%d more\n", len(files)-limit)
	}
}

func shapeGitLogRewrite(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, 32)
	skipped := 0
	inCommit := false
	subjectSeen := false
	push := func(line string) {
		if len(out) < shapeLogMaxLines {
			out = append(out, line)
			return
		}
		skipped++
	}
	for _, raw := range lines {
		line := strings.TrimRight(raw, " \t")
		trimmed := strings.TrimSpace(line)
		if shapeCommitHeader.MatchString(trimmed) {
			inCommit = true
			subjectSeen = false
			push(line)
			continue
		}
		if inCommit {
			if strings.HasPrefix(trimmed, "Author:") || strings.HasPrefix(trimmed, "Date:") {
				push(trimmed)
				continue
			}
			if trimmed == "" {
				continue
			}
			if !subjectSeen && strings.HasPrefix(line, "    ") {
				push("  Subject: " + trimmed)
				subjectSeen = true
				continue
			}
			if strings.Contains(trimmed, "file") && strings.Contains(trimmed, "changed") {
				push("  " + trimmed)
				continue
			}
			if strings.HasPrefix(trimmed, "diff --git ") {
				push("  ... diff body omitted")
			}
			continue
		}
		push(trimmed)
	}
	if skipped > 0 {
		out = append(out, fmt.Sprintf("... (%d more lines)", skipped))
	}
	return strings.Join(out, "\n")
}

func shapeGrepRewrite(text string) string {
	byFile := map[string][]string{}
	order := make([]string, 0)
	total := 0
	for _, line := range nonEmptyLines(text) {
		match := shapeGrepLine.FindStringSubmatch(line)
		if match == nil {
			return text
		}
		file, content := match[1], strings.TrimSpace(match[3])
		if _, ok := byFile[file]; !ok {
			order = append(order, file)
			byFile[file] = nil
		}
		byFile[file] = append(byFile[file], fmt.Sprintf("%s: %s", match[2], content))
		total++
	}
	sort.Strings(order)
	var b strings.Builder
	fmt.Fprintf(&b, "[grep]\n%d matches in %dF:\n\n", total, len(order))
	for _, file := range order {
		hits := byFile[file]
		fmt.Fprintf(&b, "[file] %s (%d):\n", file, len(hits))
		shown := hits
		if len(shown) > shapeGrepPerFile {
			shown = shown[:shapeGrepPerFile]
		}
		for _, hit := range shown {
			fmt.Fprintf(&b, "  %s\n", hit)
		}
		if len(hits) > shapeGrepPerFile {
			fmt.Fprintf(&b, "  +%d\n", len(hits)-shapeGrepPerFile)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func shapeFindRewrite(text string) string {
	byDir := map[string][]string{}
	dirs := make([]string, 0)
	total := 0
	for _, path := range nonEmptyLines(text) {
		dir, base := splitPath(path)
		if _, ok := byDir[dir]; !ok {
			dirs = append(dirs, dir)
			byDir[dir] = nil
		}
		byDir[dir] = append(byDir[dir], base)
		total++
	}
	sort.Strings(dirs)
	var b strings.Builder
	fmt.Fprintf(&b, "[find]\n%d files in %d dirs:\n\n", total, len(dirs))
	shownDirs := dirs
	if len(shownDirs) > shapeFindDirMax {
		shownDirs = shownDirs[:shapeFindDirMax]
	}
	for _, dir := range shownDirs {
		files := byDir[dir]
		fmt.Fprintf(&b, "%s/  (%d)\n", strings.ReplaceAll(dir, "\\", "/"), len(files))
		shown := files
		if len(shown) > shapeFindPerDir {
			shown = shown[:shapeFindPerDir]
		}
		for _, file := range shown {
			fmt.Fprintf(&b, "  %s\n", file)
		}
		if len(files) > shapeFindPerDir {
			fmt.Fprintf(&b, "  +%d\n", len(files)-shapeFindPerDir)
		}
	}
	if len(dirs) > shapeFindDirMax {
		fmt.Fprintf(&b, "\n+%d more dirs\n", len(dirs)-shapeFindDirMax)
	}
	return strings.TrimRight(b.String(), "\n")
}

func shapeLsRewrite(text string) string {
	var dirs []string
	type namedSize struct{ name, size string }
	var files []namedSize
	exts := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		parsed, ok := parseLsLine(line)
		if !ok || parsed.name == "." || parsed.name == ".." {
			continue
		}
		if shapeNoiseDirs[parsed.name] {
			dirs = append(dirs, parsed.name)
			continue
		}
		switch parsed.kind {
		case "d":
			dirs = append(dirs, parsed.name)
		case "-", "l":
			ext := "no ext"
			if dot := strings.LastIndex(parsed.name, "."); dot > 0 {
				ext = parsed.name[dot:]
			}
			exts[ext]++
			files = append(files, namedSize{parsed.name, humanBytes(parsed.size)})
		}
	}
	if len(dirs) == 0 && len(files) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString("[ls]\n")
	for _, dir := range dirs {
		fmt.Fprintf(&b, "%s/\n", dir)
	}
	for _, file := range files {
		fmt.Fprintf(&b, "%s  %s\n", file.name, file.size)
	}
	fmt.Fprintf(&b, "\nSummary: %d files, %d dirs", len(files), len(dirs))
	if len(exts) > 0 {
		type pair struct {
			ext   string
			count int
		}
		ranked := make([]pair, 0, len(exts))
		for ext, count := range exts {
			ranked = append(ranked, pair{ext, count})
		}
		sort.SliceStable(ranked, func(i, j int) bool {
			if ranked[i].count != ranked[j].count {
				return ranked[i].count > ranked[j].count
			}
			return ranked[i].ext < ranked[j].ext
		})
		limit := shapeExtSummaryTop
		if len(ranked) < limit {
			limit = len(ranked)
		}
		parts := make([]string, 0, limit)
		for _, item := range ranked[:limit] {
			parts = append(parts, fmt.Sprintf("%d %s", item.count, item.ext))
		}
		fmt.Fprintf(&b, " (%s", strings.Join(parts, ", "))
		if len(ranked) > shapeExtSummaryTop {
			fmt.Fprintf(&b, ", +%d more", len(ranked)-shapeExtSummaryTop)
		}
		b.WriteByte(')')
	}
	return b.String()
}

type lsEntry struct {
	kind string
	size int
	name string
}

func parseLsLine(line string) (lsEntry, bool) {
	loc := shapeLsDate.FindStringIndex(line)
	if loc == nil {
		return lsEntry{}, false
	}
	name := line[loc[1]:]
	before := strings.Fields(line[:loc[0]])
	if len(before) < 4 || name == "" {
		return lsEntry{}, false
	}
	size := 0
	for i := len(before) - 1; i >= 0; i-- {
		n, ok := parseInt(before[i])
		if ok {
			size = n
			break
		}
	}
	return lsEntry{kind: before[0][:1], size: size, name: name}, true
}

func shapeBuildRewrite(text string) string {
	var errors, warnings, deprecations []string
	var summary []string
	compiling, downloading := 0, 0
	inError := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if inError {
			if trimmed == "" {
				inError = false
				continue
			}
			if shapeCargoCont.MatchString(line) {
				errors = append(errors, line)
				continue
			}
			inError = false
		}
		if trimmed == "" {
			continue
		}
		switch {
		case shapeDeprecLine.MatchString(trimmed):
			deprecations = append(deprecations, line)
		case shapeErrorLine.MatchString(trimmed):
			errors = append(errors, line)
			inError = true
		case shapeWarnLine.MatchString(trimmed):
			warnings = append(warnings, line)
			inError = true
		case strings.HasPrefix(strings.ToLower(trimmed), "compiling "):
			compiling++
		case strings.HasPrefix(strings.ToLower(trimmed), "downloading ") || strings.HasPrefix(trimmed, "Fetching "):
			downloading++
		case isBuildSummary(trimmed):
			summary = append(summary, line)
		}
	}
	var b strings.Builder
	b.WriteString("[build]\n")
	keepDep := deprecations
	if len(keepDep) > shapeDeprecKeep {
		keepDep = keepDep[:shapeDeprecKeep]
	}
	for _, line := range keepDep {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if len(deprecations) > shapeDeprecKeep {
		fmt.Fprintf(&b, "... +%d more deprecated packages\n", len(deprecations)-shapeDeprecKeep)
	}
	if compiling > 0 {
		fmt.Fprintf(&b, "Compiled %d packages\n", compiling)
	}
	if downloading > 0 {
		fmt.Fprintf(&b, "Downloaded %d packages\n", downloading)
	}
	for _, line := range errors {
		fmt.Fprintf(&b, "%s\n", line)
	}
	keepWarn := warnings
	if len(keepWarn) > shapeWarnKeep {
		keepWarn = keepWarn[:shapeWarnKeep]
	}
	for _, line := range keepWarn {
		fmt.Fprintf(&b, "%s\n", line)
	}
	if len(warnings) > shapeWarnKeep {
		fmt.Fprintf(&b, "... +%d more warnings\n", len(warnings)-shapeWarnKeep)
	}
	for _, line := range summary {
		fmt.Fprintf(&b, "%s\n", line)
	}
	rewritten := strings.TrimRight(b.String(), "\n")
	if rewritten == "" {
		return text
	}
	return rewritten
}

func isBuildSummary(line string) bool {
	switch {
	case strings.HasPrefix(line, "Finished "),
		strings.HasPrefix(line, "BUILD SUCCESS"),
		strings.HasPrefix(line, "Successfully installed"),
		strings.HasPrefix(line, "Successfully built"),
		strings.HasPrefix(line, "added "),
		strings.HasPrefix(line, "removed "),
		strings.HasPrefix(line, "audited "):
		return true
	default:
		return false
	}
}

func shapeNumberedRewrite(text string) string {
	return shapeHeadTail(text, "... +%d lines truncated (file continues)")
}

func shapeDedupRewrite(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	blankKept := false
	prev := ""
	havePrev := false
	dropped := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blankKept {
				dropped++
				continue
			}
			blankKept = true
		} else {
			blankKept = false
		}
		if havePrev && line == prev {
			dropped++
			continue
		}
		if len(out) >= shapeDedupMaxLines {
			dropped += len(lines) - (len(out) + dropped)
			break
		}
		out = append(out, line)
		prev = line
		havePrev = true
	}
	if dropped > 0 {
		out = append(out, fmt.Sprintf("... (%d repeated lines folded)", dropped))
	}
	return strings.Join(out, "\n")
}

func shapeHeadTail(text, marker string) string {
	lines := strings.Split(text, "\n")
	if len(lines) < shapeReadMinLines {
		return text
	}
	head := lines[:shapeReadHead]
	tail := lines[len(lines)-shapeReadTail:]
	cut := len(lines) - len(head) - len(tail)
	if cut <= 0 {
		return text
	}
	out := append(append([]string{}, head...), fmt.Sprintf(marker, cut))
	out = append(out, tail...)
	return strings.Join(out, "\n")
}

func shapeCapLines(text string, limit int, marker string) string {
	lines := strings.Split(text, "\n")
	if len(lines) <= limit {
		return text
	}
	kept := append([]string{}, lines[:limit]...)
	kept = append(kept, fmt.Sprintf(marker, len(lines)-limit))
	return strings.Join(kept, "\n")
}

func nonEmptyLines(text string) []string {
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func splitPath(path string) (string, string) {
	slash := strings.LastIndexAny(path, `/\`)
	if slash < 0 {
		return ".", path
	}
	dir := path[:slash]
	if dir == "" {
		dir = "/"
	}
	return dir, path[slash+1:]
}

func humanBytes(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%.1fM", float64(size)/float64(1<<20))
	case size >= 1<<10:
		return fmt.Sprintf("%.1fK", float64(size)/float64(1<<10))
	default:
		return fmt.Sprintf("%dB", size)
	}
}

func parseInt(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// shapeBytes is the size the A/B ledger uses. Invalid UTF-8 still counts.
func shapeBytes(text string) int {
	if utf8.ValidString(text) {
		return len(text)
	}
	return len(text)
}
