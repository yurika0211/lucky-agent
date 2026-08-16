package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// TraceRecord is a user-safe summary of a completed tool call.
type TraceRecord struct {
	Name       string `json:"name"`
	Arguments  string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	Success    bool   `json:"success"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Annotation string `json:"annotation"`
}

// NewTraceRecord creates a trace entry with a deterministic natural-language
// annotation. It never sends tool data to a model.
func NewTraceRecord(name, arguments, result string, duration time.Duration) TraceRecord {
	return NewTraceRecordWithTemplates(name, arguments, result, duration, nil)
}

// NewTraceRecordWithTemplates creates a trace entry and applies an optional
// per-tool annotation override before falling back to built-in templates.
func NewTraceRecordWithTemplates(name, arguments, result string, duration time.Duration, templates map[string]string) TraceRecord {
	record := TraceRecord{
		Name:       strings.TrimSpace(name),
		Arguments:  strings.TrimSpace(arguments),
		Result:     strings.TrimSpace(result),
		DurationMS: duration.Milliseconds(),
	}
	record.Success, record.Error = traceResultStatus(record.Result)
	record.Annotation = AnnotateToolCallWithTemplates(record.Name, record.Arguments, record.Result, templates)
	return record
}

// AnnotateToolCall describes a tool invocation using only its supplied
// arguments and result. Unknown tools receive a safe generic summary.
func AnnotateToolCall(name, arguments, result string) string {
	return AnnotateToolCallWithTemplates(name, arguments, result, nil)
}

// AnnotateToolCallWithTemplates applies a configured template for the named
// tool. Supported placeholders are {tool}, {path}, {query}, {command},
// {workdir}, {result}, {status}, and {content_size}.
func AnnotateToolCallWithTemplates(name, arguments, result string, templates map[string]string) string {
	toolName := strings.ToLower(strings.TrimSpace(name))
	args := traceArguments(arguments)
	path := traceArgument(args, "path", "file", "filepath", "filename", "source", "destination")
	query := traceArgument(args, "query", "q", "keyword", "search")
	if template := traceTemplateForTool(toolName, templates); template != "" {
		return renderTraceAnnotationTemplate(template, name, path, query, args, result)
	}

	switch toolName {
	case "file_read", "document_read":
		return traceFileReadAnnotation(path, args)
	case "file_write":
		return fmt.Sprintf("将 %s 内容写入 %s", traceContentSize(args), traceValue(path, "文件"))
	case "file_list":
		return "列出了 " + traceValue(path, "目录") + " 的内容"
	case "file_delete":
		return "删除了 " + traceValue(path, "文件")
	case "file_move":
		return fmt.Sprintf("将 %s 移动到 %s", traceValue(traceArgument(args, "source", "from", "path"), "文件"), traceValue(traceArgument(args, "destination", "to", "target"), "目标位置"))
	case "file_patch":
		return "修改了 " + traceValue(path, "文件")
	case "shell", "terminal":
		return fmt.Sprintf("在 %s 执行 %s", traceValue(traceArgument(args, "workdir", "cwd", "dir"), "当前目录"), traceValue(clipTraceValue(traceArgument(args, "command", "cmd", "script"), 80), "终端命令"))
	case "web_search":
		return "搜索了「" + traceValue(clipTraceValue(query, 80), "相关资料") + "」"
	case "web_fetch":
		return "获取了 " + traceValue(clipTraceValue(traceArgument(args, "url", "uri", "link"), 100), "网页") + " 的内容"
	case "recall":
		return "召回了与「" + traceValue(clipTraceValue(query, 80), "当前问题") + "」相关的记忆"
	case "remember":
		return "保存了一条" + traceValue(traceArgument(args, "category", "type"), "长期") + "记忆"
	case "rag_search":
		return "在知识库中搜索了「" + traceValue(clipTraceValue(query, 80), "当前问题") + "」"
	case "rag_index":
		return "索引了 " + traceValue(path, "知识库内容")
	case "image_generate":
		return "生成图片：" + traceValue(clipTraceValue(traceArgument(args, "prompt", "description", "query"), 80), "未提供描述")
	case "image_analyze":
		return "分析了 " + traceValue(path, "图片")
	case "text_to_speech":
		return "将文本转换为语音"
	case "delegate_task":
		return "委托子 Agent：" + traceValue(clipTraceValue(traceArgument(args, "description", "task", "prompt"), 90), "未提供任务说明")
	case "cron_add":
		return "创建了定时任务：" + traceValue(clipTraceValue(traceArgument(args, "command", "prompt", "task"), 80), "未提供任务说明")
	case "opencli":
		return "通过 OpenCLI 调用了 " + traceValue(traceArgument(args, "service", "command", "tool"), "外部服务")
	case "csv_query":
		return "查询了 CSV 数据：" + traceValue(clipTraceValue(query, 80), "未提供查询条件")
	case "sql_query":
		return "执行了 SQL 查询：" + traceValue(clipTraceValue(traceArgument(args, "query", "sql", "statement"), 80), "未提供查询语句")
	case "db_schema":
		return "读取了数据库结构"
	}

	if result != "" {
		return "调用了 " + traceValue(name, "工具") + "，已获得结果"
	}
	return "调用了 " + traceValue(name, "工具")
}

func traceTemplateForTool(toolName string, templates map[string]string) string {
	for configuredName, annotation := range templates {
		if strings.EqualFold(strings.TrimSpace(configuredName), toolName) {
			return strings.TrimSpace(annotation)
		}
	}
	return ""
}

func renderTraceAnnotationTemplate(template, name, path, query string, args map[string]any, result string) string {
	success, _ := traceResultStatus(result)
	status := "成功"
	if !success {
		status = "失败"
	}
	values := map[string]string{
		"tool":         traceValue(strings.TrimSpace(name), "工具"),
		"path":         traceValue(clipTraceValue(path, 160), "未提供路径"),
		"query":        traceValue(clipTraceValue(query, 120), "未提供查询"),
		"command":      traceValue(clipTraceValue(traceArgument(args, "command", "cmd", "script"), 120), "未提供命令"),
		"workdir":      traceValue(clipTraceValue(traceArgument(args, "workdir", "cwd", "dir"), 120), "当前目录"),
		"result":       traceValue(clipTraceValue(result, 160), "无返回内容"),
		"status":       status,
		"content_size": traceContentSize(args),
	}
	for key, value := range values {
		template = strings.ReplaceAll(template, "{"+key+"}", value)
	}
	return strings.TrimSpace(template)
}

func traceArguments(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil
	}
	return args
}

func traceArgument(args map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func traceFileReadAnnotation(path string, args map[string]any) string {
	offset := traceArgument(args, "offset", "start_line", "start")
	limit := traceArgument(args, "limit", "lines", "max_lines")
	file := traceValue(path, "文件")
	if offset == "" && limit == "" {
		return "读取了 " + file
	}
	if offset == "" || offset == "1" {
		return "读取了 " + file + " 的前 " + traceValue(limit, "若干") + " 行"
	}
	if limit == "" {
		return "从第 " + offset + " 行开始读取了 " + file
	}
	return "读取了 " + file + " 的第 " + offset + " 行起共 " + limit + " 行"
}

func traceContentSize(args map[string]any) string {
	content := traceArgument(args, "content", "text", "data", "patch")
	if content == "" {
		return "内容"
	}
	bytes := len(content)
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
}

func traceResultStatus(result string) (bool, string) {
	trimmed := strings.TrimSpace(result)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"error:", "failed:", "failure:", "panic:", "timeout:"} {
		if strings.HasPrefix(lower, prefix) {
			return false, clipTraceValue(trimmed, 180)
		}
	}
	return true, ""
}

func traceValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func clipTraceValue(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}
