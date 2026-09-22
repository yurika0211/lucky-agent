package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/autonomy"
)

// AutonomyStartFunc starts the runtime autonomy kit when a tool action needs
// workers or heartbeat execution.
type AutonomyStartFunc func() error

// AutonomyToolService wraps autonomy tool definitions for tool-layer registration.
type AutonomyToolService struct {
	kit         *autonomy.AutonomyKit
	tools       *autonomy.ToolDefinitions
	ensureStart AutonomyStartFunc
}

// NewAutonomyToolService creates an autonomy tool service.
func NewAutonomyToolService(kit *autonomy.AutonomyKit, start ...AutonomyStartFunc) *AutonomyToolService {
	if kit == nil {
		return nil
	}
	var ensureStart AutonomyStartFunc
	if len(start) > 0 {
		ensureStart = start[0]
	}
	return &AutonomyToolService{
		kit:         kit,
		tools:       autonomy.NewToolDefinitions(kit),
		ensureStart: ensureStart,
	}
}

// RegisterTools registers autonomy-related tools onto the registry.
func (s *AutonomyToolService) RegisterTools(r *Registry) {
	if s == nil || r == nil || s.tools == nil {
		return
	}

	r.Register(&Tool{
		Name:        "autonomy",
		Description: "High-level autonomy manager. Use it to enqueue background tasks, inspect the autonomy queue and workers, or report worker outputs when the user asks for deferred, proactive, or background work.",
		Category:    CatDelegate,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"action":              {Type: "string", Description: "Action: status, add, list, report, update, complete, fail, block, unblock, resolve, workers, spawn, heartbeat, scale_up, scale_down, set_workers", Required: true},
			"title":               {Type: "string", Description: "Task title for action=add", Required: false},
			"description":         {Type: "string", Description: "Task details for action=add", Required: false},
			"acceptance_criteria": {Type: "array", Description: "Concrete completion criteria checked by an independent review before marking a background task done", Required: false},
			"operation_id":        {Type: "string", Description: "Ambiguous operation ID for action=resolve; requires explicit operator reconciliation", Required: false},
			"resolution":          {Type: "string", Description: "For action=resolve: completed (supply observed result), or retry (operator confirmed it is safe to repeat)", Required: false},
			"priority":            {Type: "string", Description: "Task priority for action=add: low, normal, high, critical", Required: false, Default: "normal"},
			"tags":                {Type: "array", Description: "Tags for action=add", Required: false},
			"dry_run":             {Type: "boolean", Description: "Preview add/spawn/scale/set_workers/heartbeat without mutating runtime state", Required: false, Default: false},
			"start_if_needed":     {Type: "boolean", Description: "Allow write actions to start the autonomy runtime when it is stopped", Required: false, Default: true},
			"idempotency_key":     {Type: "string", Description: "Optional key for action=add duplicate detection", Required: false},
			"state":               {Type: "string", Description: "Filter for action=list: ready, in_progress, blocked, done", Required: false},
			"task_id":             {Type: "string", Description: "Task ID for update, complete, fail, block, unblock, or spawn", Required: false},
			"count":               {Type: "number", Description: "Worker count for scale_up, scale_down, or set_workers", Required: false},
			"limit":               {Type: "number", Description: "Maximum tasks to return for action=report", Required: false},
			"result":              {Type: "string", Description: "Completion result for action=complete", Required: false},
			"error":               {Type: "string", Description: "Error message for action=fail", Required: false},
			"reason":              {Type: "string", Description: "Block reason for action=block", Required: false},
			"retry":               {Type: "boolean", Description: "Whether action=fail should retry the task", Required: false},
		},
		Handler: s.HandleAutonomy,
		ContextDetailedHandler: func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
			if err := authorizeAutonomyOperatorAction(exec, args); err != nil {
				return ToolCallResult{}, err
			}
			out, err := s.HandleAutonomy(args)
			return ToolCallResult{Output: out}, err
		},
	})
	r.Register(&Tool{
		Name:            "autonomy_queue_add",
		Description:     "Add a task to the autonomy task queue. Tasks are picked up by workers automatically.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"title":               {Type: "string", Description: "Task title", Required: true},
			"description":         {Type: "string", Description: "Detailed task description", Required: false},
			"acceptance_criteria": {Type: "array", Description: "Concrete completion criteria", Required: false},
			"priority":            {Type: "string", Description: "Priority: low, normal, high, critical", Required: false, Default: "normal"},
			"tags":                {Type: "array", Description: "Tags for categorization", Required: false},
			"dry_run":             {Type: "boolean", Description: "Preview the queue add without adding or starting runtime", Required: false, Default: false},
			"start_if_needed":     {Type: "boolean", Description: "Allow this call to start the autonomy runtime when it is stopped", Required: false, Default: true},
			"idempotency_key":     {Type: "string", Description: "Optional key for duplicate detection", Required: false},
		},
		Handler: s.HandleQueueAdd,
	})
	r.Register(&Tool{
		Name:            "autonomy_queue_list",
		Description:     "List tasks in the autonomy queue. Optionally filter by state.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"state": {Type: "string", Description: "Filter by state: ready, in_progress, blocked, done", Required: false},
		},
		Handler: s.HandleQueueList,
	})
	r.Register(&Tool{
		Name:            "autonomy_queue_update",
		Description:     "Update a task's state in the autonomy queue.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"task_id":      {Type: "string", Description: "Task ID to update", Required: true},
			"action":       {Type: "string", Description: "Action: complete, fail, block, unblock, resolve", Required: true},
			"operation_id": {Type: "string", Description: "Operation to reconcile", Required: false},
			"resolution":   {Type: "string", Description: "completed or retry; requires operator reconciliation", Required: false},
			"result":       {Type: "string", Description: "Result text (for complete action)", Required: false},
			"error":        {Type: "string", Description: "Error message (for fail action)", Required: false},
			"reason":       {Type: "string", Description: "Block reason (for block action)", Required: false},
			"retry":        {Type: "boolean", Description: "Whether to retry on failure (default true)", Required: false},
		},
		Handler: s.HandleQueueUpdate,
		ContextDetailedHandler: func(exec ExecutionContext, args map[string]any) (ToolCallResult, error) {
			if err := authorizeAutonomyOperatorAction(exec, args); err != nil {
				return ToolCallResult{}, err
			}
			out, err := s.HandleQueueUpdate(args)
			return ToolCallResult{Output: out}, err
		},
	})
	r.Register(&Tool{
		Name:            "autonomy_worker_spawn",
		Description:     "Spawn a worker to execute a specific task from the queue.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermApprove,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"task_id":         {Type: "string", Description: "Task ID to execute", Required: true},
			"dry_run":         {Type: "boolean", Description: "Preview dispatch without spawning", Required: false, Default: false},
			"start_if_needed": {Type: "boolean", Description: "Allow this call to start the autonomy runtime when it is stopped", Required: false, Default: true},
		},
		Handler: s.HandleWorkerSpawn,
	})
	r.Register(&Tool{
		Name:            "autonomy_worker_list",
		Description:     "List active workers and their status.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]Param{},
		Handler:         s.HandleWorkerList,
	})
	r.Register(&Tool{
		Name:            "autonomy_heartbeat_trigger",
		Description:     "Manually trigger a heartbeat cycle to check for work and dispatch tasks.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"dry_run":         {Type: "boolean", Description: "Preview heartbeat without triggering it", Required: false, Default: false},
			"start_if_needed": {Type: "boolean", Description: "Allow this call to start the autonomy runtime when it is stopped", Required: false, Default: true},
		},
		Handler: s.HandleHeartbeatTrigger,
	})
	r.Register(&Tool{
		Name:            "autonomy_status",
		Description:     "Get the overall status of the autonomy system (queue, workers, heartbeat).",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]Param{},
		Handler:         s.HandleStatus,
	})
}

// Model-generated reconciliation cannot establish the outcome of an external
// operation. Require an exact operator instruction in the current user turn.
// Direct service/registry callers without a Source are trusted operator APIs.
func authorizeAutonomyOperatorAction(exec ExecutionContext, args map[string]any) error {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if (action != "resolve" && action != "complete") || exec.Source == "" {
		return nil
	}
	if exec.Source == "autonomy" {
		return fmt.Errorf("background workers cannot reconcile operations or manually complete tasks")
	}
	text := strings.TrimSpace(exec.UserRequest)
	if strings.HasPrefix(text, "```json") {
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "```json"), "```"))
	}
	var instruction map[string]any
	if json.Unmarshal([]byte(text), &instruction) == nil {
		matches := true
		keys := []string{"action", "task_id"}
		if action == "resolve" {
			keys = append(keys, "operation_id", "resolution")
		}
		for _, key := range keys {
			expected, ok := instruction[key].(string)
			actual, actualOK := args[key].(string)
			if !ok || !actualOK || expected != actual || strings.TrimSpace(actual) == "" {
				matches = false
			}
		}
		if instruction["resolution"] == "completed" || action == "complete" {
			expected, _ := instruction["result"].(string)
			actual, _ := args["result"].(string)
			matches = matches && strings.TrimSpace(expected) != "" && expected == actual
		}
		if matches {
			return nil
		}
	}
	return fmt.Errorf("%s requires the operator's current message to contain the exact action JSON including task_id and observed result (plus operation_id/resolution for resolve); inspect the external operation before retrying or completing", action)
}

// HandleAutonomy exposes a single model-visible autonomy control surface while
// keeping the lower-level autonomy_* tools available for internal callers.
func (s *AutonomyToolService) HandleAutonomy(args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	canonical, err := canonicalAutonomyAction(action)
	if err != nil {
		return "", err
	}

	var (
		out     string
		callErr error
	)
	switch canonical {
	case "status":
		out, callErr = s.HandleStatus(args)
	case "add":
		out, callErr = s.HandleQueueAdd(args)
	case "list":
		out, callErr = s.HandleQueueList(args)
	case "report":
		out, callErr = s.HandleReport(args)
	case "update":
		out, callErr = s.HandleQueueUpdate(args)
	case "complete", "fail", "block", "unblock", "resolve":
		next := cloneToolArgs(args)
		next["action"] = canonical
		out, callErr = s.HandleQueueUpdate(next)
	case "workers":
		out, callErr = s.HandleWorkerList(args)
	case "spawn":
		out, callErr = s.HandleWorkerSpawn(args)
	case "heartbeat":
		out, callErr = s.HandleHeartbeatTrigger(args)
	case "scale_up":
		out, callErr = s.HandleScaleUp(args)
	case "scale_down":
		out, callErr = s.HandleScaleDown(args)
	case "set_workers":
		out, callErr = s.HandleSetWorkers(args)
	}
	return s.withCanonicalAction(out, callErr, action, canonical)
}

func (s *AutonomyToolService) HandleQueueAdd(args map[string]any) (string, error) {
	runtimeStartedBefore := s.runtimeStarted()
	existing := s.findTaskByIdempotencyKey(args)
	if existing != nil {
		out, _ := json.Marshal(map[string]any{
			"ok":                      true,
			"action":                  "add",
			"task_id":                 existing.ID,
			"title":                   existing.Title,
			"priority":                existing.Priority.String(),
			"state":                   existing.State,
			"tags":                    existing.Tags,
			"deduped":                 true,
			"idempotency_key":         strings.TrimSpace(fmt.Sprint(args["idempotency_key"])),
			"runtime_started_before":  runtimeStartedBefore,
			"runtime_started_by_tool": false,
			"message":                 fmt.Sprintf("Task '%s' already exists for idempotency key", existing.Title),
		})
		return string(out), nil
	}
	if autonomyBoolArg(args, "dry_run", false) {
		return s.queueAddDryRun(args, runtimeStartedBefore)
	}
	_, startedByTool, err := s.ensureStarted(autonomyBoolArg(args, "start_if_needed", true))
	if err != nil {
		return "", err
	}
	out, err := s.tools.HandleQueueAdd(args)
	if err != nil {
		return "", err
	}
	return augmentJSONFields(out, map[string]any{
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": startedByTool,
	})
}

func (s *AutonomyToolService) HandleQueueList(args map[string]any) (string, error) {
	return s.tools.HandleQueueList(args)
}

func (s *AutonomyToolService) HandleReport(args map[string]any) (string, error) {
	return s.tools.HandleReport(args)
}

func (s *AutonomyToolService) HandleQueueUpdate(args map[string]any) (string, error) {
	return s.tools.HandleQueueUpdate(args)
}

func (s *AutonomyToolService) HandleWorkerSpawn(args map[string]any) (string, error) {
	runtimeStartedBefore := s.runtimeStarted()
	if autonomyBoolArg(args, "dry_run", false) {
		return s.workerSpawnDryRun(args, runtimeStartedBefore)
	}
	_, startedByTool, err := s.ensureStarted(autonomyBoolArg(args, "start_if_needed", true))
	if err != nil {
		return "", err
	}
	out, err := s.tools.HandleWorkerSpawn(args)
	if err != nil {
		return "", err
	}
	return augmentJSONFields(out, map[string]any{
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": startedByTool,
	})
}

func (s *AutonomyToolService) HandleWorkerList(args map[string]any) (string, error) {
	return s.tools.HandleWorkerList(args)
}

func (s *AutonomyToolService) HandleHeartbeatTrigger(args map[string]any) (string, error) {
	runtimeStartedBefore := s.runtimeStarted()
	if autonomyBoolArg(args, "dry_run", false) {
		status := s.kit.Status()
		out, _ := json.Marshal(map[string]any{
			"ok":                      true,
			"action":                  "heartbeat",
			"dry_run":                 true,
			"would_start_runtime":     !runtimeStartedBefore && autonomyBoolArg(args, "start_if_needed", true),
			"runtime_started_before":  runtimeStartedBefore,
			"runtime_started_by_tool": false,
			"queue_ready":             status.QueueReady,
			"queue_blocked":           status.QueueBlocked,
			"worker_count":            status.PoolStats.WorkerCount,
			"warnings":                []string{"dry_run=true; heartbeat was not triggered"},
		})
		return string(out), nil
	}
	_, startedByTool, err := s.ensureStarted(autonomyBoolArg(args, "start_if_needed", true))
	if err != nil {
		return "", err
	}
	out, err := s.tools.HandleHeartbeatTrigger(args)
	if err != nil {
		return "", err
	}
	return augmentJSONFields(out, map[string]any{
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": startedByTool,
	})
}

func (s *AutonomyToolService) HandleStatus(args map[string]any) (string, error) {
	return s.tools.HandleStatus(args)
}

func (s *AutonomyToolService) HandleScaleUp(args map[string]any) (string, error) {
	runtimeStartedBefore := s.runtimeStarted()
	count, err := parsePositiveCountArg(args, "count", 1)
	if err != nil {
		return "", err
	}
	if autonomyBoolArg(args, "dry_run", false) {
		return s.workerScaleDryRun("scale_up", count, runtimeStartedBefore)
	}
	_, startedByTool, err := s.ensureStarted(autonomyBoolArg(args, "start_if_needed", true))
	if err != nil {
		return "", err
	}
	if err := s.kit.ScaleUp(context.Background(), count); err != nil {
		return "", err
	}
	out, err := s.workerScaleResult("scale_up", count, 0)
	if err != nil {
		return "", err
	}
	return augmentJSONFields(out, map[string]any{
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": startedByTool,
	})
}

func (s *AutonomyToolService) HandleScaleDown(args map[string]any) (string, error) {
	count, err := parsePositiveCountArg(args, "count", 1)
	if err != nil {
		return "", err
	}
	if autonomyBoolArg(args, "dry_run", false) {
		return s.workerScaleDryRun("scale_down", count, s.runtimeStarted())
	}
	removed := s.kit.ScaleDown(count)
	return s.workerScaleResult("scale_down", count, removed)
}

func (s *AutonomyToolService) HandleSetWorkers(args map[string]any) (string, error) {
	runtimeStartedBefore := s.runtimeStarted()
	count, err := parsePositiveCountArg(args, "count", 1)
	if err != nil {
		return "", err
	}
	if autonomyBoolArg(args, "dry_run", false) {
		return s.workerScaleDryRun("set_workers", count, runtimeStartedBefore)
	}
	_, startedByTool, err := s.ensureStarted(autonomyBoolArg(args, "start_if_needed", true))
	if err != nil {
		return "", err
	}
	actual, err := s.kit.SetWorkerCount(context.Background(), count)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"action":        "set_workers",
		"requested":     count,
		"worker_count":  actual,
		"pool_stats":    s.kit.Status().PoolStats,
		"queue_ready":   s.kit.Status().QueueReady,
		"queue_blocked": s.kit.Status().QueueBlocked,
	})
	return augmentJSONFields(string(out), map[string]any{
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": startedByTool,
	})
}

func (s *AutonomyToolService) ensureStarted(startIfNeeded bool) (bool, bool, error) {
	if s == nil || s.tools == nil {
		return false, false, fmt.Errorf("autonomy service not initialized")
	}
	startedBefore := s.runtimeStarted()
	if s.ensureStart == nil {
		return startedBefore, false, nil
	}
	if startedBefore {
		return true, false, nil
	}
	if !startIfNeeded {
		return false, false, fmt.Errorf("autonomy runtime is not started; set start_if_needed=true or start autonomy explicitly")
	}
	return false, true, s.ensureStart()
}

func (s *AutonomyToolService) workerScaleResult(action string, requested int, removed int) (string, error) {
	status := s.kit.Status()
	out, _ := json.Marshal(map[string]any{
		"ok":            true,
		"action":        action,
		"requested":     requested,
		"removed":       removed,
		"worker_count":  status.PoolStats.WorkerCount,
		"idle_workers":  status.PoolStats.IdleWorkers,
		"busy_workers":  status.PoolStats.BusyWorkers,
		"queue_ready":   status.QueueReady,
		"queue_blocked": status.QueueBlocked,
	})
	return string(out), nil
}

func (s *AutonomyToolService) runtimeStarted() bool {
	return s != nil && s.kit != nil && s.kit.Status().Started
}

func (s *AutonomyToolService) withCanonicalAction(out string, err error, original, canonical string) (string, error) {
	if err != nil {
		return "", err
	}
	if original == "" {
		original = canonical
	}
	return augmentJSONFields(out, map[string]any{
		"input_action":     original,
		"canonical_action": canonical,
	})
}

func canonicalAutonomyAction(action string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "", "status":
		return "status", nil
	case "add", "enqueue", "queue_add":
		return "add", nil
	case "list", "queue", "queue_list":
		return "list", nil
	case "report", "outputs", "results":
		return "report", nil
	case "update", "queue_update":
		return "update", nil
	case "complete":
		return "complete", nil
	case "fail":
		return "fail", nil
	case "block":
		return "block", nil
	case "unblock":
		return "unblock", nil
	case "resolve":
		return "resolve", nil
	case "workers", "worker_list":
		return "workers", nil
	case "spawn", "worker_spawn", "run":
		return "spawn", nil
	case "heartbeat", "trigger", "heartbeat_trigger":
		return "heartbeat", nil
	case "scale_up", "scaleup", "workers_add":
		return "scale_up", nil
	case "scale_down", "scaledown", "workers_remove":
		return "scale_down", nil
	case "set_workers", "workers_set":
		return "set_workers", nil
	default:
		return "", fmt.Errorf("invalid autonomy action %q (use status, add, list, report, update, complete, fail, block, unblock, resolve, workers, spawn, heartbeat, scale_up, scale_down, set_workers)", action)
	}
}

func (s *AutonomyToolService) findTaskByIdempotencyKey(args map[string]any) *autonomy.QueueTask {
	key := strings.TrimSpace(fmt.Sprint(args["idempotency_key"]))
	if key == "" || key == "<nil>" || s == nil || s.kit == nil || s.kit.Queue() == nil {
		return nil
	}
	for _, task := range s.kit.Queue().ListAll() {
		if task != nil && task.Metadata != nil && task.Metadata["idempotency_key"] == key {
			return task
		}
	}
	return nil
}

func (s *AutonomyToolService) queueAddDryRun(args map[string]any, runtimeStartedBefore bool) (string, error) {
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	description, _ := args["description"].(string)
	criteria, err := autonomy.ParseAcceptanceCriteria(args["acceptance_criteria"])
	if err != nil {
		return "", err
	}
	priorityStr := "normal"
	if p, ok := args["priority"].(string); ok && strings.TrimSpace(p) != "" {
		priorityStr = strings.TrimSpace(p)
	}
	tags := autonomyTagsArg(args)
	ready, inProgress, blocked, done := s.kit.Queue().Stats()
	out, _ := json.Marshal(map[string]any{
		"ok":                      true,
		"action":                  "add",
		"dry_run":                 true,
		"would_start_runtime":     !runtimeStartedBefore && autonomyBoolArg(args, "start_if_needed", true),
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": false,
		"task": map[string]any{
			"title":               title,
			"description":         description,
			"acceptance_criteria": criteria,
			"priority":            autonomy.ParseTaskPriority(priorityStr).String(),
			"tags":                tags,
		},
		"idempotency_key":   strings.TrimSpace(fmt.Sprint(args["idempotency_key"])),
		"queue_ready":       ready,
		"queue_in_progress": inProgress,
		"queue_blocked":     blocked,
		"queue_done":        done,
		"warnings":          []string{"dry_run=true; task was not queued"},
	})
	return string(out), nil
}

func (s *AutonomyToolService) workerSpawnDryRun(args map[string]any, runtimeStartedBefore bool) (string, error) {
	taskID, _ := args["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}
	task, ok := s.kit.Queue().Get(taskID)
	if !ok {
		return "", fmt.Errorf("task %s not found", taskID)
	}
	out, _ := json.Marshal(map[string]any{
		"ok":                      true,
		"action":                  "spawn",
		"dry_run":                 true,
		"task_id":                 task.ID,
		"task_state":              task.State,
		"would_spawn":             task.State == autonomy.TaskReady,
		"would_start_runtime":     !runtimeStartedBefore && autonomyBoolArg(args, "start_if_needed", true),
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": false,
		"warnings":                []string{"dry_run=true; worker was not spawned"},
	})
	return string(out), nil
}

func (s *AutonomyToolService) workerScaleDryRun(action string, requested int, runtimeStartedBefore bool) (string, error) {
	status := s.kit.Status()
	out, _ := json.Marshal(map[string]any{
		"ok":                      true,
		"action":                  action,
		"dry_run":                 true,
		"requested":               requested,
		"would_start_runtime":     (action == "scale_up" || action == "set_workers") && !runtimeStartedBefore,
		"runtime_started_before":  runtimeStartedBefore,
		"runtime_started_by_tool": false,
		"worker_count":            status.PoolStats.WorkerCount,
		"idle_workers":            status.PoolStats.IdleWorkers,
		"busy_workers":            status.PoolStats.BusyWorkers,
		"queue_ready":             status.QueueReady,
		"queue_blocked":           status.QueueBlocked,
		"warnings":                []string{"dry_run=true; worker count was not changed"},
	})
	return string(out), nil
}

func autonomyTagsArg(args map[string]any) []string {
	var tags []string
	switch raw := args["tags"].(type) {
	case []string:
		tags = append(tags, raw...)
	case []any:
		for _, item := range raw {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				tags = append(tags, strings.TrimSpace(s))
			}
		}
	}
	return tags
}

func augmentJSONFields(out string, fields map[string]any) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return out, nil
	}
	for k, v := range fields {
		payload[k] = v
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func autonomyBoolArg(args map[string]any, key string, fallback bool) bool {
	if args == nil {
		return fallback
	}
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y", "on":
			return true
		case "false", "0", "no", "n", "off":
			return false
		default:
			return fallback
		}
	default:
		return fallback
	}
}

func parsePositiveCountArg(args map[string]any, name string, fallback int) (int, error) {
	if args == nil {
		return fallback, nil
	}
	raw, ok := args[name]
	if !ok || raw == nil {
		return fallback, nil
	}
	switch v := raw.(type) {
	case int:
		if v <= 0 {
			return 0, fmt.Errorf("%s must be positive", name)
		}
		return v, nil
	case int64:
		if v <= 0 {
			return 0, fmt.Errorf("%s must be positive", name)
		}
		return int(v), nil
	case float64:
		if v <= 0 {
			return 0, fmt.Errorf("%s must be positive", name)
		}
		return int(v), nil
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return fallback, nil
		}
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
			return 0, fmt.Errorf("parse %s: %w", name, err)
		}
		if n <= 0 {
			return 0, fmt.Errorf("%s must be positive", name)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%s must be a number", name)
	}
}

func cloneToolArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}
