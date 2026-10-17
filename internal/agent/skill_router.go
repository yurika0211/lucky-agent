package agent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yurika0211/luckyagent-tools/skillroute"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type skillRouteMatch struct {
	skill        *tool.SkillInfo
	score        int
	explicit     bool
	reason       string
	preferredRun string
}

type skillRouteOptions struct {
	DisabledTools []string
}

func (a *Agent) buildSkillRouteSystemHint(userInput string) string {
	return a.buildSkillRouteSystemHintWithOptions(userInput, skillRouteOptions{})
}

func (a *Agent) buildSkillRouteSystemHintWithOptions(userInput string, opts skillRouteOptions) string {
	match := a.matchSkillRoute(userInput)
	if match == nil || match.skill == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(match.skill.Name), "obsidian") && isLuckyAgentMemoryBackendQuestion(userInput) {
		return `Skill routing note:
- This request is about the LuckyAgent memory backend, not an external Obsidian app vault.
- Do not use the Obsidian skill or OBSIDIAN_VAULT_PATH to decide whether LuckyAgent memory exists unless the user explicitly asks to operate an external Obsidian vault.
- Use recall, local LuckyAgent memory vault files, or runtime inspection as direct evidence.`
	}

	var lines []string
	lines = append(lines, "Skill routing hint:")
	lines = append(lines, fmt.Sprintf("- This request strongly matches the %q skill.", match.skill.Name))
	if match.reason != "" {
		lines = append(lines, "- Reason: "+match.reason)
	}
	lines = append(lines, "- Prefer this skill workflow before ad-hoc reasoning if it fits the task after inspection.")
	if a.hasModelVisibleToolExcept("skill_read", opts.DisabledTools) {
		lines = append(lines, fmt.Sprintf("- First inspect the skill guidance with skill_read(action=\"read\", name=%q, detail=\"summary\"). Use detail=\"full\" only if the summary is insufficient.", match.skill.Name))
	}
	if preferredRun := a.visibleSkillRunTool(match.skill.Name, opts.DisabledTools); preferredRun != "" {
		lines = append(lines, fmt.Sprintf("- Preferred execution entry: %s", preferredRun))
	}
	if summary := routeFriendlySkillSummary(match.skill); summary != "" {
		lines = append(lines, "- Skill summary: "+summary)
	}
	lines = append(lines, "- Do not ignore better direct evidence, but bias toward this skill workflow unless the task clearly does not fit.")
	return strings.Join(lines, "\n")
}

func isLuckyAgentMemoryBackendQuestion(input string) bool {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return false
	}
	hasMemory := strings.Contains(lower, "memory") ||
		strings.Contains(lower, "记忆") ||
		strings.Contains(lower, "回忆") ||
		strings.Contains(lower, "recall") ||
		strings.Contains(lower, "remember")
	hasBackend := strings.Contains(lower, "luckyagent") ||
		strings.Contains(lower, "记忆系统") ||
		strings.Contains(lower, "记忆库") ||
		strings.Contains(lower, "存储") ||
		strings.Contains(lower, "后端") ||
		strings.Contains(lower, "取代") ||
		strings.Contains(lower, "生效") ||
		strings.Contains(lower, "双链记忆")
	return hasMemory && hasBackend
}

// matchSkillRoute takes one skill snapshot and delegates matching to the
// reusable router. Host-specific tool visibility and presentation stay here.
func (a *Agent) matchSkillRoute(userInput string) *skillRouteMatch {
	if a == nil {
		return nil
	}
	skills := a.snapshotSkills()
	descriptors := make([]skillroute.Descriptor, len(skills))
	for i, skill := range skills {
		if skill == nil {
			continue
		}
		descriptors[i] = skillroute.Descriptor{Name: skill.Name, Aliases: skill.Aliases, Description: skill.Description, Summary: routeFriendlySkillSummary(skill)}
	}
	matched := skillroute.Match(userInput, descriptors)
	if matched == nil {
		return nil
	}
	skill := skills[matched.Index]
	preferred := ""
	if name := fmt.Sprintf("skill_%s_run", skill.Name); a.hasModelVisibleTool(name) {
		preferred = name
	}
	return &skillRouteMatch{skill: skill, score: matched.Score, explicit: matched.Explicit, reason: matched.Reason, preferredRun: preferred}
}

func (a *Agent) hasModelVisibleTool(name string) bool {
	return a.hasModelVisibleToolExcept(name, nil)
}

func (a *Agent) hasModelVisibleToolExcept(name string, disabled []string) bool {
	if a == nil || a.tools == nil || strings.TrimSpace(name) == "" {
		return false
	}
	if toolNameInSet(makeToolNameSet(disabled), name) {
		return false
	}
	t, ok := a.tools.Get(name)
	return ok && t != nil && t.Enabled && !t.HiddenFromModel
}

func (a *Agent) visibleSkillRunTool(skillName string, disabled []string) string {
	runToolName := fmt.Sprintf("skill_%s_run", strings.TrimSpace(skillName))
	if a.hasModelVisibleToolExcept(runToolName, disabled) {
		return runToolName
	}
	return ""
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
	if strings.EqualFold(strings.TrimSpace(match.skill.Name), "obsidian") && isLuckyAgentMemoryBackendQuestion(userInput) {
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
