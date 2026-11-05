package contextx

import (
	"hash/fnv"
	"math"
	"sync"
	"unicode/utf8"
)

// --- Token 估算器 ---
//
// 精确的 tiktoken 需要大字典文件，这里用启发式估算：
// - 英文: ~4 chars/token (GPT-4 系列)
// - 中文: ~1.5 chars/token (CJK 字符占更多 token)
// - 代码: ~3 chars/token (代码比自然语言更密集)
//
// 估算误差约 ±10%，足够用于上下文窗口管理

// ContentType 表示文本内容类型
type ContentType int

const (
	ContentAuto    ContentType = iota // 自动检测
	ContentEnglish                    // 英文文本
	ContentChinese                    // 中文文本
	ContentCode                       // 代码
	ContentMixed                      // 混合内容
)

// TokenEstimator 估算文本的 token 数量
type TokenEstimator struct {
	modelContextWindow int // 模型上下文窗口大小
	mu                 sync.RWMutex
	cache              map[uint64]int
}

// NewTokenEstimator 创建 Token 估算器
func NewTokenEstimator(modelContextWindow int) *TokenEstimator {
	if modelContextWindow <= 0 {
		modelContextWindow = 4096 // 默认
	}
	return &TokenEstimator{
		modelContextWindow: modelContextWindow,
		cache:              make(map[uint64]int),
	}
}

// Estimate 估算文本的 token 数量
func (te *TokenEstimator) Estimate(text string) int {
	if text == "" {
		return 0
	}

	key := tokenEstimateCacheKey(text)
	te.mu.RLock()
	if cached, ok := te.cache[key]; ok {
		te.mu.RUnlock()
		return cached
	}
	te.mu.RUnlock()

	contentType := detectContentType(text)
	charsPerToken := charsPerToken(contentType)

	// 基础估算
	chars := utf8.RuneCountInString(text)
	tokens := float64(chars) / charsPerToken

	// 特殊 token 开销：每条消息约 +4 (role 标记等)
	// 这里只估算文本本身，消息开销在 MessageEstimate 中处理

	estimated := int(math.Ceil(tokens))
	te.mu.Lock()
	// 简单上限，避免无界增长
	if len(te.cache) > 4096 {
		te.cache = make(map[uint64]int, 2048)
	}
	te.cache[key] = estimated
	te.mu.Unlock()
	return estimated
}

// EstimateMessages 估算消息列表的总 token 数量
func (te *TokenEstimator) EstimateMessages(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += te.EstimateMessage(msg)
	}
	// 对话模板开销（system 格式化等）
	total += 4 // <|im_start|> 和 <|im_end|> 等
	return total
}

// EstimateMessage 估算单条消息的 token 数量
func (te *TokenEstimator) EstimateMessage(msg Message) int {
	// 消息开销：role 标记 + 格式化 ≈ 4 tokens
	overhead := 4

	// 如果有 name 字段，额外 +1
	if msg.Name != "" {
		overhead += 1 + te.Estimate(msg.Name)
	}

	contentTokens := te.Estimate(msg.Content)
	if msg.ReasoningContent != "" {
		contentTokens += te.Estimate(msg.ReasoningContent)
	}

	// 工具调用额外开销
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			contentTokens += te.Estimate(tc.Name) + te.Estimate(tc.Arguments) + 4
		}
	}

	return overhead + contentTokens
}

// ModelContextWindow 返回模型的上下文窗口大小
func (te *TokenEstimator) ModelContextWindow() int {
	return te.modelContextWindow
}

// SetModelContextWindow 设置模型上下文窗口大小
func (te *TokenEstimator) SetModelContextWindow(window int) {
	if window > 0 {
		te.mu.Lock()
		te.modelContextWindow = window
		te.mu.Unlock()
	}
}

// RemainingTokens 计算剩余可用 token 数量
func (te *TokenEstimator) RemainingTokens(messages []Message) int {
	used := te.EstimateMessages(messages)
	te.mu.RLock()
	window := te.modelContextWindow
	te.mu.RUnlock()
	remaining := window - used
	if remaining < 0 {
		return 0
	}
	return remaining
}

// UsagePercent 计算上下文使用百分比
func (te *TokenEstimator) UsagePercent(messages []Message) float64 {
	used := te.EstimateMessages(messages)
	return float64(used) / float64(te.modelContextWindow) * 100
}

// --- 辅助函数 ---

// detectContentType 检测文本的主要语言/类型
func detectContentType(text string) ContentType {
	if text == "" {
		return ContentMixed
	}

	cjkCount := 0
	latinCount := 0
	codeCount := 0
	total := 0

	for _, r := range text {
		total++
		switch {
		case isCJK(r):
			cjkCount++
		case isCodeChar(r):
			codeCount++
		default:
			latinCount++
		}
	}

	if total == 0 {
		return ContentMixed
	}

	cjkRatio := float64(cjkCount) / float64(total)
	codeRatio := float64(codeCount) / float64(total)

	if cjkRatio > 0.3 {
		return ContentChinese
	}
	if codeRatio > 0.15 {
		return ContentCode
	}
	if cjkRatio > 0.05 {
		return ContentMixed
	}
	return ContentEnglish
}

// charsPerToken 根据内容类型返回每 token 的字符数
func charsPerToken(ct ContentType) float64 {
	switch ct {
	case ContentEnglish:
		return 4.0 // 英文约 4 字符/token
	case ContentChinese:
		return 1.5 // 中文约 1.5 字符/token
	case ContentCode:
		return 3.0 // 代码约 3 字符/token
	case ContentMixed:
		return 2.5 // 混合内容取中间值
	default:
		return 3.0
	}
}

// isCJK 判断是否是 CJK 字符
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x3000 && r <= 0x303F) || // CJK Symbols and Punctuation
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Korean Syllables
}

// isCodeChar 判断是否是代码字符（大括号、分号等）
func isCodeChar(r rune) bool {
	return r == '{' || r == '}' || r == '(' || r == ')' ||
		r == '[' || r == ']' || r == ';' || r == '=' ||
		r == '<' || r == '>' || r == '/' || r == '\\' ||
		r == '&' || r == '|' || r == '!' || r == '@' ||
		r == '#' || r == '$' || r == '%' || r == '^' ||
		r == '*' || r == '~' || r == '`'
}

func tokenEstimateCacheKey(text string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	return h.Sum64()
}
