package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
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
	want := []string{"single", "parallel", "pipeline", "debate", "autonomy", "heavy"}
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
	if len(tasks) != 60 {
		t.Fatalf("expected synthetic suite to contain 60 tasks, got %d", len(tasks))
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

func TestDatasetIncludesHeavyHermesCases(t *testing.T) {
	_, tasks := testFixture()
	heavy := tasksForScenario(tasks, "heavy")
	if len(heavy) < 5 {
		t.Fatalf("expected at least 5 heavy tasks, got %d", len(heavy))
	}
	task := findTask(t, tasks, "H6-001")
	if task.GoldMode != "pipeline" {
		t.Fatalf("Hermes reproduction should be pipeline, got %s", task.GoldMode)
	}
	if !task.NeedsCritic {
		t.Fatalf("Hermes reproduction should require critic/verifier path")
	}
	if len(task.Subtasks) < 8 {
		t.Fatalf("Hermes reproduction should be super-heavy, got %d subtasks", len(task.Subtasks))
	}
	modes := map[string]bool{}
	for _, task := range heavy {
		modes[task.GoldMode] = true
	}
	for _, mode := range []string{"parallel", "pipeline", "debate", "autonomy_queue"} {
		if !modes[mode] {
			t.Fatalf("heavy dataset does not cover mode %q", mode)
		}
	}
}

func TestHeavyHermesBaselineExposesCoordinationFailures(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "H6-001")
	baseCfg := benchConfig{Variant: "baseline", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	depCfg := benchConfig{Variant: "dependency-aware", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	base := evaluateTask(baseCfg, task, runStrategy(baseCfg, task, agents), agents)
	dep := evaluateTask(depCfg, task, runStrategy(depCfg, task, agents), agents)
	if base.ModeCorrect {
		t.Fatalf("expected baseline to misroute super-heavy Hermes reproduction")
	}
	if base.SubtaskRecall >= dep.SubtaskRecall {
		t.Fatalf("expected dependency-aware to cover more Hermes subtasks, base=%.2f dep=%.2f", base.SubtaskRecall, dep.SubtaskRecall)
	}
	if dep.DependencyViolations != 0 {
		t.Fatalf("expected dependency-aware to preserve Hermes dependencies, got %d", dep.DependencyViolations)
	}
	if dep.CapabilityRecall <= base.CapabilityRecall {
		t.Fatalf("expected dependency-aware capability recall to improve, base=%.2f dep=%.2f", base.CapabilityRecall, dep.CapabilityRecall)
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

func TestMathFullPlannerHandlesHeavyHermesReproduction(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "H6-001")
	cfg := benchConfig{Variant: "math-full-v1", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	result := runStrategy(cfg, task, agents)
	if result.Mode != "pipeline" {
		t.Fatalf("expected math-full-v1 to choose pipeline, got %s", result.Mode)
	}
	if result.Diagnostics == nil {
		t.Fatalf("expected math diagnostics")
	}
	if result.Diagnostics.ExpectedUtility <= 0 {
		t.Fatalf("expected positive utility, got %.2f", result.Diagnostics.ExpectedUtility)
	}
	if result.Diagnostics.TraceID == "" {
		t.Fatalf("expected stable trace id")
	}
	if result.Diagnostics.EdgeNLL <= 0 {
		t.Fatalf("expected positive EdgeNLL, got %.2f", result.Diagnostics.EdgeNLL)
	}
	if result.Diagnostics.CalibrationECE < 0 {
		t.Fatalf("expected non-negative CalibrationECE, got %.2f", result.Diagnostics.CalibrationECE)
	}
	if result.Diagnostics.LyapunovDecrease <= 0 {
		t.Fatalf("expected Lyapunov decrease, got %.2f", result.Diagnostics.LyapunovDecrease)
	}
	if result.Diagnostics.LyapunovDecreaseRate <= 0 {
		t.Fatalf("expected Lyapunov decrease rate, got %.2f", result.Diagnostics.LyapunovDecreaseRate)
	}
	if !result.Diagnostics.VerifierRequired || !result.Diagnostics.VerifierAvailable {
		t.Fatalf("expected verifier to be required and available: %#v", result.Diagnostics)
	}
	if len(result.Diagnostics.Trace) < len(task.Subtasks) {
		t.Fatalf("expected trace to cover subtasks, trace=%d subtasks=%d", len(result.Diagnostics.Trace), len(task.Subtasks))
	}
	metrics := evaluateTask(cfg, task, result, agents)
	if !metrics.Clean {
		t.Fatalf("expected math-full heavy Hermes result to be clean")
	}
}

func TestMathPlannerRejectsForbiddenAutonomyTrap(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "A5-003")
	cfg := benchConfig{Variant: "math-full-v1", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	result := runStrategy(cfg, task, agents)
	if result.Mode != "single" {
		t.Fatalf("expected math-full-v1 to keep autonomy trap single, got %s", result.Mode)
	}
	if result.Diagnostics == nil {
		t.Fatalf("expected math diagnostics")
	}
	for _, candidate := range result.Diagnostics.CandidateScores {
		if candidate.Mode == "autonomy_queue" && !candidate.Rejected {
			t.Fatalf("expected autonomy_queue candidate to be rejected")
		}
	}
}

func TestMathPlannerHandlesHeavySingleGuard(t *testing.T) {
	agents, tasks := testFixture()
	task := findTask(t, tasks, "H6-011")
	cfg := benchConfig{Variant: "math-full-v1", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	result := runStrategy(cfg, task, agents)
	if result.Mode != "single" {
		t.Fatalf("expected math-full-v1 to preserve heavy single guard, got %s", result.Mode)
	}
	if result.Diagnostics == nil {
		t.Fatalf("expected math diagnostics")
	}
	if result.Diagnostics.ReplanCount == 0 {
		t.Fatalf("expected rejected oversplit candidates for heavy single guard")
	}
}

func TestMathSummaryIncludesDiagnostics(t *testing.T) {
	agents, tasks := testFixture()
	cfg := benchConfig{Variant: "math-full-v1", SuccessThreshold: 0.70, MaxCoordOverhead: 0.30}
	records := []benchRecord{
		runTask(cfg, 1, findTask(t, tasks, "H6-001"), agents),
		runTask(cfg, 1, findTask(t, tasks, "A5-003"), agents),
	}
	summary := summarizeRecords(cfg, "mixed", records)
	if summary.AvgExpectedUtility == 0 {
		t.Fatalf("expected avg expected utility in summary")
	}
	if summary.AvgEdgeNLL <= 0 {
		t.Fatalf("expected avg EdgeNLL in summary")
	}
	if summary.CalibrationECE <= 0 {
		t.Fatalf("expected calibration ECE in summary")
	}
	if summary.AvgLyapunovDecrease <= 0 {
		t.Fatalf("expected positive Lyapunov decrease in summary")
	}
	if summary.LyapunovDecreaseRate <= 0 {
		t.Fatalf("expected Lyapunov decrease rate in summary")
	}
	if summary.VerifierRequiredCount == 0 {
		t.Fatalf("expected verifier required count")
	}
	if summary.VerifierCatchRate <= 0 {
		t.Fatalf("expected verifier catch rate")
	}
	if summary.AvgPathRegret < 0 {
		t.Fatalf("expected non-negative path regret")
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

func TestRunWritesMathDiagnosticsJSONL(t *testing.T) {
	out := filepath.Join(t.TempDir(), "multiagent-bench-math.jsonl")
	cfg := benchConfig{
		Variant:          "math-full-v1",
		Scenario:         "heavy",
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
	if !strings.Contains(string(data), `"trace_id"`) {
		t.Fatalf("expected JSONL diagnostics to include trace_id")
	}
	var sawRecord bool
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var record benchRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		if record.Type != "record" {
			continue
		}
		sawRecord = true
		if record.Diagnostics == nil {
			t.Fatalf("expected diagnostics on math record")
		}
		if record.Diagnostics.TraceID == "" || len(record.Diagnostics.Trace) == 0 {
			t.Fatalf("expected trace id and trace edges")
		}
		if record.Diagnostics.EdgeNLL <= 0 {
			t.Fatalf("expected positive EdgeNLL")
		}
	}
	if !sawRecord {
		t.Fatalf("expected at least one math record")
	}
}

func TestLoadReplayTasksFromJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replay.jsonl")
	data := `{"id":"R7-001","prompt":"只查看多 agent benchmark 状态，不要拆子代理。","should_split":false}` + "\n" +
		`{"id":"R7-002","prompt":"先抽取历史会话，再标注 mode，再跑 replay benchmark。","gold_mode":"pipeline","needs_verifier":true}` + "\n" +
		`{"id":"R7-003","messages":[{"role":"user","content":"把 Hermes replay 放到后台队列，完成后通知 verifier。"}],"allows_background":true}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	tasks, err := loadReplayTasks(path)
	if err != nil {
		t.Fatalf("loadReplayTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 replay tasks, got %d", len(tasks))
	}
	if tasks[0].GoldMode != "single" {
		t.Fatalf("expected first replay task to be single, got %s", tasks[0].GoldMode)
	}
	if tasks[1].GoldMode != "pipeline" || !tasks[1].NeedsCritic {
		t.Fatalf("expected second replay task to be verifier pipeline: %#v", tasks[1])
	}
	if tasks[2].GoldMode != "autonomy_queue" || !tasks[2].AllowsBackground {
		t.Fatalf("expected third replay task to be background: %#v", tasks[2])
	}
}

func TestRunReplayOnlyWritesSummary(t *testing.T) {
	dir := t.TempDir()
	replayPath := filepath.Join(dir, "replay.json")
	out := filepath.Join(dir, "results.jsonl")
	data := `{
  "cases": [
    {
      "id": "R7-010",
      "scenario": "historical replay",
      "prompt": "复现 Hermes agent 的历史任务：先分析，再实现，再验证。",
      "gold_mode": "pipeline",
      "needs_verifier": true
    },
    {
      "id": "R7-011",
      "scenario": "historical replay",
      "prompt": "让安全和性能代理辩论是否开启自动子代理。",
      "gold_mode": "debate",
      "needs_critic": true
    }
  ]
}`
	if err := os.WriteFile(replayPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := benchConfig{
		Variant:          "math-full-v1",
		Scenario:         "historical_replay",
		ReplayPath:       replayPath,
		ReplayOnly:       true,
		OutPath:          out,
		Rounds:           1,
		SuccessThreshold: 0.70,
		MaxCoordOverhead: 0.30,
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run replay: %v", err)
	}
	dataOut, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read replay output: %v", err)
	}
	var summary benchSummary
	for _, line := range strings.Split(strings.TrimSpace(string(dataOut)), "\n") {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("decode probe: %v", err)
		}
		if probe.Type != "summary" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			t.Fatalf("decode summary: %v", err)
		}
	}
	if summary.Records != 2 {
		t.Fatalf("expected replay summary with 2 records, got %#v", summary)
	}
	if summary.VerifierNeedAccuracy <= 0 {
		t.Fatalf("expected verifier need accuracy")
	}
	if summary.OODFalseNegativeRate != 0 {
		t.Fatalf("expected OOD false negative rate 0, got %.2f", summary.OODFalseNegativeRate)
	}
}

func TestReplayLabelExportRoundTrips(t *testing.T) {
	dir := t.TempDir()
	replayPath := filepath.Join(dir, "replay.jsonl")
	labelsPath := filepath.Join(dir, "labels.jsonl")
	data := `{"id":"R7-020","prompt":"先抽取历史会话，再训练边模型，再验证。","needs_verifier":true}` + "\n" +
		`{"id":"R7-021","prompt":"只查看 session 状态，不要拆子代理。","should_split":false}` + "\n"
	if err := os.WriteFile(replayPath, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := benchConfig{ReplayPath: replayPath, ReplayLabelOut: labelsPath}
	if err := runReplayLabelExport(cfg); err != nil {
		t.Fatalf("runReplayLabelExport: %v", err)
	}
	labelData, err := os.ReadFile(labelsPath)
	if err != nil {
		t.Fatalf("read labels: %v", err)
	}
	if !strings.Contains(string(labelData), `"type":"replay_label"`) {
		t.Fatalf("expected replay label records, got %s", string(labelData))
	}
	tasks, err := loadReplayTasks(labelsPath)
	if err != nil {
		t.Fatalf("load labels as replay tasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks from label export, got %d", len(tasks))
	}
	if tasks[0].GoldMode != "pipeline" || !tasks[0].NeedsCritic {
		t.Fatalf("expected exported first label to be pipeline with verifier: %#v", tasks[0])
	}
	if tasks[1].GoldMode != "single" {
		t.Fatalf("expected exported second label to be single: %#v", tasks[1])
	}
}

func TestLoadReplayTasksFromSessionMarkdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.md")
	data := `# LuckyAgent Session

` + "```json" + `
{
  "id": "sess-001",
  "title": "Hermes replay session",
  "messages": [
    {"role": "user", "content": "先分析 Hermes planner，再实现 bridge，最后跑 verifier。"},
    {"role": "assistant", "content": "ok"}
  ]
}
` + "```" + `
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	tasks, err := loadReplayTasks(path)
	if err != nil {
		t.Fatalf("loadReplayTasks session md: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one task, got %d", len(tasks))
	}
	if tasks[0].ID != "sess-001" {
		t.Fatalf("expected session id, got %s", tasks[0].ID)
	}
	if tasks[0].GoldMode != "pipeline" {
		t.Fatalf("expected pipeline from session prompt, got %s", tasks[0].GoldMode)
	}
	if tasks[0].TaskType != "replay_hermes" {
		t.Fatalf("expected Hermes replay task type, got %s", tasks[0].TaskType)
	}
}

func TestLoadReplayTasksFromSessionMarkdownWithNestedFenceText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.md")
	body, err := json.Marshal(replaySessionData{
		ID:    "sess-fence",
		Title: "nested fence",
		Messages: []provider.Message{
			{Role: "user", Content: "先检查 benchmark，再修复。"},
			{Role: "assistant", Content: "example ```json inside string should not close the outer fence"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	data := "# LuckyAgent Session\n\n```json\n" + string(body) + "\n```\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	tasks, err := loadReplayTasks(path)
	if err != nil {
		t.Fatalf("loadReplayTasks nested fence: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "sess-fence" {
		t.Fatalf("unexpected tasks: %#v", tasks)
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
