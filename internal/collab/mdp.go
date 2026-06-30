package collab

import (
	"fmt"
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

// MDPDecision is attached to PlanResult so scheduling decisions are auditable.
type MDPDecision struct {
	Version    string                 `json:"version"`
	State      MDPState               `json:"state"`
	StateKey   string                 `json:"state_key"`
	QValues    map[CollabMode]float64 `json:"q_values"`
	Samples    map[CollabMode]int     `json:"samples"`
	BestAction CollabMode             `json:"best_action,omitempty"`
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
	mu      sync.RWMutex
	alpha   float64
	gamma   float64
	q       map[string]map[CollabMode]float64
	samples map[string]map[CollabMode]int
}

// NewMDPModel creates a Q-learning model with conservative defaults.
func NewMDPModel() *MDPModel {
	return &MDPModel{
		alpha:   0.35,
		gamma:   0.72,
		q:       make(map[string]map[CollabMode]float64),
		samples: make(map[string]map[CollabMode]int),
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
		Version:  mdpPlannerVersion,
		State:    state,
		StateKey: state.Key(),
		QValues:  make(map[CollabMode]float64, len(modes)),
		Samples:  make(map[CollabMode]int, len(modes)),
	}
	if m == nil {
		return decision
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	values := m.q[decision.StateKey]
	samples := m.samples[decision.StateKey]
	bestSet := false
	bestValue := 0.0
	for _, mode := range modes {
		decision.QValues[mode] = 0
		decision.Samples[mode] = 0
		if values != nil {
			decision.QValues[mode] = values[mode]
		}
		if samples != nil {
			decision.Samples[mode] = samples[mode]
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
	reward := obs.Reward
	if reward == nil {
		r := RewardFromObservation(obs.Request, obs.Action, obs.Outcome, obs.Duration)
		reward = &r
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.q[stateKey] == nil {
		m.q[stateKey] = make(map[CollabMode]float64)
	}
	if m.samples[stateKey] == nil {
		m.samples[stateKey] = make(map[CollabMode]int)
	}
	old := m.q[stateKey][obs.Action]
	nextBest := maxQValue(m.q[stateKey])
	updated := (1-m.alpha)*old + m.alpha*(reward.Total+m.gamma*nextBest)
	m.q[stateKey][obs.Action] = updated
	m.samples[stateKey][obs.Action]++
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

func maxQValue(values map[CollabMode]float64) float64 {
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
		parts = append(parts, fmt.Sprintf("%s=%.4f/%d", mode, decision.QValues[mode], decision.Samples[mode]))
	}
	return strings.Join(parts, " ")
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
