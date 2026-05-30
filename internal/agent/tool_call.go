package agent

import (
	"fmt"

	"github.com/yurika0211/luckyharness/internal/provider"
)

/**
 * ParseToolCalls 从 LLM 响应中提取结构化工具调用列表
 */
func ParseToolCalls(resp *provider.Response) []provider.ToolCall {
	if len(resp.ToolCalls) > 0 {
		return resp.ToolCalls
	}
	return nil
}

/**
 * FormatToolResult 将工具执行结果格式化为统一文本
 */
func FormatToolResult(toolName string, result string, err error) string {
	if err != nil {
		return fmt.Sprintf("[Tool Error: %s] %v", toolName, err)
	}
	return fmt.Sprintf("[Tool Result: %s] %s", toolName, result)
}
