package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/utils"
)

const defaultAgentIdentity = `You are LuckyHarness Agent, an intelligent AI assistant.
You are helpful, knowledgeable, concise, and direct.
You assist users with coding, analysis, writing, automation, and tool-driven execution.
You should prefer acting with tools over merely describing what you would do.
Answer in the user's language by default unless the user asks otherwise.`

const memoryGuidance = `When you learn a durable fact about the user, environment, or recurring workflow, store it in memory.
Do not save temporary task progress or one-off intermediate results as memory.`

const skillsGuidance = `When a task matches an available skill, use skill_read first to load the skill instructions, then follow them.
If you discover a reusable workflow, prefer updating or creating a skill rather than repeating fragile ad-hoc steps.`

const toolExecutionGuidance = `Tool-use discipline:
- Use tools whenever they improve correctness, grounding, or completeness.
- Do not stop at a plan when you can actually execute the next step.
- If a claim depends on the local machine, files, or current state, verify it with tools instead of guessing.
- Never print tool-call protocol, JSON tool payloads, XML tool tags, or scratch tool plans to the user. Execute tools directly and only show user-facing conclusions.`

const openAIExecutionGuidance = `OpenAI-model execution rules:
- Never do arithmetic, file inspection, git inspection, or current-state checks from memory when tools can verify them.
- Prefer concrete execution over speculation.
- If a tool result is partial, retry with a better query or a narrower scope before giving up.`

var (
	contextThreatPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`),
		regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`),
		regexp.MustCompile(`(?i)system\s+prompt\s+override`),
		regexp.MustCompile(`(?i)do\s+not\s+tell\s+the\s+user`),
	}
)

func (a *Agent) buildSystemPrompt(sess *session.Session) string {
	parts := make([]string, 0, 12)

	identity := defaultAgentIdentity
	if a != nil && a.soul != nil && strings.TrimSpace(a.soul.SystemPrompt()) != "" {
		identity = strings.TrimSpace(a.soul.SystemPrompt())
	}
	parts = append(parts, identity)

	toolNames := a.enabledToolNames()
	if slices.Contains(toolNames, "remember") || slices.Contains(toolNames, "recall") {
		parts = append(parts, memoryGuidance)
	}
	if len(a.skills) > 0 && slices.Contains(toolNames, "skill_read") {
		parts = append(parts, skillsGuidance)
	}
	if len(toolNames) > 0 {
		parts = append(parts, toolExecutionGuidance)
	}

	modelName := ""
	providerName := ""
	platform := "cli"
	if a != nil && a.cfg != nil {
		cfg := a.cfg.Get()
		modelName = strings.TrimSpace(cfg.Model)
		providerName = strings.TrimSpace(cfg.Provider)
		if strings.TrimSpace(cfg.MsgGateway.Platform) != "" {
			platform = strings.TrimSpace(strings.ToLower(cfg.MsgGateway.Platform))
		}
	}
	if m := strings.ToLower(modelName); strings.Contains(m, "gpt") || strings.Contains(m, "codex") {
		parts = append(parts, openAIExecutionGuidance)
	}

	if skillsBlock := a.buildSkillsPromptBlock(); skillsBlock != "" {
		parts = append(parts, skillsBlock)
	}
	if manualBlock := a.buildLuckyHarnessManualPrompt(sess); manualBlock != "" {
		parts = append(parts, manualBlock)
	}
	if contextBlock := a.buildContextFilesPrompt(sess); contextBlock != "" {
		parts = append(parts, contextBlock)
	}

	meta := []string{
		fmt.Sprintf("Conversation started: %s", time.Now().Format("Monday, January 02, 2006 03:04 PM")),
	}
	if modelName != "" {
		meta = append(meta, "Model: "+modelName)
	}
	if providerName != "" {
		meta = append(meta, "Provider: "+providerName)
	}
	parts = append(parts, strings.Join(meta, "\n"))

	if hint := platformHint(platform); hint != "" {
		parts = append(parts, hint)
	}

	return strings.TrimSpace(strings.Join(utils.FilterNonEmptyTrimmed(parts), "\n\n"))
}

func (a *Agent) enabledToolNames() []string {
	if a == nil || a.tools == nil {
		return nil
	}
	tools := a.tools.ListEnabled()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil || strings.TrimSpace(t.Name) == "" {
			continue
		}
		names = append(names, t.Name)
	}
	return names
}

func (a *Agent) buildSkillsPromptBlock() string {
	if a == nil || len(a.skills) == 0 {
		return ""
	}

	lines := make([]string, 0, min(8, len(a.skills))+1)
	lines = append(lines, "Available skills:")
	count := 0
	for _, s := range a.skills {
		if s == nil || strings.TrimSpace(s.Name) == "" {
			continue
		}
		summary := strings.TrimSpace(s.Summary)
		if summary == "" {
			summary = strings.TrimSpace(s.Description)
		}
		if summary == "" {
			continue
		}
		summary = utils.Truncate(summary, 180)
		lines = append(lines, fmt.Sprintf("- %s: %s", s.Name, summary))
		count++
		if count >= 8 {
			break
		}
	}
	if count == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func (a *Agent) buildLuckyHarnessManualPrompt(sess *session.Session) string {
	manualPath := a.findLuckyHarnessManualPath(sess)
	if manualPath == "" {
		return ""
	}

	data, err := os.ReadFile(manualPath)
	if err != nil {
		return ""
	}

	content := sanitizeContextContent(string(data), filepath.Base(manualPath))
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(content) > 12000 {
		head := int(float64(len(content)) * 0.7)
		tail := int(float64(len(content)) * 0.2)
		if head+tail > len(content) {
			head = len(content)
			tail = 0
		}
		content = strings.TrimSpace(content[:head] + "\n\n[... omitted ...]\n\n" + content[len(content)-tail:])
	}

	return fmt.Sprintf("LuckyHarness manual (%s):\n%s", filepath.Base(manualPath), content)
}

func (a *Agent) buildContextFilesPrompt(sess *session.Session) string {
	cwd := ""
	if sess != nil {
		cwd = strings.TrimSpace(sess.GetCwd())
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if cwd == "" {
		return ""
	}

	contextPath := findNearestContextFile(cwd)
	if contextPath == "" {
		return ""
	}

	data, err := os.ReadFile(contextPath)
	if err != nil {
		return ""
	}

	content := sanitizeContextContent(string(data), filepath.Base(contextPath))
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(content) > 8000 {
		head := int(float64(len(content)) * 0.7)
		tail := int(float64(len(content)) * 0.2)
		if head+tail > len(content) {
			head = len(content)
			tail = 0
		}
		content = strings.TrimSpace(content[:head] + "\n\n[... omitted ...]\n\n" + content[len(content)-tail:])
	}

	return fmt.Sprintf("Context file (%s):\n%s", filepath.Base(contextPath), content)
}

func (a *Agent) findLuckyHarnessManualPath(sess *session.Session) string {
	candidates := make([]string, 0, 6)

	if raw := strings.TrimSpace(os.Getenv("LUCKYHARNESS_MANUAL_FILE")); raw != "" {
		candidates = append(candidates, raw)
	}

	cwd := ""
	if sess != nil {
		cwd = strings.TrimSpace(sess.GetCwd())
	}
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			cwd = abs
			candidates = append(candidates, filepath.Join(cwd, "LUCKYHARNESS_AGENT_MANUAL.md"))
			candidates = append(candidates, filepath.Join(cwd, ".luckyharness-manual.md"))
			if root := findGitRoot(cwd); root != "" && root != cwd {
				candidates = append(candidates, filepath.Join(root, "LUCKYHARNESS_AGENT_MANUAL.md"))
				candidates = append(candidates, filepath.Join(root, ".luckyharness-manual.md"))
			}
		}
	}

	if a != nil && a.cfg != nil {
		if homeDir := strings.TrimSpace(a.cfg.HomeDir()); homeDir != "" {
			candidates = append(candidates, filepath.Join(homeDir, "LUCKYHARNESS_AGENT_MANUAL.md"))
			candidates = append(candidates, filepath.Join(homeDir, ".luckyharness-manual.md"))
		}
	}

	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}

	return ""
}

func findNearestContextFile(cwd string) string {
	current, err := filepath.Abs(cwd)
	if err != nil {
		return ""
	}

	stopAt := findGitRoot(current)
	for {
		for _, name := range []string{"AGENTS.md", ".cursorrules", ".luckyharness.md", "LUCKYHARNESS.md", ".hermes.md", "HERMES.md"} {
			candidate := filepath.Join(current, name)
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate
			}
		}
		if current == stopAt {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ""
}

func findGitRoot(start string) string {
	current := start
	for {
		if st, err := os.Stat(filepath.Join(current, ".git")); err == nil && st != nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return current
		}
		current = parent
	}
}

func sanitizeContextContent(content string, filename string) string {
	for _, pattern := range contextThreatPatterns {
		if pattern.MatchString(content) {
			return fmt.Sprintf("[BLOCKED: %s contained potential prompt-injection content and was not loaded.]", filename)
		}
	}
	return content
}

func platformHint(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "telegram":
		return "You are on Telegram. Standard markdown may be rendered, but keep responses compact. If you need to deliver a real file, prefer returning a concrete file path or a generated file artifact instead of dumping the whole file inline."
	case "onebot":
		return "You are on OneBot/QQ-style messaging. Keep responses short and chat-friendly. If you need to deliver a file, prefer returning a concrete file path or artifact instead of pasting large payloads inline."
	case "cli":
		return "You are interacting through a CLI/terminal. Prefer plain text over decorative markdown."
	default:
		return ""
	}
}
