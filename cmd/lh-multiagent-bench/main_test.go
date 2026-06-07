package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testFixture() (map[string]agentSpec, []benchTask) {
	return defaultAgents(), defaultTasks()
}

func TestExpandScenarios(t *testing.T) {
	_, tasks := testFixture()
	got, err := expandScenarios("all", tasks)
	if err != nil {
		t.Fatalf("expandScenarios: %v", err)
	}
	want := []string{"single", "parallel", "pipeline", "debate", "autonomy"}
	if len(got) != len(want) {
		t.Fatalf("expected %d scenarios, got %#v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scenario %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDatasetCoversMultiAgentModes(t *testing.T) {
	_, tasks := testFixture()
	if len(tasks) < 15 {
		t.Fatalf("expected at least 15 tasks, got %d", len(tasks))
	}
	modes := map[string]bool{}
	for _, task := range tasks {
		modes[task.GoldMode] = true
	}
	for _, mode := range []string{"single", "parallel", "pipeline", "debate", "autonomy_queue"} {
		if !modes[mode] {
			t.Fatalf("dataset does not cover mode %q", mode)
		}
	}
}

func TestBaselineOverSplitsAutonomyStatusTrap(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "A5-003")
	cfg := benchConfig{Variant: "baseline", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	result := runStrategy(cfg, task, agents)
	if result.Mode == task.GoldMode {
		t.Fatalf("expected baseline to misroute autonomy status trap, got %s", result.Mode)
	}
	metrics := evaluateTask(cfg, task, result, agents)
	if !metrics.ForbiddenModeHit {
		t.Fatalf("expected forbidden mode hit for baseline trap")
	}
}

func TestCapabilityRoutingImprovesCoverage(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "P2-001")
	baseCfg := benchConfig{Variant: "baseline", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	capCfg := benchConfig{Variant: "capability-routed", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	base := evaluateTask(baseCfg, task, runStrategy(baseCfg, task, agents), agents)
	capability := evaluateTask(capCfg, task, runStrategy(capCfg, task, agents), agents)
	if capability.CapabilityRecall <= base.CapabilityRecall {
		t.Fatalf("expected capability recall to improve, base=%.2f capability=%.2f", base.CapabilityRecall, capability.CapabilityRecall)
	}
	if capability.RoleFit <= base.RoleFit {
		t.Fatalf("expected role fit to improve, base=%.2f capability=%.2f", base.RoleFit, capability.RoleFit)
	}
}

func TestDependencyAwareRemovesPipelineViolation(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "L3-001")
	baseCfg := benchConfig{Variant: "baseline", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	depCfg := benchConfig{Variant: "dependency-aware", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	base := evaluateTask(baseCfg, task, runStrategy(baseCfg, task, agents), agents)
	dep := evaluateTask(depCfg, task, runStrategy(depCfg, task, agents), agents)
	if base.DependencyViolations == 0 {
		t.Fatalf("expected baseline to violate pipeline dependencies")
	}
	if dep.DependencyViolations != 0 {
		t.Fatalf("expected dependency-aware to preserve dependencies, got %d", dep.DependencyViolations)
	}
	if !dep.ModeCorrect {
		t.Fatalf("expected dependency-aware mode to be correct")
	}
}

func TestDebateReviewUsesCritic(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "D4-001")
	cfg := benchConfig{Variant: "debate-review", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	result := runStrategy(cfg, task, agents)
	metrics := evaluateTask(cfg, task, result, agents)
	if result.Mode != "debate" {
		t.Fatalf("expected debate mode, got %s", result.Mode)
	}
	if !result.CriticUsed {
		t.Fatalf("expected critic to be used")
	}
	if metrics.CriticRecall != 1 {
		t.Fatalf("expected critic recall 1, got %.2f", metrics.CriticRecall)
	}
}

func TestRunWritesJSONL(t *testing.T) {
	out := filepath.Join(t.TempDir(), "multiagent-bench.jsonl")
	cfg := benchConfig{
		Variant:          "baseline",
		Scenario:         "single",
		OutPath:          out,
		Rounds:           1,
		SuccessThreshold: 0.70,
		MaxCoordOverhead: 0.30,
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected JSONL output")
	}
}

func TestLoadCompareSummaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	data := `{"type":"record","variant":"baseline","scenario":"single"}` + "\n" +
		`{"type":"summary","variant":"baseline","scenario":"all","success_rate":0.5,"clean":false}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadCompareSummaries([]string{path})
	if err != nil {
		t.Fatalf("loadCompareSummaries: %v", err)
	}
	if len(got) != 1 || got[0].Variant != "baseline" {
		t.Fatalf("unexpected summaries: %#v", got)
	}
}

func findTask(t *testing.T, tasks []benchTask, id string) benchTask {
	t.Helper()
	for _, task := range tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %s not found", id)
	return benchTask{}
}
