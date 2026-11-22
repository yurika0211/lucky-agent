package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

// TaskRelationPlan is the small host/application contract used when a second
// task arrives in a session that already has work running. A false Parallel
// value is conservative: the new task waits until its dependency is settled.
type TaskRelationPlan struct {
	Parallel  bool     `json:"parallel"`
	DependsOn []string `json:"depends_on,omitempty"`
	Reason    string   `json:"reason"`
	Source    string   `json:"source"`
}

// PlanTaskRelation asks the current main model whether a new session task can
// run independently. It is deliberately a no-tools JSON call, so the planner
// cannot mutate state or bypass the session runner's existing limits.
func (a *Agent) PlanTaskRelation(ctx context.Context, activeTasks []string, nextTask string) (TaskRelationPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	nextTask = strings.TrimSpace(nextTask)
	if nextTask == "" || len(activeTasks) == 0 {
		return TaskRelationPlan{Parallel: true, Reason: "no dependency", Source: "local"}, nil
	}
	if a == nil || a.provider == nil {
		return TaskRelationPlan{Parallel: false, Reason: "planner unavailable; wait conservatively", Source: "fallback"}, fmt.Errorf("task planner provider unavailable")
	}
	for i := range activeTasks {
		activeTasks[i] = strings.TrimSpace(activeTasks[i])
	}
	prompt := "Decide whether a new task can run concurrently with active tasks in the same session. Return JSON only: {\"parallel\":true|false,\"depends_on\":[string],\"reason\":string}. Set parallel=false whenever the new task reads, modifies, tests, or relies on an active task's intermediate result, or when uncertain. Set true only when the tasks are independent and do not conflict.\nActive tasks:\n" + strings.Join(activeTasks, "\n- ") + "\nNew task:\n" + nextTask
	response, err := a.provider.Chat(ctx, []provider.Message{
		{Role: "system", Content: "You are the session task dependency planner. Do not execute tools or solve the tasks."},
		{Role: "user", Content: prompt},
	})
	if err != nil || response == nil {
		if err == nil {
			err = fmt.Errorf("task planner returned no response")
		}
		return TaskRelationPlan{Parallel: false, Reason: "planner unavailable; wait conservatively", Source: "fallback"}, err
	}
	var plan TaskRelationPlan
	if err := json.Unmarshal([]byte(response.Content), &plan); err != nil {
		return TaskRelationPlan{Parallel: false, Reason: "planner returned invalid JSON; wait conservatively", Source: "fallback"}, err
	}
	plan.Source = "model"
	if strings.TrimSpace(plan.Reason) == "" {
		plan.Reason = "model dependency decision"
	}
	return plan, nil
}
