package task

import "testing"

func TestReduceObservationRecommendsVerifyForCompletedMutationWithoutTests(t *testing.T) {
	record := Record{
		ID:     "task-verify",
		Mode:   ModeParallel,
		Status: StatusCompleted,
	}
	obs := ReduceObservation(record, []Event{{
		Type:   EventProgress,
		TaskID: record.ID,
		Files:  []string{"internal/tool/delegate.go"},
	}})

	if obs.RecommendedNext != "verify" {
		t.Fatalf("expected verify recommendation, got %+v", obs)
	}
	if len(obs.FilesChanged) != 1 {
		t.Fatalf("expected file evidence, got %+v", obs)
	}
}

func TestReduceObservationRecommendsWaitForRunningChildren(t *testing.T) {
	record := Record{
		ID:     "task-wait",
		Mode:   ModeParallel,
		Status: StatusRunning,
	}
	obs := ReduceObservation(record, []Event{{
		Type:    EventChildCreated,
		TaskID:  record.ID,
		ChildID: "task-wait-sub-1",
	}})

	if obs.RunningChildren != 1 || obs.RecommendedNext != "wait" {
		t.Fatalf("expected wait with running child, got %+v", obs)
	}
}

func TestReduceObservationRecommendsAggregateForPartialFailure(t *testing.T) {
	record := Record{
		ID:     "task-aggregate",
		Mode:   ModeParallel,
		Status: StatusRunning,
	}
	obs := ReduceObservation(record, []Event{
		{Type: EventChildCreated, TaskID: record.ID, ChildID: "child-1"},
		{Type: EventChildCreated, TaskID: record.ID, ChildID: "child-2"},
		{Type: EventCompleted, TaskID: record.ID, ChildID: "child-1", Evidence: []string{"implemented module A"}},
		{Type: EventFailed, TaskID: record.ID, ChildID: "child-2", Error: "module B blocked"},
	})

	if obs.CompletedChildren != 1 || obs.FailedChildren != 1 || obs.RecommendedNext != "aggregate" {
		t.Fatalf("expected aggregate on partial failure, got %+v", obs)
	}
	if len(obs.Blockers) != 1 || obs.Blockers[0] != "module B blocked" {
		t.Fatalf("expected blocker, got %+v", obs.Blockers)
	}
}
