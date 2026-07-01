package collab

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlannerChoosesParallelForIndependentWork(t *testing.T) {
	planner := NewPlanner(nil)
	plan := planner.Plan(PlanRequest{
		Description: "并行审计 CLI、websocket、autonomy 三个模块，然后合并风险清单",
		AgentIDs:    []string{"repo", "ws", "auto"},
	})

	if plan.Mode != ModeParallel {
		t.Fatalf("mode = %s, want %s; candidates=%+v", plan.Mode, ModeParallel, plan.Candidates)
	}
	if len(plan.Trace) == 0 || len(plan.Path) == 0 {
		t.Fatalf("expected dijkstra path trace, got path=%v trace=%v", plan.Path, plan.Trace)
	}
}

func TestPlannerChoosesPipelineForSequentialWork(t *testing.T) {
	planner := NewPlanner(nil)
	plan := planner.Plan(PlanRequest{
		Description: "先定位配置读取路径，再修改实现，最后跑测试",
		AgentIDs:    []string{"repo", "coder", "test"},
	})

	if plan.Mode != ModePipeline {
		t.Fatalf("mode = %s, want %s; candidates=%+v", plan.Mode, ModePipeline, plan.Candidates)
	}
}

func TestPlannerChoosesDebateForReviewWork(t *testing.T) {
	planner := NewPlanner(nil)
	plan := planner.Plan(PlanRequest{
		Description: "让数学代理和工程代理辩论 Lyapunov 能不能替代 Dijkstra，并由 critic 裁决",
		AgentIDs:    []string{"math", "eng", "critic"},
	})

	if plan.Mode != ModeDebate {
		t.Fatalf("mode = %s, want %s; candidates=%+v", plan.Mode, ModeDebate, plan.Candidates)
	}
}

func TestPlannerMarkovObservationsShiftDecision(t *testing.T) {
	model := NewAdaptiveMarkovModel()
	planner := NewPlanner(model)
	req := PlanRequest{
		Description: "并行检查三个独立模块",
		AgentIDs:    []string{"a", "b", "c"},
	}
	if initial := planner.Plan(req); initial.Mode != ModeParallel {
		t.Fatalf("initial mode = %s, want %s", initial.Mode, ModeParallel)
	}

	for i := 0; i < 12; i++ {
		model.Observe(ModeParallel, "failure")
		model.Observe(ModePipeline, "success")
	}

	updated := planner.Plan(req)
	if updated.Mode != ModePipeline {
		t.Fatalf("updated mode = %s, want %s; candidates=%+v", updated.Mode, ModePipeline, updated.Candidates)
	}
}

func TestPlannerIncludesMDPTrace(t *testing.T) {
	planner := NewPlanner(nil)
	req := PlanRequest{
		Description: "并行审计三个独立模块，然后由 verifier 跑测试",
		AgentIDs:    []string{"repo", "ws", "test"},
		Agents: []*AgentProfile{
			{ID: "repo", Capabilities: []string{"repo"}},
			{ID: "ws", Capabilities: []string{"websocket"}},
			{ID: "test", Capabilities: []string{"testing", "verifier"}},
		},
		Timeout: 60 * time.Second,
	}

	plan := planner.Plan(req)
	if plan.MDP.Version != mdpPlannerVersion {
		t.Fatalf("mdp version = %q, want %q", plan.MDP.Version, mdpPlannerVersion)
	}
	if plan.MDP.StateKey == "" {
		t.Fatal("expected mdp state key")
	}
	if !plan.MDP.State.HasVerifier {
		t.Fatalf("expected verifier state, got %+v", plan.MDP.State)
	}
	if _, ok := plan.MDP.QValues[ModeParallel]; !ok {
		t.Fatalf("expected q value for parallel, got %+v", plan.MDP.QValues)
	}
}

func TestPlannerMDPObservationsUpdateQValues(t *testing.T) {
	planner := NewPlanner(nil)
	req := PlanRequest{
		Description: "并行检查三个独立模块",
		AgentIDs:    []string{"a", "b", "c"},
		Timeout:     60 * time.Second,
	}

	for i := 0; i < 8; i++ {
		planner.ObserveExecution(req, ModeParallel, "failure", 40*time.Second)
		planner.ObserveExecution(req, ModePipeline, "success", 20*time.Second)
	}

	plan := planner.Plan(req)
	if plan.MDP.QValues[ModePipeline] <= plan.MDP.QValues[ModeParallel] {
		t.Fatalf("expected pipeline q > parallel q, got mdp=%+v", plan.MDP)
	}
	if plan.MDP.Samples[ModePipeline] == 0 || plan.MDP.Samples[ModeParallel] == 0 {
		t.Fatalf("expected mdp samples, got %+v", plan.MDP.Samples)
	}
}

func TestPlannerMDPPersistenceAndTransitions(t *testing.T) {
	planner := NewPlanner(nil)
	req := PlanRequest{
		Description: "先读代码，再修复问题，最后跑 verifier 测试",
		AgentIDs:    []string{"repo", "coder", "test"},
		Agents: []*AgentProfile{
			{ID: "repo", Capabilities: []string{"repo"}},
			{ID: "coder", Capabilities: []string{"go", "backend"}},
			{ID: "test", Capabilities: []string{"test", "verifier"}},
		},
		Timeout: 2 * time.Minute,
	}
	planner.ObserveExecution(req, ModePipeline, "success", 40*time.Second)
	before := planner.Plan(req)
	action := before.MDP.Actions[ModePipeline]
	actionKey := action.Key()
	if actionKey == "" {
		t.Fatal("expected expanded action key")
	}
	if before.MDP.ActionQValues[actionKey] == 0 {
		t.Fatalf("expected learned action q value, got %+v", before.MDP)
	}
	if len(before.MDP.TransitionProbabilities[actionKey]) == 0 {
		t.Fatalf("expected transition probabilities, got %+v", before.MDP.TransitionProbabilities)
	}

	path := filepath.Join(t.TempDir(), "mdp.json")
	if err := planner.SaveMDP(path); err != nil {
		t.Fatalf("save mdp: %v", err)
	}
	restored := NewPlanner(nil)
	if err := restored.LoadMDP(path); err != nil {
		t.Fatalf("load mdp: %v", err)
	}
	after := restored.Plan(req)
	if after.MDP.ActionQValues[actionKey] != before.MDP.ActionQValues[actionKey] {
		t.Fatalf("q value not restored: before=%v after=%v", before.MDP.ActionQValues, after.MDP.ActionQValues)
	}
	if len(after.MDP.TransitionProbabilities[actionKey]) == 0 {
		t.Fatalf("transition probabilities not restored: %+v", after.MDP.TransitionProbabilities)
	}
}

func TestDelegateManagerAutoModeUsesPlanner(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&AgentProfile{ID: "agent-1", Name: "Agent 1"})
	_ = r.Register(&AgentProfile{ID: "agent-2", Name: "Agent 2"})
	_ = r.Register(&AgentProfile{ID: "agent-3", Name: "Agent 3"})

	handler := TaskHandlerFunc(func(ctx context.Context, task *SubTask) (string, error) {
		return "result_from_" + task.AgentID, nil
	})
	dm := NewDelegateManager(r, handler)

	task, err := dm.Delegate(context.Background(), ModeAuto, "分别检查三个独立模块", "input", []string{"agent-1", "agent-2", "agent-3"}, 10*time.Second)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	updated, ok := dm.GetTask(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if updated.Mode != ModeParallel {
		t.Fatalf("planned mode = %s, want %s", updated.Mode, ModeParallel)
	}
	if updated.Metadata["planner"] != plannerVersion {
		t.Fatalf("missing planner metadata: %+v", updated.Metadata)
	}
	if !strings.Contains(updated.Metadata["planner_trace"], `"version":"`+plannerVersion+`"`) {
		t.Fatalf("missing planner trace payload: %s", updated.Metadata["planner_trace"])
	}
	if updated.Metadata["mdp_version"] != mdpPlannerVersion {
		t.Fatalf("missing mdp metadata: %+v", updated.Metadata)
	}
	if updated.Metadata["mdp_state"] == "" {
		t.Fatalf("missing mdp state metadata: %+v", updated.Metadata)
	}
}
