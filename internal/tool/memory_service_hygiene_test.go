package tool

import (
	"encoding/json"
	"testing"

	"github.com/yurika0211/luckyagent/internal/memory"
)

func TestMemoryHygieneRequiresDeleteConfirmation(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTier("User: raw turn", "conversation", memory.TierShort, 0.7); err != nil {
		t.Fatalf("SaveWithTier: %v", err)
	}
	svc := NewMemoryToolService(store)

	if _, err := svc.HandleHygiene(map[string]any{
		"action":       "delete",
		"min_severity": "medium",
	}); err == nil {
		t.Fatal("expected delete confirmation error")
	}
}

func TestMemoryHygieneDryRunDoesNotModify(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTier("User: raw turn", "conversation", memory.TierShort, 0.7); err != nil {
		t.Fatalf("SaveWithTier: %v", err)
	}
	svc := NewMemoryToolService(store)

	out, err := svc.HandleHygiene(map[string]any{
		"action":       "quarantine",
		"dry_run":      true,
		"min_severity": "medium",
	})
	if err != nil {
		t.Fatalf("HandleHygiene dry_run: %v", err)
	}
	var report memory.HygieneReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("unmarshal report: %v\n%s", err, out)
	}
	if !report.DryRun || report.Action != "quarantine" || len(report.Findings) != 1 {
		t.Fatalf("unexpected dry_run report: %+v", report)
	}
	if got := store.Search("raw turn"); len(got) == 0 {
		t.Fatal("dry_run should not archive matching memory")
	}
}

func TestMemoryHygieneRejectsUnsafeLimit(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	if _, err := svc.HandleHygiene(map[string]any{"limit": -1}); err == nil {
		t.Fatal("expected negative limit error")
	}
	if _, err := svc.HandleHygiene(map[string]any{"limit": maxHygieneLimit + 1}); err == nil {
		t.Fatal("expected max limit error")
	}
	if _, err := svc.HandleHygiene(map[string]any{"limit": 0}); err == nil {
		t.Fatal("expected unlimited confirmation error")
	}
}

func TestMemoryHygieneRestoreArchivedEntry(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTier("User: raw turn", "conversation", memory.TierShort, 0.7); err != nil {
		t.Fatalf("SaveWithTier: %v", err)
	}
	svc := NewMemoryToolService(store)

	out, err := svc.HandleHygiene(map[string]any{
		"action":       "quarantine",
		"min_severity": "medium",
	})
	if err != nil {
		t.Fatalf("quarantine: %v", err)
	}
	var quarantined memory.HygieneReport
	if err := json.Unmarshal([]byte(out), &quarantined); err != nil {
		t.Fatalf("unmarshal quarantine: %v", err)
	}
	if len(quarantined.Findings) != 1 {
		t.Fatalf("expected one finding, got %+v", quarantined)
	}
	id := quarantined.Findings[0].ID

	out, err = svc.HandleHygiene(map[string]any{
		"action":           "restore",
		"ids":              []any{id},
		"include_inactive": true,
	})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	var restored memory.HygieneReport
	if err := json.Unmarshal([]byte(out), &restored); err != nil {
		t.Fatalf("unmarshal restore: %v", err)
	}
	if restored.Restored != 1 {
		t.Fatalf("restored = %d, want 1", restored.Restored)
	}
}
