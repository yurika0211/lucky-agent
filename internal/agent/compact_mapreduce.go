package agent

import (
	"context"
	"fmt"
	"strings"

	compactlib "github.com/yurika0211/luckyagent-tools/compact"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/provider"
)

const (
	defaultCompactMaxChunkTokens = compactlib.DefaultMaxChunkTokens
	defaultCompactMaxParallel    = compactlib.DefaultMaxParallel
)

func (a *Agent) compactConfigLimits() (maxChunkTokens, maxParallel int) {
	maxChunkTokens = defaultCompactMaxChunkTokens
	maxParallel = defaultCompactMaxParallel
	if a == nil || a.cfg == nil {
		return maxChunkTokens, maxParallel
	}
	cfg := a.cfg.Get().Context
	if cfg.CompactMaxChunkTokens > 0 {
		maxChunkTokens = cfg.CompactMaxChunkTokens
	}
	if cfg.CompactMaxParallel > 0 {
		maxParallel = cfg.CompactMaxParallel
	}
	return maxChunkTokens, maxParallel
}

// resolveCompactProvider returns a provider snapshot that uses context.compact_model
// when configured; otherwise it keeps the turn provider.
func (a *Agent) resolveCompactProvider(turnProvider providerSnapshot) providerSnapshot {
	if !turnProvider.valid() {
		return turnProvider
	}
	if a == nil || a.cfg == nil || a.registry == nil {
		return turnProvider
	}
	cfg := a.cfg.Get()
	if cfg == nil {
		return turnProvider
	}
	model := strings.TrimSpace(cfg.Context.CompactModel)
	if selected, ok := cfg.ModelSelection(config.ModelKindCompact); ok && strings.TrimSpace(selected.ID) != "" {
		model = strings.TrimSpace(selected.ID)
	}
	if model == "" {
		return turnProvider
	}
	if model == strings.TrimSpace(turnProvider.model) {
		return turnProvider
	}
	if err := resolveConfiguredCredentials(a.cfg.HomeDir(), cfg); err != nil {
		logger.Warn("compact model: resolve credentials failed, using turn provider", "error", err)
		return turnProvider
	}
	pCfg := toProviderConfig(cfg, model, "")
	if strings.TrimSpace(pCfg.LlmProvider.Name) == "" {
		return turnProvider
	}
	a.providerMu.Lock()
	p, err := a.registry.Create(pCfg.LlmProvider.Name, pCfg)
	a.providerMu.Unlock()
	if err != nil || p == nil {
		logger.Warn("compact model: create provider failed, using turn provider",
			"model", model, "error", err)
		return turnProvider
	}
	apiBase := strings.TrimSpace(pCfg.LlmProvider.BaseURL)
	if apiBase == "" {
		apiBase = turnProvider.apiBase
	}
	return providerSnapshot{
		provider: wrapProviderWithMiddleware(p, cfg),
		model:    model,
		apiBase:  apiBase,
	}
}

// generateMapReduceCompactSummary owns only host provider/config assembly.
// The shared engine does not receive Agent, session state, or credentials.
func (a *Agent) generateMapReduceCompactSummary(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (string, string, error) {
	if len(messages) == 0 {
		return "", "", fmt.Errorf("compact session: no textual content to summarize")
	}
	snap := a.resolveCompactProvider(turnProvider)
	if !snap.valid() {
		return "", "", fmt.Errorf("compact session: provider is not initialized")
	}
	est := a.contextEst
	if est == nil {
		est = contextx.NewTokenEstimator(4096)
	}
	maxChunk, maxParallel := a.compactConfigLimits()
	engine := compactlib.New(compactlib.Config{
		MaxChunkTokens: maxChunk, MaxParallel: maxParallel, Model: snap.model, AgentName: "LuckyAgent",
		Estimator: est, Text: compactTextOptions(), Logger: logger.GetLogger(),
		OnProgress: func(progress compactlib.Progress) { emitCompactProgress(opts, progress) },
		Chat: func(ctx context.Context, request compactlib.ChatRequest) (string, error) {
			response, err := snap.provider.Chat(ctx, []provider.Message{{Role: "system", Content: request.SystemPrompt}, {Role: "user", Content: request.UserPrompt}})
			if err != nil {
				return "", err
			}
			if response == nil {
				return "", nil
			}
			return response.Content, nil
		},
	})
	return engine.Generate(ctx, toCompactMessages(messages))
}
