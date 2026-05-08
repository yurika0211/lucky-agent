package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/multimodal"
)

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
