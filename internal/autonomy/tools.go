package autonomy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Built-in Tools for Agent Loop integration
// ---------------------------------------------------------------------------

// ToolDefinitions returns the tool definitions for autonomy tools.
// These are registered with the agent's tool registry so the LLM
// can call them directly during a RunLoop.
type ToolDefinitions struct {
	kit *AutonomyKit
}

// NewToolDefinitions creates tool definitions bound to an autonomy kit.
func NewToolDefinitions(kit *AutonomyKit) *ToolDefinitions {
	return &ToolDefinitions{kit: kit}
}

// AutonomyQueueAdd adds a task to the autonomy task queue.
// Tool: autonomy_queue_add
func (td *ToolDefinitions) HandleQueueAdd(args map[string]any) (string, error) {
	title, _ := args["title"].(string)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}

	description, _ := args["description"].(string)

	priorityStr := "normal"
	if p, ok := args["priority"].(string); ok {
		priorityStr = p
	}
	priority := ParseTaskPriority(priorityStr)

	var tags []string
	if t, ok := args["tags"].([]any); ok {
		for _, tag := range t {
			if s, ok := tag.(string); ok {
				tags = append(tags, s)
			}
		}
	}

	task, err := td.kit.AddTaskWithError(title, description, priority, tags)
	if err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]any{
		"task_id":  task.ID,
		"title":    task.Title,
		"priority": task.Priority.String(),
		"state":    task.State,
		"message":  fmt.Sprintf("Task '%s' added to queue with %s priority", title, priority),
	})

	return string(result), nil
}

// HandleQueueList lists tasks in the queue.
// Tool: autonomy_queue_list
func (td *ToolDefinitions) HandleQueueList(args map[string]any) (string, error) {
	stateFilter, _ := args["state"].(string)

	var tasks []*QueueTask
	if stateFilter != "" {
		tasks = td.kit.Queue().ListByState(TaskState(stateFilter))
	} else {
		tasks = td.kit.Queue().ListAll()
	}

	// Summarize for LLM consumption
	ready, inProgress, blocked, done := td.kit.Queue().Stats()

	var items []map[string]any
	for _, t := range tasks {
		item := map[string]any{
			"id":          t.ID,
			"title":       t.Title,
			"priority":    t.Priority.String(),
			"state":       t.State,
			"assigned_to": t.AssignedTo,
			"tags":        t.Tags,
		}
		if t.Result != "" {
			item["result_preview"] = truncateToolText(t.Result, 1200)
		}
		if t.Error != "" {
			item["error"] = truncateToolText(t.Error, 1200)
		}
		if t.BlockReason != "" {
			item["block_reason"] = truncateToolText(t.BlockReason, 1200)
		}
		if !t.CreatedAt.IsZero() {
			item["created_at"] = t.CreatedAt
		}
		if !t.StartedAt.IsZero() {
			item["started_at"] = t.StartedAt
		}
		if !t.CompletedAt.IsZero() {
			item["completed_at"] = t.CompletedAt
		}
		items = append(items, item)
	}

	result, _ := json.Marshal(map[string]any{
		"tasks":       items,
		"total":       len(items),
		"ready":       ready,
		"in_progress": inProgress,
		"blocked":     blocked,
		"done":        done,
	})

	return string(result), nil
}

// HandleReport returns recent autonomy task outputs for user-facing summaries.
func (td *ToolDefinitions) HandleReport(args map[string]any) (string, error) {
	state := TaskDone
	if raw, _ := args["state"].(string); strings.TrimSpace(raw) != "" {
		state = TaskState(strings.TrimSpace(raw))
	}
	limit := parseToolLimit(args, 5, 20)

	tasks := td.kit.Queue().ListByState(state)
	total := len(tasks)
	sort.SliceStable(tasks, func(i, j int) bool {
		return taskReportTime(tasks[i]).After(taskReportTime(tasks[j]))
	})
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}

	items := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		item := map[string]any{
			"id":          t.ID,
			"title":       t.Title,
			"description": t.Description,
			"priority":    t.Priority.String(),
			"state":       t.State,
			"assigned_to": t.AssignedTo,
			"tags":        t.Tags,
			"created_at":  t.CreatedAt,
		}
		if !t.StartedAt.IsZero() {
			item["started_at"] = t.StartedAt
		}
		if !t.CompletedAt.IsZero() {
			item["completed_at"] = t.CompletedAt
		}
		if t.Result != "" {
			item["result"] = truncateToolText(t.Result, 4000)
		}
		if t.Error != "" {
			item["error"] = truncateToolText(t.Error, 4000)
		}
		if t.BlockReason != "" {
			item["block_reason"] = truncateToolText(t.BlockReason, 4000)
		}
		items = append(items, item)
	}

	result, _ := json.Marshal(map[string]any{
		"state":    state,
		"total":    total,
		"returned": len(items),
		"tasks":    items,
	})
	return string(result), nil
}

// HandleQueueUpdate updates a task's state.
// Tool: autonomy_queue_update
func (td *ToolDefinitions) HandleQueueUpdate(args map[string]any) (string, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	action, _ := args["action"].(string)
	if action == "" {
		return "", fmt.Errorf("action is required (complete, fail, block, unblock)")
	}

	var err error
	var msg string

	switch action {
	case "complete":
		result, _ := args["result"].(string)
		err = td.kit.Queue().Complete(taskID, result)
		msg = fmt.Sprintf("Task %s completed", taskID)
	case "fail":
		errMsg, _ := args["error"].(string)
		retry := true
		if r, ok := args["retry"].(bool); ok {
			retry = r
		}
		err = td.kit.Queue().Fail(taskID, errMsg, retry)
		if retry {
			msg = fmt.Sprintf("Task %s failed (queued for retry)", taskID)
		} else {
			msg = fmt.Sprintf("Task %s failed (blocked)", taskID)
		}
	case "block":
		reason, _ := args["reason"].(string)
		err = td.kit.Queue().Block(taskID, reason)
		msg = fmt.Sprintf("Task %s blocked: %s", taskID, reason)
	case "unblock":
		err = td.kit.Queue().Unblock(taskID)
		msg = fmt.Sprintf("Task %s unblocked and back in ready queue", taskID)
	default:
		return "", fmt.Errorf("unknown action: %s (use: complete, fail, block, unblock)", action)
	}

	if err != nil {
		return "", err
	}

	result, _ := json.Marshal(map[string]any{
		"task_id": taskID,
		"action":  action,
		"message": msg,
	})

	return string(result), nil
}

// HandleWorkerSpawn spawns a new worker to execute a specific task.
// Tool: autonomy_worker_spawn
func (td *ToolDefinitions) HandleWorkerSpawn(args map[string]any) (string, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	task, ok := td.kit.Queue().Get(taskID)
	if !ok {
		return "", fmt.Errorf("task %s not found", taskID)
	}

	if task.State != TaskReady {
		return "", fmt.Errorf("task %s is not ready (state: %s)", taskID, task.State)
	}

	// Find an idle worker
	worker := td.kit.Pool().findIdleWorker()
	if worker == nil {
		return "", fmt.Errorf("no idle workers available")
	}

	// Pull the task for this worker
	pulled := td.kit.Queue().Pull(worker.ID)
	if pulled == nil || pulled.ID != taskID {
		return "", fmt.Errorf("failed to pull task %s", taskID)
	}

	// Execute asynchronously
	go td.kit.Pool().executeTask(context.Background(), worker, pulled)

	result, _ := json.Marshal(map[string]any{
		"task_id":   taskID,
		"worker_id": worker.ID,
		"status":    "dispatched",
		"message":   fmt.Sprintf("Task '%s' dispatched to worker %s", task.Title, worker.ID),
	})

	return string(result), nil
}

// HandleWorkerList lists active workers.
// Tool: autonomy_worker_list
func (td *ToolDefinitions) HandleWorkerList(args map[string]any) (string, error) {
	workers := td.kit.Pool().ListWorkers()
	stats := td.kit.Pool().Stats()

	result, _ := json.Marshal(map[string]any{
		"workers":      workers,
		"total":        stats.WorkerCount,
		"idle":         stats.IdleWorkers,
		"busy":         stats.BusyWorkers,
		"total_tasks":  stats.TotalTasks,
		"failed_tasks": stats.FailedTasks,
	})

	return string(result), nil
}

// HandleHeartbeatTrigger manually triggers a heartbeat cycle.
// Tool: autonomy_heartbeat_trigger
func (td *ToolDefinitions) HandleHeartbeatTrigger(args map[string]any) (string, error) {
	event := td.kit.Heartbeat().Trigger(nil)

	result, _ := json.Marshal(map[string]any{
		"timestamp":    event.Timestamp.Format("2006-01-02 15:04:05"),
		"mode":         event.Mode,
		"tasks_pulled": event.TasksPulled,
		"actions":      event.Actions,
		"message":      fmt.Sprintf("Heartbeat triggered: %d tasks pulled, %d actions", event.TasksPulled, len(event.Actions)),
	})

	return string(result), nil
}

// HandleStatus returns the overall autonomy kit status.
// Tool: autonomy_status
func (td *ToolDefinitions) HandleStatus(args map[string]any) (string, error) {
	status := td.kit.Status()

	result, _ := json.Marshal(status)
	return string(result), nil
}

func taskReportTime(task *QueueTask) time.Time {
	if task == nil {
		return time.Time{}
	}
	if !task.CompletedAt.IsZero() {
		return task.CompletedAt
	}
	if !task.StartedAt.IsZero() {
		return task.StartedAt
	}
	return task.CreatedAt
}

func parseToolLimit(args map[string]any, fallback, max int) int {
	if fallback <= 0 {
		fallback = 5
	}
	if max < fallback {
		max = fallback
	}
	raw, ok := args["limit"]
	if !ok {
		return fallback
	}
	var n int
	switch v := raw.(type) {
	case int:
		n = v
	case int64:
		n = int(v)
	case float64:
		n = int(v)
	case json.Number:
		parsed, err := strconv.Atoi(v.String())
		if err == nil {
			n = parsed
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			n = parsed
		}
	}
	if n <= 0 {
		return fallback
	}
	if n > max {
		return max
	}
	return n
}

func truncateToolText(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "\n...（truncated）"
}
