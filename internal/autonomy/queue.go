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
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Priority    TaskPriority      `json:"priority"`
	State       TaskState         `json:"state"`
	AssignedTo  string            `json:"assigned_to,omitempty"` // worker ID
	BlockReason string            `json:"block_reason,omitempty"`
	Result      string            `json:"result,omitempty"`
	Error       string            `json:"error,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
}

// TaskQueue is a concurrent-safe, persistent task queue.
type TaskQueue struct {
	mu          sync.RWMutex
	tasks       map[string]*QueueTask
	nextID      atomic.Int64
	ready       chan *QueueTask // buffered channel for ready tasks
	bufferSize  int
	persistPath string
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
		tasks:      make(map[string]*QueueTask),
		ready:      make(chan *QueueTask, bufferSize),
		bufferSize: bufferSize,
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
	q.mu.Lock()
	defer q.mu.Unlock()

	id := fmt.Sprintf("tq-%d", q.nextID.Add(1))
	task := &QueueTask{
		ID:          id,
		Title:       title,
		Description: description,
		Priority:    priority,
		State:       TaskReady,
		Tags:        tags,
		Metadata:    make(map[string]string),
		CreatedAt:   time.Now(),
	}

	q.tasks[id] = task

	q.enqueueReadyLocked(task)

	if err := q.persistLocked(); err != nil {
		return task, err
	}
	return task, nil
}

// Pull pulls the highest-priority ready task and marks it in-progress.
// Returns nil if no ready tasks.
func (q *TaskQueue) Pull(workerID string) *QueueTask {
	q.mu.Lock()
	defer q.mu.Unlock()

	var best *QueueTask
	for _, t := range q.tasks {
		if t.State != TaskReady {
			continue
		}
		if best == nil || t.Priority > best.Priority {
			best = t
		}
	}

	if best == nil {
		return nil
	}

	best.State = TaskInProgress
	best.AssignedTo = workerID
	best.StartedAt = time.Now()
	_ = q.persistLocked()

	return best
}

// PullChan returns a channel that yields ready tasks.
// Blocks until a task is available or context is cancelled.
func (q *TaskQueue) PullChan(ctx context.Context, workerID string) <-chan *QueueTask {
	out := make(chan *QueueTask, 1)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case task := <-q.ready:
				q.mu.Lock()
				// Re-check state (might have been claimed)
				if t, ok := q.tasks[task.ID]; ok && t.State == TaskReady {
					t.State = TaskInProgress
					t.AssignedTo = workerID
					t.StartedAt = time.Now()
					_ = q.persistLocked()
					q.mu.Unlock()
					select {
					case out <- t:
					case <-ctx.Done():
						return
					}
					return
				}
				q.mu.Unlock()
				// Task was already claimed, try again
			default:
				// No task in channel, try Pull
				if t := q.Pull(workerID); t != nil {
					select {
					case out <- t:
					case <-ctx.Done():
						return
					}
					return
				}
				// Wait a bit before retrying
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		}
	}()

	return out
}

// Complete marks a task as done.
func (q *TaskQueue) Complete(taskID, result string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	t, ok := q.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	t.State = TaskDone
	t.Result = result
	t.CompletedAt = time.Now()
	return q.persistLocked()
}

// Fail marks a task as failed (moves back to ready for retry, or blocked).
func (q *TaskQueue) Fail(taskID, errMsg string, retry bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	t, ok := q.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}

	if retry {
		t.State = TaskReady
		t.AssignedTo = ""
		t.StartedAt = time.Time{}
		t.Error = errMsg
		q.enqueueReadyLocked(t)
	} else {
		t.State = TaskBlocked
		t.BlockReason = errMsg
		t.Error = errMsg
		t.CompletedAt = time.Now()
	}
	return q.persistLocked()
}

// Block marks a task as blocked.
func (q *TaskQueue) Block(taskID, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	t, ok := q.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	t.State = TaskBlocked
	t.BlockReason = reason
	t.AssignedTo = ""
	return q.persistLocked()
}

// Unblock moves a blocked task back to ready.
func (q *TaskQueue) Unblock(taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	t, ok := q.tasks[taskID]
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	if t.State != TaskBlocked {
		return fmt.Errorf("task %s is not blocked", taskID)
	}
	t.State = TaskReady
	t.BlockReason = ""

	q.enqueueReadyLocked(t)

	return q.persistLocked()
}

// Get retrieves a task by ID.
func (q *TaskQueue) Get(taskID string) (*QueueTask, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	t, ok := q.tasks[taskID]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// ListByState lists tasks filtered by state.
func (q *TaskQueue) ListByState(state TaskState) []*QueueTask {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var result []*QueueTask
	for _, t := range q.tasks {
		if t.State == state {
			cp := *t
			result = append(result, &cp)
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
		cp := *t
		result = append(result, &cp)
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
		return 0, fmt.Errorf("read autonomy queue store: %w", err)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.persistPath = path

	if os.IsNotExist(err) {
		return 0, nil
	}

	var state persistedTaskQueue
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("parse autonomy queue store: %w", err)
	}

	q.tasks = make(map[string]*QueueTask, len(state.Tasks))
	q.ready = make(chan *QueueTask, q.bufferSize)

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
		if task.State == TaskReady {
			q.enqueueReadyLocked(&task)
		}
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

// Persist flushes the current queue state.
func (q *TaskQueue) Persist() error {
	if q == nil {
		return nil
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.persistLocked()
}

func (q *TaskQueue) enqueueReadyLocked(task *QueueTask) {
	select {
	case q.ready <- task:
	default:
	}
}

func (q *TaskQueue) persistLocked() error {
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
	tmp := q.persistPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write autonomy queue store: %w", err)
	}
	if err := os.Rename(tmp, q.persistPath); err != nil {
		return fmt.Errorf("replace autonomy queue store: %w", err)
	}
	return nil
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
