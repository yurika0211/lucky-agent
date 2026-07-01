package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/collab"
)

type strategyRunner struct {
	variant        string
	runtimePlanner *collab.Planner
	snapshotPath   string
}

func newStrategyRunner(cfg benchConfig) (*strategyRunner, error) {
	variant := normalizeVariant(cfg.Variant)
	runner := &strategyRunner{variant: variant, snapshotPath: strings.TrimSpace(cfg.MDPSnapshotPath)}
	if variant != "runtime-mdp-v1" {
		return runner, nil
	}
	planner := collab.NewPlanner(nil)
	if runner.snapshotPath != "" {
		if err := planner.LoadMDP(runner.snapshotPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load runtime mdp snapshot: %w", err)
		}
	}
	runner.runtimePlanner = planner
	return runner, nil
}

func (r *strategyRunner) Run(cfg benchConfig, task benchTask, agents map[string]agentSpec) strategyResult {
	if r == nil || r.variant != "runtime-mdp-v1" {
		return runStrategy(cfg, task, agents)
	}
	if r.runtimePlanner == nil {
		r.runtimePlanner = collab.NewPlanner(nil)
	}
	return runRuntimeMDPStrategy(r.runtimePlanner, task, agents)
}

func (r *strategyRunner) Observe(task benchTask, result strategyResult, metrics taskMetrics, agents map[string]agentSpec) {
	if r == nil || r.variant != "runtime-mdp-v1" || r.runtimePlanner == nil {
		return
	}
	mode := collabModeFromBench(result.Mode)
	if mode == "" {
		return
	}
	outcome := runtimeMDPOutcome(metrics)
	duration := time.Duration(metrics.CriticalPathMS * float64(time.Millisecond))
	r.runtimePlanner.ObserveExecution(collabRequestFromBenchTask(task, agents), mode, outcome, duration)
}

func (r *strategyRunner) Save() error {
	if r == nil || r.variant != "runtime-mdp-v1" || r.snapshotPath == "" || r.runtimePlanner == nil {
		return nil
	}
	return r.runtimePlanner.SaveMDP(r.snapshotPath)
}

func runRuntimeMDPStrategy(planner *collab.Planner, task benchTask, agents map[string]agentSpec) strategyResult {
	if planner == nil {
		planner = collab.NewPlanner(nil)
	}
	if runtimeMDPShouldStaySingle(task) {
		return runtimeMDPSingleResult(task, agents)
	}
	if task.AllowsBackground && containsAny(strings.ToLower(task.Prompt), "后台", "异步", "worker", "队列", "autonomy") {
		return runtimeMDPBackgroundResult(task, agents)
	}

	req := collabRequestFromBenchTask(task, agents)
	plan := planner.Plan(req)
	mode := string(plan.Mode)
	executed := chooseSubtasks("dependency-aware", mode, task)
	assignments := assignSubtasks("dependency-aware", mode, executed, agents)
	diag := runtimeMDPDiagnostics(plan, task, mode)
	return strategyResult{
		Mode:               mode,
		ShouldSplitProb:    estimateSplitProbability("parallel-routed", task),
		Assignments:        assignments,
		ExecutedSubtasks:   executed,
		Aggregation:        runtimeMDPAggregation(plan, mode, task),
		CoordinatorTokens:  coordinatorTokens("dependency-aware", mode, len(executed), task),
		CoordinatorLatency: coordinatorLatencyMS("dependency-aware", mode, len(executed)),
		BackgroundQueued:   false,
		CriticUsed:         runtimeMDPUsesCritic(plan, assignments),
		DependencyAware:    true,
		Diagnostics:        &diag,
	}
}

func runtimeMDPSingleResult(task benchTask, agents map[string]agentSpec) strategyResult {
	executed := chooseSubtasks("dependency-aware", "single", task)
	assignments := assignSubtasks("dependency-aware", "single", executed, agents)
	return strategyResult{
		Mode:               "single",
		ShouldSplitProb:    estimateSplitProbability("parallel-routed", task),
		Assignments:        assignments,
		ExecutedSubtasks:   executed,
		Aggregation:        "none",
		CoordinatorTokens:  coordinatorTokens("dependency-aware", "single", len(executed), task),
		CoordinatorLatency: coordinatorLatencyMS("dependency-aware", "single", len(executed)),
		CriticUsed:         false,
		DependencyAware:    true,
	}
}

func runtimeMDPBackgroundResult(task benchTask, agents map[string]agentSpec) strategyResult {
	executed := chooseSubtasks("dependency-aware", "autonomy_queue", task)
	assignments := assignSubtasks("dependency-aware", "autonomy_queue", executed, agents)
	return strategyResult{
		Mode:               "autonomy_queue",
		ShouldSplitProb:    estimateSplitProbability("parallel-routed", task),
		Assignments:        assignments,
		ExecutedSubtasks:   executed,
		Aggregation:        "report",
		CoordinatorTokens:  coordinatorTokens("dependency-aware", "autonomy_queue", len(executed), task),
		CoordinatorLatency: coordinatorLatencyMS("dependency-aware", "autonomy_queue", len(executed)),
		BackgroundQueued:   true,
		CriticUsed:         usesCritic(assignments),
		DependencyAware:    true,
	}
}

func runtimeMDPShouldStaySingle(task benchTask) bool {
	prompt := strings.ToLower(task.Prompt)
	if containsAny(prompt, "不要拆", "不要新建", "只查看", "解释", "概括", "推导") && !containsAny(prompt, "辩论", "并行", "先", "再", "最后") {
		return true
	}
	return len(task.Subtasks) <= 1 && estimateSplitProbability("parallel-routed", task) < 0.50
}

func collabRequestFromBenchTask(task benchTask, agents map[string]agentSpec) collab.PlanRequest {
	agentIDs := make([]string, 0, len(task.Subtasks))
	profiles := make([]*collab.AgentProfile, 0, len(task.Subtasks))
	seen := map[string]bool{}
	for _, sub := range task.Subtasks {
		agentID, _, _ := bestAgentForSubtask(sub, agents)
		if seen[agentID] {
			continue
		}
		seen[agentID] = true
		agentIDs = append(agentIDs, agentID)
		spec := agents[agentID]
		profiles = append(profiles, &collab.AgentProfile{
			ID:           spec.ID,
			Name:         spec.Name,
			Capabilities: append([]string(nil), spec.Capabilities...),
			Status:       collab.StatusOnline,
		})
	}
	if len(agentIDs) == 0 {
		agentIDs = []string{"generalist"}
		spec := agents["generalist"]
		profiles = append(profiles, &collab.AgentProfile{ID: spec.ID, Name: spec.Name, Capabilities: append([]string(nil), spec.Capabilities...), Status: collab.StatusOnline})
	}
	return collab.PlanRequest{
		Description:  task.Prompt,
		Input:        strings.Join(task.IntentTerms, " "),
		AgentIDs:     agentIDs,
		Agents:       profiles,
		Timeout:      90 * time.Second,
		AllowedModes: []collab.CollabMode{collab.ModeParallel, collab.ModePipeline, collab.ModeDebate},
	}
}

func collabModeFromBench(mode string) collab.CollabMode {
	switch normalizeMode(mode) {
	case "parallel":
		return collab.ModeParallel
	case "pipeline":
		return collab.ModePipeline
	case "debate":
		return collab.ModeDebate
	default:
		return ""
	}
}

func runtimeMDPOutcome(metrics taskMetrics) string {
	switch {
	case metrics.ForbiddenModeHit:
		return "blocked"
	case metrics.DependencyViolations > 0:
		return "failure"
	case metrics.Success && metrics.Clean && metrics.ModeCorrect:
		return "success"
	case metrics.Success && !metrics.ModeCorrect:
		return "partial"
	case metrics.Success:
		return "partial"
	default:
		return "failure"
	}
}

func runtimeMDPAggregation(plan collab.PlanResult, mode string, task benchTask) string {
	cmode := collab.CollabMode(mode)
	if action, ok := plan.MDP.Actions[cmode]; ok && action.Aggregation != "" {
		return action.Aggregation
	}
	return chooseAggregation("dependency-aware", mode, task)
}

func runtimeMDPUsesCritic(plan collab.PlanResult, assignments []assignment) bool {
	if usesCritic(assignments) {
		return true
	}
	if action, ok := plan.MDP.Actions[plan.Mode]; ok {
		return action.RequireVerifier || action.Aggregation == "critic_merge"
	}
	return false
}

func runtimeMDPDiagnostics(plan collab.PlanResult, task benchTask, mode string) mathDiagnostics {
	candidates := make([]candidateScore, 0, len(plan.Candidates))
	modeUtils := make(map[string]float64, len(plan.Candidates))
	for _, candidate := range plan.Candidates {
		utility := 1 / (1 + math.Max(0, candidate.DecisionWeight))
		candidates = append(candidates, candidateScore{
			Mode:             string(candidate.Mode),
			ExpectedUtility:  utility,
			PathWeight:       candidate.DecisionWeight,
			EstimatedSuccess: candidate.SuccessProb,
			EstimatedCost:    candidate.EstimatedCost,
			EstimatedRisk:    candidate.EstimatedRisk,
		})
		modeUtils[string(candidate.Mode)] = utility
	}
	trace := make([]mathTraceEdge, 0, len(plan.Trace))
	for _, edge := range plan.Trace {
		trace = append(trace, mathTraceEdge{
			From:          edge.From,
			To:            edge.To,
			Action:        string(edge.Action),
			EstimatedProb: 1 / (1 + math.Max(0, edge.Weight)),
			EdgeWeight:    edge.Weight,
		})
	}
	chosen := candidateForMode(candidates, mode)
	if chosen.Mode == "" {
		chosen = candidateScore{Mode: mode, ExpectedUtility: 1 / (1 + math.Max(0, plan.TotalWeight)), PathWeight: plan.TotalWeight}
	}
	actionKey := ""
	if action, ok := plan.MDP.Actions[plan.Mode]; ok {
		actionKey = action.Key()
	}
	return mathDiagnostics{
		PlannerVariant:         "runtime-mdp-v1",
		TraceID:                plan.MDP.StateKey,
		ContextFeatures:        buildMathContext(task),
		CandidateScores:        candidates,
		Trace:                  trace,
		ExpectedUtility:        chosen.ExpectedUtility,
		PathWeight:             plan.TotalWeight,
		EstimatedSuccess:       chosen.EstimatedSuccess,
		EstimatedCost:          chosen.EstimatedCost,
		EstimatedRisk:          chosen.EstimatedRisk,
		EdgeNLL:                math.Max(0.001, plan.TotalWeight),
		CalibrationECE:         math.Abs(chosen.EstimatedSuccess - chosen.ExpectedUtility),
		VerifierRequired:       runtimeMDPUsesCritic(plan, nil),
		VerifierAvailable:      plan.MDP.State.HasVerifier,
		VerifierExpectedPass:   boolFloat(plan.MDP.State.HasVerifier) * 0.82,
		VerifierExpectedCatch:  boolFloat(plan.MDP.State.HasVerifier) * 0.64,
		RecommendedNextAction:  actionKey,
		DecisionRationale:      fmt.Sprintf("state=%s q=%s", plan.MDP.StateKey, mdpQSummary(plan.MDP.QValues)),
		ModeEstimatedUtilities: modeUtils,
	}
}

func candidateForMode(candidates []candidateScore, mode string) candidateScore {
	for _, candidate := range candidates {
		if candidate.Mode == mode {
			return candidate
		}
	}
	return candidateScore{}
}

func mdpQSummary(values map[collab.CollabMode]float64) string {
	parts := make([]string, 0, len(values))
	for mode, value := range values {
		parts = append(parts, fmt.Sprintf("%s=%.3f", mode, value))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}
