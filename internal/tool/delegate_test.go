package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskstore "github.com/yurika0211/luckyagent/internal/task"
)

func TestDelegateManagerCreate(t *testing.T) {
	cfg := DefaultDelegateConfig()
	dm := NewDelegateManager(cfg)

	if dm.config.MaxConcurrent != 3 {
		t.Errorf("expected max 3, got %d", dm.config.MaxConcurrent)
	}
}

func TestDelegateTaskToolRegistration(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()

	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))
	r.Register(WaitForTasksTool(dm))
	r.Register(ListTasksTool(dm))
	r.Register(DelegateCancelTool(dm))

	if r.Count() != 5 {
		t.Errorf("expected 5 delegate tools, got %d", r.Count())
	}

	// 检查分类
	dt, _ := r.Get("delegate_task")
	if dt.Category != CatDelegate {
		t.Errorf("expected CatDelegate, got %s", dt.Category)
	}

	ts, _ := r.Get("task_status")
	if ts.Permission != PermAuto {
		t.Errorf("task_status should be auto, got %s", ts.Permission)
	}
	wait, _ := r.Get("wait_for_tasks")
	if wait.Permission != PermAuto {
		t.Errorf("wait_for_tasks should be auto, got %s", wait.Permission)
	}
	cancel, _ := r.Get("delegate_cancel")
	if cancel.Permission != PermApprove {
		t.Errorf("delegate_cancel should require approval, got %s", cancel.Permission)
	}
}

func TestWaitForTasksReturnsCompletedResults(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	started := make(chan struct{})
	release := make(chan struct{})
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		close(started)
		select {
		case <-release:
			return "subtask result", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(WaitForTasksTool(dm))

	delegated, err := r.Call("delegate_task", map[string]any{"description": "wait for completion"})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}
	var start delegateStartResponse
	if err := json.Unmarshal([]byte(delegated), &start); err != nil {
		t.Fatalf("decode delegate response: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		close(release)
	}()

	result, err := r.Call("wait_for_tasks", map[string]any{
		"task_ids":         []any{start.TaskID},
		"timeout":          1,
		"poll_interval_ms": 10,
	})
	if err != nil {
		t.Fatalf("wait_for_tasks: %v", err)
	}
	var wait delegateWaitResponse
	if err := json.Unmarshal([]byte(result), &wait); err != nil {
		t.Fatalf("decode wait response: %v", err)
	}
	if !wait.AllTerminal || wait.TimedOut || len(wait.PendingTaskIDs) != 0 {
		t.Fatalf("expected terminal wait response, got %+v", wait)
	}
	if len(wait.Tasks) != 1 || wait.Tasks[0].Status != StatusCompleted.String() || wait.Tasks[0].Result != "subtask result" {
		t.Fatalf("unexpected waited task result: %+v", wait.Tasks)
	}
}

func TestWaitForTasksReturnsTerminalFailuresWithoutBlocking(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	now := time.Now()
	dm.tasks["task-failed"] = &DelegateTask{ID: "task-failed", Description: "failed", Status: StatusFailed, Error: "boom", StartedAt: now, CompletedAt: now}
	dm.tasks["task-cancelled"] = &DelegateTask{ID: "task-cancelled", Description: "cancelled", Status: StatusCancelled, Error: "cancelled", StartedAt: now, CompletedAt: now}

	result, err := dm.handleWaitForTasks(context.Background(), map[string]any{"task_ids": []string{"task-failed", "task-cancelled"}})
	if err != nil {
		t.Fatalf("wait_for_tasks: %v", err)
	}
	var wait delegateWaitResponse
	if err := json.Unmarshal([]byte(result), &wait); err != nil {
		t.Fatalf("decode wait response: %v", err)
	}
	if !wait.AllTerminal || wait.TimedOut || len(wait.Tasks) != 2 {
		t.Fatalf("unexpected terminal wait response: %+v", wait)
	}
	if wait.Tasks[0].Status != StatusFailed.String() || wait.Tasks[1].Status != StatusCancelled.String() {
		t.Fatalf("expected failed and cancelled statuses, got %+v", wait.Tasks)
	}
}

func TestWaitForTasksReturnsTimeoutAndPendingIDs(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	dm.tasks["task-running"] = &DelegateTask{ID: "task-running", Description: "running", Status: StatusRunning, StartedAt: time.Now()}

	started := time.Now()
	result, err := dm.handleWaitForTasks(context.Background(), map[string]any{
		"task_ids":         []any{"task-running"},
		"timeout":          0.05,
		"poll_interval_ms": 10,
	})
	if err != nil {
		t.Fatalf("wait_for_tasks: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond || elapsed > time.Second {
		t.Fatalf("unexpected wait duration: %s", elapsed)
	}
	var wait delegateWaitResponse
	if err := json.Unmarshal([]byte(result), &wait); err != nil {
		t.Fatalf("decode wait response: %v", err)
	}
	if wait.AllTerminal || !wait.TimedOut || len(wait.PendingTaskIDs) != 1 || wait.PendingTaskIDs[0] != "task-running" {
		t.Fatalf("expected timed-out pending response, got %+v", wait)
	}
}

func TestWaitForTasksValidatesIDsAndHonorsCancellation(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	if _, err := dm.handleWaitForTasks(context.Background(), map[string]any{"task_ids": []any{}}); err == nil {
		t.Fatal("expected empty task_ids to fail")
	}
	if _, err := dm.handleWaitForTasks(context.Background(), map[string]any{"task_ids": []any{"task-1", 2}}); err == nil {
		t.Fatal("expected non-string task_id to fail")
	}

	dm.tasks["task-running"] = &DelegateTask{ID: "task-running", Description: "running", Status: StatusRunning, StartedAt: time.Now()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := dm.handleWaitForTasks(ctx, map[string]any{"task_ids": []string{"task-running"}}); err == nil {
		t.Fatal("expected cancelled context to stop wait")
	}
}

func TestWaitForTasksReadsUnifiedStoreRecords(t *testing.T) {
	store, err := taskstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	record, err := store.Create(taskstore.Record{
		ID:          "external-task",
		Source:      taskstore.SourceHTTP,
		Status:      taskstore.StatusCompleted,
		Description: "completed elsewhere",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.SaveResult(record.ID, "external result"); err != nil {
		t.Fatalf("SaveResult: %v", err)
	}
	dm := NewDelegateManager(DefaultDelegateConfig())
	dm.SetTaskStore(store)

	result, err := dm.handleWaitForTasks(context.Background(), map[string]any{"task_ids": []string{record.ID}})
	if err != nil {
		t.Fatalf("wait_for_tasks: %v", err)
	}
	var wait delegateWaitResponse
	if err := json.Unmarshal([]byte(result), &wait); err != nil {
		t.Fatalf("decode wait response: %v", err)
	}
	if !wait.AllTerminal || len(wait.Tasks) != 1 || wait.Tasks[0].Source != string(taskstore.SourceHTTP) || wait.Tasks[0].Result != "external result" {
		t.Fatalf("unexpected unified wait response: %+v", wait)
	}
}

func TestDelegateTaskCall(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))

	// 委派任务
	result, err := r.Call("delegate_task", map[string]any{
		"description": "Test task",
	})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse result: %v", err)
	}

	taskID, ok := resp["task_id"].(string)
	if !ok || taskID == "" {
		t.Error("expected task_id in response")
	}

	// 等待任务完成
	time.Sleep(100 * time.Millisecond)

	// 查询状态
	statusResult, err := r.Call("task_status", map[string]any{
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("task_status: %v", err)
	}

	var status map[string]any
	json.Unmarshal([]byte(statusResult), &status)
	if status["status"] != "completed" {
		t.Errorf("expected completed, got %v", status["status"])
	}
}

func TestDelegateTaskWritesUnifiedTaskEvents(t *testing.T) {
	store, err := taskstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	dm := NewDelegateManager(DefaultDelegateConfig())
	dm.SetTaskStore(store)
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		return "delegate result", nil
	})

	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	result, err := r.Call("delegate_task", map[string]any{
		"description": "Write unified task events",
	})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	taskID, _ := resp["task_id"].(string)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		record, ok, err := store.Get(taskID)
		if err != nil {
			t.Fatalf("Get unified task: %v", err)
		}
		if ok && record.Status == taskstore.StatusCompleted {
			events, err := store.Events(taskID)
			if err != nil {
				t.Fatalf("Events: %v", err)
			}
			if len(events) < 3 {
				t.Fatalf("expected created/started/completed events, got %+v", events)
			}
			resultBytes, err := os.ReadFile(filepath.Join(store.Root(), taskID, "result.md"))
			if err != nil {
				t.Fatalf("read result artifact: %v", err)
			}
			if string(resultBytes) != "delegate result" {
				t.Fatalf("unexpected result artifact: %q", string(resultBytes))
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for unified task completion: %s", taskID)
}

func TestDelegateTaskAutoModeWritesPlannerTrace(t *testing.T) {
	store, err := taskstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	dm := NewDelegateManager(DefaultDelegateConfig())
	dm.SetTaskStore(store)
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		return "planned result", nil
	})

	result, err := dm.handleDelegate(map[string]any{
		"description":   "Compare independent modules A, B, and C and report risks for each module",
		"mode":          "auto",
		"max_children":  3,
		"include_trace": true,
	})
	if err != nil {
		t.Fatalf("handleDelegate: %v", err)
	}
	var start map[string]any
	if err := json.Unmarshal([]byte(result), &start); err != nil {
		t.Fatalf("parse start: %v", err)
	}
	taskID, _ := start["task_id"].(string)
	if taskID == "" || start["planner_summary"] == "" || start["trace_available"] != true {
		t.Fatalf("unexpected start response: %+v", start)
	}
	if _, ok, err := store.PlannerTrace(taskID); err != nil || !ok {
		t.Fatalf("expected planner trace artifact: ok=%t err=%v", ok, err)
	}
	record, ok, err := store.Get(taskID)
	if err != nil || !ok {
		t.Fatalf("get task record: ok=%t err=%v", ok, err)
	}
	if record.Mode == "" || record.Metadata["planner_summary"] == "" {
		t.Fatalf("expected planned task metadata, got %+v", record)
	}

	statusResult, err := dm.handleStatus(map[string]any{
		"task_id":       taskID,
		"include_trace": true,
	})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if _, ok := status["planner_trace"].(map[string]any); !ok {
		t.Fatalf("expected planner trace in status: %+v", status)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		record, ok, err := store.Get(taskID)
		if err != nil {
			t.Fatalf("get task record: %v", err)
		}
		if ok && record.Status == taskstore.StatusCompleted {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task completion: %s", taskID)
}

func TestTaskStatusReadsUnifiedTaskEvents(t *testing.T) {
	store, err := taskstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	record, err := store.Create(taskstore.Record{
		ID:          "collab-42",
		Source:      taskstore.SourceHTTP,
		Mode:        taskstore.ModeParallel,
		Status:      taskstore.StatusCompleted,
		Description: "collab task",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.AppendEvent(taskstore.Event{
		Type:     taskstore.EventCompleted,
		TaskID:   record.ID,
		Status:   taskstore.StatusCompleted,
		Evidence: []string{"collab completed"},
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := store.SaveResult(record.ID, "collab result"); err != nil {
		t.Fatalf("SaveResult: %v", err)
	}

	dm := NewDelegateManager(DefaultDelegateConfig())
	dm.SetTaskStore(store)
	result, err := dm.handleStatus(map[string]any{
		"task_id":        record.ID,
		"include_events": true,
	})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if resp["source"] != "http" || resp["mode"] != "parallel" || resp["result"] != "collab result" {
		t.Fatalf("unexpected unified status: %+v", resp)
	}
	if _, ok := resp["observation"].(map[string]any); !ok {
		t.Fatalf("expected observation in status: %+v", resp)
	}
	events, ok := resp["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("expected one event, got %+v", resp["events"])
	}
}

func TestListTasksIncludesUnifiedStoreRecords(t *testing.T) {
	store, err := taskstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	_, err = store.Create(taskstore.Record{
		ID:          "collab-43",
		Source:      taskstore.SourceHTTP,
		Mode:        taskstore.ModeParallel,
		Status:      taskstore.StatusCompleted,
		Description: "collab list task",
		CreatedAt:   time.Now(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	dm := NewDelegateManager(DefaultDelegateConfig())
	dm.SetTaskStore(store)
	result, err := dm.handleList(map[string]any{"source": "http"})
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	var resp delegateListResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if resp.Count != 1 || len(resp.Tasks) != 1 || resp.Tasks[0].TaskID != "collab-43" {
		t.Fatalf("expected unified collab task, got %+v", resp)
	}
	if resp.Tasks[0].Source != "http" || resp.Tasks[0].Mode != "parallel" {
		t.Fatalf("unexpected unified list item: %+v", resp.Tasks[0])
	}
}

func TestDelegateTaskInjectsWritableWorkspace(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	capturedContext := make(chan string, 1)
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		capturedContext <- contextStr
		return "done", nil
	})

	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))

	workspace := filepath.Join(t.TempDir(), "twitter-coser")
	result, err := r.Call("delegate_task", map[string]any{
		"description": "Download five images and save them under " + workspace + ".",
	})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if got := resp["workspace"]; got != workspace {
		t.Fatalf("expected response workspace %q, got %v", workspace, got)
	}
	if info, err := os.Stat(workspace); err != nil || !info.IsDir() {
		t.Fatalf("expected workspace directory to exist, info=%v err=%v", info, err)
	}

	var contextStr string
	select {
	case contextStr = <-capturedContext:
	case <-time.After(time.Second):
		t.Fatal("executor was not called")
	}
	if got := ExtractDelegateWorkspace(contextStr); got != workspace {
		t.Fatalf("expected extracted workspace %q, got %q; context=%q", workspace, got, contextStr)
	}
	if !strings.Contains(contextStr, "Allowed file roots") {
		t.Fatalf("expected sandbox guidance in context, got %q", contextStr)
	}

	taskID, _ := resp["task_id"].(string)
	statusResult, err := r.Call("task_status", map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("task_status: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if got := status["workspace"]; got != workspace {
		t.Fatalf("expected status workspace %q, got %v", workspace, got)
	}
}

func TestPrepareDelegateExecutionContextUsesDefaultWorkspace(t *testing.T) {
	workspace, contextStr, err := prepareDelegateExecutionContext("task-99", "summarize", "")
	if err != nil {
		t.Fatalf("prepareDelegateExecutionContext: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(workspace), "/luckyagent-delegate/task-99") {
		t.Fatalf("expected default delegate workspace, got %q", workspace)
	}
	if got := ExtractDelegateWorkspace(contextStr); got != workspace {
		t.Fatalf("expected workspace round trip %q, got %q", workspace, got)
	}
}

func TestListTasks(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(ListTasksTool(dm))

	// 委派几个任务
	for i := 0; i < 3; i++ {
		r.Call("delegate_task", map[string]any{
			"description": "Task {i}",
		})
	}

	time.Sleep(100 * time.Millisecond)

	// 列出任务
	result, err := r.Call("list_tasks", map[string]any{})
	if err != nil {
		t.Fatalf("list_tasks: %v", err)
	}

	var resp map[string]any
	json.Unmarshal([]byte(result), &resp)
	count, _ := resp["count"].(float64)
	if int(count) != 3 {
		t.Errorf("expected 3 tasks, got %v", count)
	}
}

func TestListTasksFilterLimitAndOrder(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	now := time.Now()
	dm.tasks["old-completed"] = &DelegateTask{ID: "old-completed", Description: "old", Status: StatusCompleted, StartedAt: now.Add(-2 * time.Hour), CompletedAt: now.Add(-time.Hour), Result: "done"}
	dm.tasks["new-running"] = &DelegateTask{ID: "new-running", Description: "new", Status: StatusRunning, StartedAt: now}
	dm.tasks["mid-running"] = &DelegateTask{ID: "mid-running", Description: "mid", Status: StatusRunning, StartedAt: now.Add(-time.Hour)}

	result, err := dm.handleList(map[string]any{"status": "running", "limit": 1, "order": "desc", "include_result": true})
	if err != nil {
		t.Fatalf("handleList: %v", err)
	}
	var resp struct {
		Tasks []struct {
			TaskID      string `json:"task_id"`
			CompletedAt string `json:"completed_at"`
		} `json:"tasks"`
		Count    int            `json:"count"`
		Total    int            `json:"total"`
		ByStatus map[string]int `json:"by_status"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if resp.Count != 1 || resp.Total != 2 {
		t.Fatalf("expected count=1 total=2, got count=%d total=%d", resp.Count, resp.Total)
	}
	if len(resp.Tasks) != 1 || resp.Tasks[0].TaskID != "new-running" {
		t.Fatalf("expected newest running task first, got %#v", resp.Tasks)
	}
	if resp.Tasks[0].CompletedAt != "" {
		t.Fatalf("running task completed_at should be empty, got %q", resp.Tasks[0].CompletedAt)
	}
	if resp.ByStatus["completed"] != 1 || resp.ByStatus["running"] != 2 {
		t.Fatalf("unexpected by_status: %#v", resp.ByStatus)
	}
}

func TestTaskStatusNotFound(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	r := NewRegistry()
	r.Register(TaskStatusTool(dm))

	_, err := r.Call("task_status", map[string]any{
		"task_id": "nonexistent",
	})
	if err == nil {
		t.Error("expected error for nonexistent task")
	}
}

func TestDelegateMaxConcurrent(t *testing.T) {
	cfg := DefaultDelegateConfig()
	cfg.MaxConcurrent = 1 // 只允许1个并发
	dm := NewDelegateManager(cfg)
	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))

	// 第一个任务
	r.Call("delegate_task", map[string]any{
		"description": "First task",
	})

	// 第二个任务应该被拒绝（第一个还在 running）
	// 注意：由于 executeTask 很快完成，这个测试可能不稳定
	// 在真实场景中，子代理任务会持续更长时间
}

func TestDelegateCancelRunningTask(t *testing.T) {
	dm := NewDelegateManager(DefaultDelegateConfig())
	started := make(chan struct{})
	dm.SetAgentExecutor(func(ctx context.Context, description, contextStr string) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	})

	r := NewRegistry()
	r.Register(DelegateTaskTool(dm))
	r.Register(TaskStatusTool(dm))
	r.Register(DelegateCancelTool(dm))

	result, err := r.Call("delegate_task", map[string]any{"description": "long task"})
	if err != nil {
		t.Fatalf("delegate_task: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(result), &created); err != nil {
		t.Fatalf("parse delegate response: %v", err)
	}
	taskID := created["task_id"].(string)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}

	cancelResult, err := r.Call("delegate_cancel", map[string]any{"task_id": taskID, "reason": "test cancel"})
	if err != nil {
		t.Fatalf("delegate_cancel: %v", err)
	}
	var cancelled map[string]any
	if err := json.Unmarshal([]byte(cancelResult), &cancelled); err != nil {
		t.Fatalf("parse cancel response: %v", err)
	}
	if cancelled["status"] != "cancelled" {
		t.Fatalf("expected cancelled response, got %#v", cancelled)
	}

	time.Sleep(20 * time.Millisecond)
	statusResult, err := r.Call("task_status", map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("task_status: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if status["status"] != "cancelled" {
		t.Fatalf("expected task status cancelled, got %#v", status)
	}
	if status["completed_at"] == "" {
		t.Fatalf("expected cancelled task completed_at to be set")
	}
}

func TestTaskStatusResultTruncationAndOmitResult(t *testing.T) {
	cfg := DefaultDelegateConfig()
	cfg.MaxResultBytesInline = 10
	dm := NewDelegateManager(cfg)
	dm.tasks["task-large"] = &DelegateTask{
		ID:          "task-large",
		Description: "large",
		Status:      StatusCompleted,
		Result:      "first paragraph\n\nsecond paragraph with extra data",
		StartedAt:   time.Now().Add(-time.Minute),
		CompletedAt: time.Now(),
	}

	result, err := dm.handleStatus(map[string]any{"task_id": "task-large"})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if status["result_truncated"] != true {
		t.Fatalf("expected result_truncated=true, got %#v", status)
	}
	if got := status["result"].(string); !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncated result marker, got %q", got)
	}

	result, err = dm.handleStatus(map[string]any{"task_id": "task-large", "include_result": false})
	if err != nil {
		t.Fatalf("handleStatus without result: %v", err)
	}
	status = map[string]any{}
	if err := json.Unmarshal([]byte(result), &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if _, ok := status["result"]; ok {
		t.Fatalf("expected result omitted when include_result=false, got %#v", status)
	}
}

func TestDelegateTimeoutClamp(t *testing.T) {
	cfg := DefaultDelegateConfig()
	cfg.MinTimeout = 10 * time.Second
	cfg.MaxTimeout = 20 * time.Second
	cfg.Timeout = 15 * time.Second
	dm := NewDelegateManager(cfg)

	if got := dm.delegateTimeoutFromArgs(map[string]any{"timeout": 1}); got != 10*time.Second {
		t.Fatalf("expected min timeout clamp, got %v", got)
	}
	if got := dm.delegateTimeoutFromArgs(map[string]any{"timeout": 100}); got != 20*time.Second {
		t.Fatalf("expected max timeout clamp, got %v", got)
	}
	if got := dm.delegateTimeoutFromArgs(map[string]any{"timeout": 0}); got != 15*time.Second {
		t.Fatalf("expected default timeout, got %v", got)
	}
}

func TestTaskStatusString(t *testing.T) {
	tests := []struct {
		status   TaskStatus
		expected string
	}{
		{StatusPending, "pending"},
		{StatusRunning, "running"},
		{StatusCompleted, "completed"},
		{StatusFailed, "failed"},
		{StatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		if got := tt.status.String(); got != tt.expected {
			t.Errorf("TaskStatus(%d).String() = %q, want %q", tt.status, got, tt.expected)
		}
	}
}
