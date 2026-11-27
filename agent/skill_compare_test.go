package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestSkillComparisonUsesExistingSafetyDecision(t *testing.T) {
	previous := skillRepeatSafety
	t.Cleanup(func() { skillRepeatSafety = previous })
	skillRepeatSafety = func(description string) (bool, string) {
		if description == "repeatable lookup" {
			return true, ""
		}
		return false, "existing checker: external effect"
	}
	if reason := skillComparisonSafetyReason("send the weekly report"); reason == "" {
		t.Fatal("non-repeatable task was accepted without the word list")
	}
	if reason := skillComparisonSafetyReason("repeatable lookup"); reason != "" {
		t.Fatalf("repeatable task rejected: %s", reason)
	}
}

func TestSkillCompareDualRunSelectsLowerScore(t *testing.T) {
	previous := skillRepeatSafety
	t.Cleanup(func() { skillRepeatSafety = previous })
	skillRepeatSafety = func(string) (bool, string) { return true, "" }
	reg := tool.NewRegistry()
	for _, name := range []string{"alpha", "beta"} {
		skill := name
		reg.Register(&tool.Tool{
			Name: "skill_" + skill + "_run", Enabled: true,
			Handler: func(map[string]any) (string, error) { return skill + " done", nil },
		})
	}
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	store := &preferenceMemory{}
	sessions, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{tools: reg, memory: store, sessions: sessions, skills: []*tool.SkillInfo{
		{Name: "alpha", Description: "lookup notes", Dir: t.TempDir()},
		{Name: "beta", Description: "lookup notes", Dir: t.TempDir()},
	}}
	a.delegate = tool.NewDelegateManager(tool.DefaultDelegateConfig())
	a.delegateChildRun = func(_ context.Context, _ *session.Session, input UserTurnInput, cfg LoopConfig) (*LoopResult, error) {
		calls := []toolCallLog{{Name: "read"}, {Name: "read"}, {Name: "read"}, {Name: "read"}}
		if strings.Contains(input.RoutingText, "skill alpha") {
			calls = []toolCallLog{{Name: "read"}}
			time.Sleep(15 * time.Millisecond)
		} else {
			time.Sleep(40 * time.Millisecond)
		}
		_ = cfg
		return &LoopResult{Response: "completed with evidence", ToolCalls: calls}, nil
	}
	a.bindOptionalExecutionServices()
	a.installSkillComparisonTool()
	compare, ok := a.tools.Get(skillComparisonToolName)
	if !ok {
		t.Fatal("skill_compare missing")
	}
	out, err := compare.ContextDetailedHandler(tool.ExecutionContext{Context: context.Background()}, map[string]any{
		"description": "lookup notes", "skill_a": "alpha", "skill_b": "beta", "safe_to_repeat": true, "timeout": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, `"status":"selected"`) || !strings.Contains(out.Output, `"winner":"alpha"`) {
		t.Fatalf("lower cost×time did not win: %s", out.Output)
	}
	if strings.Contains(strings.Join(store.saved, "\n"), "lookup notes") {
		t.Fatalf("saved memory contains the task: %v", store.saved)
	}
}

func TestSkillCompareSelectsTheOnlyCompletedCandidate(t *testing.T) {
	previous := skillRepeatSafety
	t.Cleanup(func() { skillRepeatSafety = previous })
	skillRepeatSafety = func(string) (bool, string) { return true, "" }
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	sessions, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{tools: reg, sessions: sessions, skills: []*tool.SkillInfo{
		{Name: "alpha", Description: "lookup notes", Dir: t.TempDir()},
		{Name: "beta", Description: "lookup notes", Dir: t.TempDir()},
	}}
	a.delegate = tool.NewDelegateManager(tool.DefaultDelegateConfig())
	a.delegateChildRun = func(_ context.Context, _ *session.Session, input UserTurnInput, _ LoopConfig) (*LoopResult, error) {
		if strings.Contains(input.RoutingText, "skill beta") {
			return &LoopResult{Response: "beta failed"}, fmt.Errorf("beta failed")
		}
		time.Sleep(15 * time.Millisecond)
		return &LoopResult{Response: "completed with evidence", ToolCalls: []toolCallLog{{Name: "read"}, {Name: "read"}}}, nil
	}
	a.bindOptionalExecutionServices()
	a.installSkillComparisonTool()
	compare, _ := a.tools.Get(skillComparisonToolName)
	out, err := compare.ContextDetailedHandler(tool.ExecutionContext{Context: context.Background()}, map[string]any{
		"description": "lookup notes", "skill_a": "alpha", "skill_b": "beta", "safe_to_repeat": true, "timeout": 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Output, `"winner":"alpha"`) {
		t.Fatalf("only completed candidate was not selected: %s", out.Output)
	}
	if strings.Contains(out.Output, "lookup notes") && strings.Contains(out.Output, "memory_saved\":true") {
		t.Fatalf("task text was written into the comparison result: %s", out.Output)
	}
}

func TestComparisonPreferenceOmitsTaskText(t *testing.T) {
	secret := "token sk-live-secret and the full task body"
	a := &Agent{}
	saved := a.saveSkillComparisonPreference(secret, skillComparisonCandidate{Skill: "alpha", Score: 10}, skillComparisonCandidate{Skill: "beta", Score: 20})
	if saved {
		t.Fatal("preference was saved without a memory store")
	}
	content := comparisonPreferenceText(secret, skillComparisonCandidate{Skill: "alpha", Score: 10}, skillComparisonCandidate{Skill: "beta", Score: 20})
	if strings.Contains(content, secret) || strings.Contains(content, "sk-live") {
		t.Fatalf("comparison memory contains the task text: %s", content)
	}
	if !strings.Contains(content, "alpha") || !strings.Contains(content, "system comparison") {
		t.Fatalf("comparison memory lost the skill names: %s", content)
	}
	overhead := comparisonOverhead([]skillComparisonCandidate{{ToolCalls: 2}, {ToolCalls: 3}})
	if overhead != 5 {
		t.Fatalf("comparison overhead = %d", overhead)
	}
}

func TestComparisonCandidateRequiresCompletionAndMeasuredCost(t *testing.T) {
	status := delegateruntime.StatusResponse{
		TaskID: "task-1", Status: "completed", ToolCalls: 2, ElapsedMS: 10,
		Result: "inspected two sources", ResultSummary: "completed with evidence",
	}
	c := comparisonCandidate("alpha", status)
	if !c.Complete || !c.CostAvailable || c.Score != 20 {
		t.Fatalf("unexpected completed candidate: %+v", c)
	}
	status.ToolCalls = 0
	c = comparisonCandidate("alpha", status)
	if c.Complete && c.CostAvailable {
		t.Fatalf("missing cost must not be eligible: %+v", c)
	}
	status.ToolCalls = 2
	status.Result = "partial result"
	c = comparisonCandidate("alpha", status)
	if c.Complete {
		t.Fatalf("partial result must not be complete: %+v", c)
	}
}
