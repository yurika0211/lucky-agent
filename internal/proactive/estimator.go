package proactive

import (
	"fmt"
	"math"
	"time"
)

// Estimator predicts the near-future state from sampled signals. The v1 model
// is intentionally heuristic; learned kernels can replace it behind this type.
type Estimator struct{}

func NewEstimator() Estimator {
	return Estimator{}
}

func (Estimator) Estimate(signals []Signal, horizon time.Duration) StateEstimate {
	if horizon <= 0 {
		horizon = 5 * time.Minute
	}
	now := time.Now()
	if len(signals) > 0 && !signals[0].CreatedAt.IsZero() {
		now = signals[0].CreatedAt
	}

	labels := map[string]Signal{}
	for _, signal := range signals {
		labels[signal.Channel] = signal
	}

	state := "unknown"
	confidence := 0.25
	reasons := []string{"only passive local signals available"}

	timeSig, hasTime := labels["time_of_day"]
	workspaceSig, hasWorkspace := labels["workspace_context"]
	toolActivity, hasToolActivity := labels["runtime_tool_activity"]
	chatActivity, hasChatActivity := labels["runtime_chat_activity"]
	if hasWorkspace {
		switch workspaceSig.Label {
		case "go_repo", "node_repo", "git_repo":
			state = "coding"
			confidence = 0.58
			reasons = append(reasons, fmt.Sprintf("workspace looks like %s", workspaceSig.Label))
		}
	}
	if hasTime {
		switch timeSig.Label {
		case "morning_work", "afternoon_work":
			if state == "coding" {
				confidence += 0.14
				reasons = append(reasons, "inside common deep-work hours")
			} else {
				state = "planning"
				confidence = 0.46
				reasons = append(reasons, "inside common work hours")
			}
		case "evening":
			if state == "coding" {
				confidence += 0.07
				reasons = append(reasons, "evening repo work often indicates continuation")
			}
		case "deep_night", "late_night":
			if state == "coding" {
				confidence = math.Max(confidence-0.08, 0.45)
				reasons = append(reasons, "late-night coding signal is noisier")
			} else {
				state = "low_energy"
				confidence = 0.52
				reasons = append(reasons, "late-night period increases low-energy likelihood")
			}
		case "morning_ramp":
			if state == "unknown" {
				state = "planning"
				confidence = 0.42
				reasons = append(reasons, "morning ramp is more likely preparation than execution")
			}
		}
	}
	if hasToolActivity && toolActivity.Value > 0 {
		if state == "coding" {
			confidence += 0.05
			reasons = append(reasons, "recent tool activity supports active work state")
		} else if state == "unknown" {
			state = "working"
			confidence = 0.50
			reasons = append(reasons, "recent tool activity indicates active work")
		}
	}
	if hasChatActivity && chatActivity.Value > 0 && state == "unknown" {
		state = "planning"
		confidence = 0.40
		reasons = append(reasons, "recent chat activity indicates planning or clarification")
	}

	confidence = clamp(confidence, 0, 0.95)
	return StateEstimate{
		ID:             fmt.Sprintf("state-%d", now.UnixNano()),
		PredictedState: state,
		Confidence:     confidence,
		NoiseVariance:  clamp(1-confidence, 0.05, 1),
		Horizon:        horizon,
		Reasons:        reasons,
		CreatedAt:      now,
	}
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
