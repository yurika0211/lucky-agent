package autonomy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrYield means a slice ended with a durable checkpoint, not a failed task.
var ErrYield = errors.New("task slice exhausted; continue from checkpoint")
var ErrBlocked = errors.New("task requires intervention")
var ErrStaleExecution = errors.New("task execution is no longer current")

type RunPolicy struct {
	MaxRetries   int
	RetryInitial time.Duration
	RetryMax     time.Duration
	MaxSlices    int
	MaxTotalTime time.Duration
}

func DefaultRunPolicy() RunPolicy {
	return RunPolicy{MaxRetries: 3, RetryInitial: 5 * time.Second, RetryMax: 5 * time.Minute, MaxSlices: 48, MaxTotalTime: 4 * time.Hour}
}

func normalizeRunPolicy(p RunPolicy) RunPolicy {
	d := DefaultRunPolicy()
	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}
	if p.RetryInitial <= 0 {
		p.RetryInitial = d.RetryInitial
	}
	if p.RetryMax < p.RetryInitial {
		p.RetryMax = d.RetryMax
	}
	if p.RetryMax < p.RetryInitial {
		p.RetryMax = p.RetryInitial
	}
	if p.MaxSlices <= 0 {
		p.MaxSlices = d.MaxSlices
	}
	if p.MaxTotalTime <= 0 {
		p.MaxTotalTime = d.MaxTotalTime
	}
	return p
}

// Operation is a write-ahead record. "started" without a result is ambiguous:
// the external effect may have happened even though its acknowledgement was lost.
type Operation struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Arguments  string          `json:"arguments"`
	State      string          `json:"state"` // started, completed, retry
	Output     string          `json:"output,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
	Failed     bool            `json:"failed,omitempty"`
	ResolvedBy string          `json:"resolved_by,omitempty"`
}

// Execution binds checkpoint writes to one persisted queue claim.
type Execution struct {
	queue   *TaskQueue
	taskID  string
	attempt int
}

func NewExecution(q *TaskQueue, task *QueueTask) *Execution {
	return &Execution{queue: q, taskID: task.ID, attempt: task.Attempts}
}

func (e *Execution) Snapshot() (*QueueTask, error) {
	t, ok := e.queue.Get(e.taskID)
	if !ok || t.State != TaskInProgress || t.Attempts != e.attempt {
		return nil, ErrStaleExecution
	}
	return t, nil
}

func (e *Execution) update(fn func(*QueueTask) error) error {
	e.queue.mu.Lock()
	defer e.queue.mu.Unlock()
	return e.queue.updateLocked(e.taskID, func(t *QueueTask) error {
		if t.State != TaskInProgress || t.Attempts != e.attempt {
			return ErrStaleExecution
		}
		return fn(t)
	})
}

func (e *Execution) BindSession(id string) error {
	return e.update(func(t *QueueTask) error {
		if t.SessionID != "" && t.SessionID != id {
			return fmt.Errorf("task already has a different session")
		}
		t.SessionID = id
		return nil
	})
}

func (e *Execution) SaveCheckpoint(data json.RawMessage) error {
	if !json.Valid(data) {
		return fmt.Errorf("invalid checkpoint JSON")
	}
	return e.update(func(t *QueueTask) error {
		t.Checkpoint = append(json.RawMessage(nil), data...)
		t.CheckpointAt = time.Now()
		return nil
	})
}

func (e *Execution) BeginOperation(op Operation) (Operation, error) {
	var result Operation
	err := e.update(func(t *QueueTask) error {
		for i, existing := range t.Operations {
			if existing.ID != op.ID {
				continue
			}
			if existing.Name != op.Name || existing.Arguments != op.Arguments {
				return fmt.Errorf("%w: operation identity mismatch", ErrBlocked)
			}
			result = existing
			if existing.State == "retry" {
				t.Operations[i].State = "started"
			}
			return nil
		}
		op.State = "started"
		t.Operations = append(t.Operations, op)
		result = op
		result.State = "new"
		return nil
	})
	return result, err
}

func (e *Execution) FinishOperation(id, output string, failed bool, detail json.RawMessage) error {
	return e.update(func(t *QueueTask) error {
		for i := range t.Operations {
			if t.Operations[i].ID == id && t.Operations[i].State == "started" {
				t.Operations[i].State = "completed"
				t.Operations[i].Output = output
				t.Operations[i].Failed = failed
				t.Operations[i].Detail = append(json.RawMessage(nil), detail...)
				return nil
			}
		}
		return fmt.Errorf("operation %s is not started", id)
	})
}

// ResolveOperation records an operator's reconciliation, never a guess.
func (q *TaskQueue) ResolveOperation(taskID, operationID, resolution, output string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.updateLocked(taskID, func(t *QueueTask) error {
		if t.State != TaskBlocked {
			return fmt.Errorf("task must be blocked before reconciliation")
		}
		for i := range t.Operations {
			op := &t.Operations[i]
			if op.ID != operationID {
				continue
			}
			if op.State != "started" {
				return fmt.Errorf("operation is not ambiguous")
			}
			switch resolution {
			case "completed":
				if strings.TrimSpace(output) == "" {
					return fmt.Errorf("completed resolution requires the observed result")
				}
				op.State, op.Output, op.Detail = "completed", output, nil
			case "retry":
				op.State = "retry"
			default:
				return fmt.Errorf("resolution must be completed or retry")
			}
			op.ResolvedBy = "operator"
			return nil
		}
		return fmt.Errorf("operation %s not found", operationID)
	})
}

func (q *TaskQueue) updateLocked(id string, fn func(*QueueTask) error) error {
	old, ok := q.tasks[id]
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}
	next := cloneTask(old)
	if err := fn(next); err != nil {
		return err
	}
	q.tasks[id] = next
	if err := q.persistLocked(); err != nil {
		q.tasks[id] = old
		return err
	}
	return nil
}

func cloneTask(t *QueueTask) *QueueTask {
	cp := *t
	cp.Tags = append([]string(nil), t.Tags...)
	cp.AcceptanceCriteria = append([]string(nil), t.AcceptanceCriteria...)
	cp.Checkpoint = append(json.RawMessage(nil), t.Checkpoint...)
	cp.Metadata = make(map[string]string, len(t.Metadata))
	for k, v := range t.Metadata {
		cp.Metadata[k] = v
	}
	cp.Operations = append([]Operation(nil), t.Operations...)
	for i := range cp.Operations {
		cp.Operations[i].Detail = append(json.RawMessage(nil), cp.Operations[i].Detail...)
	}
	return &cp
}

func (q *TaskQueue) finishAttempt(t *QueueTask, result *WorkerResult, stopped bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.updateLocked(t.ID, func(current *QueueTask) error {
		if current.State != TaskInProgress || current.Attempts != t.Attempts {
			return ErrStaleExecution
		}
		current.AssignedTo = ""
		current.Error = ""
		if result.Error == nil {
			if !result.Verified {
				current.State, current.BlockReason = TaskBlocked, "completion was not verified"
			} else {
				current.State, current.Result = TaskDone, result.Output
				current.Verified, current.Verification = true, result.Verification
			}
			current.CompletedAt = time.Now()
			return nil
		}
		current.Error = result.Error.Error()
		switch {
		case errors.Is(result.Error, ErrBlocked):
			current.State, current.BlockReason = TaskBlocked, current.Error
		case stopped:
			current.State = TaskReady
		case errors.Is(result.Error, ErrYield):
			current.State = TaskReady
			current.Continuations++
		default:
			current.Retries++
			if current.Retries > q.policy.MaxRetries {
				current.State, current.BlockReason = TaskBlocked, "retry budget exhausted: "+current.Error
			} else {
				current.State = TaskReady
				delay := q.policy.RetryInitial
				for i := 1; i < current.Retries && delay < q.policy.RetryMax; i++ {
					if delay > q.policy.RetryMax/2 {
						delay = q.policy.RetryMax
						break
					}
					delay *= 2
				}
				current.NextRunAt = time.Now().Add(delay)
			}
		}
		if current.State == TaskReady {
			if reason := q.budgetExceeded(current, time.Now()); reason != "" {
				current.State, current.BlockReason = TaskBlocked, reason
			}
		}
		if current.State == TaskBlocked {
			current.CompletedAt = time.Now()
		}
		return nil
	})
}

func (q *TaskQueue) budgetExceeded(t *QueueTask, now time.Time) string {
	if t.Attempts-t.BudgetStartAttempt >= q.policy.MaxSlices {
		return "task execution slice budget exhausted"
	}
	if !t.FirstStartedAt.IsZero() && now.Sub(t.FirstStartedAt) >= q.policy.MaxTotalTime {
		return "task total time budget exhausted"
	}
	return ""
}
