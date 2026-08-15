package agent

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
)

func TestToProviderConfigCopiesLLMProtocol(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LlmProvider.Protocol = "responses"

	providerCfg := toProviderConfig(cfg, "", "")
	if providerCfg.LlmProvider.Protocol != "responses" {
		t.Fatalf("protocol = %q, want responses", providerCfg.LlmProvider.Protocol)
	}
}
