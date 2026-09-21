package autonomy

import "sort"

// CompactCompleted bounds history in a caller-owned foreground store. Unfinished
// tasks retain every checkpoint and operation; completed transcripts already
// live in their session, so only recent status/result summaries are retained.
func (q *TaskQueue) CompactCompleted(keep int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if keep < 0 {
		keep = 0
	}
	var completed []*QueueTask
	for _, t := range q.tasks {
		if t.State == TaskDone {
			completed = append(completed, t)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].CreatedAt.After(completed[j].CreatedAt) })
	changed := false
	next := make(map[string]*QueueTask, len(q.tasks))
	for id, t := range q.tasks {
		next[id] = t
	}
	for i, t := range completed {
		if i >= keep {
			delete(next, t.ID)
			changed = true
		} else if len(t.Checkpoint) > 0 || len(t.Operations) > 0 {
			copy := cloneTask(t)
			copy.Checkpoint, copy.Operations = nil, nil
			next[t.ID] = copy
			changed = true
		}
	}
	if !changed {
		return nil
	}
	previous := q.tasks
	q.tasks = next
	if err := q.persistLocked(); err != nil {
		q.tasks = previous
		return err
	}
	return nil
}

// SetRunPolicy configures a caller-owned store before any claims are made.
func (q *TaskQueue) SetRunPolicy(policy RunPolicy) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.policy = normalizeRunPolicy(policy)
}

// ClaimTask reserves a specific task for a caller-owned execution. It does not
// start a worker or scheduler. Foreground callers use a separate persistent store.
func (q *TaskQueue) ClaimTask(taskID, owner string) (*QueueTask, error) {
	if taskID == "" {
		return nil, ErrStaleExecution
	}
	return q.claim(owner, taskID)
}

// Finish records the result of a caller-owned attempt using the same lease and
// completion checks as a worker. Cancellation never starts a successor itself.
func (e *Execution) Finish(result *WorkerResult, canceled bool) error {
	return e.queue.finishAttempt(&QueueTask{ID: e.taskID, Attempts: e.attempt}, result, canceled)
}
