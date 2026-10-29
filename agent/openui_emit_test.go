package agent

import (
	"encoding/json"
	"testing"
)

func TestEmitUIDeltaDropsUnknownKeepsTable(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"tree_id": "t1",
		"state":   "complete",
		"nodes": []map[string]any{
			{"id": "tbl", "type": "Table", "props": map[string]any{"columns": []string{"a"}, "rows": []any{[]any{"1"}}}},
			{"id": "x", "type": "Carousel", "props": map[string]any{"items": []any{}}},
		},
	})
	evt, dropped, err := EmitUIDelta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if evt == nil || evt.Type != ChatEventUIDelta || evt.UI == nil || len(evt.UI.Nodes) != 1 {
		t.Fatalf("evt=%#v", evt)
	}
	if evt.UI.Nodes[0].Type != "Table" {
		t.Fatalf("type %s", evt.UI.Nodes[0].Type)
	}
	if len(dropped) != 1 {
		t.Fatalf("dropped %#v", dropped)
	}
}

func TestEmitUIDeltaEmptyAfterFilter(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"tree_id": "t1",
		"nodes":   []map[string]any{{"id": "x", "type": "Carousel", "props": map[string]any{"items": []any{}}}},
	})
	evt, dropped, err := EmitUIDelta(raw)
	if err != nil {
		t.Fatal(err)
	}
	if evt != nil {
		t.Fatalf("expected nil event, got %#v", evt)
	}
	if len(dropped) == 0 {
		t.Fatal("expected dropped")
	}
}
