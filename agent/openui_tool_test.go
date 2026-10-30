package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestPresentUIKeepsTableAndStripsTreeFromModelNote(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok || registered.Handler == nil {
		t.Fatal("present_ui was not registered")
	}
	out, err := registered.Handler(map[string]any{
		"tree": map[string]any{
			"tree_id": "compare",
			"state":   "complete",
			"nodes": []any{
				map[string]any{"id": "tbl", "type": "Table", "props": map[string]any{"columns": []any{"name"}, "rows": []any{[]any{"a"}}}},
				map[string]any{"id": "bad", "type": "Carousel", "props": map[string]any{"items": []any{}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta, note := takeOpenUIDelta(presentUIToolName, out)
	if delta == nil || len(delta.Nodes) != 1 || delta.Nodes[0].Type != "Table" || delta.State != "complete" {
		t.Fatalf("delta=%#v", delta)
	}
	if strings.Contains(note, "tree_id") || strings.Contains(note, openUIMetadataKey) {
		t.Fatalf("model note still contains the tree: %s", note)
	}
	if _, plain := takeOpenUIDelta("file_read", out); plain != out {
		t.Fatal("other tools must keep their result")
	}
}

func TestPresentUIEmptyTreeStaysMarkdown(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok {
		t.Fatal("present_ui was not registered")
	}
	out, err := registered.Handler(map[string]any{
		"tree": map[string]any{
			"tree_id": "t1",
			"nodes":   []any{map[string]any{"id": "x", "type": "Carousel", "props": map[string]any{"items": []any{}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta, note := takeOpenUIDelta(presentUIToolName, out)
	if delta != nil {
		t.Fatalf("expected no tree, got %#v", delta)
	}
	if !strings.Contains(note, "Markdown") {
		t.Fatalf("note=%s", note)
	}
}
