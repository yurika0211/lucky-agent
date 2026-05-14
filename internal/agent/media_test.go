package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/multimodal"
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
