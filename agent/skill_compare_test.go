package agent

import (
	"testing"

	delegateruntime "github.com/yurika0211/luckyagent-orchestrator/delegate"
)

func TestSkillComparisonRejectsExternalEffects(t *testing.T) {
	for _, input := range []string{"send this message", "deploy the service", "write the file", "purchase a ticket"} {
		if reason := skillComparisonSafetyReason(input); reason == "" {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
	if reason := skillComparisonSafetyReason("research and summarize public documentation"); reason != "" {
		t.Fatalf("read-only task rejected: %s", reason)
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
