package tool_test

import (
	"encoding/json"
	"github.com/yurika0211/luckyagent-runtime/memoryaestus"
	. "github.com/yurika0211/luckyagent-tools"
	"github.com/yurika0211/luckyagent-tools/memory"
	"testing"
)

func TestMemoryHygieneToolRegistration(t *testing.T) {
	store, err := memoryaestus.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	services := NewServices(nil, nil, "", nil, nil, ImageGenerationDefaults{}, nil, TTSDefaults{}, store, nil, nil)
	registry := NewRegistry()
	services.RegisterCoreTools(registry)

	got, ok := registry.Get("memory")
	if !ok {
		t.Fatal("memory tool not registered")
	}
	if got.EffectivePermission(map[string]any{"action": "hygiene"}) != PermApprove {
		t.Fatal("memory hygiene should require approval")
	}
	if got.EffectivePermission(map[string]any{"action": "recall"}) != PermAuto {
		t.Fatal("memory recall should be auto")
	}
}

func TestMemoryHygieneHandlerAuditAndQuarantine(t *testing.T) {
	store, err := memoryaestus.NewStore(t.TempDir())
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
