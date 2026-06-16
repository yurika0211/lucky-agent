package contextx

import (
	"fmt"
	"time"
)

// --- 上下文窗口管理器 ---
//
// 核心职责：
// 1. 根据模型上下文窗口大小，管理消息列表
// 2. 当上下文超出窗口时，按优先级策略裁剪
// 3. 支持多种压缩策略：截断、摘要、滑动窗口
// 4. 保留 system prompt 和关键记忆

// MessagePriority 消息优先级
type MessagePriority int

const (
	// PriorityCritical 不可裁剪（system prompt、SOUL）
	PriorityCritical MessagePriority = iota
	// PriorityHigh 高优先级（长期记忆、重要上下文）
	PriorityHigh
	// PriorityNormal 普通优先级（近期对话、中期记忆）
	PriorityNormal
	// PriorityLow 低优先级（短期记忆、旧对话）
	PriorityLow
)

func (p MessagePriority) String() string {
	switch p {
	case PriorityCritical:
		return "critical"
	case PriorityHigh:
		return "high"
	case PriorityNormal:
		return "normal"
	case PriorityLow:
		return "low"
	default:
		return "unknown"
	}
}

// Message 代表上下文中的一条消息
type Message struct {
	Role             string          `json:"role"`                        // system, user, assistant, tool
	Content          string          `json:"content"`                     // 消息内容
	ReasoningContent string          `json:"reasoning_content,omitempty"` // thinking-mode assistant reasoning
	Name             string          `json:"name"`                        // 可选名称
	ToolCallID       string          `json:"tool_call_id,omitempty"`      // 工具结果对应的调用 ID
	Priority         MessagePriority `json:"priority"`                    // 优先级
	Category         string          `json:"category"`                    // 分类: system, soul, memory_long, memory_medium, memory_short, conversation, tool_result
	Timestamp        time.Time       `json:"timestamp"`                   // 时间戳
	ToolCalls        []ToolCall      `json:"tool_calls"`                  // 工具调用（仅 assistant）
	SourceID         string          `json:"source_id"`                   // 来源 ID（用于追踪裁剪）
}

// ToolCall 工具调用
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// TrimStrategy 裁剪策略
type TrimStrategy int

const (
	// TrimOldest 优先裁剪最旧的消息
	TrimOldest TrimStrategy = iota
	// TrimLowPriority 优先裁剪低优先级消息
	TrimLowPriority
	// TrimSlidingWindow 滑动窗口：保留最近 N 条
	TrimSlidingWindow
	// TrimSummarize 摘要压缩：将旧消息压缩为摘要
	TrimSummarize
)

func (s TrimStrategy) String() string {
	switch s {
	case TrimOldest:
		return "oldest_first"
	case TrimLowPriority:
		return "low_priority_first"
	case TrimSlidingWindow:
		return "sliding_window"
	case TrimSummarize:
		return "summarize"
	default:
		return "unknown"
	}
}

// WindowConfig 上下文窗口配置
type WindowConfig struct {
	// MaxTokens 最大 token 数量（模型上下文窗口大小）
	MaxTokens int `yaml:"max_tokens"`
	// ReservedTokens 为回复预留的 token 数量
	ReservedTokens int `yaml:"reserved_tokens"`
	// Strategy 默认裁剪策略
	Strategy TrimStrategy `yaml:"strategy"`
	// SlidingWindowSize 滑动窗口大小（TrimSlidingWindow 时使用）
	SlidingWindowSize int `yaml:"sliding_window_size"`
	// MaxConversationTurns 最大对话轮数
	MaxConversationTurns int `yaml:"max_conversation_turns"`
	// MemoryBudget 记忆预算（token 数量）
	MemoryBudget int `yaml:"memory_budget"`
	// SummarizeThreshold 触发摘要压缩的使用率阈值（0.0~1.0）
	SummarizeThreshold float64 `yaml:"summarize_threshold"`
}

// DefaultWindowConfig 返回默认窗口配置
func DefaultWindowConfig() WindowConfig {
	return WindowConfig{
		MaxTokens:            4096,
		ReservedTokens:       1024, // 为回复预留 1/4
		Strategy:             TrimLowPriority,
		SlidingWindowSize:    10,
		MaxConversationTurns: 50,
		MemoryBudget:         800, // 记忆最多占 800 tokens
		SummarizeThreshold:   0.8, // 80% 时触发摘要
	}
}

// ContextWindow 上下文窗口管理器
type ContextWindow struct {
	config    WindowConfig
	estimator *TokenEstimator
}

// Config 返回上下文窗口配置
func (cw *ContextWindow) Config() WindowConfig {
	return cw.config
}

// NewContextWindow 创建上下文窗口管理器
func NewContextWindow(config WindowConfig) *ContextWindow {
	availableTokens := config.MaxTokens - config.ReservedTokens
	if availableTokens <= 0 {
		availableTokens = config.MaxTokens / 2 // 至少留一半给回复
	}

	return &ContextWindow{
		config:    config,
		estimator: NewTokenEstimator(config.MaxTokens),
	}
}

// Fit 将消息列表裁剪到上下文窗口内
// 返回裁剪后的消息列表和裁剪统计
func (cw *ContextWindow) Fit(messages []Message) ([]Message, TrimResult) {
	availableTokens := cw.config.MaxTokens - cw.config.ReservedTokens
	if availableTokens <= 0 {
		availableTokens = cw.config.MaxTokens / 2
	}

	result := TrimResult{
		OriginalCount:   len(messages),
		OriginalTokens:  cw.estimator.EstimateMessages(messages),
		AvailableTokens: availableTokens,
		Strategy:        cw.config.Strategy,
	}

	// 如果已经在窗口内，直接返回
	if result.OriginalTokens <= availableTokens {
		result.FinalCount = len(messages)
		result.FinalTokens = result.OriginalTokens
		result.Trimmed = false
		return messages, result
	}

	// 按策略裁剪
	var trimmed []Message
	switch cw.config.Strategy {
	case TrimOldest:
		trimmed = cw.trimOldest(messages, availableTokens)
	case TrimLowPriority:
		trimmed = cw.trimLowPriority(messages, availableTokens)
	case TrimSlidingWindow:
		trimmed = cw.trimSlidingWindow(messages, availableTokens)
	case TrimSummarize:
		trimmed = cw.trimSummarize(messages, availableTokens)
	default:
		trimmed = cw.trimLowPriority(messages, availableTokens)
	}

	result.FinalCount = len(trimmed)
	result.FinalTokens = cw.estimator.EstimateMessages(trimmed)
	result.Trimmed = true

	return trimmed, result
}

// EstimateTokens 估算消息列表的 token 数量
func (cw *ContextWindow) EstimateTokens(messages []Message) int {
	return cw.estimator.EstimateMessages(messages)
}

// RemainingTokens 计算剩余可用 token
func (cw *ContextWindow) RemainingTokens(messages []Message) int {
	availableTokens := cw.config.MaxTokens - cw.config.ReservedTokens
	used := cw.estimator.EstimateMessages(messages)
	remaining := availableTokens - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// UsagePercent 计算上下文使用百分比
func (cw *ContextWindow) UsagePercent(messages []Message) float64 {
	availableTokens := cw.config.MaxTokens - cw.config.ReservedTokens
	used := cw.estimator.EstimateMessages(messages)
	return float64(used) / float64(availableTokens) * 100
}

// Stats 返回上下文窗口统计信息
func (cw *ContextWindow) Stats(messages []Message) ContextStats {
	availableTokens := cw.config.MaxTokens - cw.config.ReservedTokens
	used := cw.estimator.EstimateMessages(messages)

	stats := ContextStats{
		MaxTokens:       cw.config.MaxTokens,
		ReservedTokens:  cw.config.ReservedTokens,
		AvailableTokens: availableTokens,
		UsedTokens:      used,
		RemainingTokens: availableTokens - used,
		UsagePercent:    float64(used) / float64(availableTokens) * 100,
		MessageCount:    len(messages),
		Strategy:        cw.config.Strategy.String(),
	}

	if stats.RemainingTokens < 0 {
		stats.RemainingTokens = 0
	}
	if stats.UsagePercent > 100 {
		stats.UsagePercent = 100
	}

	// 按分类统计
	stats.ByCategory = make(map[string]CategoryStats)
	for _, msg := range messages {
		cat := msg.Category
		if cat == "" {
			cat = msg.Role
		}
		cs, ok := stats.ByCategory[cat]
		if !ok {
			cs = CategoryStats{}
		}
		cs.Count++
		cs.Tokens += cw.estimator.Estimate(msg.Content)
		stats.ByCategory[cat] = cs
	}

	return stats
}

// --- 裁剪策略实现 ---

// trimOldest 优先裁剪最旧的消息（保留 system 和 critical）
func (cw *ContextWindow) trimOldest(messages []Message, maxTokens int) []Message {
	// 分离 critical 和非 critical
	var critical []Message
	var normal []Message
	for _, msg := range messages {
		if msg.Priority == PriorityCritical {
			critical = append(critical, msg)
		} else {
			normal = append(normal, msg)
		}
	}

	// 从最新开始保留，直到达到 token 限制
	var result []Message
	result = append(result, critical...)
	criticalTokens := cw.estimator.EstimateMessages(critical)
	remaining := maxTokens - criticalTokens

	// 从后往前添加非 critical 消息
	for i := len(normal) - 1; i >= 0 && remaining > 0; i-- {
		msgTokens := cw.estimator.EstimateMessage(normal[i])
		if msgTokens <= remaining {
			result = append(result, normal[i])
			remaining -= msgTokens
		}
	}

	// 重新排序：critical 在前，然后按时间顺序
	return cw.sortByPriority(result)
}

// trimLowPriority 优先裁剪低优先级消息
func (cw *ContextWindow) trimLowPriority(messages []Message, maxTokens int) []Message {
	// 按优先级分组
	priorityGroups := make(map[MessagePriority][]Message)
	for _, msg := range messages {
		priorityGroups[msg.Priority] = append(priorityGroups[msg.Priority], msg)
	}

	// 按优先级从高到低填充
	var result []Message
	remaining := maxTokens

	for _, pri := range []MessagePriority{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		group := priorityGroups[pri]
		for _, msg := range group {
			msgTokens := cw.estimator.EstimateMessage(msg)
			if msgTokens <= remaining {
				result = append(result, msg)
				remaining -= msgTokens
			}
		}
	}

	return cw.sortByPriority(result)
}

// trimSlidingWindow 滑动窗口：保留最近 N 条 + critical
func (cw *ContextWindow) trimSlidingWindow(messages []Message, maxTokens int) []Message {
	var critical []Message
	var normal []Message
	for _, msg := range messages {
		if msg.Priority == PriorityCritical {
			critical = append(critical, msg)
		} else {
			normal = append(normal, msg)
		}
	}

	// 保留最近 N 条
	windowSize := cw.config.SlidingWindowSize
	if windowSize <= 0 {
		windowSize = 10
	}

	start := len(normal) - windowSize
	if start < 0 {
		start = 0
	}
	recent := normal[start:]

	// 合并并检查 token 限制
	var result []Message
	result = append(result, critical...)
	result = append(result, recent...)

	// 如果仍然超出，回退到低优先级裁剪
	if cw.estimator.EstimateMessages(result) > maxTokens {
		return cw.trimLowPriority(result, maxTokens)
	}

	return result
}

// trimSummarize 摘要压缩：将低优先级消息压缩为摘要
func (cw *ContextWindow) trimSummarize(messages []Message, maxTokens int) []Message {
	// 分离 critical 和非 critical
	var critical []Message
	var lowPriority []Message
	var normalPriority []Message

	for _, msg := range messages {
		switch msg.Priority {
		case PriorityCritical:
			critical = append(critical, msg)
		case PriorityLow:
			lowPriority = append(lowPriority, msg)
		default:
			normalPriority = append(normalPriority, msg)
		}
	}

	var result []Message
	result = append(result, critical...)

	// 如果有低优先级消息，压缩为摘要
	if len(lowPriority) > 0 {
		summary := cw.summarizeMessages(lowPriority)
		result = append(result, Message{
			Role:      "system",
			Content:   summary,
			Priority:  PriorityNormal,
			Category:  "summary",
			Timestamp: time.Now(),
			SourceID:  "auto-summary",
		})
	}

	result = append(result, normalPriority...)

	// 如果仍然超出，回退到低优先级裁剪
	if cw.estimator.EstimateMessages(result) > maxTokens {
		return cw.trimLowPriority(result, maxTokens)
	}

	return result
}

// summarizeMessages 将多条消息压缩为摘要文本
func (cw *ContextWindow) summarizeMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}

	var contentParts []string
	for _, msg := range messages {
		if msg.Content != "" {
			contentParts = append(contentParts, msg.Content)
		}
	}

	if len(contentParts) == 0 {
		return ""
	}

	// 简单拼接摘要（v0.13.0: 后续可接入 LLM 生成更智能摘要）
	summary := "[Summary of prior context] "
	for i, part := range contentParts {
		if i > 0 {
			summary += " | "
		}
		// 截断过长的内容
		if len(part) > 200 {
			summary += part[:200] + "..."
		} else {
			summary += part
		}
	}

	// 限制摘要总长度
	if len(summary) > 1000 {
		summary = summary[:1000] + "..."
	}

	return summary
}

// sortByPriority 按优先级和时间排序消息
func (cw *ContextWindow) sortByPriority(messages []Message) []Message {
	// 先按优先级分组
	groups := make(map[MessagePriority][]Message)
	for _, msg := range messages {
		groups[msg.Priority] = append(groups[msg.Priority], msg)
	}

	// 按优先级顺序合并：critical → high → normal → low
	var result []Message
	for _, pri := range []MessagePriority{PriorityCritical, PriorityHigh, PriorityNormal, PriorityLow} {
		result = append(result, groups[pri]...)
	}

	return result
}

// --- 结果类型 ---

// TrimResult 裁剪结果
type TrimResult struct {
	OriginalCount   int          `json:"original_count"`
	OriginalTokens  int          `json:"original_tokens"`
	FinalCount      int          `json:"final_count"`
	FinalTokens     int          `json:"final_tokens"`
	AvailableTokens int          `json:"available_tokens"`
	Trimmed         bool         `json:"trimmed"`
	Strategy        TrimStrategy `json:"strategy"`
}

// Summary 返回裁剪结果的文本摘要
func (r TrimResult) Summary() string {
	if !r.Trimmed {
		return fmt.Sprintf("Context OK: %d messages, %d/%d tokens (%.0f%%)",
			r.FinalCount, r.FinalTokens, r.AvailableTokens,
			float64(r.FinalTokens)/float64(r.AvailableTokens)*100)
	}
	return fmt.Sprintf("Context trimmed: %d→%d messages, %d→%d tokens (strategy: %s)",
		r.OriginalCount, r.FinalCount, r.OriginalTokens, r.FinalTokens, r.Strategy)
}

// ContextStats 上下文统计信息
type ContextStats struct {
	MaxTokens       int                      `json:"max_tokens"`
	ReservedTokens  int                      `json:"reserved_tokens"`
	AvailableTokens int                      `json:"available_tokens"`
	UsedTokens      int                      `json:"used_tokens"`
	RemainingTokens int                      `json:"remaining_tokens"`
	UsagePercent    float64                  `json:"usage_percent"`
	MessageCount    int                      `json:"message_count"`
	Strategy        string                   `json:"strategy"`
	ByCategory      map[string]CategoryStats `json:"by_category"`
}

// CategoryStats 分类统计
type CategoryStats struct {
	Count  int `json:"count"`
	Tokens int `json:"tokens"`
}

// Summary 返回统计信息的文本摘要
func (s ContextStats) Summary() string {
	return fmt.Sprintf("Context: %d/%d tokens (%.1f%%), %d messages, strategy: %s",
		s.UsedTokens, s.AvailableTokens, s.UsagePercent, s.MessageCount, s.Strategy)
}
