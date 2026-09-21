package autonomy

import (
	"context"
	"errors"
	"fmt"
	"log"
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

// LoopConfig carries agent loop limits without importing the agent package.
type LoopConfig struct {
	Execution              *Execution
	MaxIterations          int
	Timeout                time.Duration
	AutoApprove            bool
	AutoApproveSet         bool
	RepeatToolCallLimit    int
	ToolOnlyIterationLimit int
	DuplicateFetchLimit    int
	DisabledTools          []string
}

// DefaultWorkerLoopConfig returns the default loop limits for autonomy workers.
func DefaultWorkerLoopConfig() LoopConfig {
	return LoopConfig{
		MaxIterations:          300,
		Timeout:                300 * time.Second,
		AutoApprove:            true,
		AutoApproveSet:         true,
		RepeatToolCallLimit:    300,
		ToolOnlyIterationLimit: 300,
		DuplicateFetchLimit:    300,
		DisabledTools:          []string{"autonomy"},
	}
}

func normalizeWorkerLoopConfig(cfg LoopConfig) LoopConfig {
	def := DefaultWorkerLoopConfig()
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = def.MaxIterations
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = def.Timeout
	}
	if !cfg.AutoApproveSet {
		cfg.AutoApprove = def.AutoApprove
		cfg.AutoApproveSet = true
	}
	if cfg.RepeatToolCallLimit <= 0 {
		cfg.RepeatToolCallLimit = def.RepeatToolCallLimit
	}
	if cfg.ToolOnlyIterationLimit <= 0 {
		cfg.ToolOnlyIterationLimit = def.ToolOnlyIterationLimit
	}
	if cfg.DuplicateFetchLimit <= 0 {
		cfg.DuplicateFetchLimit = def.DuplicateFetchLimit
	}
	if cfg.DisabledTools == nil {
		cfg.DisabledTools = append([]string(nil), def.DisabledTools...)
	} else {
		cfg.DisabledTools = append([]string{}, cfg.DisabledTools...)
	}
	return cfg
}

// LoopResult mirrors agent.LoopResult to avoid import cycle.
type LoopResult struct {
	Response     string
	TokensUsed   int
	Iterations   int
	Verified     bool
	Verification string
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
	TaskID       string
	Output       string
	Error        error
	Duration     time.Duration
	TokensUsed   int
	Verified     bool
	Verification string
	State        TaskState
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
	LoopConfig  LoopConfig

	mu        sync.RWMutex
	startedAt time.Time
	taskCount atomic.Int64
	queue     *TaskQueue
	reserved  bool
}

// WorkerConfig configures a worker.
type WorkerConfig struct {
	Queue        *TaskQueue
	ID           string
	SystemPrompt string // optional override for worker's system prompt
	MaxTokens    int    // max tokens per task (0 = use agent default)
	LoopConfig   LoopConfig
}

// NewWorker creates a new worker bound to an agent executor.
func NewWorker(cfg WorkerConfig, executor AgentExecutor) *Worker {
	w := &Worker{
		ID:         cfg.ID,
		State:      WorkerIdle,
		Executor:   executor,
		queue:      cfg.Queue,
		LoopConfig: normalizeWorkerLoopConfig(cfg.LoopConfig),
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
		if !w.reserved {
			w.State = WorkerIdle
		}
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
	loopCfg := normalizeWorkerLoopConfig(w.LoopConfig)

	w.mu.RLock()
	executor := w.Executor
	sessionID := task.SessionID
	w.mu.RUnlock()

	if executor == nil {
		return &WorkerResult{
			TaskID:   task.ID,
			Error:    fmt.Errorf("worker %s has no executor", w.ID),
			Duration: time.Since(start),
		}
	}

	if w.queue != nil {
		loopCfg.Execution = NewExecution(w.queue, task)
	}
	if sessionID == "" {
		sessionID = executor.NewSession("task-" + task.ID)
		if loopCfg.Execution != nil {
			if err := loopCfg.Execution.BindSession(sessionID); err != nil {
				return &WorkerResult{TaskID: task.ID, Error: fmt.Errorf("%w: save task session: %v", ErrBlocked, err)}
			}
		}
	}
	w.mu.Lock()
	w.SessionID = sessionID
	w.mu.Unlock()
	result, err := executor.RunLoopWithSession(ctx, sessionID, prompt, loopCfg)

	duration := time.Since(start)

	wr := &WorkerResult{
		TaskID:   task.ID,
		Duration: duration,
	}

	if result != nil {
		wr.Output, wr.TokensUsed = result.Response, result.TokensUsed
		wr.Verified, wr.Verification = result.Verified, result.Verification
	}
	if err != nil {
		wr.Error = err
		return wr
	}

	if result == nil {
		wr.Error = fmt.Errorf("executor returned no result")
	}
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
	RunPolicy   RunPolicy
	MaxWorkers  int           // maximum concurrent workers (default: 8)
	TaskTimeout time.Duration // per-task timeout (default: 300s)
	QueueBuffer int           // task queue buffer size (default: 64)
	AutoScale   bool          // auto-scale workers based on queue depth
	MinWorkers  int           // minimum workers when auto-scaling (default: 1)
	WorkerLoop  LoopConfig    // agent loop limits used by workers
}

// DefaultPoolConfig returns sensible defaults.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		RunPolicy:   DefaultRunPolicy(),
		MaxWorkers:  8,
		TaskTimeout: 300 * time.Second,
		QueueBuffer: 64,
		AutoScale:   false,
		MinWorkers:  1,
		WorkerLoop:  DefaultWorkerLoopConfig(),
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

	mu       sync.RWMutex
	workers  map[string]*Worker
	nextID   atomic.Int64
	running  atomic.Bool
	stopCh   chan struct{}
	wg       sync.WaitGroup
	stopping bool

	// Results channel — non-blocking, consumers drain at their pace
	results chan *WorkerResult

	// Metrics
	totalTasks    atomic.Int64
	failedTasks   atomic.Int64
	totalDuration atomic.Int64 // nanoseconds
}

// NewWorkerPool creates a new worker pool.
func NewWorkerPool(cfg PoolConfig, executor AgentExecutor, queue *TaskQueue) *WorkerPool {
	if cfg.QueueBuffer <= 0 {
		cfg.QueueBuffer = 64
	}
	cfg.WorkerLoop = normalizeWorkerLoopConfig(cfg.WorkerLoop)
	if cfg.TaskTimeout <= 0 {
		cfg.TaskTimeout = 300 * time.Second
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = 8
	}
	if cfg.MinWorkers <= 0 {
		cfg.MinWorkers = 1
	}
	if cfg.MinWorkers > cfg.MaxWorkers {
		cfg.MinWorkers = cfg.MaxWorkers
	}
	if cfg.RunPolicy == (RunPolicy{}) {
		cfg.RunPolicy = DefaultRunPolicy()
	}
	cfg.RunPolicy = normalizeRunPolicy(cfg.RunPolicy)
	queue.policy = cfg.RunPolicy
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
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return fmt.Errorf("worker pool is still stopping")
	}
	if !p.running.CompareAndSwap(false, true) {
		p.mu.Unlock()
		return fmt.Errorf("worker pool already running")
	}
	p.stopCh = make(chan struct{})
	p.workers = make(map[string]*Worker)
	p.wg.Add(1)
	p.mu.Unlock()

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
	p.mu.Lock()
	if !p.running.CompareAndSwap(true, false) {
		p.mu.Unlock()
		return fmt.Errorf("worker pool not running")
	}
	p.stopping = true
	close(p.stopCh)

	// Mark all workers as stopping
	for _, w := range p.workers {
		w.mu.Lock()
		w.State = WorkerStopping
		w.mu.Unlock()
	}
	p.mu.Unlock()
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		p.mu.Lock()
		p.stopping = false
		p.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("workers still stopping; a tool has not acknowledged cancellation")
	}
}

// spawnWorker creates and registers a new worker.
func (p *WorkerPool) spawnWorker(ctx context.Context) *Worker {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.workers) >= p.config.MaxWorkers || p.stopping {
		return nil
	}

	id := fmt.Sprintf("worker-%d", p.nextID.Add(1))
	var worker *Worker
	if p.executor != nil {
		worker = NewWorker(WorkerConfig{ID: id, LoopConfig: p.config.WorkerLoop, Queue: p.queue}, p.executor)
	} else {
		// No executor yet, create placeholder
		worker = &Worker{
			ID:         id,
			State:      WorkerIdle,
			LoopConfig: p.config.WorkerLoop,
			queue:      p.queue,
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
		worker.mu.Unlock()
	}
}

// dispatch is the main loop that assigns tasks to idle workers.
func (p *WorkerPool) dispatch(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		default:
		}

		if p.config.AutoScale && p.findIdleWorker() == nil {
			p.mu.RLock()
			count := len(p.workers)
			p.mu.RUnlock()
			if count < p.config.MaxWorkers {
				p.spawnWorker(ctx)
			}
		}
		if _, _, err := p.dispatchOne(ctx, ""); err != nil {
			log.Printf("[autonomy] dispatch: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
		}
	}
}

// dispatchOne serializes reservation across the dispatcher, heartbeat and
// explicit spawn requests. No worker is exposed as idle after a task is claimed.
func (p *WorkerPool) dispatchOne(ctx context.Context, taskID string) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running.Load() || ctx.Err() != nil {
		return "", "", nil
	}
	for _, w := range p.workers {
		w.mu.Lock()
		if w.State != WorkerIdle || w.reserved || w.Executor == nil {
			w.mu.Unlock()
			continue
		}
		t, err := p.queue.claim(w.ID, taskID)
		if err != nil || t == nil {
			w.mu.Unlock()
			return "", "", err
		}
		w.State, w.reserved = WorkerBusy, true
		w.mu.Unlock()
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer func() {
				w.mu.Lock()
				w.reserved = false
				if p.running.Load() {
					w.State = WorkerIdle
				} else {
					w.State = WorkerStopped
				}
				w.mu.Unlock()
			}()
			p.executeTask(ctx, w, t)
		}()
		return t.ID, w.ID, nil
	}
	return "", "", nil
}

// executeTask runs a task on a worker and handles the result.
func (p *WorkerPool) executeTask(ctx context.Context, w *Worker, task *QueueTask) {
	timeout := p.config.TaskTimeout
	if remaining := time.Until(task.FirstStartedAt.Add(p.config.RunPolicy.MaxTotalTime)); !task.FirstStartedAt.IsZero() && remaining < timeout {
		timeout = remaining
	}
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	p.mu.RLock()
	stopCh := p.stopCh
	p.mu.RUnlock()
	go func() {
		select {
		case <-stopCh:
			cancel()
		case <-taskCtx.Done():
		}
	}()

	result := w.Execute(taskCtx, task)

	if result.Error != nil && !errors.Is(result.Error, ErrYield) {
		p.failedTasks.Add(1)
	}
	if err := p.queue.finishAttempt(task, result, errors.Is(taskCtx.Err(), context.Canceled)); err != nil {
		if !errors.Is(err, ErrStaleExecution) {
			log.Printf("[autonomy] persist result for %s: %v", task.ID, err)
			result.Error = fmt.Errorf("persist task result: %w", err)
		}
	}
	if current, ok := p.queue.Get(task.ID); ok {
		result.State = current.State
		if current.State == TaskBlocked && result.Error == nil {
			result.Error = errors.New(current.BlockReason)
		}
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
		reserved := w.reserved
		w.mu.RUnlock()
		if state == WorkerIdle && !reserved {
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
		if p.spawnWorker(ctx) == nil {
			break
		}
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
