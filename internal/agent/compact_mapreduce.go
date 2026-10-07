package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/contextx"
	"github.com/yurika0211/luckyagent/internal/logger"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/utils"
)

const (
	defaultCompactMaxChunkTokens = 12000
	defaultCompactMaxParallel    = 4
)

// sliceCompactInputChunks splits compactInput on complete user-turn boundaries
// so each chunk stays near maxChunkTokens. A single oversized turn becomes its
// own chunk (transcript budget still trims the LLM payload).
func sliceCompactInputChunks(messages []provider.Message, est *contextx.TokenEstimator, maxChunkTokens int) [][]provider.Message {
	if len(messages) == 0 {
		return nil
	}
	if maxChunkTokens <= 0 {
		maxChunkTokens = defaultCompactMaxChunkTokens
	}
	if estimateProviderMessages(est, messages) <= maxChunkTokens {
		return [][]provider.Message{append([]provider.Message(nil), messages...)}
	}

	turnStarts := make([]int, 0, len(messages)/2+1)
	for i, msg := range messages {
		if msg.Role == "user" {
			turnStarts = append(turnStarts, i)
		}
	}
	if len(turnStarts) == 0 {
		return [][]provider.Message{append([]provider.Message(nil), messages...)}
	}
	if turnStarts[0] != 0 {
		turnStarts = append([]int{0}, turnStarts...)
	}
	turnStarts = append(turnStarts, len(messages))

	var chunks [][]provider.Message
	chunkStart := turnStarts[0]
	for t := 0; t < len(turnStarts)-1; t++ {
		turnEnd := turnStarts[t+1]
		candidate := messages[chunkStart:turnEnd]
		if estimateProviderMessages(est, candidate) <= maxChunkTokens {
			continue
		}
		// Flush everything before this turn when the running chunk overflowed.
		prevEnd := turnStarts[t]
		if prevEnd > chunkStart {
			chunks = append(chunks, append([]provider.Message(nil), messages[chunkStart:prevEnd]...))
			chunkStart = prevEnd
			candidate = messages[chunkStart:turnEnd]
		}
		// Single turn still over budget: emit it alone and continue.
		if estimateProviderMessages(est, candidate) > maxChunkTokens {
			chunks = append(chunks, append([]provider.Message(nil), candidate...))
			chunkStart = turnEnd
		}
	}
	if chunkStart < len(messages) {
		chunks = append(chunks, append([]provider.Message(nil), messages[chunkStart:]...))
	}
	if len(chunks) == 0 {
		return [][]provider.Message{append([]provider.Message(nil), messages...)}
	}
	return chunks
}

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

// generateMapReduceCompactSummary slices long ranges, summarizes chunks in
// parallel, then merges. Returns summary text and source label (llm or llm-mapreduce).
func (a *Agent) generateMapReduceCompactSummary(ctx context.Context, messages []provider.Message, turnProvider providerSnapshot, opts CompactSessionOptions) (string, string, error) {
	if len(messages) == 0 {
		return "", "", fmt.Errorf("compact session: no textual content to summarize")
	}
	compactProvider := a.resolveCompactProvider(turnProvider)
	if !compactProvider.valid() {
		return "", "", fmt.Errorf("compact session: provider is not initialized")
	}

	est := a.contextEst
	if est == nil {
		est = contextx.NewTokenEstimator(4096)
	}
	maxChunkTokens, maxParallel := a.compactConfigLimits()
	chunks := sliceCompactInputChunks(messages, est, maxChunkTokens)
	if len(chunks) <= 1 {
		emitCompactProgress(opts, CompactProgress{
			Phase:   "progress",
			Message: "Summarizing conversation context…",
			Model:   compactProvider.model,
			Chunk:   1,
			Chunks:  1,
		})
		summary, err := a.chatCompactSummaryOnce(ctx, compactProvider, messages, compactSegmentPromptKind)
		if err != nil {
			return "", "", err
		}
		return summary, "llm", nil
	}

	logger.Info("compact map-reduce start",
		"chunks", len(chunks),
		"max_parallel", maxParallel,
		"max_chunk_tokens", maxChunkTokens,
		"model", compactProvider.model,
	)
	emitCompactProgress(opts, CompactProgress{
		Phase:   "progress",
		Message: fmt.Sprintf("Compressing context in %d parallel chunks…", len(chunks)),
		Chunks:  len(chunks),
		Model:   compactProvider.model,
	})

	type chunkResult struct {
		index   int
		summary string
		source  string
		err     error
	}
	results := make([]chunkResult, len(chunks))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for i := range chunks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			emitCompactProgress(opts, CompactProgress{
				Phase:   "progress",
				Message: fmt.Sprintf("Compressing context · chunk %d/%d", i+1, len(chunks)),
				Chunk:   i + 1,
				Chunks:  len(chunks),
				Model:   compactProvider.model,
			})
			summary, err := a.chatCompactSummaryOnce(ctx, compactProvider, chunks[i], compactSegmentPromptKind)
			source := "llm"
			if err != nil {
				logger.Warn("compact chunk LLM failed, trying local",
					"chunk", i+1, "chunks", len(chunks), "error", err)
				local := generateLocalCompactSummary(chunks[i], est)
				if strings.TrimSpace(local) == "" {
					results[i] = chunkResult{index: i, err: err}
					return
				}
				if validation := validateCompactSummary(local, chunks[i]); !validation.Valid {
					results[i] = chunkResult{index: i, err: fmt.Errorf("chunk %d local fallback invalid: %s", i+1, validation.Reason)}
					return
				}
				summary = local
				source = "local"
				err = nil
			}
			results[i] = chunkResult{index: i, summary: summary, source: source, err: err}
		}(i)
	}
	wg.Wait()

	segmentSummaries := make([]string, 0, len(chunks))
	usedLocal := 0
	for i, r := range results {
		if r.err != nil {
			return "", "", fmt.Errorf("compact session: chunk %d/%d: %w", i+1, len(chunks), r.err)
		}
		if strings.TrimSpace(r.summary) == "" {
			return "", "", fmt.Errorf("compact session: chunk %d/%d produced empty summary", i+1, len(chunks))
		}
		if r.source == "local" {
			usedLocal++
		}
		segmentSummaries = append(segmentSummaries, fmt.Sprintf("### Segment %d/%d\n%s", i+1, len(chunks), strings.TrimSpace(r.summary)))
	}

	emitCompactProgress(opts, CompactProgress{
		Phase:   "progress",
		Message: fmt.Sprintf("Merging %d compact segments…", len(segmentSummaries)),
		Chunks:  len(chunks),
		Model:   compactProvider.model,
	})
	merged, err := a.chatCompactMergeOnce(ctx, compactProvider, segmentSummaries)
	if err != nil {
		logger.Warn("compact merge LLM failed, trying local merge", "error", err, "segments", len(segmentSummaries))
		merged = mergeCompactSummariesLocal(segmentSummaries)
		if strings.TrimSpace(merged) == "" {
			return "", "", fmt.Errorf("compact session: merge summaries: %w", err)
		}
	}
	source := "llm-mapreduce"
	if usedLocal == len(chunks) {
		source = "local-mapreduce"
	} else if usedLocal > 0 {
		source = "llm-mapreduce-hybrid"
	}
	return merged, source, nil
}

const (
	compactSegmentPromptKind = "segment"
	compactMergePromptKind   = "merge"
)

func (a *Agent) chatCompactSummaryOnce(ctx context.Context, snap providerSnapshot, messages []provider.Message, kind string) (string, error) {
	transcript := compactTranscript(messages)
	if strings.TrimSpace(transcript) == "" {
		return "", fmt.Errorf("compact session: no textual content to summarize")
	}
	prompt := compactSegmentUserPrompt(transcript)
	return a.invokeCompactChat(ctx, snap, prompt, len(messages), utf8.RuneCountInString(transcript), kind)
}

func (a *Agent) chatCompactMergeOnce(ctx context.Context, snap providerSnapshot, segmentSummaries []string) (string, error) {
	body := strings.Join(segmentSummaries, "\n\n")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("compact session: no segment summaries to merge")
	}
	// Keep merge input bounded even when many chunks return long text.
	body = truncateRunes(body, compactTranscriptMaxRunes)
	prompt := compactMergeUserPrompt(body)
	return a.invokeCompactChat(ctx, snap, prompt, len(segmentSummaries), utf8.RuneCountInString(body), compactMergePromptKind)
}

func (a *Agent) invokeCompactChat(ctx context.Context, snap providerSnapshot, userPrompt string, unitCount, transcriptRunes int, kind string) (string, error) {
	if !snap.valid() {
		return "", fmt.Errorf("compact session: provider is not initialized")
	}
	sumCtx, cancel := compactSummaryContext(ctx)
	defer cancel()
	logger.Debug("compact summary request",
		"kind", kind,
		"units", unitCount,
		"transcript_runes", transcriptRunes,
		"model", snap.model,
		"timeout", compactSummaryTimeout.String(),
	)
	resp, err := snap.provider.Chat(sumCtx, []provider.Message{
		{Role: "system", Content: "You are a compaction agent. Tool use is not allowed. Produce only a factual text summary for future context."},
		{Role: "user", Content: userPrompt},
	})
	if err != nil {
		return "", fmt.Errorf("compact session: generate summary: %w", err)
	}
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("compact session: empty summary")
	}
	return strings.TrimSpace(resp.Content), nil
}

func compactSegmentUserPrompt(transcript string) string {
	return "Summarize the conversation below so another LuckyAgent instance can continue the current task.\n" +
		"Output plain text only under these headings:\n" +
		"Current user goal:\nCompleted work:\nPending work:\nKey files and functions:\nCommands and test results:\nUser constraints:\nUncertain facts:\n" +
		"Rules:\n" +
		"- Preserve exact file paths, commands, config keys, errors, decisions, and unresolved items.\n" +
		"- Do not invent commands, test results, files, or user preferences.\n" +
		"- Do not include generic advice or commentary.\n" +
		"- Do not request or use tools; this compaction must only produce text.\n" +
		"- If the transcript notes older lines were omitted, focus on the retained recent evidence.\n\n" +
		"Conversation:\n" + transcript
}

func compactMergeUserPrompt(segmentBody string) string {
	return "Merge the segment summaries below into one compact summary for another LuckyAgent instance.\n" +
		"Output plain text only under these headings:\n" +
		"Current user goal:\nCompleted work:\nPending work:\nKey files and functions:\nCommands and test results:\nUser constraints:\nUncertain facts:\n" +
		"Rules:\n" +
		"- Prefer the latest user goal when segments disagree.\n" +
		"- Keep concrete paths, commands, errors, decisions, and unresolved items.\n" +
		"- Deduplicate repeated facts; do not invent new work.\n" +
		"- Do not request or use tools; produce text only.\n\n" +
		"Segment summaries:\n" + segmentBody
}

func mergeCompactSummariesLocal(segmentSummaries []string) string {
	if len(segmentSummaries) == 0 {
		return ""
	}
	if len(segmentSummaries) == 1 {
		// Strip optional ### Segment header.
		body := segmentSummaries[0]
		if idx := strings.Index(body, "\n"); idx >= 0 && strings.HasPrefix(strings.TrimSpace(body), "### ") {
			body = strings.TrimSpace(body[idx+1:])
		}
		return body
	}

	var goals, completed, pending, files, commands, constraints, uncertain []string
	for _, seg := range segmentSummaries {
		body := seg
		if idx := strings.Index(body, "\n"); idx >= 0 && strings.Contains(body[:idx], "### ") {
			body = strings.TrimSpace(body[idx+1:])
		}
		goals = append(goals, sectionBullets(body, "Current user goal")...)
		completed = append(completed, sectionBullets(body, "Completed work")...)
		pending = append(pending, sectionBullets(body, "Pending work")...)
		files = append(files, sectionBullets(body, "Key files and functions")...)
		commands = append(commands, sectionBullets(body, "Commands and test results")...)
		constraints = append(constraints, sectionBullets(body, "User constraints")...)
		uncertain = append(uncertain, sectionBullets(body, "Uncertain facts")...)
	}

	pickGoal := "Continue the current LuckyAgent session from prior segment summaries."
	if len(goals) > 0 {
		pickGoal = strings.TrimPrefix(strings.TrimSpace(goals[len(goals)-1]), "- ")
	}

	var b strings.Builder
	b.WriteString("Current user goal:\n- " + pickGoal + "\n")
	writeLocalSection(&b, "Completed work", utils.DedupStringsLimit(completed, 6), "- Prior segment work was recorded without reliable detail.")
	writeLocalSection(&b, "Pending work", utils.DedupStringsLimit(pending, 4), "- Continue from the latest user goal and verify unfinished work.")
	writeLocalSection(&b, "Key files and functions", utils.DedupStringsLimit(files, 12), "- No explicit file path was found in segment summaries.")
	writeLocalSection(&b, "Commands and test results", utils.DedupStringsLimit(commands, 8), "- No explicit command or tool result was found in segment summaries.")
	writeLocalSection(&b, "User constraints", utils.DedupStringsLimit(constraints, 4), "- Preserve the latest user instructions.")
	writeLocalSection(&b, "Uncertain facts", utils.DedupStringsLimit(uncertain, 4), "- Segment merge used local rules; verify repository state before treating progress as complete.")
	return strings.TrimSpace(b.String())
}

func writeLocalSection(b *strings.Builder, title string, lines []string, fallback string) {
	b.WriteString(title + ":\n")
	if len(lines) == 0 {
		b.WriteString(fallback + "\n")
		return
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "-") {
			line = "- " + line
		}
		b.WriteString(line + "\n")
	}
}

func sectionBullets(summary, heading string) []string {
	summary = strings.TrimSpace(summary)
	if summary == "" || heading == "" {
		return nil
	}
	lower := strings.ToLower(summary)
	headLower := strings.ToLower(heading)
	idx := strings.Index(lower, headLower)
	if idx < 0 {
		return nil
	}
	rest := summary[idx:]
	// Skip heading line.
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:]
	} else {
		return nil
	}
	var out []string
	for _, line := range strings.Split(rest, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		// Next section heading (Title case with trailing colon).
		if strings.HasSuffix(trim, ":") && !strings.HasPrefix(trim, "-") && len(trim) < 80 {
			break
		}
		out = append(out, trim)
	}
	return out
}
