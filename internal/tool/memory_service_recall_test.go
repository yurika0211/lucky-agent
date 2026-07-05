package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/memory"
)

func TestMemoryToolServiceRecallJSONWithFilters(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("LuckyAgent recall should return structured JSON.", "project", memory.TierLong, 0.9, memory.SaveOptions{
		Links: []string{"LuckyAgent"},
	}); err != nil {
		t.Fatalf("SaveWithOptions project: %v", err)
	}
	if err := store.SaveWithOptions("Python is preferred for scripts.", "preference", memory.TierMedium, 0.7, memory.SaveOptions{}); err != nil {
		t.Fatalf("SaveWithOptions preference: %v", err)
	}
	svc := NewMemoryToolService(store)

	out, err := svc.HandleRecall(map[string]any{
		"query":    "LuckyAgent recall",
		"category": "project",
		"tier":     "long",
		"limit":    1,
		"format":   "json",
	})
	if err != nil {
		t.Fatalf("HandleRecall: %v", err)
	}
	var payload struct {
		Source  string `json:"source"`
		Query   string `json:"query"`
		Count   int    `json:"count"`
		Results []struct {
			ID       string  `json:"id"`
			Category string  `json:"category"`
			Tier     string  `json:"tier"`
			Score    float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal recall json: %v\n%s", err, out)
	}
	if payload.Source == "" || payload.Query != "LuckyAgent recall" || payload.Count != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if got := payload.Results[0]; got.ID == "" || got.Category != "project" || got.Tier != "long" || got.Score <= 0 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestMemoryToolServiceRecallRejectsBounds(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	if _, err := svc.HandleRecall(map[string]any{"query": strings.Repeat("x", maxRecallQueryRunes+1)}); err == nil {
		t.Fatal("expected query length error")
	}
	if _, err := svc.HandleRecall(map[string]any{"query": "x", "limit": maxRecallLimit + 1}); err == nil {
		t.Fatal("expected limit error")
	}
	if _, err := svc.HandleRecall(map[string]any{"query": "x", "graph_depth": maxRecallGraphDepth + 1}); err == nil {
		t.Fatal("expected graph_depth error")
	}
}

func TestMemoryToolServiceRecallRecentJSON(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithOptions("Recent project memory.", "project", memory.TierMedium, 0.7, memory.SaveOptions{}); err != nil {
		t.Fatalf("SaveWithOptions: %v", err)
	}
	svc := NewMemoryToolService(store)

	out, err := svc.HandleRecall(map[string]any{
		"mode":   "recent",
		"limit":  1,
		"format": "json",
	})
	if err != nil {
		t.Fatalf("HandleRecall recent: %v", err)
	}
	var payload struct {
		Mode  string `json:"mode"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal recent json: %v\n%s", err, out)
	}
	if payload.Mode != "recent" || payload.Count != 1 {
		t.Fatalf("unexpected recent payload: %+v", payload)
	}
}

func TestMemoryToolServiceRecallDetailedIncludesMemoryTrace(t *testing.T) {
	store, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SaveWithTierAndTags("Outdoor walks often include [[Daughter]].", "plan", memory.TierMedium, 0.6, []string{"family"}); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	if err := store.SaveWithTierAndTags("[[Daughter]] has [[Pollen Allergy]].", "health", memory.TierLong, 0.95, []string{"health"}); err != nil {
		t.Fatalf("save health: %v", err)
	}
	svc := NewMemoryToolService(store)

	result, err := svc.HandleRecallDetailed(map[string]any{
		"query":         "Outdoor walks",
		"limit":         5,
		"graph_depth":   1,
		"explain_graph": false,
	})
	if err != nil {
		t.Fatalf("HandleRecallDetailed: %v", err)
	}
	if strings.TrimSpace(result.Output) == "" {
		t.Fatal("expected model-visible recall output")
	}
	rawTrace, ok := result.Metadata["memory_trace"]
	if !ok {
		t.Fatalf("expected memory_trace metadata, got %#v", result.Metadata)
	}
	trace, ok := rawTrace.(memory.SearchTrace)
	if !ok {
		t.Fatalf("expected memory.SearchTrace metadata, got %T", rawTrace)
	}
	if trace.Query != "Outdoor walks" || len(trace.Results) < 2 || len(trace.Hops) == 0 {
		t.Fatalf("unexpected trace: %+v", trace)
	}
}
