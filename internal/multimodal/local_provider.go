package multimodal

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yurika0211/luckyharness/internal/utils"
)

// LocalProvider implements Provider for local text processing
// (no external API calls — useful for text modality and testing)
type LocalProvider struct {
	mu         sync.RWMutex
	modalities []Modality
	analyzed   int
}

// NewLocalProvider creates a new local processing provider
func NewLocalProvider(modalities ...Modality) *LocalProvider {
	if len(modalities) == 0 {
		modalities = []Modality{ModalityText}
	}
	return &LocalProvider{
		modalities: modalities,
	}
}

// Name returns the provider name
func (lp *LocalProvider) Name() string {
	return "local"
}

// SupportedModalities returns supported modalities
func (lp *LocalProvider) SupportedModalities() []Modality {
	return lp.modalities
}

// Analyze processes input locally
func (lp *LocalProvider) Analyze(ctx context.Context, input *Input) (*AnalysisResult, error) {
	// Check if modality is supported
	supported := false
	for _, m := range lp.modalities {
		if m == input.Modality {
			supported = true
			break
		}
	}
	if !supported {
		return nil, fmt.Errorf("unsupported modality: %q", input.Modality)
	}

	lp.mu.Lock()
	lp.analyzed++
	lp.mu.Unlock()

	data := input.Data
	if len(data) == 0 && input.FilePath != "" {
		var err error
		data, err = os.ReadFile(input.FilePath)
		if err != nil {
			return nil, fmt.Errorf("read local input %q: %w", input.FilePath, err)
		}
	}

	result := &AnalysisResult{
		InputID:  input.ID,
		Modality: input.Modality,
	}

	switch input.Modality {
	case ModalityText:
		result.Text = string(data)
		result.Summary = utils.Truncate(string(data), 200)
		result.Confidence = 1.0
		result.Labels = []string{"text"}

	case ModalityDocument:
		result.Text = fmt.Sprintf("[Document: %s, %d bytes]", input.MimeType, len(data))
		result.Summary = fmt.Sprintf("Document file (%s, %d bytes)", input.MimeType, len(data))
		result.Labels = []string{"document", input.MimeType}
		result.Confidence = 0.4
		result.Metadata = map[string]string{
			"size":      fmt.Sprintf("%d", len(data)),
			"mime_type": input.MimeType,
		}

	case ModalityImage:
		// Local image processing: extract metadata
		result.Text = fmt.Sprintf("[Image: %s, %d bytes]", input.MimeType, len(data))
		result.Summary = fmt.Sprintf("Image file (%s, %d bytes)", input.MimeType, len(data))
		result.Labels = []string{"image", input.MimeType}
		result.Confidence = 0.5 // Low confidence without vision model
		result.Metadata = map[string]string{
			"size":      fmt.Sprintf("%d", len(data)),
			"mime_type": input.MimeType,
		}

	case ModalityAudio:
		result.Text = fmt.Sprintf("[Audio: %s, %d bytes]", input.MimeType, len(data))
		result.Summary = fmt.Sprintf("Audio file (%s, %d bytes)", input.MimeType, len(data))
		result.Labels = []string{"audio", input.MimeType}
		result.Confidence = 0.3
		result.Metadata = map[string]string{
			"size":      fmt.Sprintf("%d", len(data)),
			"mime_type": input.MimeType,
		}

	case ModalityVideo:
		result.Text = fmt.Sprintf("[Video: %s, %d bytes]", input.MimeType, len(data))
		result.Summary = fmt.Sprintf("Video file (%s, %d bytes)", input.MimeType, len(data))
		result.Labels = []string{"video", input.MimeType}
		result.Confidence = 0.3
		result.Metadata = map[string]string{
			"size":      fmt.Sprintf("%d", len(data)),
			"mime_type": input.MimeType,
		}

	default:
		return nil, fmt.Errorf("unsupported modality: %q", input.Modality)
	}

	return result, nil
}

// AnalyzeStream streams analysis result (for local provider, just sends the full text at once)
func (lp *LocalProvider) AnalyzeStream(ctx context.Context, input *Input) (<-chan StreamChunk, error) {
	result, err := lp.Analyze(ctx, input)
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
func (lp *LocalProvider) Validate() error {
	return nil // Local provider always validates
}

// AnalyzedCount returns the number of inputs analyzed
func (lp *LocalProvider) AnalyzedCount() int {
	lp.mu.RLock()
	defer lp.mu.RUnlock()
	return lp.analyzed
}

// DetectModality detects the modality of an input based on MIME type
func DetectModality(mimeType string) Modality {
	switch {
	case mimeType == "text/plain" || mimeType == "text/markdown" || mimeType == "text/html":
		return ModalityText
	case mimeType == "application/pdf":
		return ModalityDocument
	case len(mimeType) >= 5 && mimeType[:5] == "image":
		return ModalityImage
	case len(mimeType) >= 5 && mimeType[:5] == "audio":
		return ModalityAudio
	case len(mimeType) >= 5 && mimeType[:5] == "video":
		return ModalityVideo
	default:
		return ModalityText
	}
}

// NewInput creates a new Input with a generated ID
func NewInput(modality Modality, mimeType string, data []byte) *Input {
	return &Input{
		ID:        uuid.New().String(),
		Modality:  modality,
		MimeType:  mimeType,
		Data:      data,
		CreatedAt: time.Now(),
	}
}
