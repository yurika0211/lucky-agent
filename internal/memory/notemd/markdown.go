// Package notemd parses the small Markdown subset shared by Aestus notes.
package notemd

import (
	"regexp"
	"strings"
)

// BlockIDPattern matches a trailing Obsidian block id.
var BlockIDPattern = regexp.MustCompile(`(?m)(?:\s+\^[A-Za-z0-9_-]+|\n\^[A-Za-z0-9_-]+\s*)$`)

// SplitFrontmatter separates a leading YAML frontmatter block from the body.
func SplitFrontmatter(md string) (string, string, bool) {
	md = strings.TrimPrefix(md, "\ufeff")
	if !strings.HasPrefix(md, "---\n") {
		return "", md, false
	}
	rest := md[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", md, false
	}
	fm := rest[:end]
	body := rest[end+len("\n---"):]
	return fm, strings.TrimLeft(body, "\r\n"), true
}

// BodyWithoutTitle drops a leading H1 line.
func BodyWithoutTitle(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "# ") {
		lines = lines[1:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// Section returns the body of one H2 section.
func Section(body, heading string) string {
	lines := strings.Split(body, "\n")
	target := "## " + heading
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == target {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "## ") {
			end = i
			break
		}
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}
