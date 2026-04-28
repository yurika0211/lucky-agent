package embedder

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

var jsonAPI = jsoniter.ConfigCompatibleWithStandardLibrary

// MockEmbedder is a deterministic hash-based embedder for testing.
type MockEmbedder struct {
	dim   int
	model string
}

// NewMockEmbedder creates a mock embedder with the given dimension.
func NewMockEmbedder(dim int) *MockEmbedder {
	if dim <= 0 {
		dim = 128
	}
	return &MockEmbedder{dim: dim, model: "mock-embedding"}
}

// NewMockEmbedderWithModel creates a mock embedder with a custom model name.
func NewMockEmbedderWithModel(dim int, model string) *MockEmbedder {
	if dim <= 0 {
		dim = 128
	}
	return &MockEmbedder{dim: dim, model: model}
}

func (m *MockEmbedder) Name() string   { return "mock" }
func (m *MockEmbedder) Model() string  { return m.model }
func (m *MockEmbedder) Dimension() int { return m.dim }

func (m *MockEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	return mockVector(text, m.dim), nil
}

func (m *MockEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	result := make([][]float64, len(texts))
	for i, t := range texts {
		result[i] = mockVector(t, m.dim)
	}
	return result, nil
}

// mockVector generates a deterministic pseudo-random vector from text.
func mockVector(text string, dim int) []float64 {
	vec := make([]float64, dim)
	if len(text) == 0 {
		return vec
	}
	for i, ch := range text {
		idx := i % dim
		vec[idx] += float64(ch)
	}
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	norm = math.Sqrt(norm)
	if norm > 0 {
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}

// OpenAIEmbedder calls OpenAI-compatible embedding APIs.
// Supports text-embedding-3-small, text-embedding-3-large, text-embedding-ada-002,
// and any OpenAI-compatible endpoint (e.g. Azure, local proxies).
type OpenAIEmbedder struct {
	apiKey    string
	model     string
	baseURL   string
	dimension int
	client    *http.Client
}

// OpenAIEmbedderConfig configures an OpenAI embedder.
type OpenAIEmbedderConfig struct {
	APIKey    string
	Model     string // defaults to "text-embedding-3-small"
	BaseURL   string // defaults to "https://api.openai.com/v1"
	Dimension int    // defaults to model's native dimension
}

type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
	Object    string    `json:"object"`
}

type embeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type embeddingResponse struct {
	Data   []embeddingData `json:"data"`
	Model  string          `json:"model"`
	Object string          `json:"object"`
	Usage  embeddingUsage  `json:"usage"`
}

// NewOpenAIEmbedder creates an OpenAI embedder.
func NewOpenAIEmbedder(cfg OpenAIEmbedderConfig) *OpenAIEmbedder {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	model := cfg.Model
	if model == "" {
		model = "text-embedding-3-small"
	}
	dim := cfg.Dimension
	if dim <= 0 {
		dim = openAIDefaultDim(model)
	}
	return &OpenAIEmbedder{
		apiKey:    cfg.APIKey,
		model:     model,
		baseURL:   baseURL,
		dimension: dim,
	}
}

func openAIDefaultDim(model string) int {
	switch model {
	case "text-embedding-3-small":
		return 1536
	case "text-embedding-3-large":
		return 3072
	case "text-embedding-ada-002":
		return 1536
	default:
		return 1536
	}
}

func (o *OpenAIEmbedder) Name() string   { return "openai" }
func (o *OpenAIEmbedder) Model() string  { return o.model }
func (o *OpenAIEmbedder) Dimension() int { return o.dimension }

func (o *OpenAIEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	vecs, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return vecs[0], nil
}

// EmbedBatch embeds a batch of texts using the OpenAI API.
func (o *OpenAIEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	modelName := strings.TrimSpace(o.model)
	envModelName := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_NAME"))
	if envModelName != "" && (modelName == "" || modelName == "text-embedding-3-small") {
		modelName = envModelName
	} else if modelName == "" {
		modelName = envModelName
	}
	if modelName == "" {
		modelName = "text-embedding-3-small"
	}

	apiKey := strings.TrimSpace(o.apiKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_KEY"))
	}

	baseURL := strings.TrimSpace(o.baseURL)
	if baseURL == "" || baseURL == "https://api.openai.com/v1" {
		if envURL := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL_URL")); envURL != "" {
			baseURL = envURL
		}
	}

	if o.dimension <= 0 {
		o.dimension = openAIDefaultDim(modelName)
	}
	if o.dimension <= 0 {
		return nil, fmt.Errorf("unsupported model: %s", modelName)
	}

	o.model = modelName
	o.apiKey = apiKey
	o.baseURL = baseURL

	if apiKey == "" || baseURL == "" {
		mock := NewMockEmbedder(o.dimension)
		return mock.EmbedBatch(ctx, texts)
	}

	reqBody := embeddingRequest{
		Model: modelName,
		Input: texts,
	}

	data, err := jsonAPI.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := o.client
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var respBody embeddingResponse
	if err := jsonAPI.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		return nil, fmt.Errorf("failed to decode response body: %w", err)
	}

	result := make([][]float64, len(respBody.Data))
	for _, item := range respBody.Data {
		if item.Index < 0 || item.Index >= len(result) {
			return nil, fmt.Errorf("embedding index out of range: %d", item.Index)
		}
		result[item.Index] = item.Embedding
	}

	return result, nil
}

// OllamaEmbedder calls Ollama's embedding API.
// Supports models like nomic-embed-text, mxbai-embed-large, etc.
type OllamaEmbedder struct {
	baseURL   string
	model     string
	dimension int
}

// OllamaEmbedderConfig configures an Ollama embedder.
type OllamaEmbedderConfig struct {
	BaseURL   string // defaults to "http://localhost:11434"
	Model     string // defaults to "nomic-embed-text"
	Dimension int    // defaults to model's native dimension
}

// NewOllamaEmbedder creates an Ollama embedder.
func NewOllamaEmbedder(cfg OllamaEmbedderConfig) *OllamaEmbedder {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	model := cfg.Model
	if model == "" {
		model = "nomic-embed-text"
	}
	dim := cfg.Dimension
	if dim <= 0 {
		dim = ollamaDefaultDim(model)
	}
	return &OllamaEmbedder{
		baseURL:   baseURL,
		model:     model,
		dimension: dim,
	}
}

func ollamaDefaultDim(model string) int {
	switch model {
	case "nomic-embed-text":
		return 768
	case "mxbai-embed-large":
		return 1024
	case "all-minilm":
		return 384
	default:
		return 768
	}
}

func (o *OllamaEmbedder) Name() string   { return "ollama" }
func (o *OllamaEmbedder) Model() string  { return o.model }
func (o *OllamaEmbedder) Dimension() int { return o.dimension }

func (o *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	vecs, err := o.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return vecs[0], nil
}

func (o *OllamaEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	// TODO: implement real HTTP call to Ollama /api/embeddings
	// For now, fall back to mock vectors.
	mock := NewMockEmbedder(o.dimension)
	return mock.EmbedBatch(ctx, texts)
}
