package tool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// maxScanFileBytes bounds how much of a file the content rules read. Anything
// larger is treated as a blob and only contributes to the digest.
const maxScanFileBytes = 1 << 20

type contentRule struct {
	name     string
	severity string
	re       *regexp.Regexp
	detail   string
}

// secretRules look for credentials committed into a skill. A private key is a
// hard block; the rest are warnings by default because a README documenting an
// API key format is a common false positive.
var secretRules = []contentRule{
	{"private_key", SeverityBlock, regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`), "private key material"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`sk-ant-[A-Za-z0-9_\-]{20,}`), "Anthropic API key"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`), "OpenAI-style API key"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "AWS access key id"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`), "GitHub personal access token"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`github_pat_[A-Za-z0-9_]{50,}`), "GitHub fine-grained token"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), "Slack token"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`AIza[0-9A-Za-z_\-]{35}`), "Google API key"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.`), "JWT"},
	{"secret_like", SeverityWarn, regexp.MustCompile(`(?i)(api[_\-]?key|secret|passwd|password|token)\s*[:=]\s*["'][^"']{16,}["']`), "hardcoded credential literal"},
}

// dangerRules flag script behavior a reviewer should see. Only the handful that
// have no legitimate use in a skill are blocks; the rest are warnings shown
// prominently in the install report.
var dangerRules = []contentRule{
	{"remote_code_execution", SeverityBlock, regexp.MustCompile(`(?i)\bcurl\b[^|\n]*\|\s*(?:sudo\s+)?(?:ba)?sh`), "pipes a download straight into a shell"},
	{"remote_code_execution", SeverityBlock, regexp.MustCompile(`(?i)\bwget\b[^|\n]*\|\s*(?:sudo\s+)?(?:ba)?sh`), "pipes a download straight into a shell"},
	{"remote_code_execution", SeverityBlock, regexp.MustCompile(`base64\s+(?:-d|--decode)[^|\n]*\|\s*(?:ba)?sh`), "pipes decoded data into a shell"},
	{"destructive", SeverityBlock, regexp.MustCompile(`\brm\s+-[a-zA-Z]*[rR][a-zA-Z]*f?\s+(?:/|~|\$HOME)(?:\s|$)`), "recursive delete of a root or home path"},
	{"fork_bomb", SeverityBlock, regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\}\s*;\s*:`), "fork bomb"},

	{"privilege", SeverityWarn, regexp.MustCompile(`(?m)^\s*sudo\s`), "invokes sudo"},
	{"permissions", SeverityWarn, regexp.MustCompile(`chmod\s+(?:-R\s+)?777`), "world-writable chmod"},
	{"persistence", SeverityWarn, regexp.MustCompile(`\bcrontab\b`), "modifies cron"},
	{"persistence", SeverityWarn, regexp.MustCompile(`>>?\s*~?/?\.(?:bash|zsh|prof)[a-z]*rc\b`), "writes to a shell rc file"},
	{"credential_access", SeverityWarn, regexp.MustCompile(`\.ssh/(?:authorized_keys|id_[a-z0-9]+)`), "touches SSH key material"},
	{"credential_access", SeverityWarn, regexp.MustCompile(`\.luckyagent/(?:config\.json|tokens)`), "reads or writes LuckyAgent credentials"},
	{"network", SeverityWarn, regexp.MustCompile(`\bnc\s+-[a-zA-Z]*e`), "netcat with command execution"},
	{"network", SeverityWarn, regexp.MustCompile(`/dev/tcp/`), "raw TCP via /dev/tcp"},
	{"dynamic_exec", SeverityWarn, regexp.MustCompile(`subprocess\.[A-Za-z_]+\([^)]*shell\s*=\s*True`), "subprocess with shell=True"},
	{"dynamic_exec", SeverityWarn, regexp.MustCompile(`\bos\.system\(`), "os.system call"},
	{"dynamic_exec", SeverityWarn, regexp.MustCompile(`(?m)(?:^|[^.\w])eval\s*\(`), "eval call"},
	{"dynamic_exec", SeverityWarn, regexp.MustCompile(`(?m)(?:^|[^.\w])exec\s*\(`), "exec call"},
	{"dynamic_exec", SeverityWarn, regexp.MustCompile(`\bchild_process\b`), "Node child_process"},
	{"obfuscation", SeverityWarn, regexp.MustCompile(`\batob\s*\(`), "base64 decode of embedded data"},
	{"deserialization", SeverityWarn, regexp.MustCompile(`\b(?:pickle|marshal)\.loads\s*\(`), "unsafe deserialization"},
}

// scannedExtensions are the files the content rules read. SKILL.md is included
// because its fenced code blocks are copy-paste instructions for the model.
var scannedExtensions = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true,
	".py": true, ".js": true, ".mjs": true, ".ts": true, ".go": true,
	".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true,
}

// strippedDirs are removed from the staged tree and reported as warnings. They
// are never part of a skill and inflate every limit.
var strippedDirs = map[string]string{
	".git":         "vcs_dir",
	".svn":         "vcs_dir",
	".hg":          "vcs_dir",
	"node_modules": "vendored_deps",
	"__pycache__":  "vendored_deps",
	".venv":        "vendored_deps",
	"venv":         "vendored_deps",
}

// ScanSkillDir runs every static rule over a staged skill directory. It executes
// nothing and makes no network calls, which is what lets it run before the trial
// load. Directories on the strip list are deleted as a side effect.
func ScanSkillDir(skillDir string, limits InstallLimits) (*ScanReport, error) {
	limits = limits.withDefaults()
	report := &ScanReport{ScannedAt: time.Now()}

	if err := stripJunkDirs(skillDir, report); err != nil {
		return nil, err
	}

	// SKILL.md is the one required file; without it the loader has nothing.
	skillMDCount := 0
	type digestEntry struct {
		rel  string
		mode os.FileMode
		sum  string
	}
	var digestEntries []digestEntry

	err := filepath.WalkDir(skillDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(skillDir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)

		// Re-check for symlinks on the materialized tree. The extractor rejects
		// them per entry, but this catches a bug or a race in that path.
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			report.add("symlink_entry", SeverityBlock, rel, "symlink found in the staged tree", 0)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			report.add("special_file", SeverityBlock, rel, "non-regular file in the staged tree", 0)
			return nil
		}

		report.Files++
		report.Bytes += info.Size()
		if filepath.Base(rel) == "SKILL.md" {
			skillMDCount++
		}

		sum, err := hashFile(p)
		if err != nil {
			return err
		}
		digestEntries = append(digestEntries, digestEntry{rel: rel, mode: info.Mode().Perm(), sum: sum})

		scanFileContent(p, rel, info.Size(), limits, report)
		return nil
	})
	if err != nil {
		return nil, err
	}

	switch skillMDCount {
	case 0:
		report.add("missing_skill_md", SeverityBlock, "", "no SKILL.md in the skill directory", 0)
	case 1:
	default:
		report.add("multiple_skill_md", SeverityBlock, "", fmt.Sprintf("%d SKILL.md files found; exactly one is expected", skillMDCount), 0)
	}

	// Content-addressed identity: stable across re-uploads of identical trees.
	sort.Slice(digestEntries, func(i, j int) bool { return digestEntries[i].rel < digestEntries[j].rel })
	h := sha256.New()
	for _, e := range digestEntries {
		fmt.Fprintf(h, "%s\x00%o\x00%s\n", e.rel, e.mode, e.sum)
	}
	report.Digest = "sha256:" + hex.EncodeToString(h.Sum(nil))

	return report, nil
}

func stripJunkDirs(root string, report *ScanReport) error {
	var doomed []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || p == root {
			return nil
		}
		if rule, ok := strippedDirs[d.Name()]; ok {
			rel, _ := filepath.Rel(root, p)
			report.add(rule, SeverityWarn, filepath.ToSlash(rel), "removed from the installed skill", 0)
			doomed = append(doomed, p)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, p := range doomed {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}

func scanFileContent(p, rel string, size int64, limits InstallLimits, report *ScanReport) {
	if !scannedExtensions[strings.ToLower(filepath.Ext(rel))] {
		return
	}
	if size > maxScanFileBytes {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return
	}
	// Binary sniff: a NUL byte means this is not source we can reason about.
	if bytes.IndexByte(data, 0) >= 0 {
		return
	}

	for _, rule := range secretRules {
		severity := rule.severity
		if limits.BlockOnSecrets && severity == SeverityWarn {
			severity = SeverityBlock
		}
		reportMatches(report, data, rule, severity, rel, true)
	}

	// Danger rules only apply to executable content and to SKILL.md, whose fenced
	// blocks are instructions the model may follow.
	ext := strings.ToLower(filepath.Ext(rel))
	isCode := ext == ".sh" || ext == ".bash" || ext == ".zsh" || ext == ".py" ||
		ext == ".js" || ext == ".mjs" || ext == ".ts" || ext == ".go"
	if !isCode && filepath.Base(rel) != "SKILL.md" {
		return
	}
	for _, rule := range dangerRules {
		reportMatches(report, data, rule, rule.severity, rel, false)
	}
}

// reportMatches records at most one finding per rule per file to keep the report
// readable; the line number points at the first hit.
func reportMatches(report *ScanReport, data []byte, rule contentRule, severity, rel string, redact bool) {
	loc := rule.re.FindIndex(data)
	if loc == nil {
		return
	}
	line := bytes.Count(data[:loc[0]], []byte{'\n'}) + 1
	detail := rule.detail
	if redact {
		// Show just enough of the match to locate it, never the whole secret.
		snippet := string(data[loc[0]:loc[1]])
		if len(snippet) > 4 {
			snippet = snippet[:4]
		}
		detail = fmt.Sprintf("%s (%s…)", rule.detail, snippet)
	}
	report.add(rule.name, severity, rel, detail, line)
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
