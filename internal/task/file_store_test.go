package task

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileStorePersistsRecordEventsAndArtifacts(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	record, err := store.Create(Record{
		ID:          "task-1",
		Source:      SourceTool,
		Mode:        ModeSingle,
		Description: "delegate implementation",
		Input:       "run focused tests",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if record.ID != "task-1" || record.Status != StatusPending || record.CreatedAt.IsZero() {
		t.Fatalf("unexpected created record: %+v", record)
	}

	bus := NewEventBus(store)
	if err := bus.Created(record); err != nil {
		t.Fatalf("Created event: %v", err)
	}
	record.Status = StatusRunning
	record.StartedAt = time.Now()
	if err := store.Update(record); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := bus.Started(record); err != nil {
		t.Fatalf("Started event: %v", err)
	}
	if err := store.AppendEvent(Event{
		Type:     EventProgress,
		TaskID:   record.ID,
		Progress: 0.5,
		Evidence: []string{"go test ./internal/task passed"},
		Files:    []string{"internal/task/file_store.go"},
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := store.SaveResult(record.ID, "# Result\nok\n"); err != nil {
		t.Fatalf("SaveResult: %v", err)
	}
	if err := store.SavePlannerTrace(record.ID, map[string]any{"planner": "test"}); err != nil {
		t.Fatalf("SavePlannerTrace: %v", err)
	}

	got, ok, err := store.Get(record.ID)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%t err=%v", ok, err)
	}
	if got.Status != StatusRunning {
		t.Fatalf("expected running, got %+v", got)
	}
	events, err := store.Events(record.ID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %+v", events)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), record.ID, "result.md")); err != nil {
		t.Fatalf("missing result artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), record.ID, "planner_trace.json")); err != nil {
		t.Fatalf("missing planner trace artifact: %v", err)
	}
}

func TestFileStoreListFiltersAndSorts(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	oldTask, err := store.Create(Record{
		ID:          "old",
		Source:      SourceTool,
		Status:      StatusCompleted,
		Description: "old",
		CreatedAt:   time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("Create old: %v", err)
	}
	newTask, err := store.Create(Record{
		ID:          "new",
		Source:      SourceHTTP,
		Status:      StatusCompleted,
		Description: "new",
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("Create new: %v", err)
	}

	all, err := store.List(ListFilter{Status: StatusCompleted})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 || all[0].ID != newTask.ID || all[1].ID != oldTask.ID {
		t.Fatalf("expected newest first, got %+v", all)
	}
	httpOnly, err := store.List(ListFilter{Source: SourceHTTP})
	if err != nil {
		t.Fatalf("List source: %v", err)
	}
	if len(httpOnly) != 1 || httpOnly[0].ID != newTask.ID {
		t.Fatalf("expected HTTP task only, got %+v", httpOnly)
	}
}
