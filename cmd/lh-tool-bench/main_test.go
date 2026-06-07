package main

import (
	"os"
	"path/filepath"
	"testing"
)

func testFixture() (map[string]toolSpec, map[string]operationSpec, []benchTask) {
	tools := defaultToolCatalog()
	ops := defaultOperationCatalog()
	tasks := normalizeTasks(defaultTasks(), ops)
	return tools, ops, tasks
}

func TestExpandScenarios(t *testing.T) {
	_, _, tasks := testFixture()
	got, err := expandScenarios("all", tasks)
	if err != nil {
		t.Fatalf("expandScenarios: %v", err)
	}
	want := []string{"no_tool", "read_only", "single_tool", "multi_tool", "risk", "trap"}
	if len(got) != len(want) {
		t.Fatalf("expected %d scenarios, got %#v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scenario %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestExpandedDatasetCoversRealToolDomains(t *testing.T) {
	_, ops, tasks := testFixture()
	if len(tasks) < 60 {
		t.Fatalf("expected expanded dataset to have at least 60 tasks, got %d", len(tasks))
	}
	required := map[string]bool{
		"rag_search":       false,
		"rag_index":        false,
		"image_analyze":    false,
		"image_generate":   false,
		"text_to_speech":   false,
		"sql_query":        false,
		"db_schema":        false,
		"cron_add":         false,
		"cron_list":        false,
		"autonomy_status":  false,
		"delegate_task":    false,
		"heartbeat_status": false,
	}
	for _, task := range tasks {
		for _, opName := range task.RequiredOperations {
			if op, ok := ops[opName]; ok {
				if _, tracked := required[op.Tool]; tracked {
					required[op.Tool] = true
				}
			}
		}
	}
	for name, seen := range required {
		if !seen {
			t.Fatalf("expanded dataset does not cover required tool %q", name)
		}
	}
}

func TestBaselineShowsNoToolMisfire(t *testing.T) {
	tools, ops, tasks := testFixture()
	cfg := benchConfig{Variant: "baseline", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}
	var task benchTask
	for _, candidate := range tasks {
		if candidate.ID == "T1-001" {
			task = candidate
			break
		}
	}
	result := runStrategy(cfg, task, tools, ops)
	if result.NeedToolProb < cfg.NeedThreshold {
		t.Fatalf("expected baseline to over-trigger tools for formula task, got %.2f", result.NeedToolProb)
	}
	metrics := evaluateTask(cfg, task, result, tools, ops)
	if metrics.NeedPredictionCorrect {
		t.Fatalf("expected no-tool baseline need prediction miss")
	}
}

func TestRiskAwareRunsExpandedSpecializedTools(t *testing.T) {
	tools, ops, tasks := testFixture()
	cfg := benchConfig{Variant: "risk-aware", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}
	for _, id := range []string{"T3-006", "T3-007", "T3-008", "T3-009", "T4-011", "T5-006", "T5-007", "T5-009"} {
		task := findTask(t, tasks, id)
		metrics := evaluateTask(cfg, task, runStrategy(cfg, task, tools, ops), tools, ops)
		if !metrics.NeedPredictionCorrect {
			t.Fatalf("%s need prediction miss", id)
		}
		if metrics.OperationRecall < 1 {
			t.Fatalf("%s operation recall = %.2f, want 1", id, metrics.OperationRecall)
		}
		if metrics.ForbiddenCallCount != 0 {
			t.Fatalf("%s unexpected forbidden calls: %d", id, metrics.ForbiddenCallCount)
		}
	}
}

func TestRiskAwareSuppressesCronMutationNegation(t *testing.T) {
	tools, ops, tasks := testFixture()
	task := findTask(t, tasks, "T5-005")
	cfg := benchConfig{Variant: "risk-aware", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}
	metrics := evaluateTask(cfg, task, runStrategy(cfg, task, tools, ops), tools, ops)
	if metrics.ForbiddenCallCount != 0 {
		t.Fatalf("expected cron mutation negation to block forbidden calls, got %d", metrics.ForbiddenCallCount)
	}
	if metrics.RouteRisk != 0 {
		t.Fatalf("expected no realized route risk, got %.2f", metrics.RouteRisk)
	}
}

func TestRiskAwareSuppressesNegatedPush(t *testing.T) {
	tools, ops, tasks := testFixture()
	task := findTask(t, tasks, "T5-001")
	baseCfg := benchConfig{Variant: "baseline", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}
	riskCfg := benchConfig{Variant: "risk-aware", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}

	base := evaluateTask(baseCfg, task, runStrategy(baseCfg, task, tools, ops), tools, ops)
	riskAware := evaluateTask(riskCfg, task, runStrategy(riskCfg, task, tools, ops), tools, ops)
	if base.ForbiddenCallCount == 0 {
		t.Fatalf("expected baseline to call forbidden push")
	}
	if riskAware.ForbiddenCallCount != 0 {
		t.Fatalf("expected risk-aware to avoid forbidden push, got %d", riskAware.ForbiddenCallCount)
	}
	if riskAware.RouteRisk >= base.RouteRisk {
		t.Fatalf("expected risk-aware route risk to drop, base=%v risk=%v", base.RouteRisk, riskAware.RouteRisk)
	}
}

func TestPackedResultsReduceNoise(t *testing.T) {
	tools, ops, tasks := testFixture()
	task := findTask(t, tasks, "T4-001")
	riskCfg := benchConfig{Variant: "risk-aware", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}
	packedCfg := benchConfig{Variant: "packed-results", NeedThreshold: 0.60, SuccessThreshold: 0.70, MaxRedundantRate: 0.25}
	riskAware := evaluateTask(riskCfg, task, runStrategy(riskCfg, task, tools, ops), tools, ops)
	packed := evaluateTask(packedCfg, task, runStrategy(packedCfg, task, tools, ops), tools, ops)
	if packed.ToolResultNoise >= riskAware.ToolResultNoise {
		t.Fatalf("expected packed result noise to drop, risk=%v packed=%v", riskAware.ToolResultNoise, packed.ToolResultNoise)
	}
	if packed.ToolResultTokens >= riskAware.ToolResultTokens {
		t.Fatalf("expected packed result tokens to drop, risk=%d packed=%d", riskAware.ToolResultTokens, packed.ToolResultTokens)
	}
}

func TestRunWritesJSONL(t *testing.T) {
	out := filepath.Join(t.TempDir(), "tool-bench.jsonl")
	cfg := benchConfig{
		Variant:          "baseline",
		Scenario:         "single_tool",
		OutPath:          out,
		Rounds:           1,
		NeedThreshold:    0.60,
		SuccessThreshold: 0.70,
		MaxRedundantRate: 0.25,
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestLoadCompareSummaries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "results.jsonl")
	data := `{"type":"record","variant":"baseline","scenario":"no_tool"}` + "\n" +
		`{"type":"summary","variant":"baseline","scenario":"all","success_rate":0.5,"clean":false}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadCompareSummaries([]string{path})
	if err != nil {
		t.Fatalf("loadCompareSummaries: %v", err)
	}
	if len(got) != 1 || got[0].Variant != "baseline" || got[0].SuccessRate != 0.5 {
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
