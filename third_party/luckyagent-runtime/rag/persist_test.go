package rag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistenceSaveLoad(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	embedder := newMockEmbedder(64)
	mgr := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})

	// Index some content
	_, err := mgr.IndexText("test-source", "Test Doc", "This is test content for persistence. It should survive save and load.")
	if err != nil {
		t.Fatalf("index text: %v", err)
	}

	// Save
	if err := p.Save(mgr); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Verify files exist
	if !p.Exists() {
		t.Error("expected persistence to exist after save")
	}

	indexFile := filepath.Join(dir, "rag-index.json")
	if _, err := os.Stat(indexFile); err != nil {
		t.Errorf("index file should exist: %v", err)
	}

	metaFile := filepath.Join(dir, "rag-meta.json")
	if _, err := os.Stat(metaFile); err != nil {
		t.Errorf("meta file should exist: %v", err)
	}

	// Create a new manager and load
	mgr2 := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	docCount, err := p.Load(mgr2)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if docCount != 1 {
		t.Errorf("expected 1 document loaded, got %d", docCount)
	}

	// Verify loaded data
	stats := mgr2.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}
	if stats.ChunkCount == 0 {
		t.Error("expected chunks after load")
	}

	// Verify document content
	docs := mgr2.ListDocuments()
	if len(docs) != 1 {
		t.Errorf("expected 1 doc ID, got %d", len(docs))
	}

	doc, ok := mgr2.GetDocument(docs[0])
	if !ok {
		t.Fatal("document should exist")
	}
	if doc.Title != "Test Doc" {
		t.Errorf("expected title 'Test Doc', got %s", doc.Title)
	}
}

func TestPersistenceMultipleDocs(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	embedder := newMockEmbedder(64)
	mgr := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})

	// Index multiple documents
	for i := 0; i < 5; i++ {
		_, err := mgr.IndexText(
			"doc-"+string(rune('A'+i)),
			"Document "+string(rune('A'+i)),
			"Content for document "+string(rune('A'+i))+". Some unique text here.",
		)
		if err != nil {
			t.Fatalf("index doc %d: %v", i, err)
		}
	}

	if err := p.Save(mgr); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Load into new manager
	mgr2 := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	docCount, err := p.Load(mgr2)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if docCount != 5 {
		t.Errorf("expected 5 documents, got %d", docCount)
	}

	stats := mgr2.Stats()
	if stats.DocumentCount != 5 {
		t.Errorf("expected 5 documents in stats, got %d", stats.DocumentCount)
	}
}

func TestPersistenceNoData(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	embedder := newMockEmbedder(64)
	mgr := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})

	// Load from empty dir should succeed with 0 docs
	docCount, err := p.Load(mgr)
	if err != nil {
		t.Fatalf("load from empty dir: %v", err)
	}
	if docCount != 0 {
		t.Errorf("expected 0 documents, got %d", docCount)
	}

	if p.Exists() {
		t.Error("expected Exists() to be false for empty dir")
	}
}

func TestPersistenceLastSaved(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	// Before save
	if !p.LastSaved().IsZero() {
		t.Error("expected zero time before save")
	}

	embedder := newMockEmbedder(64)
	mgr := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	mgr.IndexText("src", "Title", "Content")

	if err := p.Save(mgr); err != nil {
		t.Fatalf("save: %v", err)
	}

	// After save
	lastSaved := p.LastSaved()
	if lastSaved.IsZero() {
		t.Error("expected non-zero time after save")
	}
}

func TestPersistenceClear(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	embedder := newMockEmbedder(64)
	mgr := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	mgr.IndexText("src", "Title", "Content")

	if err := p.Save(mgr); err != nil {
		t.Fatalf("save: %v", err)
	}

	if !p.Exists() {
		t.Error("expected persistence to exist")
	}

	if err := p.Clear(); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if p.Exists() {
		t.Error("expected persistence to be gone after clear")
	}
}

func TestPersistenceOverwrite(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	embedder := newMockEmbedder(64)

	// Save first version
	mgr1 := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	mgr1.IndexText("src1", "Doc1", "First document content.")
	p.Save(mgr1)

	// Save second version (different data)
	mgr2 := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	mgr2.IndexText("src2", "Doc2", "Second document content.")
	mgr2.IndexText("src3", "Doc3", "Third document content.")
	p.Save(mgr2)

	// Load and verify it's the second version
	mgr3 := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	docCount, _ := p.Load(mgr3)
	if docCount != 2 {
		t.Errorf("expected 2 documents from second save, got %d", docCount)
	}
}

func TestPersistenceSearchAfterLoad(t *testing.T) {
	dir := t.TempDir()
	p := NewPersistence(dir)

	embedder := newMockEmbedder(64)
	mgr := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})

	// Index and save
	mgr.IndexText("go-lang", "Go Language", "Go is a statically typed compiled programming language designed at Google by Robert Griesemer, Rob Pike, and Ken Thompson.")
	p.Save(mgr)

	// Load into new manager and search
	mgr2 := NewRAGManager(embedder, RAGConfig{EmbeddingDim: 64})
	p.Load(mgr2)

	results, err := mgr2.Search(nil, "programming language")
	if err != nil {
		t.Fatalf("search after load: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results after loading persisted index")
	}
}
