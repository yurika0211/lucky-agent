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
			"action":      {Type: "string", Description: "Action: status, add, list, report, update, complete, fail, block, unblock, workers, spawn, heartbeat, scale_up, scale_down, set_workers", Required: true},
			"title":       {Type: "string", Description: "Task title for action=add", Required: false},
			"description": {Type: "string", Description: "Task details for action=add", Required: false},
			"priority":    {Type: "string", Description: "Task priority for action=add: low, normal, high, critical", Required: false, Default: "normal"},
			"tags":        {Type: "array", Description: "Tags for action=add", Required: false},
			"state":       {Type: "string", Description: "Filter for action=list: ready, in_progress, blocked, done", Required: false},
			"task_id":     {Type: "string", Description: "Task ID for update, complete, fail, block, unblock, or spawn", Required: false},
			"count":       {Type: "number", Description: "Worker count for scale_up, scale_down, or set_workers", Required: false},
			"limit":       {Type: "number", Description: "Maximum tasks to return for action=report", Required: false},
			"result":      {Type: "string", Description: "Completion result for action=complete", Required: false},
			"error":       {Type: "string", Description: "Error message for action=fail", Required: false},
			"reason":      {Type: "string", Description: "Block reason for action=block", Required: false},
			"retry":       {Type: "boolean", Description: "Whether action=fail should retry the task", Required: false},
		},
		Handler: s.HandleAutonomy,
	})
	r.Register(&Tool{
		Name:            "autonomy_queue_add",
		Description:     "Add a task to the autonomy task queue. Tasks are picked up by workers automatically.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"title":       {Type: "string", Description: "Task title", Required: true},
			"description": {Type: "string", Description: "Detailed task description", Required: false},
			"priority":    {Type: "string", Description: "Priority: low, normal, high, critical", Required: false, Default: "normal"},
			"tags":        {Type: "array", Description: "Tags for categorization", Required: false},
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
			"task_id": {Type: "string", Description: "Task ID to update", Required: true},
			"action":  {Type: "string", Description: "Action: complete, fail, block, unblock", Required: true},
			"result":  {Type: "string", Description: "Result text (for complete action)", Required: false},
			"error":   {Type: "string", Description: "Error message (for fail action)", Required: false},
			"reason":  {Type: "string", Description: "Block reason (for block action)", Required: false},
			"retry":   {Type: "boolean", Description: "Whether to retry on failure (default true)", Required: false},
		},
		Handler: s.HandleQueueUpdate,
	})
	r.Register(&Tool{
		Name:            "autonomy_worker_spawn",
		Description:     "Spawn a worker to execute a specific task from the queue.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermApprove,
		HiddenFromModel: true,
		Parameters: map[string]Param{
			"task_id": {Type: "string", Description: "Task ID to execute", Required: true},
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
		Parameters:      map[string]Param{},
		Handler:         s.HandleHeartbeatTrigger,
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

// HandleAutonomy exposes a single model-visible autonomy control surface while
// keeping the lower-level autonomy_* tools available for internal callers.
func (s *AutonomyToolService) HandleAutonomy(args map[string]any) (string, error) {
	if args == nil {
		args = map[string]any{}
	}
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))

	switch action {
	case "", "status":
		return s.HandleStatus(args)
	case "add", "enqueue", "queue_add":
		return s.HandleQueueAdd(args)
	case "list", "queue", "queue_list":
		return s.HandleQueueList(args)
	case "report", "outputs", "results":
		return s.HandleReport(args)
	case "update", "queue_update":
		return s.HandleQueueUpdate(args)
	case "complete", "fail", "block", "unblock":
		next := cloneToolArgs(args)
		next["action"] = action
		return s.HandleQueueUpdate(next)
	case "workers", "worker_list":
		return s.HandleWorkerList(args)
	case "spawn", "worker_spawn", "run":
		return s.HandleWorkerSpawn(args)
	case "heartbeat", "trigger", "heartbeat_trigger":
		return s.HandleHeartbeatTrigger(args)
	case "scale_up", "scaleup", "workers_add":
		return s.HandleScaleUp(args)
	case "scale_down", "scaledown", "workers_remove":
		return s.HandleScaleDown(args)
	case "set_workers", "workers_set":
		return s.HandleSetWorkers(args)
	default:
		return "", fmt.Errorf("invalid autonomy action %q (use status, add, list, report, update, complete, fail, block, unblock, workers, spawn, heartbeat, scale_up, scale_down, set_workers)", action)
	}
}

func (s *AutonomyToolService) HandleQueueAdd(args map[string]any) (string, error) {
	if err := s.ensureStarted(); err != nil {
		return "", err
	}
	return s.tools.HandleQueueAdd(args)
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
	if err := s.ensureStarted(); err != nil {
		return "", err
	}
	return s.tools.HandleWorkerSpawn(args)
}

func (s *AutonomyToolService) HandleWorkerList(args map[string]any) (string, error) {
	return s.tools.HandleWorkerList(args)
}

func (s *AutonomyToolService) HandleHeartbeatTrigger(args map[string]any) (string, error) {
	if err := s.ensureStarted(); err != nil {
		return "", err
	}
	return s.tools.HandleHeartbeatTrigger(args)
}

func (s *AutonomyToolService) HandleStatus(args map[string]any) (string, error) {
	return s.tools.HandleStatus(args)
}

func (s *AutonomyToolService) HandleScaleUp(args map[string]any) (string, error) {
	if err := s.ensureStarted(); err != nil {
		return "", err
	}
	count, err := parsePositiveCountArg(args, "count", 1)
	if err != nil {
		return "", err
	}
	if err := s.kit.ScaleUp(context.Background(), count); err != nil {
		return "", err
	}
	return s.workerScaleResult("scale_up", count, 0)
}

func (s *AutonomyToolService) HandleScaleDown(args map[string]any) (string, error) {
	count, err := parsePositiveCountArg(args, "count", 1)
	if err != nil {
		return "", err
	}
	removed := s.kit.ScaleDown(count)
	return s.workerScaleResult("scale_down", count, removed)
}

func (s *AutonomyToolService) HandleSetWorkers(args map[string]any) (string, error) {
	if err := s.ensureStarted(); err != nil {
		return "", err
	}
	count, err := parsePositiveCountArg(args, "count", 1)
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
	return string(out), nil
}

func (s *AutonomyToolService) ensureStarted() error {
	if s == nil || s.tools == nil {
		return fmt.Errorf("autonomy service not initialized")
	}
	if s.ensureStart == nil {
		return nil
	}
	if s.kit != nil && s.kit.Status().Started {
		return nil
	}
	return s.ensureStart()
}

func (s *AutonomyToolService) workerScaleResult(action string, requested int, removed int) (string, error) {
	status := s.kit.Status()
	out, _ := json.Marshal(map[string]any{
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
