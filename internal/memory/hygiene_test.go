package memory

import (
	"strings"
	"testing"
	"time"
)

func TestAuditHygieneFindsDirtyMemory(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTier("User: hello\nAssistant: hi", "conversation", TierShort, 0.5); err != nil {
		t.Fatalf("save raw conversation: %v", err)
	}
	if err := store.SaveWithTier("Project fact: run tests before deploy", "project", TierMedium, 0.7); err != nil {
		t.Fatalf("save clean fact: %v", err)
	}

	report := store.AuditHygiene(HygieneOptions{MinSeverity: "medium"})
	if report.Scanned != 2 {
		t.Fatalf("scanned = %d, want 2", report.Scanned)
	}
	if len(report.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(report.Findings), report.Findings)
	}
	if report.Findings[0].Reason != "raw_conversation" {
		t.Fatalf("reason = %s, want raw_conversation", report.Findings[0].Reason)
	}
}

func TestQuarantineDirtyRemovesFromRecall(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTier("Assistant: stale answer about deploy", "conversation", TierShort, 0.9); err != nil {
		t.Fatalf("save: %v", err)
	}

	report, err := store.QuarantineDirty(HygieneOptions{MinSeverity: "medium"})
	if err != nil {
		t.Fatalf("QuarantineDirty: %v", err)
	}
	if report.Quarantined != 1 {
		t.Fatalf("quarantined = %d, want 1", report.Quarantined)
	}
	if got := store.Search("deploy"); len(got) != 0 {
		t.Fatalf("quarantined memory should not be recalled: %+v", got)
	}
	auditInactive := store.AuditHygiene(HygieneOptions{IncludeInactive: true, MinSeverity: "medium"})
	if len(auditInactive.Findings) == 0 {
		t.Fatalf("expected inactive dirty memory to remain auditable")
	}
}

func TestDeleteDirtyRemovesDuplicatesAndExpired(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	expired := time.Now().Add(-time.Hour)
	if err := store.SaveWithOptions("temporary stale fact", "project", TierMedium, 0.5, SaveOptions{ExpiresAt: &expired}); err != nil {
		t.Fatalf("save expired: %v", err)
	}
	if err := store.SaveWithTier("same fact", "project", TierMedium, 0.4); err != nil {
		t.Fatalf("save duplicate 1: %v", err)
	}
	if err := store.SaveWithTier("same fact", "project", TierMedium, 0.8); err != nil {
		t.Fatalf("save duplicate 2: %v", err)
	}

	report, err := store.DeleteDirty(HygieneOptions{IncludeInactive: true, MinSeverity: "medium"})
	if err != nil {
		t.Fatalf("DeleteDirty: %v", err)
	}
	if report.Deleted == 0 {
		t.Fatalf("expected at least one deleted finding: %+v", report)
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(report.Findings[0].Reason)), "secret") {
		t.Fatalf("unexpected secret finding in duplicate/expired test: %+v", report.Findings)
	}
}

func TestHygieneDetectsStateConflict(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("Pollen allergy is active", "health", TierLong, 0.8, SaveOptions{StateKey: "family.daughter.pollen", StateValue: "active", Confidence: 0.8}); err != nil {
		t.Fatalf("save active: %v", err)
	}
	if err := store.SaveWithOptions("Pollen allergy is resolved", "health", TierLong, 0.9, SaveOptions{StateKey: "family.daughter.pollen", StateValue: "resolved", Confidence: 0.9}); err != nil {
		t.Fatalf("save resolved: %v", err)
	}

	report := store.AuditHygiene(HygieneOptions{MinSeverity: "high"})
	found := false
	for _, issue := range report.Findings {
		if issue.Reason == "state_conflict" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected state_conflict finding: %+v", report.Findings)
	}
}
