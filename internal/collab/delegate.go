package collab

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	taskstore "github.com/yurika0211/luckyagent/internal/task"
)

// TaskState 协作任务状态
type TaskState string

const (
	TaskPending   TaskState = "pending"
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
	TaskTimeout   TaskState = "timeout"
)

// SubTask 子任务
type SubTask struct {
	ID          string        `json:"id"`
	ParentID    string        `json:"parent_id"`
	AgentID     string        `json:"agent_id"` // 被委派的 Agent
	Description string        `json:"description"`
	Input       string        `json:"input"`  // 子任务输入
	Output      string        `json:"output"` // 子任务输出
	State       TaskState     `json:"state"`
	Error       string        `json:"error,omitempty"`
	StartedAt   time.Time     `json:"started_at"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
	Timeout     time.Duration `json:"timeout"`
}

// CollabTask 协作任务（包含多个子任务）
type CollabTask struct {
	ID          string            `json:"id"`
	Mode        CollabMode        `json:"mode"` // 协作模式
	Description string            `json:"description"`
	Input       string            `json:"input"`
	SubTasks    []*SubTask        `json:"sub_tasks"`
	State       TaskState         `json:"state"`
	Result      string            `json:"result,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
	Timeout     time.Duration     `json:"timeout"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// DelegateManager 协作任务委派管理器
type DelegateManager struct {
	mu         sync.RWMutex
	registry   *Registry
	tasks      map[string]*CollabTask
	nextID     int
	handler    TaskHandler // 子任务执行处理器
	planner    *Planner
	taskStore  taskstore.Store
	taskEvents *taskstore.EventBus
}

// TaskHandler 子任务执行处理器接口
type TaskHandler interface {
	HandleSubTask(ctx context.Context, task *SubTask) (string, error)
}

// SubTaskVerifier is an optional extension for handlers that can validate a
// subtask output before the orchestration policy accepts it.
type SubTaskVerifier interface {
	VerifySubTask(ctx context.Context, task *SubTask, output string) error
}

// TaskHandlerFunc 函数式 TaskHandler
type TaskHandlerFunc func(ctx context.Context, task *SubTask) (string, error)

func (f TaskHandlerFunc) HandleSubTask(ctx context.Context, task *SubTask) (string, error) {
	return f(ctx, task)
}

// NewDelegateManager 创建委派管理器
func NewDelegateManager(registry *Registry, handler TaskHandler) *DelegateManager {
	return &DelegateManager{
		registry: registry,
		tasks:    make(map[string]*CollabTask),
		handler:  handler,
		planner:  NewPlanner(nil),
	}
}

// SetPlanner replaces the default Dijkstra/Markov planner.
func (dm *DelegateManager) SetPlanner(planner *Planner) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if planner == nil {
		planner = NewPlanner(nil)
	}
	dm.planner = planner
}

func (dm *DelegateManager) SetTaskStore(store taskstore.Store) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.taskStore = store
	if store == nil {
		dm.taskEvents = nil
		return
	}
	dm.taskEvents = taskstore.NewEventBus(store)
}

// Delegate 创建并执行协作任务
func (dm *DelegateManager) Delegate(ctx context.Context, mode CollabMode, description, input string, agentIDs []string, timeout time.Duration) (*CollabTask, error) {
	if len(agentIDs) == 0 {
		return nil, fmt.Errorf("at least one agent ID is required")
	}

	dm.mu.Lock()
	dm.nextID++
	taskID := fmt.Sprintf("collab-%d", dm.nextID)

	// 创建子任务
	subTasks := make([]*SubTask, 0, len(agentIDs))
	agents := make([]*AgentProfile, 0, len(agentIDs))
	for i, agentID := range agentIDs {
		// 验证 Agent 存在
		profile, ok := dm.registry.Get(agentID)
		if !ok {
			dm.mu.Unlock()
			return nil, fmt.Errorf("agent %s not found in registry", agentID)
		}
		agents = append(agents, profile)

		subID := fmt.Sprintf("%s-sub-%d", taskID, i+1)
		subTasks = append(subTasks, &SubTask{
			ID:          subID,
			ParentID:    taskID,
			AgentID:     agentID,
			Description: description,
			Input:       input,
			State:       TaskPending,
			Timeout:     timeout,
		})
	}

	var plan *PlanResult
	planner := dm.planner
	if mode == "" || mode == ModeAuto {
		if planner == nil {
			planner = NewPlanner(nil)
			dm.planner = planner
		}
		result := planner.Plan(PlanRequest{
			Description: description,
			Input:       input,
			AgentIDs:    append([]string(nil), agentIDs...),
			Agents:      agents,
			Timeout:     timeout,
		})
		plan = &result
		mode = result.Mode
	}

	task := &CollabTask{
		ID:          taskID,
		Mode:        mode,
		Description: description,
		Input:       input,
		SubTasks:    subTasks,
		State:       TaskPending,
		CreatedAt:   time.Now(),
		Timeout:     timeout,
		Metadata:    make(map[string]string),
	}
	if plan != nil {
		task.Metadata["planner"] = plan.Version
		task.Metadata["planned_mode"] = string(plan.Mode)
		task.Metadata["planner_path"] = fmt.Sprint(plan.Path)
		task.Metadata["planner_weight"] = fmt.Sprintf("%.6f", plan.TotalWeight)
		task.Metadata["mdp_version"] = plan.MDP.Version
		task.Metadata["mdp_state"] = plan.MDP.StateKey
		if summary := mdpDecisionSummary(plan.MDP); summary != "" {
			task.Metadata["mdp_q_values"] = summary
		}
		if action, ok := plan.MDP.Actions[plan.Mode]; ok {
			task.Metadata["mdp_action"] = action.Key()
		}
		if payload, err := json.Marshal(plan); err == nil {
			task.Metadata["planner_trace"] = string(payload)
		}
	}

	dm.tasks[taskID] = task
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()

	dm.recordCollabTaskCreated(store, events, task, plan)

	// 根据模式执行
	switch mode {
	case ModePipeline:
		go dm.executePipeline(ctx, task)
	case ModeParallel:
		go dm.executeParallel(ctx, task)
	case ModeDebate:
		go dm.executeDebate(ctx, task)
	default:
		go dm.executeParallel(ctx, task)
	}

	return task, nil
}

// GetTask 获取任务
func (dm *DelegateManager) GetTask(taskID string) (*CollabTask, bool) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	t, ok := dm.tasks[taskID]
	if !ok {
		return nil, false
	}
	// 深拷贝，避免 data race
	cp := *t
	// 拷贝 SubTasks 切片
	if t.SubTasks != nil {
		cp.SubTasks = make([]*SubTask, len(t.SubTasks))
		for i, sub := range t.SubTasks {
			subCopy := *sub
			cp.SubTasks[i] = &subCopy
		}
	}
	// 拷贝 Metadata map
	if t.Metadata != nil {
		cp.Metadata = make(map[string]string, len(t.Metadata))
		for k, v := range t.Metadata {
			cp.Metadata[k] = v
		}
	}
	return &cp, true
}

// ListTasks 列出所有任务
func (dm *DelegateManager) ListTasks() []*CollabTask {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	result := make([]*CollabTask, 0, len(dm.tasks))
	for _, t := range dm.tasks {
		// 深拷贝，避免 data race
		cp := *t
		// 拷贝 SubTasks 切片
		if t.SubTasks != nil {
			cp.SubTasks = make([]*SubTask, len(t.SubTasks))
			for i, sub := range t.SubTasks {
				subCopy := *sub
				cp.SubTasks[i] = &subCopy
			}
		}
		// 拷贝 Metadata map
		if t.Metadata != nil {
			cp.Metadata = make(map[string]string, len(t.Metadata))
			for k, v := range t.Metadata {
				cp.Metadata[k] = v
			}
		}
		result = append(result, &cp)
	}
	return result
}

// CancelTask 取消任务
func (dm *DelegateManager) CancelTask(taskID string) error {
	dm.mu.Lock()

	task, ok := dm.tasks[taskID]
	if !ok {
		dm.mu.Unlock()
		return fmt.Errorf("task %s not found", taskID)
	}

	if task.State == TaskCompleted || task.State == TaskCancelled {
		dm.mu.Unlock()
		return fmt.Errorf("task %s is already %s", taskID, task.State)
	}

	task.State = TaskCancelled
	task.CompletedAt = time.Now()

	// 取消所有未完成的子任务
	for _, sub := range task.SubTasks {
		if sub.State == TaskPending || sub.State == TaskRunning {
			sub.State = TaskCancelled
			sub.CompletedAt = time.Now()
		}
	}
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()

	dm.recordCollabTaskFinished(store, events, task)
	return nil
}

func (dm *DelegateManager) recordCollabTaskCreated(store taskstore.Store, events *taskstore.EventBus, task *CollabTask, plan *PlanResult) {
	if store == nil || events == nil || task == nil {
		return
	}
	record := taskstore.Record{
		ID:          task.ID,
		Source:      taskstore.SourceHTTP,
		Mode:        collabModeToUnified(task.Mode),
		Status:      collabStateToUnified(task.State),
		Description: task.Description,
		Input:       task.Input,
		CreatedAt:   task.CreatedAt,
		Budget: taskstore.Budget{
			Timeout:         task.Timeout,
			MaxChildren:     len(task.SubTasks),
			MaxConcurrent:   len(task.SubTasks),
			RequireVerifier: planRequiresVerifier(plan),
		},
		Metadata: cloneStringMap(task.Metadata),
	}
	if _, err := store.Create(record); err != nil {
		return
	}
	_ = events.Created(record)
	for _, sub := range task.SubTasks {
		_ = events.Emit(taskstore.Event{
			Type:    taskstore.EventChildCreated,
			TaskID:  task.ID,
			ChildID: sub.ID,
			Message: sub.Description,
			Metadata: map[string]string{
				"agent_id": sub.AgentID,
			},
		})
	}
	if plan != nil {
		_ = store.SavePlannerTrace(task.ID, plan)
		_ = events.Emit(taskstore.Event{
			Type:    taskstore.EventPlanned,
			TaskID:  task.ID,
			Mode:    collabModeToUnified(plan.Mode),
			Message: fmt.Sprintf("planner=%s mode=%s weight=%.6f", plan.Version, plan.Mode, plan.TotalWeight),
			Metadata: map[string]string{
				"planner":      plan.Version,
				"planned_mode": string(plan.Mode),
				"mdp_version":  plan.MDP.Version,
				"mdp_state":    plan.MDP.StateKey,
			},
		})
	}
}

func (dm *DelegateManager) recordCollabTaskStarted(store taskstore.Store, events *taskstore.EventBus, task *CollabTask) {
	if store == nil || events == nil || task == nil {
		return
	}
	record, ok, err := store.Get(task.ID)
	if err != nil || !ok {
		return
	}
	record.Status = taskstore.StatusRunning
	record.StartedAt = time.Now()
	if err := store.Update(record); err != nil {
		return
	}
	_ = events.Started(record)
}

func (dm *DelegateManager) recordCollabTaskFinishedForCurrentStore(task *CollabTask) {
	dm.mu.RLock()
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.RUnlock()
	dm.recordCollabTaskFinished(store, events, task)
}

func (dm *DelegateManager) recordCollabTaskFinished(store taskstore.Store, events *taskstore.EventBus, task *CollabTask) {
	if store == nil || events == nil || task == nil {
		return
	}
	dm.mu.RLock()
	snapshot := cloneCollabTask(task)
	dm.mu.RUnlock()
	record, ok, err := store.Get(snapshot.ID)
	if err != nil || !ok {
		return
	}
	record.Status = collabStateToUnified(snapshot.State)
	record.CompletedAt = snapshot.CompletedAt
	record.Outcome.Status = record.Status
	record.Outcome.Verified = snapshot.Metadata["verifier_failed"] != "true" && snapshot.State == TaskCompleted
	record.Outcome.Cost.ChildCount = len(snapshot.SubTasks)
	record.Metadata = cloneStringMap(snapshot.Metadata)
	if err := store.Update(record); err != nil {
		return
	}
	switch snapshot.State {
	case TaskCompleted:
		_ = store.SaveResult(snapshot.ID, snapshot.Result)
		_ = events.Completed(record, "collab task completed")
	case TaskFailed, TaskTimeout:
		_ = events.Failed(record, snapshot.Result)
	case TaskCancelled:
		_ = events.Emit(taskstore.Event{
			Type:    taskstore.EventCancelled,
			TaskID:  snapshot.ID,
			Status:  taskstore.StatusCancelled,
			Message: "collab task cancelled",
		})
	}
}

func collabStateToUnified(state TaskState) taskstore.Status {
	switch state {
	case TaskPending:
		return taskstore.StatusPending
	case TaskRunning:
		return taskstore.StatusRunning
	case TaskCompleted:
		return taskstore.StatusCompleted
	case TaskFailed, TaskTimeout:
		return taskstore.StatusFailed
	case TaskCancelled:
		return taskstore.StatusCancelled
	default:
		return ""
	}
}

func collabModeToUnified(mode CollabMode) taskstore.Mode {
	switch mode {
	case ModeAuto:
		return taskstore.ModeAuto
	case ModeParallel:
		return taskstore.ModeParallel
	case ModePipeline:
		return taskstore.ModePipeline
	case ModeDebate:
		return taskstore.ModeDebate
	default:
		return taskstore.ModeSingle
	}
}

func planRequiresVerifier(plan *PlanResult) bool {
	if plan == nil {
		return false
	}
	if action, ok := plan.MDP.Actions[plan.Mode]; ok {
		return action.RequireVerifier
	}
	return false
}

func cloneCollabTask(task *CollabTask) CollabTask {
	if task == nil {
		return CollabTask{}
	}
	cp := *task
	cp.Metadata = cloneStringMap(task.Metadata)
	if task.SubTasks != nil {
		cp.SubTasks = make([]*SubTask, len(task.SubTasks))
		for i, sub := range task.SubTasks {
			subCopy := *sub
			cp.SubTasks[i] = &subCopy
		}
	}
	return cp
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// executePipeline 串行执行
func (dm *DelegateManager) executePipeline(ctx context.Context, task *CollabTask) {
	defer dm.observePlannedOutcome(task)
	defer dm.recordCollabTaskFinishedForCurrentStore(task)

	dm.mu.Lock()
	task.State = TaskRunning
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()
	dm.recordCollabTaskStarted(store, events, task)

	action := dm.actionForTask(task)
	var pipelineResult string
	input := task.Input
	limit := len(task.SubTasks)
	if action.MaxSteps > 0 && action.MaxSteps < limit {
		limit = action.MaxSteps
	}

	for _, sub := range task.SubTasks[:limit] {
		// 检查取消
		dm.mu.RLock()
		if task.State == TaskCancelled {
			dm.mu.RUnlock()
			return
		}
		dm.mu.RUnlock()

		// 更新子任务输入（前一步的输出作为下一步的输入）
		sub.Input = input

		result, err := dm.executeSubTaskWithAction(ctx, sub, action)
		if err != nil {
			dm.mu.Lock()
			sub.State = TaskFailed
			sub.Error = err.Error()
			sub.CompletedAt = time.Now()
			task.State = TaskFailed
			task.Result = fmt.Sprintf("Pipeline failed at sub-task %s: %s", sub.ID, err)
			task.CompletedAt = time.Now()
			dm.mu.Unlock()
			return
		}

		pipelineResult = result
		input = result // 传递给下一步
	}

	dm.mu.Lock()
	task.State = TaskCompleted
	task.Result = pipelineResult
	if limit < len(task.SubTasks) {
		task.Metadata["step_limit_reached"] = "true"
		task.Metadata["partial_failure"] = "true"
	}
	task.CompletedAt = time.Now()
	dm.mu.Unlock()
}

// executeParallel 并行执行
func (dm *DelegateManager) executeParallel(ctx context.Context, task *CollabTask) {
	defer dm.observePlannedOutcome(task)
	defer dm.recordCollabTaskFinishedForCurrentStore(task)

	dm.mu.Lock()
	task.State = TaskRunning
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()
	dm.recordCollabTaskStarted(store, events, task)

	action := dm.actionForTask(task)
	var wg sync.WaitGroup
	results := make([]string, len(task.SubTasks))
	errors := make([]error, len(task.SubTasks))
	maxConcurrent := action.MaxConcurrent
	if maxConcurrent <= 0 || maxConcurrent > len(task.SubTasks) {
		maxConcurrent = len(task.SubTasks)
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	sem := make(chan struct{}, maxConcurrent)

	for i, sub := range task.SubTasks {
		wg.Add(1)
		go func(idx int, s *SubTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, err := dm.executeSubTaskWithAction(ctx, s, action)
			dm.mu.Lock()
			if err != nil {
				s.State = TaskFailed
				s.Error = err.Error()
				errors[idx] = err
			} else {
				s.State = TaskCompleted
				results[idx] = result
			}
			s.CompletedAt = time.Now()
			dm.mu.Unlock()
		}(i, sub)
	}

	wg.Wait()

	// 检查结果
	dm.mu.Lock()
	failed := 0
	for _, err := range errors {
		if err != nil {
			failed++
		}
	}

	if failed == len(task.SubTasks) {
		task.State = TaskFailed
		task.Result = "All sub-tasks failed"
	} else if failed > 0 {
		task.State = TaskCompleted
		task.Result = fmt.Sprintf("Completed with %d/%d sub-task failures", failed, len(task.SubTasks))
		task.Metadata["partial_failure"] = "true"
	} else {
		task.State = TaskCompleted
		task.Result = "All sub-tasks completed successfully"
	}
	task.Metadata["results_count"] = fmt.Sprintf("%d", len(results)-failed)
	task.Metadata["max_concurrent"] = fmt.Sprintf("%d", maxConcurrent)
	task.CompletedAt = time.Now()
	dm.mu.Unlock()
}

// executeDebate 辩论模式 — Agent 轮流发言，最后投票
func (dm *DelegateManager) executeDebate(ctx context.Context, task *CollabTask) {
	defer dm.observePlannedOutcome(task)
	defer dm.recordCollabTaskFinishedForCurrentStore(task)

	dm.mu.Lock()
	task.State = TaskRunning
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()
	dm.recordCollabTaskStarted(store, events, task)

	action := dm.actionForTask(task)
	rounds := 2
	if len(task.SubTasks) > 0 && action.MaxSteps > 0 {
		rounds = maxInt(1, action.MaxSteps/len(task.SubTasks))
	}
	positions := make(map[string][]string) // agentID -> positions per round
	votes := make(map[string]string)       // agentID -> voted position

	for round := 0; round < rounds; round++ {
		for _, sub := range task.SubTasks {
			// 检查取消
			dm.mu.RLock()
			if task.State == TaskCancelled {
				dm.mu.RUnlock()
				return
			}
			dm.mu.RUnlock()

			// 构建辩论上下文
			debateCtx := fmt.Sprintf("Round %d/%d. Topic: %s\nInput: %s", round+1, rounds, task.Description, task.Input)
			if round > 0 {
				debateCtx += "\n\nPrevious positions:"
				for aid, pos := range positions {
					if len(pos) > 0 {
						debateCtx += fmt.Sprintf("\n- Agent %s: %s", aid, pos[len(pos)-1])
					}
				}
			}

			sub.Input = debateCtx
			result, err := dm.executeSubTaskWithAction(ctx, sub, action)
			if err != nil {
				positions[sub.AgentID] = append(positions[sub.AgentID], fmt.Sprintf("[Error: %s]", err))
			} else {
				positions[sub.AgentID] = append(positions[sub.AgentID], result)
			}
		}
	}

	// 投票阶段 — 每个 Agent 对最终立场投票
	for _, sub := range task.SubTasks {
		voteCtx := fmt.Sprintf("Debate topic: %s\n\nFinal positions:", task.Description)
		for aid, pos := range positions {
			if len(pos) > 0 {
				voteCtx += fmt.Sprintf("\n- Agent %s: %s", aid, pos[len(pos)-1])
			}
		}
		voteCtx += "\n\nCast your vote for the best position (reply with the agent ID you support)."

		sub.Input = voteCtx
		result, err := dm.executeSubTaskWithAction(ctx, sub, action)
		if err == nil {
			votes[sub.AgentID] = result
		}
	}

	// 统计投票
	voteCount := make(map[string]int)
	for _, v := range votes {
		voteCount[v]++
	}

	winner := ""
	maxVotes := 0
	for aid, count := range voteCount {
		if count > maxVotes {
			maxVotes = count
			winner = aid
		}
	}

	dm.mu.Lock()
	task.State = TaskCompleted
	if winner != "" && len(positions[winner]) > 0 {
		task.Result = positions[winner][len(positions[winner])-1]
		if action.RequireVerifier {
			if err := verifyFinalResult(task.Result); err != nil {
				task.State = TaskFailed
				task.Result = fmt.Sprintf("Debate verifier failed: %s", err)
				task.Metadata["verifier_failed"] = "true"
			}
		}
	} else {
		task.Result = "Debate completed with no clear winner"
		if action.RetryPolicy == "critic_tiebreak" {
			for aid, pos := range positions {
				if len(pos) > 0 {
					winner = aid
					task.Result = pos[len(pos)-1]
					task.Metadata["critic_tiebreak"] = "true"
					break
				}
			}
		}
	}
	task.Metadata["debate_rounds"] = fmt.Sprintf("%d", rounds)
	task.Metadata["debate_winner"] = winner
	task.Metadata["debate_votes"] = fmt.Sprintf("%d", len(votes))
	task.CompletedAt = time.Now()
	dm.mu.Unlock()
}

func (dm *DelegateManager) observePlannedOutcome(task *CollabTask) {
	if task == nil {
		return
	}
	dm.mu.RLock()
	mode := task.Mode
	state := task.State
	outcome := stateToOutcome(state)
	if task.Metadata["partial_failure"] == "true" {
		outcome = "partial"
	}
	planner := dm.planner
	req := PlanRequest{
		Description: task.Description,
		Input:       task.Input,
		AgentIDs:    agentIDsFromSubTasks(task.SubTasks),
		Timeout:     task.Timeout,
	}
	duration := time.Duration(0)
	if !task.CreatedAt.IsZero() && !task.CompletedAt.IsZero() {
		duration = task.CompletedAt.Sub(task.CreatedAt)
	}
	dm.mu.RUnlock()
	if planner != nil {
		planner.ObserveExecution(req, mode, outcome, duration)
	}
}

func agentIDsFromSubTasks(subTasks []*SubTask) []string {
	agentIDs := make([]string, 0, len(subTasks))
	for _, sub := range subTasks {
		if sub != nil && sub.AgentID != "" {
			agentIDs = append(agentIDs, sub.AgentID)
		}
	}
	return agentIDs
}

func (dm *DelegateManager) actionForTask(task *CollabTask) MDPAction {
	if task == nil {
		return MDPAction{}
	}
	if task.Metadata != nil {
		if raw := task.Metadata["planner_trace"]; raw != "" {
			var plan PlanResult
			if err := json.Unmarshal([]byte(raw), &plan); err == nil {
				if action, ok := plan.MDP.Actions[task.Mode]; ok {
					return action
				}
			}
		}
	}
	return MDPActionForMode(PlanRequest{
		Description: task.Description,
		Input:       task.Input,
		AgentIDs:    agentIDsFromSubTasks(task.SubTasks),
		Timeout:     task.Timeout,
	}, task.Mode)
}

func (dm *DelegateManager) executeSubTaskWithAction(ctx context.Context, sub *SubTask, action MDPAction) (string, error) {
	attempts := 1
	if action.RetryPolicy == "retry_failed_once" || action.RetryPolicy == "verify_each_step" {
		attempts = 2
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		result, err := dm.executeSubTask(ctx, sub)
		if err == nil && action.RequireVerifier {
			err = dm.verifySubTaskOutput(ctx, sub, result)
			if err != nil {
				dm.markSubTaskVerificationFailed(sub, err)
			}
		}
		if err == nil {
			if attempt > 1 {
				dm.mu.Lock()
				sub.Error = ""
				dm.mu.Unlock()
			}
			return result, nil
		}
		lastErr = err
		if !shouldRetryAction(action, attempt, attempts) {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("sub-task %s failed", sub.ID)
	}
	return "", lastErr
}

func shouldRetryAction(action MDPAction, attempt, attempts int) bool {
	if attempt >= attempts {
		return false
	}
	return action.RetryPolicy == "retry_failed_once" || action.RetryPolicy == "verify_each_step"
}

func (dm *DelegateManager) verifySubTaskOutput(ctx context.Context, sub *SubTask, output string) error {
	if dm.handler != nil {
		if verifier, ok := dm.handler.(SubTaskVerifier); ok {
			return verifier.VerifySubTask(ctx, sub, output)
		}
	}
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("verifier rejected empty output")
	}
	return nil
}

func (dm *DelegateManager) markSubTaskVerificationFailed(sub *SubTask, err error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	sub.State = TaskFailed
	sub.Error = fmt.Sprintf("verification failed: %s", err)
	sub.CompletedAt = time.Now()
}

func verifyFinalResult(output string) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("verifier rejected empty final result")
	}
	return nil
}

// executeSubTask 执行单个子任务
func (dm *DelegateManager) executeSubTask(ctx context.Context, sub *SubTask) (string, error) {
	dm.mu.Lock()
	sub.State = TaskRunning
	sub.StartedAt = time.Now()
	dm.mu.Unlock()

	// 设置超时
	var cancel context.CancelFunc
	if sub.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, sub.Timeout)
		defer cancel()
	}

	if dm.handler == nil {
		return "", fmt.Errorf("no task handler configured")
	}

	result, err := dm.handler.HandleSubTask(ctx, sub)

	dm.mu.Lock()
	if err != nil {
		sub.State = TaskFailed
		sub.Error = err.Error()
		if ctx.Err() == context.DeadlineExceeded {
			sub.State = TaskTimeout
		}
	} else {
		sub.State = TaskCompleted
		sub.Output = result
		sub.Error = ""
	}
	sub.CompletedAt = time.Now()
	dm.mu.Unlock()

	return result, err
}

// Stats 委派统计
func (dm *DelegateManager) Stats() (total, running, completed, failed int) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	for _, t := range dm.tasks {
		total++
		switch t.State {
		case TaskRunning, TaskPending:
			running++
		case TaskCompleted:
			completed++
		case TaskFailed, TaskTimeout:
			failed++
		}
	}
	return
}
