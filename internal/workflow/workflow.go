// Package workflow provides DAG-based workflow orchestration for LuckyAgent.
// It supports task dependencies, parallel execution, and state management.
package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskStatus represents the current state of a task.
type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusSkipped   TaskStatus = "skipped"
)

// Task represents a single unit of work in a workflow.
type Task struct {
	ID          string                 `json:"id" yaml:"id"`
	Name        string                 `json:"name" yaml:"name"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Action      string                 `json:"action" yaml:"action"` // Action type: "http", "script", "tool", "agent"
	Params      map[string]interface{} `json:"params,omitempty" yaml:"params,omitempty"`
	DependsOn   []string               `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	Timeout     time.Duration          `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	RetryCount  int                    `json:"retryCount,omitempty" yaml:"retryCount,omitempty"`
	RetryDelay  time.Duration          `json:"retryDelay,omitempty" yaml:"retryDelay,omitempty"`
}

// TaskResult contains the result of a task execution.
type TaskResult struct {
	TaskID    string        `json:"taskId"`
	Status    TaskStatus    `json:"status"`
	Output    interface{}   `json:"output,omitempty"`
	Error     string        `json:"error,omitempty"`
	StartTime time.Time     `json:"startTime"`
	EndTime   time.Time     `json:"endTime"`
	Duration  time.Duration `json:"duration"`
}

// Workflow represents a DAG-based workflow definition.
type Workflow struct {
	ID          string    `json:"id" yaml:"id"`
	Name        string    `json:"name" yaml:"name"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
	Tasks       []*Task   `json:"tasks" yaml:"tasks"`
	Version     string    `json:"version,omitempty" yaml:"version,omitempty"`
	CreatedAt   time.Time `json:"createdAt" yaml:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt" yaml:"updatedAt"`
}

// WorkflowInstance represents a running instance of a workflow.
type WorkflowInstance struct {
	ID         string                 `json:"id"`
	WorkflowID string                 `json:"workflowId"`
	Status     TaskStatus             `json:"status"`
	Results    map[string]*TaskResult `json:"results"`
	StartTime  time.Time              `json:"startTime"`
	EndTime    time.Time              `json:"endTime,omitempty"`
	Context    context.Context        `json:"-"`
	CancelFunc context.CancelFunc     `json:"-"`
	readyCh    chan<- struct{}        `json:"-"`
	mu         sync.RWMutex
}

// NewWorkflow creates a new workflow with the given tasks.
func NewWorkflow(name string, tasks []*Task) *Workflow {
	now := time.Now()
	return &Workflow{
		ID:        uuid.New().String(),
		Name:      name,
		Tasks:     tasks,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewWorkflowInstance creates a new instance of a workflow.
func NewWorkflowInstance(workflowID string) *WorkflowInstance {
	ctx, cancel := context.WithCancel(context.Background())
	return &WorkflowInstance{
		ID:         uuid.New().String(),
		WorkflowID: workflowID,
		Status:     StatusPending,
		Results:    make(map[string]*TaskResult),
		Context:    ctx,
		CancelFunc: cancel,
	}
}

// Validate checks if the workflow is valid.
func (w *Workflow) Validate() error {
	if w.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if len(w.Tasks) == 0 {
		return fmt.Errorf("workflow must have at least one task")
	}

	// Check for duplicate task IDs
	taskIDs := make(map[string]bool)
	for _, task := range w.Tasks {
		if task.ID == "" {
			return fmt.Errorf("task ID is required")
		}
		if taskIDs[task.ID] {
			return fmt.Errorf("duplicate task ID: %s", task.ID)
		}
		taskIDs[task.ID] = true

		if task.Name == "" {
			return fmt.Errorf("task %s: name is required", task.ID)
		}
		if task.Action == "" {
			return fmt.Errorf("task %s: action is required", task.ID)
		}
	}

	// Check for circular dependencies and invalid references
	for _, task := range w.Tasks {
		for _, dep := range task.DependsOn {
			if !taskIDs[dep] {
				return fmt.Errorf("task %s: depends on non-existent task %s", task.ID, dep)
			}
			if dep == task.ID {
				return fmt.Errorf("task %s: cannot depend on itself", task.ID)
			}
		}
	}

	// Check for cycles
	if err := w.detectCycle(); err != nil {
		return err
	}

	return nil
}

// detectCycle uses DFS to detect cycles in the DAG.
func (w *Workflow) detectCycle() error {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	taskMap := make(map[string]*Task)
	for _, task := range w.Tasks {
		taskMap[task.ID] = task
	}

	var dfs func(taskID string) error
	dfs = func(taskID string) error {
		visited[taskID] = true
		recStack[taskID] = true

		task := taskMap[taskID]
		for _, dep := range task.DependsOn {
			if !visited[dep] {
				if err := dfs(dep); err != nil {
					return err
				}
			} else if recStack[dep] {
				return fmt.Errorf("cycle detected: task %s -> %s", taskID, dep)
			}
		}

		recStack[taskID] = false
		return nil
	}

	for _, task := range w.Tasks {
		if !visited[task.ID] {
			if err := dfs(task.ID); err != nil {
				return err
			}
		}
	}

	return nil
}

// GetExecutionOrder returns tasks in topological order.
func (w *Workflow) GetExecutionOrder() ([]string, error) {
	taskMap := make(map[string]*Task)
	inDegree := make(map[string]int)

	for _, task := range w.Tasks {
		taskMap[task.ID] = task
		inDegree[task.ID] = 0
	}

	// Calculate in-degrees
	for _, task := range w.Tasks {
		for range task.DependsOn {
			inDegree[task.ID]++
		}
	}

	// Kahn's algorithm
	queue := make([]string, 0)
	for taskID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, taskID)
		}
	}

	result := make([]string, 0, len(w.Tasks))
	for len(queue) > 0 {
		taskID := queue[0]
		queue = queue[1:]
		result = append(result, taskID)

		// Find tasks that depend on this task
		for _, task := range w.Tasks {
			for _, dep := range task.DependsOn {
				if dep == taskID {
					inDegree[task.ID]--
					if inDegree[task.ID] == 0 {
						queue = append(queue, task.ID)
					}
				}
			}
		}
	}

	if len(result) != len(w.Tasks) {
		return nil, fmt.Errorf("cycle detected in workflow")
	}

	return result, nil
}

// GetReadyTasks returns tasks that are ready to execute (all dependencies completed).
func (w *Workflow) GetReadyTasks(completed map[string]bool) []*Task {
	ready := make([]*Task, 0)
	for _, task := range w.Tasks {
		if completed[task.ID] {
			continue
		}

		allDepsCompleted := true
		for _, dep := range task.DependsOn {
			if !completed[dep] {
				allDepsCompleted = false
				break
			}
		}

		if allDepsCompleted {
			ready = append(ready, task)
		}
	}
	return ready
}

// ToJSON serializes the workflow to JSON.
func (w *Workflow) ToJSON() ([]byte, error) {
	return json.MarshalIndent(w, "", "  ")
}

// FromJSON deserializes a workflow from JSON.
func FromJSON(data []byte) (*Workflow, error) {
	var workflow Workflow
	if err := json.Unmarshal(data, &workflow); err != nil {
		return nil, fmt.Errorf("failed to parse workflow: %w", err)
	}
	return &workflow, nil
}

// SetStatus updates the instance status safely.
func (i *WorkflowInstance) SetStatus(status TaskStatus) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Status = status
}

// GetStatus returns the current status safely.
func (i *WorkflowInstance) GetStatus() TaskStatus {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.Status
}

// SetResult stores a task result safely.
func (i *WorkflowInstance) SetResult(taskID string, result *TaskResult) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.Results[taskID] = result
}

// GetResult retrieves a task result safely.
func (i *WorkflowInstance) GetResult(taskID string) (*TaskResult, bool) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	result, ok := i.Results[taskID]
	return result, ok
}

// Snapshot returns a copy of the instance for safe reading.
func (i *WorkflowInstance) Snapshot() *WorkflowInstance {
	i.mu.RLock()
	defer i.mu.RUnlock()

	results := make(map[string]*TaskResult, len(i.Results))
	for k, v := range i.Results {
		resultCopy := *v
		results[k] = &resultCopy
	}

	return &WorkflowInstance{
		ID:         i.ID,
		WorkflowID: i.WorkflowID,
		Status:     i.Status,
		Results:    results,
		StartTime:  i.StartTime,
		EndTime:    i.EndTime,
	}
}

// SetStartTime sets the start time safely.
func (i *WorkflowInstance) SetStartTime(t time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.StartTime = t
}

// SetEndTime sets the end time safely.
func (i *WorkflowInstance) SetEndTime(t time.Time) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.EndTime = t
}

// Cancel cancels the workflow instance.
func (i *WorkflowInstance) Cancel() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.CancelFunc != nil {
		i.CancelFunc()
	}
	i.Status = StatusFailed
}
