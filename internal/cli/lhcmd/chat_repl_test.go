package lhcmd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/config"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/session"
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

	engine := a.CronEngine()
	store := a.CronStore()
	handled := handleCronCommand("add shell-job 每小时 echo hello-cron", engine, store, a, agent.DefaultLoopConfig(), "")
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

func TestHandleCronCommandAgentJobUsesAgentCronService(t *testing.T) {
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

	handled := handleCronCommand("add agent-job 每小时 agent: summarize yesterday logs", a.CronEngine(), a.CronStore(), a, agent.DefaultLoopConfig(), "")
	if !handled {
		t.Fatal("expected command to be handled")
	}

	job, ok := a.CronEngine().GetJob("agent-job")
	if !ok {
		t.Fatal("expected agent-job to exist")
	}
	if got := job.Metadata["mode"]; got != string(cronTaskAgent) {
		t.Fatalf("expected agent mode metadata, got %q", got)
	}
	if got := job.Metadata["command"]; got != "summarize yesterday logs" {
		t.Fatalf("expected command without agent prefix, got %q", got)
	}
	if got := job.Metadata["session_id"]; got != "" {
		t.Fatalf("expected CLI agent cron job to be sessionless by default, got session_id %q", got)
	}
}

func TestHandleCronCommandBindCurrentSession(t *testing.T) {
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

	handled := handleCronCommand("add --bind-current agent-job 每小时 agent: summarize yesterday logs", a.CronEngine(), a.CronStore(), a, agent.DefaultLoopConfig(), "session-123")
	if !handled {
		t.Fatal("expected command to be handled")
	}

	job, ok := a.CronEngine().GetJob("agent-job")
	if !ok {
		t.Fatal("expected agent-job to exist")
	}
	if got := job.Metadata["session_id"]; got != "session-123" {
		t.Fatalf("expected current session_id to be bound, got %q", got)
	}
}

func TestHandleCronCommandBindExplicitSession(t *testing.T) {
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

	handled := handleCronCommand("add --session session-456 agent-job 每小时 agent: summarize yesterday logs", a.CronEngine(), a.CronStore(), a, agent.DefaultLoopConfig(), "")
	if !handled {
		t.Fatal("expected command to be handled")
	}

	job, ok := a.CronEngine().GetJob("agent-job")
	if !ok {
		t.Fatal("expected agent-job to exist")
	}
	if got := job.Metadata["session_id"]; got != "session-456" {
		t.Fatalf("expected explicit session_id to be bound, got %q", got)
	}
}

func TestHandleCronCommandSupportsInlineChineseScheduleAndCommand(t *testing.T) {
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

	handled := handleCronCommand("add --session lh job1 每六个小时去调研一下智能体的论文", a.CronEngine(), a.CronStore(), a, agent.DefaultLoopConfig(), "")
	if !handled {
		t.Fatal("expected command to be handled")
	}

	job, ok := a.CronEngine().GetJob("job1")
	if !ok {
		t.Fatal("expected job1 to exist")
	}
	if got := job.Metadata["session_id"]; got != "lh" {
		t.Fatalf("expected session_id lh, got %q", got)
	}
	if got := job.Metadata["mode"]; got != string(cronTaskAgent) {
		t.Fatalf("expected bound inline command to default to agent mode, got %q", got)
	}
	if got := job.Metadata["command"]; got != "调研一下智能体的论文" {
		t.Fatalf("unexpected command %q", got)
	}
	if job.Schedule.String() != "every 6h0m0s" {
		t.Fatalf("expected six-hour interval, got %s", job.Schedule)
	}
}

func TestParseCronAddSpecRejectsConflictingSessionOptions(t *testing.T) {
	if _, err := parseCronAddSpec([]string{"add", "--bind-current", "--session", "session-456", "job1", "每小时", "agent:", "hello"}); err == nil {
		t.Fatal("expected conflicting session options to fail")
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

func TestHandleCommandNewAliasCreatesSession(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("new session manager: %v", err)
	}

	current := mgr.New()
	originalID := current.ID

	handled, exit := handleCommand("/new", nil, &agent.LoopConfig{}, nil, nil, nil, mgr, &current, nil)
	if !handled {
		t.Fatal("expected command to be handled")
	}
	if exit {
		t.Fatal("expected command to stay in repl")
	}
	if current == nil {
		t.Fatal("expected current session to be set")
	}
	if current.ID == originalID {
		t.Fatal("expected /new to switch to a fresh session")
	}
	if _, ok := mgr.Get(current.ID); !ok {
		t.Fatalf("expected session %s to be owned by manager", current.ID)
	}
	if mgr.Count() != 2 {
		t.Fatalf("expected 2 sessions, got %d", mgr.Count())
	}
}
