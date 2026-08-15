package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCallOpenAIResponsesConvertsHistoryToolsAndUsage(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() { openAIHTTPClient = orig })

	var request map[string]any
	openAIHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"status":"completed",
				"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}],
				"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":3},"output_tokens":2,"total_tokens":12}
			}`)),
			Request: req,
		}, nil
	})}

	cfg := Config{LlmProvider: LlmProvider{
		BaseURL:  "https://api.openai.com/v1",
		APIKey:   "sk-test",
		Model:    "gpt-5.6-terra",
		Protocol: "responses",
	}, Limits: LimitsConfig{MaxTokens: 256}}
	opts := CallOptions{
		Tools: []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "lookup",
				"description": "Look up a value",
				"parameters":  map[string]any{"type": "object"},
			},
		}},
		ToolChoice: map[string]any{"type": "function", "function": map[string]any{"name": "lookup"}},
	}

	result, err := callOpenAI(context.Background(), cfg, []Message{
		{Role: "system", Content: "Use the lookup tool."},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_lookup", Name: "lookup", Arguments: `{"key":"one"}`}}},
		{Role: "tool", ToolCallID: "call_lookup", Name: "lookup", Content: `{"value":"1"}`},
		{Role: "user", Content: "What did it return?"},
	}, opts)
	if err != nil {
		t.Fatalf("callOpenAI: %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q, want done", result.Content)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 10 || result.Usage.CachedPromptTokens != 3 || result.TokensUsed != 12 {
		t.Fatalf("usage = %#v, tokens = %d", result.Usage, result.TokensUsed)
	}

	if request["stream"] != false {
		t.Fatalf("stream = %#v, want false", request["stream"])
	}
	if request["max_output_tokens"] != float64(256) {
		t.Fatalf("max_output_tokens = %#v, want 256", request["max_output_tokens"])
	}
	input, ok := request["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("input = %#v, want four items", request["input"])
	}
	functionCall, ok := input[1].(map[string]any)
	if !ok || functionCall["type"] != "function_call" || functionCall["call_id"] != "call_lookup" {
		t.Fatalf("function call input = %#v", input[1])
	}
	functionOutput, ok := input[2].(map[string]any)
	if !ok || functionOutput["type"] != "function_call_output" || functionOutput["call_id"] != "call_lookup" || functionOutput["output"] != `{"value":"1"}` {
		t.Fatalf("function output input = %#v", input[2])
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", request["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != "function" || tool["name"] != "lookup" {
		t.Fatalf("responses tool = %#v", tools[0])
	}
	if _, hasNestedFunction := tool["function"]; hasNestedFunction {
		t.Fatalf("responses tool must not nest a function field: %#v", tool)
	}
	choice, ok := request["tool_choice"].(map[string]any)
	if !ok || choice["type"] != "function" || choice["name"] != "lookup" {
		t.Fatalf("responses tool_choice = %#v", request["tool_choice"])
	}
}

func TestCallOpenAIResponsesStreamParsesToolCallEvents(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() { openAIHTTPClient = orig })

	openAIHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Fatalf("expected stream request, got %s", body)
		}
		sse := strings.Join([]string{
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"key\":"}`,
			`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"one\"}"}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup","arguments":"{\"key\":\"one\"}"}}`,
			`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":9,"input_tokens_details":{"cached_tokens":4},"output_tokens":1,"total_tokens":10}}}`,
			"",
		}, "\n")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(sse)),
			Request:    req,
		}, nil
	})}

	cfg := Config{LlmProvider: LlmProvider{
		BaseURL:  "https://api.openai.com/v1",
		APIKey:   "sk-test",
		Model:    "gpt-5.6-terra",
		Protocol: "responses",
	}}
	ch, err := callOpenAIStream(context.Background(), cfg, []Message{{Role: "user", Content: "look this up"}}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAIStream: %v", err)
	}

	var name, id, arguments string
	var usage *UsageDetails
	finishReason := ""
	for chunk := range ch {
		for _, delta := range chunk.ToolCallDeltas {
			if delta.ID != "" {
				id = delta.ID
			}
			if delta.Name != "" {
				name = delta.Name
			}
			arguments += delta.Arguments
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if chunk.Done {
			finishReason = chunk.FinishReason
		}
	}
	if id != "call_1" || name != "lookup" || arguments != `{"key":"one"}` {
		t.Fatalf("tool call = id=%q name=%q arguments=%q", id, name, arguments)
	}
	if finishReason != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls", finishReason)
	}
	if usage == nil || usage.PromptTokens != 9 || usage.CachedPromptTokens != 4 || usage.TotalTokens != 10 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestCallOpenAIResponsesStreamKeepsCompletedOutputIndexes(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() { openAIHTTPClient = orig })

	openAIHTTPClient = &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		sse := strings.Join([]string{
			`data: {"type":"response.output_text.delta","output_index":0,"delta":"Searching"}`,
			`data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Searching"}]},{"type":"function_call","id":"fc_2","call_id":"call_2","name":"lookup","arguments":"{\"key\":\"two\"}"}]}}`,
			"",
		}, "\n")
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(sse)), Request: req}, nil
	})}

	cfg := Config{LlmProvider: LlmProvider{BaseURL: "https://api.openai.com/v1", APIKey: "sk-test", Model: "gpt-5.6-terra", Protocol: "responses"}}
	ch, err := callOpenAIStream(context.Background(), cfg, []Message{{Role: "user", Content: "look this up"}}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAIStream: %v", err)
	}

	var content string
	var delta *StreamToolCallDelta
	for chunk := range ch {
		content += chunk.Content
		if len(chunk.ToolCallDeltas) > 0 {
			copy := chunk.ToolCallDeltas[0]
			delta = &copy
		}
	}
	if content != "Searching" {
		t.Fatalf("content = %q, want Searching", content)
	}
	if delta == nil || delta.Index != 1 || delta.ID != "call_2" || delta.Name != "lookup" || delta.Arguments != `{"key":"two"}` {
		t.Fatalf("tool delta = %#v", delta)
	}
}

func TestToResponsesInputConvertsImageParts(t *testing.T) {
	input, err := toResponsesInput([]Message{{
		Role: "user",
		ContentParts: []ContentPart{
			{Type: "text", Text: "Describe this"},
			{Type: "image", Image: &ImagePart{URL: "https://example.test/image.png", Detail: "high"}},
		},
	}})
	if err != nil {
		t.Fatalf("toResponsesInput: %v", err)
	}
	message, ok := input[0].(map[string]any)
	if !ok {
		t.Fatalf("input message = %T", input[0])
	}
	content, ok := message["content"].([]map[string]any)
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v", message["content"])
	}
	if content[0]["type"] != "input_text" || content[1]["type"] != "input_image" || content[1]["image_url"] != "https://example.test/image.png" {
		t.Fatalf("responses content = %#v", content)
	}
}
