package proactive

import "fmt"

// FeedbackCalibrator lightly adjusts estimate confidence from recent feedback.
// It is intentionally conservative; learned response kernels can later replace
// this without changing Runner callers.
type FeedbackCalibrator struct {
	Store               *Store
	Limit               int
	MinEvents           int
	KernelMinSamples    int
	KernelMaxAdjustment float64
}

func NewFeedbackCalibrator(store *Store) FeedbackCalibrator {
	return FeedbackCalibrator{Store: store, Limit: 100, MinEvents: 3, KernelMinSamples: 2, KernelMaxAdjustment: 0.10}
}

func (c FeedbackCalibrator) Calibrate(estimate StateEstimate, signals []Signal) StateEstimate {
	if c.Store == nil || estimate.PredictedState == "" {
		return estimate
	}
	estimate = c.calibrateWithKernel(estimate, signals)
	limit := c.Limit
	if limit <= 0 {
		limit = 100
	}
	minEvents := c.MinEvents
	if minEvents <= 0 {
		minEvents = 3
	}
	stats, err := c.Store.FeedbackStatsForState(estimate.PredictedState, limit)
	if err != nil || stats.Events < minEvents {
		return estimate
	}

	delta := 0.0
	switch {
	case stats.Accuracy >= 0.80:
		delta = 0.05
	case stats.Accuracy < 0.50:
		delta = -0.15
	case stats.Accuracy < 0.65:
		delta = -0.07
	}
	if delta == 0 {
		estimate.Reasons = append(estimate.Reasons, fmt.Sprintf("feedback calibration: accuracy %.2f over %d events kept confidence unchanged", stats.Accuracy, stats.Events))
		return estimate
	}
	estimate.Confidence = clamp(estimate.Confidence+delta, 0.05, 0.95)
	estimate.NoiseVariance = clamp(1-estimate.Confidence, 0.05, 1)
	estimate.Reasons = append(estimate.Reasons, fmt.Sprintf("feedback calibration: accuracy %.2f over %d events adjusted confidence by %.2f", stats.Accuracy, stats.Events, delta))
	return estimate
}

func (c FeedbackCalibrator) calibrateWithKernel(estimate StateEstimate, signals []Signal) StateEstimate {
	if len(signals) == 0 || c.Store == nil {
		return estimate
	}
	weights, err := c.Store.SignalWeightsForState(estimate.PredictedState)
	if err != nil || len(weights) == 0 {
		return estimate
	}
	minSamples := c.KernelMinSamples
	if minSamples <= 0 {
		minSamples = 2
	}
	maxAdjustment := c.KernelMaxAdjustment
	if maxAdjustment <= 0 || maxAdjustment > 0.3 {
		maxAdjustment = 0.10
	}
	var total float64
	var matched int
	for _, signal := range signals {
		weight, ok := weights[signalWeightKey(signal.Channel, signal.Label)]
		if !ok || weight.Samples < minSamples {
			continue
		}
		total += weight.Weight
		matched++
	}
	if matched == 0 {
		return estimate
	}
	avg := total / float64(matched)
	delta := clamp(avg*maxAdjustment, -maxAdjustment, maxAdjustment)
	if delta == 0 {
		return estimate
	}
	estimate.Confidence = clamp(estimate.Confidence+delta, 0.05, 0.95)
	estimate.NoiseVariance = clamp(1-estimate.Confidence, 0.05, 1)
	estimate.Reasons = append(estimate.Reasons, fmt.Sprintf("learned response kernel: %d matching signals adjusted confidence by %.2f", matched, delta))
	return estimate
}
