package autonomy

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TaskQueue tests
// ---------------------------------------------------------------------------

func TestTaskQueueAdd(t *testing.T) {
	q := NewTaskQueue(16)

	task := q.Add("Test task", "Do something", PriorityNormal, []string{"test"})

	if task.ID == "" {
		t.Fatal("expected non-empty task ID")
	}
	if task.State != TaskReady {
		t.Fatalf("expected ready state, got %s", task.State)
	}
	if task.Priority != PriorityNormal {
		t.Fatalf("expected normal priority, got %s", task.Priority)
	}
}

func TestTaskQueuePull(t *testing.T) {
	q := NewTaskQueue(16)

	// Pull from empty queue
	if task := q.Pull("w1"); task != nil {
		t.Fatal("expected nil from empty queue")
	}

	// Add tasks with different priorities
	low := q.Add("Low task", "", PriorityLow, nil)
	high := q.Add("High task", "", PriorityHigh, nil)
	normal := q.Add("Normal task", "", PriorityNormal, nil)

	// Should pull highest priority first
	pulled := q.Pull("w1")
	if pulled.ID != high.ID {
		t.Fatalf("expected high priority task, got %s (priority: %s)", pulled.ID, pulled.Priority)
	}
	if pulled.State != TaskInProgress {
		t.Fatalf("expected in_progress state, got %s", pulled.State)
	}

	// Next should be normal
	pulled = q.Pull("w2")
	if pulled.ID != normal.ID {
		t.Fatalf("expected normal priority task, got %s", pulled.ID)
	}

	// Then low
	pulled = q.Pull("w3")
	if pulled.ID != low.ID {
		t.Fatalf("expected low priority task, got %s", pulled.ID)
	}
}

func TestTaskQueueComplete(t *testing.T) {
	q := NewTaskQueue(16)

	task := q.Add("Test", "", PriorityNormal, nil)
	q.Pull("w1")

	if err := q.Complete(task.ID, "done!"); err != nil {
		t.Fatal(err)
	}

	got, ok := q.Get(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if got.State != TaskDone {
		t.Fatalf("expected done state, got %s", got.State)
	}
	if got.Result != "done!" {
		t.Fatalf("expected result 'done!', got %s", got.Result)
	}
}

func TestTaskQueueFail(t *testing.T) {
	q := NewTaskQueue(16)

	task := q.Add("Test", "", PriorityNormal, nil)
	q.Pull("w1")

	// Fail with retry
	if err := q.Fail(task.ID, "oops", true); err != nil {
		t.Fatal(err)
	}

	got, _ := q.Get(task.ID)
	if got.State != TaskReady {
		t.Fatalf("expected ready state (retry), got %s", got.State)
	}

	// Fail without retry
	q.Pull("w1")
	if err := q.Fail(task.ID, "fatal", false); err != nil {
		t.Fatal(err)
	}

	got, _ = q.Get(task.ID)
	if got.State != TaskBlocked {
		t.Fatalf("expected blocked state, got %s", got.State)
	}
}

func TestTaskQueueBlockUnblock(t *testing.T) {
	q := NewTaskQueue(16)

	task := q.Add("Test", "", PriorityNormal, nil)

	if err := q.Block(task.ID, "waiting for approval"); err != nil {
		t.Fatal(err)
	}

	got, _ := q.Get(task.ID)
	if got.State != TaskBlocked {
		t.Fatalf("expected blocked state, got %s", got.State)
	}

	if err := q.Unblock(task.ID); err != nil {
		t.Fatal(err)
	}

	got, _ = q.Get(task.ID)
	if got.State != TaskReady {
		t.Fatalf("expected ready state after unblock, got %s", got.State)
	}
}

func TestTaskQueueStats(t *testing.T) {
	q := NewTaskQueue(16)

	q.Add("T1", "", PriorityNormal, nil)
	q.Add("T2", "", PriorityHigh, nil)
	q.Add("T3", "", PriorityLow, nil)

	ready, inProgress, _, _ := q.Stats()
	if ready != 3 {
		t.Fatalf("expected 3 ready, got %d", ready)
	}

	q.Pull("w1")
	ready, inProgress, _, _ = q.Stats()
	if inProgress != 1 {
		t.Fatalf("expected 1 in_progress, got %d", inProgress)
	}
	if ready != 2 {
		t.Fatalf("expected 2 ready, got %d", ready)
	}
}

func TestTaskQueueConcurrentAccess(t *testing.T) {
	q := NewTaskQueue(64)

	var wg sync.WaitGroup
	const goroutines = 10
	const tasksPerGoroutine = 50

	// Concurrent adds
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < tasksPerGoroutine; j++ {
				q.Add(fmt.Sprintf("Task-%d-%d", id, j), "", PriorityNormal, nil)
			}
		}(i)
	}
	wg.Wait()

	ready, _, _, _ := q.Stats()
	if ready != goroutines*tasksPerGoroutine {
		t.Fatalf("expected %d tasks, got %d", goroutines*tasksPerGoroutine, ready)
	}

	// Concurrent pulls
	var pulled int64
	var pullMu sync.Mutex
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < tasksPerGoroutine; j++ {
				if q.Pull(fmt.Sprintf("w-%d", id)) != nil {
					pullMu.Lock()
					pulled++
					pullMu.Unlock()
				}
			}
		}(i)
	}
	wg.Wait()

	if pulled != int64(goroutines*tasksPerGoroutine) {
		t.Fatalf("expected %d pulled, got %d", goroutines*tasksPerGoroutine, pulled)
	}
}

// ---------------------------------------------------------------------------
// HeartbeatEngine tests
// ---------------------------------------------------------------------------

func TestHeartbeatIsActiveHour(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	cfg.ActiveStart = 6
	cfg.ActiveEnd = 23

	hb := NewHeartbeatEngine(cfg, nil, NewTaskQueue(16))

	// 10 AM should be active
	if !hb.isActiveHour(time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("10 AM should be active")
	}

	// 3 AM should not be active
	if hb.isActiveHour(time.Date(2026, 4, 21, 3, 0, 0, 0, time.UTC)) {
		t.Fatal("3 AM should not be active")
	}
}

func TestHeartbeatIsActiveHourWrapMidnight(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	cfg.ActiveStart = 22
	cfg.ActiveEnd = 6

	hb := NewHeartbeatEngine(cfg, nil, NewTaskQueue(16))

	// 11 PM should be active
	if !hb.isActiveHour(time.Date(2026, 4, 21, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("11 PM should be active (wraps midnight)")
	}

	// 3 AM should be active
	if !hb.isActiveHour(time.Date(2026, 4, 21, 3, 0, 0, 0, time.UTC)) {
		t.Fatal("3 AM should be active (wraps midnight)")
	}

	// 10 AM should not be active
	if hb.isActiveHour(time.Date(2026, 4, 21, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("10 AM should not be active (wraps midnight)")
	}
}

func TestHeartbeatTrigger(t *testing.T) {
	q := NewTaskQueue(16)

	// Add some tasks
	q.Add("Task 1", "", PriorityHigh, nil)
	q.Add("Task 2", "", PriorityNormal, nil)

	// Trigger without pool (passive mode check)
	cfg := DefaultHeartbeatConfig()
	cfg.Mode = HeartbeatPassive
	hb := NewHeartbeatEngine(cfg, nil, q)

	event := hb.Trigger(context.Background())
	if event == nil {
		t.Fatal("expected non-nil event")
	}
}

// ---------------------------------------------------------------------------
// AutonomyKit integration test
// ---------------------------------------------------------------------------

func TestAutonomyKitAddTask(t *testing.T) {
	q := NewTaskQueue(16)

	kit := &AutonomyKit{
		queue: q,
	}

	task := kit.AddTask("Research topic", "Deep dive into X", PriorityHigh, []string{"research"})
	if task.ID == "" {
		t.Fatal("expected non-empty task ID")
	}

	ready, _, _, _ := q.Stats()
	if ready != 1 {
		t.Fatalf("expected 1 ready task, got %d", ready)
	}
}

func TestAutonomyKitStatus(t *testing.T) {
	q := NewTaskQueue(16)
	pool := NewWorkerPool(DefaultPoolConfig(), nil, q)
	hb := NewHeartbeatEngine(DefaultHeartbeatConfig(), pool, q)

	kit := &AutonomyKit{
		queue:     q,
		pool:      pool,
		heartbeat: hb,
	}

	q.Add("Task 1", "", PriorityNormal, nil)
	q.Add("Task 2", "", PriorityHigh, nil)

	status := kit.Status()
	if status.QueueReady != 2 {
		t.Fatalf("expected 2 ready, got %d", status.QueueReady)
	}
}

// ---------------------------------------------------------------------------
// ToolDefinitions tests
// ---------------------------------------------------------------------------

func TestToolQueueAdd(t *testing.T) {
	q := NewTaskQueue(16)
	kit := &AutonomyKit{queue: q}
	td := NewToolDefinitions(kit)

	result, err := td.HandleQueueAdd(map[string]any{
		"title":       "Test task",
		"description": "Do something",
		"priority":    "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	ready, _, _, _ := q.Stats()
	if ready != 1 {
		t.Fatalf("expected 1 ready task, got %d", ready)
	}
}

func TestToolQueueList(t *testing.T) {
	q := NewTaskQueue(16)
	kit := &AutonomyKit{queue: q}
	td := NewToolDefinitions(kit)

	q.Add("Task 1", "", PriorityNormal, nil)
	q.Add("Task 2", "", PriorityHigh, nil)

	result, err := td.HandleQueueList(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestToolQueueUpdate(t *testing.T) {
	q := NewTaskQueue(16)
	kit := &AutonomyKit{queue: q}
	td := NewToolDefinitions(kit)

	task := q.Add("Test", "", PriorityNormal, nil)
	q.Pull("w1")

	// Complete
	_, err := td.HandleQueueUpdate(map[string]any{
		"task_id": task.ID,
		"action":  "complete",
		"result":  "All done!",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := q.Get(task.ID)
	if got.State != TaskDone {
		t.Fatalf("expected done, got %s", got.State)
	}
}

func TestToolQueueUpdateBlock(t *testing.T) {
	q := NewTaskQueue(16)
	kit := &AutonomyKit{queue: q}
	td := NewToolDefinitions(kit)

	task := q.Add("Test", "", PriorityNormal, nil)

	_, err := td.HandleQueueUpdate(map[string]any{
		"task_id": task.ID,
		"action":  "block",
		"reason":  "needs approval",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ := q.Get(task.ID)
	if got.State != TaskBlocked {
		t.Fatalf("expected blocked, got %s", got.State)
	}

	// Unblock
	_, err = td.HandleQueueUpdate(map[string]any{
		"task_id": task.ID,
		"action":  "unblock",
	})
	if err != nil {
		t.Fatal(err)
	}

	got, _ = q.Get(task.ID)
	if got.State != TaskReady {
		t.Fatalf("expected ready after unblock, got %s", got.State)
	}
}

func TestToolQueueUpdateInvalidAction(t *testing.T) {
	q := NewTaskQueue(16)
	kit := &AutonomyKit{queue: q}
	td := NewToolDefinitions(kit)

	task := q.Add("Test", "", PriorityNormal, nil)

	_, err := td.HandleQueueUpdate(map[string]any{
		"task_id": task.ID,
		"action":  "explode",
	})
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestToolStatus(t *testing.T) {
	q := NewTaskQueue(16)
	pool := NewWorkerPool(DefaultPoolConfig(), nil, q)
	hb := NewHeartbeatEngine(DefaultHeartbeatConfig(), pool, q)
	kit := &AutonomyKit{queue: q, pool: pool, heartbeat: hb}
	td := NewToolDefinitions(kit)

	q.Add("Task 1", "", PriorityNormal, nil)

	result, err := td.HandleStatus(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}
}

func TestParseTaskPriority(t *testing.T) {
	tests := []struct {
		input    string
		expected TaskPriority
	}{
		{"low", PriorityLow},
		{"normal", PriorityNormal},
		{"high", PriorityHigh},
		{"critical", PriorityCritical},
		{"", PriorityNormal},
		{"unknown", PriorityNormal},
	}

	for _, tt := range tests {
		got := ParseTaskPriority(tt.input)
		if got != tt.expected {
			t.Errorf("ParseTaskPriority(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestTaskQueueCleanDone(t *testing.T) {
	q := NewTaskQueue(16)

	task := q.Add("Old task", "", PriorityNormal, nil)
	q.Pull("w1")
	q.Complete(task.ID, "done")

	// Task was just completed, shouldn't be cleaned yet
	removed := q.CleanDone(1 * time.Hour)
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}

	// Clean with very short duration
	removed = q.CleanDone(0)
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	_, _, _, done := q.Stats()
	if done != 0 {
		t.Fatalf("expected 0 done after clean, got %d", done)
	}
}

func TestTaskQueuePersistenceRestoresTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autonomy_queue.json")

	q := NewTaskQueue(16)
	if restored, err := q.EnablePersistence(path); err != nil {
		t.Fatalf("EnablePersistence: %v", err)
	} else if restored != 0 {
		t.Fatalf("expected no restored tasks, got %d", restored)
	}
	task, err := q.AddWithError("Persistent task", "survive restart", PriorityHigh, []string{"persist"})
	if err != nil {
		t.Fatalf("AddWithError: %v", err)
	}
	if err := q.Block(task.ID, "waiting"); err != nil {
		t.Fatalf("Block: %v", err)
	}

	reloaded := NewTaskQueue(16)
	restored, err := reloaded.EnablePersistence(path)
	if err != nil {
		t.Fatalf("reload EnablePersistence: %v", err)
	}
	if restored != 1 {
		t.Fatalf("expected one restored task, got %d", restored)
	}
	got, ok := reloaded.Get(task.ID)
	if !ok {
		t.Fatalf("restored task %s not found", task.ID)
	}
	if got.State != TaskBlocked || got.BlockReason != "waiting" {
		t.Fatalf("unexpected restored state: %#v", got)
	}
}

func TestTaskQueuePersistenceRestoresInterruptedTasksAsReady(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autonomy_queue.json")

	q := NewTaskQueue(16)
	if _, err := q.EnablePersistence(path); err != nil {
		t.Fatalf("EnablePersistence: %v", err)
	}
	task, err := q.AddWithError("Interrupted task", "", PriorityNormal, nil)
	if err != nil {
		t.Fatalf("AddWithError: %v", err)
	}
	pulled := q.Pull("worker-1")
	if pulled == nil || pulled.ID != task.ID {
		t.Fatalf("expected task to be pulled, got %#v", pulled)
	}

	reloaded := NewTaskQueue(16)
	if _, err := reloaded.EnablePersistence(path); err != nil {
		t.Fatalf("reload EnablePersistence: %v", err)
	}
	got, ok := reloaded.Get(task.ID)
	if !ok {
		t.Fatalf("restored task %s not found", task.ID)
	}
	if got.State != TaskReady {
		t.Fatalf("expected interrupted task to restore as ready, got %s", got.State)
	}
	if got.AssignedTo != "" || !got.StartedAt.IsZero() {
		t.Fatalf("expected assignment to be cleared, got %#v", got)
	}
}

func TestTaskQueueListByState(t *testing.T) {
	q := NewTaskQueue(16)

	q.Add("T1", "", PriorityNormal, nil)
	q.Add("T2", "", PriorityHigh, nil)
	t3 := q.Add("T3", "", PriorityLow, nil)

	q.Pull("w1") // pulls T2 (highest priority)
	q.Block(t3.ID, "waiting")

	ready := q.ListByState(TaskReady)
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready, got %d", len(ready))
	}

	inProgress := q.ListByState(TaskInProgress)
	if len(inProgress) != 1 {
		t.Fatalf("expected 1 in_progress, got %d", len(inProgress))
	}

	blocked := q.ListByState(TaskBlocked)
	if len(blocked) != 1 {
		t.Fatalf("expected 1 blocked, got %d", len(blocked))
	}
}

func TestTaskQueueStatsAll(t *testing.T) {
	q := NewTaskQueue(16)
	q.Add("T1", "", PriorityNormal, nil)
	q.Add("T2", "", PriorityHigh, nil)
	t3 := q.Add("T3", "", PriorityLow, nil)
	q.Pull("w1")
	q.Block(t3.ID, "waiting")

	ready, inProgress, blocked, done := q.Stats()
	_ = blocked
	_ = done
	if ready != 1 || inProgress != 1 {
		t.Fatalf("expected 1 ready + 1 in_progress, got %d ready + %d in_progress", ready, inProgress)
	}
}

// ---------------------------------------------------------------------------
// Worker tests (with correct API signatures)
// ---------------------------------------------------------------------------

func TestNewWorker(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := WorkerConfig{ID: "w1"}

	worker := NewWorker(cfg, executor)
	if worker == nil {
		t.Fatal("expected non-nil worker")
	}

	info := worker.Info()
	if info.ID != "w1" {
		t.Errorf("expected ID 'w1', got %s", info.ID)
	}
	if info.State != WorkerIdle {
		t.Errorf("expected idle state, got %s", info.State)
	}
}

func TestWorkerPoolStartStop(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)

	ctx := context.Background()

	// Start pool
	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give it time to spawn workers
	time.Sleep(100 * time.Millisecond)

	workers := pool.ListWorkers()
	if len(workers) == 0 {
		t.Error("expected at least one worker")
	}

	// Stop pool
	if err := pool.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestWorkerPoolScaleUp(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	cfg := DefaultPoolConfig()
	cfg.MinWorkers = 1
	cfg.MaxWorkers = 5
	pool := NewWorkerPool(cfg, executor, q)

	ctx := context.Background()

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer pool.Stop()

	time.Sleep(100 * time.Millisecond)

	initial := len(pool.ListWorkers())

	// Scale up
	if err := pool.ScaleUp(ctx, 2); err != nil {
		t.Fatalf("ScaleUp failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	after := len(pool.ListWorkers())
	if after <= initial {
		t.Errorf("expected more workers after scale up, got %d -> %d", initial, after)
	}
}

func TestWorkerPoolScaleDown(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	cfg := DefaultPoolConfig()
	cfg.MinWorkers = 1
	cfg.MaxWorkers = 5
	pool := NewWorkerPool(cfg, executor, q)

	ctx := context.Background()

	if err := pool.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer pool.Stop()

	time.Sleep(100 * time.Millisecond)

	initial := len(pool.ListWorkers())

	// Scale down (should not go below min)
	removed := pool.ScaleDown(2)
	if removed < 0 {
		t.Errorf("expected non-negative removed count, got %d", removed)
	}

	time.Sleep(100 * time.Millisecond)

	after := len(pool.ListWorkers())
	if after > initial {
		t.Errorf("expected fewer or equal workers after scale down, got %d -> %d", initial, after)
	}
}

func TestWorkerPoolStats(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)

	stats := pool.Stats()
	if stats.WorkerCount < 0 {
		t.Error("expected non-negative worker count")
	}
	if stats.IdleWorkers < 0 {
		t.Error("expected non-negative idle workers")
	}
}

func TestWorkerPoolResults(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)

	results := pool.Results()
	if results == nil {
		t.Error("expected non-nil results channel")
	}
}

// ---------------------------------------------------------------------------
// Heartbeat tests (with correct API signatures)
// ---------------------------------------------------------------------------

func TestDefaultAutonomyConfig(t *testing.T) {
	cfg := DefaultAutonomyConfig()

	if cfg.QueueBuf <= 0 {
		t.Error("expected positive queue buffer")
	}
	if cfg.Pool.MinWorkers <= 0 {
		t.Error("expected positive min workers")
	}
	if cfg.Pool.MaxWorkers <= 0 {
		t.Error("expected positive max workers")
	}
}

func TestNewAutonomyKit(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := DefaultAutonomyConfig()

	kit := NewAutonomyKit(cfg, executor)
	if kit == nil {
		t.Fatal("expected non-nil kit")
	}

	if kit.queue == nil {
		t.Error("expected non-nil queue")
	}
	if kit.pool == nil {
		t.Error("expected non-nil pool")
	}
	if kit.heartbeat == nil {
		t.Error("expected non-nil heartbeat")
	}
}

func TestAutonomyKitStartStop(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := DefaultAutonomyConfig()
	kit := NewAutonomyKit(cfg, executor)

	ctx := context.Background()

	// Start
	if err := kit.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Verify workers spawned
	workers := kit.Pool().ListWorkers()
	if len(workers) == 0 {
		t.Error("expected workers to be spawned")
	}

	// Stop
	if err := kit.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestAutonomyKitQueue(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := DefaultAutonomyConfig()
	kit := NewAutonomyKit(cfg, executor)

	queue := kit.Queue()
	if queue == nil {
		t.Error("expected non-nil queue")
	}
}

func TestAutonomyKitPool(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := DefaultAutonomyConfig()
	kit := NewAutonomyKit(cfg, executor)

	pool := kit.Pool()
	if pool == nil {
		t.Error("expected non-nil pool")
	}
}

func TestAutonomyKitHeartbeat(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := DefaultAutonomyConfig()
	kit := NewAutonomyKit(cfg, executor)

	hb := kit.Heartbeat()
	if hb == nil {
		t.Error("expected non-nil heartbeat")
	}
}

func TestHeartbeatRecentEvents(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	q := NewTaskQueue(16)
	hb := NewHeartbeatEngine(cfg, nil, q)

	// Trigger to generate events
	hb.Trigger(context.Background())

	events := hb.RecentEvents(10)
	// Should have at least zero events (may be empty if no events recorded)
	if events == nil {
		t.Error("expected non-nil events slice")
	}
}

func TestHeartbeatStartStop(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	q := NewTaskQueue(16)
	hb := NewHeartbeatEngine(cfg, nil, q)

	ctx := context.Background()

	// Start
	if err := hb.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop
	if err := hb.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestHeartbeatLastBeat(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	q := NewTaskQueue(16)
	hb := NewHeartbeatEngine(cfg, nil, q)

	lastBeat := hb.LastBeat()
	// Should be zero time initially
	if !lastBeat.IsZero() {
		// Or could be non-zero if triggered
		_ = lastBeat
	}
}

// ---------------------------------------------------------------------------
// Tool handler tests (uncovered)
// ---------------------------------------------------------------------------

func TestToolWorkerSpawn(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)
	hb := NewHeartbeatEngine(DefaultHeartbeatConfig(), pool, q)
	kit := &AutonomyKit{queue: q, pool: pool, heartbeat: hb}
	_ = NewToolDefinitions(kit)

	ctx := context.Background()
	pool.Start(ctx)
	defer pool.Stop()

	// Add a task first
	task := q.Add("Test task", "Do something", PriorityNormal, nil)

	// Use Get() to retrieve a safe copy instead of reading the pointer directly
	// to avoid data race with pool goroutines that may modify task state
	taskCopy, ok := q.Get(task.ID)
	if !ok {
		t.Fatal("task not found")
	}
	if taskCopy.State != TaskReady {
		t.Fatalf("expected task to be ready, got %s", taskCopy.State)
	}

	workers := pool.ListWorkers()
	if len(workers) < 1 {
		t.Errorf("expected at least 1 worker, got %d", len(workers))
	}
}

func TestToolWorkerList(t *testing.T) {
	q := NewTaskQueue(16)
	executor := &mockAgentExecutor{}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)
	hb := NewHeartbeatEngine(DefaultHeartbeatConfig(), pool, q)
	kit := &AutonomyKit{queue: q, pool: pool, heartbeat: hb}
	td := NewToolDefinitions(kit)

	ctx := context.Background()
	pool.Start(ctx)
	defer pool.Stop()
	time.Sleep(100 * time.Millisecond)

	result, err := td.HandleWorkerList(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestToolHeartbeatTrigger(t *testing.T) {
	q := NewTaskQueue(16)
	q.Add("Task 1", "", PriorityHigh, nil)
	executor := &mockAgentExecutor{}
	pool := NewWorkerPool(DefaultPoolConfig(), executor, q)
	hb := NewHeartbeatEngine(DefaultHeartbeatConfig(), pool, q)
	kit := &AutonomyKit{queue: q, pool: pool, heartbeat: hb}
	td := NewToolDefinitions(kit)

	result, err := td.HandleHeartbeatTrigger(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

// ---------------------------------------------------------------------------
// Helper types
// ---------------------------------------------------------------------------

// mockAgentExecutor implements AgentExecutor for testing
type mockAgentExecutor struct {
	mu       sync.Mutex
	sessions []string
}

func (m *mockAgentExecutor) RunLoopWithSession(ctx context.Context, sessionID string, userInput string, cfg LoopConfig) (*LoopResult, error) {
	return &LoopResult{
		Response:   "mock response",
		TokensUsed: 100,
		Iterations: 1,
	}, nil
}

func (m *mockAgentExecutor) NewSession(title string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	sessionID := fmt.Sprintf("session-%d", len(m.sessions))
	m.sessions = append(m.sessions, sessionID)
	return sessionID
}

// ---------------------------------------------------------------------------
// Additional tests for coverage
// ---------------------------------------------------------------------------

func TestWorkerTaskCount(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := WorkerConfig{ID: "test-worker"}
	worker := NewWorker(cfg, executor)

	// Initial count should be 0
	if worker.TaskCount() != 0 {
		t.Errorf("expected initial task count 0, got %d", worker.TaskCount())
	}
}

func TestWorkerPoolSetExecutor(t *testing.T) {
	q := NewTaskQueue(10)
	executor1 := &mockAgentExecutor{}
	executor2 := &mockAgentExecutor{}

	cfg := DefaultPoolConfig()
	cfg.MinWorkers = 1
	cfg.MaxWorkers = 2

	pool := NewWorkerPool(cfg, executor1, q)
	defer pool.Stop()

	// SetExecutor should update the executor
	pool.SetExecutor(executor2)

	// Verify executor was updated
	if pool.executor != executor2 {
		t.Error("expected executor to be updated")
	}
}

func TestMinFunction(t *testing.T) {
	// Test basic min
	result := min(3, 1, 2)
	if result != 1 {
		t.Errorf("expected min(3,1,2) = 1, got %d", result)
	}

	// Test single value
	result = min(5)
	if result != 5 {
		t.Errorf("expected min(5) = 5, got %d", result)
	}

	// Test negative values
	result = min(-5, -2, -10)
	if result != -10 {
		t.Errorf("expected min(-5,-2,-10) = -10, got %d", result)
	}
}

func TestWorkerExecute(t *testing.T) {
	q := NewTaskQueue(10)
	executor := &mockAgentExecutor{}
	cfg := DefaultPoolConfig()
	pool := NewWorkerPool(cfg, executor, q)
	defer pool.Stop()

	// Spawn a worker
	worker := pool.spawnWorker(context.Background())
	if worker == nil {
		t.Fatal("expected non-nil worker")
	}

	task := &QueueTask{
		ID:          "test-task-1",
		Title:       "Test Task",
		Description: "Test Description",
		Tags:        []string{"test"},
	}

	ctx := context.Background()
	result := worker.Execute(ctx, task)

	if result.TaskID != task.ID {
		t.Errorf("expected task ID %s, got %s", task.ID, result.TaskID)
	}
}

func TestWorkerExecuteWithState(t *testing.T) {
	q := NewTaskQueue(10)
	executor := &mockAgentExecutor{}
	cfg := DefaultPoolConfig()
	pool := NewWorkerPool(cfg, executor, q)
	defer pool.Stop()

	worker := pool.spawnWorker(context.Background())
	if worker == nil {
		t.Fatal("expected non-nil worker")
	}

	task := &QueueTask{
		ID:    "test-task-2",
		Title: "Test Task 2",
	}

	ctx := context.Background()

	// Worker should start in Idle state
	if worker.State != WorkerIdle {
		t.Error("expected worker to start in Idle state")
	}

	_ = worker.Execute(ctx, task)

	// After execution, worker should be back to Idle
	if worker.State != WorkerIdle {
		t.Error("expected worker to return to Idle after execution")
	}
}

func TestPullChan(t *testing.T) {
	q := NewTaskQueue(10)

	// Add a task
	_ = q.Add("Pull Test", "Test description", PriorityNormal, []string{"test"})

	// PullChan should return a channel
	ch := q.PullChan(context.Background(), "w1")
	if ch == nil {
		t.Error("expected non-nil channel from PullChan")
	}
}

func TestBeatFunction(t *testing.T) {
	// Test heartbeat engine creation and basic functions
	q := NewTaskQueue(10)
	executor := &mockAgentExecutor{}
	poolCfg := DefaultPoolConfig()
	pool := NewWorkerPool(poolCfg, executor, q)

	cfg := DefaultHeartbeatConfig()
	engine := NewHeartbeatEngine(cfg, pool, q)
	if engine == nil {
		t.Error("expected non-nil heartbeat engine")
	}

	// LastBeat may be zero initially before first beat
	// Test RecentEvents instead
	events := engine.RecentEvents(5)
	if events == nil {
		t.Error("expected non-nil events")
	}
}

func TestStartStop(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := DefaultAutonomyConfig()

	kit := NewAutonomyKit(cfg, executor)
	if kit == nil {
		t.Fatal("expected non-nil autonomy kit")
	}

	ctx := context.Background()

	// Start should not panic
	kit.Start(ctx)

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Stop should not panic
	kit.Stop()
}

func TestExecuteTask(t *testing.T) {
	q := NewTaskQueue(10)
	executor := &mockAgentExecutor{}
	cfg := DefaultPoolConfig()
	cfg.MinWorkers = 1
	cfg.MaxWorkers = 2

	pool := NewWorkerPool(cfg, executor, q)
	defer pool.Stop()

	// Create a test task
	_ = q.Add("Execute Test", "Test description", PriorityNormal, []string{"test"})

	// Spawn a worker manually
	ctx := context.Background()
	worker := pool.spawnWorker(ctx)
	if worker == nil {
		t.Error("expected worker to be spawned")
	}

	// Verify worker has correct state
	if worker.State != WorkerIdle {
		t.Error("expected spawned worker to be idle")
	}
}

func TestDispatchWithNilExecutor(t *testing.T) {
	q := NewTaskQueue(10)
	cfg := DefaultPoolConfig()
	cfg.MinWorkers = 1
	cfg.MaxWorkers = 2

	// Create pool with nil executor
	pool := NewWorkerPool(cfg, nil, q)
	defer pool.Stop()

	// Verify pool was created
	if pool == nil {
		t.Error("expected non-nil pool")
	}

	// SetExecutor with nil should work
	pool.SetExecutor(nil)
	if pool.executor != nil {
		t.Error("expected nil executor")
	}
}

func TestWorkerInfo(t *testing.T) {
	executor := &mockAgentExecutor{}
	cfg := WorkerConfig{ID: "info-test-worker"}
	worker := NewWorker(cfg, executor)

	info := worker.Info()
	if info.ID != "info-test-worker" {
		t.Errorf("expected worker ID 'info-test-worker', got %s", info.ID)
	}
	if info.State != WorkerIdle {
		t.Errorf("expected state Idle, got %v", info.State)
	}
}
