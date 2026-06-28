package tool

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/autonomy"
)

func TestAutonomyToolServiceRegistersUnifiedVisibleTool(t *testing.T) {
	kit := autonomy.NewAutonomyKit(autonomy.DefaultAutonomyConfig(), nil)
	svc := NewAutonomyToolService(kit)
	reg := NewRegistry()

	svc.RegisterTools(reg)

	unified, ok := reg.Get("autonomy")
	if !ok {
		t.Fatal("expected unified autonomy tool to be registered")
	}
	if unified.HiddenFromModel {
		t.Fatal("unified autonomy tool should be visible to the model")
	}
	if unified.Permission != PermApprove {
		t.Fatalf("expected autonomy permission approve, got %s", unified.Permission)
	}

	lowLevel, ok := reg.Get("autonomy_status")
	if !ok {
		t.Fatal("expected low-level autonomy_status tool to be registered")
	}
	if !lowLevel.HiddenFromModel {
		t.Fatal("low-level autonomy tools should remain hidden from the model")
	}

	var visibleNames []string
	for _, t := range reg.ListModelVisible() {
		visibleNames = append(visibleNames, t.Name)
	}
	if !stringSliceContains(visibleNames, "autonomy") {
		t.Fatalf("expected autonomy in model-visible tools, got %v", visibleNames)
	}
	if stringSliceContains(visibleNames, "autonomy_status") {
		t.Fatalf("did not expect autonomy_status in model-visible tools, got %v", visibleNames)
	}
}

func TestAutonomyToolServiceUnifiedAddInvokesStarterAndQueuesTask(t *testing.T) {
	kit := autonomy.NewAutonomyKit(autonomy.DefaultAutonomyConfig(), nil)
	starts := 0
	svc := NewAutonomyToolService(kit, func() error {
		starts++
		return nil
	})

	out, err := svc.HandleAutonomy(map[string]any{
		"action":      "add",
		"title":       "review memory routing",
		"description": "Check whether memory routing is using the markdown vault.",
		"priority":    "high",
	})
	if err != nil {
		t.Fatalf("HandleAutonomy(add): %v", err)
	}
	if starts != 1 {
		t.Fatalf("expected starter to be invoked once, got %d", starts)
	}
	if !strings.Contains(out, "task_id") {
		t.Fatalf("expected queued task response, got %q", out)
	}

	tasks := kit.Queue().ListAll()
	if len(tasks) != 1 {
		t.Fatalf("expected one queued task, got %d", len(tasks))
	}
	if tasks[0].Title != "review memory routing" {
		t.Fatalf("unexpected task title: %q", tasks[0].Title)
	}
}

func TestAutonomyToolServiceWorkerScaling(t *testing.T) {
	kit := autonomy.NewAutonomyKit(autonomy.DefaultAutonomyConfig(), nil)
	starts := 0
	svc := NewAutonomyToolService(kit, func() error {
		starts++
		return nil
	})

	out, err := svc.HandleAutonomy(map[string]any{
		"action": "set_workers",
		"count":  5,
	})
	if err != nil {
		t.Fatalf("HandleAutonomy(set_workers): %v", err)
	}
	if starts != 1 {
		t.Fatalf("expected starter once, got %d", starts)
	}
	if !strings.Contains(out, `"worker_count":5`) {
		t.Fatalf("expected worker count response, got %q", out)
	}

	out, err = svc.HandleAutonomy(map[string]any{
		"action": "scale_down",
		"count":  2,
	})
	if err != nil {
		t.Fatalf("HandleAutonomy(scale_down): %v", err)
	}
	if !strings.Contains(out, `"removed":2`) {
		t.Fatalf("expected removed response, got %q", out)
	}
	if got := kit.Status().PoolStats.WorkerCount; got != 3 {
		t.Fatalf("expected 3 workers after scale down, got %d", got)
	}
}

func TestAutonomyToolServiceReportIncludesWorkerOutput(t *testing.T) {
	kit := autonomy.NewAutonomyKit(autonomy.DefaultAutonomyConfig(), nil)
	svc := NewAutonomyToolService(kit)

	task := kit.AddTask("background digest", "Summarize worker output", autonomy.PriorityNormal, []string{"report"})
	if err := kit.Queue().Complete(task.ID, "worker finished the digest"); err != nil {
		t.Fatalf("complete task: %v", err)
	}

	out, err := svc.HandleAutonomy(map[string]any{
		"action": "report",
		"state":  "done",
		"limit":  1,
	})
	if err != nil {
		t.Fatalf("HandleAutonomy(report): %v", err)
	}
	if !strings.Contains(out, "worker finished the digest") {
		t.Fatalf("expected worker result in report, got %q", out)
	}
}

func stringSliceContains(items []string, needle string) bool {
	for _, item := range items {
		if item == needle {
			return true
		}
	}
	return false
}
