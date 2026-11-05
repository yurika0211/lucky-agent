package provider

import (
	"testing"
)

func TestStreamParserContentOnly(t *testing.T) {
	sp := NewStreamParser()

	sp.FeedDelta(&openaiDelta{Content: "Hello"})
	sp.FeedDelta(&openaiDelta{Content: " world"})
	sp.FeedDelta(&openaiDelta{Content: "!"})

	if sp.GetContent() != "Hello world!" {
		t.Errorf("expected 'Hello world!', got '%s'", sp.GetContent())
	}
	if sp.HasToolCalls() {
		t.Error("should not have tool calls")
	}
}

func TestStreamParserToolCalls(t *testing.T) {
	sp := NewStreamParser()

	// 第一个 delta: 工具调用开始
	sp.FeedDelta(&openaiDelta{
		ToolCalls: []deltaToolCall{
			{
				Index: 0,
				ID:    "call_abc123",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{
					Name: "web_search",
				},
			},
		},
	})

	// 第二个 delta: 参数片段
	sp.FeedDelta(&openaiDelta{
		ToolCalls: []deltaToolCall{
			{
				Index: 0,
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{
					Arguments: `{"qu`,
				},
			},
		},
	})

	// 第三个 delta: 参数片段续
	sp.FeedDelta(&openaiDelta{
		ToolCalls: []deltaToolCall{
			{
				Index: 0,
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{
					Arguments: `ery": "Go"}`,
				},
			},
		},
	})

	if !sp.HasToolCalls() {
		t.Error("expected tool calls")
	}

	calls := sp.GetToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}

	if calls[0].ID != "call_abc123" {
		t.Errorf("expected ID 'call_abc123', got '%s'", calls[0].ID)
	}
	if calls[0].Name != "web_search" {
		t.Errorf("expected name 'web_search', got '%s'", calls[0].Name)
	}
	expectedArgs := `{"query": "Go"}`
	if calls[0].Arguments != expectedArgs {
		t.Errorf("expected args '%s', got '%s'", expectedArgs, calls[0].Arguments)
	}
}

func TestStreamParserMultipleToolCalls(t *testing.T) {
	sp := NewStreamParser()

	// 两个工具调用
	sp.FeedDelta(&openaiDelta{
		ToolCalls: []deltaToolCall{
			{
				Index: 0,
				ID:    "call_1",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "web_search"},
			},
			{
				Index: 1,
				ID:    "call_2",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "file_read"},
			},
		},
	})

	calls := sp.GetToolCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Name != "web_search" {
		t.Errorf("expected web_search, got %s", calls[0].Name)
	}
	if calls[1].Name != "file_read" {
		t.Errorf("expected file_read, got %s", calls[1].Name)
	}
}

func TestStreamParserMixedContentAndToolCalls(t *testing.T) {
	sp := NewStreamParser()

	// 先有文本内容
	sp.FeedDelta(&openaiDelta{Content: "Let me search for that."})

	// 然后工具调用
	sp.FeedDelta(&openaiDelta{
		ToolCalls: []deltaToolCall{
			{
				Index: 0,
				ID:    "call_mixed",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "shell", Arguments: `{"command": "ls"}`},
			},
		},
	})

	if sp.GetContent() != "Let me search for that." {
		t.Errorf("expected content, got '%s'", sp.GetContent())
	}
	if !sp.HasToolCalls() {
		t.Error("expected tool calls")
	}
}

func TestStreamParserBuildResponse(t *testing.T) {
	sp := NewStreamParser()

	sp.FeedDelta(&openaiDelta{Content: "Hello"})
	sp.FeedDelta(&openaiDelta{Content: "!"})
	sp.done = true

	resp := sp.BuildResponse()
	if resp.Content != "Hello!" {
		t.Errorf("expected 'Hello!', got '%s'", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("expected 'stop', got '%s'", resp.FinishReason)
	}
}

func TestStreamParserBuildResponseWithToolCalls(t *testing.T) {
	sp := NewStreamParser()

	sp.FeedDelta(&openaiDelta{
		ToolCalls: []deltaToolCall{
			{
				Index: 0,
				ID:    "call_test",
				Type:  "function",
				Function: struct {
					Name      string `json:"name,omitempty"`
					Arguments string `json:"arguments,omitempty"`
				}{Name: "current_time", Arguments: `{}`},
			},
		},
	})

	resp := sp.BuildResponse()
	if resp.FinishReason != "tool_calls" {
		t.Errorf("expected 'tool_calls', got '%s'", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
}

func TestStreamParserFeedChunk(t *testing.T) {
	sp := NewStreamParser()

	// 正常 chunk
	if !sp.Feed(StreamChunk{Content: "hello", Model: "gpt-5.4-mini"}) {
		t.Error("expected Feed to return true")
	}

	// Done chunk
	if sp.Feed(StreamChunk{Done: true, Model: "gpt-5.4-mini"}) {
		t.Error("expected Feed to return false for done")
	}

	if !sp.IsDone() {
		t.Error("expected IsDone")
	}
	if sp.GetModel() != "gpt-5.4-mini" {
		t.Errorf("expected gpt-5.4-mini, got %s", sp.GetModel())
	}
}
