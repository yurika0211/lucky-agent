package collab

import (
	"context"
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
}
