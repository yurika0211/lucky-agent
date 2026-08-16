package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/collab"
	taskstore "github.com/yurika0211/luckyagent/internal/task"
)

const delegateWorkspaceMarker = "LuckyAgent delegate workspace:"
const (
	defaultDelegateTimeoutSeconds = 120
	minDelegateTimeoutSeconds     = 5
	maxDelegateTimeoutSeconds     = 1800
	defaultDelegateListLimit      = 20
	maxDelegateListLimit          = 100
	defaultDelegateResultInline   = 4000
	defaultWaitPollInterval       = 200 * time.Millisecond
	minWaitPollInterval           = 10 * time.Millisecond
	maxWaitPollInterval           = 5 * time.Second
	minWaitTimeout                = 50 * time.Millisecond
	maxWaitTaskIDs                = 100
)

var delegateWorkspacePathRe = regexp.MustCompile(`(?:/tmp/[^\s"'<>，。；；,，)）\]}]+|~[/\\]\.luckyagent[/\\]?[^\s"'<>，。；；,，)）\]}]*)`)

// DelegateConfig 子代理委派配置
type DelegateConfig struct {
	MaxConcurrent        int           // 最大并发子代理数
	Timeout              time.Duration // 子代理超时
	MinTimeout           time.Duration // 最小子代理超时
	MaxTimeout           time.Duration // 最大子代理超时
	MaxResultBytesInline int           // task_status 内联结果上限
	AutoApprove          bool          // 自动批准子代理任务
}

// DefaultDelegateConfig 默认委派配置
func DefaultDelegateConfig() DelegateConfig {
	return DelegateConfig{
		MaxConcurrent:        3,
		Timeout:              120 * time.Second,
		MinTimeout:           minDelegateTimeoutSeconds * time.Second,
		MaxTimeout:           maxDelegateTimeoutSeconds * time.Second,
		MaxResultBytesInline: defaultDelegateResultInline,
		AutoApprove:          false,
	}
}

func normalizeDelegateConfig(cfg DelegateConfig) DelegateConfig {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 3
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultDelegateTimeoutSeconds * time.Second
	}
	if cfg.MinTimeout <= 0 {
		cfg.MinTimeout = minDelegateTimeoutSeconds * time.Second
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = maxDelegateTimeoutSeconds * time.Second
	}
	if cfg.MaxTimeout < cfg.MinTimeout {
		cfg.MaxTimeout = cfg.MinTimeout
	}
	if cfg.Timeout < cfg.MinTimeout {
		cfg.Timeout = cfg.MinTimeout
	}
	if cfg.Timeout > cfg.MaxTimeout {
		cfg.Timeout = cfg.MaxTimeout
	}
	if cfg.MaxResultBytesInline <= 0 {
		cfg.MaxResultBytesInline = defaultDelegateResultInline
	}
	return cfg
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
	ID             string
	Description    string
	Context        string
	Workspace      string
	Mode           taskstore.Mode
	PlannedMode    taskstore.Mode
	PlannerSummary string
	Status         TaskStatus
	Result         string
	Error          string
	StartedAt      time.Time
	CompletedAt    time.Time
}

type delegateStartResponse struct {
	TaskID         string `json:"task_id"`
	Status         string `json:"status"`
	Mode           string `json:"mode"`
	PlannedMode    string `json:"planned_mode,omitempty"`
	PlannerSummary string `json:"planner_summary,omitempty"`
	TraceAvailable bool   `json:"trace_available,omitempty"`
	Workspace      string `json:"workspace"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	Message        string `json:"message"`
}

type delegateStatusResponse struct {
	TaskID          string                          `json:"task_id"`
	Source          string                          `json:"source,omitempty"`
	Mode            string                          `json:"mode,omitempty"`
	ParentID        string                          `json:"parent_id,omitempty"`
	Description     string                          `json:"description"`
	Workspace       string                          `json:"workspace"`
	Status          string                          `json:"status"`
	ResultSummary   string                          `json:"result_summary,omitempty"`
	Result          string                          `json:"result,omitempty"`
	ResultBytes     int                             `json:"result_bytes"`
	ResultTruncated bool                            `json:"result_truncated"`
	Error           string                          `json:"error,omitempty"`
	StartedAt       string                          `json:"started_at"`
	CompletedAt     string                          `json:"completed_at"`
	Observation     *taskstore.MainAgentObservation `json:"observation,omitempty"`
	Events          []taskstore.Event               `json:"events,omitempty"`
	PlannerTrace    json.RawMessage                 `json:"planner_trace,omitempty"`
}

type delegateListItem struct {
	TaskID          string `json:"task_id"`
	Source          string `json:"source,omitempty"`
	Mode            string `json:"mode,omitempty"`
	ParentID        string `json:"parent_id,omitempty"`
	Description     string `json:"description"`
	Workspace       string `json:"workspace"`
	Status          string `json:"status"`
	ResultSummary   string `json:"result_summary,omitempty"`
	ResultBytes     int    `json:"result_bytes,omitempty"`
	ResultTruncated bool   `json:"result_truncated,omitempty"`
	Error           string `json:"error,omitempty"`
	StartedAt       string `json:"started_at"`
	CompletedAt     string `json:"completed_at,omitempty"`
}

type delegateListResponse struct {
	Tasks    []delegateListItem `json:"tasks"`
	Count    int                `json:"count"`
	Total    int                `json:"total"`
	ByStatus map[string]int     `json:"by_status"`
}

type delegateCancelResponse struct {
	TaskID  string `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// delegateWaitResponse is returned by wait_for_tasks. It intentionally uses
// the same detailed task representation as task_status so callers can
// synthesize completed subtask results without making a second round trip.
type delegateWaitResponse struct {
	Tasks          []delegateStatusResponse `json:"tasks"`
	AllTerminal    bool                     `json:"all_terminal"`
	TimedOut       bool                     `json:"timed_out"`
	PendingTaskIDs []string                 `json:"pending_task_ids"`
	ElapsedMS      int64                    `json:"elapsed_ms"`
}

// AgentExecutorFunc 子代理执行函数 — 通过 Agent Loop 真正执行任务
// v0.38.0: 让 delegate 不再是占位，而是真正走 LLM
type AgentExecutorFunc func(ctx context.Context, description, contextStr string) (string, error)

// DelegateManager 子代理委派管理器
type DelegateManager struct {
	mu            sync.RWMutex
	config        DelegateConfig
	tasks         map[string]*DelegateTask
	cancels       map[string]context.CancelFunc
	nextID        int
	agentExecutor AgentExecutorFunc // v0.38.0: 真正的 Agent 执行器
	taskStore     taskstore.Store
	taskEvents    *taskstore.EventBus
}

// NewDelegateManager 创建子代理委派管理器
func NewDelegateManager(cfg DelegateConfig) *DelegateManager {
	return &DelegateManager{
		config:  normalizeDelegateConfig(cfg),
		tasks:   make(map[string]*DelegateTask),
		cancels: make(map[string]context.CancelFunc),
	}
}

// SetAgentExecutor 设置 Agent 执行器 (v0.38.0)
// 让 delegate_task 工具真正通过 Agent Loop 执行
func (dm *DelegateManager) SetAgentExecutor(fn AgentExecutorFunc) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.agentExecutor = fn
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

func (dm *DelegateManager) delegateTimeoutFromArgs(args map[string]any) time.Duration {
	seconds := int(dm.config.Timeout.Seconds())
	if raw, ok := args["timeout"]; ok {
		if n, ok := delegateIntArg(raw); ok {
			seconds = n
		}
	}
	timeout := time.Duration(seconds) * time.Second
	if timeout <= 0 {
		timeout = dm.config.Timeout
	}
	if timeout < dm.config.MinTimeout {
		timeout = dm.config.MinTimeout
	}
	if timeout > dm.config.MaxTimeout {
		timeout = dm.config.MaxTimeout
	}
	return timeout
}

func delegateIntArg(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case json.Number:
		n, err := strconv.Atoi(v.String())
		return n, err == nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	default:
		return 0, false
	}
}

func delegateDurationSecondsArg(raw any) (time.Duration, bool) {
	var seconds float64
	switch v := raw.(type) {
	case int:
		seconds = float64(v)
	case int64:
		seconds = float64(v)
	case float64:
		seconds = v
	case float32:
		seconds = float64(v)
	case json.Number:
		parsed, err := strconv.ParseFloat(v.String(), 64)
		if err != nil {
			return 0, false
		}
		seconds = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		seconds = parsed
	default:
		return 0, false
	}
	if seconds <= 0 {
		return 0, false
	}
	return time.Duration(seconds * float64(time.Second)), true
}

func delegateBoolArg(args map[string]any, key string, def bool) bool {
	if args == nil {
		return def
	}
	raw, ok := args[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return def
}

func delegateStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	if s, ok := args[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func delegateModeArg(args map[string]any) (taskstore.Mode, error) {
	mode := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "mode")))
	if mode == "" {
		mode = string(taskstore.ModeSingle)
	}
	switch taskstore.Mode(mode) {
	case taskstore.ModeSingle, taskstore.ModeAuto, taskstore.ModeParallel, taskstore.ModePipeline, taskstore.ModeDebate, taskstore.ModeAutonomyQueue:
		return taskstore.Mode(mode), nil
	default:
		return "", fmt.Errorf("unsupported delegate mode %q", mode)
	}
}

func planDelegateTaskMode(description, contextStr string, requested taskstore.Mode, timeout time.Duration, maxChildren int) (taskstore.Mode, taskstore.Mode, string, any) {
	if requested == "" {
		requested = taskstore.ModeSingle
	}
	if maxChildren <= 0 {
		maxChildren = 3
	}
	if requested != taskstore.ModeAuto {
		summary := "Using explicit " + string(requested) + " mode."
		return requested, requested, summary, map[string]any{
			"planner":        "explicit",
			"requested_mode": requested,
			"selected_mode":  requested,
			"summary":        summary,
		}
	}
	if isSimpleDelegateTask(description, contextStr) {
		summary := "Kept single mode because the task appears simple and does not need multi-agent coordination."
		return taskstore.ModeSingle, taskstore.ModeSingle, summary, map[string]any{
			"planner":        "delegate-auto-guard",
			"requested_mode": requested,
			"selected_mode":  taskstore.ModeSingle,
			"summary":        summary,
			"simple_guard":   true,
		}
	}

	planner := collab.NewPlanner(nil)
	allowed := []collab.CollabMode{collab.ModePipeline, collab.ModeParallel, collab.ModeDebate}
	plan := planner.Plan(collab.PlanRequest{
		Description:  description,
		Input:        contextStr,
		AgentIDs:     syntheticAgentIDs(maxChildren),
		Timeout:      timeout,
		AllowedModes: allowed,
	})
	selected := taskModeFromCollab(plan.Mode)
	if selected == "" {
		selected = taskstore.ModeSingle
	}
	summary := fmt.Sprintf("Selected %s mode with %s.", selected, strings.TrimSpace(plan.DecisionBasis))
	return selected, selected, summary, plan
}

func isSimpleDelegateTask(description, contextStr string) bool {
	text := strings.ToLower(description + "\n" + contextStr)
	if len(strings.Fields(text)) <= 18 &&
		!strings.Contains(text, "\n-") &&
		!strings.Contains(text, "\n1.") &&
		!strings.Contains(text, " and ") &&
		!strings.Contains(text, "、") &&
		!strings.Contains(text, "多个") &&
		!strings.Contains(text, "multi") {
		return true
	}
	return false
}

func syntheticAgentIDs(maxChildren int) []string {
	if maxChildren <= 0 {
		maxChildren = 3
	}
	if maxChildren > 8 {
		maxChildren = 8
	}
	ids := make([]string, 0, maxChildren)
	for i := 1; i <= maxChildren; i++ {
		ids = append(ids, fmt.Sprintf("delegate-%d", i))
	}
	return ids
}

func taskModeFromCollab(mode collab.CollabMode) taskstore.Mode {
	switch mode {
	case collab.ModeParallel:
		return taskstore.ModeParallel
	case collab.ModePipeline:
		return taskstore.ModePipeline
	case collab.ModeDebate:
		return taskstore.ModeDebate
	default:
		return ""
	}
}

func isValidDelegateStatus(status string) bool {
	switch status {
	case StatusPending.String(), StatusRunning.String(), StatusCompleted.String(), StatusFailed.String(), StatusCancelled.String():
		return true
	default:
		return false
	}
}

func cloneDelegateTask(task *DelegateTask) DelegateTask {
	if task == nil {
		return DelegateTask{}
	}
	return *task
}

func summarizeDelegateResult(result string, maxBytes int) (summary, inline string, resultBytes int, truncated bool) {
	result = strings.TrimSpace(result)
	resultBytes = len(result)
	if result == "" {
		return "", "", 0, false
	}
	paragraph := result
	for _, part := range strings.Split(result, "\n\n") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			paragraph = trimmed
			break
		}
	}
	summary = paragraph
	if len(summary) > 300 {
		summary = summary[:300] + "... (truncated)"
	}
	inline = result
	if maxBytes > 0 && len(inline) > maxBytes {
		inline = inline[:maxBytes] + "\n... (truncated)"
		truncated = true
	}
	return summary, inline, resultBytes, truncated
}

func formatDelegateCompletedAt(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func isTerminalDelegateStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusCompleted.String(), StatusFailed.String(), StatusCancelled.String():
		return true
	default:
		return false
	}
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
				Description: "Timeout in seconds (default 120, clamped to 5-1800)",
				Required:    false,
				Default:     120,
			},
			"mode": {
				Type:        "string",
				Description: "Execution planning mode: single, auto, parallel, pipeline, debate, or autonomy_queue. Default single.",
				Required:    false,
				Default:     "single",
			},
			"max_children": {
				Type:        "number",
				Description: "Maximum child tasks allowed when planning multi-agent execution. Default 3.",
				Required:    false,
				Default:     3,
			},
			"include_trace": {
				Type:        "boolean",
				Description: "Whether task_status may return the stored planner trace when requested.",
				Required:    false,
				Default:     false,
			},
			"allow_recursive_delegate": {
				Type:        "boolean",
				Description: "Whether delegated child agents may create more delegate tasks. Default false.",
				Required:    false,
				Default:     false,
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
			"include_result": {
				Type:        "boolean",
				Description: "Whether to include the inline result text",
				Required:    false,
				Default:     true,
			},
			"include_events": {
				Type:        "boolean",
				Description: "Whether to include unified task events and reduced observation",
				Required:    false,
				Default:     false,
			},
			"include_trace": {
				Type:        "boolean",
				Description: "Whether to include the stored planner trace artifact",
				Required:    false,
				Default:     false,
			},
		},
		Handler: dm.handleStatus,
	}
}

// WaitForTasksTool waits until every requested delegated task reaches a
// terminal state. It is read-only and intentionally auto-approved.
func WaitForTasksTool(dm *DelegateManager) *Tool {
	return &Tool{
		Name:        "wait_for_tasks",
		Description: "Wait for delegated tasks to complete, fail, or be cancelled, then return their statuses and results.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"task_ids": {
				Type:        "array",
				Description: "IDs of delegated tasks to wait for",
				Required:    true,
			},
			"timeout": {
				Type:        "number",
				Description: "Maximum total wait in seconds (default 120, maximum 1800; fractional seconds allowed)",
				Required:    false,
				Default:     defaultDelegateTimeoutSeconds,
			},
			"poll_interval_ms": {
				Type:        "number",
				Description: "Status polling interval in milliseconds (default 200, clamped to 10-5000)",
				Required:    false,
				Default:     int(defaultWaitPollInterval / time.Millisecond),
			},
			"include_result": {
				Type:        "boolean",
				Description: "Whether completed task result text is included",
				Required:    false,
				Default:     true,
			},
		},
		Handler: func(args map[string]any) (string, error) {
			return dm.handleWaitForTasks(context.Background(), args)
		},
		ContextDetailedHandler: func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
			out, err := dm.handleWaitForTasks(exec.Context, args)
			return ToolCallResult{Output: out}, err
		},
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
		Parameters: map[string]Param{
			"status": {
				Type:        "string",
				Description: "Optional status filter: pending, running, completed, failed, or cancelled",
				Required:    false,
			},
			"limit": {
				Type:        "number",
				Description: "Maximum tasks to return (default 20, max 100)",
				Required:    false,
				Default:     defaultDelegateListLimit,
			},
			"order": {
				Type:        "string",
				Description: "Sort order by started_at: desc or asc",
				Required:    false,
				Default:     "desc",
			},
			"include_result": {
				Type:        "boolean",
				Description: "Whether to include result summaries for each listed task",
				Required:    false,
				Default:     false,
			},
			"source": {
				Type:        "string",
				Description: "Optional unified task source filter: tool, http, autonomy, or gateway",
				Required:    false,
			},
		},
		Handler: dm.handleList,
	}
}

// DelegateCancelTool 创建任务取消工具
func DelegateCancelTool(dm *DelegateManager) *Tool {
	return &Tool{
		Name:        "delegate_cancel",
		Description: "Cancel a pending or running delegated task.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"task_id": {
				Type:        "string",
				Description: "ID of the task to cancel",
				Required:    true,
			},
			"reason": {
				Type:        "string",
				Description: "Optional cancellation reason",
				Required:    false,
			},
		},
		Handler: dm.handleCancel,
	}
}

// handleDelegate 处理委派请求
func (dm *DelegateManager) handleDelegate(args map[string]any) (string, error) {
	description, ok := args["description"].(string)
	description = strings.TrimSpace(description)
	if !ok || description == "" {
		return "", fmt.Errorf("description is required")
	}

	contextStr := ""
	if c, ok := args["context"]; ok {
		contextStr, _ = c.(string)
	}

	timeout := dm.delegateTimeoutFromArgs(args)
	requestedMode, err := delegateModeArg(args)
	if err != nil {
		return "", err
	}
	maxChildren := 3
	if n, ok := delegateIntArg(args["max_children"]); ok {
		maxChildren = n
	}
	if maxChildren <= 0 {
		maxChildren = 1
	}
	mode, plannedMode, plannerSummary, plannerTrace := planDelegateTaskMode(description, contextStr, requestedMode, timeout, maxChildren)
	allowRecursive := delegateBoolArg(args, "allow_recursive_delegate", false)

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
		ID:             taskID,
		Description:    description,
		Context:        contextStr,
		Workspace:      workspace,
		Mode:           mode,
		PlannedMode:    plannedMode,
		PlannerSummary: plannerSummary,
		Status:         StatusPending,
		StartedAt:      time.Now(),
	}
	dm.tasks[taskID] = task
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()

	dm.recordDelegateTaskCreated(store, events, task, timeout, maxChildren, allowRecursive, plannerTrace)

	// 异步执行
	go dm.executeTask(taskID, description, enrichedContext, timeout)

	result, _ := json.Marshal(delegateStartResponse{
		TaskID:         taskID,
		Status:         StatusRunning.String(),
		Mode:           string(mode),
		PlannedMode:    string(plannedMode),
		PlannerSummary: plannerSummary,
		TraceAvailable: plannerTrace != nil,
		Workspace:      workspace,
		TimeoutSeconds: int(timeout.Seconds()),
		Message:        fmt.Sprintf("Task '%s' delegated. Use wait_for_tasks with this task ID before synthesizing a final answer.", taskID),
	})

	return string(result), nil
}

// executeTask 执行子代理任务
// v0.38.0: 通过 Agent Loop 真正执行子代理任务
func (dm *DelegateManager) executeTask(taskID, description, contextStr string, timeout time.Duration) {
	dm.mu.Lock()
	task := dm.tasks[taskID]
	task.Status = StatusRunning
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	dm.cancels[taskID] = cancel
	executor := dm.agentExecutor
	store := dm.taskStore
	events := dm.taskEvents
	dm.mu.Unlock()
	dm.recordDelegateTaskStarted(store, events, task)
	defer func() {
		cancel()
		dm.mu.Lock()
		delete(dm.cancels, taskID)
		dm.mu.Unlock()
	}()

	// v0.38.0: 如果配置了 agentExecutor，通过 Agent Loop 执行
	if executor != nil {
		result, err := executor(ctx, description, contextStr)
		dm.mu.Lock()
		if task.Status == StatusCancelled {
			// Cancellation was requested through delegate_cancel; preserve it.
		} else if err != nil {
			task.Status = StatusFailed
			task.Error = err.Error()
		} else {
			task.Status = StatusCompleted
			task.Result = result
		}
		if task.CompletedAt.IsZero() {
			task.CompletedAt = time.Now()
		}
		snapshot := cloneDelegateTask(task)
		dm.mu.Unlock()
		dm.recordDelegateTaskFinished(store, events, snapshot)
		return
	}

	// 降级：无 agentExecutor 时返回占位结果
	select {
	case <-ctx.Done():
		dm.mu.Lock()
		if task.Status != StatusCancelled {
			task.Status = StatusFailed
			task.Error = "timeout"
			task.CompletedAt = time.Now()
		}
		snapshot := cloneDelegateTask(task)
		dm.mu.Unlock()
		dm.recordDelegateTaskFinished(store, events, snapshot)
		return
	default:
	}

	dm.mu.Lock()
	if task.Status != StatusCancelled {
		task.Status = StatusCompleted
		task.Result = fmt.Sprintf("Sub-agent task completed (no executor): %s", description)
		task.CompletedAt = time.Now()
	}
	snapshot := cloneDelegateTask(task)
	dm.mu.Unlock()
	dm.recordDelegateTaskFinished(store, events, snapshot)
}

// handleStatus 处理状态查询
func (dm *DelegateManager) handleStatus(args map[string]any) (string, error) {
	taskID, ok := args["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if !ok || taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	includeResult := delegateBoolArg(args, "include_result", true)
	includeEvents := delegateBoolArg(args, "include_events", false)
	includeTrace := delegateBoolArg(args, "include_trace", false)

	dm.mu.RLock()
	task, ok := dm.tasks[taskID]
	snapshot := cloneDelegateTask(task)
	store := dm.taskStore
	dm.mu.RUnlock()

	if !ok {
		if store != nil {
			return dm.handleUnifiedStatus(store, taskID, includeResult, includeEvents, includeTrace)
		}
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	summary, inline, resultBytes, truncated := summarizeDelegateResult(snapshot.Result, dm.config.MaxResultBytesInline)
	mode := snapshot.Mode
	if mode == "" {
		mode = taskstore.ModeSingle
	}
	resp := delegateStatusResponse{
		TaskID:          snapshot.ID,
		Source:          string(taskstore.SourceTool),
		Mode:            string(mode),
		Description:     snapshot.Description,
		Workspace:       snapshot.Workspace,
		Status:          snapshot.Status.String(),
		ResultSummary:   summary,
		ResultBytes:     resultBytes,
		ResultTruncated: truncated,
		Error:           snapshot.Error,
		StartedAt:       snapshot.StartedAt.Format(time.RFC3339),
		CompletedAt:     formatDelegateCompletedAt(snapshot.CompletedAt),
	}
	if includeResult {
		resp.Result = inline
	}
	if store != nil && includeEvents {
		if record, ok, err := store.Get(snapshot.ID); err == nil && ok {
			events, _ := store.Events(snapshot.ID)
			obs := taskstore.ReduceObservation(record, events)
			resp.Observation = &obs
			resp.Events = events
		}
	}
	if store != nil && includeTrace {
		if trace, ok, err := store.PlannerTrace(snapshot.ID); err == nil && ok {
			resp.PlannerTrace = json.RawMessage(trace)
		}
	}
	result, _ := json.Marshal(resp)

	return string(result), nil
}

func delegateTaskIDsArg(args map[string]any) ([]string, error) {
	if args == nil {
		return nil, fmt.Errorf("task_ids is required")
	}
	raw, ok := args["task_ids"]
	if !ok {
		return nil, fmt.Errorf("task_ids is required")
	}
	var values []string
	switch v := raw.(type) {
	case []string:
		values = append(values, v...)
	case []any:
		for _, item := range v {
			id, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("task_ids must contain only strings")
			}
			values = append(values, id)
		}
	default:
		return nil, fmt.Errorf("task_ids must be an array of task IDs")
	}
	seen := make(map[string]struct{}, len(values))
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" {
			return nil, fmt.Errorf("task_ids must not contain empty IDs")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("task_ids must contain at least one task ID")
	}
	if len(ids) > maxWaitTaskIDs {
		return nil, fmt.Errorf("task_ids supports at most %d task IDs", maxWaitTaskIDs)
	}
	return ids, nil
}

func (dm *DelegateManager) waitTimeoutFromArgs(args map[string]any) time.Duration {
	timeout := dm.config.Timeout
	if raw, ok := args["timeout"]; ok {
		if requested, ok := delegateDurationSecondsArg(raw); ok {
			timeout = requested
		}
	}
	if timeout < minWaitTimeout {
		timeout = minWaitTimeout
	}
	if timeout > dm.config.MaxTimeout {
		timeout = dm.config.MaxTimeout
	}
	return timeout
}

func waitPollIntervalFromArgs(args map[string]any) time.Duration {
	interval := defaultWaitPollInterval
	if n, ok := delegateIntArg(args["poll_interval_ms"]); ok {
		interval = time.Duration(n) * time.Millisecond
	}
	if interval < minWaitPollInterval {
		return minWaitPollInterval
	}
	if interval > maxWaitPollInterval {
		return maxWaitPollInterval
	}
	return interval
}

func (dm *DelegateManager) waitTaskStatuses(taskIDs []string, includeResult bool) ([]delegateStatusResponse, []string, error) {
	statuses := make([]delegateStatusResponse, 0, len(taskIDs))
	pending := make([]string, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		statusJSON, err := dm.handleStatus(map[string]any{
			"task_id":        taskID,
			"include_result": includeResult,
		})
		if err != nil {
			return nil, nil, err
		}
		var status delegateStatusResponse
		if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
			return nil, nil, fmt.Errorf("decode task status %q: %w", taskID, err)
		}
		statuses = append(statuses, status)
		if !isTerminalDelegateStatus(status.Status) {
			pending = append(pending, taskID)
		}
	}
	return statuses, pending, nil
}

func marshalWaitResponse(tasks []delegateStatusResponse, pending []string, timedOut bool, started time.Time) (string, error) {
	response, err := json.Marshal(delegateWaitResponse{
		Tasks:          tasks,
		AllTerminal:    len(pending) == 0,
		TimedOut:       timedOut,
		PendingTaskIDs: pending,
		ElapsedMS:      time.Since(started).Milliseconds(),
	})
	if err != nil {
		return "", err
	}
	return string(response), nil
}

func (dm *DelegateManager) handleWaitForTasks(ctx context.Context, args map[string]any) (string, error) {
	taskIDs, err := delegateTaskIDsArg(args)
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := dm.waitTimeoutFromArgs(args)
	pollInterval := waitPollIntervalFromArgs(args)
	includeResult := delegateBoolArg(args, "include_result", true)
	started := time.Now()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		statuses, pending, err := dm.waitTaskStatuses(taskIDs, includeResult)
		if err != nil {
			return "", err
		}
		if len(pending) == 0 {
			return marshalWaitResponse(statuses, pending, false, started)
		}

		select {
		case <-waitCtx.Done():
			if time.Since(started) >= timeout {
				statuses, pending, err = dm.waitTaskStatuses(taskIDs, includeResult)
				if err != nil {
					return "", err
				}
				return marshalWaitResponse(statuses, pending, len(pending) > 0, started)
			}
			return "", fmt.Errorf("wait for delegated tasks cancelled: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (dm *DelegateManager) handleUnifiedStatus(store taskstore.Store, taskID string, includeResult bool, includeEvents bool, includeTrace bool) (string, error) {
	record, ok, err := store.Get(taskID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("task not found: %s", taskID)
	}
	resultText, _, _ := store.Result(taskID)
	summary, inline, resultBytes, truncated := summarizeDelegateResult(resultText, dm.config.MaxResultBytesInline)
	resp := delegateStatusResponse{
		TaskID:          record.ID,
		Source:          string(record.Source),
		Mode:            string(record.Mode),
		ParentID:        record.ParentID,
		Description:     record.Description,
		Workspace:       record.Runtime.Workspace,
		Status:          string(record.Status),
		ResultSummary:   summary,
		ResultBytes:     resultBytes,
		ResultTruncated: truncated,
		StartedAt:       formatDelegateCompletedAt(record.StartedAt),
		CompletedAt:     formatDelegateCompletedAt(record.CompletedAt),
	}
	if includeResult {
		resp.Result = inline
	}
	if includeEvents {
		events, _ := store.Events(taskID)
		obs := taskstore.ReduceObservation(record, events)
		resp.Observation = &obs
		resp.Events = events
	}
	if includeTrace {
		if trace, ok, err := store.PlannerTrace(taskID); err == nil && ok {
			resp.PlannerTrace = json.RawMessage(trace)
		}
	}
	out, _ := json.Marshal(resp)
	return string(out), nil
}

// handleList 处理任务列表
func (dm *DelegateManager) handleList(args map[string]any) (string, error) {
	statusFilter := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "status")))
	if statusFilter != "" && !isValidDelegateStatus(statusFilter) {
		return "", fmt.Errorf("unsupported status filter %q", statusFilter)
	}
	limit := defaultDelegateListLimit
	if n, ok := delegateIntArg(args["limit"]); ok {
		limit = n
	}
	if limit <= 0 {
		limit = defaultDelegateListLimit
	}
	if limit > maxDelegateListLimit {
		limit = maxDelegateListLimit
	}
	order := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "order")))
	if order == "" {
		order = "desc"
	}
	if order != "asc" && order != "desc" {
		return "", fmt.Errorf("unsupported order %q (expected asc or desc)", order)
	}
	includeResult := delegateBoolArg(args, "include_result", false)
	sourceFilter := strings.ToLower(strings.TrimSpace(delegateStringArg(args, "source")))

	dm.mu.RLock()
	snapshots := make([]DelegateTask, 0, len(dm.tasks))
	seenIDs := make(map[string]struct{}, len(dm.tasks))
	for _, t := range dm.tasks {
		snapshots = append(snapshots, cloneDelegateTask(t))
		seenIDs[t.ID] = struct{}{}
	}
	store := dm.taskStore
	dm.mu.RUnlock()

	byStatus := map[string]int{
		StatusPending.String():   0,
		StatusRunning.String():   0,
		StatusCompleted.String(): 0,
		StatusFailed.String():    0,
		StatusCancelled.String(): 0,
	}
	filtered := make([]DelegateTask, 0, len(snapshots))
	for _, t := range snapshots {
		status := t.Status.String()
		byStatus[status]++
		if sourceFilter != "" && sourceFilter != string(taskstore.SourceTool) {
			continue
		}
		if statusFilter != "" && status != statusFilter {
			continue
		}
		filtered = append(filtered, t)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].StartedAt.Equal(filtered[j].StartedAt) {
			if order == "asc" {
				return filtered[i].ID < filtered[j].ID
			}
			return filtered[i].ID > filtered[j].ID
		}
		if order == "asc" {
			return filtered[i].StartedAt.Before(filtered[j].StartedAt)
		}
		return filtered[i].StartedAt.After(filtered[j].StartedAt)
	})
	total := len(filtered)
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	items := make([]delegateListItem, 0, len(filtered))
	for _, t := range filtered {
		summary, _, resultBytes, truncated := summarizeDelegateResult(t.Result, dm.config.MaxResultBytesInline)
		mode := t.Mode
		if mode == "" {
			mode = taskstore.ModeSingle
		}
		item := delegateListItem{
			TaskID:      t.ID,
			Source:      string(taskstore.SourceTool),
			Mode:        string(mode),
			Description: t.Description,
			Workspace:   t.Workspace,
			Status:      t.Status.String(),
			Error:       t.Error,
			StartedAt:   t.StartedAt.Format(time.RFC3339),
			CompletedAt: formatDelegateCompletedAt(t.CompletedAt),
		}
		if includeResult {
			item.ResultSummary = summary
			item.ResultBytes = resultBytes
			item.ResultTruncated = truncated
		}
		items = append(items, item)
	}
	if store != nil {
		filter := taskstore.ListFilter{
			Source: taskstore.Source(sourceFilter),
			Status: taskstore.Status(statusFilter),
			Limit:  maxDelegateListLimit,
		}
		records, err := store.List(filter)
		if err == nil {
			for _, record := range records {
				if _, seen := seenIDs[record.ID]; seen {
					continue
				}
				resultText, _, _ := store.Result(record.ID)
				summary, _, resultBytes, truncated := summarizeDelegateResult(resultText, dm.config.MaxResultBytesInline)
				item := delegateListItem{
					TaskID:      record.ID,
					Source:      string(record.Source),
					Mode:        string(record.Mode),
					ParentID:    record.ParentID,
					Description: record.Description,
					Workspace:   record.Runtime.Workspace,
					Status:      string(record.Status),
					Error:       record.Outcome.UserFeedback,
					StartedAt:   formatDelegateCompletedAt(record.StartedAt),
					CompletedAt: formatDelegateCompletedAt(record.CompletedAt),
				}
				if includeResult {
					item.ResultSummary = summary
					item.ResultBytes = resultBytes
					item.ResultTruncated = truncated
				}
				items = append(items, item)
				total++
				byStatus[string(record.Status)]++
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].StartedAt == items[j].StartedAt {
			if order == "asc" {
				return items[i].TaskID < items[j].TaskID
			}
			return items[i].TaskID > items[j].TaskID
		}
		if order == "asc" {
			return items[i].StartedAt < items[j].StartedAt
		}
		return items[i].StartedAt > items[j].StartedAt
	})
	if len(items) > limit {
		items = items[:limit]
	}

	result, _ := json.Marshal(delegateListResponse{
		Tasks:    items,
		Count:    len(items),
		Total:    total,
		ByStatus: byStatus,
	})

	return string(result), nil
}

func (dm *DelegateManager) handleCancel(args map[string]any) (string, error) {
	taskID, ok := args["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if !ok || taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	reason := strings.TrimSpace(delegateStringArg(args, "reason"))
	if reason == "" {
		reason = "cancelled by user"
	}

	dm.mu.Lock()
	task, ok := dm.tasks[taskID]
	if !ok {
		dm.mu.Unlock()
		return "", fmt.Errorf("task not found: %s", taskID)
	}
	switch task.Status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		status := task.Status.String()
		dm.mu.Unlock()
		return "", fmt.Errorf("task %s cannot be cancelled from status %s", taskID, status)
	}
	task.Status = StatusCancelled
	task.Error = reason
	task.CompletedAt = time.Now()
	cancel := dm.cancels[taskID]
	store := dm.taskStore
	events := dm.taskEvents
	snapshot := cloneDelegateTask(task)
	dm.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	dm.recordDelegateTaskFinished(store, events, snapshot)

	result, _ := json.Marshal(delegateCancelResponse{
		TaskID:  taskID,
		Status:  StatusCancelled.String(),
		Message: fmt.Sprintf("Task '%s' cancelled.", taskID),
	})
	return string(result), nil
}

func (dm *DelegateManager) recordDelegateTaskCreated(store taskstore.Store, events *taskstore.EventBus, task *DelegateTask, timeout time.Duration, maxChildren int, allowRecursive bool, plannerTrace any) {
	if store == nil || events == nil || task == nil {
		return
	}
	mode := task.Mode
	if mode == "" {
		mode = taskstore.ModeSingle
	}
	record := taskstore.Record{
		ID:          task.ID,
		Source:      taskstore.SourceTool,
		Mode:        mode,
		Status:      taskstore.StatusPending,
		Description: task.Description,
		Input:       task.Context,
		CreatedAt:   task.StartedAt,
		Runtime: taskstore.RuntimeRef{
			Type:      "delegate",
			Workspace: task.Workspace,
		},
		Budget: taskstore.Budget{
			Timeout:        timeout,
			MaxChildren:    maxChildren,
			MaxConcurrent:  dm.config.MaxConcurrent,
			AllowRecursive: allowRecursive,
		},
		Metadata: map[string]string{
			"planned_mode":    string(task.PlannedMode),
			"planner_summary": task.PlannerSummary,
		},
	}
	if _, err := store.Create(record); err != nil {
		return
	}
	_ = events.Created(record)
	if plannerTrace != nil {
		_ = store.SavePlannerTrace(task.ID, plannerTrace)
		_ = events.Emit(taskstore.Event{
			Type:    taskstore.EventPlanned,
			TaskID:  task.ID,
			Status:  taskstore.StatusPending,
			Mode:    mode,
			Message: task.PlannerSummary,
			Metadata: map[string]string{
				"planned_mode": string(task.PlannedMode),
			},
		})
	}
}

func (dm *DelegateManager) recordDelegateTaskStarted(store taskstore.Store, events *taskstore.EventBus, task *DelegateTask) {
	if store == nil || events == nil || task == nil {
		return
	}
	record, ok, err := store.Get(task.ID)
	if err != nil || !ok {
		return
	}
	record.Status = taskstore.StatusRunning
	record.StartedAt = task.StartedAt
	if err := store.Update(record); err != nil {
		return
	}
	_ = events.Started(record)
}

func (dm *DelegateManager) recordDelegateTaskFinished(store taskstore.Store, events *taskstore.EventBus, task DelegateTask) {
	if store == nil || events == nil || strings.TrimSpace(task.ID) == "" {
		return
	}
	record, ok, err := store.Get(task.ID)
	if err != nil || !ok {
		return
	}
	record.Status = delegateStatusToUnified(task.Status)
	record.CompletedAt = task.CompletedAt
	record.Outcome.Status = record.Status
	if task.Error != "" {
		record.Outcome.UserFeedback = task.Error
	}
	if err := store.Update(record); err != nil {
		return
	}
	switch task.Status {
	case StatusCompleted:
		_ = store.SaveResult(task.ID, task.Result)
		_ = events.Completed(record, "delegate task completed")
	case StatusFailed:
		_ = events.Failed(record, task.Error)
	case StatusCancelled:
		_ = events.Emit(taskstore.Event{
			Type:    taskstore.EventCancelled,
			TaskID:  task.ID,
			Status:  taskstore.StatusCancelled,
			Message: task.Error,
		})
	}
}

func delegateStatusToUnified(status TaskStatus) taskstore.Status {
	switch status {
	case StatusPending:
		return taskstore.StatusPending
	case StatusRunning:
		return taskstore.StatusRunning
	case StatusCompleted:
		return taskstore.StatusCompleted
	case StatusFailed:
		return taskstore.StatusFailed
	case StatusCancelled:
		return taskstore.StatusCancelled
	default:
		return ""
	}
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
