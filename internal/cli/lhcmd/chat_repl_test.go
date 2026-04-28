package lhcmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/cron"
)

func TestParseCronAddSpecSupportsFiveFieldCron(t *testing.T) {
	spec, err := parseCronAddSpec([]string{"add", "job1", "0", "9", "*", "*", "*", "echo", "hello"})
	if err != nil {
		t.Fatalf("parseCronAddSpec() error = %v", err)
	}
	if spec.ID != "job1" {
		t.Fatalf("expected id job1, got %q", spec.ID)
	}
	if spec.Mode != cronTaskShell {
		t.Fatalf("expected shell mode, got %s", spec.Mode)
	}
	if spec.Payload != "echo hello" {
		t.Fatalf("expected payload 'echo hello', got %q", spec.Payload)
	}
	if spec.Schedule == nil {
		t.Fatal("expected non-nil schedule")
	}
}

func TestHandleCronCommandAddsExecutableShellJob(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatalf("NewManagerWithDir() error = %v", err)
	}
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}
	defer a.Close()

	engine := cron.NewEngine()
	store := cron.NewStore(filepath.Join(tmpDir, "cron_jobs.json"))
	handled := handleCronCommand("add shell-job 每小时 echo hello-cron", engine, store, a, agent.DefaultLoopConfig())
	if !handled {
		t.Fatal("expected command to be handled")
	}
	if !engine.IsRunning() {
		t.Fatal("expected engine to auto-start after add")
	}

	job, ok := engine.GetJob("shell-job")
	if !ok {
		t.Fatal("expected shell-job to exist")
	}
	if err := job.Task(); err != nil {
		t.Fatalf("shell job task() error = %v", err)
	}
}

func TestParseCronTaskCommandAgentPrefix(t *testing.T) {
	mode, payload := parseCronTaskCommand("agent: summarize yesterday logs")
	if mode != cronTaskAgent {
		t.Fatalf("expected agent mode, got %s", mode)
	}
	if payload != "summarize yesterday logs" {
		t.Fatalf("unexpected payload %q", payload)
	}
}

func TestParseCronNotificationTargetTelegramPrefix(t *testing.T) {
	platform, chatID, replyToMsgID, stripped := parseCronNotificationTarget("tg:12345/77 agent: remind me")
	if platform != "telegram" {
		t.Fatalf("expected telegram platform, got %q", platform)
	}
	if chatID != "12345" {
		t.Fatalf("expected chatID 12345, got %q", chatID)
	}
	if replyToMsgID != "77" {
		t.Fatalf("expected replyToMsgID 77, got %q", replyToMsgID)
	}
	if stripped != "agent: remind me" {
		t.Fatalf("unexpected stripped command %q", stripped)
	}
}

func TestCronStoreSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	store := cron.NewStore(filepath.Join(tmpDir, "cron_jobs.json"))
	engine := cron.NewEngine()
	engine.Start()

	err := engine.AddJobWithMeta(
		"persisted",
		"Cron: persisted",
		"echo persisted",
		cron.IntervalSchedule{Interval: time.Hour},
		func() error { return nil },
		map[string]string{
			"mode":          string(cronTaskShell),
			"command":       "echo persisted",
			"schedule_text": "每小时",
			"platform":      "telegram",
			"chatID":        "12345",
		},
	)
	if err != nil {
		t.Fatalf("AddJobWithMeta() error = %v", err)
	}
	if err := engine.PauseJob("persisted"); err != nil {
		t.Fatalf("PauseJob() error = %v", err)
	}
	if err := store.Save(engine); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	restored := cron.NewEngine()
	count, err := store.Load(restored, func(job cron.PersistedJob) (func() error, map[string]string, error) {
		return func() error { return nil }, map[string]string{"mode": job.Mode}, nil
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 restored job, got %d", count)
	}
	if !restored.IsRunning() {
		t.Fatal("expected restored engine to be running")
	}
	job, ok := restored.GetJob("persisted")
	if !ok {
		t.Fatal("expected restored job")
	}
	if job.Status != cron.StatusPaused {
		t.Fatalf("expected paused restored job, got %s", job.Status)
	}
	if got := job.Metadata["schedule_text"]; got != "每小时" {
		t.Fatalf("expected schedule_text preserved, got %q", got)
	}
	if got := job.Metadata["platform"]; got != "telegram" {
		t.Fatalf("expected platform metadata preserved, got %q", got)
	}
	if got := job.Metadata["chatID"]; got != "12345" {
		t.Fatalf("expected chatID metadata preserved, got %q", got)
	}
}
