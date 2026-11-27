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

// summarizeCompactRange is the single-call path. It chunks only when the
// estimate does not fit one request, retries the whole task once, and then
// falls back to local. One task uses one provider call at a time, so a
// half-open breaker cannot log several chunks trying local together.
func (a *Agent) summarizeCompactRange(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (summary, evidence, source string, err error) {
	summary, evidence, source, err = a.summarizeCompactRangeOnce(ctx, messages, turnProvider, opts)
	if err == nil || !a.compactSettings().ChunkFallback {
		return summary, evidence, source, err
	}
	logger.Warn("compact task failed, retrying once", "error", err)
	summary, evidence, source, err = a.summarizeCompactRangeOnce(ctx, messages, turnProvider, opts)
	if err == nil {
		return summary, evidence, source, nil
	}
	return "", "", "", err
}

func estimateTextTokens(est *contextx.TokenEstimator, text string) int {
	if est == nil {
		est = contextx.NewTokenEstimator(4096)
	}
	return est.Estimate(text)
}

func (a *Agent) summarizeCompactRangeOnce(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (string, string, string, error) {
	snap := a.resolveCompactProvider(turnProvider)
	if !snap.valid() {
		return "", "", "", fmt.Errorf("compact session: provider is not initialized")
	}
	est := a.contextEst
	if est == nil {
		est = contextx.NewTokenEstimator(4096)
	}
	settings := a.compactSettings()
	window := a.contextInputLimit()
	if a != nil && a.contextWin != nil {
		if maxTokens := a.contextWin.Config().MaxTokens; maxTokens > window {
			window = maxTokens
		}
	}
	if window <= 0 {
		window = 128000
	}
	budget := window - settings.KeepTokens - a.autoCompactConfig().AutoCompactReservedSummaryTokens
	if budget < 1 {
		budget = 1
	}
	estimated := estimateProviderMessages(est, messages)
	if estimated <= budget || !settings.ChunkFallback {
		text, err := a.chatCompactSummary(ctx, messages, snap, opts)
		if err != nil {
			return "", "", "", err
		}
		return text, compactEvidenceBlock(messages), "llm", nil
	}
	maxChunk, _ := a.compactConfigLimits()
	if maxChunk > budget {
		maxChunk = budget
	}
	chunks := splitCompactOnExchange(messages, est, maxChunk)
	logger.Info("compact chunk fallback", "chunks", len(chunks), "model", snap.model)
	var parts []string
	for i, chunk := range chunks {
		emitCompactProgress(opts, CompactProgress{Phase: "progress", Message: fmt.Sprintf("Compressing context · chunk %d/%d", i+1, len(chunks)), Chunk: i + 1, Chunks: len(chunks), Model: snap.model})
		text, err := a.chatCompactSummary(ctx, chunk, snap, opts)
		if err != nil {
			// One task, one failure. Do not log a per-chunk local attempt.
			logger.Warn("compact task chunk failed", "chunk", i+1, "chunks", len(chunks), "error", err)
			return "", "", "", err
		}
		parts = append(parts, text)
	}
	if len(parts) == 1 {
		return parts[0], compactEvidenceBlock(messages), "llm", nil
	}
	merged := compactlib.MergeLocal(parts, compactTextOptions())
	return merged, compactEvidenceBlock(messages), "llm-fallback", nil
}

func (a *Agent) chatCompactSummary(ctx context.Context, messages []provider.Message, snap providerSnapshot, opts CompactSessionOptions) (string, error) {
	input := toCompactMessages(messages)
	body := compactlib.Transcript(input, compactTextOptions())
	prompt := compactlib.SegmentPrompt(body, "LuckyAgent")
	response, err := a.chatLoopIteration(ctx, []provider.Message{{Role: "system", Content: "Summarize the conversation. Output the required headings only."}, {Role: "user", Content: prompt}}, provider.CallOptions{}, true, snap)
	if opts.observe != nil {
		opts.observe.noteCall(response)
	}
	if err != nil {
		return "", err
	}
	if response == nil || strings.TrimSpace(response.Content) == "" {
		return "", fmt.Errorf("compact session: empty summary")
	}
	text := strings.TrimSpace(response.Content)
	if !compactSummaryAccepted(text) {
		logger.Warn("compact summary missing headings, retrying once")
		retry, retryErr := a.chatLoopIteration(ctx, []provider.Message{{Role: "user", Content: prompt + "\n\nThe previous summary missed a required heading. Repeat it with every heading present."}}, provider.CallOptions{}, true, snap)
		if opts.observe != nil {
			opts.observe.noteCall(retry)
		}
		if retryErr != nil || retry == nil || !compactSummaryAccepted(retry.Content) {
			logger.Warn("compact summary kept invalid model output", "output", text)
			return "", fmt.Errorf("compact session: summary headings incomplete")
		}
		text = strings.TrimSpace(retry.Content)
	}
	emitCompactProgress(opts, CompactProgress{Phase: "progress", Message: "Summarizing conversation context…", Model: snap.model})
	return text, nil
}

func compactSummaryAccepted(summary string) bool {
	if compactlib.Validate(summary, nil).Valid {
		return true
	}
	return compactHeadingsPresent(summary)
}

func compactHeadingsPresent(summary string) bool {
	lower := strings.ToLower(summary)
	for _, heading := range []string{"current user goal", "completed work", "pending work", "key files and functions", "commands and test results", "user constraints", "uncertain facts"} {
		if !strings.Contains(lower, heading) {
			return false
		}
	}
	return true
}

func splitCompactOnExchange(messages []provider.Message, est *contextx.TokenEstimator, maxChunk int) [][]provider.Message {
	if len(messages) == 0 {
		return nil
	}
	var chunks [][]provider.Message
	var current []provider.Message
	used := 0
	flush := func() {
		if len(current) > 0 {
			chunks = append(chunks, current)
			current = nil
			used = 0
		}
	}
	for _, message := range messages {
		size := estimateProviderMessages(est, []provider.Message{message})
		if len(current) > 0 && (message.Role == "user" || message.Role == "assistant") && used+size > maxChunk {
			flush()
		}
		current = append(current, message)
		used += size
	}
	flush()
	if len(chunks) == 0 {
		return [][]provider.Message{messages}
	}
	return chunks
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
