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
	ReplayPath       string
	ReplayLabelOut   string
	ReplayOnly       bool
	MDPSnapshotPath  string
	Rounds           int
	Delay            time.Duration
	SuccessThreshold float64
	MaxCoordOverhead float64
}

type benchRecord struct {
	Type                   string           `json:"type"`
	Variant                string           `json:"variant"`
	Scenario               string           `json:"scenario"`
	Round                  int              `json:"round"`
	TaskID                 string           `json:"task_id"`
	TaskType               string           `json:"task_type"`
	Prompt                 string           `json:"prompt"`
	GoldMode               string           `json:"gold_mode"`
	PredictedMode          string           `json:"predicted_mode"`
	ShouldSplitProb        float64          `json:"should_split_prob"`
	SplitPredictionCorrect bool             `json:"split_prediction_correct"`
	ModeCorrect            bool             `json:"mode_correct"`
	ForbiddenModeHit       bool             `json:"forbidden_mode_hit"`
	RequiredCapabilities   []string         `json:"required_capabilities,omitempty"`
	CoveredCapabilities    []string         `json:"covered_capabilities,omitempty"`
	MissingCapabilities    []string         `json:"missing_capabilities,omitempty"`
	SubtasksGold           []subtaskSpec    `json:"subtasks_gold,omitempty"`
	ExecutedSubtasks       []subtaskSpec    `json:"executed_subtasks,omitempty"`
	Assignments            []assignment     `json:"assignments,omitempty"`
	Aggregation            string           `json:"aggregation"`
	SubtaskRecall          float64          `json:"subtask_recall"`
	SubtaskPrecision       float64          `json:"subtask_precision"`
	CapabilityRecall       float64          `json:"capability_recall"`
	CapabilityPrecision    float64          `json:"capability_precision"`
	RoleFit                float64          `json:"role_fit"`
	DependencyViolations   int              `json:"dependency_violations"`
	RouteRisk              float64          `json:"route_risk"`
	SerialLatencyMS        float64          `json:"serial_latency_ms"`
	CriticalPathMS         float64          `json:"critical_path_ms"`
	SingleAgentLatencyMS   float64          `json:"single_agent_latency_ms"`
	Speedup                float64          `json:"speedup"`
	ParallelEfficiency     float64          `json:"parallel_efficiency"`
	WorkTokens             int              `json:"work_tokens"`
	TotalTokens            int              `json:"total_tokens"`
	SingleAgentTokens      int              `json:"single_agent_tokens"`
	CoordinatorTokens      int              `json:"coordinator_tokens"`
	CoordinationOverhead   float64          `json:"coordination_overhead"`
	AggregationQuality     float64          `json:"aggregation_quality"`
	ConsensusQuality       float64          `json:"consensus_quality"`
	BackgroundQueued       bool             `json:"background_queued"`
	BackgroundCorrect      bool             `json:"background_correct"`
	CriticUsed             bool             `json:"critic_used"`
	CriticRecall           float64          `json:"critic_recall"`
	SuccessProbability     float64          `json:"success_probability"`
	MultiAgentScore        float64          `json:"multi_agent_score"`
	Success                bool             `json:"success"`
	Clean                  bool             `json:"clean"`
	StartedAt              time.Time        `json:"started_at"`
	DurationNS             int64            `json:"duration_ns"`
	DurationMS             float64          `json:"duration_ms"`
	SleepBeforeNextMS      int64            `json:"sleep_before_next_ms,omitempty"`
	Diagnostics            *mathDiagnostics `json:"diagnostics,omitempty"`
}

type benchSummary struct {
	Type                     string   `json:"type"`
	Variant                  string   `json:"variant"`
	Scenario                 string   `json:"scenario"`
	Rounds                   int      `json:"rounds"`
	Tasks                    int      `json:"tasks"`
	Records                  int      `json:"records"`
	Clean                    bool     `json:"clean"`
	SuccessPass              bool     `json:"success_pass"`
	SuccessThreshold         float64  `json:"success_threshold"`
	MaxCoordOverhead         float64  `json:"max_coordination_overhead"`
	AvgDurationMS            float64  `json:"avg_duration_ms"`
	P50DurationMS            float64  `json:"p50_duration_ms"`
	P95DurationMS            float64  `json:"p95_duration_ms"`
	SuccessRate              float64  `json:"success_rate"`
	SplitAccuracy            float64  `json:"split_accuracy"`
	ModeAccuracy             float64  `json:"mode_accuracy"`
	AvgSubtaskRecall         float64  `json:"avg_subtask_recall"`
	AvgSubtaskPrecision      float64  `json:"avg_subtask_precision"`
	AvgCapabilityRecall      float64  `json:"avg_capability_recall"`
	AvgCapabilityPrecision   float64  `json:"avg_capability_precision"`
	AvgRoleFit               float64  `json:"avg_role_fit"`
	AvgRouteRisk             float64  `json:"avg_route_risk"`
	AvgDependencyViolations  float64  `json:"avg_dependency_violations"`
	AvgSpeedup               float64  `json:"avg_speedup"`
	AvgParallelEfficiency    float64  `json:"avg_parallel_efficiency"`
	AvgCoordinationOverhead  float64  `json:"avg_coordination_overhead"`
	AvgAggregationQuality    float64  `json:"avg_aggregation_quality"`
	AvgConsensusQuality      float64  `json:"avg_consensus_quality"`
	AvgCriticRecall          float64  `json:"avg_critic_recall"`
	AvgSuccessProbability    float64  `json:"avg_success_probability"`
	AvgMultiAgentScore       float64  `json:"avg_multi_agent_score"`
	AvgExpectedUtility       float64  `json:"avg_expected_utility"`
	AvgPathWeight            float64  `json:"avg_path_weight"`
	AvgEstimatedSuccess      float64  `json:"avg_estimated_success"`
	AvgEdgeNLL               float64  `json:"avg_edge_nll"`
	CalibrationECE           float64  `json:"calibration_ece"`
	AvgLyapunovDecrease      float64  `json:"avg_lyapunov_decrease"`
	LyapunovDecreaseRate     float64  `json:"lyapunov_decrease_rate"`
	ReplanRate               float64  `json:"replan_rate"`
	AvgReplanCount           float64  `json:"avg_replan_count"`
	VerifierRequiredCount    int      `json:"verifier_required_count"`
	VerifierAvailableCount   int      `json:"verifier_available_count"`
	VerifierCatchRate        float64  `json:"verifier_catch_rate"`
	OODCount                 int      `json:"ood_count"`
	OODRate                  float64  `json:"ood_rate"`
	AvgPathRegret            float64  `json:"avg_path_regret"`
	ConstraintViolationCount int      `json:"constraint_violation_count"`
	VerifierNeedAccuracy     float64  `json:"verifier_need_accuracy"`
	OODFalseNegativeRate     float64  `json:"ood_false_negative_rate"`
	ForbiddenModeCount       int      `json:"forbidden_mode_count"`
	DependencyViolationCount int      `json:"dependency_violation_count"`
	TotalCoordinatorTokens   int      `json:"total_coordinator_tokens"`
	TotalTokens              int      `json:"total_tokens"`
	UsedModes                []string `json:"used_modes,omitempty"`
	UniqueAssignedAgents     []string `json:"unique_assigned_agents,omitempty"`
	ComparedScenarios        []string `json:"compared_scenarios,omitempty"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "lh-multiagent-bench: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() benchConfig {
	now := time.Now().Format("20060102-150405")
	defaultOut := filepath.Join(os.TempDir(), "lh-multiagent-bench", "results-"+now+".jsonl")
	var cfg benchConfig
	flag.StringVar(&cfg.Variant, "variant", "baseline", "multi-agent strategy variant: baseline, capability-routed, parallel-routed, dependency-aware, debate-review, runtime-mdp-v1, math-mdp-v1, math-ssp-v1, math-lyapunov-v1, math-verifier-v1, math-full-v1")
	flag.StringVar(&cfg.Scenario, "scenario", "all", "scenario to run: single, parallel, pipeline, debate, autonomy, heavy, or all")
	flag.StringVar(&cfg.OutPath, "out", defaultOut, "JSONL output path")
	flag.StringVar(&cfg.ComparePaths, "compare", "", "comma-separated JSONL result files to compare instead of running benchmark")
	flag.StringVar(&cfg.CompareFormat, "compare-format", "markdown", "comparison output format: markdown or json")
	flag.StringVar(&cfg.ReplayPath, "replay", "", "optional JSON, JSONL, session .md, or directory of historical lh sessions to replay")
	flag.StringVar(&cfg.ReplayLabelOut, "replay-label-out", "", "write semi-automatic replay labels to this JSONL path and exit")
	flag.BoolVar(&cfg.ReplayOnly, "replay-only", false, "run only cases loaded from -replay")
	flag.StringVar(&cfg.MDPSnapshotPath, "mdp-snapshot", "", "optional runtime-mdp-v1 JSON snapshot path to load before and save after benchmark")
	flag.IntVar(&cfg.Rounds, "rounds", 3, "rounds per scenario")
	flag.DurationVar(&cfg.Delay, "delay", 0, "delay between rounds")
	flag.Float64Var(&cfg.SuccessThreshold, "success-threshold", 0.70, "probability threshold for marking a task successful")
	flag.Float64Var(&cfg.MaxCoordOverhead, "max-coord-overhead", 0.30, "maximum accepted coordination overhead for clean records")
	flag.Parse()
	return cfg
}

func run(cfg benchConfig) error {
	if strings.TrimSpace(cfg.ComparePaths) != "" {
		return runCompare(cfg)
	}
	if strings.TrimSpace(cfg.ReplayLabelOut) != "" {
		return runReplayLabelExport(cfg)
	}
	if cfg.Rounds <= 0 {
		return fmt.Errorf("rounds must be positive")
	}
	if cfg.SuccessThreshold <= 0 || cfg.SuccessThreshold >= 1 {
		return fmt.Errorf("success-threshold must be between 0 and 1")
	}
	if cfg.MaxCoordOverhead <= 0 || cfg.MaxCoordOverhead >= 1 {
		return fmt.Errorf("max-coord-overhead must be between 0 and 1")
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

	agents := defaultAgents()
	tasks := defaultTasks()
	if strings.TrimSpace(cfg.ReplayPath) != "" {
		replayTasks, err := loadReplayTasks(cfg.ReplayPath)
		if err != nil {
			return err
		}
		if cfg.ReplayOnly {
			tasks = replayTasks
		} else {
			tasks = append(tasks, replayTasks...)
		}
	}
	scenarios, err := expandScenarios(cfg.Scenario, tasks)
	if err != nil {
		return err
	}
	runner, err := newStrategyRunner(cfg)
	if err != nil {
		return err
	}

	var all []benchRecord
	for _, scenario := range scenarios {
		records, err := runScenario(cfg, scenario, tasks, agents, enc, runner)
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
	if runner != nil {
		if err := runner.Save(); err != nil {
			return err
		}
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

func runScenario(cfg benchConfig, scenario string, tasks []benchTask, agents map[string]agentSpec, enc *json.Encoder, runner *strategyRunner) ([]benchRecord, error) {
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
			record := runTaskWithRunner(cfg, round, task, agents, runner)
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

func runTask(cfg benchConfig, round int, task benchTask, agents map[string]agentSpec) benchRecord {
	return runTaskWithRunner(cfg, round, task, agents, nil)
}

func runTaskWithRunner(cfg benchConfig, round int, task benchTask, agents map[string]agentSpec, runner *strategyRunner) benchRecord {
	started := time.Now()
	result := runStrategy(cfg, task, agents)
	if runner != nil {
		result = runner.Run(cfg, task, agents)
	}
	metrics := evaluateTask(cfg, task, result, agents)
	if runner != nil {
		runner.Observe(task, result, metrics, agents)
	}
	duration := time.Since(started)
	return benchRecord{
		Type:                   "record",
		Variant:                normalizeVariant(cfg.Variant),
		Scenario:               task.Scenario,
		Round:                  round,
		TaskID:                 task.ID,
		TaskType:               task.TaskType,
		Prompt:                 task.Prompt,
		GoldMode:               task.GoldMode,
		PredictedMode:          result.Mode,
		ShouldSplitProb:        result.ShouldSplitProb,
		SplitPredictionCorrect: metrics.SplitPredictionCorrect,
		ModeCorrect:            metrics.ModeCorrect,
		ForbiddenModeHit:       metrics.ForbiddenModeHit,
		RequiredCapabilities:   task.RequiredCapabilities,
		CoveredCapabilities:    metrics.CoveredCapabilities,
		MissingCapabilities:    metrics.MissingCapabilities,
		SubtasksGold:           task.Subtasks,
		ExecutedSubtasks:       result.ExecutedSubtasks,
		Assignments:            result.Assignments,
		Aggregation:            result.Aggregation,
		SubtaskRecall:          metrics.SubtaskRecall,
		SubtaskPrecision:       metrics.SubtaskPrecision,
		CapabilityRecall:       metrics.CapabilityRecall,
		CapabilityPrecision:    metrics.CapabilityPrecision,
		RoleFit:                metrics.RoleFit,
		DependencyViolations:   metrics.DependencyViolations,
		RouteRisk:              metrics.RouteRisk,
		SerialLatencyMS:        metrics.SerialLatencyMS,
		CriticalPathMS:         metrics.CriticalPathMS,
		SingleAgentLatencyMS:   metrics.SingleAgentLatencyMS,
		Speedup:                metrics.Speedup,
		ParallelEfficiency:     metrics.ParallelEfficiency,
		WorkTokens:             metrics.WorkTokens,
		TotalTokens:            metrics.TotalTokens,
		SingleAgentTokens:      metrics.SingleAgentTokens,
		CoordinatorTokens:      result.CoordinatorTokens,
		CoordinationOverhead:   metrics.CoordinationOverhead,
		AggregationQuality:     metrics.AggregationQuality,
		ConsensusQuality:       metrics.ConsensusQuality,
		BackgroundQueued:       result.BackgroundQueued,
		BackgroundCorrect:      metrics.BackgroundCorrect,
		CriticUsed:             result.CriticUsed,
		CriticRecall:           metrics.CriticRecall,
		SuccessProbability:     metrics.SuccessProbability,
		MultiAgentScore:        metrics.MultiAgentScore,
		Success:                metrics.Success,
		Clean:                  metrics.Clean,
		StartedAt:              started,
		DurationNS:             duration.Nanoseconds(),
		DurationMS:             float64(duration.Nanoseconds()) / float64(time.Millisecond),
		Diagnostics:            result.Diagnostics,
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
		MaxCoordOverhead: cfg.MaxCoordOverhead,
	}
	if len(records) == 0 {
		return summary
	}
	taskIDs := map[string]struct{}{}
	modes := map[string]struct{}{}
	agents := map[string]struct{}{}
	var durations []float64
	var durationTotal, success, splitCorrect, modeCorrect float64
	var subtaskRecall, subtaskPrecision, capRecall, capPrecision, roleFit, routeRisk, depViolations float64
	var speedup, parallelEff, coordOverhead, aggregationQuality, consensusQuality, criticRecall, successProb, score float64
	var diagCount, expectedUtility, pathWeight, estimatedSuccess, edgeNLL, calibrationECE, lyapunovDecrease, lyapunovDecreasePositive float64
	var replan, replanCount, verifierCatch, pathRegret, verifierNeedCorrect, oodActual, oodFalseNegative float64
	for _, record := range records {
		taskIDs[record.TaskID] = struct{}{}
		modes[record.PredictedMode] = struct{}{}
		durations = append(durations, record.DurationMS)
		durationTotal += record.DurationMS
		if record.Success {
			success++
		}
		if record.SplitPredictionCorrect {
			splitCorrect++
		}
		if record.ModeCorrect {
			modeCorrect++
		}
		if record.ForbiddenModeHit {
			summary.ForbiddenModeCount++
		}
		if !record.Clean {
			summary.Clean = false
		}
		for _, asg := range record.Assignments {
			agents[asg.AgentID] = struct{}{}
		}
		subtaskRecall += record.SubtaskRecall
		subtaskPrecision += record.SubtaskPrecision
		capRecall += record.CapabilityRecall
		capPrecision += record.CapabilityPrecision
		roleFit += record.RoleFit
		routeRisk += record.RouteRisk
		depViolations += float64(record.DependencyViolations)
		speedup += record.Speedup
		parallelEff += record.ParallelEfficiency
		coordOverhead += record.CoordinationOverhead
		aggregationQuality += record.AggregationQuality
		consensusQuality += record.ConsensusQuality
		criticRecall += record.CriticRecall
		successProb += record.SuccessProbability
		score += record.MultiAgentScore
		if record.Diagnostics != nil {
			diagCount++
			expectedUtility += record.Diagnostics.ExpectedUtility
			pathWeight += record.Diagnostics.PathWeight
			estimatedSuccess += record.Diagnostics.EstimatedSuccess
			edgeNLL += record.Diagnostics.EdgeNLL
			calibrationECE += record.Diagnostics.CalibrationECE
			lyapunovDecrease += record.Diagnostics.LyapunovDecrease
			lyapunovDecreasePositive += boolFloat(record.Diagnostics.LyapunovDecrease > 0)
			replan += boolFloat(record.Diagnostics.ReplanRecommended)
			replanCount += float64(record.Diagnostics.ReplanCount)
			verifierCatch += record.Diagnostics.VerifierExpectedCatch
			pathRegret += record.Diagnostics.PathRegret
			if record.Diagnostics.VerifierRequired {
				summary.VerifierRequiredCount++
			}
			if record.Diagnostics.VerifierAvailable {
				summary.VerifierAvailableCount++
			}
			if record.Diagnostics.OutOfDistribution {
				summary.OODCount++
			}
			verifierNeedCorrect += boolFloat(record.Diagnostics.VerifierRequired == record.CriticUsed)
			actualOOD := strings.Contains(record.Scenario, "replay") && (record.TaskType == "replay_hermes" || record.TaskType == "replay_benchmark") && record.GoldMode != "single"
			if actualOOD {
				oodActual++
				if !record.Diagnostics.OutOfDistribution {
					oodFalseNegative++
				}
			}
			summary.ConstraintViolationCount += len(record.Diagnostics.ConstraintViolations)
		}
		summary.DependencyViolationCount += record.DependencyViolations
		summary.TotalCoordinatorTokens += record.CoordinatorTokens
		summary.TotalTokens += record.TotalTokens
	}
	n := float64(len(records))
	summary.Tasks = len(taskIDs)
	summary.AvgDurationMS = durationTotal / n
	sort.Float64s(durations)
	summary.P50DurationMS = percentileFloat(durations, 0.50)
	summary.P95DurationMS = percentileFloat(durations, 0.95)
	summary.SuccessRate = success / n
	summary.SplitAccuracy = splitCorrect / n
	summary.ModeAccuracy = modeCorrect / n
	summary.AvgSubtaskRecall = subtaskRecall / n
	summary.AvgSubtaskPrecision = subtaskPrecision / n
	summary.AvgCapabilityRecall = capRecall / n
	summary.AvgCapabilityPrecision = capPrecision / n
	summary.AvgRoleFit = roleFit / n
	summary.AvgRouteRisk = routeRisk / n
	summary.AvgDependencyViolations = depViolations / n
	summary.AvgSpeedup = speedup / n
	summary.AvgParallelEfficiency = parallelEff / n
	summary.AvgCoordinationOverhead = coordOverhead / n
	summary.AvgAggregationQuality = aggregationQuality / n
	summary.AvgConsensusQuality = consensusQuality / n
	summary.AvgCriticRecall = criticRecall / n
	summary.AvgSuccessProbability = successProb / n
	summary.AvgMultiAgentScore = score / n
	if diagCount > 0 {
		summary.AvgExpectedUtility = expectedUtility / diagCount
		summary.AvgPathWeight = pathWeight / diagCount
		summary.AvgEstimatedSuccess = estimatedSuccess / diagCount
		summary.AvgEdgeNLL = edgeNLL / diagCount
		summary.CalibrationECE = calibrationECE / diagCount
		summary.AvgLyapunovDecrease = lyapunovDecrease / diagCount
		summary.LyapunovDecreaseRate = lyapunovDecreasePositive / diagCount
		summary.ReplanRate = replan / diagCount
		summary.AvgReplanCount = replanCount / diagCount
		summary.VerifierCatchRate = verifierCatch / diagCount
		summary.OODRate = float64(summary.OODCount) / diagCount
		summary.AvgPathRegret = pathRegret / diagCount
		summary.VerifierNeedAccuracy = verifierNeedCorrect / diagCount
		if oodActual > 0 {
			summary.OODFalseNegativeRate = oodFalseNegative / oodActual
		}
	}
	summary.UsedModes = sortedSetKeys(modes)
	summary.UniqueAssignedAgents = sortedSetKeys(agents)
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
		"%s round=%d task=%s mode=%s/%s split=%.2f cap=%.2f sub=%.2f risk=%.2f dep=%d speedup=%.2f coord=%.2f success=%.2f clean=%t\n",
		record.Scenario,
		record.Round,
		record.TaskID,
		record.PredictedMode,
		record.GoldMode,
		record.ShouldSplitProb,
		record.CapabilityRecall,
		record.SubtaskRecall,
		record.RouteRisk,
		record.DependencyViolations,
		record.Speedup,
		record.CoordinationOverhead,
		record.SuccessProbability,
		record.Clean,
	)
}

func printSummary(summary benchSummary) {
	fmt.Fprintf(os.Stderr,
		"summary scenario=%s records=%d success=%.2f split=%.2f mode=%.2f cap=%.2f sub=%.2f risk=%.2f dep=%.2f speedup=%.2f coord=%.2f score=%.2f clean=%t\n",
		summary.Scenario,
		summary.Records,
		summary.SuccessRate,
		summary.SplitAccuracy,
		summary.ModeAccuracy,
		summary.AvgCapabilityRecall,
		summary.AvgSubtaskRecall,
		summary.AvgRouteRisk,
		summary.AvgDependencyViolations,
		summary.AvgSpeedup,
		summary.AvgCoordinationOverhead,
		summary.AvgMultiAgentScore,
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
	fmt.Println("| Variant | Success | SplitAcc | ModeAcc | CapRecall | SubRecall | RouteRisk | DepViol | Speedup | CoordOH | Score | ECE | LyapRate | Replan | Regret | Forbidden | Clean |")
	fmt.Println("| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | :---: |")
	for _, s := range summaries {
		fmt.Printf("| %s | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %.4f | %d | %t |\n",
			s.Variant,
			s.SuccessRate,
			s.SplitAccuracy,
			s.ModeAccuracy,
			s.AvgCapabilityRecall,
			s.AvgSubtaskRecall,
			s.AvgRouteRisk,
			s.AvgDependencyViolations,
			s.AvgSpeedup,
			s.AvgCoordinationOverhead,
			s.AvgMultiAgentScore,
			s.CalibrationECE,
			s.LyapunovDecreaseRate,
			s.ReplanRate,
			s.AvgPathRegret,
			s.ForbiddenModeCount,
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
