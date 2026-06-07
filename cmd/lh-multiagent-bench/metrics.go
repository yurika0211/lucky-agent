package main

import (
	"math"
	"sort"
	"strings"
)

type taskMetrics struct {
	SplitPredictionCorrect bool
	ModeCorrect            bool
	ForbiddenModeHit       bool
	SubtaskRecall          float64
	SubtaskPrecision       float64
	CapabilityRecall       float64
	CapabilityPrecision    float64
	RoleFit                float64
	DependencyViolations   int
	RouteRisk              float64
	SerialLatencyMS        float64
	CriticalPathMS         float64
	SingleAgentLatencyMS   float64
	Speedup                float64
	ParallelEfficiency     float64
	WorkTokens             int
	TotalTokens            int
	SingleAgentTokens      int
	CoordinationOverhead   float64
	AggregationQuality     float64
	ConsensusQuality       float64
	BackgroundCorrect      bool
	CriticRecall           float64
	SuccessProbability     float64
	MultiAgentScore        float64
	Clean                  bool
	Success                bool
	MissingCapabilities    []string
	CoveredCapabilities    []string
}

func evaluateTask(cfg benchConfig, task benchTask, result strategyResult, agents map[string]agentSpec) taskMetrics {
	goldSplit := task.GoldMode != "single"
	predictedSplit := result.Mode != "single"
	modeCorrect := result.Mode == task.GoldMode
	forbidden := modeForbidden(task, result.Mode)
	executedIDs := subtaskIDSet(result.ExecutedSubtasks)
	goldIDs := subtaskIDSet(task.Subtasks)
	subtaskHits := countSetIntersection(executedIDs, goldIDs)
	subtaskRecall := ratioOrPerfect(subtaskHits, len(task.Subtasks))
	subtaskPrecision := ratioOrPerfect(subtaskHits, len(result.ExecutedSubtasks))

	capRecall, capPrecision, covered, missing := capabilityMetrics(task, result, agents)
	roleFit := averageAssignmentScore(result.Assignments)
	depViolations := dependencyViolations(result, task)
	routeRisk := routeRiskScore(task, result, agents, forbidden, depViolations)
	serialLatency, criticalLatency, singleLatency := latencyStats(task, result, agents)
	speedup := ratioOrPerfectFloat(singleLatency, criticalLatency)
	parallelEfficiency := 1.0
	if result.Mode != "single" && len(result.ExecutedSubtasks) > 1 {
		parallelEfficiency = clamp(speedup/float64(len(result.ExecutedSubtasks)), 0, 1)
	}
	workTokens, totalTokens, singleTokens := tokenStats(task, result, agents)
	coordOverhead := coordinationOverhead(result, totalTokens, criticalLatency)
	aggregationQuality := aggregationQuality(task, result, subtaskRecall, depViolations)
	consensusQuality := consensusQuality(task, result)
	backgroundCorrect := backgroundCorrect(task, result)
	criticRecall := criticRecall(task, result)

	metrics := taskMetrics{
		SplitPredictionCorrect: goldSplit == predictedSplit,
		ModeCorrect:            modeCorrect,
		ForbiddenModeHit:       forbidden,
		SubtaskRecall:          subtaskRecall,
		SubtaskPrecision:       subtaskPrecision,
		CapabilityRecall:       capRecall,
		CapabilityPrecision:    capPrecision,
		RoleFit:                roleFit,
		DependencyViolations:   depViolations,
		RouteRisk:              routeRisk,
		SerialLatencyMS:        serialLatency,
		CriticalPathMS:         criticalLatency,
		SingleAgentLatencyMS:   singleLatency,
		Speedup:                speedup,
		ParallelEfficiency:     parallelEfficiency,
		WorkTokens:             workTokens,
		TotalTokens:            totalTokens,
		SingleAgentTokens:      singleTokens,
		CoordinationOverhead:   coordOverhead,
		AggregationQuality:     aggregationQuality,
		ConsensusQuality:       consensusQuality,
		BackgroundCorrect:      backgroundCorrect,
		CriticRecall:           criticRecall,
		MissingCapabilities:    missing,
		CoveredCapabilities:    covered,
	}
	metrics.SuccessProbability = successProbability(metrics)
	metrics.MultiAgentScore = multiAgentScore(metrics)
	metrics.Success = metrics.SuccessProbability >= cfg.SuccessThreshold
	metrics.Clean = !forbidden && depViolations == 0 && routeRisk <= task.RiskBudget && coordOverhead <= cfg.MaxCoordOverhead
	return metrics
}

func modeForbidden(task benchTask, mode string) bool {
	for _, forbidden := range task.ForbiddenModes {
		if normalizeMode(forbidden) == mode {
			return true
		}
	}
	if mode == "autonomy_queue" && !task.AllowsBackground && task.GoldMode != "autonomy_queue" {
		return true
	}
	return false
}

func subtaskIDSet(subtasks []subtaskSpec) map[string]struct{} {
	out := make(map[string]struct{}, len(subtasks))
	for _, sub := range subtasks {
		if sub.ID != "" {
			out[sub.ID] = struct{}{}
		}
	}
	return out
}

func capabilityMetrics(task benchTask, result strategyResult, agents map[string]agentSpec) (float64, float64, []string, []string) {
	required := stringSet(task.RequiredCapabilities)
	predicted := map[string]struct{}{}
	for _, asg := range result.Assignments {
		if agent, ok := agents[asg.AgentID]; ok {
			for _, cap := range agent.Capabilities {
				predicted[cap] = struct{}{}
			}
		}
	}
	hits := countSetIntersection(required, predicted)
	covered := sortedIntersection(required, predicted)
	missing := sortedDifference(required, predicted)
	return ratioOrPerfect(hits, len(required)), ratioOrPerfect(hits, len(predicted)), covered, missing
}

func averageAssignmentScore(assignments []assignment) float64 {
	if len(assignments) == 0 {
		return 1
	}
	sum := 0.0
	for _, asg := range assignments {
		sum += asg.Score
	}
	return sum / float64(len(assignments))
}

func dependencyViolations(result strategyResult, task benchTask) int {
	if len(result.ExecutedSubtasks) == 0 {
		return 0
	}
	executed := subtaskIDSet(result.ExecutedSubtasks)
	seen := map[string]struct{}{}
	violations := 0
	for _, sub := range result.ExecutedSubtasks {
		for _, dep := range sub.DependsOn {
			if _, ok := executed[dep]; !ok {
				violations++
				continue
			}
			if result.Mode == "parallel" && !result.DependencyAware {
				violations++
				continue
			}
			if _, ok := seen[dep]; !ok && (result.Mode == "pipeline" || result.Mode == "autonomy_queue") {
				violations++
			}
		}
		seen[sub.ID] = struct{}{}
	}
	if task.GoldMode == "pipeline" && result.Mode == "parallel" {
		violations++
	}
	return violations
}

func routeRiskScore(task benchTask, result strategyResult, agents map[string]agentSpec, forbidden bool, depViolations int) float64 {
	risk := 0.0
	if forbidden {
		risk += 2.5
	}
	if result.Mode != task.GoldMode {
		risk += 1.3
	}
	if task.GoldMode == "single" && result.Mode != "single" {
		risk += 0.8
	}
	for _, sub := range result.ExecutedSubtasks {
		asg, ok := assignmentForSubtask(result.Assignments, sub.ID)
		if !ok {
			risk += sub.Risk
			continue
		}
		score := asg.Score
		if agent, ok := agents[asg.AgentID]; ok {
			risk += sub.Risk * (1 - score) * (1 + agent.RiskBias)
		} else {
			risk += sub.Risk
		}
	}
	risk += 1.15 * float64(depViolations)
	if result.Mode == "debate" && !result.CriticUsed {
		risk += 0.6
	}
	if result.Mode == "autonomy_queue" && !result.BackgroundQueued {
		risk += 0.5
	}
	return risk
}

func latencyStats(task benchTask, result strategyResult, agents map[string]agentSpec) (float64, float64, float64) {
	if len(task.Subtasks) == 0 {
		return 0, 0, 0
	}
	generalist := agents["generalist"]
	single := 40.0
	for _, sub := range task.Subtasks {
		single += sub.WorkMS*1.12 + generalist.LatencyMS*0.20
	}
	if len(result.ExecutedSubtasks) == 0 {
		return 0, result.CoordinatorLatency, single
	}
	durations := make(map[string]float64, len(result.ExecutedSubtasks))
	serial := result.CoordinatorLatency
	for _, sub := range result.ExecutedSubtasks {
		d := assignedDurationMS(sub, result, agents)
		durations[sub.ID] = d
		serial += d
	}
	critical := result.CoordinatorLatency
	switch result.Mode {
	case "single":
		critical += firstDuration(durations)
	case "parallel":
		parallelMax := 0.0
		serialTail := 0.0
		for _, sub := range result.ExecutedSubtasks {
			d := durations[sub.ID]
			if isAggregatorSubtask(sub) {
				serialTail += d
				continue
			}
			if d > parallelMax {
				parallelMax = d
			}
		}
		critical += parallelMax + serialTail
	case "debate":
		critical += (serial - result.CoordinatorLatency) * 1.6
	case "pipeline", "autonomy_queue":
		critical = serial
	default:
		critical = serial
	}
	if result.Mode == "autonomy_queue" && result.BackgroundQueued {
		critical += 250
	}
	return serial, critical, single
}

func assignedDurationMS(sub subtaskSpec, result strategyResult, agents map[string]agentSpec) float64 {
	asg, ok := assignmentForSubtask(result.Assignments, sub.ID)
	if !ok {
		return sub.WorkMS * 1.25
	}
	agent, ok := agents[asg.AgentID]
	if !ok {
		agent = agents["generalist"]
	}
	multiplier := 1.15 - 0.25*agent.Quality - 0.15*asg.Score
	if multiplier < 0.62 {
		multiplier = 0.62
	}
	return sub.WorkMS*multiplier + agent.LatencyMS*0.20
}

func firstDuration(durations map[string]float64) float64 {
	for _, d := range durations {
		return d
	}
	return 0
}

func isAggregatorSubtask(sub subtaskSpec) bool {
	if containsAny(sub.Role, "integrator", "critic") {
		return true
	}
	return containsAny(strings.Join(sub.Capabilities, " "), "aggregation", "critic", "consensus")
}

func tokenStats(task benchTask, result strategyResult, agents map[string]agentSpec) (int, int, int) {
	work := 0
	for _, sub := range result.ExecutedSubtasks {
		asg, ok := assignmentForSubtask(result.Assignments, sub.ID)
		agentBias := 220
		if ok {
			if agent, exists := agents[asg.AgentID]; exists {
				agentBias = agent.TokenBias
			}
		}
		work += sub.Tokens + agentBias
	}
	total := work + result.CoordinatorTokens
	single := result.CoordinatorTokens/3 + 260
	for _, sub := range task.Subtasks {
		single += sub.Tokens + agents["generalist"].TokenBias
	}
	return work, total, single
}

func coordinationOverhead(result strategyResult, totalTokens int, criticalPath float64) float64 {
	tokenPart := ratioOrZero(result.CoordinatorTokens, totalTokens)
	latPart := 0.0
	if criticalPath > 0 {
		latPart = result.CoordinatorLatency / criticalPath
	}
	return clamp(0.55*tokenPart+0.45*latPart, 0, 1)
}

func aggregationQuality(task benchTask, result strategyResult, subtaskRecall float64, depViolations int) float64 {
	base := 0.60
	switch result.Aggregation {
	case "none":
		if task.GoldMode == "single" {
			base = 0.94
		} else {
			base = 0.45
		}
	case "concat":
		base = 0.58
	case "merge":
		base = 0.78
	case "critic_merge":
		base = 0.88
	case "vote":
		base = 0.84
	case "last":
		base = 0.80
	case "report":
		base = 0.76
	}
	base *= 0.55 + 0.45*subtaskRecall
	base -= 0.08 * float64(depViolations)
	return clamp(base, 0, 1)
}

func consensusQuality(task benchTask, result strategyResult) float64 {
	if task.GoldMode != "debate" && !task.NeedsCritic {
		return 1
	}
	if result.Mode == "debate" && result.CriticUsed {
		return 0.88
	}
	if result.Aggregation == "critic_merge" || result.CriticUsed {
		return 0.82
	}
	return 0.55
}

func backgroundCorrect(task benchTask, result strategyResult) bool {
	if task.GoldMode == "autonomy_queue" || task.AllowsBackground {
		return result.BackgroundQueued
	}
	return !result.BackgroundQueued
}

func criticRecall(task benchTask, result strategyResult) float64 {
	if !task.NeedsCritic && task.GoldMode != "debate" {
		return 1
	}
	if result.CriticUsed || result.Aggregation == "critic_merge" || result.Aggregation == "vote" {
		return 1
	}
	return 0
}

func successProbability(m taskMetrics) float64 {
	score := -0.55
	score += 0.70 * boolFloat(m.SplitPredictionCorrect)
	score += 0.92 * boolFloat(m.ModeCorrect)
	score += 1.15 * m.SubtaskRecall
	score += 0.80 * m.CapabilityRecall
	score += 0.35 * m.CapabilityPrecision
	score += 0.42 * m.RoleFit
	score += 0.52 * m.AggregationQuality
	score += 0.26 * m.ConsensusQuality
	score += 0.22 * clamp(m.Speedup/2.0, 0, 1)
	score += 0.28 * m.CriticRecall
	score += 0.22 * boolFloat(m.BackgroundCorrect)
	score -= 0.78 * m.CoordinationOverhead
	score -= 0.42 * m.RouteRisk
	score -= 0.72 * float64(m.DependencyViolations)
	score -= 0.95 * boolFloat(m.ForbiddenModeHit)
	return 1 / (1 + math.Exp(-score))
}

func multiAgentScore(m taskMetrics) float64 {
	score := 0.0
	score += 0.15 * m.SuccessProbability
	score += 0.10 * boolFloat(m.SplitPredictionCorrect)
	score += 0.12 * boolFloat(m.ModeCorrect)
	score += 0.12 * m.SubtaskRecall
	score += 0.14 * m.CapabilityRecall
	score += 0.07 * m.CapabilityPrecision
	score += 0.09 * m.RoleFit
	score += 0.09 * m.AggregationQuality
	score += 0.06 * m.ParallelEfficiency
	score += 0.05 * m.CriticRecall
	score += 0.04 * boolFloat(m.BackgroundCorrect)
	score -= 0.08 * clamp(m.RouteRisk/4.0, 0, 1)
	score -= 0.06 * m.CoordinationOverhead
	score -= 0.04 * clamp(float64(m.DependencyViolations)/3.0, 0, 1)
	return clamp(score, 0, 1)
}

func assignmentForSubtask(assignments []assignment, subtaskID string) (assignment, bool) {
	for _, asg := range assignments {
		if asg.SubtaskID == subtaskID {
			return asg, true
		}
	}
	return assignment{}, false
}

func capabilityScore(required, caps []string) float64 {
	if len(required) == 0 {
		return 1
	}
	return ratioOrZero(countIntersection(required, caps), len(uniqueStrings(required)))
}

func intersectionStrings(a, b []string) []string {
	bs := stringSet(b)
	var out []string
	seen := map[string]struct{}{}
	for _, value := range a {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := bs[value]; !ok {
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

func countIntersection(a, b []string) int {
	return countSetIntersection(stringSet(a), stringSet(b))
}

func countSetIntersection(a, b map[string]struct{}) int {
	count := 0
	for value := range a {
		if _, ok := b[value]; ok {
			count++
		}
	}
	return count
}

func sortedIntersection(a, b map[string]struct{}) []string {
	var out []string
	for value := range a {
		if _, ok := b[value]; ok {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func sortedDifference(a, b map[string]struct{}) []string {
	var out []string
	for value := range a {
		if _, ok := b[value]; !ok {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
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

func sortedSetKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, value := range values {
		if strings.TrimSpace(value) == want {
			return true
		}
	}
	return false
}

func containsAny(text string, needles ...string) bool {
	text = strings.ToLower(text)
	for _, needle := range needles {
		needle = strings.ToLower(strings.TrimSpace(needle))
		if needle != "" && strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func ratioOrPerfect(num, den int) float64 {
	if den == 0 {
		return 1
	}
	return float64(num) / float64(den)
}

func ratioOrPerfectFloat(num, den float64) float64 {
	if den == 0 {
		return 1
	}
	return num / den
}

func ratioOrZero(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func boolFloat(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
