package autonomy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func persistedQueue(t *testing.T) (*TaskQueue, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.json")
	q := NewTaskQueue(16)
	if _, err := q.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	return q, path
}

func TestRecoveryCheckpointAndOperationReconciliation(t *testing.T) {
	q, path := persistedQueue(t)
	task := q.Add("publish", "publish once", PriorityNormal, nil)
	claimed, err := q.claim("w1", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	run := NewExecution(q, claimed)
	if err := run.BindSession("durable-session"); err != nil {
		t.Fatal(err)
	}
	if err := run.SaveCheckpoint(json.RawMessage(`{"version":1,"step":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := run.BeginOperation(Operation{ID: "step-2", Name: "send", Arguments: "{}"}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewTaskQueue(16)
	if _, err := reloaded.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	resumed, err := reloaded.claim("w2", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SessionID != "durable-session" || len(resumed.Checkpoint) == 0 || resumed.Attempts != 2 {
		t.Fatalf("lost checkpoint: %+v", resumed)
	}
	exec := NewExecution(reloaded, resumed)
	op, err := exec.BeginOperation(Operation{ID: "step-2", Name: "send", Arguments: "{}"})
	if err != nil || op.State != "started" {
		t.Fatalf("ambiguous operation must remain started: %+v %v", op, err)
	}
	if err := reloaded.Block(task.ID, "ambiguous send"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Unblock(task.ID); err == nil {
		t.Fatal("unresolved effect was allowed to run")
	}
	if err := reloaded.ResolveOperation(task.ID, "step-2", "completed", "message ID 123"); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Unblock(task.ID); err != nil {
		t.Fatal(err)
	}
	next, _ := reloaded.claim("w3", task.ID)
	op, err = NewExecution(reloaded, next).BeginOperation(Operation{ID: "step-2", Name: "send", Arguments: "{}"})
	if err != nil || op.State != "completed" || op.Output != "message ID 123" {
		t.Fatalf("reconciliation lost: %+v %v", op, err)
	}
	if err := exec.SaveCheckpoint(json.RawMessage("{}")); !errors.Is(err, ErrStaleExecution) {
		t.Fatalf("stale execution wrote checkpoint: %v", err)
	}
}

func TestRecoveryBackoffAndBudget(t *testing.T) {
	q := NewTaskQueue(8)
	q.policy = RunPolicy{MaxRetries: 2, RetryInitial: time.Minute, RetryMax: 2 * time.Minute, MaxSlices: 10, MaxTotalTime: time.Hour}
	task := q.Add("retry", "", PriorityNormal, nil)
	for attempt := 1; attempt <= 3; attempt++ {
		claimed, err := q.claim("w", task.ID)
		if err != nil || claimed == nil {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		if err := q.finishAttempt(claimed, &WorkerResult{Error: errors.New("transport unavailable")}, false); err != nil {
			t.Fatal(err)
		}
		current, _ := q.Get(task.ID)
		if attempt < 3 {
			if current.State != TaskReady || time.Until(current.NextRunAt) < time.Duration(attempt)*time.Minute-time.Second {
				t.Fatalf("missing backoff: %+v", current)
			}
			if got := q.Pull("w"); got != nil {
				t.Fatal("retry ran before backoff elapsed")
			}
			q.mu.Lock()
			q.tasks[task.ID].NextRunAt = time.Time{}
			q.mu.Unlock()
		} else if current.State != TaskBlocked {
			t.Fatalf("unbounded retries: %+v", current)
		}
	}
}

func TestRecoveryYieldDoesNotSpendRetryBudget(t *testing.T) {
	q := NewTaskQueue(8)
	q.policy.MaxSlices = 2
	task := q.Add("multi slice", "", PriorityNormal, nil)
	for i := 0; i < 2; i++ {
		claimed, _ := q.claim("w", task.ID)
		if err := q.finishAttempt(claimed, &WorkerResult{Error: ErrYield}, false); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.Get(task.ID)
	if got.State != TaskBlocked || got.Retries != 0 || got.Continuations != 2 {
		t.Fatalf("incorrect continuation budget: %+v", got)
	}
}

func TestRecoveryPersistenceFailureDoesNotExposeWork(t *testing.T) {
	q, _ := persistedQueue(t)
	task := q.Add("safe", "", PriorityNormal, nil)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	q.persistPath = filepath.Join(file, "queue.json")
	if claimed, err := q.claim("w", task.ID); err == nil || claimed != nil {
		t.Fatalf("unpersisted claim exposed: %v %v", claimed, err)
	}
	got, _ := q.Get(task.ID)
	if got.State != TaskReady || got.Attempts != 0 {
		t.Fatalf("failed claim mutated state: %+v", got)
	}
	if added, err := q.AddWithError("new", "", PriorityNormal, nil); err == nil || added != nil {
		t.Fatalf("unpersisted task accepted: %v %v", added, err)
	}
	if len(q.ListAll()) != 1 {
		t.Fatal("failed enqueue was left runnable")
	}
}

func TestRecoveryCorruptStoreIsNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	q := NewTaskQueue(8)
	if _, err := q.EnablePersistence(path); err == nil {
		t.Fatal("corrupt store accepted")
	}
	if _, err := q.AddWithError("new", "", PriorityNormal, nil); err == nil {
		t.Fatal("corrupt store allowed mutation")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "broken" {
		t.Fatal("corrupt store overwritten")
	}
}

type reliabilityExecutor struct {
	next atomic.Int64
	run  func(context.Context, string, LoopConfig) (*LoopResult, error)
}

func (e *reliabilityExecutor) NewSession(string) string {
	return fmt.Sprintf("session-%d", e.next.Add(1))
}
func (e *reliabilityExecutor) RunLoopWithSession(ctx context.Context, sessionID, _ string, cfg LoopConfig) (*LoopResult, error) {
	return e.run(ctx, sessionID, cfg)
}

func TestRecoveryConcurrentDispatchIsolatesTasks(t *testing.T) {
	q := NewTaskQueue(32)
	var active, maxActive atomic.Int64
	var mu sync.Mutex
	sessions := make(map[string]bool)
	executor := &reliabilityExecutor{run: func(ctx context.Context, sessionID string, cfg LoopConfig) (*LoopResult, error) {
		count := active.Add(1)
		defer active.Add(-1)
		if count > maxActive.Load() {
			maxActive.Store(count)
		}
		mu.Lock()
		if sessions[sessionID] {
			t.Errorf("session reused by different tasks: %s", sessionID)
		}
		sessions[sessionID] = true
		mu.Unlock()
		if cfg.Execution == nil {
			t.Error("missing durable execution")
		}
		time.Sleep(5 * time.Millisecond)
		return &LoopResult{Response: "done", Verified: true, Verification: "checked"}, nil
	}}
	cfg := DefaultPoolConfig()
	cfg.MaxWorkers, cfg.MinWorkers = 1, 1
	pool := NewWorkerPool(cfg, executor, q)
	for i := 0; i < 12; i++ {
		q.Add(fmt.Sprint(i), "", PriorityNormal, nil)
	}
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer pool.Stop()
	var callers sync.WaitGroup
	for i := 0; i < 12; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			for n := 0; n < 30; n++ {
				_, _, _ = pool.dispatchOne(context.Background(), "")
				time.Sleep(time.Millisecond)
			}
		}()
	}
	callers.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, _, _, done := q.Stats()
		if done == 12 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, running, blocked, done := q.Stats()
	if maxActive.Load() != 1 || running != 0 || blocked != 0 || done != 12 {
		t.Fatalf("concurrent reservation failure: max=%d running=%d blocked=%d done=%d", maxActive.Load(), running, blocked, done)
	}
}

func TestRecoveryStopCancelsAndRetainsTask(t *testing.T) {
	q := NewTaskQueue(4)
	started := make(chan struct{})
	executor := &reliabilityExecutor{run: func(ctx context.Context, _ string, cfg LoopConfig) (*LoopResult, error) {
		if err := cfg.Execution.SaveCheckpoint(json.RawMessage(`{"progress":"saved"}`)); err != nil {
			return nil, err
		}
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)
	task := q.Add("stop safely", "", PriorityNormal, nil)
	if err := pool.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start")
	}
	if err := pool.Stop(); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(task.ID)
	if got.State != TaskReady || got.Retries != 0 || len(got.Checkpoint) == 0 || got.SessionID == "" {
		t.Fatalf("stop lost task: %+v", got)
	}
}

func TestRecoveryDoesNotCompleteUnverifiedResult(t *testing.T) {
	q := NewTaskQueue(4)
	task := q.Add("verify", "", PriorityNormal, nil)
	claim, _ := q.claim("w", task.ID)
	if err := q.finishAttempt(claim, &WorkerResult{Output: "I will do it"}, false); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(task.ID)
	if got.State != TaskBlocked || got.Verified {
		t.Fatalf("unverified task completed: %+v", got)
	}
}

func TestRecoveryEnqueueIdempotencyIsAtomic(t *testing.T) {
	q := NewTaskQueue(4)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := q.AddWithMetadata("once", "", PriorityNormal, nil, map[string]string{"idempotency_key": "key"})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(q.ListAll()) != 1 {
		t.Fatal("concurrent enqueue created duplicates")
	}
}
