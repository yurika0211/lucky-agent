package agent

import (
	"fmt"
	"strings"

	taskstore "github.com/yurika0211/luckyagent/internal/task"
)

func (a *Agent) appendRunningTaskNotice(response string) string {
	if a == nil || a.taskStore == nil {
		return response
	}
	tasks := a.runningUnifiedTasks(4)
	if len(tasks) == 0 {
		return response
	}
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	note := fmt.Sprintf("Pending multi-agent work remains: %s. Use task_status or list_tasks to wait, inspect, cancel, or finalize from a partial result.", strings.Join(ids, ", "))
	response = strings.TrimSpace(response)
	if response == "" {
		return note
	}
	if strings.Contains(response, note) {
		return response
	}
	return response + "\n\n" + note
}

func (a *Agent) runningUnifiedTasks(limit int) []taskstore.Record {
	if a == nil || a.taskStore == nil {
		return nil
	}
	if limit <= 0 {
		limit = 4
	}
	var out []taskstore.Record
	for _, status := range []taskstore.Status{taskstore.StatusRunning, taskstore.StatusPending} {
		records, err := a.taskStore.List(taskstore.ListFilter{Status: status, Limit: limit})
		if err != nil {
			continue
		}
		out = append(out, records...)
		if len(out) >= limit {
			return out[:limit]
		}
	}
	return out
}
