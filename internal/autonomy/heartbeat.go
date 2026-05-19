package autonomy

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Heartbeat Engine
// ---------------------------------------------------------------------------

// HeartbeatMode determines what the heartbeat does.
type HeartbeatMode string

const (
	// HeartbeatPassive only checks for urgent items (traditional behavior).
	HeartbeatPassive HeartbeatMode = "passive"
	// HeartbeatProactive checks urgent items AND pulls work from the queue.
	HeartbeatProactive HeartbeatMode = "proactive"
)

// HeartbeatConfig configures the heartbeat engine.
type HeartbeatConfig struct {
	Mode            HeartbeatMode    // proactive or passive
	Interval        time.Duration    // how often heartbeat fires
	ActiveStart     int              // active hours start (hour, 0-23), e.g. 6
	ActiveEnd       int              // active hours end (hour, 0-23), e.g. 23
	MaxTasksPerBeat int              // max tasks to process per heartbeat
	OnUrgent        func(msg string) // callback for urgent items
}

// DefaultHeartbeatConfig returns sensible defaults.
func DefaultHeartbeatConfig() HeartbeatConfig {
	return HeartbeatConfig{
		Mode:            HeartbeatProactive,
		Interval:        15 * time.Minute,
		ActiveStart:     6,
		ActiveEnd:       23,
		MaxTasksPerBeat: 3,
	}
}

// HeartbeatEvent represents a heartbeat event.
type HeartbeatEvent struct {
	Timestamp   time.Time
	Mode        HeartbeatMode
	TasksPulled int
	TasksDone   int
	TasksFailed int
	Actions     []string // log of actions taken
}

// HeartbeatEngine drives proactive agent work.
// Instead of the traditional "HEARTBEAT_OK" pattern, this engine
// actively pulls tasks from the queue and executes them.
type HeartbeatEngine struct {
	config HeartbeatConfig
	pool   *WorkerPool
	queue  *TaskQueue

	mu       sync.RWMutex
	running  bool
	stopCh   chan struct{}
	events   []HeartbeatEvent
	lastBeat time.Time
}

// NewHeartbeatEngine creates a new heartbeat engine.
func NewHeartbeatEngine(cfg HeartbeatConfig, pool *WorkerPool, queue *TaskQueue) *HeartbeatEngine {
	return &HeartbeatEngine{
		config: cfg,
		pool:   pool,
		queue:  queue,
		stopCh: make(chan struct{}),
		events: make([]HeartbeatEvent, 0, 100),
	}
}

// Start begins the heartbeat loop.
func (h *HeartbeatEngine) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return fmt.Errorf("heartbeat engine already running")
	}
	h.running = true
	h.mu.Unlock()

	h.beat(ctx)
	go h.loop(ctx)
	return nil
}

// Stop stops the heartbeat loop.
func (h *HeartbeatEngine) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.running {
		return fmt.Errorf("heartbeat engine not running")
	}
	h.running = false
	close(h.stopCh)
	return nil
}

// Trigger manually triggers a heartbeat cycle.
func (h *HeartbeatEngine) Trigger(ctx context.Context) *HeartbeatEvent {
	return h.beat(ctx)
}

// loop is the main heartbeat loop.
func (h *HeartbeatEngine) loop(ctx context.Context) {
	ticker := time.NewTicker(h.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-h.stopCh:
			return
		case now := <-ticker.C:
			// Check active hours
			if !h.isActiveHour(now) {
				continue
			}
			h.beat(ctx)
		}
	}
}

// isActiveHour checks if the current hour is within active hours.
func (h *HeartbeatEngine) isActiveHour(t time.Time) bool {
	hour := t.Hour()
	if h.config.ActiveStart <= h.config.ActiveEnd {
		return hour >= h.config.ActiveStart && hour < h.config.ActiveEnd
	}
	// Wraps midnight, e.g. 22-6
	return hour >= h.config.ActiveStart || hour < h.config.ActiveEnd
}

// beat executes one heartbeat cycle.
func (h *HeartbeatEngine) beat(ctx context.Context) *HeartbeatEvent {
	event := HeartbeatEvent{
		Timestamp: time.Now(),
		Mode:      h.config.Mode,
	}

	// Phase 1: Check for urgent items
	// (In a real implementation, this would check messages, blockers, etc.)
	// For now, we check blocked tasks that might need attention.
	blocked := h.queue.ListByState(TaskBlocked)
	if len(blocked) > 0 && h.config.OnUrgent != nil {
		for _, t := range blocked {
			h.config.OnUrgent(fmt.Sprintf("Blocked task: %s — %s", t.Title, t.BlockReason))
		}
		event.Actions = append(event.Actions, fmt.Sprintf("Checked %d blocked tasks", len(blocked)))
	}

	// Phase 2: Proactive work mode
	if h.config.Mode == HeartbeatProactive && h.pool != nil {
		ready, inProgress, _, _ := h.queue.Stats()

		// Only pull if there's capacity and tasks available
		poolStats := h.pool.Stats()
		availableSlots := poolStats.IdleWorkers

		if ready > 0 && availableSlots > 0 {
			tasksToPull := min(ready, availableSlots, h.config.MaxTasksPerBeat)

			for i := 0; i < tasksToPull; i++ {
				task := h.queue.Pull(fmt.Sprintf("heartbeat-%d", i))
				if task == nil {
					break
				}
				event.TasksPulled++

				// Find an idle worker and execute
				worker := h.pool.findIdleWorker()
				if worker == nil {
					// No worker available, put task back
					h.queue.Fail(task.ID, "no idle worker", true)
					break
				}

				go h.pool.executeTask(ctx, worker, task)
				event.Actions = append(event.Actions, fmt.Sprintf("Dispatched task %s to %s", task.ID, worker.ID))
			}
		}

		event.Actions = append(event.Actions, fmt.Sprintf("Queue: %d ready, %d in-progress, pool: %d idle/%d busy",
			ready, inProgress, poolStats.IdleWorkers, poolStats.BusyWorkers))
	}

	// Record event
	h.mu.Lock()
	h.lastBeat = time.Now()
	if len(h.events) < 1000 {
		h.events = append(h.events, event)
	}
	h.mu.Unlock()

	return &event
}

// LastBeat returns the time of the last heartbeat.
func (h *HeartbeatEngine) LastBeat() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastBeat
}

// RecentEvents returns the last N heartbeat events.
func (h *HeartbeatEngine) RecentEvents(n int) []HeartbeatEvent {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if n > len(h.events) {
		n = len(h.events)
	}
	result := make([]HeartbeatEvent, n)
	copy(result, h.events[len(h.events)-n:])
	return result
}

func min(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

// ---------------------------------------------------------------------------
// AutonomyKit — Top-level orchestrator
// ---------------------------------------------------------------------------

// AutonomyConfig configures the autonomy kit.
type AutonomyConfig struct {
	Pool      PoolConfig
	Heartbeat HeartbeatConfig
	QueueBuf  int
}

// DefaultAutonomyConfig returns sensible defaults.
func DefaultAutonomyConfig() AutonomyConfig {
	return AutonomyConfig{
		Pool:      DefaultPoolConfig(),
		Heartbeat: DefaultHeartbeatConfig(),
		QueueBuf:  64,
	}
}

// AutonomyKit is the top-level orchestrator for autonomous agent work.
// It combines WorkerPool, TaskQueue, and HeartbeatEngine into a cohesive
// system that enables agents to work proactively without human prompting.
//
// Architecture:
//
//	┌─────────────────────────────────────────────┐
//	│              AutonomyKit                     │
//	│                                              │
//	│  ┌──────────┐  ┌──────────┐  ┌───────────┐ │
//	│  │TaskQueue │──│WorkerPool│──│Heartbeat   │ │
//	│  │          │  │          │  │Engine      │ │
//	│  │ Ready ──→│  │ W1 ──→  │  │ (proactive)│ │
//	│  │ InProg   │  │ W2 ──→  │  │            │ │
//	│  │ Blocked  │  │ W3 ──→  │  │ 15m cycle  │ │
//	│  │ Done     │  │ ...     │  │            │ │
//	│  └──────────┘  └──────────┘  └───────────┘ │
//	│       ↑              │                      │
//	│       │    ┌─────────┘                      │
//	│       │    ↓                                │
//	│  ┌──────────────────────┐                   │
//	│  │  AgentExecutor       │                   │
//	│  │  (interface)         │                   │
//	│  │  (isolated session)  │                   │
//	│  └──────────────────────┘                   │
//	└─────────────────────────────────────────────┘
type AutonomyKit struct {
	config    AutonomyConfig
	queue     *TaskQueue
	pool      *WorkerPool
	heartbeat *HeartbeatEngine
	executor  AgentExecutor

	mu      sync.RWMutex
	started bool
}

// NewAutonomyKit creates a new autonomy kit.
func NewAutonomyKit(cfg AutonomyConfig, executor AgentExecutor) *AutonomyKit {
	queue := NewTaskQueue(cfg.QueueBuf)
	pool := NewWorkerPool(cfg.Pool, executor, queue)
	hb := NewHeartbeatEngine(cfg.Heartbeat, pool, queue)

	return &AutonomyKit{
		config:    cfg,
		queue:     queue,
		pool:      pool,
		heartbeat: hb,
		executor:  executor,
	}
}

// Start starts the autonomy kit (worker pool + heartbeat engine).
func (ak *AutonomyKit) Start(ctx context.Context) error {
	ak.mu.Lock()
	defer ak.mu.Unlock()

	if ak.started {
		return fmt.Errorf("autonomy kit already started")
	}

	if err := ak.pool.Start(ctx); err != nil {
		return fmt.Errorf("failed to start worker pool: %w", err)
	}

	if err := ak.heartbeat.Start(ctx); err != nil {
		ak.pool.Stop()
		return fmt.Errorf("failed to start heartbeat engine: %w", err)
	}

	ak.started = true
	log.Printf("[autonomy] kit started: pool=%d workers, heartbeat=%s mode, interval=%s",
		ak.config.Pool.MinWorkers, ak.config.Heartbeat.Mode, ak.config.Heartbeat.Interval)

	return nil
}

// Stop stops the autonomy kit.
func (ak *AutonomyKit) Stop() error {
	ak.mu.Lock()
	defer ak.mu.Unlock()

	if !ak.started {
		return fmt.Errorf("autonomy kit not started")
	}

	if err := ak.heartbeat.Stop(); err != nil {
		log.Printf("[autonomy] heartbeat stop error: %v", err)
	}

	if err := ak.pool.Stop(); err != nil {
		log.Printf("[autonomy] pool stop error: %v", err)
	}

	ak.started = false
	return nil
}

// Queue returns the task queue for direct manipulation.
func (ak *AutonomyKit) Queue() *TaskQueue {
	return ak.queue
}

// EnablePersistence loads and enables durable queue state.
func (ak *AutonomyKit) EnablePersistence(path string) (int, error) {
	if ak == nil || ak.queue == nil {
		return 0, nil
	}
	return ak.queue.EnablePersistence(path)
}

// Pool returns the worker pool.
func (ak *AutonomyKit) Pool() *WorkerPool {
	return ak.pool
}

// SetExecutor updates the executor used by the worker pool.
func (ak *AutonomyKit) SetExecutor(executor AgentExecutor) {
	ak.mu.Lock()
	defer ak.mu.Unlock()

	ak.executor = executor
	ak.pool.SetExecutor(executor)
}

// Heartbeat returns the heartbeat engine.
func (ak *AutonomyKit) Heartbeat() *HeartbeatEngine {
	return ak.heartbeat
}

// AddTask is a convenience method to add a task to the queue.
func (ak *AutonomyKit) AddTask(title, description string, priority TaskPriority, tags []string) *QueueTask {
	return ak.queue.Add(title, description, priority, tags)
}

// AddTaskWithError adds a task and surfaces persistence failures.
func (ak *AutonomyKit) AddTaskWithError(title, description string, priority TaskPriority, tags []string) (*QueueTask, error) {
	return ak.queue.AddWithError(title, description, priority, tags)
}

// ScaleUp adds workers to the pool.
func (ak *AutonomyKit) ScaleUp(ctx context.Context, count int) error {
	if ak == nil || ak.pool == nil {
		return fmt.Errorf("autonomy kit not initialized")
	}
	return ak.pool.ScaleUp(ctx, count)
}

// ScaleDown removes up to count idle workers from the pool.
func (ak *AutonomyKit) ScaleDown(count int) int {
	if ak == nil || ak.pool == nil {
		return 0
	}
	return ak.pool.ScaleDown(count)
}

// SetWorkerCount adjusts the pool toward an absolute worker count.
func (ak *AutonomyKit) SetWorkerCount(ctx context.Context, count int) (int, error) {
	if ak == nil || ak.pool == nil {
		return 0, fmt.Errorf("autonomy kit not initialized")
	}
	if count < 0 {
		return 0, fmt.Errorf("worker count must be non-negative")
	}
	current := ak.pool.Stats().WorkerCount
	if count > current {
		if err := ak.pool.ScaleUp(ctx, count-current); err != nil {
			return ak.pool.Stats().WorkerCount, err
		}
		return ak.pool.Stats().WorkerCount, nil
	}
	if count < current {
		ak.pool.ScaleDown(current - count)
	}
	return ak.pool.Stats().WorkerCount, nil
}

// Status returns the overall autonomy kit status.
func (ak *AutonomyKit) Status() AutonomyStatus {
	ak.mu.RLock()
	started := ak.started
	ak.mu.RUnlock()

	ready, inProgress, blocked, done := ak.queue.Stats()
	poolStats := ak.pool.Stats()

	return AutonomyStatus{
		Started:         started,
		QueueReady:      ready,
		QueueInProgress: inProgress,
		QueueBlocked:    blocked,
		QueueDone:       done,
		PoolStats:       poolStats,
		LastHeartbeat:   ak.heartbeat.LastBeat(),
		QueueStore:      ak.queue.PersistencePath(),
	}
}

// AutonomyStatus is a snapshot of the autonomy kit state.
type AutonomyStatus struct {
	Started         bool      `json:"started"`
	QueueReady      int       `json:"queue_ready"`
	QueueInProgress int       `json:"queue_in_progress"`
	QueueBlocked    int       `json:"queue_blocked"`
	QueueDone       int       `json:"queue_done"`
	PoolStats       PoolStats `json:"pool_stats"`
	LastHeartbeat   time.Time `json:"last_heartbeat"`
	QueueStore      string    `json:"queue_store,omitempty"`
}
