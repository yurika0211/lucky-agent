package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

const naturalCitationHeader = "参考说明："

var citationURLRe = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

type naturalCitation struct {
	Tool    string
	Summary string
}

func appendNaturalCitations(response string, toolCalls []toolCallLog) string {
	response = strings.TrimSpace(response)
	citations := naturalCitationsFromToolLogs(toolCalls)
	if len(citations) == 0 {
		return response
	}
	if strings.Contains(response, naturalCitationHeader) {
		return response
	}

	var b strings.Builder
	b.WriteString(response)
	if response != "" {
		b.WriteString("\n\n")
	}
	b.WriteString(naturalCitationHeader)
	for _, citation := range citations {
		b.WriteString("\n- ")
		b.WriteString(citation.Summary)
	}
	return b.String()
}

func naturalCitationsFromExecuted(executed []executedToolCall) []naturalCitation {
	if len(executed) == 0 {
		return nil
	}
	logs := make([]toolCallLog, 0, len(executed))
	for _, execResult := range executed {
		logs = append(logs, toolCallLog{
			Name:      execResult.ToolCall.Name,
			Arguments: execResult.ToolCall.Arguments,
			Result:    execResult.Result,
			Duration:  execResult.Duration,
		})
	}
	return naturalCitationsFromToolLogs(logs)
}

func naturalCitationsFromToolLogs(toolCalls []toolCallLog) []naturalCitation {
	if len(toolCalls) == 0 {
		return nil
	}
	citations := make([]naturalCitation, 0, len(toolCalls))
	seen := make(map[string]struct{}, len(toolCalls))
	for _, call := range toolCalls {
		if shouldSkipCitationTool(call.Name, call.Result) {
			continue
		}
		citation, ok := naturalCitationFromToolLog(call)
		if !ok {
			continue
		}
		key := citation.Tool + "|" + citation.Summary
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		citations = append(citations, citation)
		if len(citations) >= 8 {
			break
		}
	}
	return citations
}

func shouldSkipCitationTool(name, result string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	out := strings.TrimSpace(result)
	if out == "" {
		return true
	}
	lower := strings.ToLower(out)
	if strings.HasPrefix(lower, "error:") ||
		strings.Contains(lower, "no results found") ||
		strings.Contains(lower, "all search sources failed") ||
		strings.HasPrefix(lower, "failed to fetch ") {
		return true
	}
	switch name {
	case "remember", "rag_index", "cron_add", "cron_remove", "cron_pause", "cron_resume":
		return true
	default:
		return false
	}
}

func naturalCitationFromToolLog(call toolCallLog) (naturalCitation, bool) {
	name := strings.TrimSpace(call.Name)
	args := parseCitationArgs(call.Arguments)
	result := strings.TrimSpace(call.Result)
	switch name {
	case "web_search":
		query := stringArg(args, "query")
		entries := extractSearchCitationEntries(result, 2)
		if len(entries) == 0 {
			if query == "" {
				return naturalCitation{Tool: name, Summary: "我参考了网页搜索返回的结果。"}, true
			}
			return naturalCitation{Tool: name, Summary: fmt.Sprintf("我参考了关于“%s”的网页搜索结果。", query)}, true
		}
		return naturalCitation{Tool: name, Summary: formatWebSearchCitation(query, entries)}, true
	case "web_fetch":
		target := stringArg(args, "url")
		if target == "" {
			target = firstURL(result)
		}
		title := firstMarkdownTitle(result)
		if title == "" {
			title = hostFromURL(target)
		}
		switch {
		case target != "" && title != "":
			return naturalCitation{Tool: name, Summary: fmt.Sprintf("我读取了网页“%s”（%s）的正文内容。", title, target)}, true
		case target != "":
			return naturalCitation{Tool: name, Summary: fmt.Sprintf("我读取了网页 %s 的正文内容。", target)}, true
		default:
			return naturalCitation{Tool: name, Summary: "我读取了目标网页的正文内容。"}, true
		}
	case "file_read":
		path := stringArg(args, "path")
		if path == "" {
			return naturalCitation{Tool: name, Summary: "我参考了本地文件读取工具返回的内容。"}, true
		}
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我读取了本地文件 %s 的内容。", cleanCitationPath(path))}, true
	case "file_list":
		path := stringArg(args, "path")
		if path == "" {
			path = stringArg(args, "dir")
		}
		if path == "" {
			return naturalCitation{Tool: name, Summary: "我查看了本地目录列表。"}, true
		}
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我查看了本地目录 %s 的列表。", cleanCitationPath(path))}, true
	case "current_time":
		location := stringArg(args, "location")
		if location == "" {
			location = stringArg(args, "timezone")
		}
		if location == "" {
			location = extractCurrentTimeLocation(result)
		}
		if location == "" {
			return naturalCitation{Tool: name, Summary: "我用当前时间工具核对了时间。"}, true
		}
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我用当前时间工具核对了 %s 的时间。", location)}, true
	case "calculate":
		expr := stringArg(args, "expression")
		if expr == "" {
			expr = stringArg(args, "expr")
		}
		if expr == "" {
			return naturalCitation{Tool: name, Summary: "我用计算工具核对了数值。"}, true
		}
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我用计算工具核对了“%s”。", clipCitationText(expr, 80))}, true
	case "rag_search":
		query := stringArg(args, "query")
		if query == "" {
			return naturalCitation{Tool: name, Summary: "我参考了本地 RAG 检索返回的资料。"}, true
		}
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我参考了本地 RAG 检索中关于“%s”的资料。", clipCitationText(query, 80))}, true
	case "recall":
		query := stringArg(args, "query")
		if query == "" {
			return naturalCitation{Tool: name, Summary: "我参考了记忆检索返回的信息。"}, true
		}
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我参考了记忆检索中关于“%s”的信息。", clipCitationText(query, 80))}, true
	default:
		return naturalCitation{Tool: name, Summary: fmt.Sprintf("我参考了 %s 工具返回的结果。", name)}, true
	}
}

type searchCitationEntry struct {
	Title string
	URL   string
}

func extractSearchCitationEntries(result string, limit int) []searchCitationEntry {
	lines := strings.Split(result, "\n")
	entries := make([]searchCitationEntry, 0, limit)
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" || !looksLikeNumberedSearchTitle(line) {
			continue
		}
		title := stripSearchTitlePrefix(line)
		title = stripSourceSuffix(title)
		link := ""
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			if u := firstURL(lines[j]); u != "" {
				link = u
				break
			}
		}
		if title == "" && link == "" {
			continue
		}
		entries = append(entries, searchCitationEntry{Title: title, URL: link})
		if len(entries) >= limit {
			break
		}
	}
	return entries
}

func looksLikeNumberedSearchTitle(line string) bool {
	if len(line) < 3 {
		return false
	}
	dot := strings.Index(line, ".")
	if dot <= 0 || dot > 3 {
		return false
	}
	for _, r := range line[:dot] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return strings.TrimSpace(line[dot+1:]) != ""
}

func stripSearchTitlePrefix(line string) string {
	dot := strings.Index(line, ".")
	if dot < 0 {
		return strings.TrimSpace(line)
	}
	return strings.TrimSpace(line[dot+1:])
}

func stripSourceSuffix(title string) string {
	title = strings.TrimSpace(title)
	if idx := strings.LastIndex(title, " ["); idx > 0 && strings.HasSuffix(title, "]") {
		return strings.TrimSpace(title[:idx])
	}
	return title
}

func formatWebSearchCitation(query string, entries []searchCitationEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Title != "" && entry.URL != "":
			parts = append(parts, fmt.Sprintf("“%s”（%s）", clipCitationText(entry.Title, 90), entry.URL))
		case entry.Title != "":
			parts = append(parts, fmt.Sprintf("“%s”", clipCitationText(entry.Title, 90)))
		case entry.URL != "":
			parts = append(parts, entry.URL)
		}
	}
	if query != "" && len(parts) > 0 {
		return fmt.Sprintf("我参考了关于“%s”的网页搜索结果，包括 %s。", clipCitationText(query, 80), joinNaturalList(parts))
	}
	if len(parts) > 0 {
		return fmt.Sprintf("我参考了网页搜索结果，包括 %s。", joinNaturalList(parts))
	}
	if query != "" {
		return fmt.Sprintf("我参考了关于“%s”的网页搜索结果。", clipCitationText(query, 80))
	}
	return "我参考了网页搜索返回的结果。"
}

func parseCitationArgs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func stringArg(args map[string]any, key string) string {
	if len(args) == 0 {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func firstURL(text string) string {
	match := citationURLRe.FindString(text)
	return strings.TrimRight(match, ".,;:!?，。；：！？")
}

func firstMarkdownTitle(result string) string {
	for _, line := range strings.Split(result, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(u.Host)
}

func cleanCitationPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func extractCurrentTimeLocation(result string) string {
	const marker = "location:"
	idx := strings.LastIndex(strings.ToLower(result), marker)
	if idx < 0 {
		return ""
	}
	location := result[idx+len(marker):]
	location = strings.Trim(location, " )\n\t")
	return strings.TrimSpace(location)
}

func clipCitationText(text string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func joinNaturalList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " 和 " + items[1]
	default:
		return strings.Join(items[:len(items)-1], "、") + " 和 " + items[len(items)-1]
	}
}
