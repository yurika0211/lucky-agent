package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalModelConfigRoundTrip(t *testing.T) {
	cfg, err := parseConfigData([]byte(`{
	 "llm_provider":{"name":"openai","model":"legacy-chat","api_key":"old","vision":true},
	 "multimodal":{"api_key":"old-media","image_model":"old-vision","image_provider":"local"},
	 "models":{"vision_mode":"external","active":{"chat":"new-chat","vision":"new-vision","transcription":"speech"},
	 "endpoints":{"chat":{"provider":"openai","api_key":"new"},"vision":{"provider":"openai","api_key":"vision-key","api_base":"https://vision.example/v1"},"transcription":{"provider":"openai","api_key":"speech-key","api_base":"https://speech.example/v1"}}},
	 "embedding":{"dimension":1024},"image_generation":{"size":"1024x1024"},"tts":{"voice":"alloy"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"llm_provider", "multimodal", "extra_headers"} {
			if _, ok := obj[key]; ok {
				t.Fatalf("duplicate section persisted: %s", key)
			}
		}
		cfg, err = parseConfigData(data)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Models.Active[ModelKindChat] != "new-chat" || cfg.Models.VisionMode != "external" {
			t.Fatal("canonical selections lost")
		}
		if ep := cfg.ModelEndpoint(ModelKindVision); ep.APIKey != "vision-key" || ep.APIBase != "https://vision.example/v1" || ep.Provider != "openai" {
			t.Fatal("vision endpoint overwritten")
		}
		if ep := cfg.ModelEndpoint(ModelKindTranscription); ep.APIKey != "speech-key" || ep.APIBase != "https://speech.example/v1" {
			t.Fatal("transcription endpoint overwritten")
		}
		if cfg.Embedding.Dimension != 1024 || cfg.ImageGeneration.Size != "1024x1024" || cfg.TTS.Voice != "alloy" {
			t.Fatal("functional parameters lost")
		}
	}
	if len(cfg.CustomModels) != 1 || cfg.CustomModels[0].ID != "legacy-chat" {
		t.Fatal("legacy vision flag must belong only to the model it described")
	}
}

func TestModelConfigKeysAndReload(t *testing.T) {
	mgr, _ := NewManagerWithDir(t.TempDir())
	for key, value := range map[string]string{"models.vision_mode": "external", "models.active.vision": "vision-x", "models.endpoints.vision.api_base": "https://vision.example/v1", "models.endpoints.vision.api_key": "v-key", "models.endpoints.transcription.api_base": "https://speech.example/v1", "models.endpoints.transcription.api_key": "s-key"} {
		if err := mgr.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := mgr.Save(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Set("multimodal.image_model", "vision-y"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Save(); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Get().Models.Active[ModelKindVision]; got != "vision-y" {
		t.Fatalf("legacy setter lost update: %q", got)
	}
	if value, ok, err := mgr.Get().ModelConfigValue("models.vision_mode"); err != nil || !ok || value != "external" {
		t.Fatalf("get mode: %q %v", value, err)
	}
	if err := mgr.Set("models.vision_mode", "both"); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if mgr.Get().Models.VisionMode != "external" {
		t.Fatal("invalid update changed state")
	}
	if _, err := parseConfigData([]byte(`{"models":{"vision_mode":"both"}}`)); err == nil {
		t.Fatal("invalid persisted mode accepted")
	}
}

func TestExplicitEmptyModelCredentialsAreNotRefilled(t *testing.T) {
	cfg, err := parseConfigData([]byte(`{"multimodal":{"api_key":"legacy-secret"},"models":{"endpoints":{"vision":{"provider":"openai","api_key":"","api_base":"https://new.example/v1"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelEndpoint(ModelKindVision).APIKey != "" {
		t.Fatal("empty canonical secret inherited legacy key")
	}
	data, err := json.Marshal(RedactSecrets(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "legacy-secret") {
		t.Fatal("redacted config leaked secret")
	}
}

func TestLegacyVisionFlagWithCanonicalChatSelection(t *testing.T) {
	cfg, err := parseConfigData([]byte(`{"llm_provider":{"vision":true},"models":{"active":{"chat":"custom-vision-model"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.CustomModels) != 1 || cfg.CustomModels[0].ID != "custom-vision-model" {
		t.Fatalf("vision capability migrated to wrong model: %+v", cfg.CustomModels)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "llm_provider") {
		t.Fatal("legacy capability flag was persisted")
	}
}
