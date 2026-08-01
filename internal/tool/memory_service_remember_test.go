package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/memory"
)

func TestMemoryToolServiceRememberRejectsDirtyContent(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	if _, err := svc.HandleRemember(map[string]any{
		"content":  "api_key sk-1234567890abcdef should be remembered",
		"category": "security",
	}); err == nil {
		t.Fatal("expected secret-like content rejection")
	}
	if got := store.Search("sk-1234567890abcdef"); len(got) != 0 {
		t.Fatalf("dirty content should not be saved: %+v", got)
	}
}

func TestMemoryToolServiceRememberJSONDuplicateResult(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	if _, err := svc.HandleRemember(map[string]any{
		"content":  "用户喜欢Python",
		"category": "preference",
		"format":   "json",
	}); err != nil {
		t.Fatalf("HandleRemember first: %v", err)
	}
	out, err := svc.HandleRemember(map[string]any{
		"content":  "用户喜欢Python",
		"category": "preference",
		"format":   "json",
	})
	if err != nil {
		t.Fatalf("HandleRemember duplicate: %v", err)
	}
	var payload struct {
		Saved           bool   `json:"saved"`
		ID              string `json:"id"`
		Created         bool   `json:"created"`
		UpdatedExisting bool   `json:"updated_existing"`
		DuplicateOf     string `json:"duplicate_of"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal remember json: %v\n%s", err, out)
	}
	if !payload.Saved || payload.Created || !payload.UpdatedExisting || payload.ID == "" || payload.DuplicateOf == "" {
		t.Fatalf("unexpected duplicate payload: %+v", payload)
	}
}

func TestMemoryToolServiceRememberUpsertState(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	if _, err := svc.HandleRemember(map[string]any{
		"content":     "Pollen allergy is active",
		"category":    "health",
		"tier":        "long",
		"state_key":   "family.daughter.pollen",
		"state_value": "active",
		"confidence":  0.8,
	}); err != nil {
		t.Fatalf("HandleRemember first state: %v", err)
	}
	out, err := svc.HandleRemember(map[string]any{
		"content":     "Pollen allergy is resolved",
		"category":    "health",
		"tier":        "long",
		"state_key":   "family.daughter.pollen",
		"state_value": "resolved",
		"confidence":  0.9,
		"mode":        "upsert_state",
		"format":      "json",
	})
	if err != nil {
		t.Fatalf("HandleRemember upsert_state: %v", err)
	}
	var payload struct {
		Superseded []string `json:"superseded"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal upsert json: %v\n%s", err, out)
	}
	if len(payload.Superseded) != 1 {
		t.Fatalf("superseded = %v, want one old state", payload.Superseded)
	}
	results := store.Search("Pollen allergy")
	if len(results) != 1 || !strings.Contains(results[0].Content, "resolved") {
		t.Fatalf("expected only resolved state to recall, got %+v", results)
	}
}

func TestMemoryToolServiceRememberTypedRoutePolicy(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)
	policyJSON := `[{"id":"verify-deploy","match":{"query_any":["deploy"]},"required_tools":[{"name":"policy_probe","calls":[{"arguments":{"subject":"{{query}}"}}]}]}]`
	if _, err := svc.HandleRemember(map[string]any{
		"content":        "Deployment verification policy",
		"category":       "rule",
		"tier":           "long",
		"aliases":        []any{"deploy"},
		"route_policies": policyJSON,
	}); err != nil {
		t.Fatalf("HandleRemember route policy: %v", err)
	}

	route := store.Route("deploy release")
	if len(route.ToolRequirements) != 1 || route.ToolRequirements[0].Name != "policy_probe" {
		t.Fatalf("expected typed policy requirement, got %#v", route.ToolRequirements)
	}
	if got := route.ToolRequirements[0].Calls[0].Arguments["subject"]; got != "deploy release" {
		t.Fatalf("rendered subject = %#v", got)
	}
}
