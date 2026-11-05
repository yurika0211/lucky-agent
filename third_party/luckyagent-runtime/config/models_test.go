package config

import "testing"

func TestNormalizeModelsMigratesLegacySelections(t *testing.T) {
	cfg, err := parseConfigData([]byte(`{
  "llm_provider":{"name":"openai","api_key":"chat-key","base_url":"https://chat.example/v1","model":"chat-next","protocol":"responses"},
  "embedding":{"model":"embed-next","api_key":"embed-key","api_base":"https://embed.example/v1"},
  "multimodal":{"provider":"openai","api_key":"media-key","api_base":"https://media.example/v1","image_model":"vision-next","transcription_model":"transcribe-next"},
  "image_generation":{"provider":"openai","api_key":"image-key","api_base":"https://image.example/v1","model":"image-next"},
  "tts":{"provider":"openai","api_key":"tts-key","api_base":"https://tts.example/v1","model":"tts-next"}
}`))
	if err != nil {
		t.Fatalf("parseConfigData: %v", err)
	}

	for kind, want := range map[ModelKind]string{
		ModelKindChat:          "chat-next",
		ModelKindVision:        "vision-next",
		ModelKindEmbedding:     "embed-next",
		ModelKindTranscription: "transcribe-next",
		ModelKindImage:         "image-next",
		ModelKindTTS:           "tts-next",
	} {
		if got := cfg.Models.Active[kind]; got != want {
			t.Errorf("active[%s] = %q, want %q", kind, got, want)
		}
	}
	if got := cfg.Models.Endpoints[ModelKindChat].Protocol; got != "responses" {
		t.Errorf("chat protocol = %q, want responses", got)
	}
	if got := cfg.Models.Endpoints[ModelKindEmbedding].APIBase; got != "https://embed.example/v1" {
		t.Errorf("embedding api base = %q", got)
	}
}

func TestSetModelSelectionSynchronizesLegacyFields(t *testing.T) {
	cfg := DefaultConfig()
	normalizeConfig(cfg)
	err := cfg.SetModelSelection(ModelKindTTS, "voice-next", ModelEndpointConfig{
		Provider: "openai",
		APIKey:   "tts-key",
		APIBase:  "https://voice.example/v1",
	})
	if err != nil {
		t.Fatalf("SetModelSelection: %v", err)
	}
	if cfg.TTS.Model != "voice-next" || cfg.TTS.APIKey != "tts-key" || cfg.TTS.APIBase != "https://voice.example/v1" {
		t.Fatalf("legacy tts fields not synchronized: %#v", cfg.TTS)
	}
	selection, ok := cfg.ModelSelection(ModelKindTTS)
	if !ok || selection.ID != "voice-next" || selection.Provider != "openai" {
		t.Fatalf("unexpected resolved TTS selection: %#v, ok=%t", selection, ok)
	}
}

func TestRedactAndPreserveSecrets(t *testing.T) {
	current := DefaultConfig()
	normalizeConfig(current)
	current.LlmProvider.APIKey = "secret-chat"
	current.Models.Endpoints[ModelKindChat] = ModelEndpointConfig{APIKey: "secret-chat", ExtraHeaders: map[string]string{"Authorization": "Bearer secret"}}
	normalizeConfig(current)

	redacted := RedactSecrets(current)
	if redacted.LlmProvider.APIKey != "" {
		t.Fatal("chat API key was exposed by RedactSecrets")
	}
	if headers := redacted.Models.Endpoints[ModelKindChat].ExtraHeaders; len(headers) != 0 {
		t.Fatalf("extra headers were exposed: %#v", headers)
	}

	submitted := Clone(redacted)
	submitted.LlmProvider.Model = "chat-new"
	PreserveRedactedSecrets(current, submitted)
	if submitted.LlmProvider.APIKey != "secret-chat" {
		t.Fatal("redacted API key was not preserved on update")
	}
	if got := submitted.Models.Endpoints[ModelKindChat].ExtraHeaders["Authorization"]; got != "Bearer secret" {
		t.Fatalf("redacted headers were not preserved, got %q", got)
	}
}
