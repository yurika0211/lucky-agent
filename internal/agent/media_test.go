package agent

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/provider"
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

type recordingAttachmentProvider struct {
	namedAttachmentProvider
	inputs []*multimodal.Input
}

func (*recordingAttachmentProvider) SupportedModalities() []multimodal.Modality {
	return []multimodal.Modality{multimodal.ModalityImage, multimodal.ModalityAudio, multimodal.ModalityDocument}
}

func (p *recordingAttachmentProvider) Analyze(ctx context.Context, input *multimodal.Input) (*multimodal.AnalysisResult, error) {
	p.inputs = append(p.inputs, input)
	return p.namedAttachmentProvider.Analyze(ctx, input)
}

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

func TestAttachmentEvidenceManifestMarksUnavailableAttachment(t *testing.T) {
	attachments := []gateway.Attachment{{
		Type:     gateway.AttachmentDocument,
		FileID:   "file-123",
		FileName: "report.pdf",
		MimeType: "application/pdf",
	}}

	out := attachmentEvidenceManifest(attachments)
	if !strings.Contains(out, "[Current Turn Attachments]") {
		t.Fatalf("expected manifest header, got %q", out)
	}
	if !strings.Contains(out, "status=unavailable") {
		t.Fatalf("expected unavailable marker, got %q", out)
	}
	if strings.Contains(out, "workspace/document.pdf") {
		t.Fatalf("manifest must not invent stale workspace paths: %q", out)
	}
	if !strings.Contains(out, "Do not substitute files from chat history") {
		t.Fatalf("expected anti-substitution instruction, got %q", out)
	}
}

func TestFilterHistoricalMultimodalTurns(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "[Multimodal Analysis]\nImage: old-weather.jpg"},
		{Role: "assistant", Content: "旧金山天气"},
		{Role: "user", Content: "继续处理本地项目"},
		{Role: "assistant", Content: "已处理"},
	}

	got := filterHistoricalMultimodalTurns(messages)
	if len(got) != 2 || got[0].Content != "继续处理本地项目" || got[1].Content != "已处理" {
		t.Fatalf("historical multimodal turn was not isolated: %#v", got)
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
	filePath := filepath.Join(tmpDir, "report.bin")
	if err := os.WriteFile(filePath, []byte("file on disk"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	a := &Agent{mediaProcessor: processor}
	attachments := []gateway.Attachment{
		{
			Type:     gateway.AttachmentDocument,
			FileName: "report.bin",
			FilePath: filePath,
			MimeType: "application/octet-stream",
		},
	}

	out, err := a.AnalyzeAttachments(context.Background(), attachments)
	if err != nil {
		t.Fatalf("AnalyzeAttachments error: %v", err)
	}
	if !strings.Contains(out, "Document: report.bin") {
		t.Fatalf("expected document section, got %q", out)
	}
	if !strings.Contains(out, "Document file (application/octet-stream") {
		t.Fatalf("expected local provider output from file path, got %q", out)
	}
}

func TestAnalyzeAttachmentsExtractsDownloadedDocxText(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "report.docx")
	writeAgentTestZipFile(t, filePath, map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>NapCat document body</w:t></w:r></w:p></w:body></w:document>`,
	})

	a := &Agent{mediaProcessor: multimodal.NewProcessor()}
	attachments := []gateway.Attachment{{
		Type:     gateway.AttachmentDocument,
		FileName: "report.docx",
		FilePath: filePath,
		MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	}}

	out, err := a.AnalyzeAttachments(context.Background(), attachments)
	if err != nil {
		t.Fatalf("AnalyzeAttachments error: %v", err)
	}
	if !strings.Contains(out, "Document: report.docx") || !strings.Contains(out, "NapCat document body") {
		t.Fatalf("expected extracted docx text, got %q", out)
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
	analyzer := &recordingAttachmentProvider{}
	if err := processor.RegisterProvider(analyzer, true); err != nil {
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
	if len(analyzer.inputs) != 1 || analyzer.inputs[0].Modality != multimodal.ModalityImage {
		t.Fatalf("expected one image pre-analysis, got %v", analyzer.inputs)
	}
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
	analyzer := &recordingAttachmentProvider{}
	if err := processor.RegisterProvider(analyzer, true); err != nil {
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
	if len(analyzer.inputs) != 0 || messagesContainText(messages, "[Multimodal Analysis]") {
		t.Fatalf("native vision must skip image pre-analysis, calls=%d", len(analyzer.inputs))
	}
	if got := countImageParts(messages); got != 1 {
		t.Fatalf("expected exactly one image, got %d", got)
	}
}

func TestContextPlannerNativeVisionStillAnalyzesOtherAttachments(t *testing.T) {
	analyzer := &recordingAttachmentProvider{}
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(analyzer, true); err != nil {
		t.Fatal(err)
	}
	a := &Agent{catalog: provider.NewModelCatalog(), activeModel: "gpt-5.4-mini", mediaProcessor: processor}
	input := MultimodalUserTurnInput("compare the images and the recording", []gateway.Attachment{
		{Type: gateway.AttachmentImage, FileURL: "https://example.test/first.png", MimeType: "image/png"},
		{Type: gateway.AttachmentAudio, FileName: "audio.wav", Data: []byte("audio"), MimeType: "audio/wav"},
		{Type: gateway.AttachmentImage, Data: []byte("second-image"), MimeType: "image/png"},
		{Type: gateway.AttachmentDocument, FileName: "report.pdf", Data: []byte("%PDF-1.4"), MimeType: "application/pdf"},
	})
	messages := newContextPlanner(a, contextBuildOptions{}).BuildInput(context.Background(), nil, input)
	if len(analyzer.inputs) != 2 || analyzer.inputs[0].Modality != multimodal.ModalityAudio || analyzer.inputs[1].Modality != multimodal.ModalityDocument {
		t.Fatalf("expected audio and document analysis only, got %v", analyzer.inputs)
	}
	if got := countImageParts(messages); got != 2 {
		t.Fatalf("expected each image once, got %d", got)
	}
	last := messages[len(messages)-1]
	if last.Role != "user" || last.Content != input.RoutingText || len(last.ContentParts) != 3 {
		t.Fatalf("current user caption and images were not retained: %+v", last)
	}
}

func TestContextPlannerVisionRoutingUsesConfiguredEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name       string
		vision     bool
		routed     bool
		wantNative bool
	}{
		{name: "explicit vision on uncatalogued chat model", vision: true, wantNative: true},
		{name: "non-vision chat uses dedicated vision"},
		{name: "chat override does not enable vision on routed model", vision: true, routed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chatCalls, visionCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Model string          `json:"model"`
					Raw   json.RawMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/vision/responses":
					visionCalls.Add(1)
					if req.Model != "dedicated-vision" {
						t.Errorf("pre-analysis model = %q", req.Model)
					}
					fmt.Fprint(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"dedicated vision summary"}]}]}`)
				case "/chat/chat/completions":
					chatCalls.Add(1)
					wantModel := "custom-chat"
					if tc.routed {
						wantModel = "custom-text-route"
					}
					if req.Model != wantModel {
						t.Errorf("chat model = %q, want %q", req.Model, wantModel)
					}
					imageCount := strings.Count(string(req.Raw), `"type":"image_url"`)
					if (tc.wantNative && imageCount != 1) || (!tc.wantNative && imageCount != 0) {
						t.Errorf("wire image count = %d, native=%t", imageCount, tc.wantNative)
					}
					if !tc.wantNative && !strings.Contains(string(req.Raw), "dedicated vision summary") {
						t.Error("missing vision summary in chat request")
					}
					fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
				default:
					t.Errorf("unexpected upstream request: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			mgr, err := config.NewManagerWithDir(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cfg := mgr.Get()
			cfg.LlmProvider.Vision = tc.vision
			for kind, model := range map[config.ModelKind]string{config.ModelKindChat: "custom-chat", config.ModelKindVision: "dedicated-vision"} {
				if err := cfg.SetModelSelection(kind, model, config.ModelEndpointConfig{
					Provider: "openai", APIKey: "test-key", APIBase: upstream.URL + "/" + string(kind),
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := mgr.Replace(cfg); err != nil {
				t.Fatal(err)
			}
			model := "custom-chat"
			if tc.routed {
				model = "custom-text-route"
			}
			chat := provider.NewOpenAIProvider(provider.Config{LlmProvider: provider.LlmProvider{
				Name: "openai", Model: model, APIKey: "test-key", BaseURL: upstream.URL + "/chat",
			}})
			a := &Agent{cfg: mgr, catalog: provider.NewModelCatalog(), mediaProcessor: buildMediaRuntime(mgr.Get()).processor}
			planner := newContextPlannerWithProvider(a, contextBuildOptions{}, providerSnapshot{provider: chat, model: model, primaryVision: &tc.wantNative})
			input := MultimodalUserTurnInput("describe this", []gateway.Attachment{{
				Type: gateway.AttachmentImage, Data: []byte("image"), MimeType: "image/png",
			}})
			messages := planner.BuildInput(context.Background(), nil, input)
			if _, err := chat.Chat(context.Background(), messages); err != nil {
				t.Fatal(err)
			}
			wantVisionCalls := int32(1)
			if tc.wantNative {
				wantVisionCalls = 0
			}
			if chatCalls.Load() != 1 || visionCalls.Load() != wantVisionCalls {
				t.Fatalf("chat calls=%d, vision calls=%d; want 1, %d", chatCalls.Load(), visionCalls.Load(), wantVisionCalls)
			}
		})
	}
}

func countImageParts(messages []provider.Message) int {
	count := 0
	for _, msg := range messages {
		for _, part := range msg.ContentParts {
			if part.Type == "image" {
				count++
			}
		}
	}
	return count
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

func writeAgentTestZipFile(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip test file: %v", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create zip member %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write zip member %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip test file: %v", err)
	}
}
