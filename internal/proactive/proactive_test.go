package proactive

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSamplerDetectsGoRepoWorkspace(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	sampler := NewSampler(dir)
	sampler.Now = func() time.Time { return time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC) }

	signals, err := sampler.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	found := false
	for _, signal := range signals {
		if signal.Channel == "workspace_context" && signal.Label == "go_repo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected go_repo workspace signal, got %#v", signals)
	}
}

func TestSamplerIncludesRuntimeActivitySignals(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "proactive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := store.RecordRuntimeEvent(RuntimeEvent{ID: "runtime-1", Type: "tool_call", Name: "file_read", CreatedAt: now.Add(-5 * time.Minute)}); err != nil {
		t.Fatalf("RecordRuntimeEvent: %v", err)
	}
	if err := store.RecordRuntimeEvent(RuntimeEvent{ID: "runtime-2", Type: "chat_turn", Name: "chat", CreatedAt: now.Add(-10 * time.Minute)}); err != nil {
		t.Fatalf("RecordRuntimeEvent: %v", err)
	}

	sampler := NewSamplerWithStore(t.TempDir(), store)
	sampler.Now = func() time.Time { return now }
	signals, err := sampler.Sample(context.Background())
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}
	foundTool := false
	foundChat := false
	for _, signal := range signals {
		if signal.Channel == "runtime_tool_activity" && signal.Value == 1 {
			foundTool = true
		}
		if signal.Channel == "runtime_chat_activity" && signal.Value == 1 {
			foundChat = true
		}
	}
	if !foundTool || !foundChat {
		t.Fatalf("expected runtime activity signals, got %#v", signals)
	}
}

func TestEstimatorPredictsCodingForRepoDuringWorkHours(t *testing.T) {
	now := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	estimate := NewEstimator().Estimate([]Signal{
		{Channel: "time_of_day", Label: "afternoon_work", CreatedAt: now},
		{Channel: "workspace_context", Label: "go_repo", Value: 1, CreatedAt: now},
	}, 5*time.Minute)

	if estimate.PredictedState != "coding" {
		t.Fatalf("expected coding, got %s", estimate.PredictedState)
	}
	if estimate.Confidence < 0.60 {
		t.Fatalf("expected confidence >= 0.60, got %.2f", estimate.Confidence)
	}
	if estimate.NoiseVariance <= 0 || estimate.NoiseVariance > 1 {
		t.Fatalf("unexpected noise variance %.2f", estimate.NoiseVariance)
	}
}

func TestGateDryRunProducesNonExecutingActions(t *testing.T) {
	gate := NewGate(Config{Enabled: false, DryRun: true, ConfidenceThreshold: 0.60})
	estimate := StateEstimate{
		ID:             "state-1",
		PredictedState: "coding",
		Confidence:     0.80,
		CreatedAt:      time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC),
	}

	decision, err := gate.Decide(context.Background(), nil, estimate)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Enabled {
		t.Fatalf("expected disabled decision metadata")
	}
	if len(decision.Actions) != 2 {
		t.Fatalf("expected two coding actions, got %d", len(decision.Actions))
	}
	if !decision.Actions[0].Allowed {
		t.Fatalf("expected confidence gate to allow dry-run action")
	}
}

func TestStorePersistsDryRunCycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proactive.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	signal := Signal{ID: "sig-1", Channel: "time_of_day", Value: 0.5, Label: "afternoon_work", CreatedAt: now}
	estimate := StateEstimate{ID: "state-1", PredictedState: "coding", Confidence: 0.75, NoiseVariance: 0.25, Horizon: 5 * time.Minute, Reasons: []string{"test"}, CreatedAt: now}
	action := DryRunAction{ID: "action-1", StateID: "state-1", Action: "warm_memory_context", Confidence: 0.75, Allowed: true, Reason: "test", CreatedAt: now}
	feedback := FeedbackEvent{ID: "feedback-1", StateID: "state-1", PredictedState: "coding", ActualState: "coding", Source: "test", CreatedAt: now}
	runtimeEvent := RuntimeEvent{ID: "runtime-1", Source: "test", SessionID: "sess-1", Type: "tool_call", Name: "file_read", Value: 12, Metadata: map[string]string{"success": "true"}, CreatedAt: now}

	if err := store.RecordSignals([]Signal{signal}); err != nil {
		t.Fatalf("RecordSignals: %v", err)
	}
	if err := store.RecordEstimate(estimate); err != nil {
		t.Fatalf("RecordEstimate: %v", err)
	}
	if err := store.RecordActions([]DryRunAction{action}); err != nil {
		t.Fatalf("RecordActions: %v", err)
	}
	if err := store.RecordFeedback(feedback); err != nil {
		t.Fatalf("RecordFeedback: %v", err)
	}
	if err := store.RecordRuntimeEvent(runtimeEvent); err != nil {
		t.Fatalf("RecordRuntimeEvent: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Signals != 1 || stats.Estimates != 1 || stats.Actions != 1 || stats.FeedbackEvents != 1 || stats.RuntimeEvents != 1 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
	got, ok, err := store.LatestEstimate()
	if err != nil {
		t.Fatalf("LatestEstimate: %v", err)
	}
	if !ok || got.PredictedState != "coding" || got.Horizon != 5*time.Minute {
		t.Fatalf("unexpected latest estimate: ok=%t estimate=%#v", ok, got)
	}
	byID, ok, err := store.EstimateByID("state-1")
	if err != nil {
		t.Fatalf("EstimateByID: %v", err)
	}
	if !ok || byID.ID != "state-1" {
		t.Fatalf("unexpected estimate by id: ok=%t estimate=%#v", ok, byID)
	}
	feedbackStats, err := store.FeedbackStats(100)
	if err != nil {
		t.Fatalf("FeedbackStats: %v", err)
	}
	if feedbackStats.Events != 1 || feedbackStats.Correct != 1 || feedbackStats.Accuracy != 1 {
		t.Fatalf("unexpected feedback stats: %#v", feedbackStats)
	}
	runtimeStats, err := store.RuntimeEventStats()
	if err != nil {
		t.Fatalf("RuntimeEventStats: %v", err)
	}
	if runtimeStats.Events != 1 || runtimeStats.ByType["tool_call"] != 1 {
		t.Fatalf("unexpected runtime event stats: %#v", runtimeStats)
	}
	counts, err := store.RuntimeEventCountsSince(now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("RuntimeEventCountsSince: %v", err)
	}
	if counts["tool_call"] != 1 {
		t.Fatalf("unexpected runtime event counts: %#v", counts)
	}
	recent, err := store.RecentRuntimeEvents(10)
	if err != nil {
		t.Fatalf("RecentRuntimeEvents: %v", err)
	}
	if len(recent) != 1 || recent[0].Name != "file_read" || recent[0].Metadata["success"] != "true" {
		t.Fatalf("unexpected recent events: %#v", recent)
	}
}

func TestFeedbackCalibratorAdjustsConfidenceFromRecentFeedback(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "proactive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := store.RecordFeedback(FeedbackEvent{
			ID:             signalID("feedback", now.Add(time.Duration(i)*time.Second), i),
			StateID:        "state-1",
			PredictedState: "coding",
			ActualState:    "browsing",
			Source:         "test",
			CreatedAt:      now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("RecordFeedback: %v", err)
		}
	}

	estimate := StateEstimate{PredictedState: "coding", Confidence: 0.80, NoiseVariance: 0.20, Reasons: []string{"base"}}
	got := NewFeedbackCalibrator(store).Calibrate(estimate)
	if got.Confidence >= estimate.Confidence {
		t.Fatalf("expected confidence to decrease, before %.2f after %.2f", estimate.Confidence, got.Confidence)
	}
	if got.NoiseVariance <= estimate.NoiseVariance {
		t.Fatalf("expected noise variance to increase, before %.2f after %.2f", estimate.NoiseVariance, got.NoiseVariance)
	}
	if !strings.Contains(strings.Join(got.Reasons, "\n"), "feedback calibration") {
		t.Fatalf("expected feedback calibration reason, got %#v", got.Reasons)
	}
}
