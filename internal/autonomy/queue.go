// Package autonomy provides a native Agent Autonomy Kit for LuckyAgent.
// It enables proactive, self-directed agent work through:
//   - WorkerPool: goroutine-based concurrent agent execution
//   - TaskQueue: persistent priority task queue (Ready/InProgress/Blocked/Done)
//   - HeartbeatEngine: proactive heartbeat that does work, not just checks
//   - AutonomyKit: top-level orchestrator combining all components
package autonomy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yurika0211/luckyagent/internal/utils"
)

// ---------------------------------------------------------------------------
// Task Queue
// ---------------------------------------------------------------------------

// TaskPriority represents task priority.
type TaskPriority int

const (
	PriorityLow TaskPriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

func (p TaskPriority) String() string {
	switch p {
	case PriorityLow:
		return "low"
	case PriorityNormal:
		return "normal"
	case PriorityHigh:
		return "high"
	case PriorityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

// ParseTaskPriority parses a priority string.
func ParseTaskPriority(s string) TaskPriority {
	switch s {
	case "low":
		return PriorityLow
	case "high":
		return PriorityHigh
	case "critical":
		return PriorityCritical
	default:
		return PriorityNormal
	}
}

// TaskState represents the state of a task in the queue.
type TaskState string

const (
	TaskReady      TaskState = "ready"
	TaskInProgress TaskState = "in_progress"
	TaskBlocked    TaskState = "blocked"
	TaskDone       TaskState = "done"
)

// QueueTask represents a task in the autonomy task queue.
type QueueTask struct {
	SessionID          string            `json:"session_id,omitempty"`
	Attempts           int               `json:"attempts,omitempty"`
	BudgetStartAttempt int               `json:"budget_start_attempt,omitempty"`
	Retries            int               `json:"retries,omitempty"`
	Continuations      int               `json:"continuations,omitempty"`
	FirstStartedAt     time.Time         `json:"first_started_at,omitempty"`
	NextRunAt          time.Time         `json:"next_run_at,omitempty"`
	CheckpointAt       time.Time         `json:"checkpoint_at,omitempty"`
	Checkpoint         json.RawMessage   `json:"checkpoint,omitempty"`
	Operations         []Operation       `json:"operations,omitempty"`
	AcceptanceCriteria []string          `json:"acceptance_criteria,omitempty"`
	Verified           bool              `json:"verified,omitempty"`
	Verification       string            `json:"verification,omitempty"`
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Description        string            `json:"description,omitempty"`
	Priority           TaskPriority      `json:"priority"`
	State              TaskState         `json:"state"`
	AssignedTo         string            `json:"assigned_to,omitempty"` // worker ID
	BlockReason        string            `json:"block_reason,omitempty"`
	Result             string            `json:"result,omitempty"`
	Error              string            `json:"error,omitempty"`
	Tags               []string          `json:"tags,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at,omitempty"`
	StartedAt          time.Time         `json:"started_at,omitempty"`
	CompletedAt        time.Time         `json:"completed_at,omitempty"`
}

// TaskQueue is a concurrent-safe, persistent task queue.
type TaskQueue struct {
	mu               sync.RWMutex
	tasks            map[string]*QueueTask
	nextID           atomic.Int64
	persistPath      string
	policy           RunPolicy
	persistenceError error
}

type persistedTaskQueue struct {
	Version int         `json:"version"`
	NextID  int64       `json:"next_id"`
	Tasks   []QueueTask `json:"tasks"`
}

// NewTaskQueue creates a new task queue.
func NewTaskQueue(bufferSize int) *TaskQueue {
	if bufferSize <= 0 {
		bufferSize = 64
	}
	return &TaskQueue{
		tasks:  make(map[string]*QueueTask, bufferSize),
		policy: DefaultRunPolicy(),
	}
}

// Add adds a new task to the queue.
func (q *TaskQueue) Add(title, description string, priority TaskPriority, tags []string) *QueueTask {
	task, _ := q.AddWithError(title, description, priority, tags)
	return task
}

// AddWithError adds a task and returns persistence errors to callers that need
// to surface operational failures.
func (q *TaskQueue) AddWithError(title, description string, priority TaskPriority, tags []string) (*QueueTask, error) {
	return q.AddWithMetadata(title, description, priority, tags, nil)
}

// AddWithMetadata adds a task with caller-supplied metadata.
func (q *TaskQueue) AddWithMetadata(title, description string, priority TaskPriority, tags []string, metadata map[string]string) (*QueueTask, error) {
	task, _, err := q.AddWithAcceptance(title, description, priority, tags, metadata, nil)
	return task, err
}

func (q *TaskQueue) AddWithAcceptance(title, description string, priority TaskPriority, tags []string, metadata map[string]string, criteria []string) (*QueueTask, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if key := strings.TrimSpace(metadata["idempotency_key"]); key != "" {
		for _, existing := range q.tasks {
			if existing.Metadata["idempotency_key"] == key {
				return cloneTask(existing), true, nil
			}
		}
	}
	meta := make(map[string]string, len(metadata))
	for k, v := range metadata {
		if strings.TrimSpace(k) == "" {
			continue
		}
		meta[k] = v
	}
	id := fmt.Sprintf("tq-%d", q.nextID.Add(1))
	now := time.Now()
	task := &QueueTask{
		ID:                 id,
		Title:              title,
		Description:        description,
		Priority:           priority,
		State:              TaskReady,
		Tags:               append([]string(nil), tags...),
		AcceptanceCriteria: append([]string(nil), criteria...),
		Metadata:           meta,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	q.tasks[id] = task

	if err := q.persistLocked(); err != nil {
		delete(q.tasks, id)
		return nil, false, err
	}
	return cloneTask(task), false, nil
}

// Pull pulls the highest-priority ready task and marks it in-progress.
// Returns nil if no ready tasks.
func (q *TaskQueue) Pull(workerID string) *QueueTask {
	task, _ := q.claim(workerID, "")
	return task
}

// claim persists ownership before exposing work to a worker. A requested ID
// never accidentally claims another, higher-priority task.
func (q *TaskQueue) claim(workerID, taskID string) (*QueueTask, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	var best *QueueTask
	for _, t := range q.tasks {
		if t.State != TaskReady || (taskID != "" && t.ID != taskID) || now.Before(t.NextRunAt) {
			continue
		}
		if reason := q.budgetExceeded(t, now); reason != "" {
			if err := q.updateLocked(t.ID, func(next *QueueTask) error {
				next.State, next.BlockReason, next.CompletedAt = TaskBlocked, reason, now
				return nil
			}); err != nil {
				return nil, err
			}
			continue
		}
		if best == nil || t.Priority > best.Priority || (t.Priority == best.Priority && t.CreatedAt.Before(best.CreatedAt)) {
			best = t
		}
	}

	if best == nil {
		return nil, nil
	}

	if err := q.updateLocked(best.ID, func(t *QueueTask) error {
		t.State, t.AssignedTo, t.StartedAt = TaskInProgress, workerID, now
		t.NextRunAt, t.CompletedAt = time.Time{}, time.Time{}
		t.BlockReason = ""
		t.Attempts++
		if t.FirstStartedAt.IsZero() {
			t.FirstStartedAt = now
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return cloneTask(q.tasks[best.ID]), nil
}

// PullChan returns a channel that yields ready tasks.
// Blocks until a task is available or context is cancelled.
func (q *TaskQueue) PullChan(ctx context.Context, workerID string) <-chan *QueueTask {
	out := make(chan *QueueTask, 1)
	go func() {
		defer close(out)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if t, err := q.claim(workerID, ""); err != nil {
				return
			} else if t != nil {
				out <- t
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return out
}

// Complete marks a task as done.
func (q *TaskQueue) Complete(taskID, result string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.updateLocked(taskID, func(t *QueueTask) error {
		if strings.TrimSpace(result) == "" {
			return fmt.Errorf("completion requires an observed result")
		}
		if t.State == TaskInProgress {
			return fmt.Errorf("cannot manually complete a running task; block it first")
		}
		for _, op := range t.Operations {
			if op.State == "started" {
				return fmt.Errorf("resolve operation %s before completing", op.ID)
			}
		}
		t.State, t.Result, t.CompletedAt = TaskDone, result, time.Now()
		t.Verified, t.Verification = true, "operator supplied completion result"
		return nil
	})
}

// Fail marks a task as failed (moves back to ready for retry, or blocked).
func (q *TaskQueue) Fail(taskID, errMsg string, retry bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.updateLocked(taskID, func(t *QueueTask) error {
		t.Error, t.AssignedTo = errMsg, ""
		t.Retries++
		if retry && t.Retries <= q.policy.MaxRetries {
			t.State = TaskReady
			t.NextRunAt = time.Now().Add(q.policy.RetryInitial)
		} else {
			t.State, t.BlockReason, t.CompletedAt = TaskBlocked, errMsg, time.Now()
		}
		return nil
	})
}

// Block marks a task as blocked.
func (q *TaskQueue) Block(taskID, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.updateLocked(taskID, func(t *QueueTask) error {
		t.State, t.BlockReason, t.AssignedTo = TaskBlocked, reason, ""
		return nil
	})
}

// Unblock moves a blocked task back to ready.
func (q *TaskQueue) Unblock(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.updateLocked(taskID, func(t *QueueTask) error {
		if t.State != TaskBlocked {
			return fmt.Errorf("task %s is not blocked", taskID)
		}
		for _, op := range t.Operations {
			if op.State == "started" {
				return fmt.Errorf("resolve operation %s before unblocking", op.ID)
			}
		}
		t.State, t.BlockReason, t.Error = TaskReady, "", ""
		t.NextRunAt, t.CompletedAt = time.Time{}, time.Time{}
		// Explicit intervention grants a new bounded run; checkpoints survive.
		t.Attempts++
		t.Retries = 0
		t.BudgetStartAttempt = t.Attempts
		t.FirstStartedAt = time.Time{}
		return nil
	})
}

// Get retrieves a task by ID.
func (q *TaskQueue) Get(taskID string) (*QueueTask, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	t, ok := q.tasks[taskID]
	if !ok {
		return nil, false
	}
	return cloneTask(t), true
}

// ListByState lists tasks filtered by state.
func (q *TaskQueue) ListByState(state TaskState) []*QueueTask {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*QueueTask
	for _, t := range q.tasks {
		if t.State == state {
			result = append(result, cloneTask(t))
		}
	}
	return result
}

// ListAll lists all tasks.
func (q *TaskQueue) ListAll() []*QueueTask {
	q.mu.RLock()
	defer q.mu.RUnlock()

	result := make([]*QueueTask, 0, len(q.tasks))
	for _, t := range q.tasks {
		result = append(result, cloneTask(t))
	}
	return result
}

// Stats returns queue statistics.
func (q *TaskQueue) Stats() (ready, inProgress, blocked, done int) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, t := range q.tasks {
		switch t.State {
		case TaskReady:
			ready++
		case TaskInProgress:
			inProgress++
		case TaskBlocked:
			blocked++
		case TaskDone:
			done++
		}
	}
	return
}

// CleanDone removes completed tasks older than the given duration.
func (q *TaskQueue) CleanDone(olderThan time.Duration) int {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()
	removed := 0
	for id, t := range q.tasks {
		if t.State == TaskDone && !t.CompletedAt.IsZero() && now.Sub(t.CompletedAt) > olderThan {
			delete(q.tasks, id)
			removed++
		}
	}
	_ = q.persistLocked()
	return removed
}

// EnablePersistence loads queue state from path and persists subsequent
// mutations. In-progress tasks from a previous process are restored as ready so
// they can be retried instead of remaining stuck forever.
func (q *TaskQueue) EnablePersistence(path string) (int, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, nil
	}

	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		loadErr := fmt.Errorf("read autonomy queue store: %w", err)
		q.mu.Lock()
		q.persistenceError = loadErr
		q.mu.Unlock()
		return 0, loadErr
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.persistPath = path

	if os.IsNotExist(err) {
		q.persistenceError = nil
		return 0, nil
	}

	var state persistedTaskQueue
	if err := json.Unmarshal(data, &state); err != nil {
		q.persistenceError = fmt.Errorf("parse autonomy queue store: %w", err)
		return 0, q.persistenceError
	}
	if state.Version != 1 {
		q.persistenceError = fmt.Errorf("unsupported autonomy queue version %d", state.Version)
		return 0, q.persistenceError
	}
	q.persistenceError = nil

	q.tasks = make(map[string]*QueueTask, len(state.Tasks))

	maxID := state.NextID
	for _, stored := range state.Tasks {
		task := stored
		if strings.TrimSpace(task.ID) == "" {
			continue
		}
		if task.Metadata == nil {
			task.Metadata = make(map[string]string)
		}
		if task.State == TaskInProgress {
			task.State = TaskReady
			task.AssignedTo = ""
			task.StartedAt = time.Time{}
			task.Error = strings.TrimSpace(joinNonEmpty(task.Error, "restored from interrupted autonomy run"))
		}
		q.tasks[task.ID] = &task
		if n := parseTaskNumericID(task.ID); n > maxID {
			maxID = n
		}
	}
	q.nextID.Store(maxID)

	if err := q.persistLocked(); err != nil {
		return len(q.tasks), err
	}
	return len(q.tasks), nil
}

// PersistencePath returns the queue store path, if persistence is enabled.
func (q *TaskQueue) PersistencePath() string {
	if q == nil {
		return ""
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.persistPath
}

func (q *TaskQueue) PersistenceError() error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.persistenceError
}

// Persist flushes the current queue state.
func (q *TaskQueue) Persist() error {
	if q == nil {
		return nil
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.persistLocked()
}

func (q *TaskQueue) persistLocked() error {
	if q.persistenceError != nil {
		return q.persistenceError
	}
	if q == nil || strings.TrimSpace(q.persistPath) == "" {
		return nil
	}

	tasks := make([]QueueTask, 0, len(q.tasks))
	for _, t := range q.tasks {
		if t == nil {
			continue
		}
		cp := *t
		tasks = append(tasks, cp)
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})

	state := persistedTaskQueue{
		Version: 1,
		NextID:  q.nextID.Load(),
		Tasks:   tasks,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal autonomy queue store: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(q.persistPath), 0o700); err != nil {
		return fmt.Errorf("create autonomy queue store dir: %w", err)
	}
	return utils.WriteFileAtomic(q.persistPath, data, 0o600)
}

func parseTaskNumericID(id string) int64 {
	n, err := strconv.ParseInt(strings.TrimPrefix(id, "tq-"), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func joinNonEmpty(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "; ")
}
