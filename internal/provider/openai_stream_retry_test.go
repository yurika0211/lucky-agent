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
	if shouldPreferStreamFirst("gpt-5.4-mini") {
		t.Fatal("did not expect gpt-5.4-mini to prefer stream-first")
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
