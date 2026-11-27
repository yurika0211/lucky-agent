package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yurika0211/lucky-agent/agent/intentclassify"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/memory"
	"github.com/yurika0211/luckyagent-tools/skillroute"
)

// ErrPreferenceLookup is returned when long-term preference memory cannot be
// read. Callers must not treat it as "the user has no preference".
var ErrPreferenceLookup = errors.New("long-term preference lookup failed")

type preferenceLookupError struct{ cause error }

func (e *preferenceLookupError) Error() string {
	if e == nil || e.cause == nil {
		return ErrPreferenceLookup.Error()
	}
	return ErrPreferenceLookup.Error() + ": " + e.cause.Error()
}
func (e *preferenceLookupError) Unwrap() error { return ErrPreferenceLookup }

type skillRouteMatch struct {
	skill            *tool.SkillInfo
	score            int
	explicit         bool
	reason           string
	preferredRun     string
	preferredMissing bool
	preferenceErr    error
}

type skillRouteOptions struct {
	DisabledTools []string
}

func (a *Agent) buildSkillRouteSystemHint(userInput string) string {
	return a.buildSkillRouteSystemHintWithOptions(userInput, skillRouteOptions{})
}

func (a *Agent) buildSkillRouteSystemHintWithOptions(userInput string, opts skillRouteOptions) string {
	return a.skillRouteHint(userInput, opts, true)
}

// skillRouteHint renders the same hint the live turn injects. record is false
// for a read-only compact comparison so matching does not write route metrics.
func (a *Agent) skillRouteHint(userInput string, opts skillRouteOptions, record bool) string {
	if a == nil {
		return ""
	}
	match := a.matchSkillRoute(userInput)
	if match == nil || match.skill == nil {
		return ""
	}
	if record {
		a.recordMatchedSkillRoute(match, userInput)
		if match.preferenceErr != nil || match.preferredMissing {
			if _, _, err := a.resolvePreferredSkillInstall(context.Background(), "", userInput); err != nil && match.preferenceErr == nil {
				match.preferenceErr = err
			}
		}
	}
	if match.preferenceErr != nil {
		return "Skill routing note:\n- Long-term preference memory could not be read. Do not treat this as the user having no preference, and do not install or substitute another skill."
	}
	if match.preferredMissing && match.skill != nil {
		return fmt.Sprintf("Skill routing note:\n- The user's long-term preference names skill %q, which is not installed.\n- Explain the missing skill and its install source, then wait for an exact approval before installing it.\n- If the user refuses, stop and ask what to do next. Do not continue with a different skill.", match.skill.Name)
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
	if !match.explicit && !strings.EqualFold(strings.TrimSpace(match.reason), "long-term preference") {
		candidates := a.skillRouteCandidates(userInput)
		if len(candidates) >= 2 {
			lines = append(lines, fmt.Sprintf("- No long-term preference is available. Candidate skills are %q and %q.", candidates[0].skill.Name, candidates[1].skill.Name))
			if a.hasModelVisibleTool(skillComparisonToolName) {
				lines = append(lines, "- If and only if the task is idempotent and safe to repeat, call skill_compare with these two candidates and safe_to_repeat=true. It runs isolated children, compares completion evidence first, then score = cost × time; an incomplete, partial, failed, or unmeasured result cannot win. Never double-run messages, deployments, purchases, or other non-idempotent effects; choose one instead.")
			} else {
				lines = append(lines, "- If and only if the task is idempotent and safe to repeat, run both candidates with the same goal and comparable budget in isolated child tasks. Compare completion evidence first, then compare score = cost × time; an incomplete or partial result cannot win. Never double-run messages, deployments, purchases, or other non-idempotent effects; choose one instead.")
			}
		}
	}
	lines = append(lines, "- Do not ignore better direct evidence, but bias toward this skill workflow unless the task clearly does not fit.")
	return strings.Join(lines, "\n")
}

func isLuckyAgentMemoryBackendQuestion(input string) bool {
	return intentclassify.Classify(input).Has(intentclassify.LabelLuckyAgentMemory)
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
	preferred, prefErr := a.preferredSkillFromMemory(userInput, skills)
	if prefErr != nil {
		return &skillRouteMatch{skill: skill, score: matched.Score, explicit: matched.Explicit, reason: "preference lookup failed", preferredRun: a.visibleSkillRunTool(skill.Name, nil), preferenceErr: prefErr}
	}
	if preferred != nil {
		if !a.skillInstalled(preferred) {
			return &skillRouteMatch{skill: preferred, score: matched.Score + 1000, reason: "preferred skill not installed", preferredMissing: true}
		}
		skill = preferred
		return &skillRouteMatch{skill: skill, score: matched.Score + 1000, reason: "long-term preference", preferredRun: a.visibleSkillRunTool(skill.Name, nil)}
	}
	runTool := a.visibleSkillRunTool(skill.Name, nil)
	return &skillRouteMatch{skill: skill, score: matched.Score, explicit: matched.Explicit, reason: matched.Reason, preferredRun: runTool}
}

func (a *Agent) skillRouteCandidates(userInput string) []*skillRouteMatch {
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
	ranked := skillroute.Matches(userInput, descriptors)
	result := make([]*skillRouteMatch, 0, 2)
	for _, candidate := range ranked {
		if candidate == nil || candidate.Index < 0 || candidate.Index >= len(skills) || skills[candidate.Index] == nil {
			continue
		}
		result = append(result, &skillRouteMatch{
			skill: skills[candidate.Index], score: candidate.Score, explicit: candidate.Explicit,
			reason: candidate.Reason, preferredRun: a.visibleSkillRunTool(skills[candidate.Index].Name, nil),
		})
		if len(result) == 2 {
			break
		}
	}
	return result
}

func (a *Agent) preferredSkillFromMemory(userInput string, skills []*tool.SkillInfo) (*tool.SkillInfo, error) {
	if a == nil || a.memory == nil || strings.TrimSpace(userInput) == "" {
		return nil, nil
	}
	entries, err := a.longTermPreferenceEntries()
	if err != nil {
		return nil, err
	}
	return preferredSkillFromEntries(userInput, skills, entries), nil
}

// longTermPreferenceEntries reads long-term memory. An empty result with a nil
// error means no preference was stored. A non-nil error means the read failed.
func (a *Agent) longTermPreferenceEntries() ([]memory.Entry, error) {
	if a == nil || a.memory == nil {
		return nil, nil
	}
	if reader, ok := a.memory.(interface {
		ByTierWithOptionsErr(memory.Tier, memory.RouteOptions) ([]memory.Entry, error)
	}); ok {
		entries, err := reader.ByTierWithOptionsErr(memory.TierLong, memory.RouteOptions{})
		if err != nil {
			return nil, &preferenceLookupError{cause: err}
		}
		return entries, nil
	}
	return a.memory.ByTierWithOptions(memory.TierLong, memory.RouteOptions{}), nil
}

func (a *Agent) skillInstalled(skill *tool.SkillInfo) bool {
	if skill == nil || strings.TrimSpace(skill.Name) == "" || a == nil {
		return false
	}
	for _, installed := range a.snapshotSkills() {
		if installed != nil && strings.EqualFold(strings.TrimSpace(installed.Name), strings.TrimSpace(skill.Name)) && strings.TrimSpace(installed.Dir) != "" {
			return true
		}
	}
	return false
}

// resolvePreferredSkillInstall asks before installing a preferred skill that
// is not on disk. Refusal returns blocked=true and does not select a substitute.
func (a *Agent) resolvePreferredSkillInstall(ctx context.Context, sessionID, userInput string) (blocked bool, notice string, err error) {
	match := a.matchSkillRoute(userInput)
	if match == nil {
		return false, "", nil
	}
	if match.preferenceErr != nil {
		return true, "长期偏好读取失败，不能当成没有偏好，也不会改用其他技能。", match.preferenceErr
	}
	if !match.preferredMissing || match.skill == nil {
		return false, "", nil
	}
	name := strings.TrimSpace(match.skill.Name)
	source := "未记录安装来源"
	if dir := strings.TrimSpace(match.skill.Dir); dir != "" {
		source = dir
	}
	reason := fmt.Sprintf("长期偏好指定的 skill %q 尚未安装。来源：%s。批准后才会安装；拒绝后不会改用其他技能。", name, source)
	allowed, _, gateErr := a.waitHITLApproval(ctx, sessionID, "skill_install", map[string]any{
		"skill": name, "source": source,
	}, reason, "skill_install")
	if gateErr != nil {
		return true, reason, gateErr
	}
	if !allowed {
		return true, fmt.Sprintf("未安装 skill %q。请说明接下来怎么处理；不会改用其他技能。", name), nil
	}
	if a.skillInstaller == nil {
		return true, fmt.Sprintf("已批准安装 skill %q，但当前没有安装器，未做任何安装。", name), fmt.Errorf("skill installer unavailable")
	}
	if err := a.skillInstaller(ctx, name, source); err != nil {
		return true, fmt.Sprintf("安装 skill %q 失败：%s", name, err.Error()), err
	}
	return false, fmt.Sprintf("已按批准安装 skill %q。", name), nil
}

func preferredSkillFromEntries(userInput string, skills []*tool.SkillInfo, entries []memory.Entry) *tool.SkillInfo {
	if strings.TrimSpace(userInput) == "" || len(skills) == 0 || len(entries) == 0 {
		return nil
	}
	// A user-authored preference always outranks a system comparison. The
	// latter is useful only when the user has not stated a preference; treating
	// both as equal would let an automated experiment silently override it.
	userPreferred := preferredSkillFromEntriesFiltered(userInput, skills, entries, false)
	if userPreferred != nil {
		return userPreferred
	}
	return preferredSkillFromEntriesFiltered(userInput, skills, entries, true)
}

func preferredSkillFromEntriesFiltered(userInput string, skills []*tool.SkillInfo, entries []memory.Entry, comparisonOnly bool) *tool.SkillInfo {
	var selected *tool.SkillInfo
	bestImportance := -1.0
	for _, entry := range entries {
		if !strings.EqualFold(strings.TrimSpace(entry.Category), "preference") || (entry.Status != "" && !strings.EqualFold(entry.Status, "active")) {
			continue
		}
		isComparison := false
		for _, tag := range entry.Tags {
			if strings.EqualFold(strings.TrimSpace(tag), "system_comparison") {
				isComparison = true
				break
			}
		}
		if comparisonOnly != isComparison {
			continue
		}
		content := strings.ToLower(entry.Content)
		for _, skill := range skills {
			if skill == nil || !skillMatchesText(content, skill) || !skillRouteMatchesInput(userInput, skill) {
				continue
			}
			if entry.Importance > bestImportance {
				selected, bestImportance = skill, entry.Importance
			}
		}
	}
	return selected
}

func skillMatchesText(content string, skill *tool.SkillInfo) bool {
	if skill == nil {
		return false
	}
	for _, value := range append([]string{skill.Name, skill.Description, skill.Summary}, skill.Aliases...) {
		value = strings.TrimSpace(value)
		if value != "" && strings.Contains(content, strings.ToLower(value)) {
			return true
		}
	}
	return false
}

func skillRouteMatchesInput(input string, skill *tool.SkillInfo) bool {
	if skill == nil {
		return false
	}
	descriptor := skillroute.Descriptor{Name: skill.Name, Aliases: skill.Aliases, Description: skill.Description, Summary: routeFriendlySkillSummary(skill)}
	return skillroute.Match(input, []skillroute.Descriptor{descriptor}) != nil
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
	if strings.Contains(skillName, ":") {
		name := pluginResourceName("plugin_" + strings.ReplaceAll(skillName, ":", "_") + "_run")
		if a.hasModelVisibleToolExcept(name, disabled) {
			return name
		}
	}
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
	a.recordMatchedSkillRoute(match, userInput)
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
