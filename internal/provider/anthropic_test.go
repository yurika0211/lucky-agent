package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToAnthropicMessagesMapsOpenAIToolRound(t *testing.T) {
	system, messages, err := toAnthropicMessages([]Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "weather?"},
		{Role: "assistant", Content: "checking", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Paris"}`},
		}},
		{Role: "tool", Content: "rain", ToolCallID: "call_1", Name: "get_weather"},
	})
	if err != nil {
		t.Fatalf("toAnthropicMessages: %v", err)
	}
	if system != "system prompt" {
		t.Fatalf("system = %q", system)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %#v", len(messages), messages)
	}
	if messages[1].Role != "assistant" {
		t.Fatalf("expected assistant message, got %#v", messages[1])
	}
	assistantBlocks, ok := messages[1].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("assistant content type = %T", messages[1].Content)
	}
	if len(assistantBlocks) != 2 {
		t.Fatalf("expected text + tool_use blocks, got %#v", assistantBlocks)
	}
	if assistantBlocks[1].Type != "tool_use" || assistantBlocks[1].ID != "call_1" || assistantBlocks[1].Name != "get_weather" {
		t.Fatalf("unexpected tool_use block: %#v", assistantBlocks[1])
	}
	input, ok := assistantBlocks[1].Input.(map[string]any)
	if !ok || input["city"] != "Paris" {
		t.Fatalf("unexpected tool_use input: %#v", assistantBlocks[1].Input)
	}

	toolBlocks, ok := messages[2].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("tool result content type = %T", messages[2].Content)
	}
	if messages[2].Role != "user" || len(toolBlocks) != 1 {
		t.Fatalf("unexpected tool result message: %#v", messages[2])
	}
	if toolBlocks[0].Type != "tool_result" || toolBlocks[0].ToolUseID != "call_1" || toolBlocks[0].Content != "rain" {
		t.Fatalf("unexpected tool_result block: %#v", toolBlocks[0])
	}
}

func TestToAnthropicMessagesGroupsMultipleToolResults(t *testing.T) {
	_, messages, err := toAnthropicMessages([]Message{
		{Role: "user", Content: "use tools"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "toolu_1", Name: "read_file", Arguments: `{"path":"a"}`},
			{ID: "toolu_2", Name: "read_file", Arguments: `{"path":"b"}`},
		}},
		{Role: "tool", Content: "a-content", ToolCallID: "toolu_1", Name: "read_file"},
		{Role: "tool", Content: "b-content", ToolCallID: "toolu_2", Name: "read_file"},
	})
	if err != nil {
		t.Fatalf("toAnthropicMessages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected user, assistant, grouped tool result messages, got %d: %#v", len(messages), messages)
	}
	if messages[2].Role != "user" {
		t.Fatalf("expected grouped tool results to be sent as user message, got %#v", messages[2])
	}
	blocks, ok := messages[2].Content.([]anthropicContentBlock)
	if !ok {
		t.Fatalf("tool result content type = %T", messages[2].Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected two tool_result blocks in one message, got %#v", blocks)
	}
	if blocks[0].ToolUseID != "toolu_1" || blocks[1].ToolUseID != "toolu_2" {
		t.Fatalf("unexpected grouped tool ids: %#v", blocks)
	}
}

func TestAnthropicChatWithOptionsSendsToolsAndParsesToolUse(t *testing.T) {
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "sk-ant-test" {
			t.Fatalf("x-api-key = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"stop_reason":"tool_use",
			"usage":{"input_tokens":12,"output_tokens":3},
			"content":[
				{"type":"text","text":"I'll check."},
				{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"Paris"}}
			]
		}`)
	}))
	defer server.Close()

	p := NewAnthropicProvider(Config{
		LlmProvider: LlmProvider{
			APIKey:  "sk-ant-test",
			BaseURL: server.URL,
			Model:   "claude-test",
		},
	}).(FunctionCallingProvider)

	resp, err := p.ChatWithOptions(context.Background(), []Message{
		{Role: "user", Content: "weather?"},
	}, CallOptions{
		Tools: []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "get_weather",
				"description": "Get weather",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
				},
			},
		}},
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "get_weather",
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatWithOptions: %v", err)
	}

	if len(captured.Tools) != 1 {
		t.Fatalf("expected one tool in request, got %#v", captured.Tools)
	}
	if captured.Tools[0].Name != "get_weather" || captured.Tools[0].InputSchema["type"] != "object" {
		t.Fatalf("unexpected tool schema: %#v", captured.Tools[0])
	}
	choice, ok := captured.ToolChoice.(map[string]any)
	if !ok || choice["type"] != "tool" || choice["name"] != "get_weather" {
		t.Fatalf("unexpected tool_choice: %#v", captured.ToolChoice)
	}

	if resp.Content != "I'll check." {
		t.Fatalf("content = %q", resp.Content)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", resp.FinishReason)
	}
	if resp.TokensUsed != 15 || resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Fatalf("unexpected usage: tokens=%d usage=%#v", resp.TokensUsed, resp.Usage)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].ID != "toolu_1" || resp.ToolCalls[0].Name != "get_weather" || resp.ToolCalls[0].Arguments != `{"city":"Paris"}` {
		t.Fatalf("unexpected tool call: %#v", resp.ToolCalls[0])
	}
}

func TestBuildAnthropicRequestOmitsToolsWhenToolChoiceNone(t *testing.T) {
	req, err := buildAnthropicRequest(Config{
		LlmProvider: LlmProvider{Model: "claude-test"},
	}, []Message{{Role: "user", Content: "hello"}}, CallOptions{
		Tools: []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":       "noop",
				"parameters": map[string]any{"type": "object"},
			},
		}},
		ToolChoice: "none",
	}, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if len(req.Tools) != 0 {
		t.Fatalf("expected tools to be omitted, got %#v", req.Tools)
	}
	if req.ToolChoice != nil {
		t.Fatalf("expected tool_choice to be omitted, got %#v", req.ToolChoice)
	}
}

func TestAnthropicChatWithOptionsMapsToolResultRequest(t *testing.T) {
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"id":"msg_2",
			"type":"message",
			"role":"assistant",
			"model":"claude-test",
			"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":2},
			"content":[{"type":"text","text":"It is raining."}]
		}`)
	}))
	defer server.Close()

	p := NewAnthropicProvider(Config{
		LlmProvider: LlmProvider{
			APIKey:  "sk-ant-test",
			BaseURL: server.URL,
			Model:   "claude-test",
		},
	}).(FunctionCallingProvider)

	_, err := p.ChatWithOptions(context.Background(), []Message{
		{Role: "user", Content: "weather?"},
		{Role: "assistant", ToolCalls: []ToolCall{
			{ID: "toolu_1", Name: "get_weather", Arguments: `{"city":"Paris"}`},
		}},
		{Role: "tool", Content: "rain", ToolCallID: "toolu_1", Name: "get_weather"},
	}, CallOptions{})
	if err != nil {
		t.Fatalf("ChatWithOptions: %v", err)
	}

	if len(captured.Messages) != 3 {
		t.Fatalf("expected 3 request messages, got %#v", captured.Messages)
	}
	toolResultBlocks, ok := captured.Messages[2].Content.([]any)
	if !ok {
		t.Fatalf("tool result content type = %T", captured.Messages[2].Content)
	}
	if len(toolResultBlocks) != 1 {
		t.Fatalf("unexpected tool result blocks: %#v", toolResultBlocks)
	}
	block, ok := toolResultBlocks[0].(map[string]any)
	if !ok {
		t.Fatalf("tool result block type = %T", toolResultBlocks[0])
	}
	if block["type"] != "tool_result" || block["tool_use_id"] != "toolu_1" || block["content"] != "rain" {
		t.Fatalf("unexpected tool result block: %#v", block)
	}
}

func TestAnthropicChatStreamWithOptionsParsesToolUseDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\"\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\":\\\"Paris\\\"}\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	p := NewAnthropicProvider(Config{
		LlmProvider: LlmProvider{
			APIKey:  "sk-ant-test",
			BaseURL: server.URL,
			Model:   "claude-test",
		},
	}).(FunctionCallingProvider)

	ch, err := p.ChatStreamWithOptions(context.Background(), []Message{{Role: "user", Content: "weather?"}}, CallOptions{
		Tools: []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name": "get_weather",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("ChatStreamWithOptions: %v", err)
	}

	var name string
	var id string
	var args strings.Builder
	var doneReason string
	for chunk := range ch {
		for _, delta := range chunk.ToolCallDeltas {
			if delta.ID != "" {
				id = delta.ID
			}
			if delta.Name != "" {
				name = delta.Name
			}
			args.WriteString(delta.Arguments)
		}
		if chunk.Done {
			doneReason = chunk.FinishReason
		}
	}

	if id != "toolu_1" || name != "get_weather" {
		t.Fatalf("unexpected streamed tool metadata: id=%q name=%q", id, name)
	}
	if args.String() != `{"city":"Paris"}` {
		t.Fatalf("args = %q", args.String())
	}
	if doneReason != "tool_calls" {
		t.Fatalf("done reason = %q", doneReason)
	}
}
