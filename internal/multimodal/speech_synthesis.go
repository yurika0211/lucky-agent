package multimodal

import (
	"context"
	"time"
)

// SpeechSynthesisRequest describes a text-to-speech request.
type SpeechSynthesisRequest struct {
	Text   string  `json:"text"`
	Model  string  `json:"model,omitempty"`
	Voice  string  `json:"voice,omitempty"`
	Format string  `json:"format,omitempty"`
	Speed  float64 `json:"speed,omitempty"`
}

// SpeechSynthesisResult captures a synthesized audio payload.
type SpeechSynthesisResult struct {
	Provider  string            `json:"provider"`
	Model     string            `json:"model"`
	Voice     string            `json:"voice,omitempty"`
	Audio     []byte            `json:"audio"`
	MimeType  string            `json:"mime_type,omitempty"`
	CreatedAt time.Time         `json:"created_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// SpeechSynthesizer describes providers that can synthesize speech.
type SpeechSynthesizer interface {
	Name() string
	SynthesizeSpeech(ctx context.Context, req SpeechSynthesisRequest) (*SpeechSynthesisResult, error)
}
