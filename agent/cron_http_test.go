package agent

import (
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-tools/cron"
)

func TestAddCronJobPauseResumeRemovePersists(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.NewManagerWithDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Set("provider", "openai")
	cfg.Set("api_key", "sk-test")
	cfg.Set("model", "gpt-3.5-turbo")

	a1, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	schedule, err := cron.ParseNaturalLanguage("每天9点")
	if err != nil {
		t.Fatal(err)
	}
	if err := a1.AddCronJob("morning", "早间任务", "提醒", "agent", "总结昨日", map[string]string{
		"session_id":       "sess-1",
		"platform":         "android",
		"delete_after_run": "true",
	}, schedule); err != nil {
		t.Fatalf("AddCronJob() error = %v", err)
	}
	job, ok := a1.CronEngine().GetJob("morning")
	if !ok || job.Metadata["session_id"] != "sess-1" || job.Metadata["platform"] != "android" || job.Metadata["command"] != "总结昨日" || !job.DeleteAfterRun {
		t.Fatalf("created job metadata = %+v ok=%v", job, ok)
	}
	if err := a1.PauseCronJob("morning"); err != nil {
		t.Fatal(err)
	}
	job, _ = a1.CronEngine().GetJob("morning")
	if job.Status != cron.StatusPaused {
		t.Fatalf("status after pause = %s", job.Status)
	}
	if err := a1.Close(); err != nil {
		t.Fatal(err)
	}

	a2, err := New(cfg)
	if err != nil {
		t.Fatalf("restart New() error = %v", err)
	}
	defer a2.Close()
	restored, ok := a2.CronEngine().GetJob("morning")
	if !ok || restored.Status != cron.StatusPaused || restored.Metadata["session_id"] != "sess-1" || !restored.DeleteAfterRun {
		t.Fatalf("restored job = %+v ok=%v", restored, ok)
	}
	if err := a2.ResumeCronJob("morning"); err != nil {
		t.Fatal(err)
	}
	restored, _ = a2.CronEngine().GetJob("morning")
	if restored.Status == cron.StatusPaused {
		t.Fatal("resume did not clear paused status")
	}
	if err := a2.RemoveCronJob("morning"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a2.CronEngine().GetJob("morning"); ok {
		t.Fatal("job still present after remove")
	}
	if err := a2.Close(); err != nil {
		t.Fatal(err)
	}

	a3, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer a3.Close()
	if _, ok := a3.CronEngine().GetJob("morning"); ok {
		t.Fatal("removed job returned after restart")
	}
	if _, err := filepath.Abs(a3.CronStore().Path()); err != nil {
		t.Fatal(err)
	}
}
