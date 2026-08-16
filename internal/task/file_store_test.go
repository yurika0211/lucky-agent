package task

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	resultText, ok, err := store.Result(record.ID)
	if err != nil || !ok || resultText != "# Result\nok\n" {
		t.Fatalf("unexpected result read: ok=%t err=%v result=%q", ok, err, resultText)
	}
	trace, ok, err := store.PlannerTrace(record.ID)
	if err != nil || !ok || !strings.Contains(string(trace), `"planner": "test"`) {
		t.Fatalf("unexpected planner trace read: ok=%t err=%v trace=%s", ok, err, trace)
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

func TestFileStoreCleanupRetention(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	now := time.Now()
	records := []Record{
		{ID: "old-completed", Status: StatusCompleted, CreatedAt: now.Add(-48 * time.Hour), CompletedAt: now.Add(-47 * time.Hour), Description: "old completed"},
		{ID: "old-running", Status: StatusRunning, CreatedAt: now.Add(-48 * time.Hour), Description: "old running"},
		{ID: "new-completed", Status: StatusCompleted, CreatedAt: now.Add(-time.Minute), CompletedAt: now.Add(-time.Minute), Description: "new completed"},
	}
	for _, record := range records {
		if _, err := store.Create(record); err != nil {
			t.Fatalf("Create %s: %v", record.ID, err)
		}
	}

	result, err := store.Cleanup(RetentionPolicy{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != "old-completed" {
		t.Fatalf("unexpected deleted ids: %+v", result)
	}
	if _, ok, err := store.Get("old-completed"); err != nil || ok {
		t.Fatalf("old completed should be deleted: ok=%t err=%v", ok, err)
	}
	if _, ok, err := store.Get("old-running"); err != nil || !ok {
		t.Fatalf("old running should be retained: ok=%t err=%v", ok, err)
	}
	if _, ok, err := store.Get("new-completed"); err != nil || !ok {
		t.Fatalf("new completed should be retained: ok=%t err=%v", ok, err)
	}
}

func TestFileStoreCleanupKeepLast(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	now := time.Now()
	for i, id := range []string{"oldest", "middle", "newest"} {
		if _, err := store.Create(Record{
			ID:          id,
			Status:      StatusCompleted,
			CreatedAt:   now.Add(time.Duration(i-3) * time.Hour),
			CompletedAt: now.Add(time.Duration(i-3) * time.Hour),
			Description: id,
		}); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	result, err := store.Cleanup(RetentionPolicy{KeepLast: 2})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(result.DeletedIDs) != 1 || result.DeletedIDs[0] != "oldest" {
		t.Fatalf("unexpected deleted ids: %+v", result)
	}
}

func TestFileStoreConcurrentGetAndUpdateNeverReturnsPartialJSON(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	record, err := store.Create(Record{ID: "concurrent", Description: "concurrent access"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const updates = 100
	errs := make(chan error, updates)
	done := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, ok, err := store.Get(record.ID); err != nil || !ok {
					errs <- err
					return
				}
			}
		}()
	}
	for i := range updates {
		record.Status = StatusRunning
		record.Metadata = map[string]string{"sequence": strings.Repeat("x", 1024) + string(rune('a'+i%26))}
		if err := store.Update(record); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}
	close(done)
	readers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Get during Update: %v", err)
		}
		t.Fatal("Get during Update unexpectedly reported no record")
	}
}
