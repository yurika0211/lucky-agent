package provider

import (
	"context"
	"fmt"

	"github.com/yurika0211/luckyharness/internal/config"
)

type ContentPart struct {
	Type  string     `json:"type"`
	Text  string     `json:"text,omitempty"`
	Image *ImagePart `json:"image,omitempty"`
}

type ImagePart struct {
	URL      string `json:"url,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Message 代表一条对话消息
type Message struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ContentParts     []ContentPart `json:"content_parts,omitempty"`

	ToolCallID string     `json:"tool_call_id,omitempty"` // v0.16.0: function calling tool result
	Name       string     `json:"name,omitempty"`         // v0.16.0: function name for tool messages
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // v0.16.0: assistant tool calls
}

// Response 代表 Provider 的响应
type Response struct {
	Content          string
	ReasoningContent string
	TokensUsed       int
	Usage            *UsageDetails
	Model            string
	FinishReason     string
	ToolCalls        []ToolCall
}

// StreamChunk 代表流式响应的一个片段
type StreamChunk struct {
	Content          string
	ReasoningContent string
	Done             bool
	FinishReason     string
	Model            string
	Usage            *UsageDetails
	ToolCallDeltas   []StreamToolCallDelta // v0.40.0: 流式 tool_calls 增量
}

// UsageDetails carries provider-reported token usage, including cache-related fields when available.
type UsageDetails struct {
	PromptTokens          int `json:"prompt_tokens,omitempty"`
	CompletionTokens      int `json:"completion_tokens,omitempty"`
	TotalTokens           int `json:"total_tokens,omitempty"`
	InputTokens           int `json:"input_tokens,omitempty"`
	OutputTokens          int `json:"output_tokens,omitempty"`
	CachedPromptTokens    int `json:"cached_prompt_tokens,omitempty"`
	CacheCreation5MTokens int `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1HTokens int `json:"cache_creation_1h_tokens,omitempty"`
}

// StreamToolCallDelta 流式 tool_calls 的增量片段
type StreamToolCallDelta struct {
	Index     int    // tool_call 的索引
	ID        string // tool_call ID（仅首个 chunk 携带）
	Name      string // 函数名（仅首个 chunk 携带）
	Arguments string // 参数增量（逐 chunk 拼接）
}

// ToolCall 代表一次工具调用
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Provider 是 LLM 提供商的统一接口
type Provider interface {
	// Name 返回提供商名称
	Name() string

	// Chat 发送消息并获取完整响应
	Chat(ctx context.Context, messages []Message) (*Response, error)

	// ChatStream 发送消息并获取流式响应
	ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error)

	// Validate 验证配置是否有效
	Validate() error
}

type EmbeddingProvider struct {
	APIKey    string `json:"api_key"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	Dimension int    `json:"dimension"`
}

type LlmProvider struct {
	Name        string  `json:"name"`
	APIKey      string  `json:"api_key"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	Dimension   int     `json:"dimension"`
	Temperature float64 `json:"temperature"`
}

// Config 是 Provider 的配置
type Config struct {
	LlmProvider       LlmProvider       `json:"llm_provider"`
	EmbeddingProvider EmbeddingProvider `json:"embedding_provider"`
	ExtraHeaders      map[string]string `json:"extra_headers,omitempty" yaml:"extra_headers,omitempty"`

	// v0.56.0: 限制参数从配置文件加载
	Limits         LimitsConfig         `json:"limits,omitempty"`
	Retry          RetryConfig          `json:"retry,omitempty"`
	CircuitBreaker CircuitBreakerConfig `json:"circuit_breaker,omitempty"`
	RateLimit      RateLimitConfig      `json:"rate_limit,omitempty"`
	Context        ContextConfig        `json:"context,omitempty"`
}

// Reuse config package types to avoid duplicated schema drift.
type (
	LimitsConfig         = config.LimitsConfig
	RetryConfig          = config.RetryConfig
	CircuitBreakerConfig = config.CircuitBreakerConfig
	RateLimitConfig      = config.RateLimitConfig
	ContextConfig        = config.ContextConfig
)

// Registry 管理所有已注册的 Provider
type Registry struct {
	providers map[string]Provider
	factories map[string]func(Config) Provider
}

// NewRegistry 创建 Provider 注册表
func NewRegistry() *Registry {
	r := &Registry{
		providers: make(map[string]Provider),
		factories: make(map[string]func(Config) Provider),
	}
	r.RegisterFactory("openai", NewOpenAIProvider)
	r.RegisterFactory("openai-compatible", NewOpenAICompatibleProvider)
	r.RegisterFactory("anthropic", NewAnthropicProvider)
	r.RegisterFactory("ollama", NewOllamaProvider)
	r.RegisterFactory("openrouter", NewOpenRouterProvider)
	return r
}

func (r *Registry) RegisterFactory(name string, factory func(Config) Provider) {
	r.factories[name] = factory
}

func (r *Registry) Create(name string, cfg Config) (Provider, error) {
	factory, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider: %s (available: %v)", name, r.Available())
	}
	p := factory(cfg)
	r.providers[name] = p
	return p, nil
}

func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("provider not created: %s", name)
	}
	return p, nil
}

func (r *Registry) Available() []string {
	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}

func (r *Registry) Resolve(cfg Config) (Provider, error) {
	name := cfg.LlmProvider.Name
	if name == "" {
		name = "openai"
	}
	if p, ok := r.providers[name]; ok {
		return p, nil
	}
	return r.Create(name, cfg)
}

func (r *Registry) Close() error {
	return nil
}

// --- OpenAI Provider ---

type openAIBaseProvider struct {
	cfg Config
}

func newOpenAIBaseProvider(cfg Config) openAIBaseProvider {
	if cfg.LlmProvider.BaseURL == "" {
		cfg.LlmProvider.BaseURL = "https://api.openai.com/v1"
	}
	if cfg.LlmProvider.Model == "" {
		cfg.LlmProvider.Model = "gpt-5.4-mini"
	}
	return openAIBaseProvider{cfg: cfg}
}

func (p *openAIBaseProvider) Chat(ctx context.Context, messages []Message) (*Response, error) {
	return callOpenAI(ctx, p.cfg, messages, CallOptions{})
}

func (p *openAIBaseProvider) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	return callOpenAIStream(ctx, p.cfg, messages, CallOptions{})
}

// ChatWithOptions 发送消息（支持 function calling）
func (p *openAIBaseProvider) ChatWithOptions(ctx context.Context, messages []Message, opts CallOptions) (*Response, error) {
	return callOpenAI(ctx, p.cfg, messages, opts)
}

// ChatStreamWithOptions 发送消息流式（支持 function calling）
func (p *openAIBaseProvider) ChatStreamWithOptions(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	return callOpenAIStream(ctx, p.cfg, messages, opts)
}

type OpenAIProvider struct {
	openAIBaseProvider
}

func NewOpenAIProvider(cfg Config) Provider {
	return &OpenAIProvider{
		openAIBaseProvider: newOpenAIBaseProvider(cfg),
	}
}

func (p *OpenAIProvider) Name() string { return "openai" }

func (p *OpenAIProvider) Validate() error {
	if p.cfg.LlmProvider.APIKey == "" {
		return fmt.Errorf("openai: api_key is required")
	}
	return nil
}

// --- OpenAI-Compatible Provider ---

type OpenAICompatibleProvider struct {
	openAIBaseProvider
}

func NewOpenAICompatibleProvider(cfg Config) Provider {
	return &OpenAICompatibleProvider{
		openAIBaseProvider: newOpenAIBaseProvider(cfg),
	}
}

func (p *OpenAICompatibleProvider) Name() string { return "openai-compatible" }

func (p *OpenAICompatibleProvider) Validate() error {
	if p.cfg.LlmProvider.APIKey == "" {
		return fmt.Errorf("%s: api_key is required", p.cfg.LlmProvider.Name)
	}
	if p.cfg.LlmProvider.BaseURL == "" {
		return fmt.Errorf("%s: api_base is required", p.cfg.LlmProvider.BaseURL)
	}
	return nil
}

// Ensure interfaces are satisfied
var (
	_ Provider                = (*OpenAIProvider)(nil)
	_ FunctionCallingProvider = (*OpenAIProvider)(nil)
	_ Provider                = (*OpenAICompatibleProvider)(nil)
	_ FunctionCallingProvider = (*OpenAICompatibleProvider)(nil)
)
