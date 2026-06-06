package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/tool"
)

type benchConfig struct {
	Variant        string
	Scenario       string
	Rounds         int
	Delay          time.Duration
	CaptureDir     string
	OutPath        string
	Provider       string
	Model          string
	APIBase        string
	MaxIterations  int
	Timeout        time.Duration
	AutoApprove    bool
	NoTools        bool
	KeepSessions   bool
	DisabledTools  string
	PromptOverride string
}

type benchRecord struct {
	Type                  string    `json:"type"`
	Variant               string    `json:"variant"`
	Scenario              string    `json:"scenario"`
	Round                 int       `json:"round"`
	Prompt                string    `json:"prompt"`
	SessionID             string    `json:"session_id"`
	StartedAt             time.Time `json:"started_at"`
	DurationMS            int64     `json:"duration_ms"`
	Iterations            int       `json:"iterations"`
	ToolCalls             int       `json:"tool_calls"`
	ToolNames             []string  `json:"tool_names,omitempty"`
	ResponseChars         int       `json:"response_chars"`
	ProviderCalls         int       `json:"provider_calls"`
	CaptureFiles          []string  `json:"capture_files,omitempty"`
	MissingUsageCalls     int       `json:"missing_usage_calls,omitempty"`
	PromptTokens          int       `json:"prompt_tokens"`
	CachedPromptTokens    int       `json:"cached_prompt_tokens"`
	CacheCreation5MTokens int       `json:"cache_creation_5m_tokens,omitempty"`
	CacheCreation1HTokens int       `json:"cache_creation_1h_tokens,omitempty"`
	CompletionTokens      int       `json:"completion_tokens"`
	TotalTokens           int       `json:"total_tokens"`
	UncachedPromptTokens  int       `json:"uncached_prompt_tokens"`
	CachedRatio           float64   `json:"cached_ratio"`
	Error                 string    `json:"error,omitempty"`
	SleepBeforeNextMS     int64     `json:"sleep_before_next_ms,omitempty"`
}

type benchSummary struct {
	Type                  string  `json:"type"`
	Variant               string  `json:"variant"`
	Scenario              string  `json:"scenario"`
	Rounds                int     `json:"rounds"`
	Errors                int     `json:"errors"`
	ProviderCalls         int     `json:"provider_calls"`
	MissingUsageCalls     int     `json:"missing_usage_calls"`
	AvgDurationMS         float64 `json:"avg_duration_ms"`
	AvgPromptTokens       float64 `json:"avg_prompt_tokens"`
	AvgCachedPromptTokens float64 `json:"avg_cached_prompt_tokens"`
	AvgUncachedTokens     float64 `json:"avg_uncached_prompt_tokens"`
	CachedRatio           float64 `json:"cached_ratio"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "lh-cache-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultCapture := filepath.Join(os.TempDir(), "lh-cache-bench", "capture-"+now)
	defaultOut := filepath.Join(os.TempDir(), "lh-cache-bench", "results-"+now+".jsonl")

	var cfg benchConfig
	flag.StringVar(&cfg.Variant, "variant", "manual", "label written to each benchmark record, e.g. baseline or fixed")
	flag.StringVar(&cfg.Scenario, "scenario", "same-session", "scenario to run: single, same-session, tool, or all")
	flag.IntVar(&cfg.Rounds, "rounds", 5, "rounds per scenario")
	flag.DurationVar(&cfg.Delay, "delay", 0, "delay between rounds, e.g. 65s to force minute-level timestamp drift")
	flag.StringVar(&cfg.CaptureDir, "capture-dir", envOrDefault("LH_UPSTREAM_CAPTURE_DIR", defaultCapture), "directory for upstream request/response captures")
	flag.StringVar(&cfg.OutPath, "out", defaultOut, "JSONL output path")
	flag.StringVar(&cfg.Provider, "provider", "", "temporary provider override")
	flag.StringVar(&cfg.Model, "model", "", "temporary model override")
	flag.StringVar(&cfg.APIBase, "api-base", "", "temporary API base override")
	flag.IntVar(&cfg.MaxIterations, "max-iterations", 3, "agent loop max iterations per round")
	flag.DurationVar(&cfg.Timeout, "timeout", 60*time.Second, "timeout per agent loop iteration")
	flag.BoolVar(&cfg.AutoApprove, "auto-approve", true, "auto-approve tool calls during benchmark")
	flag.BoolVar(&cfg.NoTools, "no-tools", false, "hide all model-visible tools for the benchmark call options")
	flag.BoolVar(&cfg.KeepSessions, "keep-sessions", false, "keep benchmark sessions in the normal LuckyHarness session store")
	flag.StringVar(&cfg.DisabledTools, "disable-tools", "", "comma-separated model-visible tool names to hide")
	flag.StringVar(&cfg.PromptOverride, "prompt", "", "fixed prompt override for all rounds")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	if cfg.Rounds <= 0 {
		return fmt.Errorf("rounds must be positive")
	}
	if strings.TrimSpace(cfg.CaptureDir) == "" {
		return fmt.Errorf("capture-dir must not be empty")
	}
	if err := os.MkdirAll(cfg.CaptureDir, 0o700); err != nil {
		return fmt.Errorf("create capture dir: %w", err)
	}
	if err := os.Setenv("LH_UPSTREAM_CAPTURE_DIR", cfg.CaptureDir); err != nil {
		return fmt.Errorf("set LH_UPSTREAM_CAPTURE_DIR: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.OutPath), 0o700); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	out, err := os.OpenFile(cfg.OutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open output: %w", err)
	}
	defer out.Close()
	enc := json.NewEncoder(out)

	mgr, err := config.NewManager()
	if err != nil {
		return fmt.Errorf("new config manager: %w", err)
	}
	if err := mgr.Load(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	applyConfigOverrides(mgr, cfg)

	a, err := agent.New(mgr)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()

	scenarios, err := expandScenarios(cfg.Scenario)
	if err != nil {
		return err
	}

	var all []benchRecord
	for _, scenario := range scenarios {
		records, err := runScenario(context.Background(), a, cfg, scenario, enc)
		all = append(all, records...)
		if err != nil {
			return err
		}
		summary := summarizeRecords(cfg.Variant, scenario, records)
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write summary: %w", err)
		}
		printSummary(summary)
	}

	if len(scenarios) > 1 {
		summary := summarizeRecords(cfg.Variant, "all", all)
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write aggregate summary: %w", err)
		}
		printSummary(summary)
	}

	fmt.Fprintf(os.Stderr, "results: %s\ncapture: %s\n", cfg.OutPath, cfg.CaptureDir)
	return nil
}

func applyConfigOverrides(mgr *config.Manager, cfg benchConfig) {
	if strings.TrimSpace(cfg.Provider) != "" {
		_ = mgr.Set("provider", strings.TrimSpace(cfg.Provider))
	}
	if strings.TrimSpace(cfg.Model) != "" {
		_ = mgr.Set("model", strings.TrimSpace(cfg.Model))
	}
	if strings.TrimSpace(cfg.APIBase) != "" {
		_ = mgr.Set("api_base", strings.TrimSpace(cfg.APIBase))
	}
}

func expandScenarios(raw string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "single", "same-session", "tool":
		return []string{strings.ToLower(strings.TrimSpace(raw))}, nil
	case "all":
		return []string{"single", "same-session", "tool"}, nil
	default:
		return nil, fmt.Errorf("unknown scenario %q", raw)
	}
}

func runScenario(ctx context.Context, a *agent.Agent, cfg benchConfig, scenario string, enc *json.Encoder) ([]benchRecord, error) {
	if a == nil {
		return nil, errors.New("agent is nil")
	}
	sessionMgr := a.Sessions()
	if sessionMgr == nil {
		return nil, errors.New("agent session manager is nil")
	}

	disabled := parseCSV(cfg.DisabledTools)
	if cfg.NoTools {
		disabled = append(disabled, modelVisibleToolNames(a.Tools())...)
	}
	if scenario == "single" {
		disabled = append(disabled, "shell")
	}
	disabled = dedupStrings(disabled)

	loopCfg := agent.DefaultLoopConfig()
	loopCfg.MaxIterations = cfg.MaxIterations
	loopCfg.Timeout = cfg.Timeout
	loopCfg.AutoApprove = cfg.AutoApprove
	loopCfg.DisabledTools = disabled
	loopCfg.Ephemeral = true

	var sharedSessionID string
	var createdSessionIDs []string
	defer func() {
		if cfg.KeepSessions {
			return
		}
		for _, id := range createdSessionIDs {
			_ = sessionMgr.Delete(id)
		}
	}()
	if scenario == "same-session" || scenario == "tool" {
		sess := sessionMgr.NewWithTitle("cache-bench " + scenario + " " + time.Now().Format(time.RFC3339))
		sharedSessionID = sess.ID
		createdSessionIDs = append(createdSessionIDs, sess.ID)
	}

	records := make([]benchRecord, 0, cfg.Rounds)
	for i := 1; i <= cfg.Rounds; i++ {
		if i > 1 && cfg.Delay > 0 {
			time.Sleep(cfg.Delay)
		}

		sess := sessionMgr.NewWithTitle("cache-bench " + scenario + " round")
		if sharedSessionID == "" {
			createdSessionIDs = append(createdSessionIDs, sess.ID)
		}
		if sharedSessionID != "" {
			var ok bool
			sess, ok = sessionMgr.Get(sharedSessionID)
			if !ok {
				return records, fmt.Errorf("session disappeared: %s", sharedSessionID)
			}
		}

		before, err := scanCapturePrefixes(cfg.CaptureDir)
		if err != nil {
			return records, err
		}
		started := time.Now()
		prompt := scenarioPrompt(scenario, i, cfg.PromptOverride)
		roundCtx, cancel := context.WithTimeout(ctx, cfg.Timeout*time.Duration(maxInt(1, cfg.MaxIterations)+1))
		result, runErr := a.RunLoopWithSessionInput(roundCtx, sess, agent.TextUserTurnInput(prompt), loopCfg)
		cancel()
		duration := time.Since(started)

		after, err := scanCapturePrefixes(cfg.CaptureDir)
		if err != nil {
			return records, err
		}
		prefixes := diffCapturePrefixes(before, after)
		usage, files, missing := aggregateCaptureUsage(prefixes)

		record := benchRecord{
			Type:                  "round",
			Variant:               cfg.Variant,
			Scenario:              scenario,
			Round:                 i,
			Prompt:                prompt,
			SessionID:             sess.ID,
			StartedAt:             started,
			DurationMS:            duration.Milliseconds(),
			ProviderCalls:         len(prefixes),
			CaptureFiles:          files,
			MissingUsageCalls:     missing,
			PromptTokens:          usage.PromptTokens,
			CachedPromptTokens:    usage.CachedPromptTokens,
			CacheCreation5MTokens: usage.CacheCreation5MTokens,
			CacheCreation1HTokens: usage.CacheCreation1HTokens,
			CompletionTokens:      usage.CompletionTokens,
			TotalTokens:           usage.TotalTokens,
		}
		record.UncachedPromptTokens = maxInt(0, record.PromptTokens-record.CachedPromptTokens)
		if record.PromptTokens > 0 {
			record.CachedRatio = float64(record.CachedPromptTokens) / float64(record.PromptTokens)
		}
		if i < cfg.Rounds && cfg.Delay > 0 {
			record.SleepBeforeNextMS = cfg.Delay.Milliseconds()
		}
		if result != nil {
			record.Iterations = result.Iterations
			record.ToolCalls = len(result.ToolCalls)
			for _, call := range result.ToolCalls {
				if strings.TrimSpace(call.Name) != "" {
					record.ToolNames = append(record.ToolNames, call.Name)
				}
			}
			record.ResponseChars = len([]rune(result.Response))
		}
		if runErr != nil {
			record.Error = runErr.Error()
		}

		if err := enc.Encode(record); err != nil {
			return records, fmt.Errorf("write record: %w", err)
		}
		fmt.Fprintf(os.Stderr, "%s round=%d prompt=%d cached=%d ratio=%.1f%% calls=%d err=%v\n",
			scenario,
			i,
			record.PromptTokens,
			record.CachedPromptTokens,
			record.CachedRatio*100,
			record.ProviderCalls,
			runErr,
		)
		records = append(records, record)
	}
	return records, nil
}

func scenarioPrompt(scenario string, round int, override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	switch scenario {
	case "single":
		return "Reply with exactly this sentence: cache benchmark ready."
	case "same-session":
		return "Continue the cache benchmark with one concise sentence. Do not call tools."
	case "tool":
		return "Use the shell tool to run `pwd`, then answer with only the directory path."
	default:
		return fmt.Sprintf("Cache benchmark round %d. Reply in one concise sentence.", round)
	}
}

func summarizeRecords(variant, scenario string, records []benchRecord) benchSummary {
	s := benchSummary{
		Type:     "summary",
		Variant:  variant,
		Scenario: scenario,
		Rounds:   len(records),
	}
	var duration, prompt, cached, uncached int
	for _, r := range records {
		duration += int(r.DurationMS)
		prompt += r.PromptTokens
		cached += r.CachedPromptTokens
		uncached += r.UncachedPromptTokens
		s.ProviderCalls += r.ProviderCalls
		s.MissingUsageCalls += r.MissingUsageCalls
		if r.Error != "" {
			s.Errors++
		}
	}
	if len(records) > 0 {
		n := float64(len(records))
		s.AvgDurationMS = float64(duration) / n
		s.AvgPromptTokens = float64(prompt) / n
		s.AvgCachedPromptTokens = float64(cached) / n
		s.AvgUncachedTokens = float64(uncached) / n
	}
	if prompt > 0 {
		s.CachedRatio = float64(cached) / float64(prompt)
	}
	return s
}

func printSummary(s benchSummary) {
	fmt.Fprintf(os.Stderr,
		"summary scenario=%s rounds=%d calls=%d avg_prompt=%.0f avg_cached=%.0f ratio=%.1f%% errors=%d missing_usage=%d\n",
		s.Scenario,
		s.Rounds,
		s.ProviderCalls,
		s.AvgPromptTokens,
		s.AvgCachedPromptTokens,
		s.CachedRatio*100,
		s.Errors,
		s.MissingUsageCalls,
	)
}

func modelVisibleToolNames(reg *tool.Registry) []string {
	if reg == nil {
		return nil
	}
	tools := reg.ListModelVisible()
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil && strings.TrimSpace(t.Name) != "" {
			names = append(names, t.Name)
		}
	}
	return names
}

func parseCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func dedupStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
