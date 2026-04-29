package agent

import (
	"fmt"

	"github.com/yurika0211/luckyharness/internal/provider"
)

// ParseToolCalls 从 LLM 响应中解析工具调用
// 仅接受 provider 结构化返回的 tool_calls。
func ParseToolCalls(resp *provider.Response) []provider.ToolCall {
	if len(resp.ToolCalls) > 0 {
		return resp.ToolCalls
	}
	return nil
}

// FormatToolResult 格式化工具结果为消息
func FormatToolResult(toolName string, result string, err error) string {
	if err != nil {
		return fmt.Sprintf("[Tool Error: %s] %v", toolName, err)
	}
	return fmt.Sprintf("[Tool Result: %s] %s", toolName, result)
}
