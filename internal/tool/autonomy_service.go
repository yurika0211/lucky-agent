package tool

import "github.com/yurika0211/luckyharness/internal/autonomy"

// AutonomyToolService wraps autonomy tool definitions for tool-layer registration.
type AutonomyToolService struct {
	kit   *autonomy.AutonomyKit
	tools *autonomy.ToolDefinitions
}

// NewAutonomyToolService creates an autonomy tool service.
func NewAutonomyToolService(kit *autonomy.AutonomyKit) *AutonomyToolService {
	if kit == nil {
		return nil
	}
	return &AutonomyToolService{
		kit:   kit,
		tools: autonomy.NewToolDefinitions(kit),
	}
}

// RegisterTools registers autonomy-related tools onto the registry.
func (s *AutonomyToolService) RegisterTools(r *Registry) {
	if s == nil || r == nil || s.tools == nil {
		return
	}

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
		Handler: s.tools.HandleQueueAdd,
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
		Handler: s.tools.HandleQueueList,
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
		Handler: s.tools.HandleQueueUpdate,
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
		Handler: s.tools.HandleWorkerSpawn,
	})
	r.Register(&Tool{
		Name:            "autonomy_worker_list",
		Description:     "List active workers and their status.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]Param{},
		Handler:         s.tools.HandleWorkerList,
	})
	r.Register(&Tool{
		Name:            "autonomy_heartbeat_trigger",
		Description:     "Manually trigger a heartbeat cycle to check for work and dispatch tasks.",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]Param{},
		Handler:         s.tools.HandleHeartbeatTrigger,
	})
	r.Register(&Tool{
		Name:            "autonomy_status",
		Description:     "Get the overall status of the autonomy system (queue, workers, heartbeat).",
		Category:        CatDelegate,
		Source:          "builtin",
		Permission:      PermAuto,
		HiddenFromModel: true,
		Parameters:      map[string]Param{},
		Handler:         s.tools.HandleStatus,
	})
}
