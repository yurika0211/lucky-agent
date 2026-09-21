package config

import (
	"fmt"
	"strings"
)

// ModelKind identifies the runtime purpose a model is selected for.
type ModelKind string

const (
	ModelKindChat          ModelKind = "chat"
	ModelKindVision        ModelKind = "vision"
	ModelKindEmbedding     ModelKind = "embedding"
	ModelKindTranscription ModelKind = "transcription"
	ModelKindImage         ModelKind = "image"
	ModelKindTTS           ModelKind = "tts"
	ModelKindReranker      ModelKind = "reranker"
)

var modelKinds = []ModelKind{
	ModelKindChat,
	ModelKindVision,
	ModelKindEmbedding,
	ModelKindTranscription,
	ModelKindImage,
	ModelKindTTS,
	ModelKindReranker,
}

// ModelKinds returns the stable, supported model purposes.
func ModelKinds() []ModelKind {
	return append([]ModelKind(nil), modelKinds...)
}

// ParseModelKind validates a user supplied model purpose.
func ParseModelKind(value string) (ModelKind, error) {
	kind := ModelKind(strings.ToLower(strings.TrimSpace(value)))
	for _, candidate := range modelKinds {
		if candidate == kind {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown model kind %q; supported kinds: chat, vision, embedding, transcription, image, tts, reranker", value)
}

// ModelEndpointConfig stores credentials and transport settings independently
// for each model purpose. Model IDs remain in Models.Active for concise and
// backward-compatible configuration files.
type ModelEndpointConfig struct {
	Provider         string            `json:"provider,omitempty"`
	APIKey           string            `json:"api_key,omitempty"`
	APIBase          string            `json:"api_base,omitempty"`
	Protocol         string            `json:"protocol,omitempty"`
	ReasoningSummary string            `json:"reasoning_summary,omitempty"`
	ExtraHeaders     map[string]string `json:"extra_headers,omitempty"`
	TimeoutSeconds   int               `json:"timeout_seconds,omitempty"`
}

// ModelsConfig is the unified model selection section. Legacy fields are kept
// in sync so existing configurations and integrations continue to work.
type ModelsConfig struct {
	VisionMode string                            `json:"vision_mode,omitempty"` // auto (primary when capable) or external
	Active     map[ModelKind]string              `json:"active,omitempty"`
	Endpoints  map[ModelKind]ModelEndpointConfig `json:"endpoints,omitempty"`
	Profiles   map[string]map[ModelKind]string   `json:"profiles,omitempty"`
}

// ModelSelection is a resolved model reference without exposing credentials.
type ModelSelection struct {
	ID               string
	Kind             ModelKind
	Provider         string
	APIBase          string
	Protocol         string
	ReasoningSummary string
	ExtraHeaders     map[string]string
	TimeoutSeconds   int
}

func normalizeModels(cfg *Config) {
	if cfg.Models.VisionMode == "" {
		cfg.Models.VisionMode = "auto"
	}
	migrateVisionCapability(cfg)
	if cfg.Models.Active == nil {
		cfg.Models.Active = make(map[ModelKind]string, len(modelKinds))
	}
	if cfg.Models.Endpoints == nil {
		cfg.Models.Endpoints = make(map[ModelKind]ModelEndpointConfig, len(modelKinds))
	}
	if cfg.Models.Profiles == nil {
		cfg.Models.Profiles = make(map[string]map[ModelKind]string)
	}

	for _, kind := range modelKinds {
		legacy := legacyModelSelection(cfg, kind)
		if _, exists := cfg.Models.Active[kind]; !exists {
			cfg.Models.Active[kind] = legacy.ID
		}
		if _, exists := cfg.Models.Endpoints[kind]; !exists {
			endpoint := legacyEndpoint(cfg, kind)
			if kind != ModelKindChat && kind != ModelKindReranker {
				chat := cfg.Models.Endpoints[ModelKindChat]
				if endpoint.APIBase == "" {
					endpoint.APIBase = chat.APIBase
				}
				if endpoint.APIKey == "" && strings.TrimRight(endpoint.APIBase, "/") == strings.TrimRight(chat.APIBase, "/") {
					endpoint.APIKey = chat.APIKey
				}
			}
			if kind == ModelKindVision && cfg.Multimodal.ImageProvider != "" {
				endpoint.Provider = cfg.Multimodal.ImageProvider
				if endpoint.Provider == "openai-media" {
					endpoint.Provider = "openai"
				}
			}
			cfg.Models.Endpoints[kind] = endpoint
		}
	}
	cfg.Multimodal.ImageProvider = ""

	for name, profile := range cfg.Models.Profiles {
		cleanName := strings.TrimSpace(name)
		if cleanName == "" {
			delete(cfg.Models.Profiles, name)
			continue
		}
		if cleanName != name {
			delete(cfg.Models.Profiles, name)
			cfg.Models.Profiles[cleanName] = profile
		}
	}

	syncLegacyModels(cfg)
}

func mergeModelEndpoint(base, override ModelEndpointConfig) ModelEndpointConfig {
	result := base
	if value := strings.TrimSpace(override.Provider); value != "" {
		result.Provider = value
	}
	if value := strings.TrimSpace(override.APIKey); value != "" {
		result.APIKey = value
	}
	if value := strings.TrimSpace(override.APIBase); value != "" {
		result.APIBase = value
	}
	if value := strings.TrimSpace(override.Protocol); value != "" {
		result.Protocol = value
	}
	if value := strings.TrimSpace(override.ReasoningSummary); value != "" {
		result.ReasoningSummary = value
	}
	if override.ExtraHeaders != nil {
		result.ExtraHeaders = cloneStringMap(override.ExtraHeaders)
	}
	if override.TimeoutSeconds > 0 {
		result.TimeoutSeconds = override.TimeoutSeconds
	}
	return result
}

func legacyModelSelection(cfg *Config, kind ModelKind) ModelSelection {
	selection := ModelSelection{Kind: kind}
	endpoint := legacyEndpoint(cfg, kind)
	selection.Provider = endpoint.Provider
	selection.APIBase = endpoint.APIBase
	selection.Protocol = endpoint.Protocol
	selection.ReasoningSummary = endpoint.ReasoningSummary
	selection.ExtraHeaders = cloneStringMap(endpoint.ExtraHeaders)
	selection.TimeoutSeconds = endpoint.TimeoutSeconds
	switch kind {
	case ModelKindChat:
		selection.ID = cfg.LlmProvider.Model
	case ModelKindVision:
		selection.ID = cfg.Multimodal.ImageModel
	case ModelKindEmbedding:
		selection.ID = cfg.Embedding.Model
	case ModelKindTranscription:
		selection.ID = cfg.Multimodal.TranscriptionModel
	case ModelKindImage:
		selection.ID = cfg.ImageGeneration.Model
	case ModelKindTTS:
		selection.ID = cfg.TTS.Model
	}
	return selection
}

func legacyEndpoint(cfg *Config, kind ModelKind) ModelEndpointConfig {
	switch kind {
	case ModelKindChat:
		return ModelEndpointConfig{
			Provider:         cfg.LlmProvider.Name,
			APIKey:           cfg.LlmProvider.APIKey,
			APIBase:          cfg.LlmProvider.BaseURL,
			Protocol:         cfg.LlmProvider.Protocol,
			ReasoningSummary: cfg.LlmProvider.ReasoningSummary,
			ExtraHeaders:     cloneStringMap(cfg.ExtraHeaders),
		}
	case ModelKindEmbedding:
		return ModelEndpointConfig{APIKey: cfg.Embedding.APIKey, APIBase: cfg.Embedding.APIBase}
	case ModelKindVision, ModelKindTranscription:
		return ModelEndpointConfig{Provider: cfg.Multimodal.Provider, APIKey: cfg.Multimodal.APIKey, APIBase: cfg.Multimodal.APIBase}
	case ModelKindImage:
		return ModelEndpointConfig{Provider: cfg.ImageGeneration.Provider, APIKey: cfg.ImageGeneration.APIKey, APIBase: cfg.ImageGeneration.APIBase}
	case ModelKindTTS:
		return ModelEndpointConfig{Provider: cfg.TTS.Provider, APIKey: cfg.TTS.APIKey, APIBase: cfg.TTS.APIBase}
	default:
		return ModelEndpointConfig{}
	}
}

func syncLegacyModels(cfg *Config) {
	for _, kind := range modelKinds {
		id := strings.TrimSpace(cfg.Models.Active[kind])
		endpoint := cfg.Models.Endpoints[kind]
		switch kind {
		case ModelKindChat:
			cfg.LlmProvider.Model = id
			cfg.LlmProvider.Name = strings.TrimSpace(endpoint.Provider)
			cfg.LlmProvider.APIKey = endpoint.APIKey
			cfg.LlmProvider.BaseURL = endpoint.APIBase
			cfg.LlmProvider.Protocol = endpoint.Protocol
			cfg.LlmProvider.ReasoningSummary = endpoint.ReasoningSummary
			cfg.Provider = cfg.LlmProvider.Name
			cfg.APIKey = cfg.LlmProvider.APIKey
			cfg.APIBase = cfg.LlmProvider.BaseURL
			cfg.Model = cfg.LlmProvider.Model
			cfg.ExtraHeaders = cloneStringMap(endpoint.ExtraHeaders)
		case ModelKindVision:
			cfg.Multimodal.ImageModel = id
			cfg.Multimodal.Provider = endpoint.Provider
			cfg.Multimodal.APIKey = endpoint.APIKey
			cfg.Multimodal.APIBase = endpoint.APIBase
		case ModelKindEmbedding:
			cfg.Embedding.Model = id
			cfg.Embedding.APIKey = endpoint.APIKey
			cfg.Embedding.APIBase = endpoint.APIBase
		case ModelKindTranscription:
			cfg.Multimodal.TranscriptionModel = id
		case ModelKindImage:
			cfg.ImageGeneration.Model = id
			cfg.ImageGeneration.Provider = endpoint.Provider
			cfg.ImageGeneration.APIKey = endpoint.APIKey
			cfg.ImageGeneration.APIBase = endpoint.APIBase
		case ModelKindTTS:
			cfg.TTS.Model = id
			cfg.TTS.Provider = endpoint.Provider
			cfg.TTS.APIKey = endpoint.APIKey
			cfg.TTS.APIBase = endpoint.APIBase
		}
	}
}

// ModelSelection returns the resolved selection for one model purpose.
func (c *Config) ModelSelection(kind ModelKind) (ModelSelection, bool) {
	if c == nil {
		return ModelSelection{}, false
	}
	if _, err := ParseModelKind(string(kind)); err != nil {
		return ModelSelection{}, false
	}
	selection := legacyModelSelection(c, kind)
	if c.Models.Active != nil {
		if id, exists := c.Models.Active[kind]; exists {
			selection.ID = strings.TrimSpace(id)
		}
	}
	if c.Models.Endpoints != nil {
		endpoint := c.ModelEndpoint(kind)
		selection.Provider = endpoint.Provider
		selection.APIBase = endpoint.APIBase
		selection.Protocol = endpoint.Protocol
		selection.ReasoningSummary = endpoint.ReasoningSummary
		selection.ExtraHeaders = cloneStringMap(endpoint.ExtraHeaders)
		selection.TimeoutSeconds = endpoint.TimeoutSeconds
	}
	return selection, strings.TrimSpace(selection.ID) != ""
}

// SetModelSelection updates both the unified selection and the legacy field
// used by the corresponding runtime component.
func (c *Config) SetModelSelection(kind ModelKind, modelID string, endpoint ModelEndpointConfig) error {
	if c == nil {
		return fmt.Errorf("configuration is nil")
	}
	if _, err := ParseModelKind(string(kind)); err != nil {
		return err
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("model id is required")
	}
	if c.Models.Active == nil {
		c.Models.Active = make(map[ModelKind]string)
	}
	if c.Models.Endpoints == nil {
		c.Models.Endpoints = make(map[ModelKind]ModelEndpointConfig)
	}
	c.Models.Active[kind] = modelID
	c.Models.Endpoints[kind] = mergeModelEndpoint(c.ModelEndpoint(kind), endpoint)
	syncLegacyModels(c)
	return nil
}

// ModelEndpoint resolves a purpose independently. An explicit endpoint, including
// empty credentials, must never inherit another purpose's legacy credentials.
func (c *Config) ModelEndpoint(kind ModelKind) ModelEndpointConfig {
	if endpoint, ok := c.Models.Endpoints[kind]; ok {
		endpoint.ExtraHeaders = cloneStringMap(endpoint.ExtraHeaders)
		return endpoint
	}
	return legacyEndpoint(c, kind)
}

func migrateVisionCapability(c *Config) {
	if !c.LlmProvider.Vision {
		return
	}
	id := strings.TrimSpace(c.LlmProvider.Model)
	c.LlmProvider.Vision = false
	for i := range c.CustomModels {
		if c.CustomModels[i].ID == id {
			// An explicit model capability declaration takes precedence.
			return
		}
	}
	if id != "" {
		c.CustomModels = append(c.CustomModels, CustomModelInfo{ID: id, Provider: c.LlmProvider.Name,
			Capabilities: []string{"chat", "streaming", "tools", "vision"}})
	}
}

func validateModelConfig(c *Config) error {
	switch c.Models.VisionMode {
	case "", "auto", "external":
	default:
		return fmt.Errorf("models.vision_mode must be auto or external")
	}
	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
