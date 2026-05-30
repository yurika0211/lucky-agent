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

/**
 * HeartbeatMode 确定心跳引擎的行为。
 */
type HeartbeatMode string

const (
	// HeartbeatPassive only checks for urgent items (traditional behavior).
	HeartbeatPassive HeartbeatMode = "passive"
	// HeartbeatProactive checks urgent items AND pulls work from the queue.
	HeartbeatProactive HeartbeatMode = "proactive"
)

// HeartbeatConfig 配置心跳引擎。
type HeartbeatConfig struct {
	Mode            HeartbeatMode    // proactive or passive
	Interval        time.Duration    // how often heartbeat fires
	ActiveStart     int              // active hours start (hour, 0-23), e.g. 6
	ActiveEnd       int              // active hours end (hour, 0-23), e.g. 23
	MaxTasksPerBeat int              // max tasks to process per heartbeat
	OnUrgent        func(msg string) // callback for urgent items
}

/**
 * DefaultHeartbeatConfig 返回合理的默认值。
 */
func DefaultHeartbeatConfig() HeartbeatConfig {
	return HeartbeatConfig{
		Mode:            HeartbeatProactive,
		Interval:        15 * time.Minute,
		ActiveStart:     6,
		ActiveEnd:       23,
		MaxTasksPerBeat: 3,
	}
}

/**
 * HeartbeatEvent 表示心跳事件。
 */
type HeartbeatEvent struct {
	Timestamp   time.Time
	Mode        HeartbeatMode
	TasksPulled int
	TasksDone   int
	TasksFailed int
	Actions     []string // log of actions taken
}

/**
 * HeartbeatEngine 驱动主动代理工作。
 * 与传统的 "HEARTBEAT_OK" 模式不同，此引擎
 * 从队列中主动拉取任务并执行。
 */
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

/**
 * NewHeartbeatEngine 创建一个新的心跳引擎。
 */
func NewHeartbeatEngine(cfg HeartbeatConfig, pool *WorkerPool, queue *TaskQueue) *HeartbeatEngine {
	return &HeartbeatEngine{
		config: cfg,
		pool:   pool,
		queue:  queue,
		stopCh: make(chan struct{}),
		events: make([]HeartbeatEvent, 0, 100),
	}
}

/**
 * Start 开始心跳循环。
 */
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

/**
 * Stop 停止心跳循环。
 */
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

/**
 * Trigger 手动触发一次心跳周期。
 */
func (h *HeartbeatEngine) Trigger(ctx context.Context) *HeartbeatEvent {
	return h.beat(ctx)
}

/**
 * loop 是主心跳循环。
 */
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

/**
 * isActiveHour 检查当前小时是否在活动小时内。
 */
func (h *HeartbeatEngine) isActiveHour(t time.Time) bool {
	hour := t.Hour()
	if h.config.ActiveStart <= h.config.ActiveEnd {
		return hour >= h.config.ActiveStart && hour < h.config.ActiveEnd
	}
	// Wraps midnight, e.g. 22-6
	return hour >= h.config.ActiveStart || hour < h.config.ActiveEnd
}

/**
 * beat 执行一次心跳周期。
 */
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

/**
 * LastBeat 返回最后一次心跳的时间。
 */
func (h *HeartbeatEngine) LastBeat() time.Time {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.lastBeat
}

/**
 * RecentEvents 返回最后 N 个心跳事件。
 */
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

/**
 * min 返回一组整数中的最小值。
 */
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

/**
 * AutonomyConfig 配置 AutonomyKit。
 */
type AutonomyConfig struct {
	Pool      PoolConfig
	Heartbeat HeartbeatConfig
	QueueBuf  int
}

/**
 * DefaultAutonomyConfig 返回默认的 AutonomyConfig。
 */
func DefaultAutonomyConfig() AutonomyConfig {
	return AutonomyConfig{
		Pool:      DefaultPoolConfig(),
		Heartbeat: DefaultHeartbeatConfig(),
		QueueBuf:  64,
	}
}

/**
 * AutonomyKit 是自治代理工作的顶级协调器。
 * 它将 WorkerPool、TaskQueue 和 HeartbeatEngine 组合成一个连贯的系统，
 * 使代理能够主动工作而无需人类提示。
 *
 * 架构：
 *
 *	┌─────────────────────────────────────────────┐
 *	│              AutonomyKit                     │
 *	│                                              │
 *	│  ┌──────────┐  ┌──────────┐  ┌───────────┐ │
 *	│  │TaskQueue │──│WorkerPool│──│Heartbeat   │ │
 *	│  │          │  │          │  │Engine      │ │
 *	│  │ Ready ──→│  │ W1 ──→  │  │ (proactive)│ │
 *	│  │ InProg   │  │ W2 ──→  │  │            │ │
 *	│  │ Blocked  │  │ W3 ──→  │  │ 15m cycle  │ │
 *	│  │ Done     │  │ ...     │  │            │ │
 *	│  └──────────┘  └──────────┘  └───────────┘ │
 *	│       ↑              │                      │
 *	│       │    ┌─────────┘                      │
 *	│       │    ↓                                │
 *	│  ┌──────────────────────┐                   │
 *	│  │  AgentExecutor       │                   │
 *	│  │  (interface)         │                   │
 *	│  │  (isolated session)  │                   │
 *	│  └──────────────────────┘                   │
 *	└─────────────────────────────────────────────┘
 *
 *
 */

type AutonomyKit struct {
	config    AutonomyConfig
	queue     *TaskQueue
	pool      *WorkerPool
	heartbeat *HeartbeatEngine
	executor  AgentExecutor

	mu      sync.RWMutex
	started bool
}

/**
 * NewAutonomyKit 创建一个新的 AutonomyKit，包括任务队列、工作池和心跳引擎。
 */
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

/**
 * Start 启动 AutonomyKit，包括工作池和心跳引擎。
 */
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

/**
 * Stop 停止 AutonomyKit，包括心跳引擎和工作池。
 */
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

/**
 * Queue 返回任务队列，用于直接操作。
 */
func (ak *AutonomyKit) Queue() *TaskQueue {
	return ak.queue
}

/**
 * EnablePersistence 加载并启用持久化队列状态。
 */
func (ak *AutonomyKit) EnablePersistence(path string) (int, error) {
	if ak == nil || ak.queue == nil {
		return 0, nil
	}
	return ak.queue.EnablePersistence(path)
}

/**
 * Pool 返回工作池。
 */
func (ak *AutonomyKit) Pool() *WorkerPool {
	return ak.pool
}

/**
 * SetExecutor 更新工作池使用的执行器。
 */
func (ak *AutonomyKit) SetExecutor(executor AgentExecutor) {
	ak.mu.Lock()
	defer ak.mu.Unlock()

	ak.executor = executor
	ak.pool.SetExecutor(executor)
}

/**
 * Heartbeat 返回心跳引擎。
 */
func (ak *AutonomyKit) Heartbeat() *HeartbeatEngine {
	return ak.heartbeat
}

/**
 * AddTask 是一个便捷方法，用于向队列添加任务。
 */
func (ak *AutonomyKit) AddTask(title, description string, priority TaskPriority, tags []string) *QueueTask {
	return ak.queue.Add(title, description, priority, tags)
}

/**
 * AddTaskWithError 添加任务并处理持久化失败。
 */
func (ak *AutonomyKit) AddTaskWithError(title, description string, priority TaskPriority, tags []string) (*QueueTask, error) {
	return ak.queue.AddWithError(title, description, priority, tags)
}

/**
 * ScaleUp 添加工作线程到工作池。
 */
func (ak *AutonomyKit) ScaleUp(ctx context.Context, count int) error {
	if ak == nil || ak.pool == nil {
		return fmt.Errorf("autonomy kit not initialized")
	}
	return ak.pool.ScaleUp(ctx, count)
}

/**
 * ScaleDown 从工作池移除最多 count 个空闲工作线程。
 */
func (ak *AutonomyKit) ScaleDown(count int) int {
	if ak == nil || ak.pool == nil {
		return 0
	}
	return ak.pool.ScaleDown(count)
}

/**
 * SetWorkerCount 调整工作池的工作线程数量。
 */
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

/**
 * Status 返回 AutonomyKit 的整体状态。
 */
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

/**
 * AutonomyStatus 是 AutonomyKit 状态的快照。
 */
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
