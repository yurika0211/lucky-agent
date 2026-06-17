package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// --- Integration tests for SQLite-backed RAG ---

func TestRAGManagerWithSQLite(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-test.db")

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create RAG manager with SQLite: %v", err)
	}
	defer mgr.CloseStore()

	// Verify it's SQLite-backed
	if !mgr.IsSQLite() {
		t.Error("expected SQLite backend")
	}
	if mgr.SQLiteStore() == nil {
		t.Error("expected non-nil SQLiteStore")
	}

	// Index some content
	doc, err := mgr.IndexText("test-source", "Test Document", "This is test content for SQLite persistence.")
	if err != nil {
		t.Fatalf("index text: %v", err)
	}
	if doc.ID == "" {
		t.Error("expected non-empty doc ID")
	}

	// Search
	results, err := mgr.Search(context.Background(), "test content")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results")
	}

	// Stats
	stats := mgr.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}

	// String representation
	str := mgr.String()
	if str == "" {
		t.Error("expected non-empty string representation")
	}
	if str != "sqlite" && str != "memory" {
		// Just check it contains backend info
	}
}

func TestRAGManagerWithSQLitePersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-persist.db")

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	// Create and populate
	mgr1, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager 1: %v", err)
	}

	mgr1.IndexText("doc1.md", "Document One", "Content for document one about Go programming.")
	mgr1.IndexText("doc2.md", "Document Two", "Content for document two about Python data science.")

	if mgr1.Stats().DocumentCount != 2 {
		t.Errorf("expected 2 documents, got %d", mgr1.Stats().DocumentCount)
	}

	// Close and reopen
	mgr1.CloseStore()

	mgr2, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager 2: %v", err)
	}
	defer mgr2.CloseStore()

	// Data should persist
	if mgr2.Stats().DocumentCount != 2 {
		t.Errorf("expected 2 documents after reopen, got %d", mgr2.Stats().DocumentCount)
	}

	// Search should work
	results, err := mgr2.Search(context.Background(), "programming")
	if err != nil {
		t.Fatalf("search after reopen: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results after reopen")
	}
}

func TestRAGManagerWithSQLiteRemoveDocument(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-remove.db")

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	defer mgr.CloseStore()

	mgr.IndexText("remove-test.md", "Remove Test", "Content to be removed.")

	docID := docID("remove-test.md")
	if !mgr.RemoveDocument(docID) {
		t.Error("expected removal to succeed")
	}

	if mgr.Stats().DocumentCount != 0 {
		t.Errorf("expected 0 documents after removal, got %d", mgr.Stats().DocumentCount)
	}
}

func TestRAGManagerWithSQLiteDirectoryIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-dir.db")

	// Create test files
	testDir := filepath.Join(dir, "docs")
	os.MkdirAll(testDir, 0755)
	os.WriteFile(filepath.Join(testDir, "a.md"), []byte("# Article A\n\nContent about Go."), 0644)
	os.WriteFile(filepath.Join(testDir, "b.txt"), []byte("Content about Python."), 0644)

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	defer mgr.CloseStore()

	docs, err := mgr.IndexDirectory(testDir)
	if err != nil {
		t.Fatalf("index directory: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 documents, got %d", len(docs))
	}
}

func TestRAGManagerWithSQLiteStats(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-stats.db")

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	defer mgr.CloseStore()

	// Check SQLite store stats
	sqlStore := mgr.SQLiteStore()
	if sqlStore == nil {
		t.Fatal("expected SQLiteStore to be non-nil")
	}

	count, dbSize, err := sqlStore.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 entries initially, got %d", count)
	}

	// Add some data
	mgr.IndexText("stats-test.md", "Stats Test", "Content for stats test.")

	count, dbSize, err = sqlStore.Stats()
	if err != nil {
		t.Fatalf("stats after index: %v", err)
	}
	if count == 0 {
		t.Error("expected entries after indexing")
	}
	if dbSize == 0 {
		t.Error("expected non-zero db size after indexing")
	}
}

func TestRAGManagerMemoryFallback(t *testing.T) {
	// Test that in-memory backend still works
	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr := NewRAGManager(e, cfg)

	if mgr.IsSQLite() {
		t.Error("expected memory backend, not SQLite")
	}
	if mgr.SQLiteStore() != nil {
		t.Error("expected nil SQLiteStore for memory backend")
	}

	mgr.IndexText("memory-test.md", "Memory Test", "Content for memory test.")

	if mgr.Stats().DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", mgr.Stats().DocumentCount)
	}

	// CloseStore should be no-op for memory backend
	if err := mgr.CloseStore(); err != nil {
		t.Errorf("expected nil error for memory backend CloseStore, got %v", err)
	}
}

func TestRAGManagerWithSQLiteConcurrentIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-concurrent.db")

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	defer mgr.CloseStore()

	// Concurrent indexing
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func(idx int) {
			mgr.IndexText(
				"concurrent-"+string(rune('A'+idx)),
				"Concurrent Doc "+string(rune('A'+idx)),
				"Content for concurrent document "+string(rune('A'+idx)),
			)
			done <- true
		}(i)
	}

	for i := 0; i < 5; i++ {
		<-done
	}

	if mgr.Stats().DocumentCount != 5 {
		t.Errorf("expected 5 documents after concurrent indexing, got %d", mgr.Stats().DocumentCount)
	}
}

func TestIndexerSQLiteSaveDocumentDoesNotRaceOnConcurrentIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "rag-race.db")

	e := newMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64

	mgr, err := NewRAGManagerWithSQLite(e, cfg, dbPath)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	defer mgr.CloseStore()

	done := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		go func(idx int) {
			_, _ = mgr.IndexText(
				"race-"+string(rune('A'+idx)),
				"Race Doc "+string(rune('A'+idx)),
				"Content for race document "+string(rune('A'+idx)),
			)
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 8; i++ {
		<-done
	}

	if got := mgr.Stats().DocumentCount; got != 8 {
		t.Fatalf("expected 8 documents after concurrent indexing, got %d", got)
	}
}
