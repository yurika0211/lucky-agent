package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yurika0211/luckyagent/internal/autonomy"
)

func TestHandleAutonomyDashboard(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	kit := a.Autonomy()

	runningSeed := kit.AddTask("running task", "working", autonomy.PriorityHigh, nil)
	running := kit.Queue().Pull("worker-1")
	if running == nil {
		t.Fatal("expected running task")
	}
	if running.ID != runningSeed.ID {
		t.Fatalf("pulled unexpected task: got %s want %s", running.ID, runningSeed.ID)
	}
	ready := kit.AddTask("ready task", "waiting", autonomy.PriorityNormal, []string{"android"})
	blocked := kit.AddTask("blocked task", "needs input", autonomy.PriorityNormal, nil)
	if err := kit.Queue().Block(blocked.ID, "needs input"); err != nil {
		t.Fatalf("block task: %v", err)
	}
	done := kit.AddTask("done task", "finished", autonomy.PriorityLow, nil)
	if err := kit.Queue().Block(done.ID, "operator checked"); err != nil {
		t.Fatalf("prepare done task: %v", err)
	}
	if err := kit.Queue().Complete(done.ID, "complete"); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/autonomy/dashboard?limit=2", nil)
	w := httptest.NewRecorder()
	s.handleAutonomyDashboard(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard status: got %d: %s", w.Code, w.Body.String())
	}
	var response autonomyDashboardResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if response.Count != 2 || len(response.Tasks) != 2 {
		t.Fatalf("expected limited dashboard, got count=%d tasks=%d", response.Count, len(response.Tasks))
	}
	if response.Queue.Ready != 1 || response.Queue.InProgress != 1 || response.Queue.Blocked != 1 || response.Queue.Done != 1 {
		t.Fatalf("unexpected queue counts: %+v", response.Queue)
	}
	if response.Started {
		t.Fatal("dashboard must not start autonomy runtime")
	}

	filtered := httptest.NewRecorder()
	s.handleAutonomyDashboard(filtered, httptest.NewRequest(http.MethodGet, "/api/v1/autonomy/dashboard?state=blocked", nil))
	if filtered.Code != http.StatusOK {
		t.Fatalf("blocked filter status: got %d: %s", filtered.Code, filtered.Body.String())
	}
	var blockedResponse autonomyDashboardResponse
	if err := json.NewDecoder(filtered.Body).Decode(&blockedResponse); err != nil {
		t.Fatalf("decode blocked dashboard: %v", err)
	}
	if blockedResponse.Count != 1 || blockedResponse.Tasks[0].ID != blocked.ID {
		t.Fatalf("unexpected blocked filter: %+v", blockedResponse.Tasks)
	}

	invalid := httptest.NewRecorder()
	s.handleAutonomyDashboard(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/autonomy/dashboard?state=unknown", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid state status: got %d", invalid.Code)
	}
	_ = ready
}

func TestHandleAutonomyTaskByID(t *testing.T) {
	a := createTestAgent(t)
	s := New(a, DefaultServerConfig())
	task := a.Autonomy().AddTask("inspect details", "read-only details", autonomy.PriorityNormal, []string{"detail"})
	if err := a.Autonomy().Queue().Block(task.ID, "waiting for review"); err != nil {
		t.Fatalf("block task: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/autonomy/tasks/"+task.ID, nil)
	w := httptest.NewRecorder()
	s.handleAutonomyTaskByID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("detail status: got %d: %s", w.Code, w.Body.String())
	}
	var response autonomyTaskDetailResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if response.Task.ID != task.ID || response.Task.State != autonomy.TaskBlocked || response.Task.BlockReason != "waiting for review" {
		t.Fatalf("unexpected detail: %+v", response.Task)
	}
	if len(response.Task.AcceptanceCriteria) != 0 || response.Task.CheckpointPresent {
		t.Fatalf("unexpected detail fields: %+v", response.Task)
	}

	notFound := httptest.NewRecorder()
	s.handleAutonomyTaskByID(notFound, httptest.NewRequest(http.MethodGet, "/api/v1/autonomy/tasks/missing", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("missing detail status: got %d", notFound.Code)
	}
}

func TestTaskQueueUpdatesUpdatedAt(t *testing.T) {
	queue := autonomy.NewTaskQueue(4)
	task := queue.Add("timestamped", "", autonomy.PriorityNormal, nil)
	if task.UpdatedAt.IsZero() || !task.UpdatedAt.Equal(task.CreatedAt) {
		t.Fatalf("new task timestamps: created=%v updated=%v", task.CreatedAt, task.UpdatedAt)
	}
	if err := queue.Block(task.ID, "blocked"); err != nil {
		t.Fatalf("block task: %v", err)
	}
	updated, ok := queue.Get(task.ID)
	if !ok || !updated.UpdatedAt.After(task.UpdatedAt) {
		t.Fatalf("updated timestamp did not advance: before=%v after=%v", task.UpdatedAt, updated.UpdatedAt)
	}
}
