package proactive

import (
	"context"
	"fmt"
	"time"
)

// Gate evaluates the estimate and emits dry-run actions.
type Gate struct {
	Config Config
}

func NewGate(cfg Config) Gate {
	if cfg.ConfidenceThreshold <= 0 {
		cfg.ConfidenceThreshold = 0.60
	}
	if cfg.Horizon <= 0 {
		cfg.Horizon = 5 * time.Minute
	}
	return Gate{Config: cfg}
}

func (g Gate) Decide(ctx context.Context, signals []Signal, estimate StateEstimate) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	now := estimate.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	allowed := estimate.Confidence >= g.Config.ConfidenceThreshold
	actions := g.actionsForState(estimate, allowed, now)
	return Decision{
		Enabled:  g.Config.Enabled,
		DryRun:   g.Config.DryRun,
		Signals:  signals,
		Estimate: estimate,
		Actions:  actions,
	}, nil
}

func (g Gate) actionsForState(estimate StateEstimate, allowed bool, now time.Time) []DryRunAction {
	reason := fmt.Sprintf("confidence %.2f vs threshold %.2f", estimate.Confidence, g.Config.ConfidenceThreshold)
	if g.Config.DryRun {
		reason += "; dry-run only"
	}
	if !g.Config.Enabled {
		reason += "; proactive disabled"
	}
	if !allowed {
		return []DryRunAction{{
			ID:         actionID(estimate.ID, 1),
			StateID:    estimate.ID,
			Action:     "observe_only",
			Confidence: estimate.Confidence,
			Allowed:    false,
			Reason:     reason,
			CreatedAt:  now,
		}}
	}
	names := actionNames(estimate.PredictedState)
	actions := make([]DryRunAction, 0, len(names))
	for i, name := range names {
		actions = append(actions, DryRunAction{
			ID:         actionID(estimate.ID, i+1),
			StateID:    estimate.ID,
			Action:     name,
			Confidence: estimate.Confidence,
			Allowed:    true,
			Reason:     reason,
			CreatedAt:  now,
		})
	}
	return actions
}

func actionNames(state string) []string {
	switch state {
	case "coding":
		return []string{"preload_recent_project_context", "warm_memory_context"}
	case "meeting":
		return []string{"prepare_meeting_notes"}
	case "low_energy":
		return []string{"prefer_lightweight_tasks"}
	case "planning":
		return []string{"preload_recent_session_summary"}
	default:
		return []string{"observe_only"}
	}
}

func actionID(stateID string, seq int) string {
	return fmt.Sprintf("%s-action-%02d", stateID, seq)
}
