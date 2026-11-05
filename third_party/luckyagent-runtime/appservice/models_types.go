package appservice

// ModelKind identifies the purpose of a model selection.
type ModelKind string

const (
	ModelKindChat          ModelKind = "chat"
	ModelKindVision        ModelKind = "vision"
	ModelKindEmbedding     ModelKind = "embedding"
	ModelKindTranscription ModelKind = "transcription"
	ModelKindImage         ModelKind = "image"
	ModelKindTTS           ModelKind = "tts"
	ModelKindReranker      ModelKind = "reranker"
	ModelKindCompact       ModelKind = "compact"
)

// ModelRef describes a model without its credentials.
type ModelRef struct {
	ID           string    `json:"id"`
	Kind         ModelKind `json:"kind"`
	Provider     string    `json:"provider,omitempty"`
	DisplayName  string    `json:"display_name,omitempty"`
	APIBase      string    `json:"api_base,omitempty"`
	Protocol     string    `json:"protocol,omitempty"`
	Capabilities []string  `json:"capabilities,omitempty"`
	Current      bool      `json:"current"`
}

// SwitchModelOptions leaves persistence and session pinning policy to callers.
type SwitchModelOptions struct {
	Pin      bool
	Persist  bool
	Provider string
}
