package autonomy

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestForegroundCompletedCompactionPreservesUnfinishedEvidence(t *testing.T) {
	q := NewTaskQueue(8)
	path := filepath.Join(t.TempDir(), "foreground.json")
	if _, err := q.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		task := q.Add("task", "", PriorityNormal, nil)
		claimed, err := q.ClaimTask(task.ID, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		e := NewExecution(q, claimed)
		if err := e.SaveCheckpoint(json.RawMessage("{\"version\":1}")); err != nil {
			t.Fatal(err)
		}
		if _, err := e.BeginOperation(Operation{ID: "op", Name: "test", Arguments: "{}"}); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if err := e.FinishOperation("op", "confirmed", false, nil); err != nil {
				t.Fatal(err)
			}
			if err := e.Finish(&WorkerResult{Output: "done", Verified: true, Verification: "confirmed"}, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := q.CompactCompleted(1); err != nil {
		t.Fatal(err)
	}
	reloaded := NewTaskQueue(8)
	if _, err := reloaded.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.ListAll()) != 2 {
		t.Fatal("wrong retention")
	}
	for _, task := range reloaded.ListAll() {
		if task.State == TaskDone {
			if len(task.Checkpoint) > 0 || len(task.Operations) > 0 || task.Result != "done" || !task.Verified {
				t.Fatalf("bad compacted result: %+v", task)
			}
		} else if len(task.Checkpoint) == 0 || len(task.Operations) != 1 || task.Operations[0].State != "started" {
			t.Fatalf("unfinished evidence lost: %+v", task)
		}
	}
}
