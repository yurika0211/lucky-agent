package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	taskstore "github.com/yurika0211/luckyagent-orchestrator/task"
)

func TestTaskTreeRetainsOldDescendantsAndTheirResults(t *testing.T) {
	store, err := taskstore.NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-24 * time.Hour)
	for _, record := range []taskstore.Record{
		{ID: "root", Status: taskstore.StatusCompleted, CreatedAt: old, ExecutionSessionID: "root-session"},
		{ID: "child", ParentID: "root", Status: taskstore.StatusCompleted, CreatedAt: old, ExecutionSessionID: "child-session"},
		{ID: "grandchild", ParentID: "child", Status: taskstore.StatusCompleted, CreatedAt: old, ExecutionSessionID: "grandchild-session"},
	} {
		if _, err := store.Create(record); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveResult(record.ID, record.ID+" output"); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 205; i++ {
		if _, err := store.Create(taskstore.Record{ID: fmt.Sprintf("recent-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	(&Server{}).handleTaskTree(w, store, "root")
	if w.Code != http.StatusOK {
		t.Fatalf("tree status = %d: %s", w.Code, w.Body.String())
	}
	var node taskTreeNode
	if err := json.Unmarshal(w.Body.Bytes(), &node); err != nil {
		t.Fatal(err)
	}
	if len(node.Children) != 1 || len(node.Children[0].Children) != 1 {
		t.Fatalf("older tree was truncated by recent unrelated tasks: %+v", node)
	}
	child := node.Children[0]
	grandchild := child.Children[0]
	if node.Result != "root output" || child.Result != "child output" || grandchild.Result != "grandchild output" {
		t.Fatalf("tree must include results at every level: %+v", node)
	}
	if child.Task.ExecutionSessionID != "child-session" || grandchild.Task.ExecutionSessionID != "grandchild-session" {
		t.Fatalf("tree lost child session links: %+v", node)
	}
}
