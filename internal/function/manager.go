package function

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/yurika0211/luckyharness/internal/tool"
)

// FunctionDefinition 代表一个 OpenAI function calling 的函数定义
type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// FunctionCall 代表一次函数调用请求（来自 LLM 响应）
type FunctionCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// FunctionResult 代表一次函数调用的结果
type FunctionResult struct {
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// CallMode 控制 function calling 的模式
type CallMode int

const (
	CallModeAuto  CallMode = iota // 自动决定是否调用函数
	CallModeNone                  // 不调用函数
	CallModeForce                 // 强制调用指定函数
)

// Options 是 function calling 的配置选项
type Options struct {
	Mode       CallMode // 调用模式
	ForceName  string   // CallModeForce 时强制调用的函数名
	MaxCalls   int      // 单次响应最大函数调用数（0 = 不限制）
	ParallelOK bool     // 是否允许并行调用
}

// DefaultOptions 返回默认选项
func DefaultOptions() Options {
	return Options{
		Mode:       CallModeAuto,
		MaxCalls:   5,
		ParallelOK: true,
	}
}

// Manager 管理 function calling 的生命周期
type Manager struct {
	mu       sync.RWMutex
	registry *tool.Registry
	results  map[string]*FunctionResult // callID -> result
	history  []CallEntry                // 调用历史
}

// CallEntry 记录一次完整的函数调用
type CallEntry struct {
	Call      FunctionCall
	Result    *FunctionResult
	Timestamp int64
}

// NewManager 创建 function calling 管理器
func NewManager(registry *tool.Registry) *Manager {
	return &Manager{
		registry: registry,
		results:  make(map[string]*FunctionResult),
		history:  make([]CallEntry, 0),
	}
}

// BuildTools 构建 OpenAI function calling 的 tools 参数
func (m *Manager) BuildTools() []map[string]any {
	tools := m.registry.ListModelVisible()
	if len(tools) == 0 {
		return nil
	}

	result := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		result = append(result, t.ToOpenAIFormat())
	}
	return result
}

// ExecuteCalls 执行一批函数调用
func (m *Manager) ExecuteCalls(calls []FunctionCall, autoApprove bool) []FunctionResult {
	results := make([]FunctionResult, 0, len(calls))

	for _, call := range calls {
		result := m.executeCall(call, autoApprove)
		results = append(results, result)
	}

	return results
}

// executeCall 执行单个函数调用
func (m *Manager) executeCall(call FunctionCall, autoApprove bool) FunctionResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 解析参数
	var args map[string]any
	if call.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			fr := FunctionResult{
				CallID:  call.ID,
				Name:    call.Name,
				Content: fmt.Sprintf("Error parsing arguments: %v", err),
				IsError: true,
			}
			m.results[call.ID] = &fr
			m.history = append(m.history, CallEntry{Call: call, Result: &fr, Timestamp: 0})
			return fr
		}
	}

	// 通过 Gateway 执行
	// 注意: 这里需要 Agent 的 gateway，暂时用 Registry.Call
	t, ok := m.registry.Get(call.Name)
	if !ok {
		fr := FunctionResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: fmt.Sprintf("Function not found: %s", call.Name),
			IsError: true,
		}
		m.results[call.ID] = &fr
		m.history = append(m.history, CallEntry{Call: call, Result: &fr, Timestamp: 0})
		return fr
	}

	// 权限检查
	if !autoApprove && t.Permission == tool.PermApprove {
		fr := FunctionResult{
			CallID:  call.ID,
			Name:    call.Name,
			Content: "Function call requires approval (use --yolo to auto-approve)",
			IsError: true,
		}
		m.results[call.ID] = &fr
		m.history = append(m.history, CallEntry{Call: call, Result: &fr, Timestamp: 0})
		return fr
	}

	output, err := t.Handler(args)
	fr := FunctionResult{
		CallID:  call.ID,
		Name:    call.Name,
		Content: output,
		IsError: err != nil,
	}
	if err != nil {
		fr.Content = fmt.Sprintf("Error: %v", err)
	}

	m.results[call.ID] = &fr
	m.history = append(m.history, CallEntry{Call: call, Result: &fr, Timestamp: 0})
	return fr
}

// GetResult 获取函数调用结果
func (m *Manager) GetResult(callID string) (*FunctionResult, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.results[callID]
	return r, ok
}

// GetHistory 获取调用历史
func (m *Manager) GetHistory() []CallEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	history := make([]CallEntry, len(m.history))
	copy(history, m.history)
	return history
}

// ClearHistory 清除调用历史
func (m *Manager) ClearHistory() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = make([]CallEntry, 0)
	m.results = make(map[string]*FunctionResult)
}

// FormatResults 格式化函数调用结果为文本
func FormatResults(results []FunctionResult) string {
	var output string
	for _, r := range results {
		prefix := "✅"
		if r.IsError {
			prefix = "❌"
		}
		output += fmt.Sprintf("%s %s: %s\n", prefix, r.Name, r.Content)
	}
	return output
}

// BuildToolMessages 将函数调用结果构建为 OpenAI 消息格式
// 返回 assistant 消息（含 tool_calls）和 tool 消息（含结果）
func BuildToolMessages(calls []FunctionCall, results []FunctionResult) []ToolMessage {
	messages := make([]ToolMessage, 0, len(calls)+1)

	// assistant 消息（含 tool_calls）
	assistantMsg := ToolMessage{
		Role:      "assistant",
		ToolCalls: calls,
	}
	messages = append(messages, assistantMsg)

	// tool 结果消息
	for _, r := range results {
		messages = append(messages, ToolMessage{
			Role:       "tool",
			Content:    r.Content,
			ToolCallID: r.CallID,
			Name:       r.Name,
		})
	}

	return messages
}

// ToolMessage 是 function calling 的消息格式
type ToolMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []FunctionCall `json:"tool_calls,omitempty"`
}
