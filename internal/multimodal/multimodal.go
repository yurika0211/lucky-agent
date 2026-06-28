// Package multimodal provides multi-modal input processing for LuckyAgent,
// supporting image, audio, and video understanding through pluggable providers.
package multimodal

import (
	"context"
	"fmt"
	"io"
	"time"
)

// Modality represents the type of input content
type Modality string

const (
	ModalityText     Modality = "text"
	ModalityImage    Modality = "image"
	ModalityAudio    Modality = "audio"
	ModalityVideo    Modality = "video"
	ModalityDocument Modality = "document"
)

// Input represents a multi-modal input item
type Input struct {
	ID        string            `json:"id"`
	Modality  Modality          `json:"modality"`
	MimeType  string            `json:"mime_type,omitempty"`
	Data      []byte            `json:"data,omitempty"`
	URL       string            `json:"url,omitempty"`
	FilePath  string            `json:"file_path,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

// AnalysisResult represents the result of analyzing a multi-modal input
type AnalysisResult struct {
	InputID    string            `json:"input_id"`
	Modality   Modality          `json:"modality"`
	Text       string            `json:"text"`                 // Extracted/understood text
	Summary    string            `json:"summary,omitempty"`    // Brief summary
	Labels     []string          `json:"labels,omitempty"`     // Classification labels
	Confidence float64           `json:"confidence,omitempty"` // Confidence score 0-1
	Metadata   map[string]string `json:"metadata,omitempty"`
	Duration   time.Duration     `json:"duration,omitempty"` // Processing duration
	Error      string            `json:"error,omitempty"`
}

// Provider is the interface for multi-modal analysis providers
type Provider interface {
	// Name returns the provider name
	Name() string

	// SupportedModalities returns the list of modalities this provider supports
	SupportedModalities() []Modality

	// Analyze analyzes a multi-modal input and returns the result
	Analyze(ctx context.Context, input *Input) (*AnalysisResult, error)

	// AnalyzeStream analyzes a multi-modal input and streams the text result
	AnalyzeStream(ctx context.Context, input *Input) (<-chan StreamChunk, error)

	// Validate checks if the provider is properly configured
	Validate() error
}

// StreamChunk represents a chunk of streamed analysis result
type StreamChunk struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// Processor manages multi-modal input processing with provider routing
type Processor struct {
	providers map[Modality][]Provider
	defaults  map[Modality]Provider
}

// NewProcessor creates a new multi-modal processor
func NewProcessor() *Processor {
	return &Processor{
		providers: make(map[Modality][]Provider),
		defaults:  make(map[Modality]Provider),
	}
}

// RegisterProvider registers a provider for one or more modalities
func (p *Processor) RegisterProvider(provider Provider, isDefault bool, modalities ...Modality) error {
	if len(modalities) == 0 {
		modalities = provider.SupportedModalities()
	}

	for _, m := range modalities {
		p.providers[m] = append(p.providers[m], provider)
		if isDefault || p.defaults[m] == nil {
			p.defaults[m] = provider
		}
	}

	return nil
}

// Analyze analyzes a multi-modal input using the default provider for its modality
func (p *Processor) Analyze(ctx context.Context, input *Input) (*AnalysisResult, error) {
	provider, err := p.getProvider(input.Modality)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result, err := provider.Analyze(ctx, input)
	if err != nil {
		return &AnalysisResult{
			InputID:  input.ID,
			Modality: input.Modality,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, err
	}

	result.Duration = time.Since(start)
	return result, nil
}

// AnalyzeStream analyzes a multi-modal input with streaming
func (p *Processor) AnalyzeStream(ctx context.Context, input *Input) (<-chan StreamChunk, error) {
	provider, err := p.getProvider(input.Modality)
	if err != nil {
		return nil, err
	}

	return provider.AnalyzeStream(ctx, input)
}

// AnalyzeWithProvider analyzes using a specific named provider
func (p *Processor) AnalyzeWithProvider(ctx context.Context, providerName string, input *Input) (*AnalysisResult, error) {
	provider, err := p.getProviderByName(input.Modality, providerName)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result, err := provider.Analyze(ctx, input)
	if err != nil {
		return &AnalysisResult{
			InputID:  input.ID,
			Modality: input.Modality,
			Error:    err.Error(),
			Duration: time.Since(start),
		}, err
	}

	result.Duration = time.Since(start)
	return result, nil
}

// SupportedModalities returns all registered modalities
func (p *Processor) SupportedModalities() []Modality {
	var result []Modality
	for m := range p.providers {
		result = append(result, m)
	}
	return result
}

// ProvidersForModality returns all providers for a given modality
func (p *Processor) ProvidersForModality(modality Modality) []Provider {
	return p.providers[modality]
}

// NewInputFromReader creates an Input from an io.Reader
func NewInputFromReader(modality Modality, mimeType string, reader io.Reader) (*Input, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}

	return &Input{
		Modality:  modality,
		MimeType:  mimeType,
		Data:      data,
		CreatedAt: time.Now(),
	}, nil
}

// NewInputFromURL creates an Input from a URL
func NewInputFromURL(modality Modality, url string) *Input {
	return &Input{
		Modality:  modality,
		URL:       url,
		CreatedAt: time.Now(),
	}
}

// NewInputFromPath creates an Input from a file path
func NewInputFromPath(modality Modality, filePath string) *Input {
	return &Input{
		Modality:  modality,
		FilePath:  filePath,
		CreatedAt: time.Now(),
	}
}

// getProvider returns the default provider for a modality
func (p *Processor) getProvider(modality Modality) (Provider, error) {
	if prov, ok := p.defaults[modality]; ok {
		return prov, nil
	}
	return nil, fmt.Errorf("no provider registered for modality %q", modality)
}

// getProviderByName returns a specific provider by name for a modality
func (p *Processor) getProviderByName(modality Modality, name string) (Provider, error) {
	providers, ok := p.providers[modality]
	if !ok {
		return nil, fmt.Errorf("no providers registered for modality %q", modality)
	}

	for _, prov := range providers {
		if prov.Name() == name {
			return prov, nil
		}
	}

	return nil, fmt.Errorf("provider %q not found for modality %q", name, modality)
}
