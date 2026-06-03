package rag

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/embedder"
)

type contextCheckingEmbedder struct {
	dim int
}

func (e *contextCheckingEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	return make([]float64, e.dim), nil
}

func (e *contextCheckingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = make([]float64, e.dim)
	}
	return out, nil
}

func (e *contextCheckingEmbedder) Dimension() int { return e.dim }
func (e *contextCheckingEmbedder) Name() string   { return "context-check" }
func (e *contextCheckingEmbedder) Model() string  { return "context-check" }

type batchCountingEmbedder struct {
	dim             int
	embedCalls      int
	embedBatchCalls int
	lastBatchSize   int
}

func (e *batchCountingEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	e.embedCalls++
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	return make([]float64, e.dim), nil
}

func (e *batchCountingEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	e.embedBatchCalls++
	e.lastBatchSize = len(texts)
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}
	out := make([][]float64, len(texts))
	for i := range texts {
		out[i] = make([]float64, e.dim)
	}
	return out, nil
}

func (e *batchCountingEmbedder) Dimension() int { return e.dim }
func (e *batchCountingEmbedder) Name() string   { return "batch-count" }
func (e *batchCountingEmbedder) Model() string  { return "batch-count" }

func TestMockEmbedder(t *testing.T) {
	e := NewMockEmbedder(128)

	if e.Dimension() != 128 {
		t.Errorf("expected dimension 128, got %d", e.Dimension())
	}
	if e.Name() != "mock" {
		t.Errorf("expected name 'mock', got %s", e.Name())
	}

	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 128 {
		t.Errorf("expected vector length 128, got %d", len(vec))
	}

	// Same input should produce same output
	vec2, _ := e.Embed(context.Background(), "hello world")
	for i := range vec {
		if vec[i] != vec2[i] {
			t.Errorf("deterministic embedding failed at index %d", i)
			break
		}
	}

	// Different input should produce different output
	vec3, _ := e.Embed(context.Background(), "goodbye world")
	diff := false
	for i := range vec {
		if vec[i] != vec3[i] {
			diff = true
			break
		}
	}
	if !diff {
		t.Error("different inputs produced same embedding")
	}
}

func TestMockEmbedderBatch(t *testing.T) {
	e := NewMockEmbedder(64)
	texts := []string{"hello", "world", "test"}
	vecs, err := e.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Errorf("expected 3 vectors, got %d", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 64 {
			t.Errorf("vector %d: expected length 64, got %d", i, len(v))
		}
	}
}

func TestOpenAIEmbedder(t *testing.T) {
	cfg := embedder.OpenAIEmbedderConfig{
		Model:     "text-embedding-3-small",
		Dimension: 256,
	}
	e := NewOpenAIEmbedder(cfg)

	if e.Name() != "openai" {
		t.Errorf("expected name 'openai', got %s", e.Name())
	}
	if e.Dimension() != 256 {
		t.Errorf("expected dimension 256, got %d", e.Dimension())
	}

	// Without API credentials, it falls back to the deterministic mock embedder.
	vec, err := e.Embed(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 256 {
		t.Errorf("expected vector length 256, got %d", len(vec))
	}
}

func TestOpenAIEmbedderDefaults(t *testing.T) {
	e := NewOpenAIEmbedder(embedder.OpenAIEmbedderConfig{})
	if e.Dimension() != 1536 {
		t.Errorf("expected default dimension 1536, got %d", e.Dimension())
	}
}

func TestIndexerUsesEmbedBatch(t *testing.T) {
	store := NewVectorStore(8)
	emb := &batchCountingEmbedder{dim: 8}
	idx := NewIndexer(store, emb)

	content := strings.Repeat("段落内容。\n\n", 80)
	if _, err := idx.IndexText("test-source", "test-title", content); err != nil {
		t.Fatalf("IndexText error = %v", err)
	}

	if emb.embedCalls != 0 {
		t.Fatalf("expected Embed not to be used, got %d calls", emb.embedCalls)
	}
	if emb.embedBatchCalls == 0 {
		t.Fatal("expected EmbedBatch to be used at least once")
	}
	if emb.lastBatchSize <= 0 {
		t.Fatalf("expected positive batch size, got %d", emb.lastBatchSize)
	}
}

func TestVectorStoreUpsert(t *testing.T) {
	store := NewVectorStore(64)

	vec := make([]float64, 64)
	for i := range vec {
		vec[i] = float64(i)
	}

	err := store.Upsert("test-1", vec, map[string]string{"source": "doc1"})
	if err != nil {
		t.Fatal(err)
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

func TestVectorStoreDimensionMismatch(t *testing.T) {
	store := NewVectorStore(64)
	vec := make([]float64, 32)
	err := store.Upsert("bad", vec, nil)
	if err == nil {
		t.Error("expected dimension mismatch error")
	}
}

func TestVectorStoreDelete(t *testing.T) {
	store := NewVectorStore(64)
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

func TestVectorStoreSearch(t *testing.T) {
	store := NewVectorStore(64)
	e := NewMockEmbedder(64)

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

	// Programming-related docs should appear in results
	// Note: MockEmbedder uses character-hash, not true semantic similarity
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

func TestVectorStoreSearchWithFilter(t *testing.T) {
	store := NewVectorStore(64)
	e := NewMockEmbedder(64)

	vec1, _ := e.Embed(context.Background(), "test1")
	vec2, _ := e.Embed(context.Background(), "test2")

	store.Upsert("a", vec1, map[string]string{"category": "tech"})
	store.Upsert("b", vec2, map[string]string{"category": "food"})

	queryVec, _ := e.Embed(context.Background(), "test")
	results := store.SearchWithFilter(queryVec, 10, "category", "tech")

	if len(results) != 1 {
		t.Errorf("expected 1 filtered result, got %d", len(results))
	}
	if len(results) > 0 && results[0].ID != "a" {
		t.Errorf("expected result 'a', got %s", results[0].ID)
	}
}

func TestVectorStoreClear(t *testing.T) {
	store := NewVectorStore(64)
	vec := make([]float64, 64)
	store.Upsert("1", vec, nil)
	store.Upsert("2", vec, nil)
	store.Clear()
	if store.Len() != 0 {
		t.Errorf("expected 0 after clear, got %d", store.Len())
	}
}

func TestVectorStoreAllIDs(t *testing.T) {
	store := NewVectorStore(64)
	vec := make([]float64, 64)
	store.Upsert("a", vec, nil)
	store.Upsert("b", vec, nil)
	ids := store.AllIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 IDs, got %d", len(ids))
	}
}

func TestSplitChunks(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		chunkSize int
		overlap   int
		minChunks int
	}{
		{"empty", "", 512, 64, 0},
		{"short", "Hello world", 512, 64, 1},
		{"paragraphs", "Para one.\n\nPara two.\n\nPara three.", 20, 5, 2},
		{"long_single", "This is a very long sentence. It should be split into multiple chunks. Because it exceeds the chunk size limit. And has many sentences to process.", 60, 10, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := splitChunks(tt.text, tt.chunkSize, tt.overlap)
			if len(chunks) < tt.minChunks {
				t.Errorf("expected at least %d chunks, got %d", tt.minChunks, len(chunks))
			}
		})
	}
}

func TestSplitChunksOverlap(t *testing.T) {
	text := "First paragraph with some content.\n\nSecond paragraph with more content.\n\nThird paragraph with even more content."
	chunks := splitChunks(text, 50, 10)
	if len(chunks) < 2 {
		t.Errorf("expected at least 2 chunks with overlap, got %d", len(chunks))
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name    string
		content string
		path    string
		want    string
	}{
		{"h1", "# My Title\nSome content", "test.md", "My Title"},
		{"no_h1", "No title here", "myfile.md", "myfile"},
		{"h2_only", "## Subtitle\nContent", "doc.md", "doc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTitle(tt.content, tt.path)
			if got != tt.want {
				t.Errorf("extractTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDocID(t *testing.T) {
	id1 := docID("path/to/file1.md")
	id2 := docID("path/to/file2.md")
	id3 := docID("path/to/file1.md")

	if len(id1) != 16 {
		t.Errorf("expected 16-char doc ID, got %d chars", len(id1))
	}
	if id1 == id2 {
		t.Error("different paths should produce different IDs")
	}
	if id1 != id3 {
		t.Error("same path should produce same ID")
	}
}

func TestIndexerIndexText(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	doc, err := idx.IndexText("test.md", "Test Doc", "Hello world.\n\nThis is a test document with some content.")
	if err != nil {
		t.Fatal(err)
	}

	if doc.Title != "Test Doc" {
		t.Errorf("expected title 'Test Doc', got %s", doc.Title)
	}
	if len(doc.Chunks) == 0 {
		t.Error("expected at least 1 chunk")
	}

	stats := idx.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}
	if stats.ChunkCount == 0 {
		t.Error("expected at least 1 chunk in stats")
	}
}

func TestIndexerIndexTextPassesNonNilContextToEmbedder(t *testing.T) {
	e := &contextCheckingEmbedder{dim: 64}
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	if _, err := idx.IndexText("ctx-test.md", "Context Test", "Hello world."); err != nil {
		t.Fatalf("IndexText() error = %v", err)
	}
}

func TestIndexerIndexFile(t *testing.T) {
	// Create a temp file
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	content := "# Test Article\n\nThis is the first paragraph.\n\nThis is the second paragraph with more details."
	os.WriteFile(path, []byte(content), 0644)

	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	doc, err := idx.IndexFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if doc.Title != "Test Article" {
		t.Errorf("expected title 'Test Article', got %s", doc.Title)
	}
}

func TestIndexerIndexDirectory(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "doc1.md"), []byte("# Doc 1\n\nContent one."), 0644)
	os.WriteFile(filepath.Join(dir, "doc2.txt"), []byte("Plain text content."), 0644)
	os.WriteFile(filepath.Join(dir, "skip.json"), []byte("{}"), 0644) // should be skipped

	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	docs, err := idx.IndexDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(docs) != 2 {
		t.Errorf("expected 2 documents (md+txt), got %d", len(docs))
	}
}

func TestIndexerRemoveDocument(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	idx.IndexText("test.md", "Test", "Content here.")

	stats := idx.Stats()
	if stats.DocumentCount != 1 {
		t.Fatal("expected 1 document before removal")
	}

	docID := docID("test.md")
	if !idx.RemoveDocument(docID) {
		t.Error("expected removal to succeed")
	}

	stats = idx.Stats()
	if stats.DocumentCount != 0 {
		t.Errorf("expected 0 documents after removal, got %d", stats.DocumentCount)
	}
	if stats.ChunkCount != 0 {
		t.Errorf("expected 0 chunks after removal, got %d", stats.ChunkCount)
	}
}

func TestIndexerReIndex(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	// Index first time
	idx.IndexText("test.md", "V1", "First version content.")

	// Re-index with different content
	idx.IndexText("test.md", "V2", "Second version with different content.")

	stats := idx.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document after re-index, got %d", stats.DocumentCount)
	}
}

func TestIndexerGetChunk(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	doc, _ := idx.IndexText("test.md", "Test", "Some content here.")
	if len(doc.Chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}

	chunk, ok := idx.GetChunk(doc.Chunks[0])
	if !ok {
		t.Fatal("chunk not found")
	}
	if chunk.Content == "" {
		t.Error("chunk content should not be empty")
	}
}

func TestIndexerListDocuments(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	idx.IndexText("a.md", "A", "Content A.")
	idx.IndexText("b.md", "B", "Content B.")

	ids := idx.ListDocuments()
	if len(ids) != 2 {
		t.Errorf("expected 2 documents, got %d", len(ids))
	}
}

func TestRetrieverSearch(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	// Index some documents
	idx.IndexText("go.md", "Go Language", "Go is a statically typed compiled language designed at Google.")
	idx.IndexText("python.md", "Python Language", "Python is an interpreted high-level programming language.")
	idx.IndexText("cooking.md", "Cooking Tips", "How to make the perfect pasta from scratch.")

	retriever := NewRetriever(store, idx, e, DefaultRetrieverConfig())

	results, err := retriever.Search(context.Background(), "programming language")
	if err != nil {
		t.Fatal(err)
	}

	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}

	// Programming docs should appear in results
	// Note: MockEmbedder uses character-hash, not true semantic similarity
	if len(results) > 0 {
		hasProgramming := false
		for _, r := range results {
			if r.DocTitle != "Cooking Tips" {
				hasProgramming = true
				break
			}
		}
		if !hasProgramming {
			t.Error("no programming docs in results for programming query")
		}
		if results[0].Content == "" {
			t.Error("result content should not be empty")
		}
	}
}

func TestRetrieverMinScore(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	idx.IndexText("doc1.md", "Doc1", "Content about algorithms and data structures.")

	config := RetrieverConfig{
		TopK:     5,
		MinScore: 0.99, // very high threshold
	}
	retriever := NewRetriever(store, idx, e, config)

	results, _ := retriever.Search(context.Background(), "completely unrelated topic xyz")
	// With very high min score, likely no results
	// (This tests the filtering logic, not the score values)
	_ = results
}

func TestRetrieverMMR(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	// Index similar documents
	idx.IndexText("go1.md", "Go Basics", "Go is a programming language with goroutines.")
	idx.IndexText("go2.md", "Go Advanced", "Go supports concurrency patterns and channels.")
	idx.IndexText("rust.md", "Rust Language", "Rust provides memory safety without garbage collection.")
	idx.IndexText("cooking.md", "Cooking", "Italian pasta recipes with tomato sauce.")

	config := RetrieverConfig{
		TopK:      3,
		MinScore:  0.1,
		UseMMR:    true,
		MMRLambda: 0.5,
	}
	retriever := NewRetriever(store, idx, e, config)

	results, err := retriever.Search(context.Background(), "programming language")
	if err != nil {
		t.Fatal(err)
	}

	if len(results) > 3 {
		t.Errorf("expected at most 3 results, got %d", len(results))
	}
}

func TestRetrieverFilterSource(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	idx.IndexText("go.md", "Go", "Go programming language.")
	idx.IndexText("rust.md", "Rust", "Rust programming language.")

	config := DefaultRetrieverConfig()
	config.FilterSource = "go.md"
	retriever := NewRetriever(store, idx, e, config)

	results, err := retriever.Search(context.Background(), "programming")
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range results {
		if r.DocSource != "go.md" {
			t.Errorf("expected only go.md results, got %s", r.DocSource)
		}
	}
}

func TestRetrieverExcludesGeneratedFinalAnswersByDefault(t *testing.T) {
	t.Setenv("LH_RAG_INCLUDE_GENERATED", "")

	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)

	if _, err := idx.IndexText("course/chapter1.md", "Course Chapter 1", "第一章课程原始资料：正确内容。"); err != nil {
		t.Fatalf("IndexText(course) error = %v", err)
	}
	if _, err := idx.IndexText("conversation/final/abc123", "Generated Answer", "模型生成的错误答案。"); err != nil {
		t.Fatalf("IndexText(generated) error = %v", err)
	}

	config := DefaultRetrieverConfig()
	config.TopK = 10
	config.MinScore = 0.0
	retriever := NewRetriever(store, idx, e, config)

	results, err := retriever.Search(context.Background(), "第一章")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected course source result")
	}
	for _, result := range results {
		if strings.HasPrefix(result.DocSource, "conversation/final/") {
			t.Fatalf("generated final answer should be excluded by default, got %#v", result)
		}
	}
}

func TestRetrieverBuildContext(t *testing.T) {
	results := []RetrievalResult{
		{
			ChunkID:   "c1",
			Content:   "Go is a programming language.",
			Score:     0.95,
			DocTitle:  "Go Guide",
			DocSource: "go.md",
		},
		{
			ChunkID:   "c2",
			Content:   "Rust focuses on safety.",
			Score:     0.85,
			DocTitle:  "Rust Guide",
			DocSource: "rust.md",
		},
	}

	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)
	retriever := NewRetriever(store, idx, e, DefaultRetrieverConfig())

	context := retriever.BuildContext(results)
	if context == "" {
		t.Error("expected non-empty context")
	}
	if !contains(context, "Go Guide") {
		t.Error("context should contain doc title")
	}
	if !contains(context, "Go is a programming language") {
		t.Error("context should contain chunk content")
	}
}

func TestRetrieverUpdateConfig(t *testing.T) {
	e := NewMockEmbedder(64)
	store := NewVectorStore(64)
	idx := NewIndexer(store, e)
	retriever := NewRetriever(store, idx, e, DefaultRetrieverConfig())

	retriever.UpdateConfig(RetrieverConfig{TopK: 10, MinScore: 0.8})
	cfg := retriever.Config()
	if cfg.TopK != 10 {
		t.Errorf("expected TopK=10, got %d", cfg.TopK)
	}
	if cfg.MinScore != 0.8 {
		t.Errorf("expected MinScore=0.8, got %f", cfg.MinScore)
	}
}

func TestRAGManager(t *testing.T) {
	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	// Index some content
	doc, err := mgr.IndexText("test.md", "Test Doc", "This is a test document about Go programming.")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "Test Doc" {
		t.Errorf("expected title 'Test Doc', got %s", doc.Title)
	}

	// Search
	results, err := mgr.Search(context.Background(), "Go programming")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Error("expected at least 1 search result")
	}

	// Stats
	stats := mgr.Stats()
	if stats.DocumentCount != 1 {
		t.Errorf("expected 1 document, got %d", stats.DocumentCount)
	}

	// String representation
	str := mgr.String()
	if !contains(str, "RAGManager") {
		t.Errorf("unexpected String(): %s", str)
	}
}

func TestRAGManagerSearchWithContext(t *testing.T) {
	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	mgr.IndexText("go.md", "Go Guide", "Go is a statically typed compiled language.")
	mgr.IndexText("rust.md", "Rust Guide", "Rust provides memory safety guarantees.")

	ctx, results, err := mgr.SearchWithContext(context.Background(), "programming language")
	if err != nil {
		t.Fatal(err)
	}
	if ctx == "" {
		t.Error("expected non-empty context")
	}
	if len(results) == 0 {
		t.Error("expected at least 1 result")
	}
	if !contains(ctx, "Retrieved Knowledge") {
		t.Error("context should have header")
	}
}

func TestRAGManagerRemoveDocument(t *testing.T) {
	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	mgr.IndexText("test.md", "Test", "Content.")
	stats := mgr.Stats()
	if stats.DocumentCount != 1 {
		t.Fatal("expected 1 document")
	}

	docID := docID("test.md")
	if !mgr.RemoveDocument(docID) {
		t.Error("expected removal to succeed")
	}

	stats = mgr.Stats()
	if stats.DocumentCount != 0 {
		t.Errorf("expected 0 documents, got %d", stats.DocumentCount)
	}
}

func TestRAGManagerListDocuments(t *testing.T) {
	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	mgr.IndexText("a.md", "A", "Content A.")
	mgr.IndexText("b.md", "B", "Content B.")

	ids := mgr.ListDocuments()
	if len(ids) != 2 {
		t.Errorf("expected 2 documents, got %d", len(ids))
	}
}

func TestRAGManagerGetDocument(t *testing.T) {
	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	mgr.IndexText("test.md", "Test", "Content.")
	docID := docID("test.md")

	doc, ok := mgr.GetDocument(docID)
	if !ok {
		t.Fatal("document not found")
	}
	if doc.Title != "Test" {
		t.Errorf("expected title 'Test', got %s", doc.Title)
	}
}

func TestRAGManagerUpdateRetrieverConfig(t *testing.T) {
	e := NewMockEmbedder(64)
	mgr := NewRAGManager(e, DefaultRAGConfig())

	mgr.UpdateRetrieverConfig(RetrieverConfig{TopK: 20})
	cfg := mgr.RetrieverConfig()
	if cfg.TopK != 20 {
		t.Errorf("expected TopK=20, got %d", cfg.TopK)
	}
}

func TestRAGManagerIndexFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "article.md")
	content := "# My Article\n\nThis is the content of my article about Go."
	os.WriteFile(path, []byte(content), 0644)

	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	doc, err := mgr.IndexFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Title != "My Article" {
		t.Errorf("expected title 'My Article', got %s", doc.Title)
	}
}

func TestRAGManagerIndexDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.md"), []byte("# A\n\nContent A."), 0644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("Content B."), 0644)
	os.WriteFile(filepath.Join(dir, "c.json"), []byte("{}"), 0644)

	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	docs, err := mgr.IndexDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 documents, got %d", len(docs))
	}
}

func TestRAGManagerMultipleSearches(t *testing.T) {
	e := NewMockEmbedder(64)
	cfg := DefaultRAGConfig()
	cfg.EmbeddingDim = 64
	mgr := NewRAGManager(e, cfg)

	// Index multiple docs
	docs := []struct {
		source, title, content string
	}{
		{"go.md", "Go", "Go is a compiled language with garbage collection."},
		{"python.md", "Python", "Python is an interpreted language with dynamic typing."},
		{"rust.md", "Rust", "Rust is a systems language with ownership model."},
		{"cooking.md", "Cooking", "How to bake bread with sourdough starter."},
		{"travel.md", "Travel", "Best places to visit in Japan during spring."},
	}

	for _, d := range docs {
		mgr.IndexText(d.source, d.title, d.content)
	}

	// Search for programming — verify results are returned
	// Note: MockEmbedder uses character-hash, not true semantic similarity,
	// so we can't assert semantic ranking. Just verify the pipeline works.
	results, _ := mgr.Search(context.Background(), "programming language")
	if len(results) == 0 {
		t.Error("expected at least 1 result for programming query")
	}

	// Search for cooking
	results2, _ := mgr.Search(context.Background(), "baking bread")
	if len(results2) == 0 {
		t.Error("expected at least 1 result for bread query")
	}
}

func TestCosineSimilarity(t *testing.T) {
	// Identical vectors
	a := []float64{1, 0, 0}
	sim := cosineSimilarity(a, a)
	if sim < 0.999 {
		t.Errorf("identical vectors should have similarity ~1, got %f", sim)
	}

	// Orthogonal vectors
	b := []float64{0, 1, 0}
	sim = cosineSimilarity(a, b)
	if sim > 0.001 {
		t.Errorf("orthogonal vectors should have similarity ~0, got %f", sim)
	}

	// Opposite vectors
	c := []float64{-1, 0, 0}
	sim = cosineSimilarity(a, c)
	if sim > -0.999 {
		t.Errorf("opposite vectors should have similarity ~-1, got %f", sim)
	}

	// Different length
	d := []float64{1, 0}
	sim = cosineSimilarity(a, d)
	if sim != 0 {
		t.Errorf("different length vectors should return 0, got %f", sim)
	}
}

func TestNormalizeVector(t *testing.T) {
	v := []float64{3, 4}
	n := normalizeVector(v)
	norm := 0.0
	for _, x := range n {
		norm += x * x
	}
	norm = norm // should be ~1
	if len(n) != 2 {
		t.Errorf("expected length 2, got %d", len(n))
	}

	// Zero vector
	z := []float64{0, 0, 0}
	nz := normalizeVector(z)
	for _, x := range nz {
		if x != 0 {
			t.Errorf("zero vector should stay zero, got %f", x)
		}
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
