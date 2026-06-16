package tool

import (
	"encoding/json"
	"testing"

	"github.com/yurika0211/luckyharness/internal/memory"
)

func TestMemoryHygieneToolRegistration(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	services := NewServices(nil, nil, "", nil, nil, ImageGenerationDefaults{}, nil, TTSDefaults{}, store, nil, nil)
	registry := NewRegistry()
	services.RegisterCoreTools(registry)

	tool, ok := registry.Get("memory_hygiene")
	if !ok {
		t.Fatal("memory_hygiene tool not registered")
	}
	if tool.Permission != PermApprove {
		t.Fatalf("permission = %s, want approve", tool.Permission)
	}
}

func TestMemoryHygieneHandlerAuditAndQuarantine(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTier("User: stale raw turn", "conversation", memory.TierShort, 0.7); err != nil {
		t.Fatalf("SaveWithTier: %v", err)
	}
	svc := NewMemoryToolService(store)

	out, err := svc.HandleHygiene(map[string]any{
		"action":       "audit",
		"min_severity": "medium",
	})
	if err != nil {
		t.Fatalf("HandleHygiene audit: %v", err)
	}
	var audit memory.HygieneReport
	if err := json.Unmarshal([]byte(out), &audit); err != nil {
		t.Fatalf("unmarshal audit: %v\n%s", err, out)
	}
	if len(audit.Findings) != 1 {
		t.Fatalf("findings = %d, want 1: %+v", len(audit.Findings), audit.Findings)
	}

	out, err = svc.HandleHygiene(map[string]any{
		"action":       "quarantine",
		"min_severity": "medium",
	})
	if err != nil {
		t.Fatalf("HandleHygiene quarantine: %v", err)
	}
	var cleaned memory.HygieneReport
	if err := json.Unmarshal([]byte(out), &cleaned); err != nil {
		t.Fatalf("unmarshal cleaned: %v\n%s", err, out)
	}
	if cleaned.Quarantined != 1 {
		t.Fatalf("quarantined = %d, want 1", cleaned.Quarantined)
	}
	if got := store.Search("stale"); len(got) != 0 {
		t.Fatalf("quarantined memory should not be recalled: %+v", got)
	}
}
