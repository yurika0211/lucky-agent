package memory

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestShortTermBufferAdd(t *testing.T) {
	buf := NewShortTermBuffer(5)

	buf.Add("user", "hello")
	buf.Add("assistant", "hi there")

	if buf.MessageCount() != 2 {
		t.Errorf("expected 2 messages, got %d", buf.MessageCount())
	}
}

func TestShortTermBufferWindowOverflow(t *testing.T) {
	buf := NewShortTermBuffer(3) // 只保留 3 条

	// 添加 5 条消息，超出窗口
	buf.Add("user", "msg1")
	buf.Add("assistant", "resp1")
	buf.Add("user", "msg2")
	buf.Add("assistant", "resp2")
	buf.Add("user", "msg3")

	// 应该只保留最近 3 条
	if buf.MessageCount() != 3 {
		t.Errorf("expected 3 messages after overflow, got %d", buf.MessageCount())
	}

	// 应该有摘要
	if buf.Summary() == "" {
		t.Error("expected summary after overflow, got empty")
	}
}

func TestShortTermBufferNoCompressWithinWindow(t *testing.T) {
	buf := NewShortTermBuffer(10)

	// 添加 5 条消息，不超出窗口
	for i := 0; i < 5; i++ {
		buf.Add("user", "message")
	}

	if buf.MessageCount() != 5 {
		t.Errorf("expected 5 messages, got %d", buf.MessageCount())
	}

	// 不应该有摘要
	if buf.Summary() != "" {
		t.Error("expected no summary when within window")
	}
}

func TestShortTermBufferGetContext(t *testing.T) {
	buf := NewShortTermBuffer(5)

	buf.Add("user", "hello")
	buf.Add("assistant", "hi")

	ctx := buf.GetContext()
	if len(ctx) != 2 {
		t.Errorf("expected 2 context messages, got %d", len(ctx))
	}

	// 验证消息内容
	if ctx[0].Role != "user" || ctx[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", ctx[0])
	}
	if ctx[1].Role != "assistant" || ctx[1].Content != "hi" {
		t.Errorf("unexpected second message: %+v", ctx[1])
	}
}

func TestShortTermBufferGetContextWithSummary(t *testing.T) {
	buf := NewShortTermBuffer(3)

	// 添加 5 条消息触发压缩
	buf.Add("user", "msg1 about Go")
	buf.Add("assistant", "resp1 about API")
	buf.Add("user", "msg2 about debugging")
	buf.Add("assistant", "resp2 about fix")
	buf.Add("user", "msg3 about testing")

	ctx := buf.GetContext()

	// 应该有 1 条摘要 + 3 条最近消息
	if len(ctx) != 4 {
		t.Errorf("expected 4 context messages (1 summary + 3 recent), got %d", len(ctx))
	}

	// 第一条应该是摘要（system 消息）
	if ctx[0].Role != "system" {
		t.Errorf("expected first message to be system (summary), got %s", ctx[0].Role)
	}
	if ctx[0].Content == "" {
		t.Error("expected non-empty summary content")
	}

	// 后续应该是最近 3 条消息
	if ctx[1].Role != "user" || ctx[2].Role != "assistant" || ctx[3].Role != "user" {
		t.Error("expected recent messages after summary")
	}
}

func TestShortTermBufferCompress(t *testing.T) {
	buf := NewShortTermBuffer(3)

	buf.Add("user", "msg1")
	buf.Add("assistant", "resp1")
	buf.Add("user", "msg2")
	buf.Add("assistant", "resp2")
	buf.Add("user", "msg3")

	// 手动调用 Compress（应该无操作，因为已经压缩过了）
	buf.Compress()

	if buf.MessageCount() != 3 {
		t.Errorf("expected 3 messages, got %d", buf.MessageCount())
	}
}

func TestShortTermBufferClear(t *testing.T) {
	buf := NewShortTermBuffer(5)

	buf.Add("user", "hello")
	buf.Add("assistant", "hi")
	buf.Clear()

	if buf.MessageCount() != 0 {
		t.Errorf("expected 0 messages after clear, got %d", buf.MessageCount())
	}
	if buf.Summary() != "" {
		t.Error("expected empty summary after clear")
	}
}

func TestShortTermBufferMaxTurns(t *testing.T) {
	buf := NewShortTermBuffer(0) // 应该默认为 10
	if buf.MaxTurns() != 10 {
		t.Errorf("expected default maxTurns 10, got %d", buf.MaxTurns())
	}

	buf2 := NewShortTermBuffer(-1) // 应该默认为 10
	if buf2.MaxTurns() != 10 {
		t.Errorf("expected default maxTurns 10 for negative, got %d", buf2.MaxTurns())
	}
}

func TestShortTermBufferMultipleCompressions(t *testing.T) {
	buf := NewShortTermBuffer(3)

	// 第一轮压缩
	buf.Add("user", "msg1 about Go")
	buf.Add("assistant", "resp1")
	buf.Add("user", "msg2")
	buf.Add("assistant", "resp2")
	buf.Add("user", "msg3")

	if buf.MessageCount() != 3 {
		t.Errorf("expected 3 after first compress, got %d", buf.MessageCount())
	}

	// 第二轮压缩
	buf.Add("assistant", "resp3")
	buf.Add("user", "msg4 about Rust")
	buf.Add("assistant", "resp4")

	if buf.MessageCount() != 3 {
		t.Errorf("expected 3 after second compress, got %d", buf.MessageCount())
	}

	// 摘要应该包含两轮的信息
	summary := buf.Summary()
	if summary == "" {
		t.Error("expected non-empty summary after multiple compressions")
	}
}

func TestShortTermBufferStructuredSummary(t *testing.T) {
	buf := NewShortTermBuffer(2)

	buf.Add("user", "I decided to use Go for the project")
	buf.Add("assistant", "Good choice, Go is great for backend services")
	buf.Add("user", "How do I implement the API?")

	summary := buf.Summary()
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}

	// 验证结构化模板格式
	if !contains(summary, "[Prior Conversation Summary]") {
		t.Error("expected structured summary header")
	}
}

func TestShortTermBufferGetContextReturnsProviderMessages(t *testing.T) {
	buf := NewShortTermBuffer(5)
	buf.Add("user", "hello")
	buf.Add("assistant", "hi")

	ctx := buf.GetContext()

	// 验证返回类型
	var _ []provider.Message = ctx
}

func TestShortTermBufferString(t *testing.T) {
	buf := NewShortTermBuffer(5)
	buf.Add("user", "hello")

	str := buf.String()
	if str == "" {
		t.Error("expected non-empty string representation")
	}
}

// --- 辅助函数测试 ---

func TestTruncateField(t *testing.T) {
	result := truncateField("short", 10)
	if result != "short" {
		t.Errorf("expected 'short', got '%s'", result)
	}

	result = truncateField("this is a very long string that should be truncated", 10)
	if result != "this is a" {
		t.Errorf("expected no-marker truncation, got %q", result)
	}
}

func TestExtractEntities(t *testing.T) {
	entities := extractEntities(`I want to use "PostgreSQL" for the database and Go for the backend`)
	if len(entities) == 0 {
		t.Error("expected some entities to be extracted")
	}

	// 应该包含引号中的内容
	found := false
	for _, e := range entities {
		if e == "PostgreSQL" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find 'PostgreSQL' in entities, got %v", entities)
	}
}

func TestExtractDecisions(t *testing.T) {
	decisions := extractDecisions("We decided to use Redis for caching")
	if len(decisions) == 0 {
		t.Error("expected some decisions to be extracted")
	}
}

func TestDedupSlice(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b"}
	result := dedupSlice(input)
	if len(result) != 3 {
		t.Errorf("expected 3 unique items, got %d: %v", len(result), result)
	}
}

// helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
