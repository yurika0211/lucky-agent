package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/multimodal"
	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestMediaRuntimeKeepsVisionAndTranscriptionEndpointsSeparate(t *testing.T) {
	visionCalls, speechCalls := 0, 0
	vision := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visionCalls++
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer vision-key" {
			t.Errorf("wrong vision request: %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "vision-model" {
			t.Errorf("wrong vision model: %v", body["model"])
		}
		fmt.Fprint(w, `{"output_text":"visible text"}`)
	}))
	defer vision.Close()
	speech := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		speechCalls++
		if r.URL.Path != "/v1/audio/transcriptions" || r.Header.Get("Authorization") != "Bearer speech-key" {
			t.Errorf("wrong speech request: %s", r.URL.Path)
		}
		if r.FormValue("model") != "speech-model" {
			t.Error("wrong transcription model")
		}
		fmt.Fprint(w, `{"text":"spoken text"}`)
	}))
	defer speech.Close()
	cfg := config.DefaultConfig()
	cfg.Models = config.ModelsConfig{Active: map[config.ModelKind]string{config.ModelKindVision: "vision-model", config.ModelKindTranscription: "speech-model"}, Endpoints: map[config.ModelKind]config.ModelEndpointConfig{
		config.ModelKindVision:        {Provider: "openai", APIKey: "vision-key", APIBase: vision.URL + "/v1"},
		config.ModelKindTranscription: {Provider: "openai", APIKey: "speech-key", APIBase: speech.URL + "/v1"},
	}}
	cfg, err := config.Normalized(cfg)
	if err != nil {
		t.Fatal(err)
	}
	runtime := buildMediaRuntime(cfg)
	for _, input := range []*multimodal.Input{multimodal.NewInput(multimodal.ModalityImage, "image/png", []byte("image")), multimodal.NewInput(multimodal.ModalityAudio, "audio/wav", []byte("audio"))} {
		if _, err := runtime.processor.Analyze(context.Background(), input); err != nil {
			t.Fatal(err)
		}
	}
	if visionCalls != 1 || speechCalls != 1 {
		t.Fatalf("vision=%d speech=%d", visionCalls, speechCalls)
	}
}

func TestVisionModeSnapshotSurvivesConfigChange(t *testing.T) {
	mgr, _ := config.NewManagerWithDir(t.TempDir())
	a := &Agent{cfg: mgr, catalog: provider.NewModelCatalog(), activeModel: "gpt-5.4-mini"}
	snapshot := a.providerSnapshotForTurn("read image")
	if err := mgr.Set("models.vision_mode", "external"); err != nil {
		t.Fatal(err)
	}
	if !newContextPlannerWithProvider(a, contextBuildOptions{}, snapshot).supportsImageContentParts() {
		t.Fatal("active turn changed vision mode")
	}
	if newContextPlannerWithProvider(a, contextBuildOptions{}, a.providerSnapshotForTurn("read image")).supportsImageContentParts() {
		t.Fatal("next turn ignored updated mode")
	}
	if err := mgr.Set("models.vision_mode", "auto"); err != nil {
		t.Fatal(err)
	}
	if newContextPlannerWithProvider(a, contextBuildOptions{}, providerSnapshot{model: "unknown-routed-model"}).supportsImageContentParts() {
		t.Fatal("unknown routed model inherited primary capability")
	}
}
