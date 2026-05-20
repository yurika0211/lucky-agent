package provider

import (
	"context"
	"fmt"
)

// --- OpenRouter Provider ---

// OpenRouterProvider 实现 OpenRouter API 调用
// OpenRouter 兼容 OpenAI API 格式，但有自己的 base URL 和 header
type OpenRouterProvider struct {
	cfg Config
}

func NewOpenRouterProvider(cfg Config) Provider {
	if cfg.LlmProvider.BaseURL == "" {
		cfg.LlmProvider.BaseURL = "https://openrouter.ai/api/v1"
	}
	if cfg.LlmProvider.Model == "" {
		cfg.LlmProvider.Model = "openai/gpt-5.4-mini"
	}
	return &OpenRouterProvider{cfg: cfg}
}

func (p *OpenRouterProvider) Name() string { return "openrouter" }

func (p *OpenRouterProvider) Validate() error {
	if p.cfg.LlmProvider.APIKey == "" {
		return fmt.Errorf("openrouter: api_key is required")
	}
	return nil
}

func (p *OpenRouterProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	// OpenRouter 兼容 OpenAI API，复用 callOpenAI
	return callOpenAI(ctx, p.cfg, messages, CallOptions{})
}

func (p *OpenRouterProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	return callOpenAIStream(ctx, p.cfg, messages, CallOptions{})
}

// ChatWithOptions 发送消息（支持 function calling）
func (p *OpenRouterProvider) ChatWithOptions(ctx context.Context, messages []Message, opts CallOptions) (*Response, error) {
	return callOpenAI(ctx, p.cfg, messages, opts)
}

// ChatStreamWithOptions 发送消息流式（支持 function calling）
func (p *OpenRouterProvider) ChatStreamWithOptions(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	return callOpenAIStream(ctx, p.cfg, messages, opts)
}

// Ensure OpenRouterProvider implements Provider and FunctionCallingProvider
var (
	_ Provider                = (*OpenRouterProvider)(nil)
	_ FunctionCallingProvider = (*OpenRouterProvider)(nil)
)
