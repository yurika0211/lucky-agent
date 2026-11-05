package provider

import (
	"testing"
)

func TestCallOptionsDefault(t *testing.T) {
	cfg := Config{} // 使用默认 Config
	opts := DefaultCallOptions(cfg)
	if opts.ToolChoice != "auto" {
		t.Errorf("expected ToolChoice='auto', got %v", opts.ToolChoice)
	}
	if opts.MaxToolCalls != 5 {
		t.Errorf("expected MaxToolCalls=5, got %d", opts.MaxToolCalls)
	}
}

func TestFunctionCallingProviderInterface(t *testing.T) {
	// Verify OpenAIProvider implements FunctionCallingProvider
	var _ FunctionCallingProvider = (*OpenAIProvider)(nil)
	var _ FunctionCallingProvider = (*OpenAICompatibleProvider)(nil)
	var _ FunctionCallingProvider = (*OpenRouterProvider)(nil)
	var _ FunctionCallingProvider = (*AnthropicProvider)(nil)
}

func TestMessageWithToolCallID(t *testing.T) {
	msg := Message{
		Role:       "tool",
		Content:    "result data",
		ToolCallID: "call_abc123",
		Name:       "get_weather",
	}
	if msg.ToolCallID != "call_abc123" {
		t.Errorf("expected call_abc123, got %s", msg.ToolCallID)
	}
	if msg.Name != "get_weather" {
		t.Errorf("expected get_weather, got %s", msg.Name)
	}
}

func TestMessageWithToolCalls(t *testing.T) {
	msg := Message{
		Role:    "assistant",
		Content: "",
		ToolCalls: []ToolCall{
			{ID: "call_1", Name: "add", Arguments: `{"a":1,"b":2}`},
			{ID: "call_2", Name: "multiply", Arguments: `{"a":3,"b":4}`},
		},
	}
	if len(msg.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCalls[0].ID != "call_1" {
		t.Errorf("expected call_1, got %s", msg.ToolCalls[0].ID)
	}
}

func TestToOpenAIMessagesWithToolCalls(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What's 1+2?"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "add", Arguments: `{"a":1,"b":2}`},
		}},
		{Role: "tool", Content: "3", ToolCallID: "call_1", Name: "add"},
	}

	result, err := toOpenAIMessages(messages, "")
	if err != nil {
		t.Fatalf("toOpenAIMessages() error = %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(result))
	}

	// Check assistant message has tool_calls
	if len(result[1].ToolCalls) != 1 {
		t.Errorf("expected 1 tool_call in assistant message, got %d", len(result[1].ToolCalls))
	}
	if result[1].ToolCalls[0].Function.Name != "add" {
		t.Errorf("expected function name 'add', got %s", result[1].ToolCalls[0].Function.Name)
	}

	// Check tool message has tool_call_id
	if result[2].ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id 'call_1', got %s", result[2].ToolCallID)
	}
	if result[2].Name != "add" {
		t.Errorf("expected name 'add', got %s", result[2].Name)
	}
}

func TestToOpenAIMessagesWithReasoningContentForDeepseek(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "", ReasoningContent: "internal reasoning", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "add", Arguments: `{"a":1,"b":2}`},
		}},
	}

	result, err := toOpenAIMessages(messages, "deepseek-reasoner")
	if err != nil {
		t.Fatalf("toOpenAIMessages() error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].ReasoningContent != "internal reasoning" {
		t.Fatalf("expected reasoning content to be preserved, got %q", result[0].ReasoningContent)
	}
}

func TestToOpenAIMessagesBackfillsReasoningContentForDeepseekAssistantHistory(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "old answer"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "add", Arguments: `{"a":1,"b":2}`},
		}},
	}

	result, err := toOpenAIMessages(messages, "deepseek-v4-flash")
	if err != nil {
		t.Fatalf("toOpenAIMessages() error = %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	for i, msg := range result {
		if msg.ReasoningContent != missingReasoningContentPlaceholder {
			t.Fatalf("expected reasoning placeholder on message %d, got %q", i, msg.ReasoningContent)
		}
	}
}

func TestToOpenAIMessagesPreservesReasoningContent(t *testing.T) {
	messages := []Message{
		{Role: "assistant", Content: "", ReasoningContent: "internal reasoning"},
	}

	result, err := toOpenAIMessages(messages, "gpt-5.4")
	if err != nil {
		t.Fatalf("toOpenAIMessages() error = %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].ReasoningContent != "internal reasoning" {
		t.Fatalf("expected reasoning content to be preserved, got %q", result[0].ReasoningContent)
	}
}

func TestNewToolFunction(t *testing.T) {
	fn := map[string]any{
		"name":        "get_weather",
		"description": "Get weather for a city",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name",
				},
			},
		},
	}

	tf := newToolFunction(fn)
	if tf.Name != "get_weather" {
		t.Errorf("expected name 'get_weather', got %s", tf.Name)
	}
	if tf.Description != "Get weather for a city" {
		t.Errorf("expected description, got %s", tf.Description)
	}
	if tf.Parameters == nil {
		t.Error("expected non-nil parameters")
	}
}

func TestNewToolFunctionEmpty(t *testing.T) {
	fn := map[string]any{}
	tf := newToolFunction(fn)
	if tf.Name != "" {
		t.Errorf("expected empty name, got %s", tf.Name)
	}
}

func TestNormalizeToolProtocolMessagesPreservesCompleteRound(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "search"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "web_search", Arguments: `{"q":"a"}`},
			{ID: "call_2", Name: "current_time", Arguments: `{}`},
		}},
		{Role: "tool", Content: "result-a", ToolCallID: "call_1", Name: "web_search"},
		{Role: "tool", Content: "result-b", ToolCallID: "call_2", Name: "current_time"},
		{Role: "assistant", Content: "done"},
	}

	got := normalizeToolProtocolMessages(messages)
	if len(got) != len(messages) {
		t.Fatalf("expected complete round to be preserved, got %d messages", len(got))
	}
	if got[1].Role != "assistant" || len(got[1].ToolCalls) != 2 {
		t.Fatalf("expected assistant tool call message to remain intact: %+v", got[1])
	}
}

func TestNormalizeToolProtocolMessagesDropsOrphanToolMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "search"},
		{Role: "tool", Content: "orphan", ToolCallID: "call_orphan", Name: "web_search"},
		{Role: "assistant", Content: "done"},
	}

	got := normalizeToolProtocolMessages(messages)
	if len(got) != 2 {
		t.Fatalf("expected orphan tool to be dropped, got %d messages", len(got))
	}
	if got[1].Role != "assistant" {
		t.Fatalf("unexpected normalized sequence: %+v", got)
	}
}

func TestNormalizeToolProtocolMessagesDropsIncompleteRound(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "search"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "web_search", Arguments: `{"q":"a"}`},
			{ID: "call_2", Name: "current_time", Arguments: `{}`},
		}},
		{Role: "tool", Content: "result-a", ToolCallID: "call_1", Name: "web_search"},
		{Role: "assistant", Content: "should force round drop"},
	}

	got := normalizeToolProtocolMessages(messages)
	if len(got) != 2 {
		t.Fatalf("expected incomplete round to be dropped, got %d messages", len(got))
	}
	if got[0].Role != "user" || got[1].Role != "assistant" || got[1].Content != "should force round drop" {
		t.Fatalf("unexpected normalized sequence: %+v", got)
	}
}

func TestNormalizeToolProtocolMessagesDropsDuplicateToolResult(t *testing.T) {
	messages := []Message{
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "web_search", Arguments: `{"q":"a"}`},
		}},
		{Role: "tool", Content: "result-a", ToolCallID: "call_1", Name: "web_search"},
		{Role: "tool", Content: "duplicate", ToolCallID: "call_1", Name: "web_search"},
		{Role: "assistant", Content: "done"},
	}

	got := normalizeToolProtocolMessages(messages)
	if len(got) != 3 {
		t.Fatalf("expected duplicate tool result to be dropped, got %d messages", len(got))
	}
	if got[1].Role != "tool" || got[1].Content != "result-a" {
		t.Fatalf("unexpected normalized tool result: %+v", got[1])
	}
}
