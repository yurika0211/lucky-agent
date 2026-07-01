package collab

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

const plannerVersion = "dijkstra-markov-mdp-v1"

// PlanRequest describes the scheduling problem the runtime planner solves.
type PlanRequest struct {
	Description  string
	Input        string
	AgentIDs     []string
	Agents       []*AgentProfile
	Timeout      time.Duration
	AllowedModes []CollabMode
}

// PlanResult is the auditable planning trace for a delegated task.
type PlanResult struct {
	Version       string           `json:"version"`
	Mode          CollabMode       `json:"mode"`
	Path          []string         `json:"path"`
	TotalWeight   float64          `json:"total_weight"`
	Candidates    []CandidateScore `json:"candidates"`
	Trace         []PlanEdge       `json:"trace"`
	DecisionBasis string           `json:"decision_basis"`
	MDP           MDPDecision      `json:"mdp"`
}

// CandidateScore captures the Markov-derived score used by Dijkstra.
type CandidateScore struct {
	Mode            CollabMode `json:"mode"`
	SuccessProb     float64    `json:"success_prob"`
	FailureProb     float64    `json:"failure_prob"`
	BlockedProb     float64    `json:"blocked_prob"`
	EstimatedCost   float64    `json:"estimated_cost"`
	EstimatedRisk   float64    `json:"estimated_risk"`
	MDPQValue       float64    `json:"mdp_q_value"`
	MDPAdjustment   float64    `json:"mdp_adjustment"`
	DecisionWeight  float64    `json:"decision_weight"`
	ObservedSamples int        `json:"observed_samples"`
}

// PlanEdge is a weighted transition in the task graph.
type PlanEdge struct {
	From   string     `json:"from"`
	To     string     `json:"to"`
	Action CollabMode `json:"action,omitempty"`
	Weight float64    `json:"weight"`
}

// TransitionProbability is the Markov transition estimate for one mode.
type TransitionProbability struct {
	Success float64
	Failure float64
	Blocked float64
	Samples int
}

// Planner chooses a collaboration mode using a task graph and Dijkstra's
// shortest-path algorithm. Edge weights are derived from Markov transition
// estimates plus cost/risk heuristics.
type Planner struct {
	model *AdaptiveMarkovModel
	mdp   *MDPModel
}

// NewPlanner creates a planner. Passing nil uses a fresh adaptive Markov model.
func NewPlanner(model *AdaptiveMarkovModel) *Planner {
	if model == nil {
		model = NewAdaptiveMarkovModel()
	}
	return &Planner{model: model, mdp: NewMDPModel()}
}

// SetMDPModel replaces the online MDP model. Passing nil creates a fresh one.
func (p *Planner) SetMDPModel(model *MDPModel) {
	if p == nil {
		return
	}
	if model == nil {
		model = NewMDPModel()
	}
	p.mdp = model
}

// MDPModel returns the planner's online MDP model.
func (p *Planner) MDPModel() *MDPModel {
	if p == nil {
		return nil
	}
	return p.mdp
}

// SaveMDP persists the current MDP state as JSON.
func (p *Planner) SaveMDP(path string) error {
	if p == nil || p.mdp == nil {
		return fmt.Errorf("mdp model is not configured")
	}
	return p.mdp.SaveJSON(path)
}

// LoadMDP restores the planner's MDP state from a JSON snapshot.
func (p *Planner) LoadMDP(path string) error {
	if p == nil {
		return fmt.Errorf("planner is nil")
	}
	model, err := LoadMDPModelJSON(path)
	if err != nil {
		return err
	}
	p.mdp = model
	return nil
}

// Plan selects the minimum-weight executable collaboration path.
func (p *Planner) Plan(req PlanRequest) PlanResult {
	if p == nil {
		p = NewPlanner(nil)
	}
	modes := normalizeAllowedModes(req)
	if len(modes) == 0 {
		modes = []CollabMode{ModePipeline}
	}

	candidates := make([]CandidateScore, 0, len(modes))
	edges := make([]PlanEdge, 0, len(modes)*2)
	mdpDecision := MDPDecision{}
	if p.mdp != nil {
		mdpDecision = p.mdp.Decision(req, modes)
	}
	for _, mode := range modes {
		prob := p.model.Estimate(mode, req)
		cost := estimatePlannerCost(mode, req)
		risk := estimatePlannerRisk(mode, req)
		adjustment := mdpWeightAdjustment(mdpDecision, mode)
		weight := decisionWeight(prob, cost, risk) + adjustment
		candidates = append(candidates, CandidateScore{
			Mode:            mode,
			SuccessProb:     prob.Success,
			FailureProb:     prob.Failure,
			BlockedProb:     prob.Blocked,
			EstimatedCost:   cost,
			EstimatedRisk:   risk,
			MDPQValue:       mdpDecision.QValues[mode],
			MDPAdjustment:   adjustment,
			DecisionWeight:  weight,
			ObservedSamples: prob.Samples,
		})
		modeNode := "mode:" + string(mode)
		edges = append(edges,
			PlanEdge{From: "start", To: modeNode, Action: mode, Weight: 0.01},
			PlanEdge{From: modeNode, To: "end", Action: mode, Weight: weight},
		)
	}

	path, trace, total := shortestPath(edges, "start", "end")
	chosen := modeFromPath(path)
	if chosen == "" {
		chosen = candidates[0].Mode
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].DecisionWeight == candidates[j].DecisionWeight {
			return candidates[i].Mode < candidates[j].Mode
		}
		return candidates[i].DecisionWeight < candidates[j].DecisionWeight
	})

	return PlanResult{
		Version:       plannerVersion,
		Mode:          chosen,
		Path:          path,
		TotalWeight:   total,
		Candidates:    candidates,
		Trace:         trace,
		DecisionBasis: decisionBasis(req),
		MDP:           mdpDecision,
	}
}

// Observe feeds runtime outcomes back into the Markov transition model.
func (p *Planner) Observe(mode CollabMode, state TaskState) {
	p.ObserveOutcome(mode, stateToOutcome(state))
}

// ObserveOutcome feeds a normalized outcome back into the Markov model.
func (p *Planner) ObserveOutcome(mode CollabMode, outcome string) {
	if p == nil || p.model == nil || !isExecutableMode(mode) {
		return
	}
	p.model.Observe(mode, outcome)
}

// ObserveExecution feeds a stateful execution result into both the Markov
// transition estimate and the MDP Q table.
func (p *Planner) ObserveExecution(req PlanRequest, mode CollabMode, outcome string, duration time.Duration) {
	if p == nil || !isExecutableMode(mode) {
		return
	}
	if p.model != nil {
		p.model.Observe(mode, outcome)
	}
	if p.mdp != nil {
		p.mdp.Observe(MDPObservation{
			Request:  req,
			Action:   mode,
			Outcome:  outcome,
			Duration: duration,
		})
	}
}

// AdaptiveMarkovModel stores observed outcomes per mode and combines them with
// heuristic priors when there is little history.
type AdaptiveMarkovModel struct {
	mu     sync.RWMutex
	counts map[CollabMode]outcomeCounts
}

type outcomeCounts struct {
	success int
	failure int
	blocked int
}

// NewAdaptiveMarkovModel creates an empty Markov transition model.
func NewAdaptiveMarkovModel() *AdaptiveMarkovModel {
	return &AdaptiveMarkovModel{counts: make(map[CollabMode]outcomeCounts)}
}

// Observe records one transition outcome.
func (m *AdaptiveMarkovModel) Observe(mode CollabMode, outcome string) {
	if m == nil || !isExecutableMode(mode) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.counts[mode]
	switch outcome {
	case "success":
		c.success++
	case "blocked":
		c.blocked++
	default:
		c.failure++
	}
	m.counts[mode] = c
}

// Estimate returns P(success/failure/blocked | mode, task context).
func (m *AdaptiveMarkovModel) Estimate(mode CollabMode, req PlanRequest) TransitionProbability {
	prior := heuristicTransition(mode, req)
	if m == nil {
		return prior
	}
	m.mu.RLock()
	c := m.counts[mode]
	m.mu.RUnlock()
	total := c.success + c.failure + c.blocked
	if total == 0 {
		return prior
	}
	priorWeight := 8.0
	denom := priorWeight + float64(total)
	return normalizeTransition(TransitionProbability{
		Success: (prior.Success*priorWeight + float64(c.success)) / denom,
		Failure: (prior.Failure*priorWeight + float64(c.failure)) / denom,
		Blocked: (prior.Blocked*priorWeight + float64(c.blocked)) / denom,
		Samples: total,
	})
}

func heuristicTransition(mode CollabMode, req PlanRequest) TransitionProbability {
	features := plannerFeatures(req)
	success := 0.68
	blocked := 0.06 + 0.05*features.risk
	switch mode {
	case ModeParallel:
		success = 0.76 + 0.08*features.independent - 0.24*features.sequential - 0.14*features.debate
		blocked += 0.02 * float64(maxInt(0, len(req.AgentIDs)-3))
	case ModePipeline:
		success = 0.72 + 0.16*features.sequential - 0.06*features.independent
		if len(req.AgentIDs) == 1 {
			success += 0.05
		}
	case ModeDebate:
		success = 0.54 + 0.32*features.debate + 0.08*features.ambiguous - 0.18*boolFloat(len(req.AgentIDs) < 2)
		blocked += 0.03
	}
	success = clampFloat(success, 0.05, 0.96)
	blocked = clampFloat(blocked, 0.01, 0.30)
	return normalizeTransition(TransitionProbability{
		Success: success,
		Blocked: blocked,
		Failure: 1 - success - blocked,
	})
}

func decisionWeight(prob TransitionProbability, cost, risk float64) float64 {
	success := clampFloat(prob.Success, 0.001, 0.999)
	return -math.Log(success) + 0.24*cost + 0.46*risk + 0.35*prob.Blocked + 0.18*prob.Failure
}

func estimatePlannerCost(mode CollabMode, req PlanRequest) float64 {
	n := float64(maxInt(1, len(req.AgentIDs)))
	switch mode {
	case ModeParallel:
		return clampFloat(0.22+0.04*n, 0, 1)
	case ModePipeline:
		return clampFloat(0.18+0.13*n, 0, 1)
	case ModeDebate:
		return clampFloat(0.42+0.12*n, 0, 1)
	default:
		return 0.5
	}
}

func estimatePlannerRisk(mode CollabMode, req PlanRequest) float64 {
	features := plannerFeatures(req)
	switch mode {
	case ModeParallel:
		return clampFloat(0.18+0.34*features.sequential+0.12*features.debate, 0, 1)
	case ModePipeline:
		return clampFloat(0.16+0.10*features.independent+0.08*features.debate, 0, 1)
	case ModeDebate:
		return clampFloat(0.24+0.24*(1-features.debate)+0.10*features.risk, 0, 1)
	default:
		return 0.5
	}
}

type features struct {
	sequential  float64
	independent float64
	debate      float64
	ambiguous   float64
	risk        float64
}

func plannerFeatures(req PlanRequest) features {
	text := strings.ToLower(req.Description + "\n" + req.Input)
	sequential := boolFloat(containsAnyText(text, "先", "再", "最后", "依赖", "流水线", "pipeline", "sequential", "step by step"))
	debate := boolFloat(containsAnyText(text, "辩论", "争议", "评审", "裁决", "debate", "critic", "review"))
	ambiguous := boolFloat(containsAnyText(text, "是否", "比较", "权衡", "方案", "设计", "should", "compare"))
	independent := boolFloat(len(req.AgentIDs) > 1)
	if containsAnyText(text, "分别", "并行", "多个", "parallel", "independent") {
		independent = 1
	}
	risk := 0.25 + 0.20*debate + 0.15*ambiguous
	if containsAnyText(text, "删除", "发布", "上线", "迁移", "密钥", "权限", "delete", "deploy", "secret") {
		risk += 0.25
	}
	return features{
		sequential:  sequential,
		independent: independent,
		debate:      debate,
		ambiguous:   ambiguous,
		risk:        clampFloat(risk, 0, 1),
	}
}

func normalizeAllowedModes(req PlanRequest) []CollabMode {
	allowed := req.AllowedModes
	if len(allowed) == 0 {
		allowed = []CollabMode{ModeParallel, ModePipeline, ModeDebate}
	}
	if len(req.AgentIDs) <= 1 {
		return []CollabMode{ModePipeline}
	}
	out := make([]CollabMode, 0, len(allowed))
	seen := make(map[CollabMode]bool, len(allowed))
	for _, mode := range allowed {
		if mode == ModeAuto || !isExecutableMode(mode) || seen[mode] {
			continue
		}
		seen[mode] = true
		out = append(out, mode)
	}
	return out
}

func isExecutableMode(mode CollabMode) bool {
	return mode == ModePipeline || mode == ModeParallel || mode == ModeDebate
}

func shortestPath(edges []PlanEdge, start, end string) ([]string, []PlanEdge, float64) {
	graph := make(map[string][]PlanEdge)
	for _, edge := range edges {
		graph[edge.From] = append(graph[edge.From], edge)
	}
	dist := map[string]float64{start: 0}
	prevNode := make(map[string]string)
	prevEdge := make(map[string]PlanEdge)
	pq := &nodeQueue{{node: start, dist: 0}}
	heap.Init(pq)
	visited := make(map[string]bool)
	for pq.Len() > 0 {
		item := heap.Pop(pq).(queueNode)
		if visited[item.node] {
			continue
		}
		visited[item.node] = true
		if item.node == end {
			break
		}
		for _, edge := range graph[item.node] {
			nextDist := item.dist + edge.Weight
			if old, ok := dist[edge.To]; !ok || nextDist < old {
				dist[edge.To] = nextDist
				prevNode[edge.To] = item.node
				prevEdge[edge.To] = edge
				heap.Push(pq, queueNode{node: edge.To, dist: nextDist})
			}
		}
	}
	total, ok := dist[end]
	if !ok {
		return nil, nil, math.Inf(1)
	}
	var path []string
	var trace []PlanEdge
	for node := end; node != ""; node = prevNode[node] {
		path = append(path, node)
		if node == start {
			break
		}
		if edge, ok := prevEdge[node]; ok {
			trace = append(trace, edge)
		}
	}
	reverseStrings(path)
	reverseEdges(trace)
	return path, trace, total
}

type queueNode struct {
	node string
	dist float64
}

type nodeQueue []queueNode

func (q nodeQueue) Len() int           { return len(q) }
func (q nodeQueue) Less(i, j int) bool { return q[i].dist < q[j].dist }
func (q nodeQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }

func (q *nodeQueue) Push(x any) {
	*q = append(*q, x.(queueNode))
}

func (q *nodeQueue) Pop() any {
	old := *q
	n := len(old)
	item := old[n-1]
	*q = old[:n-1]
	return item
}

func modeFromPath(path []string) CollabMode {
	for _, node := range path {
		if strings.HasPrefix(node, "mode:") {
			return CollabMode(strings.TrimPrefix(node, "mode:"))
		}
	}
	return ""
}

func stateToOutcome(state TaskState) string {
	switch state {
	case TaskCompleted:
		return "success"
	case TaskCancelled, TaskTimeout:
		return "blocked"
	default:
		return "failure"
	}
}

func normalizeTransition(p TransitionProbability) TransitionProbability {
	p.Success = clampFloat(p.Success, 0, 1)
	p.Failure = clampFloat(p.Failure, 0, 1)
	p.Blocked = clampFloat(p.Blocked, 0, 1)
	sum := p.Success + p.Failure + p.Blocked
	if sum <= 0 {
		p.Success = 0.65
		p.Failure = 0.28
		p.Blocked = 0.07
		return p
	}
	p.Success /= sum
	p.Failure /= sum
	p.Blocked /= sum
	return p
}

func decisionBasis(req PlanRequest) string {
	f := plannerFeatures(req)
	parts := []string{
		fmt.Sprintf("agents=%d", len(req.AgentIDs)),
		fmt.Sprintf("sequential=%.0f", f.sequential),
		fmt.Sprintf("independent=%.0f", f.independent),
		fmt.Sprintf("debate=%.0f", f.debate),
		fmt.Sprintf("risk=%.2f", f.risk),
	}
	return strings.Join(parts, " ")
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func reverseEdges(values []PlanEdge) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func containsAnyText(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func boolFloat(ok bool) float64 {
	if ok {
		return 1
	}
	return 0
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
