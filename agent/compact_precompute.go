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
		a.logCompactSettings(settings, "no config manager", compactDefaultKeys(settings), nil)
		return settings
	}
	cfg := a.cfg.Get()
	if cfg == nil {
		a.logCompactSettings(settings, "empty config", compactDefaultKeys(settings), nil)
		return settings
	}

	// #531: these six keys are formal config.ContextConfig fields now, so read
	// them from there. Reading Extra first meant `lh config set` wrote a key
	// the compaction path could not see.
	resolved := cfg.Context.CompactSettings()
	settings.Background = resolved.Background
	settings.LeadTokens = resolved.PrecomputeLeadTokens
	settings.MaxFailures = resolved.PrecomputeMaxFailures
	settings.CacheMargin = time.Duration(resolved.CacheMarginSeconds) * time.Second
	settings.KeepTokens = resolved.KeepTokens
	settings.ChunkFallback = resolved.ChunkFallback

	// Compatibility: config files written before the formal fields existed carry
	// the same keys in Extra. Apply them only while the formal field still holds
	// its default, so an explicit setting always wins.
	var legacy []string
	extra := cfg.Extra
	if _, ok := extra["context.compact_background"]; ok && settings.Background == defaultCompactBackground {
		settings.Background = compactExtraBool(extra, "context.compact_background", settings.Background)
		legacy = append(legacy, "compact_background")
	}
	if n, ok := compactExtraInt(extra, "context.compact_precompute_lead_tokens"); ok && n > 0 && settings.LeadTokens == defaultCompactPrecomputeLeadTokens {
		settings.LeadTokens = n
		legacy = append(legacy, "compact_precompute_lead_tokens")
	}
	if n, ok := compactExtraInt(extra, "context.compact_precompute_max_failures"); ok && n > 0 && settings.MaxFailures == defaultCompactPrecomputeMaxFailures {
		settings.MaxFailures = n
		legacy = append(legacy, "compact_precompute_max_failures")
	}
	if n, ok := compactExtraInt(extra, "context.compact_cache_margin_seconds"); ok && n >= 0 && int(settings.CacheMargin/time.Second) == defaultCompactCacheMarginSeconds {
		settings.CacheMargin = time.Duration(n) * time.Second
		legacy = append(legacy, "compact_cache_margin_seconds")
	}
	if n, ok := compactExtraInt(extra, "context.compact_keep_tokens"); ok && n > 0 && settings.KeepTokens == defaultCompactKeepTokens {
		settings.KeepTokens = n
		legacy = append(legacy, "compact_keep_tokens")
	}
	if _, ok := extra["context.compact_chunk_fallback"]; ok && settings.ChunkFallback == defaultCompactChunkFallback {
		settings.ChunkFallback = compactExtraBool(extra, "context.compact_chunk_fallback", settings.ChunkFallback)
		legacy = append(legacy, "compact_chunk_fallback")
	}

	a.logCompactSettings(settings, "", compactDefaultKeys(settings), legacy)
	return settings
}

// compactDefaultKeys names the knobs still sitting on the built-in defaults, so
// the one-shot log can say which values came from the config and which did not.
func compactDefaultKeys(settings compactSettings) []string {
	var keys []string
	if settings.Background == defaultCompactBackground {
		keys = append(keys, "compact_background")
	}
	if settings.LeadTokens == defaultCompactPrecomputeLeadTokens {
		keys = append(keys, "compact_precompute_lead_tokens")
	}
	if settings.MaxFailures == defaultCompactPrecomputeMaxFailures {
		keys = append(keys, "compact_precompute_max_failures")
	}
	if int(settings.CacheMargin/time.Second) == defaultCompactCacheMarginSeconds {
		keys = append(keys, "compact_cache_margin_seconds")
	}
	if settings.KeepTokens == defaultCompactKeepTokens {
		keys = append(keys, "compact_keep_tokens")
	}
	if settings.ChunkFallback == defaultCompactChunkFallback {
		keys = append(keys, "compact_chunk_fallback")
	}
	return keys
}

// compactSettingsLogOnce keeps this out of the turn loop but still leaves one
// line per process saying what compaction actually used.
var compactSettingsLogOnce sync.Once

func (a *Agent) logCompactSettings(settings compactSettings, reason string, fromDefault, legacy []string) {
	compactSettingsLogOnce.Do(func() {
		fields := []any{
			"background", settings.Background,
			"precompute_lead_tokens", settings.LeadTokens,
			"precompute_max_failures", settings.MaxFailures,
			"cache_margin_seconds", int(settings.CacheMargin / time.Second),
			"keep_tokens", settings.KeepTokens,
			"chunk_fallback", settings.ChunkFallback,
		}
		if reason != "" {
			fields = append(fields, "reason", reason)
		}
		if len(fromDefault) > 0 {
			fields = append(fields, "using_default", strings.Join(fromDefault, ","))
		}
		if len(legacy) > 0 {
			fields = append(fields, "legacy_extra", strings.Join(legacy, ","))
		}
		logger.Info("compact config: resolved", fields...)
	})
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

// recordMeasuredPromptUsage stores the time of a provider response that
// reported prompt tokens. recordUsageAndTTFT is the only caller. A response
// with no prompt tokens is not a measurement.
func (a *Agent) recordMeasuredPromptUsage(sessionID string, usage *provider.TokenUsage) {
	if a == nil || sessionID == "" || usage == nil {
		return
	}
	if usage.InputTokens <= 0 && usage.TotalTokens <= 0 {
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
