package cron

import (
	"os"
	"path/filepath"
	"testing"
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
