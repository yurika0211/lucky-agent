package resilience

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/provider"
)

// ---------------------------------------------------------------------------
// RC-1: Retry Tests
// ---------------------------------------------------------------------------

func TestRetrySuccess(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	err := Retry(context.Background(), cfg, nil, func() error {
		return nil
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestRetrySuccessAfterFailures(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	callCount := 0
	err := Retry(context.Background(), cfg, nil, func() error {
		callCount++
		if callCount < 3 {
			return fmt.Errorf("429 rate limit")
		}
		return nil
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if callCount != 3 {
		t.Errorf("expected 3 calls, got %d", callCount)
	}
}

func TestRetryExhausted(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	err := Retry(context.Background(), cfg, nil, func() error {
		return fmt.Errorf("500 server error")
	})
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "retry exhausted") {
		t.Errorf("expected retry exhausted error, got %v", err)
	}
}

func TestRetryNonRetryable(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	callCount := 0
	err := Retry(context.Background(), cfg, nil, func() error {
		callCount++
		return fmt.Errorf("401 unauthorized")
	})
	if err == nil {
		t.Error("expected error")
	}
	if callCount != 1 {
		t.Errorf("expected 1 call (non-retryable), got %d", callCount)
	}
}

func TestRetryCustomIsRetryable(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	callCount := 0
	// Custom: only retry on "special" errors
	err := Retry(context.Background(), cfg, func(err error) bool {
		return err.Error() == "special"
	}, func() error {
		callCount++
		if callCount < 2 {
			return fmt.Errorf("special")
		}
		return fmt.Errorf("401 unauthorized")
	})
	if err == nil {
		t.Error("expected error")
	}
	if callCount != 2 {
		t.Errorf("expected 2 calls, got %d", callCount)
	}
}

func TestRetryContextCanceled(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 100, InitialDelay: 500 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err := Retry(ctx, cfg, nil, func() error {
		// Simulate a slow API call
		time.Sleep(50 * time.Millisecond)
		return fmt.Errorf("500 server error")
	})
	// The cancel should be caught either during fn() or during backoff sleep
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context canceled, got %v", err)
	}
}

func TestRetryWithResult(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, InitialDelay: 10 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	callCount := 0
	result, err := RetryWithResult(context.Background(), cfg, nil, func() (string, error) {
		callCount++
		if callCount < 2 {
			return "", fmt.Errorf("502 bad gateway")
		}
		return "success", nil
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if result != "success" {
		t.Errorf("expected 'success', got '%s'", result)
	}
}

func TestRetryBackoff(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 5, InitialDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond, BackoffFactor: 2, Jitter: false}
	start := time.Now()
	callCount := 0
	_ = Retry(context.Background(), cfg, nil, func() error {
		callCount++
		return fmt.Errorf("500")
	})
	elapsed := time.Since(start)
	// Expected delays: 10ms, 20ms, 40ms, 80ms = 150ms total (4 retries for 5 attempts)
	if elapsed < 100*time.Millisecond {
		t.Errorf("expected at least 100ms of backoff, got %v", elapsed)
	}
}

// ---------------------------------------------------------------------------
// RC-2: CircuitBreaker Tests
// ---------------------------------------------------------------------------

func TestCircuitBreakerStartsClosed(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())
	if cb.State() != StateClosed {
		t.Errorf("expected Closed, got %s", cb.State())
	}
}

func TestCircuitBreakerTripsOnFailures(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 3, SuccessThreshold: 2, Timeout: 100 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.State() != StateOpen {
		t.Errorf("expected Open after %d failures, got %s", cfg.FailureThreshold, cb.State())
	}
}

func TestCircuitBreakerRejectsWhenOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 1, Timeout: 100 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()

	if err := cb.Allow(); err == nil {
		t.Error("expected rejection when circuit is open")
	}
}

func TestCircuitBreakerTransitionsToHalfOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 1, Timeout: 50 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	if cb.State() != StateHalfOpen {
		t.Errorf("expected HalfOpen after timeout, got %s", cb.State())
	}
}

func TestCircuitBreakerAllowsInHalfOpen(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 1, Timeout: 50 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	if err := cb.Allow(); err != nil {
		t.Errorf("expected Allow in HalfOpen, got %v", err)
	}
}

func TestCircuitBreakerClosesAfterSuccesses(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 2, Timeout: 50 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	// HalfOpen: need 2 successes to close
	cb.RecordSuccess()
	if cb.State() != StateHalfOpen {
		t.Errorf("expected HalfOpen after 1 success, got %s", cb.State())
	}
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Errorf("expected Closed after 2 successes, got %s", cb.State())
	}
}

func TestCircuitBreakerAllowsSerialHalfOpenProbes(t *testing.T) {
	cfg := CircuitBreakerConfig{
		FailureThreshold: 2,
		SuccessThreshold: 3,
		Timeout:          50 * time.Millisecond,
		HalfOpenMaxReqs:  1,
	}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	for i := 0; i < cfg.SuccessThreshold; i++ {
		if err := cb.Allow(); err != nil {
			t.Fatalf("probe %d should be allowed: %v", i+1, err)
		}
		cb.RecordSuccess()
	}

	if cb.State() != StateClosed {
		t.Fatalf("expected Closed after serial half-open probes, got %s", cb.State())
	}
}

func TestCircuitBreakerReopensOnHalfOpenFailure(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 2, Timeout: 50 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)

	// Failure in HalfOpen → back to Open
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Errorf("expected Open after HalfOpen failure, got %s", cb.State())
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 1, Timeout: 1 * time.Hour}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("expected Closed after reset, got %s", cb.State())
	}
}

func TestCircuitBreakerStateChangeCallback(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 2, SuccessThreshold: 1, Timeout: 50 * time.Millisecond}
	cb := NewCircuitBreaker(cfg)

	var fromState, toState State
	cb.OnStateChange = func(from, to State) {
		fromState = from
		toState = to
	}

	cb.RecordFailure()
	cb.RecordFailure()
	time.Sleep(10 * time.Millisecond) // let callback fire

	if fromState != StateClosed || toState != StateOpen {
		t.Errorf("expected Closed→Open, got %s→%s", fromState, toState)
	}
}

func TestCircuitBreakerStats(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 5, SuccessThreshold: 3, Timeout: 30 * time.Second}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()

	stats := cb.Stats()
	if stats.State != StateClosed {
		t.Errorf("expected Closed, got %s", stats.State)
	}
	if stats.ConsecutiveFailures != 2 {
		t.Errorf("expected 2 failures, got %d", stats.ConsecutiveFailures)
	}
}

func TestCircuitBreakerSuccessResetsFailures(t *testing.T) {
	cfg := CircuitBreakerConfig{FailureThreshold: 3, SuccessThreshold: 1, Timeout: 30 * time.Second}
	cb := NewCircuitBreaker(cfg)

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets consecutive failures
	cb.RecordFailure()

	stats := cb.Stats()
	if stats.ConsecutiveFailures != 1 {
		t.Errorf("expected 1 failure after reset, got %d", stats.ConsecutiveFailures)
	}
}

// ---------------------------------------------------------------------------
// RC-3: ResilientProvider Tests
// ---------------------------------------------------------------------------

type mockProvider struct {
	chatFn    func(ctx context.Context, messages []provider.Message) (*provider.Response, error)
	streamFn  func(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error)
	validateFn func() error
}

func (m *mockProvider) Name() string { return "mock" }
func (m *mockProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, messages)
	}
	return &provider.Response{Content: "ok"}, nil
}
func (m *mockProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, messages)
	}
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) Validate() error {
	if m.validateFn != nil {
		return m.validateFn()
	}
	return nil
}

func TestResilientProviderChatSuccess(t *testing.T) {
	mock := &mockProvider{}
	rp := NewResilientProviderWithRetry(mock, RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2,
		Jitter:        false,
	})

	resp, err := rp.Chat(context.Background(), nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got '%s'", resp.Content)
	}
}

func TestResilientProviderChatRetry(t *testing.T) {
	callCount := int32(0)
	mock := &mockProvider{
		chatFn: func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
			count := atomic.AddInt32(&callCount, 1)
			if count < 3 {
				return nil, fmt.Errorf("502 bad gateway")
			}
			return &provider.Response{Content: "recovered"}, nil
		},
	}

	rp := NewResilientProviderWithRetry(mock, RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2,
		Jitter:        false,
	})

	resp, err := rp.Chat(context.Background(), nil)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("expected 'recovered', got '%s'", resp.Content)
	}
}

func TestResilientProviderCircuitBreaker(t *testing.T) {
	mock := &mockProvider{
		chatFn: func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
			return nil, fmt.Errorf("500 internal server error")
		},
	}

	rp := NewResilientProvider(mock, RetryConfig{
		MaxAttempts:   1, // no retry, let CB handle it
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2,
	}, CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Timeout:          50 * time.Millisecond,
	})

	// Trip the circuit breaker
	for i := 0; i < 3; i++ {
		_, _ = rp.Chat(context.Background(), nil)
	}

	// Should be rejected by circuit breaker
	_, err := rp.Chat(context.Background(), nil)
	if err == nil {
		t.Error("expected circuit breaker rejection")
	}
	if !strings.Contains(err.Error(), "circuit breaker") {
		t.Errorf("expected circuit breaker error, got %v", err)
	}
}

func TestResilientProviderCircuitBreakerRecovery(t *testing.T) {
	failCount := int32(0)
	mock := &mockProvider{
		chatFn: func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
			count := atomic.AddInt32(&failCount, 1)
			if count <= 3 {
				return nil, fmt.Errorf("500")
			}
			return &provider.Response{Content: "recovered"}, nil
		},
	}

	rp := NewResilientProvider(mock, RetryConfig{
		MaxAttempts:   1,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2,
	}, CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Timeout:          50 * time.Millisecond,
	})

	// Trip the breaker
	for i := 0; i < 3; i++ {
		_, _ = rp.Chat(context.Background(), nil)
	}

	// Wait for HalfOpen
	time.Sleep(60 * time.Millisecond)

	// Should recover
	resp, err := rp.Chat(context.Background(), nil)
	if err != nil {
		t.Errorf("expected recovery, got %v", err)
	}
	if resp.Content != "recovered" {
		t.Errorf("expected 'recovered', got '%s'", resp.Content)
	}
}

func TestResilientProviderName(t *testing.T) {
	mock := &mockProvider{}
	rp := NewResilientProviderWithRetry(mock, DefaultRetryConfig())
	if rp.Name() != "mock" {
		t.Errorf("expected 'mock', got '%s'", rp.Name())
	}
}

func TestResilientProviderValidate(t *testing.T) {
	mock := &mockProvider{}
	rp := NewResilientProviderWithRetry(mock, DefaultRetryConfig())
	if err := rp.Validate(); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestResilientProviderStreamRetry(t *testing.T) {
	callCount := int32(0)
	mock := &mockProvider{
		streamFn: func(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
			count := atomic.AddInt32(&callCount, 1)
			if count < 2 {
				return nil, fmt.Errorf("503 service unavailable")
			}
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "stream-ok", Done: true}
			close(ch)
			return ch, nil
		},
	}

	rp := NewResilientProviderWithRetry(mock, RetryConfig{
		MaxAttempts:   3,
		InitialDelay:  10 * time.Millisecond,
		BackoffFactor: 2,
		Jitter:        false,
	})

	ch, err := rp.ChatStream(context.Background(), nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	chunk := <-ch
	if chunk.Content != "stream-ok" {
		t.Errorf("expected 'stream-ok', got '%s'", chunk.Content)
	}
}
