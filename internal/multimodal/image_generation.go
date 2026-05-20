package multimodal

import (
	"context"
	"time"
)

// ImageInput represents an input image used for image-to-image generation.
type ImageInput struct {
	Data     []byte `json:"data,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Filename string `json:"filename,omitempty"`
}

// ImageGenerationRequest describes a text-to-image or image-to-image request.
type ImageGenerationRequest struct {
	Prompt            string       `json:"prompt"`
	Model             string       `json:"model,omitempty"`
	Size              string       `json:"size,omitempty"`
	Quality           string       `json:"quality,omitempty"`
	Background        string       `json:"background,omitempty"`
	OutputFormat      string       `json:"output_format,omitempty"`
	OutputCompression int          `json:"output_compression,omitempty"`
	Count             int          `json:"count,omitempty"`
	InputImages       []ImageInput `json:"input_images,omitempty"`
}

// GeneratedImage represents a single generated image.
type GeneratedImage struct {
	Data          []byte `json:"data,omitempty"`
	MimeType      string `json:"mime_type,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// ImageGenerationResult captures the provider response for generated images.
type ImageGenerationResult struct {
	Provider      string            `json:"provider"`
	Model         string            `json:"model"`
	Images        []GeneratedImage  `json:"images"`
	CreatedAt     time.Time         `json:"created_at,omitempty"`
	RevisedPrompt string            `json:"revised_prompt,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// ImageGenerator describes providers that can generate or edit images.
type ImageGenerator interface {
	Name() string
	GenerateImage(ctx context.Context, req ImageGenerationRequest) (*ImageGenerationResult, error)
}
