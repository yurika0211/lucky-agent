// Package resilience provides retry and circuit breaker patterns for Provider calls.
// It wraps any Provider with configurable retry logic and circuit breaking
// to handle transient failures gracefully.
package resilience

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/provider"
)

// ---------------------------------------------------------------------------
// RC-1: Retry
// ---------------------------------------------------------------------------

// RetryConfig configures retry behavior.
type RetryConfig struct {
	MaxAttempts   int           `json:"maxAttempts" yaml:"maxAttempts"`     // total attempts (including first call)
	InitialDelay  time.Duration `json:"initialDelay" yaml:"initialDelay"`   // first retry delay
	MaxDelay      time.Duration `json:"maxDelay" yaml:"maxDelay"`           // cap on delay
	BackoffFactor float64       `json:"backoffFactor" yaml:"backoffFactor"` // exponential multiplier (default 2.0)
	Jitter        bool          `json:"jitter" yaml:"jitter"`               // add random jitter
}

// DefaultRetryConfig returns sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  500 * time.Millisecond,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
		Jitter:        true,
	}
}

// IsRetryable determines if an error is worth retrying.
// By default, retries on timeout, rate limit (429), and server errors (5xx).
type IsRetryableFunc func(err error) bool

// DefaultIsRetryable retries on context deadline, rate limits, and server errors.
func DefaultIsRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Context timeout/deadline
	if msg == "context deadline exceeded" || msg == "context canceled" {
		return true
	}
	// Rate limit
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") {
		return true
	}
	// Server errors
	if strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return true
	}
	// Connection errors
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "timeout") {
		return true
	}
	return false
}

// Retry wraps a function with retry logic.
func Retry(ctx context.Context, cfg RetryConfig, isRetryable IsRetryableFunc, fn func() error) error {
	if isRetryable == nil {
		isRetryable = DefaultIsRetryable
	}

	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		// Not retryable → give up immediately
		if !isRetryable(lastErr) {
			return lastErr
		}

		// Last attempt → don't sleep
		if attempt == cfg.MaxAttempts-1 {
			break
		}

		delay := backoff(cfg, attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return fmt.Errorf("retry exhausted after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

// RetryWithResult wraps a function that returns a value with retry logic.
func RetryWithResult[T any](ctx context.Context, cfg RetryConfig, isRetryable IsRetryableFunc, fn func() (T, error)) (T, error) {
	if isRetryable == nil {
		isRetryable = DefaultIsRetryable
	}

	var lastErr error
	var zero T
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err
		if !isRetryable(err) {
			return zero, err
		}

		if attempt == cfg.MaxAttempts-1 {
			break
		}

		delay := backoff(cfg, attempt)
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(delay):
		}
	}
	return zero, fmt.Errorf("retry exhausted after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

func backoff(cfg RetryConfig, attempt int) time.Duration {
	delay := time.Duration(float64(cfg.InitialDelay) * math.Pow(cfg.BackoffFactor, float64(attempt)))
	if delay > cfg.MaxDelay {
		delay = cfg.MaxDelay
	}
	if cfg.Jitter {
		jitter := time.Duration(rand.Int63n(int64(delay) / 2))
		delay += jitter
	}
	return delay
}

// ---------------------------------------------------------------------------
// RC-2: CircuitBreaker
// ---------------------------------------------------------------------------

// State represents the circuit breaker state.
type State int

const (
	StateClosed   State = iota // Normal: requests pass through
	StateOpen                  // Tripped: requests are rejected
	StateHalfOpen              // Probing: limited requests to test recovery
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "Closed"
	case StateOpen:
		return "Open"
	case StateHalfOpen:
		return "HalfOpen"
	default:
		return "Unknown"
	}
}

// CircuitBreakerConfig configures circuit breaker behavior.
type CircuitBreakerConfig struct {
	FailureThreshold int           `json:"failureThreshold" yaml:"failureThreshold"` // failures to trip (default 5)
	SuccessThreshold int           `json:"successThreshold" yaml:"successThreshold"` // successes in HalfOpen to close (default 3)
	Timeout          time.Duration `json:"timeout" yaml:"timeout"`                   // Open→HalfOpen after this duration (default 30s)
	HalfOpenMaxReqs  int           `json:"halfOpenMaxReqs" yaml:"halfOpenMaxReqs"`   // max concurrent requests in HalfOpen (default 1)
}

// DefaultCircuitBreakerConfig returns sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		SuccessThreshold: 3,
		Timeout:          30 * time.Second,
		HalfOpenMaxReqs:  1,
	}
}

// CircuitBreaker implements the circuit breaker pattern.
type CircuitBreaker struct {
	mu     sync.RWMutex
	config CircuitBreakerConfig
	state  State
	// Closed state tracking
	consecutiveFailures int
	// HalfOpen state tracking
	halfOpenSuccesses int
	halfOpenRequests  int
	// Open state tracking
	openedAt time.Time
	// Callbacks
	OnStateChange func(from, to State)
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		config: cfg,
		state:  StateClosed,
	}
}

// State returns the current state.
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	// Auto-transition from Open to HalfOpen if timeout elapsed
	if cb.state == StateOpen && !cb.openedAt.IsZero() && time.Since(cb.openedAt) >= cb.config.Timeout {
		cb.transition(StateHalfOpen)
	}
	return cb.state
}

// Allow checks if a request is allowed to proceed.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil
	case StateOpen:
		if !cb.openedAt.IsZero() && time.Since(cb.openedAt) >= cb.config.Timeout {
			cb.transition(StateHalfOpen)
			cb.halfOpenRequests++
			return nil
		}
		return fmt.Errorf("circuit breaker is open (failures: %d, opened: %v ago)",
			cb.consecutiveFailures, time.Since(cb.openedAt).Round(time.Second))
	case StateHalfOpen:
		if cb.halfOpenRequests < cb.config.HalfOpenMaxReqs {
			cb.halfOpenRequests++
			return nil
		}
		return fmt.Errorf("circuit breaker is half-open (max %d probe requests)", cb.config.HalfOpenMaxReqs)
	default:
		return nil
	}
}

// RecordSuccess records a successful call.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Auto-transition check
	cb.checkTimeoutLocked()

	switch cb.state {
	case StateClosed:
		cb.consecutiveFailures = 0
	case StateHalfOpen:
		if cb.halfOpenRequests > 0 {
			cb.halfOpenRequests--
		}
		cb.halfOpenSuccesses++
		if cb.halfOpenSuccesses >= cb.config.SuccessThreshold {
			cb.transition(StateClosed)
		}
	}
}

// RecordFailure records a failed call.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// Auto-transition check
	cb.checkTimeoutLocked()

	switch cb.state {
	case StateClosed:
		cb.consecutiveFailures++
		if cb.consecutiveFailures >= cb.config.FailureThreshold {
			cb.transition(StateOpen)
		}
	case StateHalfOpen:
		if cb.halfOpenRequests > 0 {
			cb.halfOpenRequests--
		}
		// Any failure in HalfOpen → back to Open
		cb.transition(StateOpen)
	}
}

// checkTimeoutLocked transitions from Open to HalfOpen if timeout has elapsed.
// Must be called with cb.mu held.
func (cb *CircuitBreaker) checkTimeoutLocked() {
	if cb.state == StateOpen && !cb.openedAt.IsZero() && time.Since(cb.openedAt) >= cb.config.Timeout {
		cb.transition(StateHalfOpen)
	}
}

// Reset forces the circuit breaker to Closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.transition(StateClosed)
}

// Stats returns current circuit breaker statistics.
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	stats := CircuitBreakerStats{
		State:               cb.state,
		ConsecutiveFailures: cb.consecutiveFailures,
		HalfOpenSuccesses:   cb.halfOpenSuccesses,
		HalfOpenRequests:    cb.halfOpenRequests,
	}
	if cb.state == StateOpen && !cb.openedAt.IsZero() {
		stats.TimeUntilHalfOpen = cb.config.Timeout - time.Since(cb.openedAt)
		if stats.TimeUntilHalfOpen < 0 {
			stats.TimeUntilHalfOpen = 0
		}
	}
	return stats
}

// CircuitBreakerStats holds circuit breaker statistics.
type CircuitBreakerStats struct {
	State               State         `json:"state"`
	ConsecutiveFailures int           `json:"consecutiveFailures"`
	HalfOpenSuccesses   int           `json:"halfOpenSuccesses"`
	HalfOpenRequests    int           `json:"halfOpenRequests"`
	TimeUntilHalfOpen   time.Duration `json:"timeUntilHalfOpen,omitempty"`
}

func (cb *CircuitBreaker) transition(to State) {
	from := cb.state
	cb.state = to

	switch to {
	case StateClosed:
		cb.consecutiveFailures = 0
		cb.halfOpenSuccesses = 0
		cb.halfOpenRequests = 0
		cb.openedAt = time.Time{}
	case StateOpen:
		cb.openedAt = time.Now()
		cb.halfOpenSuccesses = 0
		cb.halfOpenRequests = 0
	case StateHalfOpen:
		cb.halfOpenSuccesses = 0
		cb.halfOpenRequests = 0
	}

	if cb.OnStateChange != nil && from != to {
		cb.OnStateChange(from, to)
	}
}

// ---------------------------------------------------------------------------
// RC-3: ResilientProvider
// ---------------------------------------------------------------------------

// ResilientProvider wraps a Provider with retry and circuit breaker.
type ResilientProvider struct {
	inner   provider.Provider
	retry   RetryConfig
	cb      *CircuitBreaker
	retryFn IsRetryableFunc
}

// NewResilientProvider creates a new resilient provider wrapper.
func NewResilientProvider(inner provider.Provider, retryCfg RetryConfig, cbCfg CircuitBreakerConfig) *ResilientProvider {
	return &ResilientProvider{
		inner:   inner,
		retry:   retryCfg,
		cb:      NewCircuitBreaker(cbCfg),
		retryFn: DefaultIsRetryable,
	}
}

// NewResilientProviderWithRetry creates a provider with only retry (no circuit breaker).
func NewResilientProviderWithRetry(inner provider.Provider, retryCfg RetryConfig) *ResilientProvider {
	return &ResilientProvider{
		inner:   inner,
		retry:   retryCfg,
		cb:      nil,
		retryFn: DefaultIsRetryable,
	}
}

// Name returns the wrapped provider's name.
func (rp *ResilientProvider) Name() string {
	return rp.inner.Name()
}

// Chat sends a message with retry and circuit breaker protection.
func (rp *ResilientProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	// Check circuit breaker
	if rp.cb != nil {
		if err := rp.cb.Allow(); err != nil {
			return nil, err
		}
	}

	resp, err := RetryWithResult(ctx, rp.retry, rp.retryFn, func() (*provider.Response, error) {
		return rp.inner.Chat(ctx, messages)
	})

	if rp.cb != nil {
		if err != nil {
			rp.cb.RecordFailure()
		} else {
			rp.cb.RecordSuccess()
		}
	}

	return resp, err
}

// ChatStream sends a message with streaming and retry protection.
func (rp *ResilientProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	// Check circuit breaker
	if rp.cb != nil {
		if err := rp.cb.Allow(); err != nil {
			return nil, err
		}
	}

	ch, err := RetryWithResult(ctx, rp.retry, rp.retryFn, func() (<-chan provider.StreamChunk, error) {
		return rp.inner.ChatStream(ctx, messages)
	})

	if rp.cb != nil {
		if err != nil {
			rp.cb.RecordFailure()
		} else {
			rp.cb.RecordSuccess()
		}
	}

	return ch, err
}

// Validate validates the wrapped provider.
func (rp *ResilientProvider) Validate() error {
	return rp.inner.Validate()
}

// Ensure ResilientProvider implements Provider
var _ provider.Provider = (*ResilientProvider)(nil)
