package rag

import (
	"context"
	"fmt"
	"sync"

	embedderpkg "github.com/yurika0211/luckyharness/internal/embedder"
)

// VectorStoreBackend is the interface that both in-memory and persistent
// vector stores must implement. This enables swapping backends without
// changing the RAG pipeline.
type VectorStoreBackend interface {
	Dimension() int
	Len() int
	Upsert(id string, vector []float64, metadata map[string]string) error
	Delete(id string) bool
	Get(id string) (*VectorEntry, bool)
	Search(query []float64, topK int) []SearchResult
	SearchWithFilter(query []float64, topK int, filterKey, filterValue string) []SearchResult
	AllIDs() []string
	Clear()
}

// Ensure VectorStore implements VectorStoreBackend
var _ VectorStoreBackend = (*VectorStore)(nil)

// Ensure SQLiteStore implements VectorStoreBackend
var _ VectorStoreBackend = (*SQLiteStore)(nil)

// RAGManager is the top-level RAG system manager.
type RAGManager struct {
	store     VectorStoreBackend // v0.20.0: supports both in-memory and SQLite backends
	indexer   *Indexer
	retriever *Retriever
	embedder  embedderpkg.Embedder

	mu sync.RWMutex
}

type RAGConfig struct {
	EmbeddingDim    int
	RetrieverConfig RetrieverConfig
}

func DefaultRAGConfig() RAGConfig {
	return RAGConfig{
		EmbeddingDim:    0, // 0 = auto-detect from embedder
		RetrieverConfig: DefaultRetrieverConfig(),
	}
}

// NewRAGManager creates a new RAG system with the given embedder and in-memory store.
func NewRAGManager(embedder embedderpkg.Embedder, config RAGConfig) *RAGManager {
	dim := config.EmbeddingDim
	if dim <= 0 {
		dim = embedder.Dimension()
	}
	if dim <= 0 {
		dim = 128
	}

	store := NewVectorStore(dim)
	indexer := NewIndexer(store, embedder)
	retriever := NewRetriever(store, indexer, embedder, config.RetrieverConfig)

	return &RAGManager{
		store:     store,
		indexer:   indexer,
		retriever: retriever,
		embedder:  embedder,
	}
}

// NewRAGManagerWithSQLite creates a new RAG system with SQLite-backed persistent store.
func NewRAGManagerWithSQLite(embedder embedderpkg.Embedder, config RAGConfig, dbPath string) (*RAGManager, error) {
	dim := config.EmbeddingDim
	if dim <= 0 {
		dim = embedder.Dimension()
	}
	if dim <= 0 {
		dim = 128
	}

	store, err := NewSQLiteStore(dim, dbPath)
	if err != nil {
		return nil, fmt.Errorf("create sqlite store: %w", err)
	}

	indexer := NewIndexerWithBackend(store, embedder)

	// Load persisted documents and chunks from SQLite
	if docs, err := store.LoadDocuments(); err == nil && len(docs) > 0 {
		indexer.mu.Lock()
		for id, doc := range docs {
			indexer.documents[id] = doc
		}
		indexer.stats.DocumentCount = len(docs)
		indexer.mu.Unlock()
	}
	if chunks, err := store.LoadChunks(); err == nil && len(chunks) > 0 {
		indexer.mu.Lock()
		for id, chunk := range chunks {
			indexer.chunks[id] = chunk
		}
		indexer.stats.ChunkCount = len(chunks)
		indexer.mu.Unlock()
	}
	if stats, err := store.LoadIndexStats(); err == nil {
		indexer.mu.Lock()
		indexer.stats.Sources = stats.Sources
		if !stats.LastIndexed.IsZero() {
			indexer.stats.LastIndexed = stats.LastIndexed
		}
		indexer.mu.Unlock()
	}

	retriever := NewRetrieverWithBackend(store, indexer, embedder, config.RetrieverConfig)

	return &RAGManager{
		store:     store,
		indexer:   indexer,
		retriever: retriever,
		embedder:  embedder,
	}, nil
}

// IndexFile indexes a single file.
func (m *RAGManager) IndexFile(path string) (*Document, error) {
	return m.indexer.IndexFile(path)
}

// IndexText indexes raw text content.
func (m *RAGManager) IndexText(source, title, content string) (*Document, error) {
	return m.indexer.IndexText(source, title, content)
}

// IndexDirectory indexes all .md/.txt files in a directory.
func (m *RAGManager) IndexDirectory(dir string) ([]*Document, error) {
	return m.indexer.IndexDirectory(dir)
}

// Search queries the knowledge base.
func (m *RAGManager) Search(ctx context.Context, query string) ([]RetrievalResult, error) {
	return m.retriever.Search(ctx, query)
}

// SearchWithContext queries and returns assembled context string.
func (m *RAGManager) SearchWithContext(ctx context.Context, query string) (string, []RetrievalResult, error) {
	results, err := m.retriever.Search(ctx, query)
	if err != nil {
		return "", nil, err
	}
	context := m.retriever.BuildContext(results)
	return context, results, nil
}

// RemoveDocument removes a document from the index.
func (m *RAGManager) RemoveDocument(docID string) bool {
	return m.indexer.RemoveDocument(docID)
}

// Stats returns index statistics.
func (m *RAGManager) Stats() IndexStats {
	return m.indexer.Stats()
}

// ListDocuments returns all document IDs.
func (m *RAGManager) ListDocuments() []string {
	return m.indexer.ListDocuments()
}

// GetDocument returns a document by ID.
func (m *RAGManager) GetDocument(docID string) (*Document, bool) {
	return m.indexer.GetDocument(docID)
}

// UpdateRetrieverConfig updates the retriever configuration.
func (m *RAGManager) UpdateRetrieverConfig(config RetrieverConfig) {
	m.retriever.UpdateConfig(config)
}

// RetrieverConfig returns the current retriever configuration.
func (m *RAGManager) RetrieverConfig() RetrieverConfig {
	return m.retriever.Config()
}

// Store returns the underlying vector store backend (for advanced use).
func (m *RAGManager) Store() VectorStoreBackend {
	return m.store
}

// Indexer returns the underlying indexer (for advanced use).
func (m *RAGManager) Indexer() *Indexer {
	return m.indexer
}

// Retriever returns the underlying retriever (for advanced use).
func (m *RAGManager) Retriever() *Retriever {
	return m.retriever
}

// IsSQLite returns true if the store backend is SQLite-backed.
func (m *RAGManager) IsSQLite() bool {
	_, ok := m.store.(*SQLiteStore)
	return ok
}

// SQLiteStore returns the underlying SQLiteStore, or nil if using in-memory store.
func (m *RAGManager) SQLiteStore() *SQLiteStore {
	if s, ok := m.store.(*SQLiteStore); ok {
		return s
	}
	return nil
}

// CloseStore closes the underlying store (for SQLite, this closes the DB connection).
func (m *RAGManager) CloseStore() error {
	if s, ok := m.store.(*SQLiteStore); ok {
		return s.Close()
	}
	return nil
}

// String returns a summary of the RAG system.
func (m *RAGManager) String() string {
	stats := m.Stats()
	backend := "memory"
	if m.IsSQLite() {
		backend = "sqlite"
	}
	return fmt.Sprintf("RAGManager{docs=%d, chunks=%d, embedder=%s, dim=%d, backend=%s}",
		stats.DocumentCount, stats.ChunkCount, m.embedder.Name(), m.store.Dimension(), backend)
}
