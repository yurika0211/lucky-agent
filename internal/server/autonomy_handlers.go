package server

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/autonomy"
)

type autonomyDashboardResponse struct {
	GeneratedAt     time.Time                 `json:"generated_at"`
	Started         bool                      `json:"started"`
	Queue           autonomyQueueCounts       `json:"queue"`
	Pool            autonomyPoolView          `json:"pool"`
	Workers         []autonomy.WorkerInfo     `json:"workers"`
	LastHeartbeat   *time.Time                `json:"last_heartbeat,omitempty"`
	HeartbeatEvents []autonomyHeartbeatEvent  `json:"heartbeat_events,omitempty"`
	Tasks           []autonomyTaskSummaryView `json:"tasks"`
	Count           int                       `json:"count"`
}

type autonomyQueueCounts struct {
	Ready      int `json:"ready"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Done       int `json:"done"`
}

type autonomyPoolView struct {
	WorkerCount    int   `json:"worker_count"`
	IdleWorkers    int   `json:"idle_workers"`
	BusyWorkers    int   `json:"busy_workers"`
	StoppedWorkers int   `json:"stopped_workers"`
	TotalTasks     int64 `json:"total_tasks"`
	FailedTasks    int64 `json:"failed_tasks"`
	AverageMs      int64 `json:"average_duration_ms"`
	Running        bool  `json:"running"`
}

type autonomyHeartbeatEvent struct {
	Timestamp   time.Time              `json:"timestamp"`
	Mode        autonomy.HeartbeatMode `json:"mode"`
	TasksPulled int                    `json:"tasks_pulled"`
	TasksDone   int                    `json:"tasks_done"`
	TasksFailed int                    `json:"tasks_failed"`
	Actions     []string               `json:"actions,omitempty"`
}

type autonomyTaskSummaryView struct {
	ID                string             `json:"id"`
	Title             string             `json:"title"`
	Description       string             `json:"description,omitempty"`
	Priority          string             `json:"priority"`
	State             autonomy.TaskState `json:"state"`
	AssignedTo        string             `json:"assigned_to,omitempty"`
	Tags              []string           `json:"tags,omitempty"`
	SessionID         string             `json:"session_id,omitempty"`
	Attempts          int                `json:"attempts"`
	Retries           int                `json:"retries"`
	Continuations     int                `json:"continuations"`
	Verified          bool               `json:"verified"`
	Verification      string             `json:"verification,omitempty"`
	BlockReason       string             `json:"block_reason,omitempty"`
	ResultPreview     string             `json:"result_preview,omitempty"`
	Error             string             `json:"error,omitempty"`
	CreatedAt         *time.Time         `json:"created_at,omitempty"`
	UpdatedAt         *time.Time         `json:"updated_at,omitempty"`
	StartedAt         *time.Time         `json:"started_at,omitempty"`
	CompletedAt       *time.Time         `json:"completed_at,omitempty"`
	NextRunAt         *time.Time         `json:"next_run_at,omitempty"`
	CheckpointAt      *time.Time         `json:"checkpoint_at,omitempty"`
	CheckpointPresent bool               `json:"checkpoint_present"`
	LastActivityAt    *time.Time         `json:"last_activity_at,omitempty"`
}

type autonomyOperationView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	State      string `json:"state"`
	Failed     bool   `json:"failed,omitempty"`
	ResolvedBy string `json:"resolved_by,omitempty"`
}

type autonomyTaskDetailView struct {
	autonomyTaskSummaryView
	AcceptanceCriteria []string                `json:"acceptance_criteria,omitempty"`
	Metadata           map[string]string       `json:"metadata,omitempty"`
	Result             string                  `json:"result,omitempty"`
	Operations         []autonomyOperationView `json:"operations,omitempty"`
}

type autonomyTaskDetailResponse struct {
	Task   autonomyTaskDetailView `json:"task"`
	Worker *autonomy.WorkerInfo   `json:"worker,omitempty"`
}

func (s *Server) handleAutonomyDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	kit := s.autonomyKit()
	if kit == nil {
		s.sendError(w, "autonomy kit not initialized", http.StatusServiceUnavailable, "")
		return
	}
	limit, err := boundedAutonomyLimit(r.URL.Query().Get("limit"))
	if err != nil {
		s.sendError(w, "invalid limit", http.StatusBadRequest, err.Error())
		return
	}
	tasks, err := autonomyTasks(kit, r.URL.Query().Get("state"), limit)
	if err != nil {
		s.sendError(w, "invalid state", http.StatusBadRequest, err.Error())
		return
	}
	status := kit.Status()
	pool := status.PoolStats
	events := kit.Heartbeat().RecentEvents(20)
	heartbeatEvents := make([]autonomyHeartbeatEvent, 0, len(events))
	for _, event := range events {
		heartbeatEvents = append(heartbeatEvents, heartbeatEventView(event))
	}

	s.sendJSON(w, http.StatusOK, autonomyDashboardResponse{
		GeneratedAt:     time.Now(),
		Started:         status.Started,
		Queue:           autonomyQueueCounts{Ready: status.QueueReady, InProgress: status.QueueInProgress, Blocked: status.QueueBlocked, Done: status.QueueDone},
		Pool:            autonomyPoolView{WorkerCount: pool.WorkerCount, IdleWorkers: pool.IdleWorkers, BusyWorkers: pool.BusyWorkers, StoppedWorkers: pool.StoppedWorkers, TotalTasks: pool.TotalTasks, FailedTasks: pool.FailedTasks, AverageMs: pool.AvgDuration.Milliseconds(), Running: pool.Running},
		Workers:         kit.Pool().ListWorkers(),
		LastHeartbeat:   autonomyTime(status.LastHeartbeat),
		HeartbeatEvents: heartbeatEvents,
		Tasks:           tasks,
		Count:           len(tasks),
	})
}

func (s *Server) handleAutonomyTaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.sendError(w, "method not allowed", http.StatusMethodNotAllowed, "")
		return
	}
	kit := s.autonomyKit()
	if kit == nil {
		s.sendError(w, "autonomy kit not initialized", http.StatusServiceUnavailable, "")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/autonomy/tasks/"), "/")
	if id == "" || strings.Contains(id, "/") {
		s.sendError(w, "task id is required", http.StatusBadRequest, "")
		return
	}
	task, ok := kit.Queue().Get(id)
	if !ok {
		s.sendError(w, "autonomy task not found", http.StatusNotFound, id)
		return
	}
	var worker *autonomy.WorkerInfo
	for _, candidate := range kit.Pool().ListWorkers() {
		if candidate.ID == task.AssignedTo {
			candidateCopy := candidate
			worker = &candidateCopy
			break
		}
	}
	operations := make([]autonomyOperationView, 0, len(task.Operations))
	for _, operation := range task.Operations {
		operations = append(operations, autonomyOperationView{ID: operation.ID, Name: operation.Name, State: operation.State, Failed: operation.Failed, ResolvedBy: operation.ResolvedBy})
	}
	view := autonomyTaskDetailView{
		autonomyTaskSummaryView: autonomyTaskSummary(task),
		AcceptanceCriteria:      append([]string(nil), task.AcceptanceCriteria...),
		Metadata:                cloneStringMap(task.Metadata),
		Result:                  task.Result,
		Operations:              operations,
	}
	s.sendJSON(w, http.StatusOK, autonomyTaskDetailResponse{Task: view, Worker: worker})
}

func (s *Server) autonomyKit() *autonomy.AutonomyKit {
	if s == nil || s.agent == nil {
		return nil
	}
	return s.agent.Autonomy()
}

func autonomyTasks(kit *autonomy.AutonomyKit, rawState string, limit int) ([]autonomyTaskSummaryView, error) {
	state := strings.TrimSpace(rawState)
	if state != "" {
		switch autonomy.TaskState(state) {
		case autonomy.TaskReady, autonomy.TaskInProgress, autonomy.TaskBlocked, autonomy.TaskDone:
		default:
			return nil, strconv.ErrSyntax
		}
	}
	all := kit.Queue().ListAll()
	tasks := make([]*autonomy.QueueTask, 0, len(all))
	for _, task := range all {
		if state == "" || string(task.State) == state {
			tasks = append(tasks, task)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		left, right := taskActivity(tasks[i]), taskActivity(tasks[j])
		if left.Equal(right) {
			return tasks[i].ID < tasks[j].ID
		}
		return left.After(right)
	})
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	result := make([]autonomyTaskSummaryView, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, autonomyTaskSummary(task))
	}
	return result, nil
}

func autonomyTaskSummary(task *autonomy.QueueTask) autonomyTaskSummaryView {
	return autonomyTaskSummaryView{
		ID:                task.ID,
		Title:             task.Title,
		Description:       task.Description,
		Priority:          task.Priority.String(),
		State:             task.State,
		AssignedTo:        task.AssignedTo,
		Tags:              append([]string(nil), task.Tags...),
		SessionID:         task.SessionID,
		Attempts:          task.Attempts,
		Retries:           task.Retries,
		Continuations:     task.Continuations,
		Verified:          task.Verified,
		Verification:      task.Verification,
		BlockReason:       task.BlockReason,
		ResultPreview:     truncateAutonomyText(task.Result, 320),
		Error:             task.Error,
		CreatedAt:         autonomyTime(task.CreatedAt),
		UpdatedAt:         autonomyTime(task.UpdatedAt),
		StartedAt:         autonomyTime(task.StartedAt),
		CompletedAt:       autonomyTime(task.CompletedAt),
		NextRunAt:         autonomyTime(task.NextRunAt),
		CheckpointAt:      autonomyTime(task.CheckpointAt),
		CheckpointPresent: len(task.Checkpoint) > 0,
		LastActivityAt:    autonomyTime(taskActivity(task)),
	}
}

func heartbeatEventView(event autonomy.HeartbeatEvent) autonomyHeartbeatEvent {
	return autonomyHeartbeatEvent{
		Timestamp:   event.Timestamp,
		Mode:        event.Mode,
		TasksPulled: event.TasksPulled,
		TasksDone:   event.TasksDone,
		TasksFailed: event.TasksFailed,
		Actions:     append([]string(nil), event.Actions...),
	}
}

func taskActivity(task *autonomy.QueueTask) time.Time {
	if task == nil {
		return time.Time{}
	}
	for _, candidate := range []time.Time{task.UpdatedAt, task.CheckpointAt, task.CompletedAt, task.StartedAt, task.CreatedAt} {
		if !candidate.IsZero() {
			return candidate
		}
	}
	return time.Time{}
}

func autonomyTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func truncateAutonomyText(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func boundedAutonomyLimit(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 200, nil
	}
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return 0, strconv.ErrSyntax
	}
	if limit > 200 {
		limit = 200
	}
	return limit, nil
}
