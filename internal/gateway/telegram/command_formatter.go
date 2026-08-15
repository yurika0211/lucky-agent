package telegram

import (
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/yurika0211/luckyagent/internal/tool"
)

const telegramCommandListPageSize = 10

// TelegramFormatter renders command responses directly as Telegram HTML.
// Dynamic tool and skill metadata is always escaped before it is inserted.
type TelegramFormatter struct{}

func (TelegramFormatter) FormatToolsList(tools []*tool.Tool, page int) string {
	tools = orderedTelegramTools(tools)
	pageTools, currentPage, pageCount := telegramListPage(tools, page)

	var body strings.Builder
	body.WriteString(fmt.Sprintf("🔧 <b>Available tools</b> (page %d/%d)\n\n", currentPage, pageCount))
	writeTelegramToolGroups(&body, pageTools)
	body.WriteString(telegramListNavigation("tools", "tool", currentPage, pageCount))
	return strings.TrimSpace(body.String())
}

func (TelegramFormatter) FormatSkillsList(skills []*tool.SkillInfo, page int) string {
	skills = orderedTelegramSkills(skills)
	pageSkills, currentPage, pageCount := telegramListPage(skills, page)

	var body strings.Builder
	body.WriteString(fmt.Sprintf("🎯 <b>Loaded skills</b> (page %d/%d)\n\n", currentPage, pageCount))
	for _, skill := range pageSkills {
		if skill == nil {
			continue
		}
		status := "✅"
		if !skill.Available {
			status = "❌"
		}
		body.WriteString(fmt.Sprintf("%s <code>%s</code> — %s\n",
			status,
			escapeTelegramCommandHTML(skill.Name),
			escapeTelegramCommandHTML(telegramCommandListSummary(skill.Description)),
		))
	}
	body.WriteString(telegramListNavigation("skills", "skill", currentPage, pageCount))
	return strings.TrimSpace(body.String())
}

func (TelegramFormatter) FormatToolDetail(item *tool.Tool) string {
	if item == nil {
		return "❌ <b>Tool not found.</b>"
	}

	var body strings.Builder
	body.WriteString(fmt.Sprintf("🔧 <b>%s</b>\n\n", escapeTelegramCommandHTML(item.Name)))
	body.WriteString("<b>Description</b>\n")
	body.WriteString(escapeTelegramCommandHTML(telegramCommandDescription(item.Description)))
	body.WriteString("\n\n")
	body.WriteString(fmt.Sprintf("<b>Status:</b> %s\n", telegramToolStatus(item.Enabled)))
	body.WriteString(fmt.Sprintf("<b>Category:</b> %s\n", telegramToolCategoryName(item.Category)))
	body.WriteString(fmt.Sprintf("<b>Permission:</b> %s\n", escapeTelegramCommandHTML(item.Permission.String())))
	if source := strings.TrimSpace(item.Source); source != "" {
		body.WriteString(fmt.Sprintf("<b>Source:</b> <code>%s</code>\n", escapeTelegramCommandHTML(source)))
	}
	body.WriteString("\n<b>Parameters</b>\n")
	writeTelegramParameters(&body, item.Parameters)
	body.WriteString("\n\n<b>Example</b>\n<code>")
	body.WriteString(escapeTelegramCommandHTML(telegramToolExample(item)))
	body.WriteString("</code>\n\n<b>Notes</b>\n")
	if item.Enabled {
		body.WriteString(fmt.Sprintf("Available to LuckyAgent; this tool requires %s permission and follows the configured approval policy.", escapeTelegramCommandHTML(item.Permission.String())))
	} else {
		body.WriteString("This tool is currently disabled and cannot be called.")
	}
	return body.String()
}

func (TelegramFormatter) FormatSkillDetail(skill *tool.SkillInfo) string {
	if skill == nil {
		return "❌ <b>Skill not found.</b>"
	}

	var body strings.Builder
	body.WriteString(fmt.Sprintf("🎯 <b>%s</b>\n\n", escapeTelegramCommandHTML(skill.Name)))
	body.WriteString("<b>Description</b>\n")
	body.WriteString(escapeTelegramCommandHTML(telegramCommandDescription(skill.Description)))
	body.WriteString("\n\n")
	body.WriteString(fmt.Sprintf("<b>Status:</b> %s\n", telegramSkillStatus(skill.Available)))
	if len(skill.Aliases) > 0 {
		aliases := make([]string, 0, len(skill.Aliases))
		for _, alias := range skill.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				aliases = append(aliases, "<code>"+escapeTelegramCommandHTML(alias)+"</code>")
			}
		}
		if len(aliases) > 0 {
			body.WriteString("<b>Aliases:</b> ")
			body.WriteString(strings.Join(aliases, ", "))
			body.WriteString("\n")
		}
	}
	if summary := strings.TrimSpace(skill.Summary); summary != "" && summary != strings.TrimSpace(skill.Description) {
		body.WriteString("\n<b>Summary</b>\n")
		body.WriteString(escapeTelegramCommandHTML(summary))
		body.WriteString("\n")
	}

	body.WriteString(fmt.Sprintf("\n<b>Provided tools (%d)</b>\n", len(skill.Tools)))
	if len(skill.Tools) == 0 {
		body.WriteString("None.")
		return body.String()
	}
	for _, item := range sortedTelegramSkillTools(skill.Tools) {
		body.WriteString(fmt.Sprintf("\n• <code>%s</code>", escapeTelegramCommandHTML(item.Name)))
		if item.ExposeToModel {
			body.WriteString(" — available to the agent")
		}
		body.WriteString("\n")
		body.WriteString(escapeTelegramCommandHTML(telegramCommandDescription(item.Description)))
		body.WriteString("\n")
		writeTelegramParameters(&body, item.Parameters)
	}
	return strings.TrimSpace(body.String())
}

func parseTelegramListPage(args string) (page int, showAll bool, err error) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		return 1, false, nil
	}
	if len(parts) != 1 {
		return 0, false, fmt.Errorf("expected one page number or all")
	}
	if strings.EqualFold(parts[0], "all") {
		return 1, true, nil
	}
	page, err = strconv.Atoi(parts[0])
	if err != nil || page < 1 {
		return 0, false, fmt.Errorf("page must be a positive number or all")
	}
	return page, false, nil
}

func telegramListPage[T any](items []T, requestedPage int) (pageItems []T, page int, pageCount int) {
	pageCount = (len(items) + telegramCommandListPageSize - 1) / telegramCommandListPageSize
	if pageCount == 0 {
		return nil, 1, 1
	}
	if requestedPage < 1 {
		requestedPage = 1
	}
	if requestedPage > pageCount {
		requestedPage = pageCount
	}
	start := (requestedPage - 1) * telegramCommandListPageSize
	end := start + telegramCommandListPageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], requestedPage, pageCount
}

func telegramListNavigation(listCommand, detailCommand string, page, pageCount int) string {
	var body strings.Builder
	body.WriteString("\n<b>More</b>\n")
	if page > 1 {
		body.WriteString(fmt.Sprintf("• <code>/%s %d</code> — previous page\n", listCommand, page-1))
	}
	if page < pageCount {
		body.WriteString(fmt.Sprintf("• <code>/%s %d</code> — next page\n", listCommand, page+1))
	}
	body.WriteString(fmt.Sprintf("• <code>/%s all</code> — show all pages\n", listCommand))
	body.WriteString(fmt.Sprintf("• <code>/%s &lt;name&gt;</code> — show details", detailCommand))
	return body.String()
}

func writeTelegramToolGroups(body *strings.Builder, tools []*tool.Tool) {
	for index := 0; index < len(tools); {
		category := tools[index].Category
		end := index + 1
		for end < len(tools) && tools[end].Category == category {
			end++
		}
		body.WriteString(fmt.Sprintf("<b>%s (%d)</b>\n", telegramToolCategoryName(category), end-index))
		for _, item := range tools[index:end] {
			body.WriteString(fmt.Sprintf("%s <code>%s</code> — %s\n",
				telegramToolStatus(item.Enabled),
				escapeTelegramCommandHTML(item.Name),
				escapeTelegramCommandHTML(telegramCommandListSummary(item.Description)),
			))
		}
		body.WriteString("\n")
		index = end
	}
}

func writeTelegramParameters(body *strings.Builder, parameters map[string]tool.Param) {
	if len(parameters) == 0 {
		body.WriteString("None.")
		return
	}
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parameter := parameters[name]
		requirement := "optional"
		if parameter.Required {
			requirement = "required"
		}
		body.WriteString(fmt.Sprintf("• <code>%s</code> (%s", escapeTelegramCommandHTML(name), requirement))
		if parameter.Type != "" {
			body.WriteString(", ")
			body.WriteString(escapeTelegramCommandHTML(parameter.Type))
		}
		body.WriteString(")")
		if description := strings.TrimSpace(parameter.Description); description != "" {
			body.WriteString(" — ")
			body.WriteString(escapeTelegramCommandHTML(description))
		}
		if parameter.Default != nil {
			body.WriteString(" (default: <code>")
			body.WriteString(escapeTelegramCommandHTML(fmt.Sprint(parameter.Default)))
			body.WriteString("</code>)")
		}
		body.WriteString("\n")
	}
}

func telegramToolExample(item *tool.Tool) string {
	if item == nil {
		return "tool_name()"
	}
	names := make([]string, 0, len(item.Parameters))
	for name, parameter := range item.Parameters {
		if parameter.Required {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		for name := range item.Parameters {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) > 1 && !hasRequiredTelegramParameter(item.Parameters) {
		names = names[:1]
	}
	arguments := make([]string, 0, len(names))
	for _, name := range names {
		parameter := item.Parameters[name]
		arguments = append(arguments, name+"="+telegramExampleValue(parameter))
	}
	return item.Name + "(" + strings.Join(arguments, ", ") + ")"
}

func hasRequiredTelegramParameter(parameters map[string]tool.Param) bool {
	for _, parameter := range parameters {
		if parameter.Required {
			return true
		}
	}
	return false
}

func telegramExampleValue(parameter tool.Param) string {
	if parameter.Default != nil {
		return fmt.Sprint(parameter.Default)
	}
	switch strings.ToLower(strings.TrimSpace(parameter.Type)) {
	case "bool", "boolean":
		return "false"
	case "int", "integer", "float", "number":
		return "0"
	case "array", "list", "slice":
		return "[]"
	case "object", "map":
		return "{}"
	default:
		return `"value"`
	}
}

func orderedTelegramTools(items []*tool.Tool) []*tool.Tool {
	tools := make([]*tool.Tool, 0, len(items))
	for _, item := range items {
		if item != nil {
			tools = append(tools, item)
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		left, right := telegramToolCategoryOrder(tools[i].Category), telegramToolCategoryOrder(tools[j].Category)
		if left != right {
			return left < right
		}
		return tools[i].Name < tools[j].Name
	})
	return tools
}

func orderedTelegramSkills(items []*tool.SkillInfo) []*tool.SkillInfo {
	skills := make([]*tool.SkillInfo, 0, len(items))
	for _, item := range items {
		if item != nil {
			skills = append(skills, item)
		}
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills
}

func sortedTelegramSkillTools(items []tool.SkillToolDef) []tool.SkillToolDef {
	tools := append([]tool.SkillToolDef(nil), items...)
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

func telegramToolCategoryName(category tool.Category) string {
	switch category {
	case tool.CatBuiltin:
		return "Built-in"
	case tool.CatSkill:
		return "Skill tools"
	case tool.CatMCP:
		return "MCP tools"
	case tool.CatDelegate:
		return "Delegation"
	default:
		return "Other"
	}
}

func telegramToolCategoryOrder(category tool.Category) int {
	switch category {
	case tool.CatBuiltin:
		return 0
	case tool.CatSkill:
		return 1
	case tool.CatMCP:
		return 2
	case tool.CatDelegate:
		return 3
	default:
		return 4
	}
}

func telegramToolStatus(enabled bool) string {
	if enabled {
		return "✅ enabled"
	}
	return "❌ disabled"
}

func telegramSkillStatus(available bool) string {
	if available {
		return "✅ available"
	}
	return "❌ unavailable"
}

func telegramCommandListSummary(value string) string {
	value = telegramCommandDescription(value)
	const limit = 96
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-3]) + "..."
}

func telegramCommandDescription(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "No description provided."
	}
	return value
}

func escapeTelegramCommandHTML(value string) string {
	return html.EscapeString(value)
}
