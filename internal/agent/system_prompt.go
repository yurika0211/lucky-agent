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

var contextThreatPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(previous|all|above|prior)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(your|all|any)\s+(instructions|rules|guidelines)`),
	regexp.MustCompile(`(?i)system\s+prompt\s+override`),
	regexp.MustCompile(`(?i)do\s+not\s+tell\s+the\s+user`),
}

/*
buildSystemPrompt 组装 Agent 的系统提示词。

它会按顺序拼接身份设定、记忆/技能/工具使用约束、
项目手册、上下文文件以及运行时元信息，最终生成给模型的
完整 system prompt。
*/
func (a *Agent) buildSystemPrompt(sess *session.Session) string {
	parts := make([]string, 0, 12)

	parts = append(parts, ``)

	toolNames := a.enabledToolNames()

	// 动态加载对应的上下文
	if slices.Contains(toolNames, "remember") || slices.Contains(toolNames, "recall") {
		parts = append(parts, ``)
	}
	if len(a.skills) > 0 && slices.Contains(toolNames, "skill_read") {
		parts = append(parts, ``)
	}
	if len(toolNames) > 0 {
		parts = append(parts, toolNames...)
	}

	// 加载大模型相关配置
	modelName := ""
	providerName := ""
	platform := "cli"
	if a != nil && a.cfg != nil {
		cfg := a.cfg.Get()
		modelName = strings.TrimSpace(cfg.Model)
		providerName = strings.TrimSpace(cfg.Provider)
		platform = strings.TrimSpace(strings.ToLower(cfg.MsgGateway.Platform))
	}

	// 加载技能提示快、智能体操作手册、上下文区块
	if skillsBlock := a.buildSkillsPromptBlock(); skillsBlock != "" {
		parts = append(parts, skillsBlock)
	}
	if manualBlock := a.buildLuckyHarnessManualPrompt(sess); manualBlock != "" {
		parts = append(parts, manualBlock)
	}
	if contextBlock := a.buildContextFilesPrompt(sess); contextBlock != "" {
		parts = append(parts, contextBlock)
	}

	// 加载元数据
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

/*
enabledToolNames 返回当前已启用工具的名称列表。

该函数会过滤空工具和未命名工具，供系统提示词判断
是否需要注入记忆、技能、工具相关的附加说明。
*/
func (a *Agent) enabledToolNames() []string {
	if a == nil || a.tools == nil {
		return nil
	}
	tools := a.tools.ListModelVisible()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t == nil || strings.TrimSpace(t.Name) == "" {
			continue
		}
		names = append(names, t.Name)
	}
	return names
}

/*
buildSkillsPromptBlock 生成技能摘要提示块。

它会从已加载技能中提取名称与简介，限制数量与长度，
用于在 system prompt 中提示模型当前有哪些可用技能。
*/
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
		if count >= 100 {
			break
		}
	}
	if count == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

/*
buildLuckyHarnessManualPrompt 读取并构造 LuckyHarness 手册提示块。

该函数会先定位手册文件，再读取内容并做注入风险过滤与长度裁剪，
最后将其包装成可直接拼入 system prompt 的文本块。
*/
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
	if len(content) > 20000 {
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

/*
buildContextFilesPrompt 读取并构造项目上下文文件提示块。

它会基于当前会话或进程工作目录向上查找最近的上下文文件，
读取后进行安全过滤和长度裁剪，再作为补充上下文注入提示词。
*/
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

	contextPath := findAgentsFile(cwd)
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
	content = utils.CompactMarkdownForPrompt(content, 20000, func(s string) int { return len([]rune(s)) }, utils.MarkdownBudgetOptions{})
	return fmt.Sprintf("Context file (%s):\n%s", filepath.Base(contextPath), content)
}

/*
findLuckyHarnessManualPath 查找 LuckyHarness 手册文件路径。

当前实现仅从环境变量 LUCKYHARNESS_MANUAL_FILE 指定的位置查找；
如果变量未设置或文件不存在，则返回空字符串。
*/
func (a *Agent) findLuckyHarnessManualPath(sess *session.Session) string {
	candidates := make([]string, 0, 6)

	raw := strings.TrimSpace(os.Getenv("LUCKYHARNESS_MANUAL_FILE"))

	if raw == "" {
		fmt.Println("Please set the LUCKYHARNESS_MANUAL_FILE environment variable")
	}

	if raw != "" {
		candidates = append(candidates, raw)
		for _, candidate := range candidates {
			if strings.TrimSpace(candidate) == "" {
				continue
			}
			if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

/*
findAgentsFile 从环境变量直接验证

查找目标AGENTS.md；一旦找到最近的
存在文件，就返回其绝对路径；若一直到根目录都未找到，则返回空字符串。
*/
func findAgentsFile(cwd string) string {
	soulPath := os.Getenv("LUCKYHARNESS_AGENTS_FILE")
	if soulPath != "" {
		return soulPath
	}
	if soulPath == "" {
		fmt.Println("Please set your agent.md 's path in environment")
	}
	return ""
}

/*
sanitizeContextContent 对外部上下文内容做基础安全过滤。

当内容命中潜在的提示注入模式时，会返回一个阻断提示，
而不是原始内容；否则原样返回输入文本。
*/
func sanitizeContextContent(content string, filename string) string {
	for _, pattern := range contextThreatPatterns {
		if pattern.MatchString(content) {
			return fmt.Sprintf("[BLOCKED: %s contained potential prompt-injection content and was not loaded.]", filename)
		}
	}
	return content
}

/*
platformHint 根据当前交互平台生成附加提示。

不同平台在消息长度、展示方式和文件交付能力上有差异，
该函数用于向模型补充平台相关的回答约束。
*/
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
