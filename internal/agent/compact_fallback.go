package agent

import (
	"strings"

	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/utils"
)

func generateLocalCompactSummary(messages []provider.Message, est *contextx.TokenEstimator) string {
	var userGoal string
	var completed []string
	var pending []string
	var files []string
	var commands []string
	var constraints []string

	seenFiles := make(map[string]struct{})
	for _, msg := range messages {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			continue
		}
		switch msg.Role {
		case "user":
			userGoal = truncate(text, 220)
			if compactLooksLikeConstraint(text) {
				constraints = append(constraints, "- "+truncate(text, 160))
			}
		case "assistant":
			completed = append(completed, "- "+truncate(text, 180))
		case "tool":
			if summary := summarizeToolResult(msg.Name, text); summary != "" {
				commands = append(commands, "- tool "+strings.TrimSpace(msg.Name)+": "+summary)
			} else {
				commands = append(commands, "- tool "+strings.TrimSpace(msg.Name)+": "+truncate(text, 180))
			}
		}
		if compactLooksLikeCommand(text) {
			commands = append(commands, "- command/evidence: "+truncate(text, 180))
		}
		for _, match := range compactFileRefPattern.FindAllString(text, -1) {
			file := strings.Trim(match, " \t\n\r\"'(:")
			if file == "" {
				continue
			}
			if _, ok := seenFiles[file]; ok {
				continue
			}
			seenFiles[file] = struct{}{}
			files = append(files, "- "+file)
		}
	}

	if userGoal == "" {
		userGoal = "Continue the current LuckyAgent session from the available prior messages."
	}
	if len(pending) == 0 {
		pending = append(pending, "- Continue from the latest user goal and verify unfinished work against the current workspace state.")
	}
	if len(completed) == 0 {
		completed = append(completed, "- No reliable assistant completion details were available to the local compact fallback.")
	}
	if len(files) == 0 {
		files = append(files, "- No explicit file path was found in the compacted range.")
	}
	if len(commands) == 0 {
		commands = append(commands, "- No explicit command or tool result was found in the compacted range.")
	}
	if len(constraints) == 0 {
		constraints = append(constraints, "- Preserve the latest user instructions and do not invent unverified command results.")
	}

	var b strings.Builder
	b.WriteString("Current user goal:\n")
	b.WriteString("- " + userGoal + "\n")
	b.WriteString("Completed work:\n")
	for _, line := range utils.DedupStringsLimit(completed, 4) {
		b.WriteString(line + "\n")
	}
	b.WriteString("Pending work:\n")
	for _, line := range utils.DedupStringsLimit(pending, 3) {
		b.WriteString(line + "\n")
	}
	b.WriteString("Key files and functions:\n")
	for _, line := range utils.DedupStringsLimit(files, 10) {
		b.WriteString(line + "\n")
	}
	b.WriteString("Commands and test results:\n")
	for _, line := range utils.DedupStringsLimit(commands, 6) {
		b.WriteString(line + "\n")
	}
	b.WriteString("User constraints:\n")
	for _, line := range utils.DedupStringsLimit(constraints, 4) {
		b.WriteString(line + "\n")
	}
	b.WriteString("Uncertain facts:\n")
	b.WriteString("- This compact summary was generated locally without an LLM; verify repository state before treating prior progress as complete.\n")

	summary := strings.TrimSpace(b.String())
	return summary
}

func compactLooksLikeCommand(text string) bool {
	lower := strings.ToLower(text)
	return compactContainsAny(lower,
		"go test", "npm test", "pnpm test", "pytest", "cargo test",
		"git status", "git diff", "git commit", "failed", "error", "panic",
	)
}

func compactLooksLikeConstraint(text string) bool {
	lower := strings.ToLower(text)
	return compactContainsAny(lower, "commit", "不要", "不能", "must", "never", "keep", "preserve", "干一点")
}
