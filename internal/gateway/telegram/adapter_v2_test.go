package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

// ── isMentioned / isReplyToBot ────────────────────────────────────────────────
// These require *tgbotapi.Message objects which are hard to construct in tests.
// Tested indirectly through integration tests.

// ── renderContent ────────────────────────────────────────────────────────────

func TestRenderContent_Thinking(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.SetThinking("Analyzing your request...")
	content := sm.renderContent()
	assert.Contains(t, content, "Analyzing")
	assert.Contains(t, content, "🧠")
}

func TestRenderContent_ToolCall(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.SetToolCall("web_search", `{"query":"test"}`)
	content := sm.renderContent()
	assert.Contains(t, content, "web_search")
	assert.Contains(t, content, "🔧")
}

func TestRenderContent_Result(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.SetResult("Found 3 results")
	content := sm.renderContent()
	assert.Contains(t, content, "Found 3 results")
}

func TestRenderContent_PlainText(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}
	sm.content = "Hello, world!"

	content := sm.renderContent()
	assert.Contains(t, content, "Hello, world!")
}

func TestRenderContent_DefaultThinking(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}
	// Both thinking and content empty → default
	content := sm.renderContent()
	assert.Contains(t, content, "Thinking...")
}

func TestRenderContent_Truncation(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}
	// Very long content should be truncated
	longContent := ""
	for i := 0; i < 5000; i++ {
		longContent += "x"
	}
	sm.content = longContent
	content := sm.renderContent()
	assert.LessOrEqual(t, len(content), 4096)
}

func TestRenderContent_TruncationClosesOpenCodeFence(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.content = "before\n```go\n" + strings.Repeat("fmt.Println(\"hello\")\n", 260) + "```\nafter fence"

	content := sm.renderContent()
	rendered := formatTelegramRichText(content)
	assert.Equal(t, strings.Count(rendered, "<pre>"), strings.Count(rendered, "</pre>"))
	assert.Equal(t, strings.Count(rendered, "<code>"), strings.Count(rendered, "</code>"))
	assert.Contains(t, rendered, "</code></pre>\n...")
}

func TestRenderContent_KeepsTextAfterClosedCodeFenceOutsidePre(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.content = "before\n```go\nfmt.Println(\"hello\")\n```\nafter fence"

	rendered := formatTelegramRichText(sm.renderContent())
	assert.Contains(t, rendered, "</code></pre>\nafter fence")
}

// ── telegramStreamSender Append ──────────────────────────────────────────────

func TestStreamSender_Append(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.Append("Hello")
	sm.Append(" World")
	assert.Equal(t, "Hello World", sm.content)
}

func TestStreamSender_AppendAfterFinish(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter, finished: true}

	err := sm.Append("should fail")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already finished")
}

// ── telegramStreamSender Finish ──────────────────────────────────────────────

func TestStreamSender_Finish(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.content = "Done"
	sm.Finish()
	assert.True(t, sm.finished)
	assert.Empty(t, sm.thinking)
}

func TestStreamSender_FinishIdempotent(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	sm.Finish()
	err := sm.Finish()
	assert.NoError(t, err)
}

// ── telegramStreamSender MessageID ───────────────────────────────────────────

func TestStreamSender_MessageID(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter, messageID: 42}

	assert.Equal(t, "42", sm.MessageID())
}

// ── throttledEdit ────────────────────────────────────────────────────────────

func TestStreamSender_ThrottledEdit_MaxEdits(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter, editCount: maxEdits}

	// Should skip edit when max edits reached
	err := sm.throttledEdit()
	assert.NoError(t, err)
}

func TestStreamSender_ThrottledEdit_TooSoon(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter, lastEdit: time.Now().Add(time.Second)} // future time

	// Should skip edit when too soon
	err := sm.throttledEdit()
	assert.NoError(t, err)
}

// ── Config validation ────────────────────────────────────────────────────────

func TestConfig_MaxMessageLenClamp(t *testing.T) {
	cfg := Config{Token: "test", MaxMessageLen: 10000}
	adapter := NewAdapter(cfg)
	// splitMessage should clamp to 4096
	longMsg := ""
	for i := 0; i < 5000; i++ {
		longMsg += "x"
	}
	chunks := adapter.splitMessage(longMsg)
	for _, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 4096)
	}
}

// ── splitMessage edge cases ──────────────────────────────────────────────────

func TestSplitMessage_Empty(t *testing.T) {
	cfg := Config{Token: "test"}
	adapter := NewAdapter(cfg)
	chunks := adapter.splitMessage("")
	assert.Len(t, chunks, 1)
	assert.Equal(t, "", chunks[0])
}

func TestSplitMessage_SingleChar(t *testing.T) {
	cfg := Config{Token: "test"}
	adapter := NewAdapter(cfg)
	chunks := adapter.splitMessage("a")
	assert.Len(t, chunks, 1)
	assert.Equal(t, "a", chunks[0])
}

// ── Adapter Stop/IsRunning ───────────────────────────────────────────────────

func TestAdapter_StopIdempotent(t *testing.T) {
	cfg := Config{Token: "test"}
	adapter := NewAdapter(cfg)

	err := adapter.Stop()
	assert.NoError(t, err)
	assert.False(t, adapter.IsRunning())

	// Second stop should also work
	err = adapter.Stop()
	assert.NoError(t, err)
}

// ── SetHandler ───────────────────────────────────────────────────────────────

func TestAdapter_SetHandlerV2(t *testing.T) {
	cfg := Config{Token: "test"}
	adapter := NewAdapter(cfg)

	adapter.SetHandler(func(_ context.Context, _ *gateway.Message) error {
		return nil
	})
	assert.NotNil(t, adapter.handler)
}

// ── ToolCall args truncation ─────────────────────────────────────────────────

func TestStreamSender_SetToolCall_ArgsTruncation(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	longArgs := ""
	for i := 0; i < 200; i++ {
		longArgs += "x"
	}
	sm.SetToolCall("tool", longArgs)
	assert.Contains(t, sm.thinking, "tool")
	assert.Contains(t, sm.thinking, "...")
	assert.LessOrEqual(t, len(sm.thinking), 120) // reasonable bound
}

func TestStreamSender_SetToolCall_EmptyArgs(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter}

	_ = sm.SetToolCall("正在联网搜索：『test』", "")
	assert.Equal(t, "🔧 正在联网搜索：『test』", sm.thinking)
	assert.NotContains(t, sm.thinking, "()")
}

// ── SetThinking after finish ─────────────────────────────────────────────────

func TestStreamSender_SetThinkingAfterFinish(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter, finished: true}

	err := sm.SetThinking("should be ignored")
	assert.NoError(t, err)
	assert.Empty(t, sm.thinking)
}

// ── SetResult after finish ───────────────────────────────────────────────────

func TestStreamSender_SetResultAfterFinish(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	sm := &telegramStreamSender{adapter: adapter, finished: true, content: "original"}

	err := sm.SetResult("should be ignored")
	assert.NoError(t, err)
	assert.Equal(t, "original", sm.content)
}
