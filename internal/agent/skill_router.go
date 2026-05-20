package agent

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/tool"
	"github.com/yurika0211/luckyharness/internal/utils"
)

var skillRouteTokenRe = regexp.MustCompile(`[a-zA-Z0-9_+\-]+|[\p{Han}]{2,}`)

var skillRouteStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "into": {}, "this": {}, "that": {},
	"use": {}, "using": {}, "need": {}, "want": {}, "help": {}, "please": {}, "about": {}, "then": {},
	"user": {}, "task": {}, "workflow": {}, "skill": {}, "when": {}, "apply": {}, "through": {}, "should": {},
	"一个": {}, "这个": {}, "那个": {}, "一下": {}, "帮我": {}, "需要": {}, "想要": {}, "使用": {}, "技能": {},
	"工作流": {}, "一下子": {}, "然后": {}, "什么": {}, "怎么": {}, "可以": {},
}

var skillRouteConceptMap = map[string][]string{
	"research":      {"调研", "研究", "资料", "来源", "报告"},
	"deep-research": {"调研", "深度调研", "研究", "资料", "来源", "报告"},
	"report":        {"报告", "汇报", "总结"},
	"evidence":      {"证据", "依据", "来源"},
	"source":        {"来源", "资料"},
	"obsidian":      {"笔记", "知识库", "vault", "markdown", "wikilink", "标签"},
	"note":          {"笔记", "便签"},
	"notes":         {"笔记", "便签"},
	"vault":         {"知识库", "仓库", "库"},
	"tag":           {"标签", "tag"},
	"tags":          {"标签", "tag"},
	"wikilink":      {"双链", "wikilink", "链接"},
	"telegram":      {"电报", "tg", "telegram", "机器人", "bot"},
	"bot":           {"机器人", "bot"},
	"workflow":      {"工作流", "流程"},
	"markdown":      {"markdown", "md"},
	"task":          {"任务", "待办"},
	"tasks":         {"任务", "待办"},
	"vikunja":       {"vikunja", "任务", "待办"},
	"calendar":      {"日历", "calendar"},
	"email":         {"邮件", "邮箱", "email"},
	"pdf":           {"pdf", "文档"},
	"image":         {"图片", "图像", "配图"},
	"images":        {"图片", "图像", "配图"},
	"video":         {"视频", "video"},
	"audio":         {"音频", "声音", "audio"},
	"weather":       {"天气", "气温", "预报"},
}

type skillRouteMatch struct {
	skill        *tool.SkillInfo
	score        int
	explicit     bool
	reason       string
	preferredRun string
}

func (a *Agent) buildSkillRouteSystemHint(userInput string) string {
	match := a.matchSkillRoute(userInput)
	if match == nil || match.skill == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(match.skill.Name), "obsidian") && isLuckyHarnessMemoryBackendQuestion(userInput) {
		return `Skill routing note:
- This request is about the LuckyHarness memory backend, not an external Obsidian app vault.
- Do not use the Obsidian skill or OBSIDIAN_VAULT_PATH to decide whether LuckyHarness memory exists unless the user explicitly asks to operate an external Obsidian vault.
- Use recall, local LuckyHarness memory vault files, or runtime inspection as direct evidence.`
	}

	var lines []string
	lines = append(lines, "Skill routing hint:")
	lines = append(lines, fmt.Sprintf("- This request strongly matches the %q skill.", match.skill.Name))
	if match.reason != "" {
		lines = append(lines, "- Reason: "+match.reason)
	}
	lines = append(lines, "- Prefer this skill workflow before ad-hoc reasoning if it fits the task after inspection.")
	if a.hasModelVisibleTool("skill_read") {
		lines = append(lines, fmt.Sprintf("- First inspect the skill guidance with skill_read(name=%q).", match.skill.Name))
	}
	if match.preferredRun != "" {
		lines = append(lines, fmt.Sprintf("- Preferred execution entry: %s", match.preferredRun))
	}
	if summary := routeFriendlySkillSummary(match.skill); summary != "" {
		lines = append(lines, "- Skill summary: "+summary)
	}
	lines = append(lines, "- Do not ignore better direct evidence, but bias toward this skill workflow unless the task clearly does not fit.")
	return strings.Join(lines, "\n")
}

func isLuckyHarnessMemoryBackendQuestion(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	hasMemory := strings.Contains(lower, "memory") ||
		strings.Contains(lower, "记忆") ||
		strings.Contains(lower, "回忆") ||
		strings.Contains(lower, "recall") ||
		strings.Contains(lower, "remember")
	hasBackend := strings.Contains(lower, "luckyharness") ||
		strings.Contains(lower, "记忆系统") ||
		strings.Contains(lower, "记忆库") ||
		strings.Contains(lower, "存储") ||
		strings.Contains(lower, "后端") ||
		strings.Contains(lower, "取代") ||
		strings.Contains(lower, "生效") ||
		strings.Contains(lower, "双链记忆")
	return hasMemory && hasBackend
}

func (a *Agent) matchSkillRoute(userInput string) *skillRouteMatch {
	if a == nil || len(a.skills) == 0 {
		return nil
	}

	input := strings.TrimSpace(strings.ToLower(userInput))
	if input == "" {
		return nil
	}
	inputTokens := expandSkillRouteTokens(tokenizeSkillRouteText(input))
	if len(inputTokens) == 0 {
		return nil
	}

	var matches []*skillRouteMatch
	for _, skill := range a.skills {
		if skill == nil || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		if m := a.scoreSkillRoute(skill, input, inputTokens); m != nil {
			matches = append(matches, m)
		}
	}
	if len(matches) == 0 {
		return nil
	}

	slices.SortFunc(matches, func(a, b *skillRouteMatch) int {
		if a.score != b.score {
			return b.score - a.score
		}
		if a.explicit != b.explicit {
			if a.explicit {
				return -1
			}
			return 1
		}
		return strings.Compare(a.skill.Name, b.skill.Name)
	})

	best := matches[0]
	if best.explicit {
		return best
	}
	if best.score < 3 {
		return nil
	}
	if len(matches) > 1 && best.score <= matches[1].score {
		return nil
	}
	return best
}

func (a *Agent) scoreSkillRoute(skill *tool.SkillInfo, loweredInput string, inputTokens []string) *skillRouteMatch {
	names := []string{skill.Name}
	names = append(names, skill.Aliases...)

	explicitHit := ""
	for _, name := range names {
		norm := strings.ToLower(strings.TrimSpace(name))
		if norm == "" {
			continue
		}
		if strings.Contains(loweredInput, norm) {
			explicitHit = norm
			break
		}
		normHyphen := strings.ReplaceAll(norm, "_", "-")
		if normHyphen != norm && strings.Contains(loweredInput, normHyphen) {
			explicitHit = normHyphen
			break
		}
	}

	summary := routeFriendlySkillSummary(skill)
	corpus := strings.Join(utils.FilterNonEmptyTrimmed([]string{
		skill.Name,
		strings.Join(skill.Aliases, " "),
		skill.Description,
		summary,
	}), " ")
	corpusTokens := expandSkillRouteTokens(tokenizeSkillRouteText(strings.ToLower(corpus)))
	if len(corpusTokens) == 0 && explicitHit == "" {
		return nil
	}

	score := 0
	reasons := make([]string, 0, 3)
	if explicitHit != "" {
		score += 100
		reasons = append(reasons, fmt.Sprintf("input explicitly mentions %q", explicitHit))
	}

	seenReason := make(map[string]struct{})
	corpusSet := make(map[string]struct{}, len(corpusTokens))
	for _, token := range corpusTokens {
		corpusSet[token] = struct{}{}
	}
	for _, token := range inputTokens {
		if _, ok := corpusSet[token]; !ok {
			continue
		}
		if len([]rune(token)) >= 4 {
			score += 2
		} else {
			score += 1
		}
		if _, ok := seenReason[token]; !ok && len(reasons) < 3 {
			reasons = append(reasons, fmt.Sprintf("keyword overlap on %q", token))
			seenReason[token] = struct{}{}
		}
	}

	if score == 0 {
		return nil
	}

	preferredRun := ""
	runToolName := fmt.Sprintf("skill_%s_run", skill.Name)
	if a.hasModelVisibleTool(runToolName) {
		preferredRun = runToolName
	}

	return &skillRouteMatch{
		skill:        skill,
		score:        score,
		explicit:     explicitHit != "",
		reason:       strings.Join(reasons, "; "),
		preferredRun: preferredRun,
	}
}

func tokenizeSkillRouteText(input string) []string {
	raw := skillRouteTokenRe.FindAllString(strings.ToLower(input), -1)
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	tokens := make([]string, 0, len(raw))
	for _, token := range raw {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, stop := skillRouteStopwords[token]; stop {
			continue
		}
		if len([]rune(token)) < 2 {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func expandSkillRouteTokens(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tokens)*2)
	expanded := make([]string, 0, len(tokens)*2)
	for _, token := range tokens {
		token = strings.TrimSpace(strings.ToLower(token))
		if token == "" {
			continue
		}
		if _, ok := seen[token]; !ok {
			seen[token] = struct{}{}
			expanded = append(expanded, token)
		}
		if related, ok := skillRouteConceptMap[token]; ok {
			for _, alt := range related {
				alt = strings.TrimSpace(strings.ToLower(alt))
				if alt == "" {
					continue
				}
				if _, ok := seen[alt]; ok {
					continue
				}
				seen[alt] = struct{}{}
				expanded = append(expanded, alt)
			}
		}
		for concept, related := range skillRouteConceptMap {
			if token == concept || !strings.Contains(token, concept) {
				continue
			}
			if _, ok := seen[concept]; !ok {
				seen[concept] = struct{}{}
				expanded = append(expanded, concept)
			}
			for _, alt := range related {
				alt = strings.TrimSpace(strings.ToLower(alt))
				if alt == "" {
					continue
				}
				if _, ok := seen[alt]; ok {
					continue
				}
				seen[alt] = struct{}{}
				expanded = append(expanded, alt)
			}
		}
		for concept, related := range skillRouteConceptMap {
			matched := false
			for _, alt := range related {
				alt = strings.TrimSpace(strings.ToLower(alt))
				if alt == "" || !strings.Contains(token, alt) {
					continue
				}
				matched = true
				if _, ok := seen[concept]; !ok {
					seen[concept] = struct{}{}
					expanded = append(expanded, concept)
				}
			}
			if !matched {
				continue
			}
			for _, alt := range related {
				alt = strings.TrimSpace(strings.ToLower(alt))
				if alt == "" {
					continue
				}
				if _, ok := seen[alt]; ok {
					continue
				}
				seen[alt] = struct{}{}
				expanded = append(expanded, alt)
			}
		}
	}
	return expanded
}

func (a *Agent) hasModelVisibleTool(name string) bool {
	if a == nil || a.tools == nil || strings.TrimSpace(name) == "" {
		return false
	}
	t, ok := a.tools.Get(name)
	return ok && t != nil && t.Enabled && !t.HiddenFromModel
}

func (a *Agent) buildFunctionCallOptionsForInput(userInput string, tools []map[string]any) provider.CallOptions {
	callOpts := provider.CallOptions{
		Tools:      tools,
		ToolChoice: "auto",
	}

	if required := a.extractRequiredToolNames(userInput); len(required) > 0 {
		filtered := make([]map[string]any, 0, len(required))
		for _, name := range required {
			if t, ok := a.tools.Get(name); ok && t.Enabled {
				filtered = append(filtered, t.ToOpenAIFormat())
			}
		}
		if len(filtered) > 0 {
			callOpts.Tools = filtered
		}
		return callOpts
	}

	match := a.matchSkillRoute(userInput)
	if match == nil {
		return callOpts
	}
	if strings.EqualFold(strings.TrimSpace(match.skill.Name), "obsidian") && isLuckyHarnessMemoryBackendQuestion(userInput) {
		return callOpts
	}

	preferred := make([]string, 0, 2)
	if a.hasModelVisibleTool("skill_read") {
		preferred = append(preferred, "skill_read")
	}
	if match.preferredRun != "" {
		preferred = append(preferred, match.preferredRun)
	}
	if len(preferred) > 0 {
		callOpts.Tools = prioritizeFunctionTools(callOpts.Tools, preferred)
	}
	if match.explicit && a.hasModelVisibleTool("skill_read") {
		callOpts.ToolChoice = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "skill_read",
			},
		}
	}
	return callOpts
}

func prioritizeFunctionTools(tools []map[string]any, preferred []string) []map[string]any {
	if len(tools) == 0 || len(preferred) == 0 {
		return tools
	}

	preferredSet := make(map[string]int, len(preferred))
	for i, name := range preferred {
		if strings.TrimSpace(name) == "" {
			continue
		}
		preferredSet[name] = i
	}

	type entry struct {
		tool  map[string]any
		name  string
		order int
	}

	matched := make([]entry, 0, len(preferred))
	rest := make([]entry, 0, len(tools))
	for _, t := range tools {
		name := functionToolName(t)
		if order, ok := preferredSet[name]; ok {
			matched = append(matched, entry{tool: t, name: name, order: order})
		} else {
			rest = append(rest, entry{tool: t, name: name})
		}
	}

	slices.SortFunc(matched, func(a, b entry) int {
		return a.order - b.order
	})

	result := make([]map[string]any, 0, len(tools))
	for _, e := range matched {
		result = append(result, e.tool)
	}
	for _, e := range rest {
		result = append(result, e.tool)
	}
	return result
}

func functionToolName(toolSpec map[string]any) string {
	if toolSpec == nil {
		return ""
	}
	fn, ok := toolSpec["function"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := fn["name"].(string)
	return strings.TrimSpace(name)
}

func relaxForcedSkillToolChoice(messages []provider.Message, opts provider.CallOptions) provider.CallOptions {
	tc, ok := opts.ToolChoice.(map[string]any)
	if !ok {
		return opts
	}
	fn, ok := tc["function"].(map[string]any)
	if !ok {
		return opts
	}
	name, _ := fn["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return opts
	}
	if !hasUsedToolInMessages(messages, name) {
		return opts
	}
	opts.ToolChoice = "auto"
	return opts
}

func hasUsedToolInMessages(messages []provider.Message, toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return false
	}
	for _, msg := range messages {
		if strings.EqualFold(strings.TrimSpace(msg.Name), toolName) {
			return true
		}
		for _, tc := range msg.ToolCalls {
			if strings.EqualFold(strings.TrimSpace(tc.Name), toolName) {
				return true
			}
		}
	}
	return false
}
