package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
)

type benchConfig struct {
	Variant     string
	Scenario    string
	TracePath   string
	TraceOnly   bool
	OutPath     string
	Rounds      int
	Delay       time.Duration
	MaxTokens   int
	KeepHome    bool
	MinQuality  float64
	MaxNoise    float64
	MaxP95MS    float64
	GoldenTrace bool
}

type benchRecord struct {
	Type                  string              `json:"type"`
	Variant               string              `json:"variant"`
	Scenario              string              `json:"scenario"`
	Round                 int                 `json:"round"`
	QueryID               string              `json:"query_id"`
	Query                 string              `json:"query"`
	StartedAt             time.Time           `json:"started_at"`
	DurationNS            int64               `json:"duration_ns"`
	DurationMS            float64             `json:"duration_ms"`
	MessageCount          int                 `json:"message_count"`
	PromptTokens          int                 `json:"prompt_tokens"`
	BucketTokens          map[string]int      `json:"bucket_tokens"`
	BucketCounts          map[string]int      `json:"bucket_counts"`
	GoldenMemoryCount     int                 `json:"golden_memory_count"`
	GoldenConstraintCount int                 `json:"golden_constraint_count"`
	GoldenWarningCount    int                 `json:"golden_warning_count"`
	GoldenToolCount       int                 `json:"golden_tool_count"`
	GoldenEvidenceCount   int                 `json:"golden_evidence_count"`
	MemoryHitCount        int                 `json:"memory_hit_count"`
	ConstraintHitCount    int                 `json:"constraint_hit_count"`
	WarningHitCount       int                 `json:"warning_hit_count"`
	ToolHitCount          int                 `json:"tool_hit_count"`
	EvidenceHitCount      int                 `json:"evidence_hit_count"`
	NoiseHitCount         int                 `json:"noise_hit_count"`
	CMR                   float64             `json:"critical_memory_retention"`
	CR                    float64             `json:"constraint_retention"`
	TWR                   float64             `json:"temporal_warning_retention"`
	ToolRecall            float64             `json:"tool_recall"`
	ERR                   float64             `json:"evidence_ref_retention"`
	ContextNoise          float64             `json:"context_noise"`
	TokenEfficiency       float64             `json:"token_efficiency"`
	Quality               float64             `json:"quality"`
	Clean                 bool                `json:"clean"`
	Missing               map[string][]string `json:"missing,omitempty"`
	Error                 string              `json:"error,omitempty"`
	SleepBeforeNextMS     int64               `json:"sleep_before_next_ms,omitempty"`
}

type benchSummary struct {
	Type               string   `json:"type"`
	Variant            string   `json:"variant"`
	Scenario           string   `json:"scenario"`
	Rounds             int      `json:"rounds"`
	Records            int      `json:"records"`
	Errors             int      `json:"errors"`
	Clean              bool     `json:"clean"`
	QualityPass        bool     `json:"quality_pass"`
	LatencyPass        bool     `json:"latency_pass"`
	MinQuality         float64  `json:"min_quality"`
	MaxNoise           float64  `json:"max_noise"`
	MaxP95MS           float64  `json:"max_p95_ms"`
	AvgDurationMS      float64  `json:"avg_duration_ms"`
	P50DurationMS      float64  `json:"p50_duration_ms"`
	P95DurationMS      float64  `json:"p95_duration_ms"`
	AvgPromptTokens    float64  `json:"avg_prompt_tokens"`
	AvgCMR             float64  `json:"avg_critical_memory_retention"`
	AvgCR              float64  `json:"avg_constraint_retention"`
	AvgTWR             float64  `json:"avg_temporal_warning_retention"`
	AvgToolRecall      float64  `json:"avg_tool_recall"`
	AvgERR             float64  `json:"avg_evidence_ref_retention"`
	AvgContextNoise    float64  `json:"avg_context_noise"`
	AvgTokenEfficiency float64  `json:"avg_token_efficiency"`
	AvgQuality         float64  `json:"avg_quality"`
	AvgMemoryTokens    float64  `json:"avg_memory_tokens,omitempty"`
	AvgHistoryTokens   float64  `json:"avg_history_tokens,omitempty"`
	AvgSystemTokens    float64  `json:"avg_system_tokens,omitempty"`
	AvgRAGTokens       float64  `json:"avg_rag_tokens,omitempty"`
	ComparedScenarios  []string `json:"compared_scenarios,omitempty"`
}

type benchDataset struct {
	HomeDir string
	Agent   *agent.Agent
	Cases   []benchCase
}

type benchCase struct {
	ID          string
	Scenario    string
	Query       string
	Session     *session.Session
	Memory      []string
	Constraints []string
	Warnings    []string
	Tools       []string
	Evidence    []string
	Noise       []string
}

type traceFile struct {
	Cases    []traceCase   `json:"cases"`
	Memories []traceMemory `json:"memories,omitempty"`
}

type traceCase struct {
	ID          string             `json:"id"`
	Scenario    string             `json:"scenario"`
	Title       string             `json:"title,omitempty"`
	Query       string             `json:"query"`
	Messages    []provider.Message `json:"messages"`
	Memory      []string           `json:"memory_golden,omitempty"`
	Critical    []string           `json:"critical_memory,omitempty"`
	Constraints []string           `json:"constraints,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
	Tools       []string           `json:"tools,omitempty"`
	Evidence    []string           `json:"evidence,omitempty"`
	Noise       []string           `json:"noise,omitempty"`
	MemoryItems []traceMemory      `json:"memory_entries,omitempty"`
}

type traceMemory struct {
	Content    string   `json:"content"`
	Category   string   `json:"category,omitempty"`
	Tier       string   `json:"tier,omitempty"`
	Importance float64  `json:"importance,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Links      []string `json:"links,omitempty"`
	Status     string   `json:"status,omitempty"`
	StateKey   string   `json:"state_key,omitempty"`
	StateValue string   `json:"state_value,omitempty"`
	Confidence float64  `json:"confidence,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "lh-context-packer-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultOut := filepath.Join(os.TempDir(), "lh-context-packer-bench", "results-"+now+".jsonl")

	var cfg benchConfig
	flag.StringVar(&cfg.Variant, "variant", "manual", "label written to each benchmark record, e.g. baseline or packer-v1")
	flag.StringVar(&cfg.Scenario, "scenario", "all", "scenario to run: one named scenario or all")
	flag.StringVar(&cfg.TracePath, "trace", "", "optional JSON or JSONL trace file with extra benchmark cases")
	flag.BoolVar(&cfg.TraceOnly, "trace-only", false, "run only trace cases from -trace")
	flag.StringVar(&cfg.OutPath, "out", defaultOut, "JSONL output path")
	flag.IntVar(&cfg.Rounds, "rounds", 3, "rounds per scenario")
	flag.DurationVar(&cfg.Delay, "delay", 0, "delay between rounds")
	flag.IntVar(&cfg.MaxTokens, "max-tokens", 4096, "context window used by the isolated benchmark agent")
	flag.BoolVar(&cfg.KeepHome, "keep-home", false, "keep isolated LuckyHarness home on disk")
	flag.Float64Var(&cfg.MinQuality, "min-quality", 0.75, "minimum accepted average quality score")
	flag.Float64Var(&cfg.MaxNoise, "max-noise", 0.25, "maximum accepted average context noise")
	flag.Float64Var(&cfg.MaxP95MS, "max-p95-ms", 10, "maximum accepted p95 context build latency in milliseconds")
	flag.BoolVar(&cfg.GoldenTrace, "golden-trace", false, "print missing golden items for each record")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	if cfg.Rounds <= 0 {
		return fmt.Errorf("rounds must be positive")
	}
	if cfg.MaxTokens <= 0 {
		return fmt.Errorf("max-tokens must be positive")
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

	ds, cleanup, err := loadBenchmarkDataset(cfg)
	if err != nil {
		return err
	}
	defer cleanup()

	scenarios, err := expandScenarios(cfg.Scenario, ds.Cases)
	if err != nil {
		return err
	}

	var all []benchRecord
	for _, scenario := range scenarios {
		records, err := runScenario(context.Background(), cfg, ds, scenario, enc)
		all = append(all, records...)
		if err != nil {
			return err
		}
		summary := summarizeRecords(cfg, scenario, records)
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write summary: %w", err)
		}
		printSummary(summary)
	}
	if len(scenarios) > 1 {
		summary := summarizeRecords(cfg, "all", all)
		summary.ComparedScenarios = scenarios
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write aggregate summary: %w", err)
		}
		printSummary(summary)
	}

	fmt.Fprintf(os.Stderr, "results: %s\nhome: %s\n", cfg.OutPath, ds.HomeDir)
	return nil
}

func expandScenarios(raw string, cases []benchCase) ([]string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	names := scenarioNamesFromCases(cases)
	if raw == "all" {
		return names, nil
	}
	for _, scenario := range names {
		if raw == scenario {
			return []string{raw}, nil
		}
	}
	return nil, fmt.Errorf("unknown scenario %q (available: %s)", raw, strings.Join(names, ", "))
}

func scenarioNamesFromCases(cases []benchCase) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(cases))
	for _, tc := range cases {
		scenario := strings.TrimSpace(tc.Scenario)
		if scenario == "" || seen[scenario] {
			continue
		}
		seen[scenario] = true
		names = append(names, scenario)
	}
	return names
}

func loadBenchmarkDataset(cfg benchConfig) (*benchDataset, func(), error) {
	home, err := os.MkdirTemp("", "lh-context-packer-bench-home-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create isolated home: %w", err)
	}
	cleanup := func() {
		if !cfg.KeepHome {
			_ = os.RemoveAll(home)
		}
	}

	mgr, err := config.NewManagerWithDir(home)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("new config manager: %w", err)
	}
	if err := mgr.InitHome(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("init home: %w", err)
	}
	_ = mgr.Set("provider", "openai")
	_ = mgr.Set("api_key", "sk-lh-context-packer-bench")
	_ = mgr.Set("model", "gpt-5.4-mini")
	_ = mgr.Set("max_tokens", fmt.Sprintf("%d", cfg.MaxTokens))
	_ = mgr.Set("context.compression_threshold", "1000")
	if err := mgr.Save(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("save config: %w", err)
	}

	a, err := agent.New(mgr)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("new agent: %w", err)
	}
	ds := &benchDataset{
		HomeDir: home,
		Agent:   a,
	}
	if err := seedBenchmarkMemory(a.Memory()); err != nil {
		_ = a.Close()
		cleanup()
		return nil, nil, err
	}
	if cfg.TraceOnly && strings.TrimSpace(cfg.TracePath) == "" {
		_ = a.Close()
		cleanup()
		return nil, nil, errors.New("-trace-only requires -trace")
	}
	if !cfg.TraceOnly {
		ds.Cases = buildCases(a.Sessions())
	}
	if strings.TrimSpace(cfg.TracePath) != "" {
		traceCases, err := loadTraceCases(cfg.TracePath, a.Sessions(), a.Memory())
		if err != nil {
			_ = a.Close()
			cleanup()
			return nil, nil, err
		}
		ds.Cases = append(ds.Cases, traceCases...)
	}
	if len(ds.Cases) == 0 {
		_ = a.Close()
		cleanup()
		return nil, nil, errors.New("no benchmark cases loaded")
	}

	return ds, func() {
		_ = a.Close()
		cleanup()
	}, nil
}

func seedBenchmarkMemory(store *memory.Store) error {
	if store == nil {
		return errors.New("memory store is nil")
	}
	records := []struct {
		content    string
		category   string
		tier       memory.Tier
		importance float64
		tags       []string
		links      []string
		opts       memory.SaveOptions
	}{
		{
			content:    "User's daughter has active [[Pollen Allergy]] and outdoor plans should account for pollen exposure.",
			category:   "health",
			tier:       memory.TierLong,
			importance: 0.98,
			tags:       []string{"family", "health", "pollen"},
			links:      []string{"Daughter", "Pollen Allergy"},
			opts: memory.SaveOptions{
				StateKey:   "pollen",
				StateValue: "active",
				Confidence: 0.95,
			},
		},
		{
			content:    "User's daughter enjoys quiet parks, but windy afternoons increase outdoor allergy risk.",
			category:   "preference",
			tier:       memory.TierMedium,
			importance: 0.78,
			tags:       []string{"family", "outdoor"},
			links:      []string{"Daughter", "Outdoor Plan", "Pollen Allergy"},
		},
		{
			content:    "Before final outdoor advice for the child, check current time, weather forecast, pollen forecast, and air quality.",
			category:   "rule",
			tier:       memory.TierLong,
			importance: 0.96,
			tags:       []string{"tool_gate", "weather", "air_quality"},
			links:      []string{"Outdoor Plan", "Daughter", "Pollen Allergy", "Weather Forecast", "Air Quality"},
		},
		{
			content:    "Preferred outdoor location hint: Shanghai.",
			category:   "location",
			tier:       memory.TierLong,
			importance: 0.85,
			tags:       []string{"location"},
			links:      []string{"Outdoor Plan", "Daughter", "Shanghai"},
		},
		{
			content:    "Old note: pollen allergy was resolved after winter medication.",
			category:   "health",
			tier:       memory.TierLong,
			importance: 0.72,
			tags:       []string{"temporal", "pollen"},
			opts: memory.SaveOptions{
				Status:     "superseded",
				StateKey:   "pollen",
				StateValue: "resolved",
				Confidence: 0.45,
			},
		},
		{
			content:    "For LuckyHarness coding tasks, prefer targeted Go tests before broad test suites.",
			category:   "project",
			tier:       memory.TierLong,
			importance: 0.82,
			tags:       []string{"project", "go"},
		},
	}

	for _, r := range records {
		r.opts.Tags = r.tags
		r.opts.Links = r.links
		if err := store.SaveWithOptions(r.content, r.category, r.tier, r.importance, r.opts); err != nil {
			return fmt.Errorf("seed memory %q: %w", r.content, err)
		}
	}
	return nil
}

func loadTraceCases(path string, mgr *session.Manager, store *memory.Store) ([]benchCase, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read trace file: %w", err)
	}
	cases, globalMemories, err := parseTraceCases(data)
	if err != nil {
		return nil, fmt.Errorf("parse trace file %s: %w", path, err)
	}
	for _, tm := range globalMemories {
		if err := saveTraceMemory(store, tm); err != nil {
			return nil, fmt.Errorf("save trace memory: %w", err)
		}
	}
	out := make([]benchCase, 0, len(cases))
	for i, tc := range cases {
		bc, err := buildTraceBenchCase(mgr, store, tc, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, bc)
	}
	return out, nil
}

func parseTraceCases(data []byte) ([]traceCase, []traceMemory, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, nil, errors.New("trace file is empty")
	}
	var tf traceFile
	if err := json.Unmarshal([]byte(trimmed), &tf); err == nil && len(tf.Cases) > 0 {
		return tf.Cases, tf.Memories, nil
	}
	var single traceCase
	if err := json.Unmarshal([]byte(trimmed), &single); err == nil && traceCaseHasContent(single) {
		return []traceCase{single}, nil, nil
	}

	var cases []traceCase
	for lineNo, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var tc traceCase
		if err := json.Unmarshal([]byte(line), &tc); err != nil {
			return nil, nil, fmt.Errorf("decode jsonl line %d: %w", lineNo+1, err)
		}
		if !traceCaseHasContent(tc) {
			return nil, nil, fmt.Errorf("jsonl line %d is missing query or messages", lineNo+1)
		}
		cases = append(cases, tc)
	}
	if len(cases) == 0 {
		return nil, nil, errors.New("trace file contains no cases")
	}
	return cases, nil, nil
}

func traceCaseHasContent(tc traceCase) bool {
	return strings.TrimSpace(tc.Query) != "" || len(tc.Messages) > 0
}

func buildTraceBenchCase(mgr *session.Manager, store *memory.Store, tc traceCase, index int) (benchCase, error) {
	if mgr == nil {
		return benchCase{}, errors.New("session manager is nil")
	}
	if strings.TrimSpace(tc.Query) == "" {
		return benchCase{}, fmt.Errorf("trace case %d missing query", index)
	}
	if len(tc.Messages) == 0 {
		return benchCase{}, fmt.Errorf("trace case %d missing messages", index)
	}
	id := strings.TrimSpace(tc.ID)
	if id == "" {
		id = fmt.Sprintf("trace-%02d", index)
	}
	scenario := normalizeTraceScenario(tc.Scenario, id)
	title := strings.TrimSpace(tc.Title)
	if title == "" {
		title = "context-packer-trace " + id
	}
	sess := mgr.NewWithTitle(title)
	for _, msg := range tc.Messages {
		if strings.TrimSpace(msg.Role) == "" {
			continue
		}
		sess.AddProviderMessage(msg)
	}
	for _, tm := range tc.MemoryItems {
		if err := saveTraceMemory(store, tm); err != nil {
			return benchCase{}, fmt.Errorf("trace case %s memory: %w", id, err)
		}
	}
	memoryGolden := tc.Memory
	if len(memoryGolden) == 0 {
		memoryGolden = tc.Critical
	}
	return benchCase{
		ID:          id,
		Scenario:    scenario,
		Query:       tc.Query,
		Session:     sess,
		Memory:      memoryGolden,
		Constraints: tc.Constraints,
		Warnings:    tc.Warnings,
		Tools:       tc.Tools,
		Evidence:    tc.Evidence,
		Noise:       tc.Noise,
	}, nil
}

func normalizeTraceScenario(scenario, id string) string {
	scenario = strings.ToLower(strings.TrimSpace(scenario))
	if scenario == "" {
		scenario = "trace"
	}
	scenario = strings.NewReplacer(" ", "_", "-", "_").Replace(scenario)
	if strings.Trim(scenario, "_") == "" {
		scenario = "trace"
	}
	return scenario
}

func saveTraceMemory(store *memory.Store, tm traceMemory) error {
	if store == nil || strings.TrimSpace(tm.Content) == "" {
		return nil
	}
	category := strings.TrimSpace(tm.Category)
	if category == "" {
		category = "trace"
	}
	importance := tm.Importance
	if importance <= 0 {
		importance = 0.8
	}
	opts := memory.SaveOptions{
		Tags:       tm.Tags,
		Links:      tm.Links,
		Status:     tm.Status,
		StateKey:   tm.StateKey,
		StateValue: tm.StateValue,
		Confidence: tm.Confidence,
	}
	return store.SaveWithOptions(tm.Content, category, parseMemoryTier(tm.Tier), importance, opts)
}

func parseMemoryTier(raw string) memory.Tier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short":
		return memory.TierShort
	case "medium", "":
		return memory.TierMedium
	case "long":
		return memory.TierLong
	default:
		return memory.TierMedium
	}
}

func buildCases(mgr *session.Manager) []benchCase {
	return []benchCase{
		{
			ID:       "memory-child-pollen",
			Scenario: "memory_constraint",
			Query:    "明天下午适合和女儿去公园吗？请结合我已有记忆给建议。",
			Session:  buildSession(mgr, "memory constraint", 8, []string{"music playlist", "desk setup"}),
			Memory: []string{
				"daughter has active",
				"pollen allergy",
				"quiet parks",
			},
			Constraints: []string{
				"Account for the remembered pollen allergy risk",
				"Use location hint: Shanghai",
			},
			Tools:    []string{"current_time", "web_search"},
			Evidence: []string{"Memory refs:"},
			Noise:    []string{"music playlist", "desk setup", "espresso grinder"},
		},
		{
			ID:       "tool-gate-weather-aqi",
			Scenario: "tool_gate",
			Query:    "今天户外空气质量和天气会不会影响孩子过敏？",
			Session:  buildSession(mgr, "tool gate", 12, []string{"movie notes", "package delivery"}),
			Memory: []string{
				"pollen exposure",
				"weather forecast",
				"air quality",
			},
			Constraints: []string{
				"Before the final answer",
				"Check air quality",
				"Use live or forecast weather",
			},
			Tools:    []string{"current_time", "web_search"},
			Evidence: []string{"Suggested web_search queries", "Memory refs:"},
			Noise:    []string{"movie notes", "package delivery", "keyboard switches"},
		},
		{
			ID:       "temporal-pollen-conflict",
			Scenario: "temporal_conflict",
			Query:    "女儿花粉过敏这个记忆现在还应该作为户外建议依据吗？",
			Session:  buildSession(mgr, "temporal conflict", 10, []string{"old travel itinerary", "coffee beans"}),
			Memory: []string{
				"daughter has active",
			},
			Constraints: []string{
				"do not apply older allergy risk unless new evidence contradicts it",
				"Apply family/child-related memories",
			},
			Warnings: []string{
				"Temporal resolution:",
				"Superseded refs:",
			},
			Evidence: []string{"Memory refs:"},
			Noise:    []string{"old travel itinerary", "coffee beans", "random lunch"},
		},
		{
			ID:       "long-history-relevant-tail",
			Scenario: "long_history",
			Query:    "继续刚才 LuckyHarness context packer benchmark 的实验，别丢掉验收指标。",
			Session:  buildLongHistorySession(mgr),
			Memory: []string{
				"targeted Go tests",
			},
			Constraints: []string{
				"CMR >= 0.95",
				"P95PackerMS <= 10",
				"Quality >= baseline",
			},
			Evidence: []string{
				"context packer benchmark",
			},
			Noise: []string{
				"recipe filler",
				"vacation filler",
				"keyboard filler",
			},
		},
		{
			ID:       "project-switch-luckyharness",
			Scenario: "project_switch",
			Query:    "继续 LuckyHarness context packer benchmark，别混进别的项目问题。",
			Session:  buildProjectSwitchSession(mgr),
			Memory: []string{
				"targeted Go tests",
			},
			Constraints: []string{
				"LuckyHarness-only context packer scope",
				"write results under docs/reports",
				"context-packer-hardcases",
			},
			Evidence: []string{
				"cmd/lh-context-packer-bench",
			},
			Noise: []string{
				"rightclaw-payment-filler",
				"newapi-subscription-filler",
				"alipay-callback-filler",
			},
		},
		{
			ID:       "user-revision-latest-direction",
			Scenario: "user_revision",
			Query:    "按最后一次确认继续 Context Packer，不要回到旧论文任务。",
			Session:  buildUserRevisionSession(mgr),
			Constraints: []string{
				"Latest user direction: context packer benchmark is the active task",
				"Do not switch back to memory activation PDF paper",
				"compare baseline, V1f, and V3c",
			},
			Evidence: []string{
				"context-packer-v3-intent-history-results-20260607.md",
			},
			Noise: []string{
				"memory-activation-paper-old-draft-filler",
				"graph-rag-package-old-plan-filler",
				"pdf-export-old-task-filler",
			},
		},
		{
			ID:       "tool-evidence-summary",
			Scenario: "tool_evidence",
			Query:    "把刚才跑过的测试和 benchmark 证据汇总出来。",
			Session:  buildToolEvidenceSession(mgr),
			Constraints: []string{
				"go test ./cmd/lh-context-packer-bench ./internal/agent ./internal/memory",
				"clean=true",
				"avg_quality=1.000",
			},
			Evidence: []string{
				"context-packer-intent-history-v3c-20260607.jsonl",
				"p95=3.35ms",
			},
			Noise: []string{
				"garden-planter-filler",
				"invoice-filing-filler",
				"desk-lamp-filler",
			},
		},
		{
			ID:       "bilingual-history-mixed-metrics",
			Scenario: "bilingual_history",
			Query:    "Continue the Context Packer benchmark 收口，保留中文验收指标和 English metric names.",
			Session:  buildBilingualHistorySession(mgr),
			Constraints: []string{
				"噪声 <= 0.25",
				"CMR >= 0.95",
				"P95PackerMS <= 10",
			},
			Evidence: []string{
				"context-packer-v3-intent-history-results-20260607.md",
			},
			Noise: []string{
				"旅行攻略 filler",
				"espresso machine filler",
				"keyboard layout filler",
			},
		},
	}
}

func buildSession(mgr *session.Manager, title string, fillerTurns int, fillers []string) *session.Session {
	sess := mgr.NewWithTitle("context-packer-bench " + title)
	for i := 0; i < fillerTurns; i++ {
		filler := fillers[i%len(fillers)]
		sess.AddMessage("user", fmt.Sprintf("unrelated %s question %02d", filler, i+1))
		sess.AddMessage("assistant", fmt.Sprintf("unrelated %s answer %02d", filler, i+1))
	}
	sess.AddMessage("user", "我们在讨论 LuckyHarness 的 context packer benchmark。")
	sess.AddMessage("assistant", "已记录：需要评估 CMR、CR、TWR、ToolRecall、ContextNoise 和 P95PackerMS。")
	return sess
}

func buildLongHistorySession(mgr *session.Manager) *session.Session {
	sess := mgr.NewWithTitle("context-packer-bench long history")
	fillers := []string{"recipe filler", "vacation filler", "keyboard filler", "movie filler", "shopping filler"}
	for i := 0; i < 45; i++ {
		filler := fillers[i%len(fillers)]
		sess.AddMessage("user", fmt.Sprintf("%s user chatter %02d with deliberately verbose irrelevant text", filler, i+1))
		sess.AddMessage("assistant", fmt.Sprintf("%s assistant chatter %02d with no benchmark relevance", filler, i+1))
	}
	sess.AddMessage("user", "Context packer benchmark acceptance gates: Quality >= baseline, CMR >= 0.95, CR >= 0.95, TWR >= 0.90, P95PackerMS <= 10.")
	sess.AddMessage("assistant", "实验计划：先跑 V0 baseline，再比较 typed memory、utility score、intent-aware history。")
	sess.AddMessage("tool", "[Tool: shell] go test ./cmd/lh-context-packer-bench ./internal/agent")
	sess.AddMessage("assistant", "下一步需要汇总 prompt tokens、bucket tokens、context noise 和 token efficiency。")
	return sess
}

func buildProjectSwitchSession(mgr *session.Manager) *session.Session {
	sess := mgr.NewWithTitle("context-packer-bench project switch")
	for i := 0; i < 24; i++ {
		switch i % 3 {
		case 0:
			sess.AddMessage("user", fmt.Sprintf("rightclaw-payment-filler user note %02d unrelated project state", i+1))
			sess.AddMessage("assistant", fmt.Sprintf("rightclaw-payment-filler assistant note %02d unrelated project state", i+1))
		case 1:
			sess.AddMessage("user", fmt.Sprintf("newapi-subscription-filler user note %02d unrelated project state", i+1))
			sess.AddMessage("assistant", fmt.Sprintf("newapi-subscription-filler assistant note %02d unrelated project state", i+1))
		default:
			sess.AddMessage("user", fmt.Sprintf("alipay-callback-filler user note %02d unrelated project state", i+1))
			sess.AddMessage("assistant", fmt.Sprintf("alipay-callback-filler assistant note %02d unrelated project state", i+1))
		}
	}
	sess.AddMessage("user", "LuckyHarness-only context packer scope: stay in cmd/lh-context-packer-bench and internal/agent.")
	sess.AddMessage("assistant", "write results under docs/reports/context-packer-hardcases-20260607.jsonl.")
	sess.AddMessage("assistant", "Evidence path for this task includes cmd/lh-context-packer-bench.")
	return sess
}

func buildUserRevisionSession(mgr *session.Manager) *session.Session {
	sess := mgr.NewWithTitle("context-packer-bench user revision")
	for i := 0; i < 18; i++ {
		sess.AddMessage("user", fmt.Sprintf("memory-activation-paper-old-draft-filler request %02d", i+1))
		sess.AddMessage("assistant", fmt.Sprintf("graph-rag-package-old-plan-filler response %02d", i+1))
		sess.AddMessage("assistant", fmt.Sprintf("pdf-export-old-task-filler response %02d", i+1))
	}
	sess.AddMessage("user", "Latest user direction: context packer benchmark is the active task.")
	sess.AddMessage("assistant", "Do not switch back to memory activation PDF paper.")
	sess.AddMessage("assistant", "Final comparison should compare baseline, V1f, and V3c.")
	sess.AddMessage("assistant", "Report path: docs/reports/context-packer-v3-intent-history-results-20260607.md.")
	return sess
}

func buildToolEvidenceSession(mgr *session.Manager) *session.Session {
	sess := mgr.NewWithTitle("context-packer-bench tool evidence")
	for i := 0; i < 18; i++ {
		sess.AddMessage("user", fmt.Sprintf("garden-planter-filler planning note %02d", i+1))
		sess.AddMessage("assistant", fmt.Sprintf("invoice-filing-filler response note %02d", i+1))
		sess.AddMessage("assistant", fmt.Sprintf("desk-lamp-filler response note %02d", i+1))
	}
	sess.AddMessage("tool", "[Tool: shell] /usr/local/go/bin/go test ./cmd/lh-context-packer-bench ./internal/agent ./internal/memory\nok github.com/yurika0211/luckyharness/cmd/lh-context-packer-bench\nok github.com/yurika0211/luckyharness/internal/agent\nok github.com/yurika0211/luckyharness/internal/memory")
	sess.AddMessage("tool", "[Tool: shell] summary scenario=all records=12 avg_tokens=1334 avg_quality=1.000 noise=0.000 p95=3.35ms clean=true")
	sess.AddMessage("assistant", "Benchmark evidence file: docs/reports/context-packer-intent-history-v3c-20260607.jsonl.")
	return sess
}

func buildBilingualHistorySession(mgr *session.Manager) *session.Session {
	sess := mgr.NewWithTitle("context-packer-bench bilingual history")
	for i := 0; i < 20; i++ {
		sess.AddMessage("user", fmt.Sprintf("旅行攻略 filler 闲聊 %02d", i+1))
		sess.AddMessage("assistant", fmt.Sprintf("espresso machine filler chit-chat %02d", i+1))
		sess.AddMessage("assistant", fmt.Sprintf("keyboard layout filler unrelated note %02d", i+1))
	}
	sess.AddMessage("user", "中文验收: 噪声 <= 0.25，关键记忆不能丢。")
	sess.AddMessage("assistant", "English metrics: CMR >= 0.95, CR >= 0.95, P95PackerMS <= 10.")
	sess.AddMessage("assistant", "Report path: docs/reports/context-packer-v3-intent-history-results-20260607.md.")
	return sess
}

func runScenario(ctx context.Context, cfg benchConfig, ds *benchDataset, scenario string, enc *json.Encoder) ([]benchRecord, error) {
	if ds == nil || ds.Agent == nil {
		return nil, errors.New("dataset agent is nil")
	}
	cases := casesForScenario(ds.Cases, scenario)
	if len(cases) == 0 {
		return nil, fmt.Errorf("no cases for scenario %s", scenario)
	}

	records := make([]benchRecord, 0, cfg.Rounds*len(cases))
	for round := 1; round <= cfg.Rounds; round++ {
		if round > 1 && cfg.Delay > 0 {
			time.Sleep(cfg.Delay)
		}
		for _, tc := range cases {
			record := runCase(ctx, cfg, ds.Agent, scenario, round, tc)
			if round < cfg.Rounds && cfg.Delay > 0 {
				record.SleepBeforeNextMS = cfg.Delay.Milliseconds()
			}
			if err := enc.Encode(record); err != nil {
				return records, fmt.Errorf("write record: %w", err)
			}
			if cfg.GoldenTrace && len(record.Missing) > 0 {
				fmt.Fprintf(os.Stderr, "missing scenario=%s round=%d query=%s missing=%v\n", scenario, round, tc.ID, record.Missing)
			}
			fmt.Fprintf(os.Stderr,
				"%s round=%d query=%s tokens=%d duration=%.2fms quality=%.3f cmr=%.3f cr=%.3f twr=%.3f tools=%.3f noise=%.3f clean=%t\n",
				scenario,
				round,
				tc.ID,
				record.PromptTokens,
				record.DurationMS,
				record.Quality,
				record.CMR,
				record.CR,
				record.TWR,
				record.ToolRecall,
				record.ContextNoise,
				record.Clean,
			)
			records = append(records, record)
		}
	}
	return records, nil
}

func casesForScenario(cases []benchCase, scenario string) []benchCase {
	var out []benchCase
	for _, tc := range cases {
		if tc.Scenario == scenario {
			out = append(out, tc)
		}
	}
	return out
}

func runCase(ctx context.Context, cfg benchConfig, a *agent.Agent, scenario string, round int, tc benchCase) benchRecord {
	started := time.Now()
	snapshot := a.BuildContextPackerSnapshot(ctx, tc.Session, agent.TextUserTurnInput(tc.Query))
	duration := time.Since(started)

	text := messagesText(snapshot.Messages)
	record := benchRecord{
		Type:                  "round",
		Variant:               cfg.Variant,
		Scenario:              scenario,
		Round:                 round,
		QueryID:               tc.ID,
		Query:                 tc.Query,
		StartedAt:             started,
		DurationNS:            duration.Nanoseconds(),
		DurationMS:            float64(duration.Nanoseconds()) / 1e6,
		MessageCount:          len(snapshot.Messages),
		PromptTokens:          snapshot.TotalTokens,
		BucketTokens:          snapshot.BucketTokens,
		BucketCounts:          snapshot.BucketCounts,
		GoldenMemoryCount:     len(tc.Memory),
		GoldenConstraintCount: len(tc.Constraints),
		GoldenWarningCount:    len(tc.Warnings),
		GoldenToolCount:       len(tc.Tools),
		GoldenEvidenceCount:   len(tc.Evidence),
	}

	record.MemoryHitCount, record.Missing = countHits(text, "memory", tc.Memory, record.Missing)
	record.ConstraintHitCount, record.Missing = countHits(text, "constraint", tc.Constraints, record.Missing)
	record.WarningHitCount, record.Missing = countHits(text, "warning", tc.Warnings, record.Missing)
	record.ToolHitCount, record.Missing = countHits(text, "tool", tc.Tools, record.Missing)
	record.EvidenceHitCount, record.Missing = countHits(text, "evidence", tc.Evidence, record.Missing)
	record.NoiseHitCount, _ = countHits(text, "noise", tc.Noise, nil)

	record.CMR = ratio(record.MemoryHitCount, len(tc.Memory), 1)
	record.CR = ratio(record.ConstraintHitCount, len(tc.Constraints), 1)
	record.TWR = ratio(record.WarningHitCount, len(tc.Warnings), 1)
	record.ToolRecall = ratio(record.ToolHitCount, len(tc.Tools), 1)
	record.ERR = ratio(record.EvidenceHitCount, len(tc.Evidence), 1)
	record.ContextNoise = ratio(record.NoiseHitCount, len(tc.Noise), 0)
	useful := record.MemoryHitCount + record.ConstraintHitCount + record.WarningHitCount + record.ToolHitCount + record.EvidenceHitCount
	if record.PromptTokens > 0 {
		record.TokenEfficiency = float64(useful) / float64(record.PromptTokens)
	}
	record.Quality = qualityScore(record)
	record.Clean = record.Error == "" && record.CMR >= 0.95 && record.CR >= 0.95 && record.TWR >= 0.90 && record.ToolRecall >= 0.95 && record.ContextNoise <= cfg.MaxNoise
	if len(record.Missing) == 0 {
		record.Missing = nil
	}
	return record
}

func messagesText(messages []provider.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		if strings.TrimSpace(msg.Role) != "" {
			b.WriteString("[")
			b.WriteString(msg.Role)
			b.WriteString("]\n")
		}
		b.WriteString(msg.Content)
		b.WriteString("\n")
	}
	return strings.ToLower(b.String())
}

func countHits(text, category string, needles []string, missing map[string][]string) (int, map[string][]string) {
	if len(needles) == 0 {
		return 0, missing
	}
	hits := 0
	for _, needle := range needles {
		needle = strings.TrimSpace(needle)
		if needle == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(needle)) {
			hits++
			continue
		}
		if missing == nil {
			missing = map[string][]string{}
		}
		missing[category] = append(missing[category], needle)
	}
	return hits, missing
}

func ratio(hit, total int, emptyValue float64) float64 {
	if total <= 0 {
		return emptyValue
	}
	return float64(hit) / float64(total)
}

func qualityScore(r benchRecord) float64 {
	return 0.30*r.CMR +
		0.20*r.CR +
		0.15*r.TWR +
		0.15*r.ERR +
		0.10*r.ToolRecall +
		0.10*1.0 -
		0.10*r.ContextNoise
}

func summarizeRecords(cfg benchConfig, scenario string, records []benchRecord) benchSummary {
	s := benchSummary{
		Type:       "summary",
		Variant:    cfg.Variant,
		Scenario:   scenario,
		Records:    len(records),
		Rounds:     cfg.Rounds,
		MinQuality: cfg.MinQuality,
		MaxNoise:   cfg.MaxNoise,
		MaxP95MS:   cfg.MaxP95MS,
	}
	if len(records) == 0 {
		return s
	}

	durations := make([]float64, 0, len(records))
	allRecordsClean := true
	for _, r := range records {
		durations = append(durations, r.DurationMS)
		if r.Error != "" {
			s.Errors++
		}
		if !r.Clean {
			allRecordsClean = false
		}
		s.AvgDurationMS += r.DurationMS
		s.AvgPromptTokens += float64(r.PromptTokens)
		s.AvgCMR += r.CMR
		s.AvgCR += r.CR
		s.AvgTWR += r.TWR
		s.AvgToolRecall += r.ToolRecall
		s.AvgERR += r.ERR
		s.AvgContextNoise += r.ContextNoise
		s.AvgTokenEfficiency += r.TokenEfficiency
		s.AvgQuality += r.Quality
		s.AvgMemoryTokens += float64(r.BucketTokens["memory"])
		s.AvgHistoryTokens += float64(r.BucketTokens["history"])
		s.AvgSystemTokens += float64(r.BucketTokens["system"])
		s.AvgRAGTokens += float64(r.BucketTokens["rag"])
	}
	n := float64(len(records))
	s.AvgDurationMS /= n
	s.AvgPromptTokens /= n
	s.AvgCMR /= n
	s.AvgCR /= n
	s.AvgTWR /= n
	s.AvgToolRecall /= n
	s.AvgERR /= n
	s.AvgContextNoise /= n
	s.AvgTokenEfficiency /= n
	s.AvgQuality /= n
	s.AvgMemoryTokens /= n
	s.AvgHistoryTokens /= n
	s.AvgSystemTokens /= n
	s.AvgRAGTokens /= n

	sort.Float64s(durations)
	s.P50DurationMS = percentile(durations, 0.50)
	s.P95DurationMS = percentile(durations, 0.95)
	s.QualityPass = s.AvgQuality >= cfg.MinQuality && s.AvgContextNoise <= cfg.MaxNoise
	s.LatencyPass = s.P95DurationMS <= cfg.MaxP95MS
	s.Clean = s.Errors == 0 && allRecordsClean && s.QualityPass && s.LatencyPass
	return s
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return sorted[lower]
	}
	weight := pos - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func printSummary(s benchSummary) {
	fmt.Fprintf(os.Stderr,
		"summary scenario=%s records=%d avg_tokens=%.0f avg_quality=%.3f cmr=%.3f cr=%.3f twr=%.3f tools=%.3f noise=%.3f p95=%.2fms clean=%t\n",
		s.Scenario,
		s.Records,
		s.AvgPromptTokens,
		s.AvgQuality,
		s.AvgCMR,
		s.AvgCR,
		s.AvgTWR,
		s.AvgToolRecall,
		s.AvgContextNoise,
		s.P95DurationMS,
		s.Clean,
	)
}
