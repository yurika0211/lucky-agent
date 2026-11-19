package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
	compactlib "github.com/yurika0211/luckyagent-tools/compact"
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
		budget:   turnProvider.budget,
		provider: wrapProviderWithMiddleware(p, cfg),
		model:    model,
		apiBase:  apiBase,
	}
}

// generateMapReduceCompactSummary owns only host provider/config assembly.
// The shared engine does not receive Agent, session state, or credentials.
func (a *Agent) generateMapReduceCompactSummary(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (string, string, error) {
	return a.generateMapReduceCompactSummaryObserved(ctx, messages, turnProvider, opts, nil)
}

func (a *Agent) generateMapReduceCompactSummaryObserved(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions, observed *compactSummaryObservation) (string, string, error) {
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
	input := toCompactMessages(messages)
	evidence, err := planCompactEvidence(ctx, input, est, maxChunk, requiredCompactIdentifiers(messages))
	if err != nil {
		return "", "", err
	}
	if evidence == nil {
		if ctx != nil && ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		emitCompactProgress(opts, CompactProgress{Phase: "progress", Message: "Using a local summary because the map inputs cannot preserve all required source evidence"})
		return generateLocalCompactSummary(messages, est), "local", nil
	}
	engine := compactlib.New(compactlib.Config{
		MaxChunkTokens: maxChunk, MaxParallel: maxParallel, Model: snap.model, AgentName: "LuckyAgent",
		Estimator: est, Text: compactTextOptions(), Logger: logger.GetLogger(),
		SummaryBudget: func(request compactlib.ChatRequest) time.Duration {
			prepared, _ := evidence.prepare(request)
			return compactSummaryBudgetForRunes(prepared.TranscriptRunes)
		},
		OnProgress: func(progress compactlib.Progress) { emitCompactProgress(opts, progress) },
		Chat: func(ctx context.Context, request compactlib.ChatRequest) (string, error) {
			request, ok := evidence.prepare(request)
			if !ok {
				return "", fmt.Errorf("compact request cannot carry the required source evidence within budget")
			}
			response, err := a.chatLoopIteration(ctx, []provider.Message{{Role: "system", Content: request.SystemPrompt}, {Role: "user", Content: request.UserPrompt}}, provider.CallOptions{}, true, snap)
			if observed != nil {
				observed.add(response)
			}
			if err != nil {
				return "", err
			}
			if response == nil {
				return "", nil
			}
			return response.Content, nil
		},
	})
	return engine.Generate(ctx, input)
}

const (
	compactSummaryBudgetMin = 45 * time.Second
	compactSummaryBudgetMax = 180 * time.Second
	// compactSummaryBudgetRunes is the transcript size that reaches the 180s cap.
	// Smaller transcripts stay at 45s. The scale is linear and does not look at
	// the model name.
	compactSummaryBudgetRunes = 48_000
)

// compactSummaryBudget maps one summarizer request onto 45s..180s.
// Zero and negative rune counts stay at the floor. The tools engine also
// treats a non-positive duration as 45s, so this host never sends one.
func compactSummaryBudgetForRunes(runes int) time.Duration {
	if runes <= 0 {
		return compactSummaryBudgetMin
	}
	if runes >= compactSummaryBudgetRunes {
		return compactSummaryBudgetMax
	}
	span := compactSummaryBudgetMax - compactSummaryBudgetMin
	extra := time.Duration(runes) * span / compactSummaryBudgetRunes
	return compactSummaryBudgetMin + extra
}
