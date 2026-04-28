package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyharness/internal/provider"
)

// ParseToolCalls 从 LLM 响应中解析工具调用
// 支持 OpenAI function calling 格式和文本格式
func ParseToolCalls(resp *provider.Response) []provider.ToolCall {
	// 优先使用结构化的 ToolCalls
	if len(resp.ToolCalls) > 0 {
		return resp.ToolCalls
	}

	// 文本回退解析只接受“纯工具调用载荷”，避免把用户可见正文误当成工具协议。
	return parseTextToolCalls(resp.Content)
}

// parseTextToolCalls 从文本中解析工具调用
func parseTextToolCalls(content string) []provider.ToolCall {
	if !isPureToolCallPayload(content) {
		return nil
	}

	var calls []provider.ToolCall

	// 查找 ```tool ... ``` 块
	blocks := extractCodeBlocks(content, "tool")
	for _, block := range blocks {
		var call struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(block), &call); err != nil {
			continue
		}
		if call.Name == "" {
			continue
		}

		argsJSON, _ := json.Marshal(call.Arguments)
		calls = append(calls, provider.ToolCall{
			Name:      call.Name,
			Arguments: string(argsJSON),
		})
	}

	// 仅在整段内容就是工具调用协议时才接受 XML 回退。
	xmlCalls := parseXMLToolCalls(content)
	calls = append(calls, xmlCalls...)

	return calls
}

func isPureToolCallPayload(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "<tool_call>") && strings.HasSuffix(trimmed, "</tool_call>") {
		return true
	}
	if strings.HasPrefix(trimmed, "```tool") {
		remainder := strings.TrimSpace(extractLeadingToolBlocks(trimmed))
		return remainder == ""
	}
	return false
}

func extractLeadingToolBlocks(content string) string {
	rest := strings.TrimSpace(content)
	for strings.HasPrefix(rest, "```tool") {
		after := rest[len("```tool"):]
		end := strings.Index(after, "```")
		if end == -1 {
			return rest
		}
		rest = strings.TrimSpace(after[end+3:])
	}
	return rest
}

// parseXMLToolCalls 解析 XML 格式的工具调用
// 格式: <tool_call>{"name": "xxx", "arguments": {...}}</tool_call>
func parseXMLToolCalls(content string) []provider.ToolCall {
	var calls []provider.ToolCall
	tag := "tool_call"

	for {
		start := strings.Index(content, "<"+tag+">")
		if start == -1 {
			break
		}
		end := strings.Index(content, "</"+tag+">")
		if end == -1 {
			break
		}

		inner := content[start+len(tag)+2 : end]
		content = content[end+len(tag)+3:]

		var call struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(inner)), &call); err != nil {
			continue
		}
		if call.Name == "" {
			continue
		}

		argsJSON, _ := json.Marshal(call.Arguments)
		calls = append(calls, provider.ToolCall{
			Name:      call.Name,
			Arguments: string(argsJSON),
		})
	}

	return calls
}

// extractCodeBlocks 提取指定语言的代码块
func extractCodeBlocks(content, lang string) []string {
	var blocks []string
	marker := "```" + lang

	for {
		idx := strings.Index(content, marker)
		if idx == -1 {
			break
		}

		after := content[idx+len(marker):]
		end := strings.Index(after, "```")
		if end == -1 {
			break
		}

		block := strings.TrimSpace(after[:end])
		blocks = append(blocks, block)
		content = after[end+3:]
	}

	return blocks
}

// FormatToolResult 格式化工具结果为消息
func FormatToolResult(toolName string, result string, err error) string {
	if err != nil {
		return fmt.Sprintf("[Tool Error: %s] %v", toolName, err)
	}
	return fmt.Sprintf("[Tool Result: %s] %s", toolName, result)
}
