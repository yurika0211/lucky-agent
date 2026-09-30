package sdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent/internal/config"
)

// Config bootstraps an embedded LuckyAgent runtime.
//
// Fields applied after bootstrap override any values loaded from HomeDir when
// LoadExisting is true.
type Config struct {
	// HomeDir is the isolated LuckyAgent data root (settings, memory, sessions, rag).
	// When empty, New uses ~/.luckyagent.
	// Embedders should usually pass an app-specific directory so they do not
	// clobber the interactive CLI/daemon profile.
	HomeDir string

	// Provider is the chat provider name (openai, anthropic, openrouter, ...).
	Provider string

	// Model is the chat model id.
	Model string

	// APIKey is the provider credential.
	// Prefer injecting from the environment in the host app; do not hard-code.
	APIKey string

	// APIBase optionally overrides the provider base URL (Azure, proxies, local gateways).
	APIBase string

	// Protocol selects the chat wire protocol when the provider supports more
	// than one. Empty keeps the runtime default. Common values: "chat_completions", "responses".
	Protocol string

	// MaxTokens overrides the default generation cap when > 0.
	MaxTokens int

	// Temperature overrides the default sampling temperature when > 0.
	// Leave 0 to keep the runtime default.
	Temperature float64

	// SystemPrompt, when non-empty, writes an embed-local SOUL.md used as the
	// agent system prompt. Prefer this over editing the shared CLI soul.
	SystemPrompt string

	// AutoApprove, when non-nil, controls whether tool calls that normally need
	// approval are auto-approved inside the embed process.
	AutoApprove *bool

	// DisableTools lists builtin/skill tool names to disable after bootstrap
	// (for example "terminal", "computer_act").
	DisableTools []string

	// LoadExisting, when true, loads HomeDir settings if present before applying
	// the fields above. Overrides from this Config still win.
	LoadExisting bool
}

func (c Config) resolvedHomeDir() (string, error) {
	home := strings.TrimSpace(c.HomeDir)
	if home != "" {
		if !filepath.IsAbs(home) {
			abs, err := filepath.Abs(home)
			if err != nil {
				return "", fmt.Errorf("resolve HomeDir: %w", err)
			}
			home = abs
		}
		return home, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(userHome, ".luckyagent"), nil
}

func (c Config) applyTo(mgr *config.Manager) error {
	base := mgr.Get()
	if base == nil {
		base = config.DefaultConfig()
	}

	if v := strings.TrimSpace(c.Provider); v != "" {
		base.LlmProvider.Name = v
		base.Provider = v
	}
	if v := strings.TrimSpace(c.Model); v != "" {
		base.LlmProvider.Model = v
		base.Model = v
	}
	if v := strings.TrimSpace(c.APIKey); v != "" {
		base.LlmProvider.APIKey = v
		base.APIKey = v
	}
	if v := strings.TrimSpace(c.APIBase); v != "" {
		base.LlmProvider.BaseURL = v
		base.APIBase = v
	}
	if v := strings.TrimSpace(c.Protocol); v != "" {
		base.LlmProvider.Protocol = v
	}
	if c.MaxTokens > 0 {
		base.MaxTokens = c.MaxTokens
		base.Limits.MaxTokens = c.MaxTokens
	}
	if c.Temperature > 0 {
		base.Temperature = c.Temperature
		base.Limits.Temperature = c.Temperature
	}
	if c.AutoApprove != nil {
		base.Agent.AutoApprove = *c.AutoApprove
	}

	// Keep soul path inside the embed home when still pointing at a foreign default.
	soulDefaultSuffix := filepath.Join(".luckyagent", "memory", "prompts", "SOUL.md")
	if strings.HasSuffix(filepath.Clean(base.SoulPath), soulDefaultSuffix) || strings.TrimSpace(base.SoulPath) == "" {
		base.SoulPath = filepath.Join(mgr.HomeDir(), "memory", "prompts", "SOUL.md")
	}

	if prompt := strings.TrimSpace(c.SystemPrompt); prompt != "" {
		base.SoulPath = filepath.Join(mgr.HomeDir(), "memory", "prompts", "SOUL.md")
		if err := os.MkdirAll(filepath.Dir(base.SoulPath), 0o700); err != nil {
			return fmt.Errorf("create soul dir: %w", err)
		}
		content := prompt
		if !strings.HasPrefix(content, "#") {
			content = "# SOUL\n\n" + content
		}
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		if err := os.WriteFile(base.SoulPath, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write system prompt: %w", err)
		}
	}

	normalized, err := config.Normalized(base)
	if err != nil {
		return err
	}
	// Replace validates, publishes, and writes runtime settings under HomeDir.
	// Always pass an app-specific HomeDir for embeds.
	return mgr.Replace(normalized)
}
