package provider

import (
	"testing"
)

func TestRegistryResolve(t *testing.T) {
	r := NewRegistry()

	cfg := Config{
		LlmProvider: LlmProvider{
			Name:   "openai",
			APIKey: "test-key",
			Model:  "gpt-5.4-mini",
		},
	}

	p, err := r.Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("expected openai, got %s", p.Name())
	}

	// Resolve again should return same instance
	p2, err := r.Resolve(cfg)
	if err != nil {
		t.Fatalf("Resolve2: %v", err)
	}
	if p2 != p {
		t.Error("expected same instance on re-resolve")
	}
}

func TestRegistryAvailable(t *testing.T) {
	r := NewRegistry()
	available := r.Available()
	if len(available) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(available))
	}
}

func TestRegistryUnknown(t *testing.T) {
	r := NewRegistry()
	cfg := Config{LlmProvider: LlmProvider{Name: "nonexistent"}}
	_, err := r.Create("nonexistent", cfg)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestOpenAIProviderValidate(t *testing.T) {
	p := NewOpenAIProvider(Config{})
	if err := p.Validate(); err == nil {
		t.Error("expected error for missing api_key")
	}

	p2 := NewOpenAIProvider(Config{LlmProvider: LlmProvider{APIKey: "sk-test"}})
	if err := p2.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestOpenAICompatibleProviderValidate(t *testing.T) {
	p := NewOpenAICompatibleProvider(Config{LlmProvider: LlmProvider{Name: "test"}})
	if err := p.Validate(); err == nil {
		t.Error("expected error for missing api_key")
	}

	p2 := NewOpenAICompatibleProvider(Config{LlmProvider: LlmProvider{Name: "test", APIKey: "sk-test", BaseURL: "http://localhost:8080/v1"}})
	if err := p2.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestToOpenAIMessages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hello"},
	}
	result, err := toOpenAIMessages(msgs, "")
	if err != nil {
		t.Fatalf("toOpenAIMessages() error = %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "system" {
		t.Errorf("expected system, got %s", result[0].Role)
	}
	if result[1].Content != "Hello" {
		t.Errorf("expected Hello, got %s", result[1].Content)
	}
}

func TestProviderDefaults(t *testing.T) {
	p := NewOpenAIProvider(Config{})
	cfg := p.(*OpenAIProvider).cfg
	if cfg.LlmProvider.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default APIBase, got %s", cfg.LlmProvider.BaseURL)
	}
	if cfg.LlmProvider.Model != "gpt-5.4-mini" {
		t.Errorf("expected default model gpt-5.4-mini, got %s", cfg.LlmProvider.Model)
	}
}

func TestProviderDefaults_WithAPIKeyStillGetsDefaultBaseURL(t *testing.T) {
	p := NewOpenAIProvider(Config{LlmProvider: LlmProvider{APIKey: "sk-test"}})
	cfg := p.(*OpenAIProvider).cfg
	if cfg.LlmProvider.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("expected default APIBase with api_key set, got %s", cfg.LlmProvider.BaseURL)
	}
}
