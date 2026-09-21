package agent

import (
	"strings"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type mediaRuntime struct {
	processor         *multimodal.Processor
	imageGenerator    multimodal.ImageGenerator
	imageDefaults     tool.ImageGenerationDefaults
	speechSynthesizer multimodal.SpeechSynthesizer
	ttsDefaults       tool.TTSDefaults
}

func buildMediaRuntime(c *config.Config) mediaRuntime {
	runtime := mediaRuntime{processor: multimodal.NewProcessor()}
	_ = runtime.processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityText,
		multimodal.ModalityVideo,
	), true)

	for _, kind := range []config.ModelKind{config.ModelKindVision, config.ModelKindTranscription} {
		selection, ok := c.ModelSelection(kind)
		if !ok {
			continue
		}
		ep := c.ModelEndpoint(kind)
		modalities := []multimodal.Modality{multimodal.ModalityImage, multimodal.ModalityDocument}
		if kind == config.ModelKindTranscription {
			modalities = []multimodal.Modality{multimodal.ModalityAudio}
		}
		if ep.Provider == "local" {
			_ = runtime.processor.RegisterProvider(multimodal.NewLocalProvider(modalities...), true, modalities...)
			continue
		}
		if media, err := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
			APIKey: ep.APIKey, APIBase: ep.APIBase, ResponsesModel: selection.ID, TranscriptionModel: selection.ID,
		}); err == nil {
			_ = runtime.processor.RegisterProvider(media, true, modalities...)
		}
	}

	if imageCfg, ok := resolveImageGenerationConfig(c); ok {
		switch imageCfg.Provider {
		case "gemini":
			if generator, err := multimodal.NewGeminiImageProvider(multimodal.GeminiImageConfig{
				APIKey: imageCfg.APIKey, APIBase: imageCfg.APIBase, AuthMode: imageCfg.AuthMode,
			}); err == nil {
				runtime.imageGenerator = generator
			}
		case "openai":
			if generator, err := multimodal.NewOpenAIMediaProvider(multimodal.OpenAIMediaConfig{
				APIKey:  imageCfg.APIKey,
				APIBase: imageCfg.APIBase,
			}); err == nil {
				runtime.imageGenerator = generator
			}
		}
	}

	if ttsCfg, ok := resolveTTSConfig(c); ok && ttsCfg.Provider == "openai" {
		if synthesizer, err := multimodal.NewOpenAITTSProvider(multimodal.OpenAITTSConfig{
			APIKey: ttsCfg.APIKey, APIBase: ttsCfg.APIBase, AuthMode: ttsCfg.AuthMode,
		}); err == nil {
			runtime.speechSynthesizer = synthesizer
		}
	}

	runtime.imageDefaults = tool.ImageGenerationDefaults{
		Model:             strings.TrimSpace(c.ImageGeneration.Model),
		Size:              strings.TrimSpace(c.ImageGeneration.Size),
		Quality:           strings.TrimSpace(c.ImageGeneration.Quality),
		Background:        strings.TrimSpace(c.ImageGeneration.Background),
		OutputFormat:      strings.TrimSpace(c.ImageGeneration.OutputFormat),
		OutputCompression: c.ImageGeneration.OutputCompression,
		Count:             c.ImageGeneration.Count,
	}
	runtime.ttsDefaults = tool.TTSDefaults{
		Model: strings.TrimSpace(c.TTS.Model), Voice: strings.TrimSpace(c.TTS.Voice),
		Format: strings.TrimSpace(c.TTS.Format), Speed: c.TTS.Speed,
	}
	return runtime
}

func (a *Agent) reloadMediaRuntime(c *config.Config) {
	if a == nil || c == nil {
		return
	}
	next := buildMediaRuntime(c)
	a.mediaMu.Lock()
	a.mediaProcessor = next.processor
	a.mediaMu.Unlock()
	if a.toolServices != nil {
		a.toolServices.ReloadMediaTools(a.tools, "", next.processor, next.imageGenerator, next.imageDefaults, next.speechSynthesizer, next.ttsDefaults)
	}
}
