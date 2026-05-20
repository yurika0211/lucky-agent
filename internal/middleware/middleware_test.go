package middleware

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/middleware/cost"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/resilience"
)

// ---------------------------------------------------------------------------
// MW-1: Chain Tests
// ---------------------------------------------------------------------------

func TestChainEmpty(t *testing.T) {
	chain := NewChain()
	if chain.Len() != 0 {
		t.Errorf("expected 0 middlewares, got %d", chain.Len())
	}
}

func TestChainUse(t *testing.T) {
	chain := NewChain()
	chain.Use(&testMiddleware{name: "mw1"})
	chain.Use(&testMiddleware{name: "mw2"})
	if chain.Len() != 2 {
		t.Errorf("expected 2 middlewares, got %d", chain.Len())
	}
	names := chain.List()
	if len(names) != 2 || names[0] != "mw1" || names[1] != "mw2" {
		t.Errorf("expected [mw1 mw2], got %v", names)
	}
}

func TestChainExecutionOrder(t *testing.T) {
	var order []string
	chain := NewChain(
		&testMiddleware{
			name: "first",
			onChat: func(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
				order = append(order, "first-before")
				resp, err := next(ctx, info)
				order = append(order, "first-after")
				return resp, err
			},
		},
		&testMiddleware{
			name: "second",
			onChat: func(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
				order = append(order, "second-before")
				resp, err := next(ctx, info)
				order = append(order, "second-after")
				return resp, err
			},
		},
	)

	info := CallInfo{Provider: "test", StartTime: time.Now()}
	_, _ = chain.ExecuteChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		order = append(order, "handler")
		return &provider.Response{Content: "ok"}, nil
	})

	expected := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, order)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("at index %d: expected %s, got %s", i, v, order[i])
		}
	}
}

func TestChainShortCircuit(t *testing.T) {
	called := false
	chain := NewChain(
		&testMiddleware{
			name: "blocker",
			onChat: func(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
				return nil, fmt.Errorf("blocked")
			},
		},
	)

	info := CallInfo{Provider: "test", StartTime: time.Now()}
	_, err := chain.ExecuteChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		called = true
		return &provider.Response{Content: "ok"}, nil
	})

	if err == nil || err.Error() != "blocked" {
		t.Errorf("expected blocked error, got %v", err)
	}
	if called {
		t.Error("handler should not have been called")
	}
}

// ---------------------------------------------------------------------------
// MW-2: Built-in Middleware Tests
// ---------------------------------------------------------------------------

func TestLoggingMiddleware(t *testing.T) {
	logger := log.New(os.Stderr, "[test] ", 0)
	mw := NewLoggingMiddleware(logger)
	if mw.Name() != "logging" {
		t.Errorf("expected 'logging', got '%s'", mw.Name())
	}

	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}
	resp, err := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{Content: "ok", TokensUsed: 100}, nil
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", resp.Content)
	}
}

func TestLoggingMiddlewareError(t *testing.T) {
	logger := log.New(os.Stderr, "[test] ", 0)
	mw := NewLoggingMiddleware(logger)

	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}
	_, err := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return nil, fmt.Errorf("500 server error")
	})
	if err == nil {
		t.Error("expected error")
	}
}

func TestCostTrackingMiddleware(t *testing.T) {
	pt := cost.NewPriceTable()
	store := cost.NewCostStore(pt)
	mw := NewCostTrackingMiddleware(store, "sess-1")
	if mw.Name() != "cost-tracking" {
		t.Errorf("expected 'cost-tracking', got '%s'", mw.Name())
	}

	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}
	resp, err := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{Content: "hello world", TokensUsed: 50}, nil
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", resp.Content)
	}

	// Verify cost was recorded
	summary := store.Summary(cost.SummaryOptions{Period: "all"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 call recorded, got %d", summary.TotalCalls)
	}
}

func TestCostTrackingMiddlewareUsesProviderUsageDetails(t *testing.T) {
	pt := cost.NewPriceTable()
	store := cost.NewCostStore(pt)
	mw := NewCostTrackingMiddleware(store, "sess-usage")

	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}
	_, err := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{
			Content:    "hello world",
			TokensUsed: 1500,
			Usage: &provider.UsageDetails{
				PromptTokens:       1200,
				CompletionTokens:   300,
				TotalTokens:        1500,
				CachedPromptTokens: 800,
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	records := store.Recent(1)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].PromptTokens != 1200 {
		t.Fatalf("expected prompt tokens 1200, got %d", records[0].PromptTokens)
	}
	if records[0].CachedPromptTokens != 800 {
		t.Fatalf("expected cached prompt tokens 800, got %d", records[0].CachedPromptTokens)
	}
	if records[0].CompletionTokens != 300 {
		t.Fatalf("expected completion tokens 300, got %d", records[0].CompletionTokens)
	}
}

func TestRetryMiddleware(t *testing.T) {
	mw := NewRetryMiddleware(resilience.RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2,
		Jitter:        false,
	})
	if mw.Name() != "retry" {
		t.Errorf("expected 'retry', got '%s'", mw.Name())
	}

	callCount := int32(0)
	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}
	resp, err := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		count := atomic.AddInt32(&callCount, 1)
		if count < 3 {
			return nil, fmt.Errorf("502 bad gateway")
		}
		return &provider.Response{Content: "recovered"}, nil
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("expected 'recovered', got '%s'", resp.Content)
	}
}

func TestCircuitBreakerMiddleware(t *testing.T) {
	mw := NewCircuitBreakerMiddleware(resilience.CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 1,
		Timeout:          50 * time.Millisecond,
	})
	if mw.Name() != "circuit-breaker" {
		t.Errorf("expected 'circuit-breaker', got '%s'", mw.Name())
	}

	// Trip the breaker
	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}
	for i := 0; i < 2; i++ {
		_, _ = mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
			return nil, fmt.Errorf("500")
		})
	}

	// Should be rejected
	_, err := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{Content: "ok"}, nil
	})
	if err == nil {
		t.Error("expected circuit breaker rejection")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	mw := NewRateLimitMiddleware(2, 1*time.Second)
	if mw.Name() != "rate-limit" {
		t.Errorf("expected 'rate-limit', got '%s'", mw.Name())
	}

	info := CallInfo{Provider: "openai", Model: "gpt-5.4-mini", StartTime: time.Now()}

	// First 2 should succeed
	_, err1 := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{Content: "ok"}, nil
	})
	_, err2 := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{Content: "ok"}, nil
	})
	if err1 != nil || err2 != nil {
		t.Errorf("expected first 2 calls to succeed, got %v, %v", err1, err2)
	}

	// Third should be rate limited
	_, err3 := mw.InterceptChat(context.Background(), info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return &provider.Response{Content: "ok"}, nil
	})
	if err3 == nil {
		t.Error("expected rate limit error")
	}
}

// ---------------------------------------------------------------------------
// MW-3: MiddlewareProvider Tests
// ---------------------------------------------------------------------------

func TestMiddlewareProviderChat(t *testing.T) {
	inner := &mockProvider{}
	chain := NewChain()
	mp := NewMiddlewareProvider(inner, chain)

	resp, err := mp.Chat(context.Background(), []provider.Message{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", resp.Content)
	}
}

func TestMiddlewareProviderWithMiddlewares(t *testing.T) {
	inner := &mockProvider{}
	pt := cost.NewPriceTable()
	store := cost.NewCostStore(pt)

	chain := NewChain(
		NewLoggingMiddleware(nil),
		NewCostTrackingMiddleware(store, "sess-1"),
	)
	mp := NewMiddlewareProvider(inner, chain)

	resp, err := mp.Chat(context.Background(), []provider.Message{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", resp.Content)
	}

	// Verify cost was recorded
	summary := store.Summary(cost.SummaryOptions{Period: "all"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 cost record, got %d", summary.TotalCalls)
	}
}

func TestMiddlewareProviderStream(t *testing.T) {
	inner := &mockProvider{}
	chain := NewChain()
	mp := NewMiddlewareProvider(inner, chain)

	ch, err := mp.ChatStream(context.Background(), []provider.Message{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	chunk := <-ch
	if chunk.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", chunk.Content)
	}
}

func TestMiddlewareProviderName(t *testing.T) {
	inner := &mockProvider{}
	mp := NewMiddlewareProvider(inner, NewChain())
	if mp.Name() != "mock" {
		t.Errorf("expected 'mock', got '%s'", mp.Name())
	}
}

func TestMiddlewareProviderValidate(t *testing.T) {
	inner := &mockProvider{}
	mp := NewMiddlewareProvider(inner, NewChain())
	if err := mp.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestMiddlewareProviderChainAccess(t *testing.T) {
	inner := &mockProvider{}
	chain := NewChain(NewLoggingMiddleware(nil))
	mp := NewMiddlewareProvider(inner, chain)
	if mp.Chain() != chain {
		t.Error("expected chain to be accessible")
	}
	if mp.Chain().Len() != 1 {
		t.Errorf("expected 1 middleware, got %d", mp.Chain().Len())
	}
}

func TestMiddlewareProviderNilChain(t *testing.T) {
	inner := &mockProvider{}
	mp := NewMiddlewareProvider(inner, nil)
	if mp.Chain() == nil {
		t.Error("expected non-nil chain")
	}
}

func TestMiddlewareProviderFullStack(t *testing.T) {
	inner := &mockProvider{}
	pt := cost.NewPriceTable()
	store := cost.NewCostStore(pt)

	chain := NewChain(
		NewLoggingMiddleware(nil),
		NewCostTrackingMiddleware(store, "sess-full"),
		NewRetryMiddleware(resilience.RetryConfig{
			MaxAttempts:   2,
			InitialDelay:  10 * time.Millisecond,
			BackoffFactor: 2,
			Jitter:        false,
		}),
		NewRateLimitMiddleware(100, 1*time.Minute),
	)
	mp := NewMiddlewareProvider(inner, chain)

	resp, err := mp.Chat(context.Background(), []provider.Message{
		{Role: "user", Content: "test full stack"},
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", resp.Content)
	}

	summary := store.Summary(cost.SummaryOptions{Period: "all"})
	if summary.TotalCalls != 1 {
		t.Errorf("expected 1 cost record, got %d", summary.TotalCalls)
	}
}

func TestMiddlewareProviderImplementsFunctionCallingProvider(t *testing.T) {
	mp := NewMiddlewareProvider(&mockFCProvider{}, NewChain())
	if _, ok := any(mp).(provider.FunctionCallingProvider); !ok {
		t.Fatal("expected middleware provider to implement FunctionCallingProvider")
	}
}

func TestMiddlewareProviderChatWithOptionsPassthrough(t *testing.T) {
	inner := &mockFCProvider{}
	mp := NewMiddlewareProvider(inner, NewChain())

	opts := provider.CallOptions{
		Tools: []map[string]any{
			{"function": map[string]any{"name": "file_read"}},
		},
		ToolChoice: "auto",
	}
	resp, err := mp.ChatWithOptions(context.Background(), []provider.Message{{Role: "user", Content: "hello"}}, opts)
	if err != nil {
		t.Fatalf("ChatWithOptions: %v", err)
	}
	if resp == nil || len(resp.ToolCalls) != 1 {
		t.Fatalf("expected tool call response, got %#v", resp)
	}
	if len(inner.lastOpts.Tools) != 1 {
		t.Fatalf("expected options passed through, got %#v", inner.lastOpts)
	}
}

func TestMiddlewareProviderChatStreamWithOptionsPassthrough(t *testing.T) {
	inner := &mockFCProvider{}
	mp := NewMiddlewareProvider(inner, NewChain())

	opts := provider.CallOptions{
		Tools: []map[string]any{
			{"function": map[string]any{"name": "file_read"}},
		},
		ToolChoice: "auto",
	}
	ch, err := mp.ChatStreamWithOptions(context.Background(), []provider.Message{{Role: "user", Content: "hello"}}, opts)
	if err != nil {
		t.Fatalf("ChatStreamWithOptions: %v", err)
	}
	var sawToolDelta bool
	for chunk := range ch {
		if len(chunk.ToolCallDeltas) > 0 {
			sawToolDelta = true
		}
	}
	if !sawToolDelta {
		t.Fatal("expected tool call deltas in stream")
	}
	if len(inner.lastOpts.Tools) != 1 {
		t.Fatalf("expected options passed through, got %#v", inner.lastOpts)
	}
}

// ---------------------------------------------------------------------------
// Test Helpers
// ---------------------------------------------------------------------------

type testMiddleware struct {
	name     string
	onChat   func(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error)
	onStream func(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error)
}

func (m *testMiddleware) Name() string { return m.name }

func (m *testMiddleware) InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
	if m.onChat != nil {
		return m.onChat(ctx, info, next)
	}
	return next(ctx, info)
}

func (m *testMiddleware) InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error) {
	if m.onStream != nil {
		return m.onStream(ctx, info, next)
	}
	return next(ctx, info)
}

type mockProvider struct{}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return &provider.Response{Content: "ok", TokensUsed: 10}, nil
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) Validate() error { return nil }

type mockFCProvider struct {
	lastOpts provider.CallOptions
}

func (m *mockFCProvider) Name() string { return "mock-fc" }
func (m *mockFCProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return &provider.Response{Content: "plain", TokensUsed: 10}, nil
}

func (m *mockFCProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "plain", Done: true}
	close(ch)
	return ch, nil
}

func (m *mockFCProvider) ChatWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	m.lastOpts = opts
	return &provider.Response{
		Content: "fc",
		ToolCalls: []provider.ToolCall{
			{ID: "call_1", Name: "file_read", Arguments: `{"path":"README.md"}`},
		},
		TokensUsed: 11,
	}, nil
}

func (m *mockFCProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	m.lastOpts = opts
	ch := make(chan provider.StreamChunk, 2)
	ch <- provider.StreamChunk{
		ToolCallDeltas: []provider.StreamToolCallDelta{
			{Index: 0, ID: "call_1", Name: "file_read", Arguments: `{"path":"README.md"}`},
		},
	}
	ch <- provider.StreamChunk{Done: true, FinishReason: "tool_calls"}
	close(ch)
	return ch, nil
}
func (m *mockFCProvider) Validate() error { return nil }
