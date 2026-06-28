package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const delegateWorkspaceMarker = "LuckyAgent delegate workspace:"

var delegateWorkspacePathRe = regexp.MustCompile(`(?:/tmp/[^\s"'<>，。；；,，)）\]}]+|~[/\\]\.luckyagent[/\\]?[^\s"'<>，。；；,，)）\]}]*)`)

// DelegateConfig 子代理委派配置
type DelegateConfig struct {
	MaxConcurrent int           // 最大并发子代理数
	Timeout       time.Duration // 子代理超时
	AutoApprove   bool          // 自动批准子代理任务
}

// DefaultDelegateConfig 默认委派配置
func DefaultDelegateConfig() DelegateConfig {
	return DelegateConfig{
		MaxConcurrent: 3,
		Timeout:       120 * time.Second,
		AutoApprove:   false,
	}
}

func prepareDelegateExecutionContext(taskID, description, contextStr string) (string, string, error) {
	workspace := findDelegateWorkspace(description, contextStr)
	if workspace == "" {
		workspace = defaultDelegateWorkspace(taskID)
	}
	workspace = normalizeDelegateWorkspace(workspace)
	if err := validatePath(workspace); err != nil {
		workspace = defaultDelegateWorkspace(taskID)
	}
	workspace = normalizeDelegateWorkspace(workspace)
	if err := validatePath(workspace); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return "", "", fmt.Errorf("create delegate workspace: %w", err)
	}
	return workspace, appendDelegateWorkspaceContext(contextStr, workspace), nil
}

func findDelegateWorkspace(parts ...string) string {
	for _, part := range parts {
		for _, candidate := range delegateWorkspacePathRe.FindAllString(part, -1) {
			candidate = normalizeDelegateWorkspace(candidate)
			if candidate == "" {
				continue
			}
			if err := validatePath(candidate); err == nil {
				return candidate
			}
		}
	}
	return ""
}

func defaultDelegateWorkspace(taskID string) string {
	return filepath.Join(os.TempDir(), "luckyagent-delegate", sanitizeDelegateTaskID(taskID))
}

func sanitizeDelegateTaskID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "task"
	}
	var b strings.Builder
	for _, r := range taskID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "task"
	}
	return b.String()
}

func normalizeDelegateWorkspace(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimRight(path, ".,;:，。；：、)]}>\n\r\t ")
	path = expandSandboxPath(path)
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	if info, err := os.Stat(clean); err == nil && !info.IsDir() {
		return filepath.Dir(clean)
	}
	base := filepath.Base(clean)
	if ext := filepath.Ext(base); ext != "" && !strings.HasPrefix(base, ".") {
		return filepath.Dir(clean)
	}
	return clean
}

func appendDelegateWorkspaceContext(contextStr, workspace string) string {
	block := fmt.Sprintf(`%s
Current working directory: %s
Allowed file roots: /tmp/ and ~/.luckyagent/.
Use relative file paths under the current working directory, or explicit paths under the allowed roots. Do not use /home or bare ~, and do not assume "." is the repository root; "." refers to the current working directory above.`, delegateWorkspaceMarker, workspace)
	contextStr = strings.TrimSpace(contextStr)
	if contextStr == "" {
		return block
	}
	if strings.Contains(contextStr, delegateWorkspaceMarker) {
		return contextStr
	}
	return contextStr + "\n\n" + block
}

func ExtractDelegateWorkspace(contextStr string) string {
	idx := strings.Index(contextStr, delegateWorkspaceMarker)
	if idx < 0 {
		return ""
	}
	for _, line := range strings.Split(contextStr[idx:], "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Current working directory") {
			continue
		}
		workspace := normalizeDelegateWorkspace(value)
		if workspace == "" {
			return ""
		}
		if err := validatePath(workspace); err != nil {
			return ""
		}
		return workspace
	}
	return ""
}

// TaskStatus 子代理任务状态
type TaskStatus int

const (
	StatusPending TaskStatus = iota
	StatusRunning
	StatusCompleted
	StatusFailed
	StatusCancelled
)

func (s TaskStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	case StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// DelegateTask 子代理任务
type DelegateTask struct {
	ID          string
	Description string
	Context     string
	Workspace   string
	Status      TaskStatus
	Result      string
	Error       string
	StartedAt   time.Time
	CompletedAt time.Time
}

// AgentExecutorFunc 子代理执行函数 — 通过 Agent Loop 真正执行任务
// v0.38.0: 让 delegate 不再是占位，而是真正走 LLM
type AgentExecutorFunc func(ctx context.Context, description, contextStr string) (string, error)

// DelegateManager 子代理委派管理器
type DelegateManager struct {
	mu            sync.RWMutex
	config        DelegateConfig
	tasks         map[string]*DelegateTask
	nextID        int
	agentExecutor AgentExecutorFunc // v0.38.0: 真正的 Agent 执行器
}

// NewDelegateManager 创建子代理委派管理器
func NewDelegateManager(cfg DelegateConfig) *DelegateManager {
	return &DelegateManager{
		config: cfg,
		tasks:  make(map[string]*DelegateTask),
	}
}

// SetAgentExecutor 设置 Agent 执行器 (v0.38.0)
// 让 delegate_task 工具真正通过 Agent Loop 执行
func (dm *DelegateManager) SetAgentExecutor(fn AgentExecutorFunc) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.agentExecutor = fn
}

// DelegateTaskTool 创建子代理委派工具
func DelegateTaskTool(dm *DelegateManager) *Tool {
	return &Tool{
		Name:        "delegate_task",
		Description: "Delegate a task to a sub-agent. The sub-agent will work independently and return results.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove, // 委派任务需要审批
		Parameters: map[string]Param{
			"description": {
				Type:        "string",
				Description: "Description of the task to delegate",
				Required:    true,
			},
			"context": {
				Type:        "string",
				Description: "Additional context or instructions for the sub-agent",
				Required:    false,
			},
			"timeout": {
				Type:        "number",
				Description: "Timeout in seconds (default 120)",
				Required:    false,
				Default:     120,
			},
		},
		Handler: dm.handleDelegate,
	}
}

// TaskStatusTool 创建任务状态查询工具
func TaskStatusTool(dm *DelegateManager) *Tool {
	return &Tool{
		Name:        "task_status",
		Description: "Check the status of a delegated task.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto, // 查询状态自动批准
		Parameters: map[string]Param{
			"task_id": {
				Type:        "string",
				Description: "ID of the task to check",
				Required:    true,
			},
		},
		Handler: dm.handleStatus,
	}
}

// ListTasksTool 创建任务列表工具
func ListTasksTool(dm *DelegateManager) *Tool {
	return &Tool{
		Name:        "list_tasks",
		Description: "List all delegated tasks and their statuses.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters:  map[string]Param{},
		Handler:     dm.handleList,
	}
}

// handleDelegate 处理委派请求
func (dm *DelegateManager) handleDelegate(args map[string]any) (string, error) {
	description, ok := args["description"].(string)
	if !ok {
		return "", fmt.Errorf("description is required")
	}

	contextStr := ""
	if c, ok := args["context"]; ok {
		contextStr, _ = c.(string)
	}

	timeout := 120
	if t, ok := args["timeout"]; ok {
		switch v := t.(type) {
		case float64:
			timeout = int(v)
		case int:
			timeout = v
		}
	}

	// 检查并发限制
	dm.mu.RLock()
	running := 0
	for _, t := range dm.tasks {
		if t.Status == StatusRunning {
			running++
		}
	}
	dm.mu.RUnlock()

	if running >= dm.config.MaxConcurrent {
		return "", fmt.Errorf("max concurrent tasks reached (%d)", dm.config.MaxConcurrent)
	}

	// 创建任务
	dm.mu.Lock()
	dm.nextID++
	taskID := fmt.Sprintf("task-%d", dm.nextID)
	workspace, enrichedContext, err := prepareDelegateExecutionContext(taskID, description, contextStr)
	if err != nil {
		dm.mu.Unlock()
		return "", err
	}
	task := &DelegateTask{
		ID:          taskID,
		Description: description,
		Context:     contextStr,
		Workspace:   workspace,
		Status:      StatusPending,
		StartedAt:   time.Now(),
	}
	dm.tasks[taskID] = task
	dm.mu.Unlock()

	// 异步执行
	go dm.executeTask(taskID, description, enrichedContext, time.Duration(timeout)*time.Second)

	result, _ := json.Marshal(map[string]any{
		"task_id":   taskID,
		"status":    "running",
		"workspace": workspace,
		"message":   fmt.Sprintf("Task '%s' delegated. Use task_status to check progress.", taskID),
	})

	return string(result), nil
}

// executeTask 执行子代理任务
// v0.38.0: 通过 Agent Loop 真正执行子代理任务
func (dm *DelegateManager) executeTask(taskID, description, contextStr string, timeout time.Duration) {
	dm.mu.Lock()
	task := dm.tasks[taskID]
	task.Status = StatusRunning
	dm.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// v0.38.0: 如果配置了 agentExecutor，通过 Agent Loop 执行
	if dm.agentExecutor != nil {
		result, err := dm.agentExecutor(ctx, description, contextStr)
		dm.mu.Lock()
		if err != nil {
			task.Status = StatusFailed
			task.Error = err.Error()
		} else {
			task.Status = StatusCompleted
			task.Result = result
		}
		task.CompletedAt = time.Now()
		dm.mu.Unlock()
		return
	}

	// 降级：无 agentExecutor 时返回占位结果
	select {
	case <-ctx.Done():
		dm.mu.Lock()
		task.Status = StatusFailed
		task.Error = "timeout"
		task.CompletedAt = time.Now()
		dm.mu.Unlock()
		return
	default:
	}

	dm.mu.Lock()
	task.Status = StatusCompleted
	task.Result = fmt.Sprintf("Sub-agent task completed (no executor): %s", description)
	task.CompletedAt = time.Now()
	dm.mu.Unlock()
}

// handleStatus 处理状态查询
func (dm *DelegateManager) handleStatus(args map[string]any) (string, error) {
	taskID, ok := args["task_id"].(string)
	if !ok {
		return "", fmt.Errorf("task_id is required")
	}

	dm.mu.RLock()
	task, ok := dm.tasks[taskID]
	dm.mu.RUnlock()

	if !ok {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	result, _ := json.Marshal(map[string]any{
		"task_id":      task.ID,
		"description":  task.Description,
		"workspace":    task.Workspace,
		"status":       task.Status.String(),
		"result":       task.Result,
		"error":        task.Error,
		"started_at":   task.StartedAt.Format(time.RFC3339),
		"completed_at": task.CompletedAt.Format(time.RFC3339),
	})

	return string(result), nil
}

// handleList 处理任务列表
func (dm *DelegateManager) handleList(args map[string]any) (string, error) {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	var tasks []map[string]any
	for _, t := range dm.tasks {
		tasks = append(tasks, map[string]any{
			"task_id":     t.ID,
			"description": t.Description,
			"workspace":   t.Workspace,
			"status":      t.Status.String(),
			"started_at":  t.StartedAt.Format(time.RFC3339),
		})
	}

	result, _ := json.Marshal(map[string]any{
		"tasks": tasks,
		"count": len(tasks),
	})

	return string(result), nil
}

// --- 并行委派支持 ---

// ParallelDelegateTask 并行委派任务
type ParallelDelegateTask struct {
	ID          string
	Description string
	Context     string
	Status      TaskStatus
	Result      string
	Error       string
	StartedAt   time.Time
	CompletedAt time.Time
}

// ParallelDelegateResult 并行委派结果
type ParallelDelegateResult struct {
	Results       []string // 各子代理结果
	Summary       string   // 汇总摘要
	FailedCount   int      // 失败任务数
	SuccessCount  int      // 成功任务数
	TotalDuration time.Duration
}

// DelegateParallel 并行委派多个任务
// 支持多个子代理并行执行任务，结果汇总
func (dm *DelegateManager) DelegateParallel(descriptions []string, contextStr string, timeout time.Duration) *ParallelDelegateResult {
	if len(descriptions) == 0 {
		return &ParallelDelegateResult{
			Summary: "No tasks to delegate",
		}
	}

	// 限制并发数
	maxConcurrent := dm.config.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}

	startTime := time.Now()
	resultCh := make(chan struct {
		index  int
		result string
		err    error
	}, len(descriptions))

	// 信号量控制并发
	sem := make(chan struct{}, maxConcurrent)

	// 启动所有任务
	for i, desc := range descriptions {
		go func(idx int, description string) {
			sem <- struct{}{}        // 获取信号量
			defer func() { <-sem }() // 释放信号量

			// 创建任务
			dm.mu.Lock()
			dm.nextID++
			taskID := fmt.Sprintf("parallel-task-%d", dm.nextID)
			workspace, enrichedContext, workspaceErr := prepareDelegateExecutionContext(taskID, description, contextStr)
			task := &DelegateTask{
				ID:          taskID,
				Description: description,
				Context:     contextStr,
				Workspace:   workspace,
				Status:      StatusPending,
				StartedAt:   time.Now(),
			}
			dm.tasks[taskID] = task
			dm.mu.Unlock()

			// 执行任务
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			var result string
			var err error

			if workspaceErr != nil {
				err = workspaceErr
			} else if dm.agentExecutor != nil {
				result, err = dm.agentExecutor(ctx, description, enrichedContext)
			} else {
				result = fmt.Sprintf("Sub-agent task completed (no executor): %s", description)
			}

			// 更新任务状态
			dm.mu.Lock()
			if err != nil {
				task.Status = StatusFailed
				task.Error = err.Error()
			} else {
				task.Status = StatusCompleted
				task.Result = result
			}
			task.CompletedAt = time.Now()
			dm.mu.Unlock()

			resultCh <- struct {
				index  int
				result string
				err    error
			}{index: i, result: result, err: err}
		}(i, desc)
	}

	// 收集所有结果
	results := make([]string, len(descriptions))
	var successCount, failedCount int

	for i := 0; i < len(descriptions); i++ {
		r := <-resultCh
		results[r.index] = r.result
		if r.err != nil {
			failedCount++
		} else {
			successCount++
		}
	}

	totalDuration := time.Since(startTime)

	// 生成汇总摘要
	summary := dm.generateParallelSummary(descriptions, results, successCount, failedCount)

	return &ParallelDelegateResult{
		Results:       results,
		Summary:       summary,
		FailedCount:   failedCount,
		SuccessCount:  successCount,
		TotalDuration: totalDuration,
	}
}

// generateParallelSummary 生成并行任务汇总摘要
func (dm *DelegateManager) generateParallelSummary(descriptions, results []string, successCount, failedCount int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Parallel Delegation Summary:\n"))
	sb.WriteString(fmt.Sprintf("- Total Tasks: %d\n", len(descriptions)))
	sb.WriteString(fmt.Sprintf("- Successful: %d\n", successCount))
	sb.WriteString(fmt.Sprintf("- Failed: %d\n", failedCount))
	sb.WriteString("\n")

	for i, desc := range descriptions {
		status := "✅"
		result := results[i]
		if len(result) > 200 {
			result = result[:200] + "..."
		}
		// 简单判断是否失败（包含 error 关键词）
		if strings.Contains(strings.ToLower(result), "error") ||
			strings.Contains(strings.ToLower(result), "failed") {
			status = "❌"
		}
		sb.WriteString(fmt.Sprintf("%s Task %d: %s\n", status, i+1, desc))
		sb.WriteString(fmt.Sprintf("   Result: %s\n\n", result))
	}

	return sb.String()
}

// DelegateParallelTool 创建并行委派工具
func (dm *DelegateManager) DelegateParallelTool() *Tool {
	return &Tool{
		Name:        "delegate_parallel",
		Description: "Delegate multiple tasks to sub-agents in parallel. Sub-agents work concurrently and results are aggregated.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"tasks": {
				Type:        "array",
				Description: "List of task descriptions to delegate",
				Required:    true,
			},
			"context": {
				Type:        "string",
				Description: "Shared context or instructions for all sub-agents",
				Required:    false,
			},
			"timeout": {
				Type:        "number",
				Description: "Timeout in seconds for each task (default 120)",
				Required:    false,
				Default:     120,
			},
		},
		Handler: dm.handleDelegateParallel,
	}
}

// handleDelegateParallel 处理并行委派请求
func (dm *DelegateManager) handleDelegateParallel(args map[string]any) (string, error) {
	// 解析 tasks 数组
	tasksArg, ok := args["tasks"].([]any)
	if !ok {
		return "", fmt.Errorf("tasks array is required")
	}

	var descriptions []string
	for _, t := range tasksArg {
		if desc, ok := t.(string); ok {
			descriptions = append(descriptions, desc)
		}
	}

	if len(descriptions) == 0 {
		return "", fmt.Errorf("at least one task description is required")
	}

	contextStr := ""
	if c, ok := args["context"]; ok {
		contextStr, _ = c.(string)
	}

	timeout := 120
	if t, ok := args["timeout"]; ok {
		switch v := t.(type) {
		case float64:
			timeout = int(v)
		case int:
			timeout = v
		}
	}

	// 执行并行委派
	result := dm.DelegateParallel(descriptions, contextStr, time.Duration(timeout)*time.Second)

	// 返回结果
	response := map[string]any{
		"success_count": result.SuccessCount,
		"failed_count":  result.FailedCount,
		"duration_sec":  result.TotalDuration.Seconds(),
		"summary":       result.Summary,
		"results":       result.Results,
	}

	data, err := json.Marshal(response)
	if err != nil {
		return "", err
	}

	return string(data), nil
}
