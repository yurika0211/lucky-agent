package rag

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteStoreCreate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	store, err := NewSQLiteStore(64, dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	if store.Dimension() != 64 {
		t.Errorf("expected dimension 64, got %d", store.Dimension())
	}

	// DB file should exist
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file should exist after creation")
	}
}

func TestSQLiteStoreUpsert(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	for i := range vec {
		vec[i] = float64(i)
	}

	err = store.Upsert("test-1", vec, map[string]string{"source": "doc1"})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	if store.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", store.Len())
	}

	entry, ok := store.Get("test-1")
	if !ok {
		t.Fatal("entry not found")
	}
	if entry.Metadata["source"] != "doc1" {
		t.Errorf("expected source=doc1, got %s", entry.Metadata["source"])
	}
}

func TestSQLiteStoreDimensionMismatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 32)
	err = store.Upsert("bad", vec, nil)
	if err == nil {
		t.Error("expected dimension mismatch error")
	}
}

func TestSQLiteStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	store.Upsert("del-me", vec, nil)

	if !store.Delete("del-me") {
		t.Error("expected delete to return true")
	}
	if store.Len() != 0 {
		t.Errorf("expected 0 entries, got %d", store.Len())
	}
	if store.Delete("nonexistent") {
		t.Error("expected delete of nonexistent to return false")
	}
}

func TestSQLiteStoreSearch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	e := newMockEmbedder(64)

	// Index some documents
	texts := []struct {
		id   string
		text string
	}{
		{"doc1", "Go is a programming language designed for simplicity"},
		{"doc2", "Python is a popular programming language for data science"},
		{"doc3", "Rust is a systems programming language focused on safety"},
		{"doc4", "Cooking recipes for Italian pasta dishes"},
		{"doc5", "Baking bread with sourdough starter techniques"},
	}

	for _, tt := range texts {
		vec, _ := e.Embed(context.Background(), tt.text)
		store.Upsert(tt.id, vec, map[string]string{"text": tt.text})
	}

	// Search for programming languages
	queryVec, _ := e.Embed(context.Background(), "programming language")
	results := store.Search(queryVec, 3)

	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}

	if len(results) > 0 {
		hasProgramming := false
		for _, r := range results {
			if r.ID != "doc4" && r.ID != "doc5" {
				hasProgramming = true
				break
			}
		}
		if !hasProgramming {
			t.Error("no programming docs in results for programming query")
		}
	}

	// Results should be sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("results not sorted: [%d]=%.4f > [%d]=%.4f", i, results[i].Score, i-1, results[i-1].Score)
		}
	}
}

func TestSQLiteStoreSearchWithFilter(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	e := newMockEmbedder(64)

	vec1, _ := e.Embed(context.Background(), "Go programming")
	vec2, _ := e.Embed(context.Background(), "Python data science")
	vec3, _ := e.Embed(context.Background(), "Cooking recipes")

	store.Upsert("doc1", vec1, map[string]string{"category": "tech"})
	store.Upsert("doc2", vec2, map[string]string{"category": "tech"})
	store.Upsert("doc3", vec3, map[string]string{"category": "food"})

	queryVec, _ := e.Embed(context.Background(), "programming")
	results := store.SearchWithFilter(queryVec, 10, "category", "tech")

	for _, r := range results {
		if r.Metadata["category"] != "tech" {
			t.Errorf("expected category=tech, got %s", r.Metadata["category"])
		}
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results with category=tech, got %d", len(results))
	}
}

func TestSQLiteStoreAllIDs(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	store.Upsert("id-1", vec, nil)
	store.Upsert("id-2", vec, nil)
	store.Upsert("id-3", vec, nil)

	ids := store.AllIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}
}

func TestSQLiteStoreClear(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	store.Upsert("id-1", vec, nil)
	store.Upsert("id-2", vec, nil)

	store.Clear()

	if store.Len() != 0 {
		t.Errorf("expected 0 entries after clear, got %d", store.Len())
	}
}

func TestSQLiteStorePersistence(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "persist.db")

	e := newMockEmbedder(64)

	// Create and populate store
	store1, err := NewSQLiteStore(64, dbPath)
	if err != nil {
		t.Fatalf("create store1: %v", err)
	}

	vec1, _ := e.Embed(context.Background(), "hello world")
	vec2, _ := e.Embed(context.Background(), "goodbye world")
	store1.Upsert("doc1", vec1, map[string]string{"source": "test1"})
	store1.Upsert("doc2", vec2, map[string]string{"source": "test2"})

	if store1.Len() != 2 {
		t.Errorf("expected 2 entries, got %d", store1.Len())
	}
	store1.Close()

	// Reopen and verify data persists
	store2, err := NewSQLiteStore(64, dbPath)
	if err != nil {
		t.Fatalf("create store2: %v", err)
	}
	defer store2.Close()

	if store2.Len() != 2 {
		t.Errorf("expected 2 entries after reopen, got %d", store2.Len())
	}

	entry, ok := store2.Get("doc1")
	if !ok {
		t.Fatal("doc1 not found after reopen")
	}
	if entry.Metadata["source"] != "test1" {
		t.Errorf("expected source=test1, got %s", entry.Metadata["source"])
	}

	// Search should work after reopen
	queryVec, _ := e.Embed(context.Background(), "hello")
	results := store2.Search(queryVec, 5)
	if len(results) == 0 {
		t.Error("expected search results after reopen")
	}
}

func TestSQLiteStoreUpsertUpdate(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	e := newMockEmbedder(64)

	vec1, _ := e.Embed(context.Background(), "original content")
	store.Upsert("doc1", vec1, map[string]string{"version": "1"})

	vec2, _ := e.Embed(context.Background(), "updated content")
	store.Upsert("doc1", vec2, map[string]string{"version": "2"})

	if store.Len() != 1 {
		t.Errorf("expected 1 entry after upsert update, got %d", store.Len())
	}

	entry, ok := store.Get("doc1")
	if !ok {
		t.Fatal("doc1 not found")
	}
	if entry.Metadata["version"] != "2" {
		t.Errorf("expected version=2, got %s", entry.Metadata["version"])
	}
}

func TestSQLiteStoreStats(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	store.Upsert("id-1", vec, nil)
	store.Upsert("id-2", vec, nil)

	count, dbSize, err := store.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if count != 2 {
		t.Errorf("expected count=2, got %d", count)
	}
	if dbSize == 0 {
		t.Error("expected non-zero db size")
	}
}

func TestSQLiteStoreEmptySearch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	results := store.Search(vec, 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty store, got %d", len(results))
	}
}

func TestSQLiteStoreConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	store, err := NewSQLiteStore(64, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	e := newMockEmbedder(64)

	// Concurrent upserts
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(idx int) {
			vec, _ := e.Embed(context.Background(), "concurrent content")
			store.Upsert("concurrent-"+string(rune('A'+idx)), vec, map[string]string{"idx": string(rune('A' + idx))})
			done <- true
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if store.Len() != 10 {
		t.Errorf("expected 10 entries after concurrent upserts, got %d", store.Len())
	}
}

func TestSQLiteStoreDirectoryCreation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nested", "deep", "test.db")

	store, err := NewSQLiteStore(64, dbPath)
	if err != nil {
		t.Fatalf("create store with nested dirs: %v", err)
	}
	defer store.Close()

	vec := make([]float64, 64)
	store.Upsert("test", vec, nil)

	if store.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", store.Len())
	}
}

func TestSQLiteStoreZeroDimension(t *testing.T) {
	dir := t.TempDir()
	_, err := NewSQLiteStore(0, filepath.Join(dir, "test.db"))
	if err == nil {
		t.Error("expected error for zero dimension")
	}
}

func TestSQLiteStoreNegativeDimension(t *testing.T) {
	dir := t.TempDir()
	_, err := NewSQLiteStore(-1, filepath.Join(dir, "test.db"))
	if err == nil {
		t.Error("expected error for negative dimension")
	}
}
