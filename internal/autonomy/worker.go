package autonomy

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// AgentExecutor — interface to break import cycle
// ---------------------------------------------------------------------------

// AgentExecutor is the interface that Agent must implement for Worker to use.
// This breaks the circular dependency: autonomy → agent → autonomy.
type AgentExecutor interface {
	// RunLoopWithSession executes the agent loop with an isolated session.
	RunLoopWithSession(ctx context.Context, sessionID string, userInput string, cfg LoopConfig) (*LoopResult, error)
	// NewSession creates a new isolated session and returns its ID.
	NewSession(title string) string
}

// LoopConfig mirrors agent.LoopConfig to avoid import cycle.
type LoopConfig struct {
	MaxIterations int
	Timeout       time.Duration
	AutoApprove   bool
}

// LoopResult mirrors agent.LoopResult to avoid import cycle.
type LoopResult struct {
	Response   string
	TokensUsed int
	Iterations int
}

// ---------------------------------------------------------------------------
// Worker
// ---------------------------------------------------------------------------

// WorkerState represents the current state of a worker.
type WorkerState string

const (
	WorkerIdle     WorkerState = "idle"
	WorkerBusy     WorkerState = "busy"
	WorkerStopping WorkerState = "stopping"
	WorkerStopped  WorkerState = "stopped"
)

// WorkerResult holds the result of a worker's task execution.
type WorkerResult struct {
	TaskID     string
	Output     string
	Error      error
	Duration   time.Duration
	TokensUsed int
}

// Worker is a lightweight agent instance that can execute tasks independently.
// Each worker has its own session for context isolation, but shares the
// parent Agent's provider and tool registry through the AgentExecutor interface.
type Worker struct {
	ID          string
	State       WorkerState
	Executor    AgentExecutor
	SessionID   string
	CurrentTask *QueueTask

	mu        sync.RWMutex
	startedAt time.Time
	taskCount atomic.Int64
}

// WorkerConfig configures a worker.
type WorkerConfig struct {
	ID           string
	SystemPrompt string // optional override for worker's system prompt
	MaxTokens    int    // max tokens per task (0 = use agent default)
}

// NewWorker creates a new worker bound to an agent executor.
func NewWorker(cfg WorkerConfig, executor AgentExecutor) *Worker {
	sessionID := executor.NewSession(fmt.Sprintf("worker-%s", cfg.ID))

	w := &Worker{
		ID:        cfg.ID,
		State:     WorkerIdle,
		Executor:  executor,
		SessionID: sessionID,
	}

	return w
}

// Execute runs a task through the agent's RunLoop.
// This is the core method — it gives the worker real LLM execution capability.
func (w *Worker) Execute(ctx context.Context, task *QueueTask) *WorkerResult {
	w.mu.Lock()
	w.State = WorkerBusy
	w.CurrentTask = task
	w.mu.Unlock()

	start := time.Now()
	defer func() {
		w.mu.Lock()
		w.State = WorkerIdle
		w.CurrentTask = nil
		w.mu.Unlock()
		w.taskCount.Add(1)
	}()

	// Build the prompt from the task
	prompt := task.Title
	if task.Description != "" {
		prompt = fmt.Sprintf("%s\n\n%s", task.Title, task.Description)
	}
	if len(task.Tags) > 0 {
		prompt = fmt.Sprintf("[tags: %v] %s", task.Tags, prompt)
	}

	// Execute through Agent Loop with session isolation
	loopCfg := LoopConfig{
		MaxIterations: 10,
		Timeout:       120 * time.Second,
		AutoApprove:   true, // workers auto-approve tool calls
	}

	w.mu.RLock()
	executor := w.Executor
	sessionID := w.SessionID
	w.mu.RUnlock()

	if executor == nil {
		return &WorkerResult{
			TaskID:   task.ID,
			Error:    fmt.Errorf("worker %s has no executor", w.ID),
			Duration: time.Since(start),
		}
	}

	result, err := executor.RunLoopWithSession(ctx, sessionID, prompt, loopCfg)

	duration := time.Since(start)

	wr := &WorkerResult{
		TaskID:   task.ID,
		Duration: duration,
	}

	if err != nil {
		wr.Error = err
		return wr
	}

	wr.Output = result.Response
	wr.TokensUsed = result.TokensUsed
	return wr
}

// TaskCount returns the total number of tasks this worker has completed.
func (w *Worker) TaskCount() int64 {
	return w.taskCount.Load()
}

// Info returns worker info for status queries.
func (w *Worker) Info() WorkerInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()

	info := WorkerInfo{
		ID:        w.ID,
		State:     w.State,
		TaskCount: w.taskCount.Load(),
		StartedAt: w.startedAt,
	}

	if w.CurrentTask != nil {
		info.CurrentTaskID = w.CurrentTask.ID
		info.CurrentTaskTitle = w.CurrentTask.Title
	}

	return info
}

// WorkerInfo is a snapshot of worker state.
type WorkerInfo struct {
	ID               string      `json:"id"`
	State            WorkerState `json:"state"`
	CurrentTaskID    string      `json:"current_task_id,omitempty"`
	CurrentTaskTitle string      `json:"current_task_title,omitempty"`
	TaskCount        int64       `json:"task_count"`
	StartedAt        time.Time   `json:"started_at,omitempty"`
}

// ---------------------------------------------------------------------------
// WorkerPool
// ---------------------------------------------------------------------------

// PoolConfig configures the worker pool.
type PoolConfig struct {
	MaxWorkers  int           // maximum concurrent workers (default: 8)
	TaskTimeout time.Duration // per-task timeout (default: 120s)
	QueueBuffer int           // task queue buffer size (default: 64)
	AutoScale   bool          // auto-scale workers based on queue depth
	MinWorkers  int           // minimum workers when auto-scaling (default: 1)
}

// DefaultPoolConfig returns sensible defaults.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxWorkers:  8,
		TaskTimeout: 120 * time.Second,
		QueueBuffer: 64,
		AutoScale:   false,
		MinWorkers:  1,
	}
}

// WorkerPool manages a pool of goroutine-based workers.
// This is where Go's concurrency advantage shines:
//   - Workers are lightweight goroutines, not OS threads
//   - Channel-based communication eliminates lock contention
//   - Backpressure via buffered task channel
//   - Graceful shutdown with context cancellation
type WorkerPool struct {
	config   PoolConfig
	executor AgentExecutor
	queue    *TaskQueue

	mu      sync.RWMutex
	workers map[string]*Worker
	nextID  atomic.Int64
	running atomic.Bool
	stopCh  chan struct{}

	// Results channel — non-blocking, consumers drain at their pace
	results chan *WorkerResult

	// Metrics
	totalTasks    atomic.Int64
	failedTasks   atomic.Int64
	totalDuration atomic.Int64 // nanoseconds
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(cfg PoolConfig, executor AgentExecutor, queue *TaskQueue) *WorkerPool {
	return &WorkerPool{
		config:   cfg,
		executor: executor,
		queue:    queue,
		workers:  make(map[string]*Worker),
		stopCh:   make(chan struct{}),
		results:  make(chan *WorkerResult, cfg.QueueBuffer),
	}
}

// Start starts the worker pool.
func (p *WorkerPool) Start(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return fmt.Errorf("worker pool already running")
	}

	// Spawn initial workers
	minWorkers := p.config.MinWorkers
	if minWorkers < 1 {
		minWorkers = 1
	}
	for i := 0; i < minWorkers; i++ {
		p.spawnWorker(ctx)
	}

	// Start the dispatcher
	go p.dispatch(ctx)

	return nil
}

// Stop gracefully stops the worker pool.
func (p *WorkerPool) Stop() error {
	if !p.running.CompareAndSwap(true, false) {
		return fmt.Errorf("worker pool not running")
	}

	close(p.stopCh)

	// Mark all workers as stopping
	p.mu.Lock()
	for _, w := range p.workers {
		w.mu.Lock()
		w.State = WorkerStopping
		w.mu.Unlock()
	}
	p.mu.Unlock()

	return nil
}

// spawnWorker creates and registers a new worker.
func (p *WorkerPool) spawnWorker(ctx context.Context) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := fmt.Sprintf("worker-%d", p.nextID.Add(1))
	var worker *Worker
	if p.executor != nil {
		worker = NewWorker(WorkerConfig{ID: id}, p.executor)
	} else {
		// No executor yet, create placeholder
		worker = &Worker{
			ID:    id,
			State: WorkerIdle,
		}
	}
	worker.startedAt = time.Now()
	p.workers[id] = worker

	return worker
}

// SetExecutor sets the agent executor (can be called after creation).
func (p *WorkerPool) SetExecutor(executor AgentExecutor) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.executor = executor

	for _, worker := range p.workers {
		worker.mu.Lock()
		worker.Executor = executor
		if executor != nil && worker.SessionID == "" {
			worker.SessionID = executor.NewSession(fmt.Sprintf("worker-%s", worker.ID))
		}
		worker.mu.Unlock()
	}
}

// dispatch is the main loop that assigns tasks to idle workers.
func (p *WorkerPool) dispatch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		default:
		}

		// Find an idle worker
		worker := p.findIdleWorker()
		if worker == nil {
			// Try to spawn a new one if under limit
			p.mu.RLock()
			count := len(p.workers)
			p.mu.RUnlock()

			if count < p.config.MaxWorkers {
				worker = p.spawnWorker(ctx)
			} else {
				// All workers busy, wait a bit
				select {
				case <-ctx.Done():
					return
				case <-time.After(200 * time.Millisecond):
					continue
				}
			}
		}

		// Skip if worker has no executor
		worker.mu.RLock()
		hasExecutor := worker.Executor != nil
		worker.mu.RUnlock()
		if !hasExecutor {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		// Pull a task from the queue
		taskCh := p.queue.PullChan(ctx, worker.ID)
		var task *QueueTask
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case t := <-taskCh:
			task = t
		}

		if task == nil {
			continue
		}

		// Dispatch to worker goroutine
		go p.executeTask(ctx, worker, task)
	}
}

// executeTask runs a task on a worker and handles the result.
func (p *WorkerPool) executeTask(ctx context.Context, w *Worker, task *QueueTask) {
	taskCtx, cancel := context.WithTimeout(ctx, p.config.TaskTimeout)
	defer cancel()

	result := w.Execute(taskCtx, task)

	// Update queue based on result
	if result.Error != nil {
		p.queue.Fail(task.ID, result.Error.Error(), true) // retry on failure
		p.failedTasks.Add(1)
	} else {
		p.queue.Complete(task.ID, result.Output)
	}

	p.totalTasks.Add(1)
	p.totalDuration.Add(int64(result.Duration))

	// Non-blocking send result
	select {
	case p.results <- result:
	default:
		// consumer not ready, drop (task result is already in queue)
	}
}

// findIdleWorker finds an idle worker in the pool.
func (p *WorkerPool) findIdleWorker() *Worker {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, w := range p.workers {
		w.mu.RLock()
		state := w.State
		w.mu.RUnlock()
		if state == WorkerIdle {
			return w
		}
	}
	return nil
}

// Results returns the channel for consuming worker results.
func (p *WorkerPool) Results() <-chan *WorkerResult {
	return p.results
}

// ListWorkers returns info about all workers.
func (p *WorkerPool) ListWorkers() []WorkerInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make([]WorkerInfo, 0, len(p.workers))
	for _, w := range p.workers {
		result = append(result, w.Info())
	}
	return result
}

// Stats returns pool statistics.
func (p *WorkerPool) Stats() PoolStats {
	p.mu.RLock()
	workerCount := len(p.workers)
	p.mu.RUnlock()

	idle, busy, stopped := 0, 0, 0
	for _, w := range p.ListWorkers() {
		switch w.State {
		case WorkerIdle:
			idle++
		case WorkerBusy:
			busy++
		case WorkerStopped, WorkerStopping:
			stopped++
		}
	}

	var avgDuration time.Duration
	total := p.totalTasks.Load()
	if total > 0 {
		avgDuration = time.Duration(p.totalDuration.Load() / total)
	}

	return PoolStats{
		WorkerCount:    workerCount,
		IdleWorkers:    idle,
		BusyWorkers:    busy,
		StoppedWorkers: stopped,
		TotalTasks:     total,
		FailedTasks:    p.failedTasks.Load(),
		AvgDuration:    avgDuration,
		Running:        p.running.Load(),
	}
}

// PoolStats holds worker pool statistics.
type PoolStats struct {
	WorkerCount    int           `json:"worker_count"`
	IdleWorkers    int           `json:"idle_workers"`
	BusyWorkers    int           `json:"busy_workers"`
	StoppedWorkers int           `json:"stopped_workers"`
	TotalTasks     int64         `json:"total_tasks"`
	FailedTasks    int64         `json:"failed_tasks"`
	AvgDuration    time.Duration `json:"avg_duration"`
	Running        bool          `json:"running"`
}

// ScaleUp adds more workers to the pool.
func (p *WorkerPool) ScaleUp(ctx context.Context, count int) error {
	p.mu.RLock()
	current := len(p.workers)
	p.mu.RUnlock()

	if current+count > p.config.MaxWorkers {
		count = p.config.MaxWorkers - current
		if count <= 0 {
			return fmt.Errorf("already at max workers (%d)", p.config.MaxWorkers)
		}
	}

	for i := 0; i < count; i++ {
		p.spawnWorker(ctx)
	}
	return nil
}

// ScaleDown removes idle workers from the pool.
func (p *WorkerPool) ScaleDown(count int) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	removed := 0
	for id, w := range p.workers {
		if removed >= count {
			break
		}
		w.mu.RLock()
		state := w.State
		w.mu.RUnlock()
		if state == WorkerIdle {
			w.mu.Lock()
			w.State = WorkerStopped
			w.mu.Unlock()
			delete(p.workers, id)
			removed++
		}
	}
	return removed
}
