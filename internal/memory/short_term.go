package memory

import (
	"fmt"
	"strings"
	"sync"

	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/utils"
)

// --- 短期记忆：滑动窗口 + 摘要压缩 ---
//
// 核心思路（参考 yurika0408.icu/post/301）：
// 1. 保留最近 N 轮完整对话，更早的压缩成摘要
// 2. 摘要用结构化模板约束，而非自由发挥
// 3. Compress 暂不接 LLM，用模板提取关键信息

// ConversationTurn 代表一轮对话（user + assistant）
type ConversationTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ShortTermBuffer 短期记忆缓冲区
// 保留最近 maxTurns 轮完整对话，更早的压缩成摘要
type ShortTermBuffer struct {
	mu       sync.RWMutex
	maxTurns int                // 最大保留轮数（默认 10）
	messages []ConversationTurn // 最近 N 轮完整对话
	summary  string             // 更早对话的压缩摘要
}

// NewShortTermBuffer 创建短期记忆缓冲区
func NewShortTermBuffer(maxTurns int) *ShortTermBuffer {
	if maxTurns <= 0 {
		maxTurns = 10
	}
	return &ShortTermBuffer{
		maxTurns: maxTurns,
		messages: make([]ConversationTurn, 0),
	}
}

// Add 添加一条消息，超出窗口时触发压缩
func (b *ShortTermBuffer) Add(role, content string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.messages = append(b.messages, ConversationTurn{Role: role, Content: content})

	// 超出窗口时压缩溢出消息
	if len(b.messages) > b.maxTurns {
		b.compress()
	}
}

// Compress 把溢出消息压缩成摘要（结构化模板）
// 暂不接 LLM，用模板提取关键信息：谁说了什么、关键实体、决策点
func (b *ShortTermBuffer) Compress() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.messages) > b.maxTurns {
		b.compress()
	}
}

// compress 内部压缩方法（调用方需持有锁）
func (b *ShortTermBuffer) compress() {
	overflow := b.messages[:len(b.messages)-b.maxTurns]
	b.messages = b.messages[len(b.messages)-b.maxTurns:]

	newSummary := b.generateStructuredSummary(overflow)
	if b.summary != "" {
		// 合并旧摘要和新摘要
		b.summary = b.mergeSummaries(b.summary, newSummary)
	} else {
		b.summary = newSummary
	}
}

// generateStructuredSummary 用结构化模板提取溢出消息的关键信息
func (b *ShortTermBuffer) generateStructuredSummary(overflow []ConversationTurn) string {
	var sb strings.Builder

	// 收集各角色的关键发言
	var userStatements []string
	var assistantStatements []string
	var entities []string
	var decisions []string

	for _, msg := range overflow {
		switch msg.Role {
		case "user":
			userStatements = append(userStatements, utils.TrimToRunes(msg.Content, 120))
			entities = append(entities, extractEntities(msg.Content)...)
			decisions = append(decisions, extractDecisions(msg.Content)...)
		case "assistant":
			assistantStatements = append(assistantStatements, utils.TrimToRunes(msg.Content, 120))
			decisions = append(decisions, extractDecisions(msg.Content)...)
		}
	}

	// 结构化模板输出
	sb.WriteString("[Prior Conversation Summary]\n")

	if len(userStatements) > 0 {
		sb.WriteString("User said:\n")
		for _, s := range utils.DedupNonEmptyStrings(userStatements) {
			sb.WriteString("  - " + s + "\n")
		}
	}

	if len(assistantStatements) > 0 {
		sb.WriteString("Assistant responded:\n")
		for _, s := range utils.DedupNonEmptyStrings(assistantStatements) {
			sb.WriteString("  - " + s + "\n")
		}
	}

	if len(entities) > 0 {
		sb.WriteString("Key entities: " + strings.Join(utils.DedupNonEmptyStrings(entities), ", ") + "\n")
	}

	if len(decisions) > 0 {
		sb.WriteString("Decisions:\n")
		for _, d := range utils.DedupNonEmptyStrings(decisions) {
			sb.WriteString("  - " + d + "\n")
		}
	}

	return sb.String()
}

// mergeSummaries 合并旧摘要和新摘要
func (b *ShortTermBuffer) mergeSummaries(oldSummary, newSummary string) string {
	// 简单合并：旧摘要截断 + 新摘要追加
	merged := oldSummary
	if len(merged) > 800 {
		merged = utils.TrimToRunes(merged, 800)
	}
	merged += "\n" + newSummary
	if len(merged) > 2000 {
		merged = utils.TrimToRunes(merged, 2000)
	}
	return merged
}

// GetContext 返回摘要 + 最近消息，用于构建上下文
func (b *ShortTermBuffer) GetContext() []provider.Message {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []provider.Message

	// 如果有摘要，作为 system 消息注入
	if b.summary != "" {
		result = append(result, provider.Message{
			Role:    "system",
			Content: b.summary,
		})
	}

	// 最近 N 轮完整对话
	for _, msg := range b.messages {
		result = append(result, provider.Message{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	return result
}

// Summary 返回当前摘要内容
func (b *ShortTermBuffer) Summary() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.summary
}

// MessageCount 返回当前缓冲区中的消息数
func (b *ShortTermBuffer) MessageCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.messages)
}

// MaxTurns 返回最大轮数配置
func (b *ShortTermBuffer) MaxTurns() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.maxTurns
}

// Clear 清空缓冲区
func (b *ShortTermBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.messages = make([]ConversationTurn, 0)
	b.summary = ""
}

// --- 辅助函数 ---

// extractEntities 从文本中提取关键实体（基于启发式规则）
// 暂不接 NLP，用简单规则提取：引号内容、大写开头词、代码相关词
func extractEntities(text string) []string {
	var entities []string

	// 提取引号中的内容
	inQuote := false
	var current strings.Builder
	for _, r := range text {
		switch r {
		case '"', '\u201c', '\u201d', '\'', '\u2018', '`':
			if inQuote {
				if current.Len() > 0 {
					entities = append(entities, current.String())
					current.Reset()
				}
				inQuote = false
			} else {
				inQuote = true
			}
		default:
			if inQuote {
				current.WriteRune(r)
			}
		}
	}

	// 提取代码/技术相关关键词
	techKeywords := []string{
		"Go", "Rust", "Python", "JavaScript", "TypeScript",
		"API", "HTTP", "REST", "gRPC", "SQL", "Redis",
		"Docker", "Kubernetes", "Git", "GitHub",
	}
	lower := strings.ToLower(text)
	for _, kw := range techKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			entities = append(entities, kw)
		}
	}

	return entities
}

// extractDecisions 从文本中提取决策点（基于启发式规则）
func extractDecisions(text string) []string {
	var decisions []string

	decisionPatterns := []string{
		"决定", "决定用", "选择了", "采用",
		"decided to", "chose to", "will use", "going with",
		"方案", "approach", "solution",
	}

	lower := strings.ToLower(text)
	for _, pattern := range decisionPatterns {
		if idx := strings.Index(lower, strings.ToLower(pattern)); idx >= 0 {
			// 提取包含该模式的句子片段
			start := idx
			if start > 30 {
				start = idx - 30
			}
			end := idx + len(pattern) + 50
			if end > len(text) {
				end = len(text)
			}
			fragment := text[start:end]
			decisions = append(decisions, utils.TrimToRunes(fragment, 100))
		}
	}

	return decisions
}

// Ensure ShortTermBuffer is used correctly
var _ fmt.Stringer = (*ShortTermBuffer)(nil)

// String implements fmt.Stringer for debugging
func (b *ShortTermBuffer) String() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return fmt.Sprintf("ShortTermBuffer{maxTurns=%d, messages=%d, hasSummary=%v}",
		b.maxTurns, len(b.messages), b.summary != "")
}
