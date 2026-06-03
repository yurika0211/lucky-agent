package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/contextx"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	"github.com/yurika0211/luckyharness/internal/provider"
)

type namedAttachmentProvider struct{}

func (namedAttachmentProvider) Name() string { return "named-attachment-provider" }
func (namedAttachmentProvider) SupportedModalities() []multimodal.Modality {
	return []multimodal.Modality{multimodal.ModalityImage}
}
func (namedAttachmentProvider) Analyze(ctx context.Context, input *multimodal.Input) (*multimodal.AnalysisResult, error) {
	return &multimodal.AnalysisResult{
		InputID:    input.ID,
		Modality:   input.Modality,
		Text:       "attachment provider text",
		Summary:    "attachment provider summary",
		Confidence: 0.91,
		Metadata: map[string]string{
			"source": "named-attachment-provider",
		},
	}, nil
}
func (namedAttachmentProvider) AnalyzeStream(ctx context.Context, input *multimodal.Input) (<-chan multimodal.StreamChunk, error) {
	ch := make(chan multimodal.StreamChunk, 1)
	close(ch)
	return ch, nil
}
func (namedAttachmentProvider) Validate() error { return nil }

func TestAnalyzeAttachmentsUsesMediaProcessor(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityImage,
		multimodal.ModalityAudio,
		multimodal.ModalityDocument,
	), true); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	a := &Agent{mediaProcessor: processor}
	attachments := []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FileName: "photo.jpg",
			MimeType: "image/jpeg",
			Data:     []byte("fake-image"),
		},
		{
			Type:     gateway.AttachmentDocument,
			FileName: "report.pdf",
			MimeType: "application/pdf",
			Data:     []byte("%PDF-1.4"),
		},
	}

	out, err := a.AnalyzeAttachments(context.Background(), attachments)
	if err != nil {
		t.Fatalf("AnalyzeAttachments error: %v", err)
	}
	if !strings.Contains(out, "[Multimodal Analysis]") {
		t.Fatalf("expected multimodal header, got %q", out)
	}
	if !strings.Contains(out, "Image: photo.jpg") {
		t.Fatalf("expected image section, got %q", out)
	}
	if !strings.Contains(out, "Document: report.pdf") {
		t.Fatalf("expected document section, got %q", out)
	}
}

func TestAnalyzeAttachmentsUsesDownloadedFilePath(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityDocument,
	), true); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "report.pdf")
	if err := os.WriteFile(filePath, []byte("%PDF-1.4 file on disk"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	a := &Agent{mediaProcessor: processor}
	attachments := []gateway.Attachment{
		{
			Type:     gateway.AttachmentDocument,
			FileName: "report.pdf",
			FilePath: filePath,
			MimeType: "application/pdf",
		},
	}

	out, err := a.AnalyzeAttachments(context.Background(), attachments)
	if err != nil {
		t.Fatalf("AnalyzeAttachments error: %v", err)
	}
	if !strings.Contains(out, "Document: report.pdf") {
		t.Fatalf("expected document section, got %q", out)
	}
	if !strings.Contains(out, "Document file (application/pdf") {
		t.Fatalf("expected local provider output from file path, got %q", out)
	}
}

func TestAnalyzeAttachmentsUsesConfiguredProvider(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityImage,
	), true); err != nil {
		t.Fatalf("register local provider: %v", err)
	}
	if err := processor.RegisterProvider(namedAttachmentProvider{}, false); err != nil {
		t.Fatalf("register named provider: %v", err)
	}

	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	if err := cfg.Set("multimodal.image_provider", "named-attachment-provider"); err != nil {
		t.Fatalf("set multimodal.image_provider: %v", err)
	}

	a := &Agent{
		cfg:            cfg,
		mediaProcessor: processor,
	}
	attachments := []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FileName: "screen.png",
			MimeType: "image/png",
			Data:     []byte("fake-image"),
		},
	}

	out, err := a.AnalyzeAttachments(context.Background(), attachments)
	if err != nil {
		t.Fatalf("AnalyzeAttachments error: %v", err)
	}
	if !strings.Contains(out, "attachment provider summary") {
		t.Fatalf("expected configured provider output, got %q", out)
	}
}

func TestContextPlannerDropsImagePartsForNonVisionModel(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(namedAttachmentProvider{}, true); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	a := &Agent{
		catalog:        provider.NewModelCatalog(),
		contextWin:     contextx.NewContextWindow(contextx.DefaultWindowConfig()),
		contextEst:     contextx.NewTokenEstimator(4096),
		mediaProcessor: processor,
		activeModel:    "deepseek-v4-flash",
	}
	planner := newContextPlanner(a, contextBuildOptions{
		IncludeRAG:     false,
		IncludeHistory: false,
	})

	input := MultimodalUserTurnInput("describe it", []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FileName: "screen.png",
			MimeType: "image/png",
			Data:     []byte("fake-image"),
		},
	})

	messages := planner.BuildInput(context.Background(), nil, input)
	if !messagesContainText(messages, "attachment provider summary") {
		t.Fatalf("expected multimodal analysis summary, got %+v", messages)
	}
	for _, msg := range messages {
		if len(msg.ContentParts) > 0 {
			t.Fatalf("expected no image content parts for non-vision model, got %+v", msg.ContentParts)
		}
	}
}

func TestContextPlannerKeepsImagePartsForVisionModel(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(namedAttachmentProvider{}, true); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	a := &Agent{
		catalog:        provider.NewModelCatalog(),
		contextWin:     contextx.NewContextWindow(contextx.DefaultWindowConfig()),
		contextEst:     contextx.NewTokenEstimator(4096),
		mediaProcessor: processor,
		activeModel:    "gpt-5.4-mini",
	}
	planner := newContextPlanner(a, contextBuildOptions{
		IncludeRAG:     false,
		IncludeHistory: false,
	})

	input := MultimodalUserTurnInput("describe it", []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FileName: "screen.png",
			FilePath: "/tmp/screen.png",
			MimeType: "image/png",
		},
	})

	messages := planner.BuildInput(context.Background(), nil, input)
	if !messagesContainText(messages, "attachment provider summary") {
		t.Fatalf("expected multimodal analysis summary, got %+v", messages)
	}
	if !messagesContainImagePart(messages) {
		t.Fatalf("expected image content parts for vision model, got %+v", messages)
	}
}

func messagesContainText(messages []provider.Message, needle string) bool {
	for _, msg := range messages {
		if strings.Contains(msg.Content, needle) {
			return true
		}
		for _, part := range msg.ContentParts {
			if strings.Contains(part.Text, needle) {
				return true
			}
		}
	}
	return false
}

func messagesContainImagePart(messages []provider.Message) bool {
	for _, msg := range messages {
		for _, part := range msg.ContentParts {
			if part.Type == "image" {
				return true
			}
		}
	}
	return false
}
