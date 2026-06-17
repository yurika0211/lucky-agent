package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLoadSkipsMissedRunOnRestart(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "cron_jobs.json")
	staleNextRun := time.Now().Add(-1 * time.Hour).UnixMilli()
	data := `{
  "version": 1,
  "engine_running": false,
  "jobs": [
    {
      "id": "stale-job",
      "schedule_text": "每小时",
      "command": "echo stale",
      "mode": "shell",
      "next_run_unix": ` + fmt.Sprint(staleNextRun) + `,
      "metadata": {
        "mode": "shell",
        "command": "echo stale",
        "schedule_text": "每小时"
      }
    }
  ]
}`
	if err := os.WriteFile(storePath, []byte(data), 0600); err != nil {
		t.Fatalf("write json store: %v", err)
	}

	engine := NewEngine()
	store := NewStore(storePath)
	beforeLoad := time.Now()
	restored, err := store.Load(engine, func(job PersistedJob) (func() error, map[string]string, error) {
		return func() error { return nil }, map[string]string{
			"mode":          job.Mode,
			"command":       job.Command,
			"schedule_text": job.ScheduleText,
		}, nil
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if restored != 1 {
		t.Fatalf("expected 1 restored job, got %d", restored)
	}
	job, ok := engine.GetJob("stale-job")
	if !ok {
		t.Fatal("expected stale-job to be restored")
	}
	if !job.NextRun.After(beforeLoad) {
		t.Fatalf("expected stale next run to be rescheduled after load, got %v beforeLoad=%v", job.NextRun, beforeLoad)
	}
}
