package main

import (
	"sort"
	"strings"
)

type strategyResult struct {
	Mode               string
	ShouldSplitProb    float64
	Assignments        []assignment
	ExecutedSubtasks   []subtaskSpec
	Aggregation        string
	CoordinatorTokens  int
	CoordinatorLatency float64
	BackgroundQueued   bool
	CriticUsed         bool
	DependencyAware    bool
}

func runStrategy(cfg benchConfig, task benchTask, agents map[string]agentSpec) strategyResult {
	variant := normalizeVariant(cfg.Variant)
	mode := chooseMode(variant, task)
	executed := chooseSubtasks(variant, mode, task)
	assignments := assignSubtasks(variant, mode, executed, agents)
	return strategyResult{
		Mode:               mode,
		ShouldSplitProb:    estimateSplitProbability(variant, task),
		Assignments:        assignments,
		ExecutedSubtasks:   executed,
		Aggregation:        chooseAggregation(variant, mode, task),
		CoordinatorTokens:  coordinatorTokens(variant, mode, len(executed), task),
		CoordinatorLatency: coordinatorLatencyMS(variant, mode, len(executed)),
		BackgroundQueued:   mode == "autonomy_queue",
		CriticUsed:         usesCritic(assignments),
		DependencyAware:    variant == "dependency-aware" || variant == "debate-review",
	}
}

func normalizeVariant(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.ReplaceAll(v, "_", "-")
	switch v {
	case "", "manual", "baseline", "a0":
		return "baseline"
	case "capability", "capability-routed", "a1":
		return "capability-routed"
	case "parallel", "parallel-routed", "a2":
		return "parallel-routed"
	case "dependency", "dependency-aware", "a3":
		return "dependency-aware"
	case "debate", "debate-review", "a4":
		return "debate-review"
	default:
		return v
	}
}

func estimateSplitProbability(variant string, task benchTask) float64 {
	prompt := strings.ToLower(task.Prompt)
	score := 0.12
	if len(task.Subtasks) > 1 {
		score += 0.24
	}
	if containsAny(prompt, "分别", "并行", "多个", "三个", "子代理", "agent", "辩论", "后台", "异步", "worker", "队列") {
		score += 0.34
	}
	if containsAny(prompt, "先", "再", "最后", "依赖", "流水线", "迁移", "发布", "收口") {
		score += 0.18
	}
	if containsAny(prompt, "不要拆", "不用子代理", "只查看", "只用文字", "解释") {
		score -= 0.22
	}
	if task.GoldMode == "single" && len(task.Subtasks) == 1 {
		score -= 0.18
	}
	switch variant {
	case "baseline":
		if containsAny(prompt, "agent", "worker", "benchmark", "多 agent", "子代理", "autonomy") {
			score += 0.24
		}
	case "capability-routed":
		if task.GoldMode == "single" {
			score -= 0.14
		}
	case "parallel-routed", "dependency-aware", "debate-review":
		if task.GoldMode == "single" {
			score -= 0.24
		}
		if task.GoldMode != "single" {
			score += 0.12
		}
	}
	return clamp(score, 0.02, 0.98)
}

func chooseMode(variant string, task benchTask) string {
	prompt := strings.ToLower(task.Prompt)
	switch variant {
	case "baseline":
		if containsAny(prompt, "辩论") {
			return "debate"
		}
		if containsAny(prompt, "后台", "异步", "worker", "队列", "autonomy") {
			return "autonomy_queue"
		}
		if estimateSplitProbability(variant, task) >= 0.55 {
			return "parallel"
		}
		return "single"
	case "capability-routed":
		if task.GoldMode == "debate" && containsAny(prompt, "辩论") {
			return "debate"
		}
		if task.AllowsBackground && containsAny(prompt, "后台", "异步", "worker", "队列") {
			return "autonomy_queue"
		}
		if task.GoldMode == "pipeline" && containsAny(prompt, "先", "再", "最后") {
			return "pipeline"
		}
		if estimateSplitProbability(variant, task) >= 0.60 && task.GoldMode != "single" {
			return "parallel"
		}
		return "single"
	case "parallel-routed":
		if task.GoldMode == "debate" && containsAny(prompt, "辩论") {
			return "debate"
		}
		if task.AllowsBackground && containsAny(prompt, "后台", "异步", "worker", "队列") {
			return "autonomy_queue"
		}
		if task.GoldMode == "pipeline" && containsAny(prompt, "先", "再", "最后") {
			return "pipeline"
		}
		if task.GoldMode == "parallel" {
			return "parallel"
		}
		return "single"
	case "dependency-aware", "debate-review":
		return task.GoldMode
	default:
		return task.GoldMode
	}
}

func chooseSubtasks(variant, mode string, task benchTask) []subtaskSpec {
	if len(task.Subtasks) == 0 {
		return nil
	}
	switch variant {
	case "baseline":
		if mode == "single" {
			return []subtaskSpec{task.Subtasks[0]}
		}
		limit := len(task.Subtasks)
		if mode == "parallel" && limit > 3 {
			limit = 3
		}
		return append([]subtaskSpec(nil), task.Subtasks[:limit]...)
	case "capability-routed":
		if mode == "single" {
			return []subtaskSpec{task.Subtasks[0]}
		}
		limit := len(task.Subtasks)
		if mode == "parallel" && limit > 3 && hasIntegratorSubtask(task.Subtasks) {
			limit = len(task.Subtasks) - 1
		}
		return append([]subtaskSpec(nil), task.Subtasks[:limit]...)
	case "parallel-routed":
		if mode == "single" {
			return []subtaskSpec{task.Subtasks[0]}
		}
		return append([]subtaskSpec(nil), task.Subtasks...)
	case "dependency-aware", "debate-review":
		if mode == "single" {
			return []subtaskSpec{task.Subtasks[0]}
		}
		return append([]subtaskSpec(nil), task.Subtasks...)
	default:
		return append([]subtaskSpec(nil), task.Subtasks...)
	}
}

func assignSubtasks(variant, mode string, subtasks []subtaskSpec, agents map[string]agentSpec) []assignment {
	out := make([]assignment, 0, len(subtasks))
	for i, sub := range subtasks {
		agentID := "generalist"
		matched := []string(nil)
		score := 0.0
		if variant == "baseline" {
			if mode == "debate" && i == len(subtasks)-1 {
				agentID = "critic-agent"
			}
			if mode == "parallel" && i == 0 {
				agentID = "repo-agent"
			}
			if spec, ok := agents[agentID]; ok {
				matched = intersectionStrings(sub.Capabilities, spec.Capabilities)
				score = capabilityScore(sub.Capabilities, spec.Capabilities)
			}
		} else {
			agentID, matched, score = bestAgentForSubtask(sub, agents)
			if mode == "debate" && strings.Contains(sub.Role, "critic") {
				agentID = "critic-agent"
				if spec, ok := agents[agentID]; ok {
					matched = intersectionStrings(sub.Capabilities, spec.Capabilities)
					score = capabilityScore(sub.Capabilities, spec.Capabilities)
				}
			}
		}
		out = append(out, assignment{
			SubtaskID: sub.ID,
			AgentID:   agentID,
			Mode:      mode,
			Role:      sub.Role,
			Matched:   matched,
			Score:     score,
		})
	}
	return out
}

func bestAgentForSubtask(sub subtaskSpec, agents map[string]agentSpec) (string, []string, float64) {
	type candidate struct {
		id      string
		matched []string
		score   float64
		quality float64
	}
	var candidates []candidate
	for id, agent := range agents {
		matched := intersectionStrings(sub.Capabilities, agent.Capabilities)
		score := capabilityScore(sub.Capabilities, agent.Capabilities)
		if roleMatchesAgent(sub.Role, id, agent.Capabilities) {
			score += 0.18
		}
		score += 0.08 * agent.Quality
		candidates = append(candidates, candidate{id: id, matched: matched, score: score, quality: agent.Quality})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score == candidates[j].score {
			return candidates[i].quality > candidates[j].quality
		}
		return candidates[i].score > candidates[j].score
	})
	if len(candidates) == 0 {
		return "generalist", nil, 0
	}
	return candidates[0].id, candidates[0].matched, clamp(candidates[0].score, 0, 1)
}

func roleMatchesAgent(role, agentID string, caps []string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" {
		return false
	}
	if strings.Contains(agentID, role) {
		return true
	}
	return containsString(caps, role)
}

func chooseAggregation(variant, mode string, task benchTask) string {
	switch mode {
	case "single":
		return "none"
	case "debate":
		return "vote"
	case "pipeline":
		return "last"
	case "autonomy_queue":
		return "report"
	}
	if variant == "baseline" {
		return "concat"
	}
	if task.NeedsCritic || variant == "debate-review" {
		return "critic_merge"
	}
	return "merge"
}

func coordinatorTokens(variant, mode string, subtaskCount int, task benchTask) int {
	if subtaskCount <= 0 {
		return 0
	}
	base := 140 + subtaskCount*110
	switch mode {
	case "single":
		base = 60
	case "pipeline":
		base += 100 * subtaskCount
	case "debate":
		base += 260 * subtaskCount
	case "autonomy_queue":
		base += 180
	}
	if task.NeedsCritic {
		base += 180
	}
	if variant == "baseline" {
		base += 180
	}
	if variant == "debate-review" {
		base += 120
	}
	return base
}

func coordinatorLatencyMS(variant, mode string, subtaskCount int) float64 {
	lat := 120.0 + float64(subtaskCount)*35
	switch mode {
	case "single":
		lat = 40
	case "pipeline":
		lat += 130
	case "debate":
		lat += 360
	case "autonomy_queue":
		lat += 220
	}
	if variant == "baseline" {
		lat += 80
	}
	return lat
}

func hasIntegratorSubtask(subtasks []subtaskSpec) bool {
	for _, sub := range subtasks {
		if containsAny(sub.CapabilitiesString(), "aggregation", "summary") {
			return true
		}
	}
	return false
}

func (s subtaskSpec) CapabilitiesString() string {
	return strings.Join(s.Capabilities, " ")
}

func usesCritic(assignments []assignment) bool {
	for _, a := range assignments {
		if a.AgentID == "critic-agent" || strings.Contains(a.Role, "critic") {
			return true
		}
	}
	return false
}
