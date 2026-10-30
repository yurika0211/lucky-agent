package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/turn"
)

func TestCompletionReviewRetainsEarlyFailureAndRejectsUnsupportedComplete(t *testing.T) {
	a, _, _, _, cfg := durableHarness(t, durableCheckpoint{})
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("check-%d", i)
		_, err := cfg.Execution.BeginOperation(turn.DurableOperation{ID: id, Name: "terminal", Arguments: fmt.Sprintf(`{"command":"check-%d"}`, i)})
		if err != nil {
			t.Fatal(err)
		}
		output := "success"
		if i == 0 {
			output = "EARLY_BUILD_FAILED"
		}
		if err := cfg.Execution.FinishOperation(id, output, i == 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	a.provider = &durableScriptProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		var payload struct {
			Recent   []completionObservation `json:"recent_tool_evidence"`
			Failures []completionObservation `json:"unresolved_failures"`
		}
		if err := json.Unmarshal([]byte(messages[1].Content), &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Recent) > 24 || len(payload.Failures) != 1 || !strings.Contains(payload.Failures[0].Result, "EARLY_BUILD_FAILED") {
			t.Fatalf("early failure lost: %+v", payload)
		}
		return &provider.Response{Content: verifiedAssessment}, nil
	}}
	review, _, err := a.verifyDurableCandidate(context.Background(), "build the project", nil, &durableCheckpoint{Candidate: "done"}, cfg, a.baseProviderSnapshot())
	if err != nil || review.Status != "continue" || !strings.Contains(review.Reason, "check-0") {
		t.Fatalf("unsupported completion accepted: review=%+v err=%v", review, err)
	}
}

func TestCompleteFromOperationLedgerWithoutCriteria(t *testing.T) {
	review, ok := completeFromOperationLedger("已完成", []turn.DurableOperation{{ID: "op-1", State: "completed", Name: "file_read"}})
	if !ok || review.Status != "complete" {
		t.Fatalf("complete ledger was not accepted: review=%+v ok=%v", review, ok)
	}
	if len(review.Evidence) == 0 {
		t.Fatal("completion did not retain ledger evidence")
	}
}

func TestCompleteFromOperationLedgerRejectsIncompleteEvidence(t *testing.T) {
	if review, ok := completeFromOperationLedger("已完成", []turn.DurableOperation{{ID: "op-1", State: "started"}}); ok || review != nil {
		t.Fatalf("incomplete operation was accepted: review=%+v ok=%v", review, ok)
	}
	failure := []turn.DurableOperation{{ID: "op-1", State: "completed", Name: "terminal", Failed: true}}
	if review, ok := completeFromOperationLedger("已完成", failure); ok || review != nil {
		t.Fatalf("unresolved failure was accepted: review=%+v ok=%v", review, ok)
	}
}

func TestCompletionFailureRequiresEquivalentLaterSuccess(t *testing.T) {
	failure := turn.DurableOperation{ID: "failed", Name: "terminal", Arguments: `{"command":"go test","cwd":"/one"}`, State: "completed", Failed: true}
	unrelated := turn.DurableOperation{ID: "other", Name: "terminal", Arguments: `{"command":"go test","cwd":"/two"}`, State: "completed"}
	if got := unresolvedCompletionFailures([]turn.DurableOperation{failure, unrelated}); len(got) != 1 {
		t.Fatal("unrelated success cleared failure")
	}
	recheck := turn.DurableOperation{ID: "recheck", Name: "terminal", Arguments: `{"cwd":"/one","command":"go test"}`, State: "completed"}
	if got := unresolvedCompletionFailures([]turn.DurableOperation{failure, unrelated, recheck}); len(got) != 0 {
		t.Fatal("equivalent successful recheck did not clear failure")
	}
	failure.Detail = []byte(`{"shell":{"cwd":"/one"}}`)
	recheck.Detail = []byte(`{"shell":{"cwd":"/two"}}`)
	if got := unresolvedCompletionFailures([]turn.DurableOperation{failure, recheck}); len(got) != 1 {
		t.Fatal("same command in a different shell cleared failure")
	}
}

func TestCompletionProofValidatesCriteriaAndFailureReferences(t *testing.T) {
	input := completionReviewInput{Candidate: "built", Criteria: []string{"build succeeds"}, Operations: []turn.DurableOperation{
		{ID: "fail", Name: "terminal", State: "completed", Failed: true},
		{ID: "pass", Name: "verify_build", State: "completed"},
	}}
	failures := unresolvedCompletionFailures(input.Operations)
	review := durableVerification{Status: "complete", Criteria: []criterionAssessment{{Index: 1, Satisfied: true, Source: "operation", OperationIDs: []string{"pass"}, Evidence: "build exited successfully"}}, Failures: []failureAssessment{{OperationID: "fail", Status: "resolved", OperationIDs: []string{"pass"}, Reason: "fresh independent build succeeded"}}}
	if reason := validateCompletionProof(input, failures, review); reason != "" {
		t.Fatal(reason)
	}
	review.Criteria[0].OperationIDs = []string{"made-up"}
	if reason := validateCompletionProof(input, failures, review); reason == "" {
		t.Fatal("fabricated criterion proof accepted")
	}
	review.Criteria[0].OperationIDs = []string{"pass"}
	review.Failures[0].OperationIDs = []string{"fail"}
	if reason := validateCompletionProof(input, failures, review); reason == "" {
		t.Fatal("failed operation accepted as resolution proof")
	}
}
