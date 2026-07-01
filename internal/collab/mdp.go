package collab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const mdpPlannerVersion = "mdp-q-v1"

// MDPState is the compact state abstraction used by the multi-agent planner.
// It deliberately keeps only stable task features so the online table can
// learn from repeated task shapes instead of memorizing prompt text.
type MDPState struct {
	TaskShape     string `json:"task_shape"`
	Ambiguity     string `json:"ambiguity"`
	Risk          string `json:"risk"`
	AgentBucket   string `json:"agent_bucket"`
	HasCritic     bool   `json:"has_critic"`
	HasVerifier   bool   `json:"has_verifier"`
	TimeoutBucket string `json:"timeout_bucket"`
}

// Key returns a stable table key for Q(S,A).
func (s MDPState) Key() string {
	return strings.Join([]string{
		"shape=" + s.TaskShape,
		"amb=" + s.Ambiguity,
		"risk=" + s.Risk,
		"agents=" + s.AgentBucket,
		fmt.Sprintf("critic=%t", s.HasCritic),
		fmt.Sprintf("verifier=%t", s.HasVerifier),
		"timeout=" + s.TimeoutBucket,
	}, "|")
}

// RewardBreakdown captures the reward terms used for MDP updates.
type RewardBreakdown struct {
	Outcome       string  `json:"outcome"`
	Success       float64 `json:"success"`
	Partial       float64 `json:"partial"`
	Failure       float64 `json:"failure"`
	Blocked       float64 `json:"blocked"`
	LatencyCost   float64 `json:"latency_cost"`
	CoordCost     float64 `json:"coord_cost"`
	RiskPenalty   float64 `json:"risk_penalty"`
	VerifierBonus float64 `json:"verifier_bonus"`
	Total         float64 `json:"total"`
}

// MDPAction is the expanded action used by the Q table. Mode remains the
// executable decision, while the rest describes the orchestration policy.
type MDPAction struct {
	Mode            CollabMode `json:"mode"`
	Aggregation     string     `json:"aggregation"`
	RetryPolicy     string     `json:"retry_policy"`
	RequireVerifier bool       `json:"require_verifier"`
	MaxSteps        int        `json:"max_steps"`
	MaxConcurrent   int        `json:"max_concurrent"`
}

// Key returns a stable action identifier for Q(S,A).
func (a MDPAction) Key() string {
	return fmt.Sprintf("mode=%s|agg=%s|retry=%s|verify=%t|steps=%d|conc=%d",
		a.Mode, a.Aggregation, a.RetryPolicy, a.RequireVerifier, a.MaxSteps, a.MaxConcurrent)
}

// MDPDecision is attached to PlanResult so scheduling decisions are auditable.
type MDPDecision struct {
	Version                 string                        `json:"version"`
	State                   MDPState                      `json:"state"`
	StateKey                string                        `json:"state_key"`
	Actions                 map[CollabMode]MDPAction      `json:"actions"`
	QValues                 map[CollabMode]float64        `json:"q_values"`
	Samples                 map[CollabMode]int            `json:"samples"`
	ActionQValues           map[string]float64            `json:"action_q_values"`
	ActionSamples           map[string]int                `json:"action_samples"`
	TransitionProbabilities map[string]map[string]float64 `json:"transition_probabilities,omitempty"`
	BestAction              CollabMode                    `json:"best_action,omitempty"`
}

// MDPObservation is the execution feedback used by Q-learning.
type MDPObservation struct {
	Request  PlanRequest
	Action   CollabMode
	Outcome  string
	Duration time.Duration
	Reward   *RewardBreakdown
}

// MDPModel stores an online Q table for collaboration mode selection.
type MDPModel struct {
	mu          sync.RWMutex
	alpha       float64
	gamma       float64
	q           map[string]map[string]float64
	samples     map[string]map[string]int
	transitions map[string]map[string]map[string]int
}

// MDPModelSnapshot is the JSON-persisted form of MDPModel.
type MDPModelSnapshot struct {
	Version     string                               `json:"version"`
	Alpha       float64                              `json:"alpha"`
	Gamma       float64                              `json:"gamma"`
	Q           map[string]map[string]float64        `json:"q"`
	Samples     map[string]map[string]int            `json:"samples"`
	Transitions map[string]map[string]map[string]int `json:"transitions"`
	UpdatedAt   time.Time                            `json:"updated_at"`
}

// NewMDPModel creates a Q-learning model with conservative defaults.
func NewMDPModel() *MDPModel {
	return &MDPModel{
		alpha:       0.35,
		gamma:       0.72,
		q:           make(map[string]map[string]float64),
		samples:     make(map[string]map[string]int),
		transitions: make(map[string]map[string]map[string]int),
	}
}

// PlanStateFromRequest converts a scheduling request into a compact MDP state.
func PlanStateFromRequest(req PlanRequest) MDPState {
	f := plannerFeatures(req)
	text := strings.ToLower(req.Description + "\n" + req.Input)
	return MDPState{
		TaskShape:     taskShapeBucket(f),
		Ambiguity:     ternaryBucket(f.ambiguous, 0.70, 0.25),
		Risk:          ternaryBucket(f.risk, 0.60, 0.32),
		AgentBucket:   agentCountBucket(len(req.AgentIDs)),
		HasCritic:     hasCapability(req.Agents, "critic", "review", "verifier") || containsAnyText(text, "critic", "评审", "裁决"),
		HasVerifier:   hasCapability(req.Agents, "verifier", "test", "testing", "qa") || containsAnyText(text, "验证", "测试", "verifier", "test"),
		TimeoutBucket: timeoutBucket(req.Timeout),
	}
}

// Decision returns the current Q estimates for the allowed actions.
func (m *MDPModel) Decision(req PlanRequest, modes []CollabMode) MDPDecision {
	state := PlanStateFromRequest(req)
	decision := MDPDecision{
		Version:                 mdpPlannerVersion,
		State:                   state,
		StateKey:                state.Key(),
		Actions:                 make(map[CollabMode]MDPAction, len(modes)),
		QValues:                 make(map[CollabMode]float64, len(modes)),
		Samples:                 make(map[CollabMode]int, len(modes)),
		ActionQValues:           make(map[string]float64, len(modes)),
		ActionSamples:           make(map[string]int, len(modes)),
		TransitionProbabilities: make(map[string]map[string]float64, len(modes)),
	}
	if m == nil {
		return decision
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	values := m.q[decision.StateKey]
	samples := m.samples[decision.StateKey]
	transitions := m.transitions[decision.StateKey]
	bestSet := false
	bestValue := 0.0
	for _, mode := range modes {
		action := MDPActionForMode(req, mode)
		actionKey := action.Key()
		decision.Actions[mode] = action
		decision.QValues[mode] = 0
		decision.Samples[mode] = 0
		decision.ActionQValues[actionKey] = 0
		decision.ActionSamples[actionKey] = 0
		if values != nil {
			decision.QValues[mode] = values[actionKey]
			decision.ActionQValues[actionKey] = values[actionKey]
		}
		if samples != nil {
			decision.Samples[mode] = samples[actionKey]
			decision.ActionSamples[actionKey] = samples[actionKey]
		}
		if transitions != nil {
			if counts := transitions[actionKey]; len(counts) > 0 {
				decision.TransitionProbabilities[actionKey] = transitionProbabilities(counts)
			}
		}
		if !bestSet || decision.QValues[mode] > bestValue {
			bestSet = true
			bestValue = decision.QValues[mode]
			decision.BestAction = mode
		}
	}
	return decision
}

// Observe updates Q(S,A) from a completed collaboration task.
func (m *MDPModel) Observe(obs MDPObservation) {
	if m == nil || !isExecutableMode(obs.Action) {
		return
	}
	state := PlanStateFromRequest(obs.Request)
	stateKey := state.Key()
	actionKey := MDPActionForMode(obs.Request, obs.Action).Key()
	nextStateKey := terminalStateKey(obs.Outcome)
	reward := obs.Reward
	if reward == nil {
		r := RewardFromObservation(obs.Request, obs.Action, obs.Outcome, obs.Duration)
		reward = &r
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.q[stateKey] == nil {
		m.q[stateKey] = make(map[string]float64)
	}
	if m.samples[stateKey] == nil {
		m.samples[stateKey] = make(map[string]int)
	}
	if m.transitions[stateKey] == nil {
		m.transitions[stateKey] = make(map[string]map[string]int)
	}
	if m.transitions[stateKey][actionKey] == nil {
		m.transitions[stateKey][actionKey] = make(map[string]int)
	}
	old := m.q[stateKey][actionKey]
	nextBest := maxQValue(m.q[stateKey])
	updated := (1-m.alpha)*old + m.alpha*(reward.Total+m.gamma*nextBest)
	m.q[stateKey][actionKey] = updated
	m.samples[stateKey][actionKey]++
	m.transitions[stateKey][actionKey][nextStateKey]++
}

// Snapshot returns a deep copy suitable for persistence or inspection.
func (m *MDPModel) Snapshot() MDPModelSnapshot {
	if m == nil {
		return MDPModelSnapshot{Version: mdpPlannerVersion, UpdatedAt: time.Now()}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return MDPModelSnapshot{
		Version:     mdpPlannerVersion,
		Alpha:       m.alpha,
		Gamma:       m.gamma,
		Q:           cloneFloatTable(m.q),
		Samples:     cloneIntTable(m.samples),
		Transitions: cloneTransitionTable(m.transitions),
		UpdatedAt:   time.Now(),
	}
}

// Restore replaces the model state from a snapshot.
func (m *MDPModel) Restore(snapshot MDPModelSnapshot) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alpha = snapshot.Alpha
	if m.alpha <= 0 {
		m.alpha = 0.35
	}
	m.gamma = snapshot.Gamma
	if m.gamma <= 0 {
		m.gamma = 0.72
	}
	m.q = cloneFloatTable(snapshot.Q)
	m.samples = cloneIntTable(snapshot.Samples)
	m.transitions = cloneTransitionTable(snapshot.Transitions)
	if m.q == nil {
		m.q = make(map[string]map[string]float64)
	}
	if m.samples == nil {
		m.samples = make(map[string]map[string]int)
	}
	if m.transitions == nil {
		m.transitions = make(map[string]map[string]map[string]int)
	}
}

// SaveJSON persists the model snapshot to disk.
func (m *MDPModel) SaveJSON(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("mdp snapshot path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create mdp snapshot dir: %w", err)
	}
	data, err := json.MarshalIndent(m.Snapshot(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode mdp snapshot: %w", err)
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadMDPModelJSON loads a model snapshot from disk.
func LoadMDPModelJSON(path string) (*MDPModel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mdp snapshot: %w", err)
	}
	var snapshot MDPModelSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode mdp snapshot: %w", err)
	}
	model := NewMDPModel()
	model.Restore(snapshot)
	return model, nil
}

// RewardFromObservation builds the default reward from runtime outcome.
func RewardFromObservation(req PlanRequest, mode CollabMode, outcome string, duration time.Duration) RewardBreakdown {
	r := RewardBreakdown{Outcome: outcome}
	switch outcome {
	case "success":
		r.Success = 1.0
	case "partial":
		r.Partial = 0.45
	case "blocked":
		r.Blocked = -0.75
	default:
		r.Failure = -0.90
	}
	if req.Timeout > 0 && duration > 0 {
		r.LatencyCost = 0.18 * clampFloat(duration.Seconds()/req.Timeout.Seconds(), 0, 1)
	}
	r.CoordCost = 0.12 * estimatePlannerCost(mode, req)
	r.RiskPenalty = 0.18 * estimatePlannerRisk(mode, req)
	if r.Success > 0 && verifierAvailable(req) {
		r.VerifierBonus = 0.12
	}
	r.Total = r.Success + r.Partial + r.Failure + r.Blocked - r.LatencyCost - r.CoordCost - r.RiskPenalty + r.VerifierBonus
	return r
}

func maxQValue(values map[string]float64) float64 {
	if len(values) == 0 {
		return 0
	}
	best := 0.0
	first := true
	for _, v := range values {
		if first || v > best {
			best = v
			first = false
		}
	}
	return best
}

// MDPActionForMode expands a collaboration mode into a concrete orchestration
// policy action.
func MDPActionForMode(req PlanRequest, mode CollabMode) MDPAction {
	f := plannerFeatures(req)
	action := MDPAction{Mode: mode, RetryPolicy: "none", MaxSteps: maxInt(1, len(req.AgentIDs)), MaxConcurrent: 1}
	switch mode {
	case ModeParallel:
		action.Aggregation = "merge"
		action.MaxConcurrent = maxInt(2, len(req.AgentIDs))
		if f.risk >= 0.55 || PlanStateFromRequest(req).HasVerifier {
			action.RequireVerifier = true
			action.Aggregation = "critic_merge"
		}
		if f.risk >= 0.65 {
			action.RetryPolicy = "retry_failed_once"
		}
	case ModePipeline:
		action.Aggregation = "last"
		action.RetryPolicy = "fail_fast"
		action.MaxSteps = maxInt(1, len(req.AgentIDs))
		if f.risk >= 0.60 || PlanStateFromRequest(req).HasVerifier {
			action.RequireVerifier = true
			action.RetryPolicy = "verify_each_step"
		}
	case ModeDebate:
		action.Aggregation = "vote"
		action.RetryPolicy = "critic_tiebreak"
		action.RequireVerifier = true
		action.MaxSteps = maxInt(2, len(req.AgentIDs)*2)
		action.MaxConcurrent = 1
	default:
		action.Aggregation = "none"
	}
	return action
}

func mdpWeightAdjustment(decision MDPDecision, mode CollabMode) float64 {
	if len(decision.QValues) == 0 {
		return 0
	}
	totalSamples := 0
	for _, n := range decision.Samples {
		totalSamples += n
	}
	if totalSamples == 0 {
		return 0
	}
	confidence := clampFloat(float64(totalSamples)/12.0, 0, 1)
	return -0.32 * confidence * decision.QValues[mode]
}

func mdpDecisionSummary(decision MDPDecision) string {
	if decision.StateKey == "" {
		return ""
	}
	modes := make([]string, 0, len(decision.QValues))
	for mode := range decision.QValues {
		modes = append(modes, string(mode))
	}
	sort.Strings(modes)
	parts := make([]string, 0, len(modes))
	for _, modeName := range modes {
		mode := CollabMode(modeName)
		actionKey := ""
		if action, ok := decision.Actions[mode]; ok {
			actionKey = action.Key()
		}
		parts = append(parts, fmt.Sprintf("%s=%.4f/%d[%s]", mode, decision.QValues[mode], decision.Samples[mode], actionKey))
	}
	return strings.Join(parts, " ")
}

func terminalStateKey(outcome string) string {
	switch outcome {
	case "success", "partial", "blocked":
		return "terminal|outcome=" + outcome
	default:
		return "terminal|outcome=failure"
	}
}

func transitionProbabilities(counts map[string]int) map[string]float64 {
	total := 0
	for _, count := range counts {
		total += count
	}
	out := make(map[string]float64, len(counts))
	if total == 0 {
		return out
	}
	for state, count := range counts {
		out[state] = float64(count) / float64(total)
	}
	return out
}

func cloneFloatTable(in map[string]map[string]float64) map[string]map[string]float64 {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]float64, len(in))
	for state, values := range in {
		out[state] = make(map[string]float64, len(values))
		for action, value := range values {
			out[state][action] = value
		}
	}
	return out
}

func cloneIntTable(in map[string]map[string]int) map[string]map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]int, len(in))
	for state, values := range in {
		out[state] = make(map[string]int, len(values))
		for action, value := range values {
			out[state][action] = value
		}
	}
	return out
}

func cloneTransitionTable(in map[string]map[string]map[string]int) map[string]map[string]map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]map[string]int, len(in))
	for state, actions := range in {
		out[state] = make(map[string]map[string]int, len(actions))
		for action, nextStates := range actions {
			out[state][action] = make(map[string]int, len(nextStates))
			for nextState, count := range nextStates {
				out[state][action][nextState] = count
			}
		}
	}
	return out
}

func taskShapeBucket(f features) string {
	switch {
	case f.debate >= 0.5:
		return "debate"
	case f.sequential >= 0.5:
		return "sequential"
	case f.independent >= 0.5:
		return "independent"
	default:
		return "single"
	}
}

func ternaryBucket(v, high, medium float64) string {
	switch {
	case v >= high:
		return "high"
	case v >= medium:
		return "medium"
	default:
		return "low"
	}
}

func agentCountBucket(n int) string {
	switch {
	case n <= 1:
		return "one"
	case n <= 3:
		return "few"
	default:
		return "many"
	}
}

func timeoutBucket(timeout time.Duration) string {
	switch {
	case timeout <= 0:
		return "none"
	case timeout < 30*time.Second:
		return "short"
	case timeout <= 2*time.Minute:
		return "normal"
	default:
		return "long"
	}
}

func hasCapability(agents []*AgentProfile, terms ...string) bool {
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		for _, cap := range agent.Capabilities {
			cap = strings.ToLower(cap)
			for _, term := range terms {
				if strings.Contains(cap, strings.ToLower(term)) {
					return true
				}
			}
		}
	}
	return false
}

func verifierAvailable(req PlanRequest) bool {
	state := PlanStateFromRequest(req)
	return state.HasVerifier
}
