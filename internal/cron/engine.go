package cron

import (
	"fmt"
	"sync"
	"time"
)

const (
	maxSleepInterval = 5 * time.Minute
	maxRunHistory    = 20
)

// JobStatus 任务状态
type JobStatus int

const (
	StatusIdle JobStatus = iota
	StatusRunning
	StatusPaused
	StatusDone
	StatusFailed
)

func (s JobStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusRunning:
		return "running"
	case StatusPaused:
		return "paused"
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Job 定时任务
type Job struct {
	ID             string
	Name           string
	Description    string
	Schedule       Schedule
	Task           func() error
	Status         JobStatus
	LastRun        time.Time
	NextRun        time.Time
	RunCount       int
	ErrorCount     int
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RunHistory     []RunRecord
	DeleteAfterRun bool
	Metadata       map[string]string // v0.44.0: 自定义元数据（如 chatID, triggerType 等）
}

// RunRecord 单次执行记录
type RunRecord struct {
	RunAt    time.Time
	Status   string
	Duration time.Duration
	Error    string
}

// Schedule 调度策略
type Schedule interface {
	Next(from time.Time) time.Time
	String() string
}

// IntervalSchedule 固定间隔调度
type IntervalSchedule struct {
	Interval time.Duration
}

func (s IntervalSchedule) Next(from time.Time) time.Time {
	return from.Add(s.Interval)
}

func (s IntervalSchedule) String() string {
	return fmt.Sprintf("every %s", s.Interval)
}

// DailySchedule 每日定时调度
type DailySchedule struct {
	Hour   int
	Minute int
}

func (s DailySchedule) Next(from time.Time) time.Time {
	next := time.Date(from.Year(), from.Month(), from.Day(), s.Hour, s.Minute, 0, 0, from.Location())
	if !next.After(from) {
		next = next.Add(24 * time.Hour)
	}
	return next
}

func (s DailySchedule) String() string {
	return fmt.Sprintf("daily at %02d:%02d", s.Hour, s.Minute)
}

// CronSchedule 标准 cron 表达式调度（简化版：分 时 日 月 周）
type CronSchedule struct {
	Minute  []int // 0-59
	Hour    []int // 0-23
	Day     []int // 1-31
	Month   []int // 1-12
	Weekday []int // 0-6 (0=Sunday)
}

func (s CronSchedule) Next(from time.Time) time.Time {
	// 简化实现：从下一分钟开始逐分钟检查
	next := from.Add(1 * time.Minute)
	next = time.Date(next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute(), 0, 0, next.Location())

	// 最多搜索 366 天
	deadline := from.Add(366 * 24 * time.Hour)
	for next.Before(deadline) {
		if s.matches(next) {
			return next
		}
		next = next.Add(1 * time.Minute)
	}

	return time.Time{} // 无匹配
}

func (s CronSchedule) matches(t time.Time) bool {
	if !matchField(t.Minute(), s.Minute) {
		return false
	}
	if !matchField(t.Hour(), s.Hour) {
		return false
	}
	if !matchField(t.Day(), s.Day) {
		return false
	}
	if !matchField(int(t.Month()), s.Month) {
		return false
	}
	if !matchField(int(t.Weekday()), s.Weekday) {
		return false
	}
	return true
}

func matchField(value int, fields []int) bool {
	if len(fields) == 0 {
		return true // 空表示匹配所有
	}
	for _, f := range fields {
		if f == value {
			return true
		}
	}
	return false
}

func (s CronSchedule) String() string {
	return fmt.Sprintf("cron(%v %v %v %v %v)", s.Minute, s.Hour, s.Day, s.Month, s.Weekday)
}

// OnceSchedule 单次调度
type OnceSchedule struct {
	At time.Time
}

func (s OnceSchedule) Next(from time.Time) time.Time {
	if s.At.After(from) {
		return s.At
	}
	return time.Time{} // 已过期
}

func (s OnceSchedule) String() string {
	return fmt.Sprintf("once at %s", s.At.Format(time.RFC3339))
}

// DescribeSchedule returns a stable human-readable summary of a schedule.
// Structured persistence should use serializeSchedule; this string is for
// display and legacy migration only.
func DescribeSchedule(schedule Schedule) string {
	if schedule == nil {
		return ""
	}
	switch s := schedule.(type) {
	case CronSchedule:
		return cronExprDisplay(s)
	case *CronSchedule:
		return cronExprDisplay(*s)
	case DailySchedule:
		return fmt.Sprintf("每天%02d:%02d", s.Hour, s.Minute)
	case *DailySchedule:
		return fmt.Sprintf("每天%02d:%02d", s.Hour, s.Minute)
	case IntervalSchedule:
		return intervalDisplay(s.Interval)
	case *IntervalSchedule:
		return intervalDisplay(s.Interval)
	case OnceSchedule:
		return s.At.Format("2006-01-02 15:04")
	case *OnceSchedule:
		return s.At.Format("2006-01-02 15:04")
	default:
		return schedule.String()
	}
}

// Engine Cron 调度引擎
type Engine struct {
	mu      sync.RWMutex
	jobs    map[string]*Job
	stopCh  chan struct{}
	wakeCh  chan struct{}
	running bool
	onEvent func(event Event)
}

// Event 调度事件
type Event struct {
	Type     EventType
	JobID    string
	JobName  string
	Time     time.Time
	Error    error
	Metadata map[string]string // v0.44.0: 从 Job 透传的元数据
}

// EventType 事件类型
type EventType int

const (
	EventJobAdded EventType = iota
	EventJobRemoved
	EventJobStarted
	EventJobCompleted
	EventJobFailed
	EventEngineStarted
	EventEngineStopped
)

func (e EventType) String() string {
	switch e {
	case EventJobAdded:
		return "job_added"
	case EventJobRemoved:
		return "job_removed"
	case EventJobStarted:
		return "job_started"
	case EventJobCompleted:
		return "job_completed"
	case EventJobFailed:
		return "job_failed"
	case EventEngineStarted:
		return "engine_started"
	case EventEngineStopped:
		return "engine_stopped"
	default:
		return "unknown"
	}
}

// NewEngine 创建调度引擎
func NewEngine() *Engine {
	return &Engine{
		jobs: make(map[string]*Job),
	}
}

// SetEventHandler 设置事件处理器
func (e *Engine) SetEventHandler(handler func(event Event)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onEvent = handler
}

// AddJob 添加定时任务
func (e *Engine) AddJob(id, name, description string, schedule Schedule, task func() error) error {
	return e.AddJobWithMeta(id, name, description, schedule, task, nil)
}

// AddJobWithMeta 添加定时任务（带元数据）
func (e *Engine) AddJobWithMeta(id, name, description string, schedule Schedule, task func() error, metadata map[string]string) error {
	e.mu.Lock()
	if _, exists := e.jobs[id]; exists {
		e.mu.Unlock()
		return fmt.Errorf("job %s already exists", id)
	}

	now := time.Now()
	job := &Job{
		ID:          id,
		Name:        name,
		Description: description,
		Schedule:    schedule,
		Task:        task,
		Status:      StatusIdle,
		NextRun:     schedule.Next(now),
		CreatedAt:   now,
		UpdatedAt:   now,
		Metadata:    metadata,
	}

	e.jobs[id] = job
	e.notifyWakeLocked()
	e.mu.Unlock()

	e.emit(Event{Type: EventJobAdded, JobID: id, JobName: name, Time: now})
	return nil
}

// RemoveJob 移除定时任务
func (e *Engine) RemoveJob(id string) error {
	e.mu.Lock()
	job, exists := e.jobs[id]
	if !exists {
		e.mu.Unlock()
		return fmt.Errorf("job %s not found", id)
	}

	delete(e.jobs, id)
	e.notifyWakeLocked()
	e.mu.Unlock()

	e.emit(Event{Type: EventJobRemoved, JobID: id, JobName: job.Name, Time: time.Now()})
	return nil
}

// PauseJob 暂停任务
func (e *Engine) PauseJob(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	job, exists := e.jobs[id]
	if !exists {
		return fmt.Errorf("job %s not found", id)
	}

	job.Status = StatusPaused
	job.UpdatedAt = time.Now()
	e.notifyWakeLocked()
	return nil
}

// ResumeJob 恢复任务
func (e *Engine) ResumeJob(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	job, exists := e.jobs[id]
	if !exists {
		return fmt.Errorf("job %s not found", id)
	}

	job.Status = StatusIdle
	job.NextRun = job.Schedule.Next(time.Now())
	job.UpdatedAt = time.Now()
	e.notifyWakeLocked()
	return nil
}

// GetJob 获取任务
func (e *Engine) GetJob(id string) (*Job, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	job, exists := e.jobs[id]
	if !exists {
		return nil, false
	}
	// 返回副本避免外部修改
	copy := *job
	return &copy, true
}

// ListJobs 列出所有任务
func (e *Engine) ListJobs() []*Job {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var jobs []*Job
	for _, job := range e.jobs {
		copy := *job
		jobs = append(jobs, &copy)
	}
	return jobs
}

// Start 启动调度引擎
func (e *Engine) Start() {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.stopCh = make(chan struct{})
	e.wakeCh = make(chan struct{}, 1)
	e.mu.Unlock()

	e.emit(Event{Type: EventEngineStarted, Time: time.Now()})

	go e.run()
}

// Stop 停止调度引擎
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}

	e.running = false
	close(e.stopCh)
	e.mu.Unlock()

	e.emit(Event{Type: EventEngineStopped, Time: time.Now()})
}

// IsRunning 检查引擎是否运行
func (e *Engine) IsRunning() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.running
}

// JobCount 返回任务数量
func (e *Engine) JobCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.jobs)
}

// run 调度循环
func (e *Engine) run() {
	for {
		nextWake := e.nextWake()
		delay := maxSleepInterval
		if !nextWake.IsZero() {
			delay = time.Until(nextWake)
			if delay < 0 {
				delay = 0
			}
			if delay > maxSleepInterval {
				delay = maxSleepInterval
			}
		}

		timer := time.NewTimer(delay)
		select {
		case <-e.stopCh:
			timer.Stop()
			return
		case <-e.wakeCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case now := <-timer.C:
			e.tick(now)
		}
	}
}

// tick 每分钟检查一次
func (e *Engine) tick(now time.Time) {
	e.mu.RLock()
	jobs := make([]*Job, 0, len(e.jobs))
	for _, job := range e.jobs {
		jobs = append(jobs, job)
	}
	e.mu.RUnlock()

	for _, job := range jobs {
		if job.Status == StatusPaused || job.Status == StatusRunning {
			continue
		}

		if job.NextRun.IsZero() {
			continue
		}

		// 检查是否到执行时间（允许1分钟误差）
		if !now.Before(job.NextRun) {
			e.executeJob(job.ID, now)
		}
	}
}

// executeJob 执行任务
func (e *Engine) executeJob(jobID string, now time.Time) {
	e.mu.Lock()
	job, exists := e.jobs[jobID]
	if !exists || job.Status == StatusRunning || job.Status == StatusPaused {
		e.mu.Unlock()
		return
	}
	job.Status = StatusRunning
	job.LastRun = now
	job.UpdatedAt = now
	jobName := job.Name
	jobMeta := job.Metadata
	task := job.Task
	e.mu.Unlock()

	e.emit(Event{Type: EventJobStarted, JobID: jobID, JobName: jobName, Time: now, Metadata: jobMeta})

	// 执行任务
	startedAt := time.Now()
	err := task()
	finishedAt := time.Now()

	e.mu.Lock()
	job, exists = e.jobs[jobID]
	if !exists {
		e.mu.Unlock()
		return
	}
	job.RunCount++
	record := RunRecord{
		RunAt:    now,
		Duration: finishedAt.Sub(startedAt),
	}
	if err != nil {
		job.Status = StatusFailed
		job.ErrorCount++
		job.LastError = err.Error()
		record.Status = "error"
		record.Error = err.Error()
	} else {
		if job.NextRun.IsZero() && job.DeleteAfterRun {
			job.Status = StatusDone
		} else {
			job.Status = StatusIdle
		}
		job.LastError = ""
		record.Status = "ok"
	}
	job.NextRun = job.Schedule.Next(finishedAt)
	if job.NextRun.IsZero() && err == nil {
		job.Status = StatusDone
	}
	job.RunHistory = append(job.RunHistory, record)
	if len(job.RunHistory) > maxRunHistory {
		job.RunHistory = append([]RunRecord(nil), job.RunHistory[len(job.RunHistory)-maxRunHistory:]...)
	}
	job.UpdatedAt = finishedAt
	if job.DeleteAfterRun && job.NextRun.IsZero() {
		delete(e.jobs, jobID)
	}
	e.notifyWakeLocked()
	e.mu.Unlock()

	// 在锁外发送事件
	if err != nil {
		e.emit(Event{Type: EventJobFailed, JobID: jobID, JobName: jobName, Time: time.Now(), Error: err, Metadata: jobMeta})
	} else {
		e.emit(Event{Type: EventJobCompleted, JobID: jobID, JobName: jobName, Time: time.Now(), Metadata: jobMeta})
	}
}

func (e *Engine) nextWake() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var next time.Time
	for _, job := range e.jobs {
		if job.Status == StatusPaused || job.Status == StatusRunning || job.NextRun.IsZero() {
			continue
		}
		if next.IsZero() || job.NextRun.Before(next) {
			next = job.NextRun
		}
	}
	return next
}

func (e *Engine) notifyWakeLocked() {
	if !e.running || e.wakeCh == nil {
		return
	}
	select {
	case e.wakeCh <- struct{}{}:
	default:
	}
}

// RestoreJobState applies persisted runtime state to an already-registered job.
func (e *Engine) RestoreJobState(id string, state RestoredJobState) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	job, exists := e.jobs[id]
	if !exists {
		return fmt.Errorf("job %s not found", id)
	}

	if state.CreatedAt != nil {
		job.CreatedAt = *state.CreatedAt
	}
	if state.UpdatedAt != nil {
		job.UpdatedAt = *state.UpdatedAt
	}
	if state.LastRun != nil {
		job.LastRun = *state.LastRun
	}
	if state.NextRun != nil {
		job.NextRun = *state.NextRun
	}
	job.RunCount = state.RunCount
	job.ErrorCount = state.ErrorCount
	job.LastError = state.LastError
	job.RunHistory = append([]RunRecord(nil), state.RunHistory...)
	job.DeleteAfterRun = state.DeleteAfterRun

	switch {
	case state.Paused:
		job.Status = StatusPaused
	case state.Status == StatusRunning.String():
		job.Status = StatusIdle
	case state.Status == StatusFailed.String():
		job.Status = StatusFailed
	case state.Status == StatusDone.String():
		job.Status = StatusDone
	default:
		job.Status = StatusIdle
	}

	e.notifyWakeLocked()
	return nil
}

type RestoredJobState struct {
	Status         string
	Paused         bool
	LastRun        *time.Time
	NextRun        *time.Time
	RunCount       int
	ErrorCount     int
	LastError      string
	CreatedAt      *time.Time
	UpdatedAt      *time.Time
	RunHistory     []RunRecord
	DeleteAfterRun bool
}

// emit 发送事件
func (e *Engine) emit(event Event) {
	e.mu.RLock()
	handler := e.onEvent
	e.mu.RUnlock()

	if handler != nil {
		handler(event)
	}
}

// ParseCronExpr 解析简化 cron 表达式（分 时 日 月 周）
// 支持: * (任意), 数字, 逗号分隔, 范围 (1-5), 步长 (*/5)
func ParseCronExpr(expr string) (*CronSchedule, error) {
	fields := splitFields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	day, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day: %w", err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	weekday, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("weekday: %w", err)
	}

	return &CronSchedule{
		Minute:  minute,
		Hour:    hour,
		Day:     day,
		Month:   month,
		Weekday: weekday,
	}, nil
}

func splitFields(expr string) []string {
	var fields []string
	current := ""
	for _, ch := range expr {
		if ch == ' ' || ch == '\t' {
			if current != "" {
				fields = append(fields, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		fields = append(fields, current)
	}
	return fields
}

func parseField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return []int{}, nil // 空表示匹配所有
	}

	// 步长: */5
	if len(field) > 2 && field[:2] == "*/" {
		step := 0
		if _, err := fmt.Sscanf(field[2:], "%d", &step); err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step: %s", field)
		}
		var values []int
		for i := min; i <= max; i += step {
			values = append(values, i)
		}
		return values, nil
	}

	// 范围: 1-5
	if idx := indexByte(field, '-'); idx >= 0 {
		start, end, err := parseRange(field[:idx], field[idx+1:], min, max)
		if err != nil {
			return nil, err
		}
		var values []int
		for i := start; i <= end; i++ {
			values = append(values, i)
		}
		return values, nil
	}

	// 逗号分隔: 1,3,5
	if indexByte(field, ',') >= 0 {
		var values []int
		parts := splitBy(field, ',')
		for _, part := range parts {
			v, err := parseNumber(part, min, max)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		}
		return values, nil
	}

	// 单个数字
	v, err := parseNumber(field, min, max)
	if err != nil {
		return nil, err
	}
	return []int{v}, nil
}

func parseRange(startStr, endStr string, min, max int) (int, int, error) {
	start, err := parseNumber(startStr, min, max)
	if err != nil {
		return 0, 0, err
	}
	end, err := parseNumber(endStr, min, max)
	if err != nil {
		return 0, 0, err
	}
	if start > end {
		return 0, 0, fmt.Errorf("invalid range: %d-%d", start, end)
	}
	return start, end, nil
}

func parseNumber(s string, min, max int) (int, error) {
	v := 0
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0, fmt.Errorf("invalid number: %s", s)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("value %d out of range [%d, %d]", v, min, max)
	}
	return v, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func splitBy(s string, c byte) []string {
	var parts []string
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(s[i])
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
