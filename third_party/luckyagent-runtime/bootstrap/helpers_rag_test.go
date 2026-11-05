package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/rag"
)

func TestInitRAGRuntimeDoesNotLoadJSONFallback(t *testing.T) {
	home := t.TempDir()
	mgr, err := config.NewManagerWithDir(home)
	if err != nil {
		t.Fatal(err)
	}
	ragDir := filepath.Join(home, "rag")
	if err := os.MkdirAll(ragDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dump := []byte(`{"version":1,"documents":{"doc-1":{"id":"doc-1","title":"should-not-load","content":"x"}},"chunks":{},"vectors":{},"stats":{"DocumentCount":1}}`)
	if err := os.WriteFile(filepath.Join(ragDir, "rag-index.json"), dump, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ragDir, "luckyagent.db"), []byte("not-a-sqlite-database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !rag.NewPersistence(ragDir).Exists() {
		t.Fatal("expected persist dump")
	}

	rt, err := InitRAGRuntime(mgr, mgr.Get())
	if err != nil {
		t.Fatalf("InitRAGRuntime: %v", err)
	}
	if rt.SQLiteError == nil {
		t.Fatal("expected sqlite open error")
	}
	if rt.Manager == nil {
		t.Fatal("expected empty in-memory manager")
	}
	if rt.Manager.Stats().DocumentCount != 0 {
		t.Fatalf("json dump was loaded into memory: %+v", rt.Manager.Stats())
	}
}
