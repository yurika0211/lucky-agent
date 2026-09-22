package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
)

func TestHandleConfigRedactsSecrets(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	response := httptest.NewRecorder()
	s.handleConfig(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sk-test") {
		t.Fatalf("config endpoint exposed API key: %s", response.Body.String())
	}
}

func TestHandleConfigCanonicalRoundTripAndVisionMode(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	before := a.Config().Get().APIKey
	response := httptest.NewRecorder()
	s.handleConfig(response, httptest.NewRequest(http.MethodGet, "/api/v1/config", nil))
	var submitted map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	if _, exists := submitted["llm_provider"]; exists {
		t.Fatal("API returned duplicate legacy model settings")
	}
	submitted["models"].(map[string]any)["vision_mode"] = "external"
	data, err := json.Marshal(submitted)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	s.handleConfig(response, httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(data)))
	if response.Code != http.StatusOK {
		t.Fatalf("canonical config save: %d %s", response.Code, response.Body.String())
	}
	if got := a.Config().Get(); got.Models.VisionMode != "external" || got.ModelEndpoint(config.ModelKindChat).APIKey != before {
		t.Fatal("mode or redacted secret did not survive save")
	}
	submitted["models"].(map[string]any)["vision_mode"] = "both"
	data, _ = json.Marshal(submitted)
	response = httptest.NewRecorder()
	s.handleConfig(response, httptest.NewRequest(http.MethodPut, "/api/v1/config", bytes.NewReader(data)))
	if response.Code != http.StatusBadRequest || a.Config().Get().Models.VisionMode != "external" {
		t.Fatal("invalid mode was not rejected atomically")
	}
}

func TestHandleModelSwitchPersistsTypedSelection(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	request := httptest.NewRequest(http.MethodPost, "/api/v1/models/switch", bytes.NewBufferString(`{"kind":"embedding","model":"embed-test","provider":"jina"}`))
	response := httptest.NewRecorder()
	s.handleModelSwitch(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if got := a.Config().Get().Models.Active["embedding"]; got != "embed-test" {
		t.Fatalf("active embedding model = %q", got)
	}
}
