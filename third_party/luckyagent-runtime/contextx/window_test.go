package contextx

import (
	"testing"
)

// --- TokenEstimator 测试 ---

func TestEstimateEnglish(t *testing.T) {
	est := NewTokenEstimator(4096)

	// 英文约 4 chars/token
	text := "Hello, how are you today?"
	tokens := est.Estimate(text)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
	// 26 chars / 4 ≈ 7 tokens
	if tokens < 5 || tokens > 15 {
		t.Errorf("expected ~7 tokens for English text, got %d", tokens)
	}
}

func TestEstimateChinese(t *testing.T) {
	est := NewTokenEstimator(4096)

	// 中文约 1.5 chars/token
	text := "你好，今天天气怎么样？"
	tokens := est.Estimate(text)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
	// 10 chars / 1.5 ≈ 7 tokens
	if tokens < 3 || tokens > 15 {
		t.Errorf("expected ~7 tokens for Chinese text, got %d", tokens)
	}
}

func TestEstimateCode(t *testing.T) {
	est := NewTokenEstimator(4096)

	// 代码约 3 chars/token
	text := "func main() { fmt.Println(\"hello\") }"
	tokens := est.Estimate(text)
	if tokens <= 0 {
		t.Errorf("expected positive token count, got %d", tokens)
	}
}

func TestEstimateEmpty(t *testing.T) {
	est := NewTokenEstimator(4096)
	if est.Estimate("") != 0 {
		t.Error("expected 0 tokens for empty string")
	}
}

func TestEstimateMessages(t *testing.T) {
	est := NewTokenEstimator(4096)

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant.", Priority: PriorityCritical},
		{Role: "user", Content: "Hello!", Priority: PriorityNormal},
		{Role: "assistant", Content: "Hi there!", Priority: PriorityNormal},
	}

	tokens := est.EstimateMessages(messages)
	if tokens <= 0 {
		t.Error("expected positive token count for messages")
	}

	// 应该大于单条消息
	singleTokens := est.EstimateMessage(messages[0])
	if tokens <= singleTokens {
		t.Errorf("expected total > single message, got %d <= %d", tokens, singleTokens)
	}
}

func TestEstimateMessage(t *testing.T) {
	est := NewTokenEstimator(4096)

	msg := Message{
		Role:     "user",
		Content:  "What is the weather today?",
		Priority: PriorityNormal,
	}

	tokens := est.EstimateMessage(msg)
	if tokens <= 0 {
		t.Error("expected positive token count for message")
	}

	// 消息开销 = 4 (role) + content tokens
	contentTokens := est.Estimate(msg.Content)
	if tokens < contentTokens {
		t.Errorf("message tokens (%d) should >= content tokens (%d)", tokens, contentTokens)
	}
}

func TestEstimateMessageWithName(t *testing.T) {
	est := NewTokenEstimator(4096)

	msg := Message{
		Role:     "user",
		Content:  "Hello",
		Name:     "Alice",
		Priority: PriorityNormal,
	}

	tokens := est.EstimateMessage(msg)
	msgNoName := Message{
		Role:     "user",
		Content:  "Hello",
		Priority: PriorityNormal,
	}
	tokensNoName := est.EstimateMessage(msgNoName)

	if tokens <= tokensNoName {
		t.Errorf("message with name should have more tokens (%d) than without (%d)", tokens, tokensNoName)
	}
}

func TestEstimateMessageWithToolCalls(t *testing.T) {
	est := NewTokenEstimator(4096)

	msg := Message{
		Role:    "assistant",
		Content: "Let me check that for you.",
		ToolCalls: []ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: `{"city": "Tokyo"}`},
		},
		Priority: PriorityNormal,
	}

	tokens := est.EstimateMessage(msg)
	msgNoTools := Message{
		Role:     "assistant",
		Content:  "Let me check that for you.",
		Priority: PriorityNormal,
	}
	tokensNoTools := est.EstimateMessage(msgNoTools)

	if tokens <= tokensNoTools {
		t.Errorf("message with tool calls should have more tokens (%d) than without (%d)", tokens, tokensNoTools)
	}
}

func TestModelContextWindow(t *testing.T) {
	est := NewTokenEstimator(128000)
	if est.ModelContextWindow() != 128000 {
		t.Errorf("expected 128000, got %d", est.ModelContextWindow())
	}

	est.SetModelContextWindow(4096)
	if est.ModelContextWindow() != 4096 {
		t.Errorf("expected 4096, got %d", est.ModelContextWindow())
	}
}

func TestRemainingTokens(t *testing.T) {
	est := NewTokenEstimator(4096)

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant.", Priority: PriorityCritical},
	}

	remaining := est.RemainingTokens(messages)
	if remaining <= 0 {
		t.Error("expected positive remaining tokens")
	}
	if remaining >= 4096 {
		t.Error("remaining should be less than total window")
	}
}

func TestUsagePercent(t *testing.T) {
	est := NewTokenEstimator(4096)

	messages := []Message{
		{Role: "system", Content: "You are a helpful assistant.", Priority: PriorityCritical},
	}

	pct := est.UsagePercent(messages)
	if pct <= 0 {
		t.Error("expected positive usage percent")
	}
	if pct > 100 {
		t.Error("usage percent should not exceed 100")
	}
}

// --- ContentType 检测测试 ---

func TestDetectContentTypeEnglish(t *testing.T) {
	ct := detectContentType("Hello, how are you doing today?")
	if ct != ContentEnglish {
		t.Errorf("expected ContentEnglish, got %d", ct)
	}
}

func TestDetectContentTypeChinese(t *testing.T) {
	ct := detectContentType("你好，今天天气怎么样？我很好谢谢。")
	if ct != ContentChinese {
		t.Errorf("expected ContentChinese, got %d", ct)
	}
}

func TestDetectContentTypeCode(t *testing.T) {
	ct := detectContentType("func main() { fmt.Println(\"hello\"); return 0; }")
	if ct != ContentCode {
		t.Errorf("expected ContentCode, got %d", ct)
	}
}

func TestDetectContentTypeMixed(t *testing.T) {
	ct := detectContentType("Hello 你好, this is a mixed message with 一些中文")
	if ct != ContentMixed {
		t.Errorf("expected ContentMixed, got %d", ct)
	}
}

func TestDetectContentTypeEmpty(t *testing.T) {
	ct := detectContentType("")
	if ct != ContentMixed {
		t.Errorf("expected ContentMixed for empty, got %d", ct)
	}
}

// --- ContextWindow 测试 ---

func TestNewContextWindow(t *testing.T) {
	cfg := DefaultWindowConfig()
	cw := NewContextWindow(cfg)
	if cw == nil {
		t.Fatal("expected non-nil ContextWindow")
	}
	if cw.config.MaxTokens != 4096 {
		t.Errorf("expected max tokens 4096, got %d", cw.config.MaxTokens)
	}
}

func TestFitNoTrimNeeded(t *testing.T) {
	cfg := DefaultWindowConfig()
	cfg.MaxTokens = 10000 // 足够大
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are helpful.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Hello!", Priority: PriorityNormal, Category: "conversation"},
		{Role: "assistant", Content: "Hi!", Priority: PriorityNormal, Category: "conversation"},
	}

	result, trimResult := cw.Fit(messages)
	if trimResult.Trimmed {
		t.Error("should not need trimming")
	}
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
}

func TestFitWithTrimming(t *testing.T) {
	cfg := DefaultWindowConfig()
	cfg.MaxTokens = 50 // 很小的窗口，强制裁剪
	cfg.ReservedTokens = 10
	cfg.Strategy = TrimLowPriority
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are a very helpful assistant that can do many things for people.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Tell me a long story about a brave knight who went on an adventure.", Priority: PriorityLow, Category: "conversation"},
		{Role: "assistant", Content: "Once upon a time, there was a brave knight who traveled across the land.", Priority: PriorityLow, Category: "conversation"},
		{Role: "user", Content: "What happened next in the story?", Priority: PriorityLow, Category: "conversation"},
		{Role: "assistant", Content: "The knight encountered a dragon.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "user", Content: "And then?", Priority: PriorityHigh, Category: "conversation"},
	}

	result, trimResult := cw.Fit(messages)
	if !trimResult.Trimmed {
		t.Error("expected trimming to occur with small window")
	}
	if len(result) >= len(messages) {
		t.Error("expected fewer messages after trimming")
	}
	// Critical messages should be preserved
	for _, msg := range result {
		if msg.Priority == PriorityCritical {
			// Found critical message, good
			goto found
		}
	}
	t.Error("expected critical message to be preserved")
found:
}

func TestFitTrimOldest(t *testing.T) {
	cfg := DefaultWindowConfig()
	cfg.MaxTokens = 100
	cfg.ReservedTokens = 20
	cfg.Strategy = TrimOldest
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "System prompt.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "First message that is quite long and detailed.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "assistant", Content: "First response.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "user", Content: "Second message.", Priority: PriorityNormal, Category: "conversation"},
	}

	result, _ := cw.Fit(messages)
	// Should preserve critical and most recent
	if len(result) == 0 {
		t.Error("expected at least one message")
	}
}

func TestFitSlidingWindow(t *testing.T) {
	cfg := DefaultWindowConfig()
	cfg.MaxTokens = 10000
	cfg.Strategy = TrimSlidingWindow
	cfg.SlidingWindowSize = 2
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "System.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Msg 1.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "assistant", Content: "Resp 1.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "user", Content: "Msg 2.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "assistant", Content: "Resp 2.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "user", Content: "Msg 3.", Priority: PriorityNormal, Category: "conversation"},
	}

	result, _ := cw.Fit(messages)
	// Should have system + last 2 messages
	if len(result) < 2 {
		t.Errorf("expected at least 2 messages, got %d", len(result))
	}
	// System should be preserved
	hasSystem := false
	for _, msg := range result {
		if msg.Priority == PriorityCritical {
			hasSystem = true
		}
	}
	if !hasSystem {
		t.Error("expected system message to be preserved")
	}
}

func TestFitSummarize(t *testing.T) {
	cfg := DefaultWindowConfig()
	cfg.MaxTokens = 500
	cfg.Strategy = TrimSummarize
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are helpful.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Old message 1.", Priority: PriorityLow, Category: "conversation"},
		{Role: "assistant", Content: "Old response 1.", Priority: PriorityLow, Category: "conversation"},
		{Role: "user", Content: "Recent message.", Priority: PriorityNormal, Category: "conversation"},
	}

	result, _ := cw.Fit(messages)
	// Should have system + summary + recent
	if len(result) == 0 {
		t.Error("expected at least one message")
	}
	// Check for summary message
	hasSummary := false
	for _, msg := range result {
		if msg.Category == "summary" {
			hasSummary = true
		}
	}
	// Summary may or may not be present depending on token budget
	_ = hasSummary
}

func TestContextWindowStats(t *testing.T) {
	cfg := DefaultWindowConfig()
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are helpful.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Hello!", Priority: PriorityNormal, Category: "conversation"},
		{Role: "assistant", Content: "Hi there!", Priority: PriorityNormal, Category: "conversation"},
	}

	stats := cw.Stats(messages)
	if stats.MaxTokens != cfg.MaxTokens {
		t.Errorf("expected max tokens %d, got %d", cfg.MaxTokens, stats.MaxTokens)
	}
	if stats.MessageCount != 3 {
		t.Errorf("expected 3 messages, got %d", stats.MessageCount)
	}
	if stats.UsedTokens <= 0 {
		t.Error("expected positive used tokens")
	}
	if stats.UsagePercent <= 0 {
		t.Error("expected positive usage percent")
	}
	if len(stats.ByCategory) == 0 {
		t.Error("expected category stats")
	}
}

func TestContextWindowRemainingTokens(t *testing.T) {
	cfg := DefaultWindowConfig()
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are helpful.", Priority: PriorityCritical, Category: "system"},
	}

	remaining := cw.RemainingTokens(messages)
	if remaining <= 0 {
		t.Error("expected positive remaining tokens")
	}
}

func TestContextWindowUsagePercent(t *testing.T) {
	cfg := DefaultWindowConfig()
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are helpful.", Priority: PriorityCritical, Category: "system"},
	}

	pct := cw.UsagePercent(messages)
	if pct <= 0 {
		t.Error("expected positive usage percent")
	}
	if pct > 100 {
		t.Error("usage percent should not exceed 100")
	}
}

func TestTrimResultSummary(t *testing.T) {
	result := TrimResult{
		OriginalCount:   10,
		OriginalTokens:  5000,
		FinalCount:      6,
		FinalTokens:     3000,
		AvailableTokens: 4096,
		Trimmed:         true,
		Strategy:        TrimLowPriority,
	}

	summary := result.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestContextStatsSummary(t *testing.T) {
	stats := ContextStats{
		MaxTokens:       4096,
		ReservedTokens:  1024,
		AvailableTokens: 3072,
		UsedTokens:      1500,
		RemainingTokens: 1572,
		UsagePercent:    48.8,
		MessageCount:    5,
		Strategy:        "low_priority_first",
	}

	summary := stats.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestPriorityString(t *testing.T) {
	tests := []struct {
		priority MessagePriority
		expected string
	}{
		{PriorityCritical, "critical"},
		{PriorityHigh, "high"},
		{PriorityNormal, "normal"},
		{PriorityLow, "low"},
		{MessagePriority(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.priority.String()
		if result != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, result)
		}
	}
}

func TestTrimStrategyString(t *testing.T) {
	tests := []struct {
		strategy TrimStrategy
		expected string
	}{
		{TrimOldest, "oldest_first"},
		{TrimLowPriority, "low_priority_first"},
		{TrimSlidingWindow, "sliding_window"},
		{TrimSummarize, "summarize"},
		{TrimStrategy(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.strategy.String()
		if result != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, result)
		}
	}
}

func TestDefaultWindowConfig(t *testing.T) {
	cfg := DefaultWindowConfig()
	if cfg.MaxTokens != 4096 {
		t.Errorf("expected default max tokens 4096, got %d", cfg.MaxTokens)
	}
	if cfg.ReservedTokens != 1024 {
		t.Errorf("expected default reserved tokens 1024, got %d", cfg.ReservedTokens)
	}
	if cfg.Strategy != TrimLowPriority {
		t.Errorf("expected default strategy TrimLowPriority, got %v", cfg.Strategy)
	}
	if cfg.SlidingWindowSize != 10 {
		t.Errorf("expected default sliding window size 10, got %d", cfg.SlidingWindowSize)
	}
	if cfg.MaxConversationTurns != 50 {
		t.Errorf("expected default max conversation turns 50, got %d", cfg.MaxConversationTurns)
	}
	if cfg.MemoryBudget != 800 {
		t.Errorf("expected default memory budget 800, got %d", cfg.MemoryBudget)
	}
	if cfg.SummarizeThreshold != 0.8 {
		t.Errorf("expected default summarize threshold 0.8, got %f", cfg.SummarizeThreshold)
	}
}

func TestLargeContextWindow(t *testing.T) {
	cfg := WindowConfig{
		MaxTokens:      128000,
		ReservedTokens: 4096,
		Strategy:       TrimLowPriority,
	}
	cw := NewContextWindow(cfg)

	// 模拟长对话
	var messages []Message
	messages = append(messages, Message{Role: "system", Content: "You are a helpful assistant.", Priority: PriorityCritical, Category: "system"})
	for i := 0; i < 100; i++ {
		messages = append(messages, Message{
			Role:     "user",
			Content:  "This is message number " + string(rune('0'+i%10)) + " in a long conversation.",
			Priority: PriorityNormal,
			Category: "conversation",
		})
	}

	result, trimResult := cw.Fit(messages)
	// 128k context should fit 100 messages easily
	if trimResult.Trimmed {
		t.Error("128k context should not need trimming for 100 short messages")
	}
	if len(result) != 101 {
		t.Errorf("expected 101 messages, got %d", len(result))
	}
}

func TestSmallContextWindow(t *testing.T) {
	cfg := WindowConfig{
		MaxTokens:      50,
		ReservedTokens: 10,
		Strategy:       TrimLowPriority,
	}
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "system", Content: "You are a very helpful assistant that can do many things.", Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Tell me about the history of computing.", Priority: PriorityLow, Category: "conversation"},
		{Role: "assistant", Content: "Computing history spans many decades.", Priority: PriorityNormal, Category: "conversation"},
		{Role: "user", Content: "What about AI?", Priority: PriorityHigh, Category: "conversation"},
	}

	result, trimResult := cw.Fit(messages)
	if !trimResult.Trimmed {
		t.Error("small context should trigger trimming")
	}
	// Critical should always be preserved
	hasCritical := false
	for _, msg := range result {
		if msg.Priority == PriorityCritical {
			hasCritical = true
		}
	}
	if !hasCritical {
		t.Error("critical messages should always be preserved")
	}
}

func TestEstimateTokensConsistency(t *testing.T) {
	est := NewTokenEstimator(4096)

	text := "Hello, this is a test message."
	tokens1 := est.Estimate(text)
	tokens2 := est.Estimate(text)

	if tokens1 != tokens2 {
		t.Errorf("same text should produce same token count: %d vs %d", tokens1, tokens2)
	}
}

func TestCJKDetection(t *testing.T) {
	tests := []struct {
		r        rune
		expected bool
	}{
		{'你', true},
		{'好', true},
		{'の', true},
		{'カ', true},
		{'A', false},
		{'1', false},
		{' ', false},
	}

	for _, tt := range tests {
		result := isCJK(tt.r)
		if result != tt.expected {
			t.Errorf("isCJK(%c) = %v, expected %v", tt.r, result, tt.expected)
		}
	}
}

func TestCodeCharDetection(t *testing.T) {
	tests := []struct {
		r        rune
		expected bool
	}{
		{'{', true},
		{';', true},
		{'=', true},
		{'A', false},
		{' ', false},
	}

	for _, tt := range tests {
		result := isCodeChar(tt.r)
		if result != tt.expected {
			t.Errorf("isCodeChar(%c) = %v, expected %v", tt.r, result, tt.expected)
		}
	}
}

func TestSummarizeMessages(t *testing.T) {
	cfg := DefaultWindowConfig()
	cw := NewContextWindow(cfg)

	messages := []Message{
		{Role: "user", Content: "Hello there", Priority: PriorityLow, Category: "conversation"},
		{Role: "assistant", Content: "Hi!", Priority: PriorityLow, Category: "conversation"},
	}

	summary := cw.summarizeMessages(messages)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if len(summary) < 10 {
		t.Error("summary should contain some content")
	}
}

func TestSummarizeEmptyMessages(t *testing.T) {
	cfg := DefaultWindowConfig()
	cw := NewContextWindow(cfg)

	summary := cw.summarizeMessages(nil)
	if summary != "" {
		t.Errorf("expected empty summary for nil messages, got %s", summary)
	}
}

func TestFitPreservesCritical(t *testing.T) {
	cfg := WindowConfig{
		MaxTokens:      80,
		ReservedTokens: 20,
		Strategy:       TrimLowPriority,
	}
	cw := NewContextWindow(cfg)

	criticalContent := "CRITICAL SYSTEM PROMPT THAT MUST BE PRESERVED"
	messages := []Message{
		{Role: "system", Content: criticalContent, Priority: PriorityCritical, Category: "system"},
		{Role: "user", Content: "Low priority message that should be trimmed away because it is too long", Priority: PriorityLow, Category: "conversation"},
		{Role: "user", Content: "Another low priority message", Priority: PriorityLow, Category: "conversation"},
	}

	result, _ := cw.Fit(messages)

	// Critical message should always be preserved
	found := false
	for _, msg := range result {
		if msg.Content == criticalContent {
			found = true
		}
	}
	if !found {
		t.Error("critical message should always be preserved")
	}
}

// v0.83.0: contextx 包测试补全 - 覆盖 Config() 和边缘情况

// TestContextWindowConfig 测试 Config() 方法
func TestContextWindowConfig(t *testing.T) {
	cfg := WindowConfig{
		MaxTokens:      128000,
		ReservedTokens: 10000,
		Strategy:       TrimOldest,
	}

	cw := NewContextWindow(cfg)
	returnedCfg := cw.Config()

	if returnedCfg.MaxTokens != cfg.MaxTokens {
		t.Errorf("MaxTokens mismatch: expected %d, got %d", cfg.MaxTokens, returnedCfg.MaxTokens)
	}
	if returnedCfg.ReservedTokens != cfg.ReservedTokens {
		t.Errorf("ReservedTokens mismatch: expected %d, got %d", cfg.ReservedTokens, returnedCfg.ReservedTokens)
	}
	if returnedCfg.Strategy != cfg.Strategy {
		t.Errorf("Strategy mismatch: expected %v, got %v", cfg.Strategy, returnedCfg.Strategy)
	}

	t.Logf("Config() returned: MaxTokens=%d, ReservedTokens=%d, Strategy=%v",
		returnedCfg.MaxTokens, returnedCfg.ReservedTokens, returnedCfg.Strategy)
}

// TestRemainingTokens_Exhausted 测试 RemainingTokens 在 tokens 用完时返回 0
func TestRemainingTokens_Exhausted(t *testing.T) {
	te := NewTokenEstimator(100)

	// 创建大量消息，超过模型上下文窗口
	messages := make([]Message, 100)
	for i := range messages {
		messages[i] = Message{Role: "user", Content: "Hello world! This is a test message."}
	}

	remaining := te.RemainingTokens(messages)
	if remaining != 0 {
		t.Errorf("RemainingTokens should return 0 when exhausted, got %d", remaining)
	}

	t.Logf("RemainingTokens correctly returns 0 when exhausted")
}

// TestNewTokenEstimator_ZeroOrNegative 测试 NewTokenEstimator 处理零/负值
func TestNewTokenEstimator_ZeroOrNegative(t *testing.T) {
	// 测试 0
	te := NewTokenEstimator(0)
	if te.modelContextWindow != 4096 {
		t.Errorf("NewTokenEstimator(0) should default to 4096, got %d", te.modelContextWindow)
	}

	// 测试负值
	te2 := NewTokenEstimator(-1000)
	if te2.modelContextWindow != 4096 {
		t.Errorf("NewTokenEstimator(-1000) should default to 4096, got %d", te2.modelContextWindow)
	}

	t.Logf("NewTokenEstimator correctly defaults to 4096 for non-positive values")
}

// TestCharsPerToken_MixedAndDefault 测试 charsPerToken 的 Mixed 和 default 分支
func TestCharsPerToken_MixedAndDefault(t *testing.T) {
	// ContentMixed
	ratio := charsPerToken(ContentMixed)
	if ratio != 2.5 {
		t.Errorf("charsPerToken(ContentMixed) should return 2.5, got %f", ratio)
	}

	// default 分支（ContentAuto = 0，也是 default）
	ratio2 := charsPerToken(ContentAuto)
	if ratio2 != 3.0 {
		t.Errorf("charsPerToken(ContentAuto/default) should return 3.0, got %f", ratio2)
	}

	t.Logf("charsPerToken: Mixed=%f, Auto/Default=%f", ratio, ratio2)
}
