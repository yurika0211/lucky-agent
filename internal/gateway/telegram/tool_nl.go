package telegram

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

type telegramToolTraceStep struct {
	Name    string
	Args    string
	Result  string
	Success bool
}

func humanizeThinkingProgress(content string) string {
	content = clipOneLine(content, 180)
	if content == "" {
		return ""
	}
	if isInternalThinkingProgress(content) {
		return ""
	}
	return ensureSentenceSuffix("先整理一下当前思路：" + content)
}

func renderTelegramThinkingCard(content string) string {
	content = compactProgressCardText(content)
	if content == "" || isInternalThinkingProgress(content) {
		return ""
	}
	return "<b>💭 Reasoning Trace</b>\n<blockquote expandable>" + html.EscapeString(content) + "</blockquote>"
}

func renderTelegramSummaryCard(summary string) string {
	summary = compactProgressCardText(summary)
	if summary == "" {
		return ""
	}
	return "<b>💭 Reasoning Trace</b>\n<blockquote expandable>" + html.EscapeString(summary) + "</blockquote>"
}

func renderTelegramProgressHistoryCard(parts []string) string {
	body := make([]string, 0, len(parts))
	for _, part := range parts {
		part = compactProgressCardText(part)
		if part == "" {
			continue
		}
		body = append(body, html.EscapeString(part))
	}
	if len(body) == 0 {
		return ""
	}
	return "<b>💭 Reasoning Trace</b>\n<blockquote expandable>" + strings.Join(body, "\n") + "</blockquote>"
}

func compactProgressCardText(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func renderTelegramToolTraceCard(steps []telegramToolTraceStep) string {
	segments := make([]string, 0, len(steps))
	shown := 0
	for _, step := range steps {
		visibility := telegramToolTraceVisibility(step.Name)
		if visibility == "hidden" || visibility == "agent" {
			continue
		}
		line := formatTelegramToolTraceLine(shown+1, step, visibility)
		if strings.TrimSpace(line) == "" {
			continue
		}
		segments = append(segments, line)
		shown++
		if shown >= 6 {
			break
		}
	}
	if shown == 0 {
		return ""
	}
	body := strings.Join(segments, "  →  ")
	body += "\nDone · " + fmt.Sprintf("%d tools", shown)
	return "<b>🛠 Tool Trace</b>\n<pre><code>" + html.EscapeString(body) + "</code></pre>"
}

func renderTelegramAgentTraceCard(steps []telegramToolTraceStep) string {
	segments := make([]string, 0, len(steps))
	shown := 0
	for _, step := range steps {
		if telegramToolTraceVisibility(step.Name) != "agent" {
			continue
		}
		line := formatTelegramAgentTraceLine(shown+1, step)
		if strings.TrimSpace(line) == "" {
			continue
		}
		segments = append(segments, line)
		shown++
		if shown >= 6 {
			break
		}
	}
	if shown == 0 {
		return ""
	}
	body := strings.Join(segments, "\n")
	body += "\nDone · " + fmt.Sprintf("%d agent steps", shown)
	return "<b>🧭 Agent Trace</b>\n<pre><code>" + html.EscapeString(body) + "</code></pre>"
}

func humanizeToolCallProgress(step int, name, args string) string {
	if step <= 0 {
		step = 1
	}
	n := strings.TrimSpace(name)
	if n == "" {
		n = "unknown_tool"
	}
	lowerName := strings.ToLower(n)
	parsed := parseToolCallArgs(args)

	if strings.Contains(lowerName, "skill") {
		return "先接入现成的处理链路，确认这一步该怎么处理。"
	}

	switch lowerName {
	case "web_search":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("先查一轮公开资料，重点看 %s。", quoteAndTrim(q, 90))
		}
		return "先查一轮公开资料，看看有没有可靠线索。"
	case "web_fetch":
		if u := pickArg(parsed, "url", "uri", "link"); u != "" {
			return fmt.Sprintf("先打开原文核对细节：%s。", clipOneLine(u, 100))
		}
		return "先读取网页正文，确认原文内容。"
	case "shell":
		if cmd := pickArg(parsed, "cmd", "command", "script"); cmd != "" {
			return fmt.Sprintf("先在终端核对现状，执行：%s。", clipOneLine(cmd, 100))
		}
		return "先在终端核对当前环境状态。"
	case "file_read":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("先查看文件 %s，确认当前内容。", clipOneLine(p, 100))
		}
		return "先读取相关文件，确认判断依据。"
	case "file_write":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("先把修改写入 %s。", clipOneLine(p, 100))
		}
		return "先把这次改动真正落盘。"
	case "recall":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("先回看之前的上下文，确认 %s 有没有旧线索。", quoteAndTrim(q, 90))
		}
		return "先回看之前的上下文，避免漏掉已有信息。"
	case "remember":
		return "先把这条信息记下来，后面可以直接复用。"
	case "current_time":
		return "先确认当前时间和时区，避免时间线出错。"
	}

	if keyHint := pickArg(parsed,
		"query", "url", "path", "cmd", "command",
		"title", "task_id", "name"); keyHint != "" {
		return fmt.Sprintf("先用工具 %s 处理 %s，补齐这一步所需信息。", n, clipOneLine(keyHint, 100))
	}
	return fmt.Sprintf("先调用 %s，补齐这一小段信息。", n)
}

func humanizeToolResultProgress(step int, name, result string) string {
	if step <= 0 {
		step = 1
	}
	n := strings.TrimSpace(name)
	lowerName := strings.ToLower(n)

	switch lowerName {
	case "web_search":
		return "搜索结果已经拿到，开始筛选有效信息。"
	case "web_fetch":
		return "原文已经读完，开始提取关键细节。"
	case "shell":
		return "命令已经执行完，正在根据输出继续判断。"
	case "file_read":
		return "文件已经看过，开始收拢重点。"
	case "file_write":
		return "改动已经写入，接着做一轮确认。"
	case "recall":
		return "之前的上下文已经回看完，继续往下收束。"
	case "remember":
		return "这条信息已经记下，后面可以直接复用。"
	case "current_time":
		return "时间线已经核对完成。"
	}

	if strings.Contains(lowerName, "skill") {
		return "处理链路已经跑完，开始收束结论。"
	}

	if summary := humanizeToolResult(name, result); summary != "" {
		summary = strings.TrimPrefix(summary, "我")
		summary = strings.TrimSpace(summary)
		return ensureSentenceSuffix("这一步已经完成，" + summary)
	}
	return "这一步已经有结果，继续往下整理。"
}

func wrapFinalConclusion(finalOutput string) string {
	finalOutput = strings.TrimSpace(finalOutput)
	if finalOutput == "" {
		return "这轮暂时还没有整理出能直接给你的结论。"
	}
	return finalOutput
}

func humanizeToolCall(name, args string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		n = "unknown_tool"
	}

	parsed := parseToolCallArgs(args)

	switch n {
	case "web_search":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("正在联网搜索：%s", quoteAndTrim(q, 80))
		}
		return "正在联网搜索相关信息"

	case "web_fetch":
		if u := pickArg(parsed, "url", "uri", "link"); u != "" {
			return fmt.Sprintf("正在读取网页：%s", clipOneLine(u, 90))
		}
		return "正在读取网页内容"

	case "shell":
		if cmd := pickArg(parsed, "cmd", "command", "script"); cmd != "" {
			return fmt.Sprintf("正在执行命令：%s", clipOneLine(cmd, 90))
		}
		return "正在执行终端命令"

	case "file_read":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("正在读取文件：%s", clipOneLine(p, 90))
		}
		return "正在读取文件"

	case "file_write":
		if p := pickArg(parsed, "path", "file", "filepath", "filename"); p != "" {
			return fmt.Sprintf("正在写入文件：%s", clipOneLine(p, 90))
		}
		return "正在写入文件"

	case "current_time":
		return "正在查询当前时间"

	case "remember":
		if content := pickArg(parsed, "content", "text", "memory"); content != "" {
			return fmt.Sprintf("正在保存记忆：%s", clipOneLine(content, 80))
		}
		return "正在保存记忆"

	case "recall":
		if q := pickArg(parsed, "query", "q", "keyword"); q != "" {
			return fmt.Sprintf("正在检索记忆：%s", quoteAndTrim(q, 80))
		}
		return "正在检索记忆"
	}

	if keyHint := pickArg(parsed,
		"query", "url", "path", "cmd", "command",
		"title", "task_id", "name"); keyHint != "" {
		return fmt.Sprintf("正在调用工具 %s：%s", n, clipOneLine(keyHint, 90))
	}
	return fmt.Sprintf("正在调用工具 %s", n)
}

func humanizeToolResult(name, result string) string {
	n := strings.TrimSpace(name)
	text := clipOneLine(result, 160)
	if text == "" {
		return ""
	}

	switch n {
	case "web_search":
		if strings.Contains(text, "Results for:") {
			return "已经拿到一批相关结果，里面有几个比较可靠的入口。"
		}
		if strings.Contains(strings.ToLower(text), "no results found") {
			return "已经搜过一轮，但这次还没有拿到像样的结果。"
		}
		return "已经查到一批和这个问题比较接近的资料。"

	case "web_fetch":
		if strings.Contains(strings.ToLower(text), "failed to fetch") {
			return "尝试抓取网页正文，但这页没有顺利拿到。"
		}
		return "网页正文已经过了一遍，里面有几处细节可以直接使用。"

	case "file_read":
		return "已经找到文件里的关键片段。"

	case "recall":
		if strings.Contains(text, "没有找到") {
			return "已经回看过之前的记录，但这件事暂时没有现成线索。"
		}
		return "已经把之前相关的上下文顺了一遍。"

	case "current_time":
		return "已经把时间信息核对过了。"
	}

	return fmt.Sprintf("已经拿到 %s 这一步的结果。", n)
}

func telegramToolTraceVisibility(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case lower == "":
		return "visible"
	case lower == "skill_read",
		lower == "remember",
		lower == "recall",
		lower == "rag_search",
		lower == "rag_index",
		strings.HasPrefix(lower, "cron"):
		return "compact"
	case strings.HasPrefix(lower, "skill_") && strings.HasSuffix(lower, "_run"):
		return "compact"
	case strings.HasPrefix(lower, "delegate_"),
		strings.HasPrefix(lower, "autonomy_"),
		strings.HasPrefix(lower, "heartbeat_"):
		return "agent"
	case strings.HasPrefix(lower, "skill_"):
		return "hidden"
	default:
		return "visible"
	}
}

func formatTelegramToolTraceLine(index int, step telegramToolTraceStep, visibility string) string {
	name := strings.TrimSpace(step.Name)
	if name == "" {
		name = "unknown_tool"
	}
	status := "✅"
	if !step.Success {
		status = "⚠️"
	}
	displayName := name
	if visibility == "compact" {
		displayName = compactToolTraceName(name)
	}
	return fmt.Sprintf("[%d] %s %s", index, displayName, status)
}

func formatTelegramAgentTraceLine(index int, step telegramToolTraceStep) string {
	name := strings.TrimSpace(step.Name)
	if name == "" {
		name = "unknown_agent_step"
	}
	status := "✅"
	if !step.Success {
		status = "⚠️"
	}
	return fmt.Sprintf("[%d] %s %s %s", index, compactAgentTraceName(name), compactAgentTraceDetail(step.Args), status)
}

func compactToolTraceName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "unknown_tool"
	}
	return name
}

func compactAgentTraceName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(lower, "delegate_"):
		return "delegate"
	case strings.HasPrefix(lower, "autonomy_"):
		return "autonomy"
	case strings.HasPrefix(lower, "heartbeat_"):
		return "heartbeat"
	default:
		return name
	}
}

func compactAgentTraceDetail(args string) string {
	parsed := parseToolCallArgs(args)
	if mode := pickArg(parsed, "mode", "agent", "agent_id", "role"); mode != "" {
		return clipOneLine(mode, 48)
	}
	if task := pickArg(parsed, "task", "title", "name", "command", "query", "prompt"); task != "" {
		return clipOneLine(task, 48)
	}
	return ""
}

func isInternalThinkingProgress(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	if extractRoundNumber(trimmed) > 0 {
		return true
	}

	lower := strings.ToLower(strings.Trim(trimmed, " .!"))
	return lower == "thinking" || lower == "thinking.."
}

func parseToolCallArgs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil
	}
	return m
}

func pickArg(args map[string]any, keys ...string) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range keys {
		v, ok := args[key]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			if s := strings.TrimSpace(x); s != "" {
				return s
			}
		case []any:
			if len(x) > 0 {
				s := strings.TrimSpace(fmt.Sprintf("%v", x[0]))
				if s != "" {
					return s
				}
			}
		default:
			s := strings.TrimSpace(fmt.Sprintf("%v", v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func clipOneLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return s
	}
	if max <= 0 {
		max = 80
	}
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func quoteAndTrim(s string, max int) string {
	s = clipOneLine(s, max)
	if s == "" {
		return s
	}
	return "「" + s + "」"
}

func ensureSentenceSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "。") || strings.HasSuffix(s, "！") || strings.HasSuffix(s, "？") ||
		strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?") {
		return s
	}
	return s + "。"
}
