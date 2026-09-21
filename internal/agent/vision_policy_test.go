package agent

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type countedMediaProvider struct {
	namedAttachmentProvider
	calls []multimodal.Modality
}

func (p *countedMediaProvider) Analyze(ctx context.Context, in *multimodal.Input) (*multimodal.AnalysisResult, error) {
	p.calls = append(p.calls, in.Modality)
	return p.namedAttachmentProvider.Analyze(ctx, in)
}

func TestVisionPolicyRoutesImagesExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		mode, model string
		direct      bool
	}{{"auto", "gpt-5.4-mini", true}, {"auto", "text-only", false}, {"external", "gpt-5.4-mini", false}} {
		t.Run(tc.mode+"/"+tc.model, func(t *testing.T) {
			mgr, _ := config.NewManagerWithDir(t.TempDir())
			_ = mgr.Set("models.vision_mode", tc.mode)
			_ = mgr.Set("models.endpoints.vision.provider", "named-attachment-provider")
			fake := &countedMediaProvider{}
			processor := multimodal.NewProcessor()
			_ = processor.RegisterProvider(fake, true, multimodal.ModalityImage, multimodal.ModalityAudio)
			a := &Agent{cfg: mgr, catalog: provider.NewModelCatalog(), activeModel: tc.model, mediaProcessor: processor}
			input := MultimodalUserTurnInput("read this", []gateway.Attachment{
				{Type: gateway.AttachmentImage, MimeType: "image/png", Data: []byte("image-bytes")},
				{Type: gateway.AttachmentAudio, MimeType: "audio/wav", Data: []byte("audio-bytes")},
			})
			messages := newContextPlanner(a, contextBuildOptions{}).BuildInput(context.Background(), nil, input)
			imageCalls, audioCalls, imageParts := 0, 0, 0
			for _, m := range fake.calls {
				if m == multimodal.ModalityImage {
					imageCalls++
				}
				if m == multimodal.ModalityAudio {
					audioCalls++
				}
			}
			for _, m := range messages {
				for _, p := range m.ContentParts {
					if p.Type == "image" {
						imageParts++
						if !strings.HasSuffix(p.Image.URL, base64.StdEncoding.EncodeToString([]byte("image-bytes"))) {
							t.Fatal("image bytes lost")
						}
					}
				}
			}
			if audioCalls != 1 {
				t.Fatalf("audio calls=%d", audioCalls)
			}
			if tc.direct && (imageCalls != 0 || imageParts != 1) {
				t.Fatalf("direct: external=%d parts=%d", imageCalls, imageParts)
			}
			if !tc.direct && (imageCalls != 1 || imageParts != 0) {
				t.Fatalf("external: external=%d parts=%d", imageCalls, imageParts)
			}
			loop := DefaultLoopConfig()
			a.applyVisionToolPolicy(&loop, a.baseProviderSnapshot())
			guard := newTurnToolGuard("read image", loop.DisabledTools)
			blocked := "image_read"
			if tc.direct {
				blocked = "image_analyze"
			}
			if _, ok := guard.blockMessage(provider.ToolCall{Name: blocked}); !ok {
				t.Fatalf("%s was not blocked", blocked)
			}
		})
	}
}

func TestImageReadFeedsOriginalToPrimaryModel(t *testing.T) {
	r := tool.NewRegistry()
	r.Register(tool.ImageReadTool())
	result, err := r.CallDetailed("image_read", map[string]any{"base64_data": base64.StdEncoding.EncodeToString([]byte("image-bytes")), "mime_type": "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	msgs := appendLatestComputerObservation(nil, []executedToolCall{{ToolCall: provider.ToolCall{Name: "image_read"}, Observations: result.Observations}})
	if len(msgs) != 1 || len(msgs[0].ContentParts) != 1 || !strings.HasPrefix(msgs[0].ContentParts[0].Image.URL, "data:image/png;base64,") {
		t.Fatal("missing primary image observation")
	}
}
