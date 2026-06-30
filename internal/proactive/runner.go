package proactive

import (
	"context"
	"fmt"
)

// Runner wires sampler, estimator, gate, and optional persistence.
type Runner struct {
	Sampler   Sampler
	Estimator Estimator
	Gate      Gate
	Store     *Store
}

func NewRunner(sampler Sampler, estimator Estimator, gate Gate, store *Store) Runner {
	return Runner{Sampler: sampler, Estimator: estimator, Gate: gate, Store: store}
}

func (r Runner) RunDryRun(ctx context.Context) (Decision, error) {
	signals, err := r.Sampler.Sample(ctx)
	if err != nil {
		return Decision{}, err
	}
	estimate := r.Estimator.Estimate(signals, r.Gate.Config.Horizon)
	decision, err := r.Gate.Decide(ctx, signals, estimate)
	if err != nil {
		return Decision{}, err
	}
	if r.Store != nil {
		if err := r.Store.RecordSignals(signals); err != nil {
			return Decision{}, fmt.Errorf("persist proactive signals: %w", err)
		}
		if err := r.Store.RecordEstimate(estimate); err != nil {
			return Decision{}, fmt.Errorf("persist proactive estimate: %w", err)
		}
		if err := r.Store.RecordActions(decision.Actions); err != nil {
			return Decision{}, fmt.Errorf("persist proactive actions: %w", err)
		}
	}
	return decision, nil
}
