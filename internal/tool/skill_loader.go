package tool

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// SkillLoader 从 SKILL.md 文件加载工具定义
type SkillLoader struct {
	skillsDir string // skills 目录路径
}

// NewSkillLoader 创建 Skill 加载器
func NewSkillLoader(skillsDir string) *SkillLoader {
	return &SkillLoader{skillsDir: skillsDir}
}

// SkillInfo Skill 元信息
type SkillInfo struct {
	Name        string
	Aliases     []string
	Description string
	Summary     string // v0.36.0: SKILL.md 精简摘要，用于注入 system prompt
	Dir         string
	Tools       []SkillToolDef
	Available   bool
}

// SkillToolDef Skill 中定义的工具
type SkillToolDef struct {
	Name        string
	Description string
	Parameters  map[string]Param
	Handler     func(args map[string]any) (string, error) // 需要外部注入
}

// LoadAll 加载所有 Skill
func (sl *SkillLoader) LoadAll() ([]*SkillInfo, error) {
	entries, err := os.ReadDir(sl.skillsDir)
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var skills []*SkillInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		skillDir := filepath.Join(sl.skillsDir, entry.Name())
		skillFile := filepath.Join(skillDir, "SKILL.md")

		info, err := sl.Load(skillFile)
		if err != nil {
			continue // 跳过无法加载的
		}
		skills = append(skills, info)
	}

	return skills, nil
}

// Load 加载单个 SKILL.md
func (sl *SkillLoader) Load(path string) (*SkillInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read skill: %w", err)
	}

	content := string(data)
	info := &SkillInfo{
		Available: true,
	}

	// 解析 Skill 名称（从目录名）
	dir := filepath.Dir(path)
	info.Dir = dir
	dirName := filepath.Base(dir)
	info.Name = sanitizeName(dirName)
	addSkillAlias(info, dirName)

	// v0.35.0: 解析 frontmatter
	fm := parseFrontmatter(content)
	hasFrontmatterName := false
	if name, ok := fm["name"]; ok && name != "" {
		info.Name = sanitizeName(name)
		hasFrontmatterName = true
		addSkillAlias(info, name)
	}
	if desc, ok := fm["description"]; ok && desc != "" {
		info.Description = desc
	}

	// 解析标题（第一个 # 标题）— 仅作 fallback name
	if match := regexp.MustCompile(`(?m)^#\s+(.+)$`).FindStringSubmatch(content); len(match) > 1 {
		titleName := strings.TrimSpace(match[1])
		// 去掉标题中的 emoji 和特殊字符
		addSkillAlias(info, titleName)
		titleName = sanitizeName(titleName)
		if !hasFrontmatterName && (info.Name == "" || info.Name == sanitizeName(dirName)) {
			info.Name = titleName
		}
	}

	// 解析描述（标题后的第一段非空文本）— 仅作 fallback
	if info.Description == "" {
		info.Description = sl.extractDescription(content)
	}

	// 解析工具定义
	info.Tools = sl.parseToolDefs(content)

	// v0.35.0: 如果没有显式工具定义，自动生成一个通用工具
	if len(info.Tools) == 0 {
		info.Tools = sl.autoGenerateTools(info, content)
	}

	// v0.36.0: 提取 SKILL.md 精简摘要
	info.Summary = sl.extractSummary(content)

	return info, nil
}

func addSkillAlias(info *SkillInfo, alias string) {
	alias = sanitizeName(alias)
	if alias == "" {
		return
	}
	if alias == info.Name {
		return
	}
	for _, existing := range info.Aliases {
		if existing == alias {
			return
		}
	}
	info.Aliases = append(info.Aliases, alias)
}

// extractDescription 从 SKILL.md 提取描述
func (sl *SkillLoader) extractDescription(content string) string {
	// 去掉 frontmatter
	body := stripFrontmatter(content)

	lines := strings.Split(body, "\n")
	inHeader := true
	for _, line := range lines {
		if inHeader {
			if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
				continue
			}
			inHeader = false
		}
		if strings.TrimSpace(line) != "" {
			desc := strings.TrimSpace(line)
			// 截断过长的描述
			if len(desc) > 200 {
				desc = desc[:197] + "..."
			}
			return desc
		}
	}
	return ""
}

// autoGenerateTools 为没有显式工具定义的 skill 自动生成工具
func (sl *SkillLoader) autoGenerateTools(info *SkillInfo, content string) []SkillToolDef {
	var tools []SkillToolDef

	// 检查 scripts/ 目录下有哪些脚本
	scriptsDir := filepath.Join(info.Dir, "scripts")
	scripts := sl.findScripts(scriptsDir)

	if len(scripts) == 0 {
		// 纯文档型 skill 通过 skill_read 暴露，避免模型把静态指南当成可执行工具反复调用。
		return nil
	}

	// 为每个脚本生成一个工具
	for _, script := range scripts {
		toolName := strings.TrimSuffix(script.Name(), filepath.Ext(script.Name()))
		toolName = sanitizeName(toolName)
		if toolName == "" {
			continue
		}
		tools = append(tools, SkillToolDef{
			Name:        toolName,
			Description: fmt.Sprintf("执行 %s skill 的 %s 脚本", info.Name, toolName),
			Parameters:  map[string]Param{},
		})
	}

	// 始终生成一个主工具（skill name 本身）
	// 即使有脚本工具，也保留一个入口
	mainTool := SkillToolDef{
		Name:        "run",
		Description: info.Description,
		Parameters: map[string]Param{
			"query": {
				Type:        "string",
				Description: "用户请求内容",
				Required:    false,
			},
		},
	}
	tools = append([]SkillToolDef{mainTool}, tools...)

	return tools
}

// findScripts 查找 scripts 目录下的可执行脚本
func (sl *SkillLoader) findScripts(dir string) []os.DirEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var scripts []os.DirEntry
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// 支持的脚本类型
		if strings.HasSuffix(name, ".sh") || strings.HasSuffix(name, ".py") ||
			strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".go") {
			scripts = append(scripts, entry)
		}
	}
	return scripts
}

// parseToolDefs 从 SKILL.md 内容解析工具定义
func (sl *SkillLoader) parseToolDefs(content string) []SkillToolDef {
	var tools []SkillToolDef

	// 查找工具区域
	toolSection := sl.extractSection(content, "Tools")
	if toolSection == "" {
		toolSection = sl.extractSection(content, "可用工具")
	}
	if toolSection == "" {
		toolSection = sl.extractSection(content, "工具")
	}

	if toolSection == "" {
		return tools
	}

	// 解析工具条目
	// 格式: - `tool_name`: description
	// 或: - **tool_name**: description
	scanner := bufio.NewScanner(strings.NewReader(toolSection))
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)

		// 匹配列表项
		if !strings.HasPrefix(line, "-") {
			continue
		}
		line = strings.TrimPrefix(line, "-")
		line = strings.TrimSpace(line)

		name, desc := parseToolEntry(line)
		if name == "" {
			continue
		}

		tools = append(tools, SkillToolDef{
			Name:        name,
			Description: desc,
			Parameters:  map[string]Param{},
		})
	}

	return tools
}

// extractSection 提取 Markdown section
func (sl *SkillLoader) extractSection(content, title string) string {
	// 查找 ## title
	sectionStart := -1
	sectionEnd := -1

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimPrefix(line, "## ")
			heading = strings.TrimSpace(heading)
			if strings.EqualFold(heading, title) {
				sectionStart = i + 1
				continue
			}
			// 遇到下一个 ## 结束
			if sectionStart >= 0 && sectionEnd < 0 {
				sectionEnd = i
			}
		}
	}

	if sectionStart < 0 {
		return ""
	}
	if sectionEnd < 0 {
		sectionEnd = len(lines)
	}

	return strings.Join(lines[sectionStart:sectionEnd], "\n")
}

// parseToolEntry 解析工具条目
func parseToolEntry(line string) (name, desc string) {
	// 格式1: `tool_name`: description
	re1 := regexp.MustCompile("`([^`]+)`\\s*[:：]\\s*(.+)")
	if match := re1.FindStringSubmatch(line); len(match) > 2 {
		return match[1], match[2]
	}

	// 格式2: **tool_name**: description
	re2 := regexp.MustCompile(`\*\*([^*]+)\*\*\s*[:：]\s*(.+)`)
	if match := re2.FindStringSubmatch(line); len(match) > 2 {
		return match[1], match[2]
	}

	// 格式3: tool_name - description
	re3 := regexp.MustCompile(`^(\w[\w_-]*)\s*[-—–]\s*(.+)`)
	if match := re3.FindStringSubmatch(line); len(match) > 2 {
		return match[1], match[2]
	}

	return "", ""
}

// parseFrontmatter 解析 YAML frontmatter
func parseFrontmatter(content string) map[string]string {
	result := make(map[string]string)

	// 检查是否以 --- 开头
	if !strings.HasPrefix(content, "---") {
		return result
	}

	// 找到结束的 ---
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return result
	}

	fm := content[3 : end+3]
	scanner := bufio.NewScanner(strings.NewReader(fm))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 简单 key: value 解析（不处理嵌套 YAML）
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			// 去掉引号
			val = strings.Trim(val, "\"'")
			if key != "" && val != "" {
				result[key] = val
			}
		}
	}

	return result
}

// stripFrontmatter 去掉 frontmatter，返回 body
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return content
	}
	return strings.TrimSpace(content[end+6:])
}

// sanitizeName 清理名称，去掉特殊字符
func sanitizeName(name string) string {
	// 去掉 emoji（保留其他所有字符）
	re := regexp.MustCompile(`[\x{1F600}-\x{1F64F}\x{1F300}-\x{1F5FF}\x{1F680}-\x{1F6FF}\x{1F1E0}-\x{1F1FF}\x{2600}-\x{26FF}\x{2700}-\x{27BF}]`)
	name = re.ReplaceAllString(name, "")

	// 去掉首尾空白
	name = strings.TrimSpace(name)

	// 转小写
	name = strings.ToLower(name)

	// 将空白字符和连续特殊字符替换为单个下划线
	name = regexp.MustCompile(`\s+`).ReplaceAllString(name, "_")
	// 保留中文、字母、数字、下划线、连字符，其他都替换为下划线
	name = regexp.MustCompile(`[^a-zA-Z0-9_\x{4e00}-\x{9fff}-]`).ReplaceAllString(name, "_")

	// 去掉首尾的下划线和连字符
	name = strings.Trim(name, "_-")

	return name
}

// RegisterSkillTools 将 Skill 工具注册到 Registry
// handler 为 Skill 工具的通用处理器（通常调用脚本或子进程）
func RegisterSkillTools(r *Registry, skills []*SkillInfo, handler func(toolName string, skillDir string) func(args map[string]any) (string, error)) {
	for _, skill := range skills {
		for _, toolDef := range skill.Tools {
			tool := &Tool{
				Name:            fmt.Sprintf("skill_%s_%s", skill.Name, toolDef.Name),
				Description:     toolDef.Description,
				Parameters:      toolDef.Parameters,
				Category:        CatSkill,
				Source:          skill.Name,
				Permission:      PermApprove, // Skill 工具默认需要审批
				Enabled:         true,
				HiddenFromModel: toolDef.Name != "run",
			}
			if handler != nil {
				tool.Handler = handler(toolDef.Name, skill.Dir)
			} else {
				// v0.35.0: 默认处理器 — 尝试执行脚本，否则返回 SKILL.md 摘要
				tool.Handler = defaultSkillHandler(toolDef.Name, skill.Dir, skill.Name)
			}
			r.Register(tool)
		}
	}
}

// defaultSkillHandler 为 skill 工具提供默认执行逻辑
func defaultSkillHandler(toolName, skillDir, skillName string) func(args map[string]any) (string, error) {
	return func(args map[string]any) (string, error) {
		// 1. 尝试执行 scripts/<toolName>.sh 或 .py
		for _, ext := range []string{".sh", ".py", ".js"} {
			scriptPath := filepath.Join(skillDir, "scripts", toolName+ext)
			if _, err := os.Stat(scriptPath); err == nil {
				return executeScript(scriptPath, args)
			}
		}

		// 2. 尝试执行 scripts/ 目录下唯一的脚本
		scriptsDir := filepath.Join(skillDir, "scripts")
		if entries, err := os.ReadDir(scriptsDir); err == nil && len(entries) == 1 {
			if !entries[0].IsDir() {
				scriptPath := filepath.Join(scriptsDir, entries[0].Name())
				return executeScript(scriptPath, args)
			}
		}

		// 3. 返回 SKILL.md 内容摘要
		skillFile := filepath.Join(skillDir, "SKILL.md")
		if data, err := os.ReadFile(skillFile); err == nil {
			content := string(data)
			// 去掉 frontmatter
			body := stripFrontmatter(content)
			// 截取前 500 字符作为摘要
			if len(body) > 500 {
				body = body[:497] + "..."
			}
			return fmt.Sprintf("[Skill: %s / Tool: %s]\n%s", skillName, toolName, body), nil
		}

		return fmt.Sprintf("Skill '%s' tool '%s' — no executable script found and SKILL.md unreadable", skillName, toolName), nil
	}
}

// executeScript 执行脚本并返回输出
func executeScript(scriptPath string, args map[string]any) (string, error) {
	var cmd *exec.Cmd
	ext := filepath.Ext(scriptPath)

	switch ext {
	case ".sh":
		cmd = exec.Command("/bin/sh", scriptPath)
	case ".py":
		cmd = exec.Command("python3", scriptPath)
	case ".js":
		cmd = exec.Command("node", scriptPath)
	default:
		cmd = exec.Command("/bin/sh", scriptPath)
	}

	// 将 args 序列化为环境变量
	for k, v := range args {
		cmd.Env = append(cmd.Environ(), fmt.Sprintf("SKILL_ARG_%s=%v", strings.ToUpper(k), v))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("script error: %w", err)
	}
	return string(output), nil
}

// extractSummary 从 SKILL.md 提取精简摘要，用于注入 system prompt
// 策略：提取关键 section（Trigger/When to use/Steps/Tools）的要点，限制总长 500 字符
func (sl *SkillLoader) extractSummary(content string) string {
	body := stripFrontmatter(content)
	lines := strings.Split(body, "\n")

	var summary strings.Builder
	var inTargetSection bool

	// 目标 section 关键词（大小写不敏感，中英文）
	targetSections := []string{
		// English
		"trigger", "when to use", "when to apply", "steps", "how to use",
		"usage", "tools", "workflow", "overview", "description",
		"quick start", "key concepts", "core tools", "how to search",
		"routing rules", "how it works", "rule of thumb",
		"command reference", "common patterns", "best practices",
		"quick decision", "conflict resolution",
		// Chinese
		"触发", "工作流", "工作流程", "核心规则", "核心能力",
		"使用方式", "子技能路由", "覆盖范围", "执行规则",
		"快速原则", "服务地图", "可用机制", "触发方式",
		"聊天交付", "商业模式", "战略分析", "参考文档",
		"核心概念", "工作原理", "认证", "环境变量",
		"默认策略", "工作规则", "命令", "常见自动化",
		"设计自动化", "首次使用", "固定配置",
		"搜索源", "搜索策略", "内容提取", "快速参考",
		"执行步骤", "快速开始", "技术规范", "页面类型",
		"输出规范", "身份", "核心人格", "说话风格",
		"禁忌", "对话模式", "前置依赖", "功能说明",
		"参数表", "协议", "角色",
	}

	for _, line := range lines {
		// 检测 section 标题
		if strings.HasPrefix(line, "## ") {
			heading := strings.ToLower(strings.TrimPrefix(line, "## "))
			inTargetSection = false
			for _, ts := range targetSections {
				if strings.Contains(heading, ts) {
					inTargetSection = true
					summary.WriteString("[" + strings.TrimPrefix(line, "## ") + "] ")
					break
				}
			}
			continue
		}

		if strings.HasPrefix(line, "### ") {
			// 子标题，如果在目标 section 内则保留
			if inTargetSection {
				summary.WriteString(strings.TrimPrefix(line, "### ") + ": ")
			}
			continue
		}

		if !inTargetSection {
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// 列表项直接保留
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "1.") || strings.HasPrefix(trimmed, "2.") {
			summary.WriteString(trimmed + " ")
		} else if len(trimmed) > 0 {
			// 普通文本截断
			if len(trimmed) > 80 {
				trimmed = trimmed[:77] + "..."
			}
			summary.WriteString(trimmed + " ")
		}

		// 限制总长
		if summary.Len() > 500 {
			break
		}
	}

	result := strings.TrimSpace(summary.String())
	if len(result) > 500 {
		result = result[:497] + "..."
	}

	// 如果没提取到任何内容，用 description 兜底
	if result == "" {
		return ""
	}

	return result
}
