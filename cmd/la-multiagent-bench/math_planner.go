package main

import (
	"math"
	"strings"
)

type mathDiagnostics struct {
	PlannerVariant         string             `json:"planner_variant"`
	TraceID                string             `json:"trace_id"`
	ContextFeatures        mathContext        `json:"context_features"`
	CandidateScores        []candidateScore   `json:"candidate_scores,omitempty"`
	Trace                  []mathTraceEdge    `json:"trace,omitempty"`
	ExpectedUtility        float64            `json:"expected_utility"`
	PathWeight             float64            `json:"path_weight"`
	EstimatedSuccess       float64            `json:"estimated_success"`
	EstimatedCost          float64            `json:"estimated_cost"`
	EstimatedRisk          float64            `json:"estimated_risk"`
	EdgeNLL                float64            `json:"edge_nll"`
	CalibrationECE         float64            `json:"calibration_ece"`
	LyapunovStart          float64            `json:"lyapunov_start"`
	LyapunovEnd            float64            `json:"lyapunov_end"`
	LyapunovDecrease       float64            `json:"lyapunov_decrease"`
	LyapunovDecreaseRate   float64            `json:"lyapunov_decrease_rate"`
	ReplanRecommended      bool               `json:"replan_recommended"`
	ReplanCount            int                `json:"replan_count"`
	VerifierRequired       bool               `json:"verifier_required"`
	VerifierAvailable      bool               `json:"verifier_available"`
	VerifierExpectedPass   float64            `json:"verifier_expected_pass"`
	VerifierExpectedCatch  float64            `json:"verifier_expected_catch"`
	OutOfDistribution      bool               `json:"out_of_distribution"`
	ConstraintViolations   []string           `json:"constraint_violations,omitempty"`
	PathRegret             float64            `json:"path_regret"`
	RecommendedNextAction  string             `json:"recommended_next_action"`
	DecisionRationale      string             `json:"decision_rationale"`
	ModeEstimatedUtilities map[string]float64 `json:"mode_estimated_utilities,omitempty"`
}

type mathContext struct {
	TaskType            string  `json:"task_type"`
	SubtaskCount        int     `json:"subtask_count"`
	DependencyDepth     int     `json:"dependency_depth"`
	SubtaskIndependence float64 `json:"subtask_independence"`
	Ambiguity           float64 `json:"ambiguity"`
	RiskLevel           float64 `json:"risk_level"`
	TestObservability   float64 `json:"test_observability"`
	ContextSufficiency  float64 `json:"context_sufficiency"`
	ToolAvailability    float64 `json:"tool_availability"`
	NeedsCritic         bool    `json:"needs_critic"`
	AllowsBackground    bool    `json:"allows_background"`
	HasForbiddenModes   bool    `json:"has_forbidden_modes"`
	SuperHeavy          bool    `json:"super_heavy"`
}

type candidateScore struct {
	Mode              string   `json:"mode"`
	ExpectedUtility   float64  `json:"expected_utility"`
	PathWeight        float64  `json:"path_weight"`
	EstimatedSuccess  float64  `json:"estimated_success"`
	EstimatedCost     float64  `json:"estimated_cost"`
	EstimatedRisk     float64  `json:"estimated_risk"`
	LyapunovEnd       float64  `json:"lyapunov_end"`
	ConstraintPenalty float64  `json:"constraint_penalty"`
	Rejected          bool     `json:"rejected"`
	RejectReasons     []string `json:"reject_reasons,omitempty"`
}

type mathTraceEdge struct {
	From            string   `json:"from"`
	Action          string   `json:"action"`
	To              string   `json:"to"`
	SubtaskID       string   `json:"subtask_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	EstimatedProb   float64  `json:"estimated_prob"`
	EdgeWeight      float64  `json:"edge_weight"`
	EstimatedCost   float64  `json:"estimated_cost"`
	EstimatedRisk   float64  `json:"estimated_risk"`
	LyapunovBefore  float64  `json:"lyapunov_before"`
	LyapunovAfter   float64  `json:"lyapunov_after"`
	CapabilitiesHit []string `json:"capabilities_hit,omitempty"`
}

func isMathVariant(variant string) bool {
	return strings.HasPrefix(variant, "math-")
}

func runMathStrategy(variant string, task benchTask, agents map[string]agentSpec) strategyResult {
	ctx := buildMathContext(task)
	candidates := scoreModeCandidates(variant, task, agents, ctx)
	chosen := chooseMathCandidate(variant, candidates)
	mode := chosen.Mode
	executed := chooseMathSubtasks(mode, task)
	assignments := assignSubtasks("dependency-aware", mode, executed, agents)
	aggregation := chooseMathAggregation(variant, mode, task, ctx)
	trace := buildMathTrace(task, agents, ctx, mode, executed, assignments)
	diag := buildMathDiagnostics(variant, ctx, candidates, chosen, trace, task, mode)
	return strategyResult{
		Mode:               mode,
		ShouldSplitProb:    mathSplitProbability(ctx),
		Assignments:        assignments,
		ExecutedSubtasks:   executed,
		Aggregation:        aggregation,
		CoordinatorTokens:  mathCoordinatorTokens(variant, mode, len(executed), task, ctx),
		CoordinatorLatency: mathCoordinatorLatencyMS(variant, mode, len(executed), ctx),
		BackgroundQueued:   mode == "autonomy_queue",
		CriticUsed:         mathUsesCritic(variant, mode, task, assignments),
		DependencyAware:    true,
		Diagnostics:        &diag,
	}
}

func buildMathContext(task benchTask) mathContext {
	subCount := len(task.Subtasks)
	depEdges := 0
	for _, sub := range task.Subtasks {
		depEdges += len(sub.DependsOn)
	}
	depth := dependencyDepth(task.Subtasks)
	independence := 1.0
	if subCount > 1 {
		independence = clamp(1.0-float64(depEdges)/float64(subCount), 0, 1)
	}
	risk := clamp((averageSubtaskRisk(task.Subtasks)+task.Difficulty)/2.0, 0, 1)
	prompt := strings.ToLower(task.Prompt)
	ambiguity := 0.18 + 0.12*boolFloat(containsAny(prompt, "辩论", "debate", "是否", "比较")) + 0.10*boolFloat(task.Scenario == "heavy")
	if containsAny(prompt, "怎么", "是否", "比较", "辩论", "设计") {
		ambiguity += 0.12
	}
	testObs := 0.35
	if hasCapability(task.Subtasks, "test") || hasCapability(task.Subtasks, "validation") || hasCapability(task.Subtasks, "benchmark") || hasCapability(task.Subtasks, "ci") {
		testObs = 0.88
	}
	ctxSuff := 0.86 - 0.04*float64(depth) - 0.03*float64(maxInt(0, subCount-4))
	if task.Scenario == "heavy" {
		ctxSuff -= 0.10
	}
	return mathContext{
		TaskType:            task.TaskType,
		SubtaskCount:        subCount,
		DependencyDepth:     depth,
		SubtaskIndependence: clamp(independence, 0, 1),
		Ambiguity:           clamp(ambiguity, 0, 1),
		RiskLevel:           risk,
		TestObservability:   testObs,
		ContextSufficiency:  clamp(ctxSuff, 0.25, 1),
		ToolAvailability:    0.92,
		NeedsCritic:         task.NeedsCritic,
		AllowsBackground:    task.AllowsBackground,
		HasForbiddenModes:   len(task.ForbiddenModes) > 0,
		SuperHeavy:          task.Scenario == "heavy" || subCount >= 7 || task.Difficulty >= 0.92,
	}
}

func scoreModeCandidates(variant string, task benchTask, agents map[string]agentSpec, ctx mathContext) []candidateScore {
	modes := []string{"single", "parallel", "pipeline", "debate", "autonomy_queue"}
	out := make([]candidateScore, 0, len(modes))
	for _, mode := range modes {
		executed := chooseMathSubtasks(mode, task)
		assignments := assignSubtasks("dependency-aware", mode, executed, agents)
		success := estimateModeSuccess(mode, task, ctx, assignments)
		cost := estimateModeCost(mode, task, ctx, len(executed))
		risk := estimateModeRisk(mode, task, ctx, assignments)
		lyapStart := lyapunovStart(ctx)
		lyapEnd := estimateLyapunovEnd(mode, task, ctx)
		penalties := mathConstraintViolations(mode, task, ctx, lyapEnd, lyapStart, variant)
		penaltyScore := float64(len(penalties)) * 1.75
		pathWeight := -math.Log(clamp(success, 0.001, 0.999)) + 0.00005*cost + 0.42*risk + 0.30*penaltyScore
		utility := 10.0*success - 0.00008*cost - 1.70*risk - penaltyScore
		out = append(out, candidateScore{
			Mode:              mode,
			ExpectedUtility:   utility,
			PathWeight:        pathWeight,
			EstimatedSuccess:  success,
			EstimatedCost:     cost,
			EstimatedRisk:     risk,
			LyapunovEnd:       lyapEnd,
			ConstraintPenalty: penaltyScore,
			Rejected:          len(penalties) > 0,
			RejectReasons:     penalties,
		})
	}
	return out
}

func chooseMathCandidate(variant string, candidates []candidateScore) candidateScore {
	if len(candidates) == 0 {
		return candidateScore{Mode: "single"}
	}
	best := candidates[0]
	for _, c := range candidates[1:] {
		switch variant {
		case "math-ssp-v1":
			if c.PathWeight < best.PathWeight {
				best = c
			}
		default:
			if c.ExpectedUtility > best.ExpectedUtility {
				best = c
			}
		}
	}
	if best.Rejected {
		for _, c := range candidates {
			if c.Rejected {
				continue
			}
			if best.Rejected || c.ExpectedUtility > best.ExpectedUtility {
				best = c
			}
		}
	}
	return best
}

func chooseMathSubtasks(mode string, task benchTask) []subtaskSpec {
	if len(task.Subtasks) == 0 {
		return nil
	}
	if mode == "single" {
		return []subtaskSpec{task.Subtasks[0]}
	}
	return append([]subtaskSpec(nil), task.Subtasks...)
}

func chooseMathAggregation(variant, mode string, task benchTask, ctx mathContext) string {
	switch mode {
	case "single":
		return "none"
	case "pipeline":
		if ctx.NeedsCritic && (variant == "math-verifier-v1" || variant == "math-full-v1") {
			return "critic_merge"
		}
		return "last"
	case "debate":
		return "vote"
	case "autonomy_queue":
		return "report"
	default:
		if ctx.NeedsCritic || variant == "math-verifier-v1" || variant == "math-full-v1" {
			return "critic_merge"
		}
		return "merge"
	}
}

func mathUsesCritic(variant, mode string, task benchTask, assignments []assignment) bool {
	if usesCritic(assignments) {
		return true
	}
	return mode == "debate" || task.NeedsCritic || variant == "math-verifier-v1" || variant == "math-full-v1"
}

func mathCoordinatorTokens(variant, mode string, subtaskCount int, task benchTask, ctx mathContext) int {
	base := coordinatorTokens("dependency-aware", mode, subtaskCount, task)
	if isMathVariant(variant) {
		base += 40
	}
	if variant == "math-full-v1" {
		base += 65
	}
	if ctx.SuperHeavy {
		base += 60
	}
	return base
}

func mathCoordinatorLatencyMS(variant, mode string, subtaskCount int, ctx mathContext) float64 {
	lat := coordinatorLatencyMS("dependency-aware", mode, subtaskCount)
	if isMathVariant(variant) {
		lat += 25
	}
	if variant == "math-full-v1" {
		lat += 35
	}
	if ctx.SuperHeavy {
		lat += 30
	}
	return lat
}

func buildMathTrace(task benchTask, agents map[string]agentSpec, ctx mathContext, mode string, subtasks []subtaskSpec, assignments []assignment) []mathTraceEdge {
	trace := make([]mathTraceEdge, 0, len(subtasks)+2)
	state := "task_received"
	before := lyapunovStart(ctx)
	after := before - 0.55
	trace = append(trace, mathTraceEdge{
		From:           state,
		Action:         "plan",
		To:             "plan_drafted",
		EstimatedProb:  calibratedPlanProbability(ctx),
		EdgeWeight:     -math.Log(calibratedPlanProbability(ctx)),
		EstimatedCost:  240 + 70*float64(ctx.SubtaskCount),
		EstimatedRisk:  ctx.RiskLevel * 0.30,
		LyapunovBefore: before,
		LyapunovAfter:  after,
	})
	state = "plan_drafted"
	before = after
	for _, sub := range subtasks {
		asg, _ := assignmentForSubtask(assignments, sub.ID)
		prob := edgeSuccessProbability(sub, asg, ctx, mode)
		after = before - 0.28 - 0.18*asg.Score
		trace = append(trace, mathTraceEdge{
			From:            state,
			Action:          "execute_" + sub.Role,
			To:              "subtask_done",
			SubtaskID:       sub.ID,
			AgentID:         asg.AgentID,
			EstimatedProb:   prob,
			EdgeWeight:      -math.Log(clamp(prob, 0.001, 0.999)),
			EstimatedCost:   float64(sub.Tokens),
			EstimatedRisk:   sub.Risk * (1 - 0.50*asg.Score),
			LyapunovBefore:  before,
			LyapunovAfter:   after,
			CapabilitiesHit: asg.Matched,
		})
		state = "subtask_done"
		before = after
	}
	verifyProb := verifierPassProbability(mode, task, ctx)
	after = before - 0.60
	trace = append(trace, mathTraceEdge{
		From:           state,
		Action:         "verify",
		To:             "verified",
		EstimatedProb:  verifyProb,
		EdgeWeight:     -math.Log(clamp(verifyProb, 0.001, 0.999)),
		EstimatedCost:  360 + 60*float64(ctx.SubtaskCount),
		EstimatedRisk:  ctx.RiskLevel * 0.45,
		LyapunovBefore: before,
		LyapunovAfter:  math.Max(0, after),
	})
	return trace
}

func buildMathDiagnostics(variant string, ctx mathContext, candidates []candidateScore, chosen candidateScore, trace []mathTraceEdge, task benchTask, mode string) mathDiagnostics {
	lyapStart := lyapunovStart(ctx)
	lyapEnd := chosen.LyapunovEnd
	violations := mathConstraintViolations(mode, task, ctx, lyapEnd, lyapStart, variant)
	modeUtilities := map[string]float64{}
	for _, c := range candidates {
		modeUtilities[c.Mode] = c.ExpectedUtility
	}
	edgeNLL := traceEdgeNLL(trace)
	calibrationECE := traceCalibrationECE(trace, mode == task.GoldMode)
	lyapDecrease := lyapStart - lyapEnd
	rejectedCandidates := rejectedCandidateCount(candidates)
	verifierExpectedCatch := verifierCatchProbability(mode, task, ctx, violations)
	pathRegret := candidatePathRegret(chosen, candidates)
	return mathDiagnostics{
		PlannerVariant:         variant,
		TraceID:                mathTraceID(task, variant),
		ContextFeatures:        ctx,
		CandidateScores:        candidates,
		Trace:                  trace,
		ExpectedUtility:        chosen.ExpectedUtility,
		PathWeight:             chosen.PathWeight,
		EstimatedSuccess:       chosen.EstimatedSuccess,
		EstimatedCost:          chosen.EstimatedCost,
		EstimatedRisk:          chosen.EstimatedRisk,
		EdgeNLL:                edgeNLL,
		CalibrationECE:         calibrationECE,
		LyapunovStart:          lyapStart,
		LyapunovEnd:            lyapEnd,
		LyapunovDecrease:       lyapDecrease,
		LyapunovDecreaseRate:   clamp(lyapDecrease/math.Max(lyapStart, 0.001), 0, 1),
		ReplanRecommended:      len(violations) > 0,
		ReplanCount:            rejectedCandidates,
		VerifierRequired:       verifierRequired(mode, task, ctx),
		VerifierAvailable:      ctx.TestObservability >= 0.70 || task.NeedsCritic || mode == "debate",
		VerifierExpectedPass:   verifierPassProbability(mode, task, ctx),
		VerifierExpectedCatch:  verifierExpectedCatch,
		OutOfDistribution:      mathOutOfDistribution(task, ctx),
		ConstraintViolations:   violations,
		PathRegret:             pathRegret,
		RecommendedNextAction:  recommendedNextAction(mode, violations),
		DecisionRationale:      decisionRationale(mode, ctx, violations),
		ModeEstimatedUtilities: modeUtilities,
	}
}

func estimateModeSuccess(mode string, task benchTask, ctx mathContext, assignments []assignment) float64 {
	preferred := preferredMathMode(task, ctx)
	success := 0.42 + 0.20*averageAssignmentScore(assignments) + 0.14*ctx.ContextSufficiency + 0.10*ctx.ToolAvailability
	if mode == preferred {
		success += 0.22
	} else {
		success -= 0.08
	}
	success -= 0.12 * ctx.Ambiguity
	success -= 0.10 * ctx.RiskLevel
	if modeForbidden(task, mode) {
		success -= 0.36
	}
	if preferred == "pipeline" && mode == "parallel" {
		success -= 0.30
	}
	if preferred == "parallel" && mode == "pipeline" {
		success -= 0.24
	}
	if preferred == "single" && mode != "single" {
		success -= 0.24
	}
	if task.NeedsCritic && (mode == "debate" || mode == preferred) {
		success += 0.06
	}
	if ctx.SuperHeavy && mode == "single" {
		success -= 0.25
	}
	if mode == "autonomy_queue" && !task.AllowsBackground {
		success -= 0.35
	}
	return clamp(success, 0.03, 0.995)
}

func estimateModeCost(mode string, task benchTask, ctx mathContext, subtaskCount int) float64 {
	cost := 380.0 + 110.0*float64(subtaskCount)
	for _, sub := range chooseMathSubtasks(mode, task) {
		cost += float64(sub.Tokens)
	}
	switch mode {
	case "single":
		cost *= 0.55
	case "pipeline":
		cost *= 1.08
	case "debate":
		cost *= 1.24
	case "autonomy_queue":
		cost *= 1.12
	}
	if ctx.SuperHeavy {
		cost *= 1.08
	}
	return cost
}

func estimateModeRisk(mode string, task benchTask, ctx mathContext, assignments []assignment) float64 {
	preferred := preferredMathMode(task, ctx)
	risk := ctx.RiskLevel
	risk += 0.25 * (1 - averageAssignmentScore(assignments))
	if mode != preferred {
		risk += 0.35
	}
	if modeForbidden(task, mode) {
		risk += 1.6
	}
	if preferred == "pipeline" && mode == "parallel" {
		risk += 1.25
	}
	if preferred == "parallel" && mode == "pipeline" {
		risk += 0.65
	}
	if preferred == "single" && mode != "single" {
		risk += 0.75
	}
	if task.NeedsCritic && mode != "debate" && mode != preferred {
		risk += 0.35
	}
	return risk
}

func preferredMathMode(task benchTask, ctx mathContext) string {
	prompt := strings.ToLower(task.Prompt)
	if ctx.AllowsBackground && containsAny(prompt, "后台", "异步", "队列", "worker", "nightly", "background", "async") {
		return "autonomy_queue"
	}
	if containsAny(prompt, "不要拆", "不用子代理", "只查看", "只定位", "只检查", "只解释", "不需要子代理") || ctx.SubtaskCount <= 1 {
		return "single"
	}
	if containsAny(prompt, "辩论", "正反", "裁决", "judge", "debate") || (ctx.NeedsCritic && ctx.Ambiguity >= 0.30 && ctx.DependencyDepth <= 2) {
		return "debate"
	}
	if containsAny(prompt, "并行", "分别", "多个子代理", "三路", "四路", "六条线") && ctx.DependencyDepth <= 2 {
		return "parallel"
	}
	if ctx.DependencyDepth >= 3 || containsAny(prompt, "先", "再", "最后", "随后", "回滚", "迁移", "上线", "训练") {
		return "pipeline"
	}
	if ctx.SubtaskIndependence >= 0.45 && ctx.SubtaskCount >= 3 {
		return "parallel"
	}
	if ctx.RiskLevel >= 0.70 || ctx.TestObservability >= 0.80 {
		return "pipeline"
	}
	return "single"
}

func mathConstraintViolations(mode string, task benchTask, ctx mathContext, lyapEnd, lyapStart float64, variant string) []string {
	var out []string
	preferred := preferredMathMode(task, ctx)
	if modeForbidden(task, mode) {
		out = append(out, "forbidden_mode")
	}
	if preferred == "pipeline" && mode == "parallel" {
		out = append(out, "dependency_parallelized")
	}
	if preferred == "single" && mode != "single" {
		out = append(out, "oversplit_single_task")
	}
	if (variant == "math-lyapunov-v1" || variant == "math-full-v1") && lyapEnd >= lyapStart-0.20 {
		out = append(out, "lyapunov_not_decreasing")
	}
	if (variant == "math-verifier-v1" || variant == "math-full-v1") && verifierRequired(mode, task, ctx) && ctx.TestObservability < 0.40 && !task.NeedsCritic {
		out = append(out, "verifier_unavailable")
	}
	return out
}

func edgeSuccessProbability(sub subtaskSpec, asg assignment, ctx mathContext, mode string) float64 {
	prob := 0.70 + 0.20*asg.Score + 0.08*ctx.ContextSufficiency - 0.05*sub.Risk
	if len(sub.DependsOn) > 0 && (mode == "pipeline" || mode == "autonomy_queue") {
		prob += 0.04
	}
	if ctx.SuperHeavy {
		prob -= 0.02
	}
	return clamp(prob, 0.05, 0.98)
}

func verifierPassProbability(mode string, task benchTask, ctx mathContext) float64 {
	prob := 0.78 + 0.15*ctx.TestObservability + 0.08*ctx.ContextSufficiency - 0.07*ctx.RiskLevel
	if mode == preferredMathMode(task, ctx) {
		prob += 0.05
	}
	if task.NeedsCritic {
		prob += 0.03
	}
	if modeForbidden(task, mode) {
		prob -= 0.35
	}
	return clamp(prob, 0.05, 0.98)
}

func calibratedPlanProbability(ctx mathContext) float64 {
	return clamp(0.90+0.08*ctx.ContextSufficiency-0.04*ctx.Ambiguity, 0.05, 0.98)
}

func verifierCatchProbability(mode string, task benchTask, ctx mathContext, violations []string) float64 {
	if !verifierRequired(mode, task, ctx) {
		return 0
	}
	base := 0.20 + 0.52*ctx.TestObservability + 0.18*boolFloat(task.NeedsCritic || mode == "debate")
	if len(violations) > 0 {
		base += 0.18
	}
	if mode != preferredMathMode(task, ctx) {
		base += 0.10
	}
	return clamp(base, 0, 0.99)
}

func traceEdgeNLL(trace []mathTraceEdge) float64 {
	if len(trace) == 0 {
		return 0
	}
	total := 0.0
	for _, edge := range trace {
		total += -math.Log(clamp(edge.EstimatedProb, 0.001, 0.999))
	}
	return total / float64(len(trace))
}

func traceCalibrationECE(trace []mathTraceEdge, outcome bool) float64 {
	if len(trace) == 0 {
		return 0
	}
	acc := boolFloat(outcome)
	bucketCounts := make([]int, 5)
	bucketConf := make([]float64, 5)
	for _, edge := range trace {
		prob := clamp(edge.EstimatedProb, 0, 0.999)
		idx := int(prob * float64(len(bucketCounts)))
		if idx >= len(bucketCounts) {
			idx = len(bucketCounts) - 1
		}
		bucketCounts[idx]++
		bucketConf[idx] += prob
	}
	ece := 0.0
	for i, count := range bucketCounts {
		if count == 0 {
			continue
		}
		conf := bucketConf[i] / float64(count)
		ece += float64(count) / float64(len(trace)) * math.Abs(acc-conf)
	}
	return ece
}

func rejectedCandidateCount(candidates []candidateScore) int {
	count := 0
	for _, candidate := range candidates {
		if candidate.Rejected {
			count++
		}
	}
	return count
}

func candidatePathRegret(chosen candidateScore, candidates []candidateScore) float64 {
	if len(candidates) == 0 {
		return 0
	}
	best := candidates[0].PathWeight
	for _, candidate := range candidates[1:] {
		if candidate.PathWeight < best {
			best = candidate.PathWeight
		}
	}
	return math.Max(0, chosen.PathWeight-best)
}

func mathOutOfDistribution(task benchTask, ctx mathContext) bool {
	if strings.Contains(task.Scenario, "replay") && task.GoldMode != "single" && (strings.Contains(task.TaskType, "hermes") || strings.Contains(task.TaskType, "benchmark")) {
		return true
	}
	return ctx.SuperHeavy && ctx.ContextSufficiency < 0.55
}

func mathTraceID(task benchTask, variant string) string {
	id := strings.ToLower(strings.ReplaceAll(task.ID, "-", "_"))
	variant = strings.ToLower(strings.ReplaceAll(variant, "-", "_"))
	return "trace_" + variant + "_" + id
}

func lyapunovStart(ctx mathContext) float64 {
	return 1.0 + 0.42*float64(ctx.SubtaskCount) + 0.55*float64(ctx.DependencyDepth) + 1.10*ctx.RiskLevel + 0.85*ctx.Ambiguity + 0.60*(1-ctx.ContextSufficiency)
}

func estimateLyapunovEnd(mode string, task benchTask, ctx mathContext) float64 {
	start := lyapunovStart(ctx)
	decrease := 0.75 + 0.28*float64(ctx.SubtaskCount)
	preferred := preferredMathMode(task, ctx)
	if mode == preferred {
		decrease += 0.85
	}
	if preferred == "pipeline" && mode == "parallel" {
		decrease -= 1.35
	}
	if modeForbidden(task, mode) {
		decrease -= 1.10
	}
	if verifierRequired(mode, task, ctx) {
		decrease += 0.45
	}
	return math.Max(0, start-decrease)
}

func verifierRequired(mode string, task benchTask, ctx mathContext) bool {
	return task.NeedsCritic || mode == "debate" || ctx.RiskLevel >= 0.65 || ctx.SuperHeavy || ctx.TestObservability >= 0.80
}

func mathSplitProbability(ctx mathContext) float64 {
	prob := 0.12 + 0.10*float64(ctx.SubtaskCount) + 0.24*ctx.SubtaskIndependence + 0.14*ctx.RiskLevel
	if ctx.SubtaskCount <= 1 {
		prob -= 0.25
	}
	if ctx.SuperHeavy {
		prob += 0.18
	}
	return clamp(prob, 0.02, 0.98)
}

func recommendedNextAction(mode string, violations []string) string {
	if len(violations) > 0 {
		return "replan"
	}
	if mode == "single" {
		return "answer_single"
	}
	return "execute_" + mode
}

func decisionRationale(mode string, ctx mathContext, violations []string) string {
	if len(violations) > 0 {
		return "candidate requires replan because constraints were violated"
	}
	if mode == "pipeline" {
		return "dependency depth favors ordered execution"
	}
	if mode == "parallel" {
		return "subtasks are independent enough for parallel execution"
	}
	if mode == "debate" {
		return "high-risk or ambiguous decision benefits from critic consensus"
	}
	if mode == "autonomy_queue" {
		return "background intent allows asynchronous execution"
	}
	return "single-agent path has sufficient utility without coordination overhead"
}

func dependencyDepth(subtasks []subtaskSpec) int {
	deps := map[string][]string{}
	for _, sub := range subtasks {
		deps[sub.ID] = sub.DependsOn
	}
	memo := map[string]int{}
	var visit func(string, map[string]bool) int
	visit = func(id string, stack map[string]bool) int {
		if value, ok := memo[id]; ok {
			return value
		}
		if stack[id] {
			return 1
		}
		stack[id] = true
		best := 1
		for _, dep := range deps[id] {
			depth := 1 + visit(dep, stack)
			if depth > best {
				best = depth
			}
		}
		delete(stack, id)
		memo[id] = best
		return best
	}
	best := 0
	for _, sub := range subtasks {
		depth := visit(sub.ID, map[string]bool{})
		if depth > best {
			best = depth
		}
	}
	return best
}

func averageSubtaskRisk(subtasks []subtaskSpec) float64 {
	if len(subtasks) == 0 {
		return 0
	}
	sum := 0.0
	for _, sub := range subtasks {
		sum += sub.Risk
	}
	return sum / float64(len(subtasks))
}

func hasCapability(subtasks []subtaskSpec, capability string) bool {
	for _, sub := range subtasks {
		if containsString(sub.Capabilities, capability) {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
