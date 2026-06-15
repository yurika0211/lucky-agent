package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestShouldRetryTransportErrorBadRecordMAC(t *testing.T) {
	cfg := Config{}
	err := fmt.Errorf("local error: tls: bad record MAC")
	if !shouldRetryTransportError(err, cfg) {
		t.Fatal("expected bad record MAC to be retryable")
	}
}

func TestDoOpenAIRequestRetriesOnTransportError(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	attempts := 0
	closeFlags := make([]bool, 0, 2)
	userAgents := make([]string, 0, 2)
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			attempts++
			closeFlags = append(closeFlags, req.Close)
			userAgents = append(userAgents, req.Header.Get("User-Agent"))
			if attempts == 1 {
				return nil, fmt.Errorf("local error: tls: bad record MAC")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.boaiak.com/v1",
			APIKey:  "sk-test",
		},
		Retry: RetryConfig{
			Enabled:        true,
			MaxAttempts:    2,
			InitialDelayMs: 1,
			MaxDelayMs:     1,
		},
	}

	resp, err := doOpenAIRequest(context.Background(), cfg, []byte(`{"model":"x"}`))
	if err != nil {
		t.Fatalf("doOpenAIRequest returned error: %v", err)
	}
	defer resp.Body.Close()

	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
	if len(closeFlags) < 2 {
		t.Fatalf("expected 2 close flags, got %d", len(closeFlags))
	}
	if closeFlags[0] {
		t.Fatal("first attempt should not force close connection")
	}
	if !closeFlags[1] {
		t.Fatal("retry attempt should force close connection")
	}
	if len(userAgents) != 2 {
		t.Fatalf("expected 2 user agents, got %d", len(userAgents))
	}
	for i, ua := range userAgents {
		if ua != defaultOpenAIUserAgent {
			t.Fatalf("attempt %d user agent = %q, want %q", i+1, ua, defaultOpenAIUserAgent)
		}
	}
}

func TestDoOpenAIRequestAllowsUserAgentOverride(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	var gotUserAgent string
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			gotUserAgent = req.Header.Get("User-Agent")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
		},
		ExtraHeaders: map[string]string{
			"User-Agent": "custom-agent",
		},
	}

	resp, err := doOpenAIRequest(context.Background(), cfg, []byte(`{"model":"x"}`))
	if err != nil {
		t.Fatalf("doOpenAIRequest returned error: %v", err)
	}
	defer resp.Body.Close()

	if gotUserAgent != "custom-agent" {
		t.Fatalf("user agent = %q, want %q", gotUserAgent, "custom-agent")
	}
}

func TestNewOpenAITransportKeepsHTTP2Enabled(t *testing.T) {
	rt := newOpenAITransport()
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", rt)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Fatal("expected ForceAttemptHTTP2 to be enabled")
	}
}

func TestShouldPreferStreamFirst(t *testing.T) {
	if !shouldPreferStreamFirst("gpt-5.4-mini") {
		t.Fatal("expected gpt-5.4-mini to prefer stream-first")
	}
	if shouldPreferStreamFirst("gpt-4-turbo") {
		t.Fatal("did not expect gpt-4-turbo to prefer stream-first")
	}
}

func TestCallOpenAIUsesStreamFirstForMiniModel(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	streamCalls := 0
	nonStreamCalls := 0
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			body := string(bodyBytes)

			if strings.Contains(body, `"stream":true`) {
				streamCalls++
				sse := strings.Join([]string{
					`data: {"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":""}]}`,
					`data: [DONE]`,
					"",
				}, "\n")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(sse)),
					Request:    req,
				}, nil
			}

			nonStreamCalls++
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"fallback"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "gpt-5.4-mini",
		},
	}

	resp, err := callOpenAI(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAI returned error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Content != "hello" {
		t.Fatalf("expected streamed content 'hello', got %q", resp.Content)
	}
	if streamCalls != 1 {
		t.Fatalf("expected exactly 1 stream call, got %d", streamCalls)
	}
	if nonStreamCalls != 0 {
		t.Fatalf("expected non-stream not called, got %d", nonStreamCalls)
	}
}

func TestCallOpenAIParsesCachedUsage(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
					"choices":[{"index":0,"message":{"role":"assistant","content":"ok","reasoning_content":"step-by-step"},"finish_reason":"stop"}],
					"usage":{
						"prompt_tokens":1200,
						"completion_tokens":300,
						"total_tokens":1500,
						"input_tokens":1200,
						"output_tokens":300,
						"prompt_tokens_details":{"cached_tokens":800},
						"claude_cache_creation_5_m_tokens":100,
						"claude_cache_creation_1_h_tokens":50
					}
				}`)),
				Request: req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "gpt-5.4-mini",
		},
	}

	resp, err := callOpenAI(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAI returned error: %v", err)
	}
	if resp.Usage == nil {
		t.Fatal("expected usage details")
	}
	if resp.Usage.CachedPromptTokens != 800 {
		t.Fatalf("expected cached prompt tokens 800, got %d", resp.Usage.CachedPromptTokens)
	}
	if resp.Usage.CacheCreation5MTokens != 100 {
		t.Fatalf("expected cache creation 5m tokens 100, got %d", resp.Usage.CacheCreation5MTokens)
	}
	if resp.Usage.CacheCreation1HTokens != 50 {
		t.Fatalf("expected cache creation 1h tokens 50, got %d", resp.Usage.CacheCreation1HTokens)
	}
	if resp.ReasoningContent != "step-by-step" {
		t.Fatalf("expected reasoning content to be parsed, got %q", resp.ReasoningContent)
	}
}

func TestCallOpenAIStreamEmitsUsageChunk(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			sse := strings.Join([]string{
				`data: {"choices":[{"index":0,"delta":{"reasoning_content":"first-reasoning"},"finish_reason":""}]}`,
				`data: {"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":""}]}`,
				`data: {"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":100,"total_tokens":1000,"prompt_tokens_details":{"cached_tokens":600}}}`,
				`data: [DONE]`,
				"",
			}, "\n")
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(sse)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "gpt-5.4-mini",
		},
	}

	ch, err := callOpenAIStream(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAIStream returned error: %v", err)
	}

	var sawUsage bool
	var sawReasoning bool
	for chunk := range ch {
		if chunk.ReasoningContent != "" {
			sawReasoning = true
			if chunk.ReasoningContent != "first-reasoning" {
				t.Fatalf("expected reasoning content first-reasoning, got %q", chunk.ReasoningContent)
			}
		}
		if chunk.Usage != nil {
			sawUsage = true
			if chunk.Usage.CachedPromptTokens != 600 {
				t.Fatalf("expected cached prompt tokens 600, got %d", chunk.Usage.CachedPromptTokens)
			}
		}
	}
	if !sawUsage {
		t.Fatal("expected a usage chunk")
	}
	if !sawReasoning {
		t.Fatal("expected a reasoning chunk")
	}
}

func TestCallOpenAIOmitsToolChoiceForDeepseekModels(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	var capturedBody string
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			capturedBody = string(bodyBytes)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "deepseek-reasoner",
		},
	}

	opts := CallOptions{
		Tools: []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "ping",
					"description": "test",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
						"required":   []string{},
					},
				},
			},
		},
		ToolChoice: "auto",
	}

	_, err := callOpenAI(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, opts)
	if err != nil {
		t.Fatalf("callOpenAI returned error: %v", err)
	}
	if !strings.Contains(capturedBody, `"tools"`) {
		t.Fatalf("expected tools in request body, got %s", capturedBody)
	}
	if strings.Contains(capturedBody, `"tool_choice"`) {
		t.Fatalf("expected tool_choice to be omitted for deepseek, got %s", capturedBody)
	}
}

func TestCallOpenAIKeepsToolChoiceForOtherModels(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	var capturedBody string
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			capturedBody = string(bodyBytes)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "gpt-5.4-mini",
		},
	}

	opts := CallOptions{
		Tools: []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "ping",
					"description": "test",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{},
						"required":   []string{},
					},
				},
			},
		},
		ToolChoice: "auto",
	}

	_, err := callOpenAI(context.Background(), cfg, []Message{{Role: "user", Content: "hi"}}, opts)
	if err != nil {
		t.Fatalf("callOpenAI returned error: %v", err)
	}
	if !strings.Contains(capturedBody, `"tool_choice":"auto"`) {
		t.Fatalf("expected tool_choice to be preserved, got %s", capturedBody)
	}
}

func TestCallOpenAIIncludesReasoningContentForDeepseekMessages(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	var capturedBody string
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			capturedBody = string(bodyBytes)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "deepseek-reasoner",
		},
	}

	_, err := callOpenAI(context.Background(), cfg, []Message{
		{Role: "assistant", Content: "", ReasoningContent: "preserve-me", ToolCalls: []ToolCall{{ID: "call_1", Name: "ping", Arguments: `{}`}}},
		{Role: "tool", Content: "ok", ToolCallID: "call_1", Name: "ping"},
	}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAI returned error: %v", err)
	}
	if !strings.Contains(capturedBody, `"reasoning_content":"preserve-me"`) {
		t.Fatalf("expected reasoning_content to be included, got %s", capturedBody)
	}
}

func TestCallOpenAIRetriesReasoningContentErrorWithBackfilledAssistantHistory(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	var bodies []string
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			bodies = append(bodies, string(bodyBytes))
			if len(bodies) == 1 {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API."}}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
				Request:    req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "deepseek-reasoner",
		},
	}

	resp, err := callOpenAI(context.Background(), cfg, []Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old answer without reasoning"},
		{Role: "user", Content: "new question"},
	}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAI returned error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("expected retry response ok, got %q", resp.Content)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected two requests, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "old answer without reasoning") {
		t.Fatalf("expected first request to include old assistant history, got %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "old answer without reasoning") {
		t.Fatalf("expected retry request to preserve old assistant history, got %s", bodies[1])
	}
	if !strings.Contains(bodies[1], `"reasoning_content":"`+missingReasoningContentPlaceholder+`"`) {
		t.Fatalf("expected retry request to backfill reasoning_content, got %s", bodies[1])
	}
	if !strings.Contains(bodies[1], "new question") {
		t.Fatalf("expected retry request to keep current user message, got %s", bodies[1])
	}
}

func TestCallOpenAIStreamRetriesReasoningContentErrorWithBackfilledAssistantHistory(t *testing.T) {
	orig := openAIHTTPClient
	t.Cleanup(func() {
		openAIHTTPClient = orig
	})

	var bodies []string
	openAIHTTPClient = &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			bodyBytes, _ := io.ReadAll(req.Body)
			bodies = append(bodies, string(bodyBytes))
			if len(bodies) == 1 {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"The reasoning_content in the thinking mode must be passed back to the API."}}`)),
					Request:    req,
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"\"}]}\n\n" +
						"data: [DONE]\n\n",
				)),
				Request: req,
			}, nil
		}),
	}

	cfg := Config{
		LlmProvider: LlmProvider{
			BaseURL: "https://api.openai.com/v1",
			APIKey:  "sk-test",
			Model:   "deepseek-reasoner",
		},
	}

	ch, err := callOpenAIStream(context.Background(), cfg, []Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", Content: "old stream answer without reasoning"},
		{Role: "user", Content: "new question"},
	}, CallOptions{})
	if err != nil {
		t.Fatalf("callOpenAIStream returned error: %v", err)
	}
	var got strings.Builder
	for chunk := range ch {
		got.WriteString(chunk.Content)
	}
	if got.String() != "ok" {
		t.Fatalf("expected retry stream content ok, got %q", got.String())
	}
	if len(bodies) != 2 {
		t.Fatalf("expected two requests, got %d", len(bodies))
	}
	if !strings.Contains(bodies[0], "old stream answer without reasoning") {
		t.Fatalf("expected first request to include old assistant history, got %s", bodies[0])
	}
	if !strings.Contains(bodies[1], "old stream answer without reasoning") {
		t.Fatalf("expected retry request to preserve old assistant history, got %s", bodies[1])
	}
	if !strings.Contains(bodies[1], `"reasoning_content":"`+missingReasoningContentPlaceholder+`"`) {
		t.Fatalf("expected retry request to backfill reasoning_content, got %s", bodies[1])
	}
	if !strings.Contains(bodies[1], "new question") {
		t.Fatalf("expected retry request to keep current user message, got %s", bodies[1])
	}
}
