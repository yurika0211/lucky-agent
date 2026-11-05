package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPromptCacheConfigurationMigrationAndClear(t *testing.T) {
	for _, input := range []string{`{"llm_provider":{"prompt_cache_key":"project-a"}}`, `{"models":{"endpoints":{"chat":{"prompt_cache_key":"project-a"}}}}`} {
		cfg, err := parseConfigData([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		selection, _ := cfg.ModelSelection(ModelKindChat)
		if selection.PromptCacheKey != "project-a" || cfg.LlmProvider.PromptCacheKey != "project-a" {
			t.Fatalf("selection = %#v", selection)
		}
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"llm_provider"`) {
			t.Fatal("persisted legacy model config")
		}
		again, err := parseConfigData(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if again.LlmProvider.PromptCacheKey != "project-a" {
			t.Fatal("key lost in serialization")
		}
		if handled, err := again.setModelKey("llm_provider.prompt_cache_key", ""); !handled || err != nil {
			t.Fatalf("clear = %v, %v", handled, err)
		}
		if again.LlmProvider.PromptCacheKey != "" {
			t.Fatal("key not cleared")
		}
	}
}
