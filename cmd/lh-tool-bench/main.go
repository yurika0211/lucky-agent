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
)

type benchConfig struct {
	Variant          string
	Scenario         string
	OutPath          string
	ComparePaths     string
	CompareFormat    string
	Rounds           int
	Delay            time.Duration
	NeedThreshold    float64
	SuccessThreshold float64
	MaxRedundantRate float64
}

type benchRecord struct {
	Type                   string     `json:"type"`
	Variant                string     `json:"variant"`
	Scenario               string     `json:"scenario"`
	Round                  int        `json:"round"`
	TaskID                 string     `json:"task_id"`
	TaskType               string     `json:"task_type"`
	Prompt                 string     `json:"prompt"`
	NeedToolGold           bool       `json:"need_tool_gold"`
	NeedToolProb           float64    `json:"need_tool_prob"`
	NeedPredictionCorrect  bool       `json:"need_prediction_correct"`
	RequiredTools          []string   `json:"required_tools,omitempty"`
	RequiredOperations     []string   `json:"required_operations,omitempty"`
	ForbiddenTools         []string   `json:"forbidden_tools,omitempty"`
	ForbiddenOperations    []string   `json:"forbidden_operations,omitempty"`
	VisibleTools           []string   `json:"visible_tools,omitempty"`
	CalledTools            []string   `json:"called_tools,omitempty"`
	CalledOperations       []string   `json:"called_operations,omitempty"`
	ToolCalls              []toolCall `json:"tool_calls,omitempty"`
	RequiredToolCount      int        `json:"required_tool_count"`
	RequiredOperationCount int        `json:"required_operation_count"`
	CalledToolCount        int        `json:"called_tool_count"`
	CalledOperationCount   int        `json:"called_operation_count"`
	ToolHitCount           int        `json:"tool_hit_count"`
	OperationHitCount      int        `json:"operation_hit_count"`
	ForbiddenCallCount     int        `json:"forbidden_call_count"`
	RedundantCalls         int        `json:"redundant_calls"`
	ToolRecall             float64    `json:"tool_recall"`
	ToolPrecision          float64    `json:"tool_precision"`
	OperationRecall        float64    `json:"operation_recall"`
	OperationPrecision     float64    `json:"operation_precision"`
	RedundantRate          float64    `json:"redundant_rate"`
	RouteRisk              float64    `json:"route_risk"`
	ExpectedRouteRisk      float64    `json:"expected_route_risk"`
	ToolTokenCost          int        `json:"tool_token_cost"`
	ToolResultTokens       int        `json:"tool_result_tokens"`
	IrrelevantTokens       int        `json:"irrelevant_tokens"`
	ToolResultNoise        float64    `json:"tool_result_noise"`
	ToolAlignment          float64    `json:"tool_alignment"`
	InfoGain               float64    `json:"info_gain"`
	InfoEfficiency         float64    `json:"info_efficiency"`
	ExpectedUtility        float64    `json:"expected_utility"`
	SuccessProbability     float64    `json:"success_probability"`
	ToolTuneScore          float64    `json:"tool_tune_score"`
	Success                bool       `json:"success"`
	Clean                  bool       `json:"clean"`
	StartedAt              time.Time  `json:"started_at"`
	DurationNS             int64      `json:"duration_ns"`
	DurationMS             float64    `json:"duration_ms"`
	SleepBeforeNextMS      int64      `json:"sleep_before_next_ms,omitempty"`
}

type benchSummary struct {
	Type                  string   `json:"type"`
	Variant               string   `json:"variant"`
	Scenario              string   `json:"scenario"`
	Rounds                int      `json:"rounds"`
	Tasks                 int      `json:"tasks"`
	Records               int      `json:"records"`
	Clean                 bool     `json:"clean"`
	SuccessPass           bool     `json:"success_pass"`
	SuccessThreshold      float64  `json:"success_threshold"`
	NeedThreshold         float64  `json:"need_threshold"`
	AvgDurationMS         float64  `json:"avg_duration_ms"`
	P50DurationMS         float64  `json:"p50_duration_ms"`
	P95DurationMS         float64  `json:"p95_duration_ms"`
	SuccessRate           float64  `json:"success_rate"`
	ToolNeedAcc           float64  `json:"tool_need_acc"`
	AvgToolRecall         float64  `json:"avg_tool_recall"`
	AvgToolPrecision      float64  `json:"avg_tool_precision"`
	AvgOperationRecall    float64  `json:"avg_operation_recall"`
	AvgOperationPrecision float64  `json:"avg_operation_precision"`
	AvgRedundantRate      float64  `json:"avg_redundant_rate"`
	AvgRouteRisk          float64  `json:"avg_route_risk"`
	AvgExpectedRouteRisk  float64  `json:"avg_expected_route_risk"`
	AvgToolTokenCost      float64  `json:"avg_tool_token_cost"`
	AvgToolResultTokens   float64  `json:"avg_tool_result_tokens"`
	AvgToolResultNoise    float64  `json:"avg_tool_result_noise"`
	AvgToolAlignment      float64  `json:"avg_tool_alignment"`
	AvgInfoGain           float64  `json:"avg_info_gain"`
	AvgInfoEfficiency     float64  `json:"avg_info_efficiency"`
	AvgExpectedUtility    float64  `json:"avg_expected_utility"`
	AvgSuccessProbability float64  `json:"avg_success_probability"`
	AvgToolTuneScore      float64  `json:"avg_tool_tune_score"`
	ForbiddenCallCount    int      `json:"forbidden_call_count"`
	RedundantCalls        int      `json:"redundant_calls"`
	TotalToolCalls        int      `json:"total_tool_calls"`
	UniqueCalledTools     []string `json:"unique_called_tools,omitempty"`
	ComparedScenarios     []string `json:"compared_scenarios,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "lh-tool-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultOut := filepath.Join(os.TempDir(), "lh-tool-bench", "results-"+now+".jsonl")
	var cfg benchConfig
	flag.StringVar(&cfg.Variant, "variant", "baseline", "tool strategy variant: baseline, static-slim, intent-gated, risk-aware, packed-results")
	flag.StringVar(&cfg.Scenario, "scenario", "all", "scenario to run: no_tool, read_only, single_tool, multi_tool, risk, trap, or all")
	flag.StringVar(&cfg.OutPath, "out", defaultOut, "JSONL output path")
	flag.StringVar(&cfg.ComparePaths, "compare", "", "comma-separated JSONL result files to compare instead of running benchmark")
	flag.StringVar(&cfg.CompareFormat, "compare-format", "markdown", "comparison output format: markdown or json")
	flag.IntVar(&cfg.Rounds, "rounds", 3, "rounds per scenario")
	flag.DurationVar(&cfg.Delay, "delay", 0, "delay between rounds")
	flag.Float64Var(&cfg.NeedThreshold, "need-threshold", 0.60, "probability threshold for deciding a task needs tools")
	flag.Float64Var(&cfg.SuccessThreshold, "success-threshold", 0.70, "probability threshold for marking a task successful")
	flag.Float64Var(&cfg.MaxRedundantRate, "max-redundant-rate", 0.25, "maximum redundant tool call rate for clean records")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	if strings.TrimSpace(cfg.ComparePaths) != "" {
		return runCompare(cfg)
	}
	if cfg.Rounds <= 0 {
		return fmt.Errorf("rounds must be positive")
	}
	if cfg.NeedThreshold <= 0 || cfg.NeedThreshold >= 1 {
		return fmt.Errorf("need-threshold must be between 0 and 1")
	}
	if cfg.SuccessThreshold <= 0 || cfg.SuccessThreshold >= 1 {
		return fmt.Errorf("success-threshold must be between 0 and 1")
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

	tools := defaultToolCatalog()
	ops := defaultOperationCatalog()
	tasks := normalizeTasks(defaultTasks(), ops)
	scenarios, err := expandScenarios(cfg.Scenario, tasks)
	if err != nil {
		return err
	}

	var all []benchRecord
	for _, scenario := range scenarios {
		records, err := runScenario(cfg, scenario, tasks, tools, ops, enc)
		if err != nil {
			return err
		}
		all = append(all, records...)
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
	fmt.Fprintf(os.Stderr, "results: %s\n", cfg.OutPath)
	return nil
}

func expandScenarios(raw string, tasks []benchTask) ([]string, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	names := scenarioNames(tasks)
	if raw == "all" {
		return names, nil
	}
	for _, name := range names {
		if raw == name {
			return []string{raw}, nil
		}
	}
	return nil, fmt.Errorf("unknown scenario %q (available: %s)", raw, strings.Join(names, ", "))
}

func scenarioNames(tasks []benchTask) []string {
	seen := map[string]bool{}
	var names []string
	for _, task := range tasks {
		name := strings.TrimSpace(task.Scenario)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

func runScenario(cfg benchConfig, scenario string, tasks []benchTask, tools map[string]toolSpec, ops map[string]operationSpec, enc *json.Encoder) ([]benchRecord, error) {
	selected := tasksForScenario(tasks, scenario)
	if len(selected) == 0 {
		return nil, fmt.Errorf("no tasks for scenario %s", scenario)
	}
	records := make([]benchRecord, 0, len(selected)*cfg.Rounds)
	for round := 1; round <= cfg.Rounds; round++ {
		if round > 1 && cfg.Delay > 0 {
			time.Sleep(cfg.Delay)
		}
		for _, task := range selected {
			record := runTask(cfg, round, task, tools, ops)
			if round < cfg.Rounds && cfg.Delay > 0 {
				record.SleepBeforeNextMS = cfg.Delay.Milliseconds()
			}
			if err := enc.Encode(record); err != nil {
				return records, fmt.Errorf("write record: %w", err)
			}
			printRecord(record)
			records = append(records, record)
		}
	}
	return records, nil
}

func tasksForScenario(tasks []benchTask, scenario string) []benchTask {
	var out []benchTask
	for _, task := range tasks {
		if task.Scenario == scenario {
			out = append(out, task)
		}
	}
	return out
}

func runTask(cfg benchConfig, round int, task benchTask, tools map[string]toolSpec, ops map[string]operationSpec) benchRecord {
	started := time.Now()
	result := runStrategy(cfg, task, tools, ops)
	metrics := evaluateTask(cfg, task, result, tools, ops)
	duration := time.Since(started)
	return benchRecord{
		Type:                   "record",
		Variant:                normalizeVariant(cfg.Variant),
		Scenario:               task.Scenario,
		Round:                  round,
		TaskID:                 task.ID,
		TaskType:               task.TaskType,
		Prompt:                 task.Prompt,
		NeedToolGold:           task.NeedTool,
		NeedToolProb:           result.NeedToolProb,
		NeedPredictionCorrect:  metrics.NeedPredictionCorrect,
		RequiredTools:          task.RequiredTools,
		RequiredOperations:     task.RequiredOperations,
		ForbiddenTools:         task.ForbiddenTools,
		ForbiddenOperations:    task.ForbiddenOperations,
		VisibleTools:           result.VisibleTools,
		CalledTools:            uniqueStrings(callToolNames(result.Calls)),
		CalledOperations:       callOperationNames(result.Calls),
		ToolCalls:              result.Calls,
		RequiredToolCount:      metrics.RequiredToolCount,
		RequiredOperationCount: metrics.RequiredOperationCount,
		CalledToolCount:        metrics.CalledToolCount,
		CalledOperationCount:   metrics.CalledOperationCount,
		ToolHitCount:           metrics.ToolHitCount,
		OperationHitCount:      metrics.OperationHitCount,
		ForbiddenCallCount:     metrics.ForbiddenCallCount,
		RedundantCalls:         metrics.RedundantCalls,
		ToolRecall:             metrics.ToolRecall,
		ToolPrecision:          metrics.ToolPrecision,
		OperationRecall:        metrics.OperationRecall,
		OperationPrecision:     metrics.OperationPrecision,
		RedundantRate:          metrics.RedundantRate,
		RouteRisk:              metrics.RouteRisk,
		ExpectedRouteRisk:      metrics.ExpectedRouteRisk,
		ToolTokenCost:          metrics.ToolTokenCost,
		ToolResultTokens:       metrics.ToolResultTokens,
		IrrelevantTokens:       metrics.IrrelevantTokens,
		ToolResultNoise:        metrics.ToolResultNoise,
		ToolAlignment:          metrics.ToolAlignment,
		InfoGain:               metrics.InfoGain,
		InfoEfficiency:         metrics.InfoEfficiency,
		ExpectedUtility:        metrics.ExpectedUtility,
		SuccessProbability:     metrics.SuccessProbability,
		ToolTuneScore:          metrics.ToolTuneScore,
		Success:                metrics.Success,
		Clean:                  metrics.Clean,
		StartedAt:              started,
		DurationNS:             duration.Nanoseconds(),
		DurationMS:             float64(duration.Nanoseconds()) / float64(time.Millisecond),
	}
}

func summarizeRecords(cfg benchConfig, scenario string, records []benchRecord) benchSummary {
	summary := benchSummary{
		Type:             "summary",
		Variant:          normalizeVariant(cfg.Variant),
		Scenario:         scenario,
		Rounds:           cfg.Rounds,
		Records:          len(records),
		Clean:            true,
		SuccessThreshold: cfg.SuccessThreshold,
		NeedThreshold:    cfg.NeedThreshold,
	}
	if len(records) == 0 {
		return summary
	}
	taskIDs := map[string]struct{}{}
	calledTools := map[string]struct{}{}
	var durations []float64
	var durationTotal, success, needCorrect float64
	var toolRecall, toolPrecision, opRecall, opPrecision, redundantRate, routeRisk, expectedRisk float64
	var tokenCost, resultTokens, noise, alignment, infoGain, infoEfficiency, utility, successProb, score float64
	for _, record := range records {
		taskIDs[record.TaskID] = struct{}{}
		durations = append(durations, record.DurationMS)
		durationTotal += record.DurationMS
		if record.Success {
			success++
		}
		if record.NeedPredictionCorrect {
			needCorrect++
		}
		if !record.Clean {
			summary.Clean = false
		}
		toolRecall += record.ToolRecall
		toolPrecision += record.ToolPrecision
		opRecall += record.OperationRecall
		opPrecision += record.OperationPrecision
		redundantRate += record.RedundantRate
		routeRisk += record.RouteRisk
		expectedRisk += record.ExpectedRouteRisk
		tokenCost += float64(record.ToolTokenCost)
		resultTokens += float64(record.ToolResultTokens)
		noise += record.ToolResultNoise
		alignment += record.ToolAlignment
		infoGain += record.InfoGain
		infoEfficiency += record.InfoEfficiency
		utility += record.ExpectedUtility
		successProb += record.SuccessProbability
		score += record.ToolTuneScore
		summary.ForbiddenCallCount += record.ForbiddenCallCount
		summary.RedundantCalls += record.RedundantCalls
		summary.TotalToolCalls += record.CalledOperationCount
		for _, name := range record.CalledTools {
			calledTools[name] = struct{}{}
		}
	}
	n := float64(len(records))
	summary.Tasks = len(taskIDs)
	summary.AvgDurationMS = durationTotal / n
	sort.Float64s(durations)
	summary.P50DurationMS = percentileFloat(durations, 0.50)
	summary.P95DurationMS = percentileFloat(durations, 0.95)
	summary.SuccessRate = success / n
	summary.ToolNeedAcc = needCorrect / n
	summary.AvgToolRecall = toolRecall / n
	summary.AvgToolPrecision = toolPrecision / n
	summary.AvgOperationRecall = opRecall / n
	summary.AvgOperationPrecision = opPrecision / n
	summary.AvgRedundantRate = redundantRate / n
	summary.AvgRouteRisk = routeRisk / n
	summary.AvgExpectedRouteRisk = expectedRisk / n
	summary.AvgToolTokenCost = tokenCost / n
	summary.AvgToolResultTokens = resultTokens / n
	summary.AvgToolResultNoise = noise / n
	summary.AvgToolAlignment = alignment / n
	summary.AvgInfoGain = infoGain / n
	summary.AvgInfoEfficiency = infoEfficiency / n
	summary.AvgExpectedUtility = utility / n
	summary.AvgSuccessProbability = successProb / n
	summary.AvgToolTuneScore = score / n
	summary.UniqueCalledTools = sortedSetKeys(calledTools)
	summary.SuccessPass = summary.SuccessRate >= cfg.SuccessThreshold
	return summary
}

func percentileFloat(sorted []float64, p float64) float64 {
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
		"%s round=%d task=%s need=%.2f calls=%d recall=%.2f precision=%.2f redundant=%.2f risk=%.2f noise=%.2f success=%.2f clean=%t\n",
		record.Scenario,
		record.Round,
		record.TaskID,
		record.NeedToolProb,
		record.CalledOperationCount,
		record.OperationRecall,
		record.OperationPrecision,
		record.RedundantRate,
		record.RouteRisk,
		record.ToolResultNoise,
		record.SuccessProbability,
		record.Clean,
	)
}

func printSummary(summary benchSummary) {
	fmt.Fprintf(os.Stderr,
		"summary scenario=%s records=%d success=%.2f need_acc=%.2f op_recall=%.2f op_precision=%.2f redundant=%.2f risk=%.2f noise=%.2f score=%.2f clean=%t\n",
		summary.Scenario,
		summary.Records,
		summary.SuccessRate,
		summary.ToolNeedAcc,
		summary.AvgOperationRecall,
		summary.AvgOperationPrecision,
		summary.AvgRedundantRate,
		summary.AvgRouteRisk,
		summary.AvgToolResultNoise,
		summary.AvgToolTuneScore,
		summary.Clean,
	)
}

func runCompare(cfg benchConfig) error {
	summaries, err := loadCompareSummaries(splitCSV(cfg.ComparePaths))
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(cfg.CompareFormat)) {
	case "", "markdown", "md":
		printCompareMarkdown(summaries)
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(summaries); err != nil {
			return fmt.Errorf("write comparison json: %w", err)
		}
	default:
		return fmt.Errorf("unknown compare-format %q", cfg.CompareFormat)
	}
	return nil
}

func loadCompareSummaries(paths []string) ([]benchSummary, error) {
	var out []benchSummary
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read compare file %s: %w", path, err)
		}
		var found bool
		for lineNo, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var probe struct {
				Type     string `json:"type"`
				Scenario string `json:"scenario"`
			}
			if err := json.Unmarshal([]byte(line), &probe); err != nil {
				return nil, fmt.Errorf("decode %s line %d: %w", path, lineNo+1, err)
			}
			if probe.Type != "summary" || probe.Scenario != "all" {
				continue
			}
			var summary benchSummary
			if err := json.Unmarshal([]byte(line), &summary); err != nil {
				return nil, fmt.Errorf("decode summary %s line %d: %w", path, lineNo+1, err)
			}
			out = append(out, summary)
			found = true
		}
		if !found {
			return nil, fmt.Errorf("compare file %s has no summary with scenario=all", path)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no comparison summaries loaded")
	}
	return out, nil
}

func printCompareMarkdown(summaries []benchSummary) {
	fmt.Println("| Variant | Success | NeedAcc | OpRecall | OpPrecision | Redundant | RouteRisk | Noise | Score | Forbidden | Clean |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |")
	for _, s := range summaries {
		fmt.Printf("| %s | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %d | %t |\n",
			s.Variant,
			s.SuccessRate,
			s.ToolNeedAcc,
			s.AvgOperationRecall,
			s.AvgOperationPrecision,
			s.AvgRedundantRate,
			s.AvgRouteRisk,
			s.AvgToolResultNoise,
			s.AvgToolTuneScore,
			s.ForbiddenCallCount,
			s.Clean,
		)
	}
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
