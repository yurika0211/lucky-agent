package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/memory"
)

type benchConfig struct {
	Variant        string
	Scenario       string
	Dataset        string
	MemoryDir      string
	OutPath        string
	QueryOverride  string
	Size           int
	Rounds         int
	Limit          int
	Seed           int64
	Delay          time.Duration
	IncludeGraph   bool
	UpdateAccess   bool
	KeepDataset    bool
	MinRecall      float64
	MaxNoise       float64
	MaxStaleHits   int
	RealDatasetTag string
}

type benchRecord struct {
	Type              string    `json:"type"`
	Variant           string    `json:"variant"`
	Scenario          string    `json:"scenario"`
	Mode              string    `json:"mode,omitempty"`
	Dataset           string    `json:"dataset"`
	MemoryDir         string    `json:"memory_dir,omitempty"`
	Size              int       `json:"size"`
	Round             int       `json:"round"`
	QueryID           string    `json:"query_id"`
	Query             string    `json:"query"`
	StartedAt         time.Time `json:"started_at"`
	DurationNS        int64     `json:"duration_ns"`
	DurationMS        float64   `json:"duration_ms"`
	Limit             int       `json:"limit"`
	GraphEnabled      bool      `json:"graph_enabled"`
	UpdateAccess      bool      `json:"update_access"`
	ResultCount       int       `json:"result_count"`
	ResultIDs         []string  `json:"result_ids,omitempty"`
	ExpectedCount     int       `json:"expected_count"`
	HitCount          int       `json:"hit_count"`
	ForbidCount       int       `json:"forbid_count"`
	ForbidHitCount    int       `json:"forbid_hit_count"`
	RecallAtK         float64   `json:"recall_at_k"`
	PrecisionAtK      float64   `json:"precision_at_k"`
	MRR               float64   `json:"mrr"`
	NDCGAtK           float64   `json:"ndcg_at_k"`
	NoiseAtK          float64   `json:"noise_at_k"`
	StaleHitRate      float64   `json:"stale_hit_rate"`
	RiskFlags         []string  `json:"risk_flags,omitempty"`
	RequiredTools     []string  `json:"required_tools,omitempty"`
	ExpectedRisk      int       `json:"expected_risk_count,omitempty"`
	RiskHitCount      int       `json:"risk_hit_count,omitempty"`
	RiskRecall        float64   `json:"risk_recall,omitempty"`
	ExpectedTools     int       `json:"expected_tool_count,omitempty"`
	ToolHitCount      int       `json:"tool_hit_count,omitempty"`
	ToolRecall        float64   `json:"tool_recall,omitempty"`
	Clean             bool      `json:"clean"`
	QualityPass       bool      `json:"quality_pass"`
	Error             string    `json:"error,omitempty"`
	SleepBeforeNextMS int64     `json:"sleep_before_next_ms,omitempty"`
}

type benchSummary struct {
	Type                  string   `json:"type"`
	Variant               string   `json:"variant"`
	Scenario              string   `json:"scenario"`
	Dataset               string   `json:"dataset"`
	Size                  int      `json:"size"`
	Rounds                int      `json:"rounds"`
	Records               int      `json:"records"`
	Errors                int      `json:"errors"`
	ForbidHits            int      `json:"forbid_hits"`
	Clean                 bool     `json:"clean"`
	QualityPass           bool     `json:"quality_pass"`
	MinRecall             float64  `json:"min_recall"`
	MaxNoise              float64  `json:"max_noise"`
	AvgDurationNS         float64  `json:"avg_duration_ns"`
	P50DurationNS         int64    `json:"p50_duration_ns"`
	P95DurationNS         int64    `json:"p95_duration_ns"`
	AvgRecallAtK          float64  `json:"avg_recall_at_k"`
	AvgPrecisionAtK       float64  `json:"avg_precision_at_k"`
	AvgMRR                float64  `json:"avg_mrr"`
	AvgNDCGAtK            float64  `json:"avg_ndcg_at_k"`
	AvgNoiseAtK           float64  `json:"avg_noise_at_k"`
	AvgStaleHitRate       float64  `json:"avg_stale_hit_rate"`
	AvgRiskRecall         float64  `json:"avg_risk_recall,omitempty"`
	AvgToolRecall         float64  `json:"avg_tool_recall,omitempty"`
	AvgGraphOnRecallAtK   float64  `json:"avg_graph_on_recall_at_k,omitempty"`
	AvgGraphOffRecallAtK  float64  `json:"avg_graph_off_recall_at_k,omitempty"`
	GraphRecallLift       float64  `json:"graph_recall_lift,omitempty"`
	AvgGraphOnDurationNS  float64  `json:"avg_graph_on_duration_ns,omitempty"`
	AvgGraphOffDurationNS float64  `json:"avg_graph_off_duration_ns,omitempty"`
	ComparedModes         []string `json:"compared_modes,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "lh-memory-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultOut := filepath.Join(os.TempDir(), "lh-memory-bench", "results-"+now+".jsonl")

	var cfg benchConfig
	flag.StringVar(&cfg.Variant, "variant", "manual", "label written to each benchmark record, e.g. baseline or activation-v1")
	flag.StringVar(&cfg.Scenario, "scenario", "all", "scenario to run: lexical, graph, temporal, scale, route, or all")
	flag.StringVar(&cfg.Dataset, "dataset", "synthetic", "dataset to use: synthetic or real")
	flag.StringVar(&cfg.MemoryDir, "memory-dir", "", "memory vault path for dataset=real")
	flag.StringVar(&cfg.OutPath, "out", defaultOut, "JSONL output path")
	flag.StringVar(&cfg.QueryOverride, "query", "", "optional single query override")
	flag.IntVar(&cfg.Size, "size", 1000, "synthetic memory count")
	flag.IntVar(&cfg.Rounds, "rounds", 3, "rounds per scenario")
	flag.IntVar(&cfg.Limit, "limit", 6, "top-k result limit")
	flag.Int64Var(&cfg.Seed, "seed", 42, "synthetic dataset seed")
	flag.DurationVar(&cfg.Delay, "delay", 0, "delay between rounds")
	flag.BoolVar(&cfg.IncludeGraph, "graph", true, "enable graph activation for non-graph-pair scenarios")
	flag.BoolVar(&cfg.UpdateAccess, "update-access", false, "update memory access stats during activation scenarios")
	flag.BoolVar(&cfg.KeepDataset, "keep-dataset", false, "keep generated synthetic memory vault on disk")
	flag.Float64Var(&cfg.MinRecall, "min-recall", 0.65, "summary quality threshold for recall@k")
	flag.Float64Var(&cfg.MaxNoise, "max-noise", 0.60, "summary quality threshold for noise@k")
	flag.IntVar(&cfg.MaxStaleHits, "max-stale-hits", 0, "maximum allowed forbidden/stale hits for a clean run")
	flag.StringVar(&cfg.RealDatasetTag, "real-dataset-tag", "real", "dataset label used for dataset=real")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	if cfg.Rounds <= 0 {
		return fmt.Errorf("rounds must be positive")
	}
	if cfg.Size <= 0 {
		return fmt.Errorf("size must be positive")
	}
	if cfg.Limit <= 0 {
		return fmt.Errorf("limit must be positive")
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

	scenarios, err := expandScenarios(cfg.Scenario)
	if err != nil {
		return err
	}

	var all []benchRecord
	for _, scenario := range scenarios {
		records, err := runScenario(cfg, ds, scenario, enc)
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
		if err := enc.Encode(summary); err != nil {
			return fmt.Errorf("write aggregate summary: %w", err)
		}
		printSummary(summary)
	}

	fmt.Fprintf(os.Stderr, "results: %s\nmemory: %s\n", cfg.OutPath, ds.Dir)
	return nil
}

func expandScenarios(raw string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "lexical", "graph", "temporal", "scale", "route":
		return []string{strings.ToLower(strings.TrimSpace(raw))}, nil
	case "all":
		return []string{"lexical", "graph", "temporal", "scale", "route"}, nil
	default:
		return nil, fmt.Errorf("unknown scenario %q", raw)
	}
}

func runScenario(cfg benchConfig, ds *benchDataset, scenario string, enc *json.Encoder) ([]benchRecord, error) {
	if ds == nil || ds.Store == nil {
		return nil, fmt.Errorf("dataset is not loaded")
	}
	queries := ds.QueriesForScenario(scenario, cfg.QueryOverride)
	if len(queries) == 0 {
		return nil, fmt.Errorf("no queries for scenario %s", scenario)
	}

	records := make([]benchRecord, 0, cfg.Rounds*len(queries))
	for round := 1; round <= cfg.Rounds; round++ {
		if round > 1 && cfg.Delay > 0 {
			time.Sleep(cfg.Delay)
		}
		for _, query := range queries {
			var roundRecords []benchRecord
			if scenario == "graph" {
				roundRecords = append(roundRecords,
					runActivationRecord(cfg, ds, scenario, "graph_off", round, query, false),
					runActivationRecord(cfg, ds, scenario, "graph_on", round, query, true),
				)
			} else if scenario == "route" {
				roundRecords = append(roundRecords, runRouteRecord(cfg, ds, scenario, round, query))
			} else {
				roundRecords = append(roundRecords, runActivationRecord(cfg, ds, scenario, scenario, round, query, cfg.IncludeGraph))
			}
			for i := range roundRecords {
				if round < cfg.Rounds && cfg.Delay > 0 {
					roundRecords[i].SleepBeforeNextMS = cfg.Delay.Milliseconds()
				}
				if err := enc.Encode(roundRecords[i]); err != nil {
					return records, fmt.Errorf("write record: %w", err)
				}
				printRecord(roundRecords[i])
				records = append(records, roundRecords[i])
			}
		}
	}
	return records, nil
}

func runActivationRecord(cfg benchConfig, ds *benchDataset, scenario, mode string, round int, query benchQuery, graph bool) benchRecord {
	started := time.Now()
	opts := memory.ActivationOptions{
		Limit:             cfg.Limit,
		IncludeGraph:      graph,
		MaxGraphDepth:     1,
		MaxGraphBoost:     0.45,
		UpdateAccessStats: cfg.UpdateAccess,
		Explain:           true,
	}
	scores := ds.Store.Activate(query.Text, opts)
	duration := time.Since(started)
	entries := activationEntries(scores)
	metrics := evaluateEntries(query, entries, cfg.Limit)
	record := baseRecord(cfg, ds, scenario, mode, round, query, started, duration)
	record.GraphEnabled = graph
	record.UpdateAccess = cfg.UpdateAccess
	fillEntryMetrics(&record, metrics)
	markRecordQuality(&record, cfg)
	return record
}

func runRouteRecord(cfg benchConfig, ds *benchDataset, scenario string, round int, query benchQuery) benchRecord {
	started := time.Now()
	route := ds.Store.Route(query.Text)
	duration := time.Since(started)
	metrics := evaluateEntries(query, route.Entries, cfg.Limit)
	record := baseRecord(cfg, ds, scenario, "route", round, query, started, duration)
	record.GraphEnabled = true
	record.UpdateAccess = true
	record.RiskFlags = append([]string(nil), route.RiskFlags...)
	record.RequiredTools = append([]string(nil), route.RequiredTools...)
	fillEntryMetrics(&record, metrics)
	fillRouteMetrics(&record, query)
	markRecordQuality(&record, cfg)
	return record
}

func baseRecord(cfg benchConfig, ds *benchDataset, scenario, mode string, round int, query benchQuery, started time.Time, duration time.Duration) benchRecord {
	return benchRecord{
		Type:        "round",
		Variant:     cfg.Variant,
		Scenario:    scenario,
		Mode:        mode,
		Dataset:     ds.Name,
		MemoryDir:   ds.Dir,
		Size:        ds.Size,
		Round:       round,
		QueryID:     query.ID,
		Query:       query.Text,
		StartedAt:   started,
		DurationNS:  duration.Nanoseconds(),
		DurationMS:  float64(duration.Nanoseconds()) / float64(time.Millisecond),
		Limit:       cfg.Limit,
		Clean:       true,
		QualityPass: true,
	}
}

func fillEntryMetrics(record *benchRecord, metrics entryMetrics) {
	record.ResultCount = metrics.ResultCount
	record.ResultIDs = metrics.ResultIDs
	record.ExpectedCount = metrics.ExpectedCount
	record.HitCount = metrics.HitCount
	record.ForbidCount = metrics.ForbidCount
	record.ForbidHitCount = metrics.ForbidHitCount
	record.RecallAtK = metrics.RecallAtK
	record.PrecisionAtK = metrics.PrecisionAtK
	record.MRR = metrics.MRR
	record.NDCGAtK = metrics.NDCGAtK
	record.NoiseAtK = metrics.NoiseAtK
	record.StaleHitRate = metrics.StaleHitRate
}

func fillRouteMetrics(record *benchRecord, query benchQuery) {
	record.ExpectedRisk = len(query.WantRiskFlags)
	record.RiskHitCount = countHits(record.RiskFlags, query.WantRiskFlags)
	if record.ExpectedRisk > 0 {
		record.RiskRecall = float64(record.RiskHitCount) / float64(record.ExpectedRisk)
	}
	record.ExpectedTools = len(query.WantTools)
	record.ToolHitCount = countHits(record.RequiredTools, query.WantTools)
	if record.ExpectedTools > 0 {
		record.ToolRecall = float64(record.ToolHitCount) / float64(record.ExpectedTools)
	}
}

func markRecordQuality(record *benchRecord, cfg benchConfig) {
	record.Clean = record.Error == "" && record.ForbidHitCount <= cfg.MaxStaleHits
	record.QualityPass = record.Clean
	if record.ExpectedCount > 0 {
		record.QualityPass = record.QualityPass && record.RecallAtK >= cfg.MinRecall && record.NoiseAtK <= cfg.MaxNoise
	}
	if record.ExpectedRisk > 0 {
		record.QualityPass = record.QualityPass && record.RiskRecall >= cfg.MinRecall
	}
	if record.ExpectedTools > 0 {
		record.QualityPass = record.QualityPass && record.ToolRecall >= cfg.MinRecall
	}
}

func activationEntries(scores []memory.ActivationScore) []memory.Entry {
	entries := make([]memory.Entry, 0, len(scores))
	for _, score := range scores {
		entries = append(entries, score.Entry)
	}
	return entries
}

func summarizeRecords(cfg benchConfig, scenario string, records []benchRecord) benchSummary {
	summary := benchSummary{
		Type:      "summary",
		Variant:   cfg.Variant,
		Scenario:  scenario,
		Dataset:   cfg.Dataset,
		Size:      cfg.Size,
		Rounds:    cfg.Rounds,
		Records:   len(records),
		Clean:     true,
		MinRecall: cfg.MinRecall,
		MaxNoise:  cfg.MaxNoise,
	}
	if len(records) == 0 {
		summary.QualityPass = false
		return summary
	}

	var durations []int64
	var durationTotal int64
	var recall, precision, mrr, ndcg, noise, stale float64
	var riskRecall, toolRecall float64
	var riskN, toolN int
	var graphOnRecall, graphOffRecall, graphOnDuration, graphOffDuration float64
	var graphOnN, graphOffN int
	qualityRecords := 0
	for _, record := range records {
		durations = append(durations, record.DurationNS)
		durationTotal += record.DurationNS
		recall += record.RecallAtK
		precision += record.PrecisionAtK
		mrr += record.MRR
		ndcg += record.NDCGAtK
		noise += record.NoiseAtK
		stale += record.StaleHitRate
		summary.ForbidHits += record.ForbidHitCount
		if record.Error != "" {
			summary.Errors++
		}
		if !record.Clean {
			summary.Clean = false
		}
		if record.ExpectedCount > 0 || record.ExpectedRisk > 0 || record.ExpectedTools > 0 {
			qualityRecords++
			if record.ExpectedRisk > 0 {
				riskRecall += record.RiskRecall
				riskN++
			}
			if record.ExpectedTools > 0 {
				toolRecall += record.ToolRecall
				toolN++
			}
		}
		switch record.Mode {
		case "graph_on":
			graphOnRecall += record.RecallAtK
			graphOnDuration += float64(record.DurationNS)
			graphOnN++
		case "graph_off":
			graphOffRecall += record.RecallAtK
			graphOffDuration += float64(record.DurationNS)
			graphOffN++
		}
	}

	n := float64(len(records))
	summary.AvgDurationNS = float64(durationTotal) / n
	summary.AvgRecallAtK = recall / n
	summary.AvgPrecisionAtK = precision / n
	summary.AvgMRR = mrr / n
	summary.AvgNDCGAtK = ndcg / n
	summary.AvgNoiseAtK = noise / n
	summary.AvgStaleHitRate = stale / n
	if riskN > 0 {
		summary.AvgRiskRecall = riskRecall / float64(riskN)
	}
	if toolN > 0 {
		summary.AvgToolRecall = toolRecall / float64(toolN)
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	summary.P50DurationNS = percentileDuration(durations, 0.50)
	summary.P95DurationNS = percentileDuration(durations, 0.95)
	if graphOnN > 0 && graphOffN > 0 {
		summary.AvgGraphOnRecallAtK = graphOnRecall / float64(graphOnN)
		summary.AvgGraphOffRecallAtK = graphOffRecall / float64(graphOffN)
		summary.GraphRecallLift = summary.AvgGraphOnRecallAtK - summary.AvgGraphOffRecallAtK
		summary.AvgGraphOnDurationNS = graphOnDuration / float64(graphOnN)
		summary.AvgGraphOffDurationNS = graphOffDuration / float64(graphOffN)
		summary.ComparedModes = []string{"graph_off", "graph_on"}
	}

	summary.QualityPass = summary.Clean
	if qualityRecords > 0 {
		candidateRecords := qualityCandidateRecords(records)
		recallAvg, noiseAvg := averageRecallNoise(candidateRecords)
		summary.QualityPass = summary.QualityPass && recallAvg >= cfg.MinRecall && noiseAvg <= cfg.MaxNoise
		if riskN > 0 {
			summary.QualityPass = summary.QualityPass && summary.AvgRiskRecall >= cfg.MinRecall
		}
		if toolN > 0 {
			summary.QualityPass = summary.QualityPass && summary.AvgToolRecall >= cfg.MinRecall
		}
	}
	return summary
}

func qualityCandidateRecords(records []benchRecord) []benchRecord {
	var graphOn []benchRecord
	var expected []benchRecord
	for _, record := range records {
		if record.ExpectedCount == 0 {
			continue
		}
		if record.Mode == "graph_on" {
			graphOn = append(graphOn, record)
			continue
		}
		if record.Mode != "graph_off" {
			expected = append(expected, record)
		}
	}
	if len(graphOn) > 0 {
		return graphOn
	}
	return expected
}

func averageRecallNoise(records []benchRecord) (float64, float64) {
	if len(records) == 0 {
		return 1, 0
	}
	var recall, noise float64
	for _, record := range records {
		recall += record.RecallAtK
		noise += record.NoiseAtK
	}
	n := float64(len(records))
	return recall / n, noise / n
}

func percentileDuration(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func printRecord(record benchRecord) {
	fmt.Fprintf(os.Stderr,
		"%s mode=%s round=%d query=%s duration=%.3fms recall=%.2f precision=%.2f noise=%.2f stale_hits=%d clean=%t quality=%t\n",
		record.Scenario,
		record.Mode,
		record.Round,
		record.QueryID,
		record.DurationMS,
		record.RecallAtK,
		record.PrecisionAtK,
		record.NoiseAtK,
		record.ForbidHitCount,
		record.Clean,
		record.QualityPass,
	)
}

func printSummary(summary benchSummary) {
	fmt.Fprintf(os.Stderr,
		"summary scenario=%s records=%d avg_duration=%.3fms recall=%.2f precision=%.2f noise=%.2f forbid_hits=%d clean=%t quality=%t\n",
		summary.Scenario,
		summary.Records,
		summary.AvgDurationNS/float64(time.Millisecond),
		summary.AvgRecallAtK,
		summary.AvgPrecisionAtK,
		summary.AvgNoiseAtK,
		summary.ForbidHits,
		summary.Clean,
		summary.QualityPass,
	)
}
