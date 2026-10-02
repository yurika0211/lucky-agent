//go:build openai

package multimodal

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// OpenAIVisionProvider implements Provider using OpenAI's vision API
type OpenAIVisionProvider struct {
	mu        sync.RWMutex
	apiKey    string
	apiBase   string
	model     string
	maxTokens int
	analyzed  int
	media     *OpenAIMediaProvider
}

// OpenAIConfig holds OpenAI provider configuration
type OpenAIConfig struct {
	APIKey    string `yaml:"api_key"`
	APIBase   string `yaml:"api_base,omitempty"`
	Model     string `yaml:"model,omitempty"`
	MaxTokens int    `yaml:"max_tokens,omitempty"`
}

// NewOpenAIVisionProvider creates a new OpenAI vision provider
func NewOpenAIVisionProvider(cfg OpenAIConfig) (*OpenAIVisionProvider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai api key is required")
	}
	if cfg.Model == "" {
		cfg.Model = "gpt-5.4-mini"
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = 4096
	}
	if cfg.APIBase == "" {
		cfg.APIBase = "https://api.openai.com/v1"
	}

	media, err := NewOpenAIMediaProvider(OpenAIMediaConfig{
		APIKey:         cfg.APIKey,
		APIBase:        cfg.APIBase,
		ResponsesModel: cfg.Model,
	})
	if err != nil {
		return nil, err
	}
	return &OpenAIVisionProvider{
		apiKey:    cfg.APIKey,
		apiBase:   cfg.APIBase,
		model:     cfg.Model,
		maxTokens: cfg.MaxTokens,
		media:     media,
	}, nil
}

// Name returns the provider name
func (o *OpenAIVisionProvider) Name() string {
	return "openai-vision"
}

// SupportedModalities returns supported modalities
func (o *OpenAIVisionProvider) SupportedModalities() []Modality {
	return []Modality{ModalityImage, ModalityText}
}

// Analyze analyzes an input using OpenAI's vision API
func (o *OpenAIVisionProvider) Analyze(ctx context.Context, input *Input) (*AnalysisResult, error) {
	o.mu.Lock()
	o.analyzed++
	o.mu.Unlock()

	result := &AnalysisResult{
		InputID:  input.ID,
		Modality: input.Modality,
	}

	switch input.Modality {
	case ModalityImage:
		if o.media == nil {
			return nil, fmt.Errorf("openai vision media client is not configured")
		}
		return o.media.Analyze(ctx, input)

	case ModalityText:
		result.Text = string(input.Data)
		result.Confidence = 1.0
		result.Labels = []string{"text"}

	default:
		return nil, fmt.Errorf("unsupported modality for OpenAI vision: %q", input.Modality)
	}

	return result, nil
}

// AnalyzeStream streams analysis result via OpenAI
func (o *OpenAIVisionProvider) AnalyzeStream(ctx context.Context, input *Input) (<-chan StreamChunk, error) {
	result, err := o.Analyze(ctx, input)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- StreamChunk{Text: result.Text, Done: false}
		ch <- StreamChunk{Text: "", Done: true}
	}()

	return ch, nil
}

// Validate checks if the provider is properly configured
func (o *OpenAIVisionProvider) Validate() error {
	if o.apiKey == "" {
		return fmt.Errorf("openai api key is required")
	}
	return nil
}

// AnalyzedCount returns the number of inputs analyzed
func (o *OpenAIVisionProvider) AnalyzedCount() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.analyzed
}

// min returns the smaller of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Ensure uuid import is used
var _ = uuid.New
