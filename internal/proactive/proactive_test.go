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
	execution := ActionExecution{ID: "exec-1", ActionID: "action-1", StateID: "state-1", Action: "warm_memory_context", Status: ActionStatusSuccess, Reason: "test", Metadata: map[string]string{"path": "memory"}, CreatedAt: now}

	if err := store.RecordSignals([]Signal{signal}); err != nil {
		t.Fatalf("RecordSignals: %v", err)
	}
	if err := store.RecordEstimate(estimate); err != nil {
		t.Fatalf("RecordEstimate: %v", err)
	}
	if err := store.RecordEstimateSignals(estimate.ID, []Signal{signal}); err != nil {
		t.Fatalf("RecordEstimateSignals: %v", err)
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
	if err := store.RecordActionExecutions([]ActionExecution{execution}); err != nil {
		t.Fatalf("RecordActionExecutions: %v", err)
	}

	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Signals != 1 || stats.Estimates != 1 || stats.Actions != 1 || stats.FeedbackEvents != 1 || stats.RuntimeEvents != 1 || stats.Executions != 1 {
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
	recentExecutions, err := store.RecentActionExecutions(10)
	if err != nil {
		t.Fatalf("RecentActionExecutions: %v", err)
	}
	if len(recentExecutions) != 1 || recentExecutions[0].Action != "warm_memory_context" {
		t.Fatalf("unexpected recent executions: %#v", recentExecutions)
	}
	latestExecution, ok, err := store.LatestSuccessfulActionExecution("warm_memory_context")
	if err != nil {
		t.Fatalf("LatestSuccessfulActionExecution: %v", err)
	}
	if !ok || latestExecution.ID != "exec-1" {
		t.Fatalf("unexpected latest execution: ok=%t execution=%#v", ok, latestExecution)
	}
	linkedSignals, err := store.SignalsForEstimate("state-1")
	if err != nil {
		t.Fatalf("SignalsForEstimate: %v", err)
	}
	if len(linkedSignals) != 1 || linkedSignals[0].ID != "sig-1" {
		t.Fatalf("unexpected linked signals: %#v", linkedSignals)
	}
	updates, err := store.LearnFromFeedback(feedback, KernelLearningConfig{Enabled: true, LearningRate: 0.5})
	if err != nil {
		t.Fatalf("LearnFromFeedback: %v", err)
	}
	if updates != 1 {
		t.Fatalf("expected one kernel update, got %d", updates)
	}
	kernelStats, err := store.KernelStats()
	if err != nil {
		t.Fatalf("KernelStats: %v", err)
	}
	if kernelStats.Weights != 1 || kernelStats.Samples != 1 {
		t.Fatalf("unexpected kernel stats: %#v", kernelStats)
	}
	weights, err := store.SignalWeightsForState("coding")
	if err != nil {
		t.Fatalf("SignalWeightsForState: %v", err)
	}
	weight := weights[signalWeightKey("time_of_day", "afternoon_work")]
	if weight.Weight <= 0 {
		t.Fatalf("expected positive learned weight, got %#v", weight)
	}
	recentWeights, err := store.RecentSignalWeights(10)
	if err != nil {
		t.Fatalf("RecentSignalWeights: %v", err)
	}
	if len(recentWeights) != 1 || recentWeights[0].Channel != "time_of_day" {
		t.Fatalf("unexpected recent weights: %#v", recentWeights)
	}
}

func TestActionExecutorDryRunAndSafeExecution(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "recent.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "memory"), 0o755); err != nil {
		t.Fatalf("mkdir memory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(home, "sessions"), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	decision := Decision{
		Enabled: true,
		DryRun:  true,
		Actions: []DryRunAction{{
			ID:      "action-1",
			StateID: "state-1",
			Action:  "preload_recent_project_context",
			Allowed: true,
		}},
	}

	dryExecutor := NewActionExecutor(ActionPolicy{Enabled: true, DryRun: true, WorkspaceDir: workspace, HomeDir: home})
	dryExecutions, err := dryExecutor.Execute(context.Background(), decision)
	if err != nil {
		t.Fatalf("Execute dry-run: %v", err)
	}
	if len(dryExecutions) != 1 || dryExecutions[0].Status != ActionStatusDryRun {
		t.Fatalf("expected dry-run execution, got %#v", dryExecutions)
	}

	decision.DryRun = false
	executor := NewActionExecutor(ActionPolicy{Enabled: true, DryRun: false, WorkspaceDir: workspace, HomeDir: home})
	executions, err := executor.Execute(context.Background(), decision)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(executions) != 1 || executions[0].Status != ActionStatusSuccess {
		t.Fatalf("expected successful execution, got %#v", executions)
	}
	if executions[0].Metadata["file_count"] != "1" {
		t.Fatalf("expected file_count metadata, got %#v", executions[0].Metadata)
	}

	decision.Actions[0].Action = "prepare_meeting_notes"
	blocked, err := executor.Execute(context.Background(), decision)
	if err != nil {
		t.Fatalf("Execute blocked: %v", err)
	}
	if len(blocked) != 1 || blocked[0].Status != ActionStatusBlocked {
		t.Fatalf("expected blocked action, got %#v", blocked)
	}
}

func TestActionExecutorSkipsDuringCooldown(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "proactive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	if err := store.RecordActionExecutions([]ActionExecution{{
		ID:        "exec-1",
		ActionID:  "action-old",
		StateID:   "state-old",
		Action:    "prefer_lightweight_tasks",
		Status:    ActionStatusSuccess,
		Reason:    "test",
		Metadata:  map[string]string{},
		CreatedAt: now.Add(-time.Minute),
	}}); err != nil {
		t.Fatalf("RecordActionExecutions: %v", err)
	}

	executor := NewActionExecutor(ActionPolicy{
		Enabled:        true,
		DryRun:         false,
		Cooldown:       5 * time.Minute,
		ExecutionStore: store,
	})
	executor.Now = func() time.Time { return now }
	executions, err := executor.Execute(context.Background(), Decision{
		Enabled: true,
		DryRun:  false,
		Actions: []DryRunAction{{
			ID:      "action-1",
			StateID: "state-1",
			Action:  "prefer_lightweight_tasks",
			Allowed: true,
		}},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(executions) != 1 || executions[0].Status != ActionStatusSkipped {
		t.Fatalf("expected cooldown skip, got %#v", executions)
	}
	if !strings.Contains(executions[0].Reason, "cooldown") {
		t.Fatalf("expected cooldown reason, got %q", executions[0].Reason)
	}
}

func TestRuntimeServiceRunOncePersistsExecutions(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "proactive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	runner := NewRunnerWithCalibrator(
		staticSampler{signals: []Signal{
			{ID: "sig-1", Channel: "time_of_day", Label: "afternoon_work", Value: 1, CreatedAt: now},
			{ID: "sig-2", Channel: "workspace_context", Label: "go_repo", Value: 1, CreatedAt: now},
		}},
		NewEstimator(),
		nil,
		NewGate(Config{Enabled: true, DryRun: true, ConfidenceThreshold: 0.6, Horizon: 5 * time.Minute}),
		store,
	)
	executor := NewActionExecutor(ActionPolicy{Enabled: true, DryRun: true})
	service := NewRuntimeService(RuntimeServiceOptions{Runner: runner, Executor: executor, Store: store})

	decision, executions, err := service.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if decision.Estimate.PredictedState != "coding" {
		t.Fatalf("expected coding decision, got %#v", decision.Estimate)
	}
	if len(executions) == 0 || executions[0].Status != ActionStatusDryRun {
		t.Fatalf("expected dry-run executions, got %#v", executions)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Executions != len(executions) {
		t.Fatalf("expected persisted executions, stats=%#v executions=%#v", stats, executions)
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
	got := NewFeedbackCalibrator(store).Calibrate(estimate, nil)
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

func TestFeedbackCalibratorUsesLearnedKernel(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "proactive.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC)
	signal := Signal{ID: "sig-1", Channel: "workspace_context", Label: "go_repo", Value: 1, CreatedAt: now}
	estimate := StateEstimate{ID: "state-1", PredictedState: "coding", Confidence: 0.60, NoiseVariance: 0.40, Horizon: 5 * time.Minute, CreatedAt: now}
	if err := store.RecordSignals([]Signal{signal}); err != nil {
		t.Fatalf("RecordSignals: %v", err)
	}
	if err := store.RecordEstimate(estimate); err != nil {
		t.Fatalf("RecordEstimate: %v", err)
	}
	if err := store.RecordEstimateSignals(estimate.ID, []Signal{signal}); err != nil {
		t.Fatalf("RecordEstimateSignals: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := store.LearnFromFeedback(FeedbackEvent{
			StateID:        estimate.ID,
			PredictedState: "coding",
			ActualState:    "coding",
			CreatedAt:      now.Add(time.Duration(i) * time.Minute),
		}, KernelLearningConfig{Enabled: true, LearningRate: 0.5}); err != nil {
			t.Fatalf("LearnFromFeedback: %v", err)
		}
	}

	got := NewFeedbackCalibrator(store).Calibrate(estimate, []Signal{signal})
	if got.Confidence <= estimate.Confidence {
		t.Fatalf("expected learned kernel to raise confidence, before %.2f after %.2f", estimate.Confidence, got.Confidence)
	}
	if !strings.Contains(strings.Join(got.Reasons, "\n"), "learned response kernel") {
		t.Fatalf("expected learned kernel reason, got %#v", got.Reasons)
	}
}

type staticSampler struct {
	signals []Signal
}

func (s staticSampler) Sample(context.Context) ([]Signal, error) {
	return s.signals, nil
}
