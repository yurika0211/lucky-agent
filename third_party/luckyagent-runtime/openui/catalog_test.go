package openui

import (
	"encoding/json"
	"testing"
)

func TestFilterDeltaKeepsCatalogAndDropsUnknown(t *testing.T) {
	raw, _ := json.Marshal(Delta{
		TreeID: "t1",
		State:  StateStreaming,
		Nodes: []Node{
			{ID: "a", Type: "Table", Props: map[string]any{"columns": []string{"n"}, "rows": []any{[]any{"1"}}}},
			{ID: "b", Type: "Carousel", Props: map[string]any{"items": []any{}}},
			{ID: "c", Type: "Chart"},
		},
	})
	delta, dropped, err := ValidateDelta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta.Nodes) != 1 || delta.Nodes[0].Type != "Table" {
		t.Fatalf("kept %#v", delta.Nodes)
	}
	if delta.Nodes[0].Policy != PolicyInspect {
		t.Fatalf("policy %s", delta.Nodes[0].Policy)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped %#v", dropped)
	}
}

func TestFilterDeltaRejectsEmptyTreeID(t *testing.T) {
	_, _, err := ValidateDelta([]byte(`{"nodes":[]}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestActionsAreMutate(t *testing.T) {
	delta, _, err := FilterDelta(Delta{
		TreeID: "t1",
		Nodes: []Node{{
			ID: "go", Type: "Actions",
			Props: map[string]any{"items": []any{map[string]any{"id": "retry", "label": "重试"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if delta.Nodes[0].Policy != PolicyMutate {
		t.Fatalf("got %s", delta.Nodes[0].Policy)
	}
}

func TestValidateAction(t *testing.T) {
	if err := ValidateAction(Action{TreeID: "t", WidgetID: "w"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAction(Action{TreeID: "t"}); err == nil {
		t.Fatal("expected missing widget")
	}
}
