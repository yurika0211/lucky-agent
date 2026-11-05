package proactive

import "time"

// Config controls the proactive state estimator. The first production slice is
// intentionally dry-run first: it predicts and records, but does not execute
// user-visible actions.
type Config struct {
	Enabled             bool
	DryRun              bool
	ConfidenceThreshold float64
	Horizon             time.Duration
}

// Signal is one sampled component of the user's current "gravitational field".
type Signal struct {
	ID        string
	Channel   string
	Value     float64
	Label     string
	Metadata  map[string]string
	CreatedAt time.Time
}

// StateEstimate is the predicted user state at a short future horizon.
type StateEstimate struct {
	ID             string
	PredictedState string
	Confidence     float64
	NoiseVariance  float64
	Horizon        time.Duration
	Reasons        []string
	CreatedAt      time.Time
}

// FeedbackEvent records what actually happened after a prediction.
type FeedbackEvent struct {
	ID             string
	StateID        string
	PredictedState string
	ActualState    string
	Value          float64
	Source         string
	Note           string
	CreatedAt      time.Time
}

// SignalWeight is a learned response-kernel coefficient for one signal label.
type SignalWeight struct {
	PredictedState string
	Channel        string
	Label          string
	Weight         float64
	Samples        int
	UpdatedAt      time.Time
}

// RuntimeEvent captures passive runtime telemetry used by proactive modeling.
type RuntimeEvent struct {
	ID        string
	Source    string
	SessionID string
	Type      string
	Name      string
	Value     float64
	Metadata  map[string]string
	CreatedAt time.Time
}

// DryRunAction describes an action the proactive gate would take.
type DryRunAction struct {
	ID         string
	StateID    string
	Action     string
	Confidence float64
	Allowed    bool
	Reason     string
	CreatedAt  time.Time
}

// ActionExecution records the safety decision and result for an action.
type ActionExecution struct {
	ID        string
	ActionID  string
	StateID   string
	Action    string
	Status    string
	Reason    string
	Metadata  map[string]string
	CreatedAt time.Time
}

// Decision is the complete output of one proactive dry-run cycle.
type Decision struct {
	Enabled  bool
	DryRun   bool
	Signals  []Signal
	Estimate StateEstimate
	Actions  []DryRunAction
}

// Stats summarizes persisted proactive telemetry.
type Stats struct {
	Signals        int `json:"signals"`
	Estimates      int `json:"estimates"`
	Actions        int `json:"actions"`
	FeedbackEvents int `json:"feedback_events"`
	RuntimeEvents  int `json:"runtime_events"`
	Executions     int `json:"executions"`
}

// FeedbackStats summarizes prediction feedback.
type FeedbackStats struct {
	Events   int     `json:"events"`
	Correct  int     `json:"correct"`
	Accuracy float64 `json:"accuracy"`
}

// KernelStats summarizes learned response-kernel coverage.
type KernelStats struct {
	Weights int `json:"weights"`
	Samples int `json:"samples"`
}

// RuntimeEventStats summarizes passive runtime event collection.
type RuntimeEventStats struct {
	Events int            `json:"events"`
	ByType map[string]int `json:"by_type"`
}
