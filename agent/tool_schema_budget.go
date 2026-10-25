package agent

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
)

// corePreferredTools receive a score bonus when the catalog must be trimmed.
// The bonus never excludes an enabled tool that still fits the schema budget.
var corePreferredTools = []string{
	"file_read", "file_list", "terminal", "web_search", "web_fetch",
	"ask_user", "context_history_read", "skill_read",
}

type toolSchemaChoice struct {
	index, cost, score int
	name               string
	required           bool
}

// Select complete schemas, preserving explicitly requested/forced tools and
// the current tool round. Optional schemas use at most one third of the input
// window; required schemas may borrow more while pinned context still fits.
// Every enabled schema is eligible. Relevance and the core-tool bonus only
// decide order once the full catalog no longer fits.
func (a *Agent) selectRequestTools(b *modelRequestBudget, messages []provider.Message, priorities []contextx.Message, opts provider.CallOptions, turnRequired []string) (provider.CallOptions, error) {
	if b == nil || len(opts.Tools) == 0 {
		return opts, nil
	}
	var query strings.Builder
	for i, msg := range messages {
		if msg.Role == "system" && strings.HasPrefix(msg.Content, "[Current Task]") {
			query.WriteString(msg.Content)
			query.WriteByte('\n')
		}
		if i < len(priorities) && priorities[i].Priority == contextx.PriorityCritical && msg.Role == "user" && !isTransientComputerObservation(msg) {
			query.WriteString(msg.Content)
			for _, part := range msg.ContentParts {
				if part.Type == "text" {
					query.WriteString(part.Text)
				}
			}
		}
	}
	text := query.String()
	required := makeToolNameSet(a.extractRequiredToolNames(text))
	if required == nil {
		required = map[string]struct{}{}
	}
	for _, name := range turnRequired {
		required[name] = struct{}{}
	}
	if forced := forcedToolChoiceName(opts.ToolChoice); forced != "" {
		required[forced] = struct{}{}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if len(messages[i].ToolCalls) > 0 {
			for _, call := range messages[i].ToolCalls {
				required[call.Name] = struct{}{}
			}
			break
		}
	}
	if len(opts.Tools) == 1 {
		required[functionToolNameFromSchema(opts.Tools[0])] = struct{}{}
	}
	preferred, _ := a.intentAllowedTools(text)
	if preferred == nil {
		preferred = map[string]struct{}{}
	}
	for _, name := range corePreferredTools {
		preferred[name] = struct{}{}
	}
	var pinned []provider.Message
	for i, priority := range priorities {
		if priority.Priority == contextx.PriorityCritical && i < len(messages) {
			pinned = append(pinned, messages[i])
		}
	}
	available := b.limit - estimateProviderRequest(b.est, pinned, nil)
	if available <= 0 {
		return opts, fmt.Errorf("current task and pinned instructions exceed context input budget %d", b.limit)
	}
	limit := b.limit / 3
	if limit > available {
		limit = available
	}
	entries := make([]toolSchemaChoice, 0, len(opts.Tools))
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	mandatory := 0
	for i, schema := range opts.Tools {
		name := functionToolNameFromSchema(schema)
		_, must := required[name]
		cost := estimateToolSchemas(b.est, []map[string]any{schema})
		score := 0
		if _, ok := preferred[name]; ok {
			score += 50
		}
		fn, _ := schema["function"].(map[string]any)
		description, _ := fn["description"].(string)
		haystack := strings.ToLower(name + " " + description)
		for _, word := range words {
			if len([]rune(word)) >= 3 && strings.Contains(haystack, word) {
				score++
			}
		}
		if must {
			mandatory += cost
		}
		entries = append(entries, toolSchemaChoice{index: i, cost: cost, score: score, name: name, required: must})
	}
	if mandatory > available {
		return opts, fmt.Errorf("required tool schemas need about %d tokens but only %d remain in context input budget %d", mandatory, available, b.limit)
	}
	if mandatory > limit {
		limit = mandatory
	}
	if estimateToolSchemas(b.est, opts.Tools) <= limit {
		return opts, nil
	}
	// Required tools are pinned. Remaining enabled schemas are admitted from
	// the smallest upward, so a score bonus cannot crowd one out. Leftover
	// budget then keeps the highest-scoring schemas that still fit.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].required != entries[j].required {
			return entries[i].required
		}
		if entries[i].cost != entries[j].cost {
			return entries[i].cost < entries[j].cost
		}
		return entries[i].index < entries[j].index
	})
	selected := make(map[int]bool, len(entries))
	used := 0
	for _, item := range entries {
		if item.required || used+item.cost <= limit {
			selected[item.index] = true
			used += item.cost
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].required != entries[j].required {
			return entries[i].required
		}
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return entries[i].index < entries[j].index
	})
	for _, item := range entries {
		if selected[item.index] || used+item.cost > limit {
			continue
		}
		selected[item.index] = true
		used += item.cost
	}
	kept := make([]string, 0, len(selected))
	dropped := make([]string, 0)
	for _, item := range entries {
		if selected[item.index] {
			kept = append(kept, schemaChoiceLabel(item))
			continue
		}
		dropped = append(dropped, schemaChoiceLabel(item))
	}
	if len(selected) == 0 {
		return opts, fmt.Errorf("no tool schema fits the %d-token schema allocation within context input budget %d", limit, b.limit)
	}
	logger.Info("tool schema budget trimmed request",
		"kept", len(selected),
		"dropped", len(dropped),
		"schema_tokens", used,
		"schema_limit", limit,
		"kept_tools", strings.Join(kept, ","),
		"dropped_tools", strings.Join(dropped, ","),
	)
	tools := make([]map[string]any, 0, len(selected))
	for i, schema := range opts.Tools {
		if selected[i] {
			tools = append(tools, schema)
		}
	}
	opts.Tools = tools
	opts.ToolChoice = normalizeToolChoiceForTools(opts.ToolChoice, tools)
	return opts, nil
}

func schemaChoiceLabel(item toolSchemaChoice) string {
	name := item.name
	if name == "" {
		name = fmt.Sprintf("#%d", item.index)
	}
	return fmt.Sprintf("%s:%d", name, item.cost)
}
