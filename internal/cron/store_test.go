package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreLoadFallsBackToLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "cron_jobs.json")
	data := `{
  "version": 1,
  "engine_running": true,
  "jobs": [
    {
      "id": "legacy-job",
      "schedule_text": "每小时",
      "command": "echo hi",
      "mode": "shell",
      "paused": false,
      "metadata": {
        "mode": "shell",
        "command": "echo hi",
        "schedule_text": "每小时"
      }
    }
  ]
}`
	if err := os.WriteFile(legacyPath, []byte(data), 0600); err != nil {
		t.Fatalf("write legacy json: %v", err)
	}

	engine := NewEngine()
	store := NewStore(filepath.Join(dir, "mission.md"))
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
	if _, ok := engine.GetJob("legacy-job"); !ok {
		t.Fatal("expected legacy-job to be restored")
	}
}

func TestStoreLoadFiltersLegacyAutoCronSessionID(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "cron_jobs.json")
	data := `{
  "version": 1,
  "engine_running": false,
  "jobs": [
    {
      "id": "agent-job",
      "schedule_text": "每小时",
      "command": "summarize logs",
      "mode": "agent",
      "metadata": {
        "mode": "agent",
        "command": "summarize logs",
        "session_id": "cron-agent-job"
      }
    },
    {
      "id": "bound-job",
      "schedule_text": "每小时",
      "command": "summarize bound logs",
      "mode": "agent",
      "metadata": {
        "mode": "agent",
        "command": "summarize bound logs",
        "session_id": "user-session-123"
      }
    }
  ]
}`
	if err := os.WriteFile(storePath, []byte(data), 0600); err != nil {
		t.Fatalf("write json store: %v", err)
	}

	engine := NewEngine()
	store := NewStore(storePath)
	restored, err := store.Load(engine, func(job PersistedJob) (func() error, map[string]string, error) {
		return func() error { return nil }, map[string]string{
			"mode":    job.Mode,
			"command": job.Command,
		}, nil
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if restored != 2 {
		t.Fatalf("expected 2 restored jobs, got %d", restored)
	}

	legacyJob, ok := engine.GetJob("agent-job")
	if !ok {
		t.Fatal("expected agent-job to be restored")
	}
	if got := legacyJob.Metadata["session_id"]; got != "" {
		t.Fatalf("expected legacy auto session_id to be filtered, got %q", got)
	}

	boundJob, ok := engine.GetJob("bound-job")
	if !ok {
		t.Fatal("expected bound-job to be restored")
	}
	if got := boundJob.Metadata["session_id"]; got != "user-session-123" {
		t.Fatalf("expected explicit session_id to be preserved, got %q", got)
	}
}

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
