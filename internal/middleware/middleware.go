// Package middleware provides a composable middleware chain for Provider calls.
// Middleware can intercept, modify, or short-circuit Chat/ChatStream calls,
// enabling cross-cutting concerns like logging, cost tracking, retry, and rate limiting.
package middleware

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/middleware/cost"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/resilience"
)

// ---------------------------------------------------------------------------
// MW-1: Middleware Interface + Chain
// ---------------------------------------------------------------------------

// CallInfo contains information about the current call.
type CallInfo struct {
	Provider  string
	Model     string
	SessionID string
	Messages  []provider.Message
	StartTime time.Time
}

// CallResult contains the result of a call.
type CallResult struct {
	Response *provider.Response
	Stream   <-chan provider.StreamChunk
	Error    error
	Duration time.Duration
}

// Middleware intercepts a Provider call.
// The Handler function calls the next middleware in the chain (or the actual Provider).
type Middleware interface {
	// Name returns the middleware name for identification.
	Name() string

	// InterceptChat intercepts a Chat call.
	InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error)

	// InterceptChatStream intercepts a ChatStream call.
	InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error)
}

// ChatHandler is the function signature for the next Chat handler in the chain.
type ChatHandler func(ctx context.Context, info CallInfo) (*provider.Response, error)

// StreamHandler is the function signature for the next Stream handler in the chain.
type StreamHandler func(ctx context.Context, info CallInfo) (<-chan provider.StreamChunk, error)

// Chain executes middlewares in order.
type Chain struct {
	middlewares []Middleware
}

// NewChain creates a new middleware chain.
func NewChain(middlewares ...Middleware) *Chain {
	return &Chain{middlewares: middlewares}
}

// Use adds middleware(s) to the chain.
func (c *Chain) Use(mw ...Middleware) {
	c.middlewares = append(c.middlewares, mw...)
}

// ExecuteChat runs the middleware chain for a Chat call.
func (c *Chain) ExecuteChat(ctx context.Context, info CallInfo, final ChatHandler) (*provider.Response, error) {
	// Build the handler chain from the end
	handler := final
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		mw := c.middlewares[i]
		prev := handler
		handler = func(ctx context.Context, info CallInfo) (*provider.Response, error) {
			return mw.InterceptChat(ctx, info, prev)
		}
	}
	return handler(ctx, info)
}

// ExecuteChatStream runs the middleware chain for a ChatStream call.
func (c *Chain) ExecuteChatStream(ctx context.Context, info CallInfo, final StreamHandler) (<-chan provider.StreamChunk, error) {
	handler := final
	for i := len(c.middlewares) - 1; i >= 0; i-- {
		mw := c.middlewares[i]
		prev := handler
		handler = func(ctx context.Context, info CallInfo) (<-chan provider.StreamChunk, error) {
			return mw.InterceptChatStream(ctx, info, prev)
		}
	}
	return handler(ctx, info)
}

// List returns the names of all middlewares in the chain.
func (c *Chain) List() []string {
	names := make([]string, len(c.middlewares))
	for i, mw := range c.middlewares {
		names[i] = mw.Name()
	}
	return names
}

// Len returns the number of middlewares.
func (c *Chain) Len() int {
	return len(c.middlewares)
}

// ---------------------------------------------------------------------------
// MW-2: Built-in Middlewares
// ---------------------------------------------------------------------------

// --- LoggingMiddleware ---

// LoggingMiddleware logs each Provider call with timing.
type LoggingMiddleware struct {
	logger *log.Logger
}

// NewLoggingMiddleware creates a logging middleware.
func NewLoggingMiddleware(logger *log.Logger) *LoggingMiddleware {
	if logger == nil {
		logger = log.Default()
	}
	return &LoggingMiddleware{logger: logger}
}

func (m *LoggingMiddleware) Name() string { return "logging" }

func (m *LoggingMiddleware) InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
	m.logger.Printf("[middleware:logging] → %s/%s chat start (%d messages)", info.Provider, info.Model, len(info.Messages))
	resp, err := next(ctx, info)
	duration := time.Since(info.StartTime)
	if err != nil {
		m.logger.Printf("[middleware:logging] ← %s/%s chat error: %v (%v)", info.Provider, info.Model, err, duration)
	} else {
		m.logger.Printf("[middleware:logging] ← %s/%s chat ok: %d tokens (%v)", info.Provider, info.Model, resp.TokensUsed, duration)
	}
	return resp, err
}

func (m *LoggingMiddleware) InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error) {
	m.logger.Printf("[middleware:logging] → %s/%s stream start (%d messages)", info.Provider, info.Model, len(info.Messages))
	ch, err := next(ctx, info)
	if err != nil {
		m.logger.Printf("[middleware:logging] ← %s/%s stream error: %v", info.Provider, info.Model, err)
	}
	return ch, err
}

// --- CostTrackingMiddleware ---

// CostTrackingMiddleware records API call costs.
type CostTrackingMiddleware struct {
	store     *cost.CostStore
	idGen     func() string
	sessionID string
}

// NewCostTrackingMiddleware creates a cost tracking middleware.
func NewCostTrackingMiddleware(store *cost.CostStore, sessionID string) *CostTrackingMiddleware {
	return &CostTrackingMiddleware{
		store:     store,
		idGen:     func() string { return fmt.Sprintf("call-%d", time.Now().UnixNano()) },
		sessionID: sessionID,
	}
}

func (m *CostTrackingMiddleware) Name() string { return "cost-tracking" }

func (m *CostTrackingMiddleware) InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
	resp, err := next(ctx, info)
	if err != nil {
		return resp, err
	}

	promptTokens := estimateTokens(info.Messages)
	cachedPromptTokens := 0
	cacheCreation5MTokens := 0
	cacheCreation1HTokens := 0
	completionTokens := 0
	if resp != nil && resp.Usage != nil {
		if resp.Usage.PromptTokens > 0 {
			promptTokens = resp.Usage.PromptTokens
		}
		cachedPromptTokens = resp.Usage.CachedPromptTokens
		cacheCreation5MTokens = resp.Usage.CacheCreation5MTokens
		cacheCreation1HTokens = resp.Usage.CacheCreation1HTokens
		if resp.Usage.CompletionTokens > 0 {
			completionTokens = resp.Usage.CompletionTokens
		} else {
			completionTokens = resp.TokensUsed - promptTokens
		}
	} else if resp != nil {
		completionTokens = resp.TokensUsed - promptTokens
	}
	if completionTokens < 0 {
		completionTokens = 0
	}

	m.store.RecordCallDetailed(m.idGen(), info.Provider, info.Model, m.sessionID, promptTokens, cachedPromptTokens, cacheCreation5MTokens, cacheCreation1HTokens, completionTokens)
	return resp, nil
}

func (m *CostTrackingMiddleware) InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error) {
	ch, err := next(ctx, info)
	if err != nil {
		return ch, err
	}

	// Wrap the stream to count tokens
	promptTokens := estimateTokens(info.Messages)
	wrappedCh := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(wrappedCh)
		completionTokens := 0
		promptTokensActual := 0
		cachedPromptTokens := 0
		cacheCreation5MTokens := 0
		cacheCreation1HTokens := 0
		sawCompletionUsage := false
		for chunk := range ch {
			if chunk.Usage != nil {
				if chunk.Usage.PromptTokens > 0 {
					promptTokensActual = chunk.Usage.PromptTokens
				}
				if chunk.Usage.CompletionTokens > 0 {
					completionTokens = chunk.Usage.CompletionTokens
					sawCompletionUsage = true
				}
				cachedPromptTokens = chunk.Usage.CachedPromptTokens
				cacheCreation5MTokens = chunk.Usage.CacheCreation5MTokens
				cacheCreation1HTokens = chunk.Usage.CacheCreation1HTokens
			}
			if !sawCompletionUsage {
				completionTokens += len(chunk.Content) / 4 // rough estimate
			}
			wrappedCh <- chunk
		}
		if promptTokensActual > 0 {
			promptTokens = promptTokensActual
		}
		m.store.RecordCallDetailed(m.idGen(), info.Provider, info.Model, m.sessionID, promptTokens, cachedPromptTokens, cacheCreation5MTokens, cacheCreation1HTokens, completionTokens)
	}()

	return wrappedCh, nil
}

func estimateTokens(messages []provider.Message) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content) / 4 // rough: 4 chars per token
	}
	return total
}

// --- RetryMiddleware ---

// RetryMiddleware adds retry logic to Provider calls.
type RetryMiddleware struct {
	config      resilience.RetryConfig
	isRetryable resilience.IsRetryableFunc
}

// NewRetryMiddleware creates a retry middleware.
func NewRetryMiddleware(config resilience.RetryConfig) *RetryMiddleware {
	return &RetryMiddleware{
		config:      config,
		isRetryable: resilience.DefaultIsRetryable,
	}
}

func (m *RetryMiddleware) Name() string { return "retry" }

func (m *RetryMiddleware) InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
	return resilience.RetryWithResult(ctx, m.config, m.isRetryable, func() (*provider.Response, error) {
		return next(ctx, info)
	})
}

func (m *RetryMiddleware) InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error) {
	return resilience.RetryWithResult(ctx, m.config, m.isRetryable, func() (<-chan provider.StreamChunk, error) {
		return next(ctx, info)
	})
}

// --- CircuitBreakerMiddleware ---

// CircuitBreakerMiddleware adds circuit breaking to Provider calls.
type CircuitBreakerMiddleware struct {
	cb *resilience.CircuitBreaker
}

// NewCircuitBreakerMiddleware creates a circuit breaker middleware.
func NewCircuitBreakerMiddleware(config resilience.CircuitBreakerConfig) *CircuitBreakerMiddleware {
	return &CircuitBreakerMiddleware{
		cb: resilience.NewCircuitBreaker(config),
	}
}

func (m *CircuitBreakerMiddleware) Name() string { return "circuit-breaker" }

func (m *CircuitBreakerMiddleware) CircuitBreaker() *resilience.CircuitBreaker {
	return m.cb
}

func (m *CircuitBreakerMiddleware) InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
	if err := m.cb.Allow(); err != nil {
		return nil, err
	}
	resp, err := next(ctx, info)
	if err != nil {
		m.cb.RecordFailure()
	} else {
		m.cb.RecordSuccess()
	}
	return resp, err
}

func (m *CircuitBreakerMiddleware) InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error) {
	if err := m.cb.Allow(); err != nil {
		return nil, err
	}
	ch, err := next(ctx, info)
	if err != nil {
		m.cb.RecordFailure()
	} else {
		m.cb.RecordSuccess()
	}
	return ch, err
}

// --- RateLimitMiddleware ---

// RateLimitMiddleware limits the rate of Provider calls.
type RateLimitMiddleware struct {
	mu     sync.Mutex
	limit  int           // max calls per window
	window time.Duration // time window
	calls  []time.Time   // timestamps of recent calls
}

// NewRateLimitMiddleware creates a rate limit middleware.
func NewRateLimitMiddleware(limit int, window time.Duration) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		limit:  limit,
		window: window,
		calls:  make([]time.Time, 0),
	}
}

func (m *RateLimitMiddleware) Name() string { return "rate-limit" }

func (m *RateLimitMiddleware) InterceptChat(ctx context.Context, info CallInfo, next ChatHandler) (*provider.Response, error) {
	if err := m.acquire(); err != nil {
		return nil, err
	}
	return next(ctx, info)
}

func (m *RateLimitMiddleware) InterceptChatStream(ctx context.Context, info CallInfo, next StreamHandler) (<-chan provider.StreamChunk, error) {
	if err := m.acquire(); err != nil {
		return nil, err
	}
	return next(ctx, info)
}

func (m *RateLimitMiddleware) acquire() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	// Prune old entries
	cutoff := now.Add(-m.window)
	valid := m.calls[:0]
	for _, t := range m.calls {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	m.calls = valid

	if len(m.calls) >= m.limit {
		return fmt.Errorf("rate limit exceeded: %d calls per %v", m.limit, m.window)
	}
	m.calls = append(m.calls, now)
	return nil
}

// ---------------------------------------------------------------------------
// MW-3: MiddlewareProvider
// ---------------------------------------------------------------------------

// MiddlewareProvider wraps a Provider with a middleware chain.
type MiddlewareProvider struct {
	inner provider.Provider
	chain *Chain
}

/**
 * NewMiddlewareProvider 创建一个新的 MiddlewareProvider，将给定的 provider 包装在中间件链中。
 */
func NewMiddlewareProvider(inner provider.Provider, chain *Chain) *MiddlewareProvider {
	if chain == nil {
		chain = NewChain()
	}
	return &MiddlewareProvider{inner: inner, chain: chain}
}

// Name returns the wrapped provider's name.
func (mp *MiddlewareProvider) Name() string {
	return mp.inner.Name()
}

// Chain returns the middleware chain.
func (mp *MiddlewareProvider) Chain() *Chain {
	return mp.chain
}

// Chat sends a message through the middleware chain.
func (mp *MiddlewareProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	info := CallInfo{
		Provider:  mp.inner.Name(),
		Messages:  messages,
		StartTime: time.Now(),
	}

	return mp.chain.ExecuteChat(ctx, info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		return mp.inner.Chat(ctx, messages)
	})
}

// ChatStream sends a message through the middleware chain with streaming.
func (mp *MiddlewareProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	info := CallInfo{
		Provider:  mp.inner.Name(),
		Messages:  messages,
		StartTime: time.Now(),
	}

	return mp.chain.ExecuteChatStream(ctx, info, func(ctx context.Context, info CallInfo) (<-chan provider.StreamChunk, error) {
		return mp.inner.ChatStream(ctx, messages)
	})
}

// ChatWithOptions sends a message through the middleware chain with function-calling options.
func (mp *MiddlewareProvider) ChatWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	info := CallInfo{
		Provider:  mp.inner.Name(),
		Messages:  messages,
		StartTime: time.Now(),
	}

	return mp.chain.ExecuteChat(ctx, info, func(ctx context.Context, info CallInfo) (*provider.Response, error) {
		if fc, ok := mp.inner.(provider.FunctionCallingProvider); ok {
			return fc.ChatWithOptions(ctx, messages, opts)
		}
		return mp.inner.Chat(ctx, messages)
	})
}

// ChatStreamWithOptions sends a streaming message through the middleware chain with function-calling options.
func (mp *MiddlewareProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	info := CallInfo{
		Provider:  mp.inner.Name(),
		Messages:  messages,
		StartTime: time.Now(),
	}

	return mp.chain.ExecuteChatStream(ctx, info, func(ctx context.Context, info CallInfo) (<-chan provider.StreamChunk, error) {
		if fc, ok := mp.inner.(provider.FunctionCallingProvider); ok {
			return fc.ChatStreamWithOptions(ctx, messages, opts)
		}
		return mp.inner.ChatStream(ctx, messages)
	})
}

// Validate validates the wrapped provider.
func (mp *MiddlewareProvider) Validate() error {
	return mp.inner.Validate()
}

// Ensure MiddlewareProvider implements Provider
var (
	_ provider.Provider                = (*MiddlewareProvider)(nil)
	_ provider.FunctionCallingProvider = (*MiddlewareProvider)(nil)
)
