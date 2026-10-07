package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestRefreshChatModelsUsesCurrentKeyAndSurvivesApply(t *testing.T) {
	var gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"listed-by-key"},{"id":"listed-other"}]}`))
	}))
	defer upstream.Close()

	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := mgr.Get()
	cfg.LlmProvider.Name = "openai-compatible"
	cfg.LlmProvider.APIKey = "sk-live"
	cfg.LlmProvider.BaseURL = upstream.URL + "/v1"
	cfg.LlmProvider.Model = "listed-by-key"
	cfg.Provider = cfg.LlmProvider.Name
	cfg.APIKey = cfg.LlmProvider.APIKey
	cfg.APIBase = cfg.LlmProvider.BaseURL
	cfg.Model = cfg.LlmProvider.Model
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	a := &Agent{cfg: mgr, catalog: provider.NewModelCatalog()}

	listed, err := a.RefreshChatModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer sk-live" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if !containsModel(listed, "listed-by-key") || !containsModel(listed, "listed-other") {
		t.Fatalf("listed models = %+v", listed)
	}
	if containsModel(listed, "gpt-3.5-turbo") {
		t.Fatal("refresh still mixed the static catalog into the chat list")
	}
	if err := a.SwitchModelKind(config.ModelKindChat, "listed-other", SwitchModelOptions{Persist: true}); err != nil {
		t.Fatal(err)
	}
	if got := a.Config().Get().ModelEndpoint(config.ModelKindChat).APIKey; got != "sk-live" {
		t.Fatalf("switch replaced chat key: %q", got)
	}
	if got := a.Config().Get().ModelEndpoint(config.ModelKindChat).Provider; got != "openai-compatible" {
		t.Fatalf("switch replaced provider: %q", got)
	}
	if err := a.ApplyRuntimeConfig(a.Config().Get()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Catalog().Get("listed-other"); err != nil {
		t.Fatalf("discovered model dropped after apply: %v", err)
	}
}

func containsModel(models []ModelRef, id string) bool {
	for _, model := range models {
		if model.ID == id && model.Kind == config.ModelKindChat {
			return true
		}
	}
	return false
}
