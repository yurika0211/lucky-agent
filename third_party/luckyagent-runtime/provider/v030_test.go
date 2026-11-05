package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockProvider 用于测试的 mock Provider
type mockProvider struct {
	name        string
	chatErr     error
	streamErr   error
	validateErr error
	chatCalls   int
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	m.chatCalls++
	if m.chatErr != nil {
		return nil, m.chatErr
	}
	return &Response{Content: "mock response", Model: m.name}, nil
}

func (m *mockProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Content: "mock stream", Done: true, Model: m.name}
	close(ch)
	return ch, nil
}

func (m *mockProvider) Validate() error {
	return m.validateErr
}

// --- FallbackChain Tests ---

func TestFallbackChainSingleProvider(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
	}

	chain, err := NewFallbackChain(configs, registry)
	if err != nil {
		t.Fatalf("NewFallbackChain: %v", err)
	}

	if chain.ChainLen() != 1 {
		t.Errorf("expected chain len 1, got %d", chain.ChainLen())
	}

	if chain.Name() != "mock1" {
		t.Errorf("expected name mock1, got %s", chain.Name())
	}
}

func TestFallbackChainChatSuccess(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "mock response" {
		t.Errorf("expected 'mock response', got %s", resp.Content)
	}
}

func TestFallbackChainChatFallback(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", chatErr: fmt.Errorf("connection refused")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Model != "mock2" {
		t.Errorf("expected fallback to mock2, got %s", resp.Model)
	}
}

func TestFallbackChainRetriesPrimaryOnNextRequest(t *testing.T) {
	registry := NewRegistry()
	primary := &mockProvider{name: "mock1", chatErr: fmt.Errorf("connection refused")}
	fallback := &mockProvider{name: "mock2"}
	registry.RegisterFactory("mock1", func(cfg Config) Provider { return primary })
	registry.RegisterFactory("mock2", func(cfg Config) Provider { return fallback })

	chain, _ := NewFallbackChain([]FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "primary"},
		{Name: "mock2", APIKey: "test", Model: "fallback"},
	}, registry)

	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "first"}})
	if err != nil || resp.Model != "mock2" {
		t.Fatalf("expected first request to use fallback, response=%v err=%v", resp, err)
	}
	if got := chain.ActiveIndex(); got != 1 {
		t.Fatalf("expected fallback to be active after success, got %d", got)
	}

	primary.chatErr = nil
	resp, err = chain.Chat(context.Background(), []Message{{Role: "user", Content: "second"}})
	if err != nil || resp.Model != "mock1" {
		t.Fatalf("expected next request to probe recovered primary, response=%v err=%v", resp, err)
	}
	if got := chain.ActiveIndex(); got != 0 {
		t.Fatalf("expected recovered primary to become active, got %d", got)
	}
	if primary.chatCalls != 2 || fallback.chatCalls != 1 {
		t.Fatalf("unexpected call counts: primary=%d fallback=%d", primary.chatCalls, fallback.chatCalls)
	}
}

func TestFallbackChainRetriesPrimaryAfterCooldown(t *testing.T) {
	registry := NewRegistry()
	primary := &mockProvider{name: "mock1", chatErr: fmt.Errorf("connection refused")}
	fallback := &mockProvider{name: "mock2"}
	registry.RegisterFactory("mock1", func(cfg Config) Provider { return primary })
	registry.RegisterFactory("mock2", func(cfg Config) Provider { return fallback })

	chain, _ := NewFallbackChain([]FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "primary"},
		{Name: "mock2", APIKey: "test", Model: "fallback"},
	}, registry)
	chain.maxFails = 1
	chain.cooldown = time.Hour

	if _, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "first"}}); err != nil {
		t.Fatalf("first request: %v", err)
	}
	primary.chatErr = nil
	if _, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "during cooldown"}}); err != nil {
		t.Fatalf("request during cooldown: %v", err)
	}
	if primary.chatCalls != 1 {
		t.Fatalf("primary should be skipped during cooldown, calls=%d", primary.chatCalls)
	}

	chain.mu.Lock()
	chain.cooldownAt[0] = time.Now().Add(-time.Second)
	chain.mu.Unlock()
	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "after cooldown"}})
	if err != nil || resp.Model != "mock1" {
		t.Fatalf("expected primary after cooldown, response=%v err=%v", resp, err)
	}
}

func TestFallbackChainDuplicateProviderNamesUseDistinctConfigs(t *testing.T) {
	registry := NewRegistry()
	createdModels := make([]string, 0, 2)
	registry.RegisterFactory("mock", func(cfg Config) Provider {
		createdModels = append(createdModels, cfg.LlmProvider.Model)
		return &mockProvider{name: cfg.LlmProvider.Model}
	})

	configs := []FallbackConfig{
		{Name: "mock", APIKey: "test", Model: "primary-model"},
		{Name: "mock", APIKey: "test", Model: "fallback-model"},
	}

	chain, err := NewFallbackChain(configs, registry)
	if err != nil {
		t.Fatalf("NewFallbackChain: %v", err)
	}
	if len(createdModels) != 2 {
		t.Fatalf("expected two provider instances, got %d (%v)", len(createdModels), createdModels)
	}
	if chain.chain[0] == chain.chain[1] {
		t.Fatal("expected duplicate provider names to create distinct provider instances")
	}

	resp, err := chain.chain[1].Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("fallback provider Chat: %v", err)
	}
	if resp.Model != "fallback-model" {
		t.Fatalf("expected fallback provider to use fallback config, got %q", resp.Model)
	}
}

func TestFallbackChainChatNoFallbackOnRequestError(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", chatErr: fmt.Errorf("API error 400: invalid request")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	_, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected primary request error")
	}
	if err.Error() != "API error 400: invalid request" {
		t.Fatalf("expected original request error, got %v", err)
	}
	if got := chain.failCounts[0]; got != 0 {
		t.Fatalf("expected primary fail count to remain 0 for request error, got %d", got)
	}
	if got := chain.ActiveIndex(); got != 0 {
		t.Fatalf("expected active provider to remain primary, got index %d", got)
	}
}

func TestFallbackChainAllFail(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", chatErr: fmt.Errorf("fail1")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2", chatErr: fmt.Errorf("fail2")}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	_, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Error("expected error when all providers fail")
	}
}

func TestFallbackChainStreamSuccess(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	ch, err := chain.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	var content string
	for chunk := range ch {
		content += chunk.Content
	}
	if content != "mock stream" {
		t.Errorf("expected 'mock stream', got %s", content)
	}
}

func TestFallbackChainCooldown(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", chatErr: fmt.Errorf("connection refused")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	chain.maxFails = 1 // 快速触发降级
	chain.cooldown = 10 * time.Minute

	// 第一次调用：mock1 失败，降级到 mock2
	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Model != "mock2" {
		t.Errorf("expected fallback to mock2, got %s", resp.Model)
	}

	// mock1 应该在冷却中
	if chain.isAvailable(0) {
		t.Error("mock1 should be in cooldown")
	}
}

func TestFallbackChainStreamNoFallbackOnRequestError(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", streamErr: fmt.Errorf("API error 401: unauthorized")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	_, err := chain.ChatStream(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected primary stream request error")
	}
	if err.Error() != "API error 401: unauthorized" {
		t.Fatalf("expected original stream error, got %v", err)
	}
	if got := chain.failCounts[0]; got != 0 {
		t.Fatalf("expected primary fail count to remain 0 for stream request error, got %d", got)
	}
	if got := chain.ActiveIndex(); got != 0 {
		t.Fatalf("expected active provider to remain primary, got index %d", got)
	}
}

func TestFallbackChainDisablesModelNotFoundFallback(t *testing.T) {
	registry := NewRegistry()
	primary := &mockProvider{name: "mock1", chatErr: fmt.Errorf("connection refused")}
	fallback := &mockProvider{name: "mock2", chatErr: fmt.Errorf(`API error 404: {"error":{"type":"model_not_found"}}`)}
	registry.RegisterFactory("mock1", func(cfg Config) Provider { return primary })
	registry.RegisterFactory("mock2", func(cfg Config) Provider { return fallback })

	chain, _ := NewFallbackChain([]FallbackConfig{
		{Name: "mock1", APIKey: "test", APIBase: "https://primary.example/v1", Model: "primary"},
		{Name: "mock2", APIKey: "other", APIBase: "https://fallback.example/v1", Model: "missing"},
	}, registry)
	chain.maxFails = 1

	_, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil || !strings.Contains(err.Error(), "model_not_found") {
		t.Fatalf("expected preserved model_not_found error, got %v", err)
	}
	if !chain.modelError[1] || chain.isAvailable(1) {
		t.Fatal("expected model-not-found fallback to be cooling")
	}

	primary.chatErr = nil
	chain.mu.Lock()
	chain.cooldownAt[0] = chain.clock().Add(-time.Second)
	chain.mu.Unlock()
	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "again"}})
	if err != nil || resp.Model != "mock1" {
		t.Fatalf("expected recovered primary without retrying cooled fallback, response=%v err=%v", resp, err)
	}
	if fallback.chatCalls != 1 {
		t.Fatalf("cooled fallback should not be called again, calls=%d", fallback.chatCalls)
	}
}

func TestFallbackChainModelErrorCoolsOnlyThatModel(t *testing.T) {
	registry := NewRegistry()
	missing := &mockProvider{name: "openai", chatErr: fmt.Errorf(`API error 404: {"error":{"message":"Model \"grok-4.7\" is not supported by any configured account in this group","type":"model_not_found"}}`)}
	other := &mockProvider{name: "openai"}
	registry.RegisterFactory("openai", func(cfg Config) Provider {
		if cfg.LlmProvider.Model == "grok-4.7" {
			return missing
		}
		return other
	})

	chain, err := NewFallbackChain([]FallbackConfig{
		{Name: "openai", APIKey: "same-key", APIBase: "https://gateway.example/v1", Model: "grok-4.7"},
		{Name: "openai", APIKey: "same-key", APIBase: "https://gateway.example/v1", Model: "claude"},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil || resp.Model != "openai" || other.chatCalls != 1 {
		t.Fatalf("response=%v err=%v otherCalls=%d", resp, err, other.chatCalls)
	}
	if chain.isAvailable(0) || !chain.modelError[0] {
		t.Fatal("missing model should be cooling")
	}
	if missing.chatCalls != 1 {
		t.Fatalf("missing model calls=%d", missing.chatCalls)
	}

	chain.mu.Lock()
	chain.cooldownAt[0] = chain.clock().Add(-time.Second)
	chain.mu.Unlock()
	missing.chatErr = nil
	if _, err := chain.Chat(context.Background(), []Message{{Role: "user", Content: "probe"}}); err != nil {
		t.Fatal(err)
	}
	if missing.chatCalls != 2 || chain.modelError[0] {
		t.Fatalf("expected one probe to clear cooldown, calls=%d modelError=%v", missing.chatCalls, chain.modelError[0])
	}
	status := chain.Status()
	if status.Nodes[0].Credential == "same-key" || status.Nodes[0].Credential == "" {
		t.Fatalf("credential label leaked or empty: %+v", status.Nodes[0])
	}
	if status.Nodes[0].Model != "grok-4.7" || status.Nodes[1].Model != "claude" {
		t.Fatalf("status=%+v", status)
	}
}

type statusProvider struct {
	name, model, state string
	failures           int
	err                error
	calls              int
}

func (p *statusProvider) Name() string                 { return p.name }
func (p *statusProvider) Validate() error              { return nil }
func (p *statusProvider) CircuitStatus() (string, int) { return p.state, p.failures }
func (p *statusProvider) Chat(context.Context, []Message) (*Response, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return &Response{Content: "ok", Model: p.model}, nil
}
func (p *statusProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	resp, err := p.Chat(ctx, messages)
	if err != nil {
		return nil, err
	}
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Content: resp.Content, Done: true, Model: resp.Model}
	close(ch)
	return ch, nil
}

func TestFallbackChainStatusIncludesNodeCircuit(t *testing.T) {
	registry := NewRegistry()
	primary := &statusProvider{name: "openai", model: "grok-4.7", state: "Open", failures: 5, err: fmt.Errorf("tls: handshake failure")}
	other := &statusProvider{name: "openai", model: "claude", state: "Closed"}
	registry.RegisterFactory("openai", func(cfg Config) Provider {
		if cfg.LlmProvider.Model == "grok-4.7" {
			return primary
		}
		return other
	})
	chain, err := NewFallbackChain([]FallbackConfig{
		{Name: "openai", APIKey: "k", APIBase: "https://a.example/v1", Model: "grok-4.7"},
		{Name: "openai", APIKey: "k2", APIBase: "https://b.example/v1", Model: "claude"},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := chain.Chat(context.Background(), nil)
	if err != nil || resp.Model != "claude" {
		t.Fatalf("resp=%v err=%v", resp, err)
	}
	status := chain.Status()
	if status.Nodes[0].CircuitState != "Open" || status.Nodes[0].CircuitFailures != 5 {
		t.Fatalf("primary status=%+v", status.Nodes[0])
	}
	if status.Nodes[1].CircuitState != "Closed" || !status.Nodes[1].Available {
		t.Fatalf("other status=%+v", status.Nodes[1])
	}
	if other.calls != 1 || primary.calls != 1 {
		t.Fatalf("primary=%d other=%d", primary.calls, other.calls)
	}
}

func TestFallbackChainSharedEndpointDoesNotSwitchOnTLS(t *testing.T) {
	registry := NewRegistry()
	primaryCalls := 0
	fallbackCalls := 0
	registry.RegisterFactory("openai", func(cfg Config) Provider {
		return &countingProvider{name: "openai", model: cfg.LlmProvider.Model, onChat: func(model string) error {
			if model == "grok-4.7" {
				primaryCalls++
				return fmt.Errorf("remote error: tls: handshake failure")
			}
			fallbackCalls++
			return fmt.Errorf(`API error 404: {"error":{"type":"model_not_found"}}`)
		}}
	})
	chain, err := NewFallbackChain([]FallbackConfig{
		{Name: "openai", APIKey: "same", APIBase: "https://gateway.example/v1", Model: "grok-4.7"},
		{Name: "openai", APIKey: "same", APIBase: "https://gateway.example/v1", Model: "grok-4.5"},
	}, registry)
	if err != nil {
		t.Fatal(err)
	}
	chain.maxFails = 3

	_, err = chain.Chat(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "tls") {
		t.Fatalf("err=%v", err)
	}
	if primaryCalls != 1 || fallbackCalls != 0 {
		t.Fatalf("primary=%d fallback=%d", primaryCalls, fallbackCalls)
	}
	if chain.failCounts[0] != 1 || chain.modelError[1] {
		t.Fatalf("counts=%v modelError=%v", chain.failCounts, chain.modelError)
	}
}

type countingProvider struct {
	name   string
	model  string
	onChat func(model string) error
}

func (p *countingProvider) Name() string    { return p.name }
func (p *countingProvider) Validate() error { return nil }
func (p *countingProvider) Chat(context.Context, []Message) (*Response, error) {
	if err := p.onChat(p.model); err != nil {
		return nil, err
	}
	return &Response{Content: "ok", Model: p.model}, nil
}
func (p *countingProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	resp, err := p.Chat(ctx, messages)
	if err != nil {
		return nil, err
	}
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Content: resp.Content, Model: resp.Model, Done: true}
	close(ch)
	return ch, nil
}

func TestFallbackChainResetCooldown(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	chain.maxFails = 1
	chain.cooldown = 10 * time.Minute

	// 手动设置冷却
	chain.mu.Lock()
	chain.failCounts[0] = 3
	chain.modelError[0] = true
	chain.cooldownAt[0] = time.Now().Add(10 * time.Minute)
	chain.mu.Unlock()

	if chain.isAvailable(0) {
		t.Error("should be in cooldown")
	}

	chain.ResetCooldown(0)

	if !chain.isAvailable(0) {
		t.Error("should be available after reset")
	}
	if chain.modelError[0] {
		t.Error("provider model cooldown should be cleared after reset")
	}
}

func TestFallbackChainValidate(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", validateErr: fmt.Errorf("invalid")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	if err := chain.Validate(); err != nil {
		t.Errorf("expected valid (mock2 is ok), got: %v", err)
	}
}

func TestFallbackChainValidateAllInvalid(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", validateErr: fmt.Errorf("invalid")}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	if err := chain.Validate(); err == nil {
		t.Error("expected error when all providers invalid")
	}
}

func TestFallbackChainOnSwitch(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterFactory("mock1", func(cfg Config) Provider {
		return &mockProvider{name: "mock1", chatErr: fmt.Errorf("connection refused")}
	})
	registry.RegisterFactory("mock2", func(cfg Config) Provider {
		return &mockProvider{name: "mock2"}
	})

	configs := []FallbackConfig{
		{Name: "mock1", APIKey: "test", Model: "test"},
		{Name: "mock2", APIKey: "test", Model: "test"},
	}

	chain, _ := NewFallbackChain(configs, registry)
	chain.maxFails = 1

	switched := false
	var fromName, toName string
	var cbMu sync.Mutex
	chain.SetOnSwitch(func(from, to string) {
		cbMu.Lock()
		switched = true
		fromName = from
		toName = to
		cbMu.Unlock()
	})

	chain.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}})

	// 等待 goroutine 回调
	time.Sleep(100 * time.Millisecond)

	cbMu.Lock()
	s := switched
	fn := fromName
	tn := toName
	cbMu.Unlock()

	if !s {
		t.Error("expected onSwitch callback")
	}
	if !strings.Contains(fn, "mock1 model=test") || !strings.Contains(tn, "mock2 model=test") {
		t.Errorf("expected switch from mock1 to mock2, got %s to %s", fn, tn)
	}
}

// --- Anthropic Provider Tests ---

func TestAnthropicProviderDefaults(t *testing.T) {
	p := NewAnthropicProvider(Config{})
	ap := p.(*AnthropicProvider)
	if ap.cfg.LlmProvider.BaseURL != "https://api.anthropic.com" {
		t.Errorf("expected default APIBase, got %s", ap.cfg.LlmProvider.BaseURL)
	}
	if ap.cfg.LlmProvider.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected default model, got %s", ap.cfg.LlmProvider.Model)
	}
}

func TestAnthropicProviderValidate(t *testing.T) {
	p := NewAnthropicProvider(Config{})
	if err := p.Validate(); err == nil {
		t.Error("expected error for missing api_key")
	}

	p2 := NewAnthropicProvider(Config{LlmProvider: LlmProvider{APIKey: "sk-ant-test"}})
	if err := p2.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Ollama Provider Tests ---

func TestOllamaProviderDefaults(t *testing.T) {
	p := NewOllamaProvider(Config{})
	op := p.(*OllamaProvider)
	if op.cfg.LlmProvider.BaseURL != "http://localhost:11434" {
		t.Errorf("expected default APIBase, got %s", op.cfg.LlmProvider.BaseURL)
	}
	if op.cfg.LlmProvider.Model != "llama3" {
		t.Errorf("expected default model llama3, got %s", op.cfg.LlmProvider.Model)
	}
}

// --- OpenRouter Provider Tests ---

func TestOpenRouterProviderDefaults(t *testing.T) {
	p := NewOpenRouterProvider(Config{})
	op := p.(*OpenRouterProvider)
	if op.cfg.LlmProvider.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("expected default APIBase, got %s", op.cfg.LlmProvider.BaseURL)
	}
	if op.cfg.LlmProvider.Model != "openai/gpt-5.4-mini" {
		t.Errorf("expected default model, got %s", op.cfg.LlmProvider.Model)
	}
}

func TestOpenRouterProviderValidate(t *testing.T) {
	p := NewOpenRouterProvider(Config{})
	if err := p.Validate(); err == nil {
		t.Error("expected error for missing api_key")
	}

	p2 := NewOpenRouterProvider(Config{LlmProvider: LlmProvider{APIKey: "sk-or-test"}})
	if err := p2.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Registry Tests (updated) ---

func TestRegistryAvailableWithNewProviders(t *testing.T) {
	r := NewRegistry()
	available := r.Available()
	expected := map[string]bool{
		"openai":            false,
		"openai-compatible": false,
		"anthropic":         false,
		"ollama":            false,
		"openrouter":        false,
	}

	for _, name := range available {
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}

	for name, found := range expected {
		if !found {
			t.Errorf("expected provider %s in available list", name)
		}
	}
}

// --- Token Store Tests ---

func TestTokenStoreBasic(t *testing.T) {
	dir := t.TempDir()
	ts, err := NewTokenStore(dir)
	if err != nil {
		t.Fatalf("NewTokenStore: %v", err)
	}

	entry := &TokenEntry{
		Provider:     "openai",
		AccessToken:  "sk-test-token-12345678",
		RefreshToken: "rt-test-refresh-12345678",
	}

	if err := ts.Set(entry); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := ts.Get("openai")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AccessToken != "sk-test-token-12345678" {
		t.Errorf("expected access token, got %s", got.AccessToken)
	}
}

func TestTokenStoreExpired(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTokenStore(dir)

	entry := &TokenEntry{
		Provider:    "openai",
		AccessToken: "sk-test",
		ExpiresAt:   time.Now().Add(-1 * time.Hour), // 已过期
	}

	ts.Set(entry)

	_, err := ts.Get("openai")
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestTokenStoreList(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTokenStore(dir)

	ts.Set(&TokenEntry{Provider: "openai", AccessToken: "sk-test-token-12345678"})
	ts.Set(&TokenEntry{Provider: "anthropic", AccessToken: "sk-ant-test-12345678"})

	list := ts.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(list))
	}

	// 确认脱敏
	for _, entry := range list {
		if entry.AccessToken == "sk-test-token-12345678" {
			t.Error("access token should be masked in List()")
		}
	}
}

func TestTokenStoreDelete(t *testing.T) {
	dir := t.TempDir()
	ts, _ := NewTokenStore(dir)

	ts.Set(&TokenEntry{Provider: "openai", AccessToken: "sk-test"})
	ts.Delete("openai")

	_, err := ts.Get("openai")
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestTokenEntryExpiringSoon(t *testing.T) {
	entry := &TokenEntry{
		Provider:    "openai",
		AccessToken: "sk-test",
		ExpiresAt:   time.Now().Add(3 * time.Minute), // 3 分钟后过期
	}

	if !entry.IsExpiringSoon() {
		t.Error("token expiring in 3 min should be 'expiring soon'")
	}

	entry2 := &TokenEntry{
		Provider:    "openai",
		AccessToken: "sk-test",
		ExpiresAt:   time.Now().Add(10 * time.Minute), // 10 分钟后过期
	}

	if entry2.IsExpiringSoon() {
		t.Error("token expiring in 10 min should NOT be 'expiring soon'")
	}
}

// --- Model Catalog Tests ---

func TestModelCatalogDefaults(t *testing.T) {
	catalog := NewModelCatalog()
	models := catalog.List()

	if len(models) == 0 {
		t.Fatal("expected default models in catalog")
	}

	// 检查关键模型存在
	expected := []string{"gpt-5.4-mini", "claude-sonnet-4-20250514", "llama3", "openai/gpt-5.4-mini"}
	for _, id := range expected {
		if _, err := catalog.Get(id); err != nil {
			t.Errorf("expected model %s in catalog: %v", id, err)
		}
	}
}

func TestModelCatalogGet(t *testing.T) {
	catalog := NewModelCatalog()
	m, err := catalog.Get("gpt-5.4-mini")
	if err != nil {
		t.Fatalf("Get gpt-5.4-mini: %v", err)
	}
	if m.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", m.Provider)
	}
	if m.DisplayName != "gpt-5.4-mini" {
		t.Errorf("expected display name gpt-5.4-mini, got %s", m.DisplayName)
	}
}

func TestModelCatalogGetNotFound(t *testing.T) {
	catalog := NewModelCatalog()
	_, err := catalog.Get("nonexistent-model")
	if err == nil {
		t.Error("expected error for nonexistent model")
	}
}

func TestModelCatalogListByProvider(t *testing.T) {
	catalog := NewModelCatalog()
	openaiModels := catalog.ListByProvider("openai")
	if len(openaiModels) == 0 {
		t.Error("expected OpenAI models")
	}
	for _, m := range openaiModels {
		if m.Provider != "openai" {
			t.Errorf("expected openai provider, got %s", m.Provider)
		}
	}
}

func TestModelCatalogFindByCapability(t *testing.T) {
	catalog := NewModelCatalog()
	visionModels := catalog.FindByCapability("vision")
	if len(visionModels) == 0 {
		t.Error("expected vision-capable models")
	}
	for _, m := range visionModels {
		hasVision := false
		for _, cap := range m.Capabilities {
			if cap == "vision" {
				hasVision = true
				break
			}
		}
		if !hasVision {
			t.Errorf("model %s doesn't have vision capability", m.ID)
		}
	}
}

func TestModelCatalogRegister(t *testing.T) {
	catalog := NewModelCatalog()
	custom := ModelInfo{
		ID:           "custom-model",
		Provider:     "custom",
		DisplayName:  "Custom Model",
		Capabilities: []string{"chat"},
	}
	catalog.Register(custom)

	m, err := catalog.Get("custom-model")
	if err != nil {
		t.Fatalf("Get custom-model: %v", err)
	}
	if m.Provider != "custom" {
		t.Errorf("expected provider custom, got %s", m.Provider)
	}
}

func TestModelCatalogResolveProvider(t *testing.T) {
	catalog := NewModelCatalog()

	tests := []struct {
		modelID  string
		expected string
	}{
		{"gpt-5.4-mini", "openai"},
		{"claude-3-haiku-20240307", "anthropic"},
		{"openai/gpt-5.4-mini", "openrouter"},
		{"llama3", "ollama"},
		{"gpt-5-turbo", "openai"},      // 未知但前缀匹配
		{"claude-4-opus", "anthropic"}, // 未知但前缀匹配
		{"unknown-model", "ollama"},    // 默认回退
	}

	for _, tt := range tests {
		result, err := catalog.ResolveProvider(tt.modelID)
		if err != nil {
			t.Errorf("ResolveProvider(%s): %v", tt.modelID, err)
			continue
		}
		if result != tt.expected {
			t.Errorf("ResolveProvider(%s): expected %s, got %s", tt.modelID, tt.expected, result)
		}
	}
}
