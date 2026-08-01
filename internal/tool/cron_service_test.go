package tool

import (
	"encoding/json"
	"strings"
	"testing"

	cronpkg "github.com/yurika0211/luckyagent/internal/cron"
)

func TestCronToolServiceRegistersValidate(t *testing.T) {
	svc := NewCronToolService(cronpkg.NewEngine(), nil, testCronTaskFactory)
	reg := NewRegistry()

	svc.RegisterTools(reg)

	validate, ok := reg.Get("cron_validate")
	if !ok {
		t.Fatal("expected cron_validate to be registered")
	}
	if validate.Permission != PermAuto {
		t.Fatalf("expected cron_validate auto permission, got %s", validate.Permission)
	}
}

func TestCronValidatePreviewsSchedule(t *testing.T) {
	svc := NewCronToolService(cronpkg.NewEngine(), nil, testCronTaskFactory)

	out, err := svc.HandleValidate(map[string]any{
		"schedule":  "每天9点",
		"mode":      "shell",
		"command":   "echo hi",
		"next_runs": 2,
	})
	if err != nil {
		t.Fatalf("HandleValidate: %v", err)
	}
	payload := decodeCronToolJSON(t, out)
	if payload["ok"] != true || payload["action"] != "validate" {
		t.Fatalf("unexpected validate payload: %v", payload)
	}
	if payload["parsed_by"] != "natural_language" {
		t.Fatalf("expected natural_language parser, got %v", payload["parsed_by"])
	}
	runs, ok := payload["next_runs"].([]any)
	if !ok || len(runs) != 2 {
		t.Fatalf("expected two next runs, got %v", payload["next_runs"])
	}
}

func TestCronAddRejectsInvalidMode(t *testing.T) {
	svc := NewCronToolService(cronpkg.NewEngine(), nil, testCronTaskFactory)

	_, err := svc.HandleAdd(map[string]any{
		"schedule": "每天9点",
		"mode":     "agetn",
		"command":  "summarize inbox",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid cron mode") {
		t.Fatalf("expected invalid mode error, got %v", err)
	}
}

func TestCronAddDryRunDoesNotMutate(t *testing.T) {
	engine := cronpkg.NewEngine()
	saves := 0
	builds := 0
	svc := NewCronToolService(engine, func() error {
		saves++
		return nil
	}, func(id, mode, command string, metadata map[string]string) func() error {
		builds++
		return func() error { return nil }
	})

	out, err := svc.HandleAdd(map[string]any{
		"id":       "dry-run-job",
		"schedule": "0 9 * * *",
		"mode":     "shell",
		"command":  "rm -rf /tmp/demo",
		"dry_run":  true,
	})
	if err != nil {
		t.Fatalf("HandleAdd dry_run: %v", err)
	}
	payload := decodeCronToolJSON(t, out)
	if payload["dry_run"] != true {
		t.Fatalf("expected dry_run response, got %v", payload)
	}
	if payload["would_start_engine"] != true {
		t.Fatalf("expected would_start_engine=true, got %v", payload["would_start_engine"])
	}
	if engine.JobCount() != 0 || engine.IsRunning() {
		t.Fatalf("dry run mutated engine: jobs=%d running=%v", engine.JobCount(), engine.IsRunning())
	}
	if saves != 0 || builds != 0 {
		t.Fatalf("dry run should not save or build task, saves=%d builds=%d", saves, builds)
	}
	if warnings, ok := payload["warnings"].([]any); !ok || len(warnings) == 0 {
		t.Fatalf("expected shell risk warning, got %v", payload["warnings"])
	}
}

func TestCronAddCanAvoidStartingEngine(t *testing.T) {
	engine := cronpkg.NewEngine()
	saves := 0
	svc := NewCronToolService(engine, func() error {
		saves++
		return nil
	}, testCronTaskFactory)

	out, err := svc.HandleAdd(map[string]any{
		"id":           "manual-start-job",
		"schedule":     "0 9 * * *",
		"mode":         "agent",
		"command":      "summarize inbox",
		"start_engine": false,
	})
	if err != nil {
		t.Fatalf("HandleAdd start_engine=false: %v", err)
	}
	payload := decodeCronToolJSON(t, out)
	if payload["engine_started_by_tool"] != false {
		t.Fatalf("expected engine_started_by_tool=false, got %v", payload["engine_started_by_tool"])
	}
	if engine.JobCount() != 1 {
		t.Fatalf("expected job to be added, got %d", engine.JobCount())
	}
	if engine.IsRunning() {
		t.Fatal("expected engine to remain stopped")
	}
	if saves != 1 {
		t.Fatalf("expected save once, got %d", saves)
	}
}

func testCronTaskFactory(id, mode, command string, metadata map[string]string) func() error {
	return func() error { return nil }
}

func decodeCronToolJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode JSON %q: %v", out, err)
	}
	return payload
}
