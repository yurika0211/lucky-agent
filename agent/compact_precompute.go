package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/provider"
)

// The published runtime ContextConfig cannot carry these keys. They live in
// config.example.json and are read here with the defaults from #528.
const (
	defaultCompactBackground             = true
	defaultCompactPrecomputeLeadTokens   = 60000
	defaultCompactPrecomputeMaxFailures  = 2
	defaultCompactCacheMarginSeconds     = 30
	defaultCompactKeepTokens             = 15000
	defaultCompactChunkFallback          = true
	defaultCompactCacheLifetime          = 5 * time.Minute
	compactEvidenceBlockHeader           = "Source evidence:"
)

type compactSettings struct {
	Background       bool
	LeadTokens       int
	MaxFailures      int
	CacheMargin      time.Duration
	KeepTokens       int
	ChunkFallback    bool
	CacheLifetime    time.Duration
}

func (a *Agent) compactSettings() compactSettings {
	settings := compactSettings{
		Background:    defaultCompactBackground,
		LeadTokens:    defaultCompactPrecomputeLeadTokens,
		MaxFailures:   defaultCompactPrecomputeMaxFailures,
		CacheMargin:   defaultCompactCacheMarginSeconds * time.Second,
		KeepTokens:    defaultCompactKeepTokens,
		ChunkFallback: defaultCompactChunkFallback,
		CacheLifetime: defaultCompactCacheLifetime,
	}
	if a == nil || a.cfg == nil {
		return settings
	}
	cfg := a.cfg.Get()
	if cfg == nil {
		return settings
	}
	extra := cfg.Extra
	if !compactExtraBool(extra, "context.compact_background", settings.Background) {
		settings.Background = false
	}
	if n, ok := compactExtraInt(extra, "context.compact_precompute_lead_tokens"); ok && n > 0 {
		settings.LeadTokens = n
	}
	if n, ok := compactExtraInt(extra, "context.compact_precompute_max_failures"); ok && n > 0 {
		settings.MaxFailures = n
	}
	if n, ok := compactExtraInt(extra, "context.compact_cache_margin_seconds"); ok && n >= 0 {
		settings.CacheMargin = time.Duration(n) * time.Second
	}
	if n, ok := compactExtraInt(extra, "context.compact_keep_tokens"); ok && n > 0 {
		settings.KeepTokens = n
	}
	settings.ChunkFallback = compactExtraBool(extra, "context.compact_chunk_fallback", settings.ChunkFallback)
	return settings
}

func compactExtraBool(extra map[string]string, key string, fallback bool) bool {
	raw, ok := extra[key]
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func compactExtraInt(extra map[string]string, key string) (int, bool) {
	raw, ok := extra[key]
	if !ok {
		return 0, false
	}
	n := 0
	for _, r := range strings.TrimSpace(raw) {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// precomputedCompact is one in-memory summary for one session anchor.
// It is discarded when the session is deleted, rolled back, or the swap
// checks fail. It is never written by itself.
type precomputedCompact struct {
	anchor    string
	model     string
	summary   string
	evidence  string
	source    string
	tailLimit int
	ready     bool
	err       error
	done      chan struct{}
}

type compactPrecomputeState struct {
	mu         sync.Mutex
	current    map[string]*precomputedCompact
	failures   map[string]int
	stopped    map[string]bool
	generation    map[string]uint64
	measuredAt    map[string]time.Time
	cacheLifetime time.Duration
}

func (a *Agent) compactPrecompute() *compactPrecomputeState {
	if a.precompute == nil {
		a.precompute = &compactPrecomputeState{
			current:    map[string]*precomputedCompact{},
			failures:   map[string]int{},
			stopped:    map[string]bool{},
			generation: map[string]uint64{},
			measuredAt: map[string]time.Time{},
		}
	}
	return a.precompute
}

func (a *Agent) noteMeasuredPrompt(sessionID string, at time.Time) {
	if a == nil || sessionID == "" || at.IsZero() {
		return
	}
	state := a.compactPrecompute()
	state.mu.Lock()
	state.measuredAt[sessionID] = at
	state.mu.Unlock()
}

// noteTurnFinished records that this session's provider turn just finished.
// Cache heat is the time since that response, not whether the provider sent
// a token count. Native streams often omit usage.
func (a *Agent) noteTurnFinished(sessionID string) {
	if a == nil || sessionID == "" {
		return
	}
	a.noteMeasuredPrompt(sessionID, time.Now())
	if ttl := a.modelCacheLifetime(); ttl > 0 {
		state := a.compactPrecompute()
		state.mu.Lock()
		state.cacheLifetime = ttl
		state.mu.Unlock()
	}
}

// recordMeasuredPromptUsage keeps the old name for tests that already pass a
// session id. Empty usage still counts: the response happened.
func (a *Agent) recordMeasuredPromptUsage(sessionID string, usage *provider.TokenUsage) {
	if a == nil || sessionID == "" {
		return
	}
	_ = usage
	a.noteTurnFinished(sessionID)
}

func (a *Agent) activeCompactSessionID() string {
	if a == nil {
		return ""
	}
	a.precomputeMu.RLock()
	id := a.measuredSessionID
	a.precomputeMu.RUnlock()
	return id
}

func (a *Agent) setMeasuredSession(sessionID string) {
	if a == nil {
		return
	}
	a.precomputeMu.Lock()
	a.measuredSessionID = sessionID
	a.precomputeMu.Unlock()
}

func (a *Agent) modelCacheLifetime() time.Duration {
	if a == nil || a.cfg == nil {
		return 0
	}
	cfg := a.cfg.Get()
	if cfg == nil {
		return 0
	}
	if n, ok := compactExtraInt(cfg.Extra, "context.compact_cache_lifetime_seconds"); ok && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

func (a *Agent) invalidatePrecomputedCompact(sessionID, reason string) {
	if a == nil || sessionID == "" {
		return
	}
	state := a.compactPrecompute()
	state.mu.Lock()
	delete(state.current, sessionID)
	state.generation[sessionID]++
	state.mu.Unlock()
	if reason != "" {
		logger.Info("precomputed compact discarded", "session_id", sessionID, "reason", reason)
	}
}

// maybeStartCompactPrecompute starts one background summary when the context
// is within the lead of the ceiling. Manual compact does not call this.
func (a *Agent) maybeStartCompactPrecompute(ctx context.Context, sessionID, anchor, model string, estimated, ceiling int, messages []provider.Message, turnProvider providerSnapshot) {
	if a == nil || sessionID == "" || anchor == "" || len(messages) == 0 {
		return
	}
	settings := a.compactSettings()
	if !settings.Background {
		return
	}
	state := a.compactPrecompute()
	state.mu.Lock()
	if state.cacheLifetime > 0 {
		settings.CacheLifetime = state.cacheLifetime
	}
	if state.stopped[sessionID] {
		state.mu.Unlock()
		return
	}
	if held := state.current[sessionID]; held != nil && held.anchor == anchor {
		state.mu.Unlock()
		return
	}
	measured, measuredOK := state.measuredAt[sessionID]
	state.mu.Unlock()
	if !measuredOK || measured.IsZero() {
		logger.Info("compact precompute skipped: no measurement", "session_id", sessionID, "reason", "no measurement")
		return
	}
	if time.Since(measured) > settings.CacheLifetime-settings.CacheMargin {
		logger.Info("compact precompute skipped: cache expired", "session_id", sessionID, "reason", "measured prompt response is older than cache lifetime minus margin")
		return
	}
	state.mu.Lock()
	lead := settings.LeadTokens
	cycle := ceiling
	if cycle < 0 {
		cycle = 0
	}
	if half := cycle / 2; half < lead {
		lead = half
	}
	if estimated < ceiling-lead {
		state.mu.Unlock()
		return
	}
	state.generation[sessionID]++
	generation := state.generation[sessionID]
	job := &precomputedCompact{anchor: anchor, model: model, tailLimit: settings.KeepTokens, done: make(chan struct{})}
	state.current[sessionID] = job
	state.mu.Unlock()

	go func() {
		defer close(job.done)
		summary, evidence, source, err := a.summarizeCompactRange(context.WithoutCancel(ctx), messages, turnProvider, CompactSessionOptions{})
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.generation[sessionID] != generation {
			return
		}
		if err != nil {
			state.failures[sessionID]++
			job.err = err
			delete(state.current, sessionID)
			if state.failures[sessionID] >= settings.MaxFailures {
				state.stopped[sessionID] = true
				logger.Info("precomputed compact stopped", "session_id", sessionID, "failures", state.failures[sessionID])
			}
			return
		}
		state.failures[sessionID] = 0
		job.summary = summary
		job.evidence = evidence
		job.source = source
		job.ready = true
	}()
}

// takePrecomputedCompact returns a ready summary only when the anchor, model,
// and tail still match. A miss leaves the caller on the synchronous path.
func (a *Agent) takePrecomputedCompact(sessionID, anchor, model string, tailTokens, tailLimit int) (summary, evidence, source string, ok bool) {
	if a == nil || a.precompute == nil {
		return "", "", "", false
	}
	state := a.precompute
	state.mu.Lock()
	job := state.current[sessionID]
	state.mu.Unlock()
	if job == nil {
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "no precomputed summary")
		return "", "", "", false
	}
	select {
	case <-job.done:
	default:
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "precomputed summary not ready")
		return "", "", "", false
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.current[sessionID] != job {
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "precomputed summary replaced")
		return "", "", "", false
	}
	delete(state.current, sessionID)
	switch {
	case job.err != nil || !job.ready:
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "precomputed summary failed")
	case job.anchor != anchor:
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "precomputed summary start does not match current history")
	case job.model != model:
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "precomputed summary model does not match")
	case tailTokens > tailLimit || tailTokens > job.tailLimit:
		logger.Info("compact path", "session_id", sessionID, "mode", "synchronous", "reason", "precomputed summary tail exceeds tailLimit")
	default:
		logger.Info("compact path", "session_id", sessionID, "mode", "precomputed")
		return job.summary, job.evidence, job.source, true
	}
	return "", "", "", false
}

func compactAnchor(messages []provider.Message) string {
	if len(messages) == 0 {
		return ""
	}
	return compactRangeFingerprint(messages, 0, 1) + ":" + compactRangeFingerprint(messages, 0, len(messages))
}

func compactModelName(turn providerSnapshot) string {
	return strings.TrimSpace(turn.model)
}
