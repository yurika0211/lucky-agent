package bootstrap

import (
	"github.com/yurika0211/luckyagent-runtime/config"
	"testing"
)

func TestContextBudgetUsesConfiguredContextLimit(t *testing.T) {
	c := config.DefaultConfig()
	c.MaxTokens = 4096
	c.Context.MaxContextTokens = 16000
	if ContextTokensFor(c) != 16000 || ReservedTokensFor(c) != 4000 {
		t.Fatal("context limit and reserve disagree")
	}
	c.Context.MaxContextTokens = 0
	if ContextTokensFor(c) != 4096 || ReservedTokensFor(c) != 1024 {
		t.Fatal("legacy context fallback lost")
	}
}
