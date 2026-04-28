package rag

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLiteStore is a persistent vector store backed by SQLite.
// It stores vectors and metadata in a SQLite database, enabling
// incremental updates and efficient queries without loading everything into memory.
type SQLiteStore struct {
	mu   sync.RWMutex
	db   *sql.DB
	path string // path to the SQLite database file
	dim  int

	// In-memory cache for fast search (lazy-loaded)
	cache  map[string]*VectorEntry
	loaded bool // whether cache has been loaded from DB
}

// NewSQLiteStore creates a new SQLite-backed vector store.
// If the database file doesn't exist, it will be created.
func NewSQLiteStore(dim int, dbPath string) (*SQLiteStore, error) {
	if dim <= 0 {
		return nil, fmt.Errorf("vector dimension must be positive, got %d", dim)
	}

	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	s := &SQLiteStore{
		db:    db,
		path:  dbPath,
		dim:   dim,
		cache: make(map[string]*VectorEntry),
	}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return s, nil
}

// initSchema creates the necessary tables if they don't exist.
func (s *SQLiteStore) initSchema() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS vectors (
			id TEXT PRIMARY KEY,
			dimension INTEGER NOT NULL,
			vector BLOB NOT NULL,
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vectors_id ON vectors(id)`,
		`CREATE TABLE IF NOT EXISTS documents (
			id TEXT PRIMARY KEY,
			path TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			chunk_ids TEXT NOT NULL DEFAULT '[]',
			indexed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			metadata TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_id ON documents(id)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '{}',
			doc_id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_chunks_id ON chunks(id)`,
		`CREATE TABLE IF NOT EXISTS store_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("exec schema: %w", err)
		}
	}

	// Store dimension
	_, err := s.db.Exec(`INSERT OR REPLACE INTO store_meta (key, value) VALUES ('dimension', ?)`, fmt.Sprintf("%d", s.dim))
	if err != nil {
		return fmt.Errorf("store dimension: %w", err)
	}

	return nil
}

// Dimension returns the expected vector dimension.
func (s *SQLiteStore) Dimension() int { return s.dim }

// Len returns the number of stored vectors.
func (s *SQLiteStore) Len() int {
	s.mu.RLock()
	if s.loaded {
		count := len(s.cache)
		s.mu.RUnlock()
		return count
	}
	s.mu.RUnlock()

	var count int
	row := s.db.QueryRow("SELECT COUNT(*) FROM vectors")
	if err := row.Scan(&count); err != nil {
		return 0
	}
	return count
}

// loadCache loads all vectors from the database into memory.
func (s *SQLiteStore) loadCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.loaded {
		return nil
	}

	rows, err := s.db.Query("SELECT id, vector, metadata FROM vectors")
	if err != nil {
		return fmt.Errorf("query vectors: %w", err)
	}
	defer rows.Close()

	cache := make(map[string]*VectorEntry)
	for rows.Next() {
		var id string
		var vecBlob []byte
		var metaStr string
		if err := rows.Scan(&id, &vecBlob, &metaStr); err != nil {
			continue // skip corrupted rows
		}

		vec, err := decodeVectorBlob(vecBlob)
		if err != nil {
			continue // skip corrupted vectors
		}

		var metadata map[string]string
		if err := json.Unmarshal([]byte(metaStr), &metadata); err != nil {
			metadata = make(map[string]string)
		}

		cache[id] = &VectorEntry{
			ID:       id,
			Vector:   vec,
			Metadata: metadata,
		}
	}

	s.cache = cache
	s.loaded = true
	return nil
}

// Upsert adds or updates a vector entry.
func (s *SQLiteStore) Upsert(id string, vector []float64, metadata map[string]string) error {
	if len(vector) != s.dim {
		return fmt.Errorf("vector dimension mismatch: got %d, want %d", len(vector), s.dim)
	}

	normalized := normalizeVector(vector)
	vecBlob, err := encodeVectorBlob(normalized)
	if err != nil {
		return fmt.Errorf("encode vector: %w", err)
	}

	metaBytes, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Upsert into SQLite
	_, err = s.db.Exec(
		`INSERT INTO vectors (id, dimension, vector, metadata, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET vector=excluded.vector, metadata=excluded.metadata, updated_at=excluded.updated_at`,
		id, s.dim, vecBlob, string(metaBytes), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("upsert vector: %w", err)
	}

	// Update cache
	if s.loaded {
		s.cache[id] = &VectorEntry{
			ID:       id,
			Vector:   normalized,
			Metadata: copyMap(metadata),
		}
	}

	return nil
}

// Delete removes a vector entry.
func (s *SQLiteStore) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.Exec("DELETE FROM vectors WHERE id = ?", id)
	if err != nil {
		return false
	}

	affected, _ := result.RowsAffected()
	delete(s.cache, id)
	return affected > 0
}

// Get retrieves a vector entry by ID.
func (s *SQLiteStore) Get(id string) (*VectorEntry, bool) {
	s.mu.RLock()
	if s.loaded {
		entry, ok := s.cache[id]
		if ok {
			s.mu.RUnlock()
			cp := *entry
			cp.Metadata = copyMap(entry.Metadata)
			return &cp, true
		}
		s.mu.RUnlock()
		return nil, false
	}
	s.mu.RUnlock()

	// Fallback to DB query
	var vecBlob []byte
	var metaStr string
	err := s.db.QueryRow("SELECT vector, metadata FROM vectors WHERE id = ?", id).Scan(&vecBlob, &metaStr)
	if err != nil {
		return nil, false
	}

	vec, err := decodeVectorBlob(vecBlob)
	if err != nil {
		return nil, false
	}

	var metadata map[string]string
	json.Unmarshal([]byte(metaStr), &metadata)

	return &VectorEntry{
		ID:       id,
		Vector:   vec,
		Metadata: metadata,
	}, true
}

// Search returns the top-K most similar vectors using cosine similarity.
func (s *SQLiteStore) Search(query []float64, topK int) []SearchResult {
	if topK <= 0 {
		return nil
	}

	// Ensure cache is loaded
	if err := s.loadCache(); err != nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return rankEntries(s.cache, query, topK, "", "")
}

// SearchWithFilter returns top-K results filtered by metadata key=value.
func (s *SQLiteStore) SearchWithFilter(query []float64, topK int, filterKey, filterValue string) []SearchResult {
	if topK <= 0 {
		return nil
	}

	if err := s.loadCache(); err != nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	return rankEntries(s.cache, query, topK, filterKey, filterValue)
}

// AllIDs returns all stored vector IDs.
func (s *SQLiteStore) AllIDs() []string {
	s.mu.RLock()
	if s.loaded {
		ids := make([]string, 0, len(s.cache))
		for id := range s.cache {
			ids = append(ids, id)
		}
		s.mu.RUnlock()
		return ids
	}
	s.mu.RUnlock()

	rows, err := s.db.Query("SELECT id FROM vectors")
	if err != nil {
		return nil
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// Clear removes all entries from both DB and cache.
// Implements VectorStoreBackend interface.
func (s *SQLiteStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, _ = s.db.Exec("DELETE FROM vectors")
	s.cache = make(map[string]*VectorEntry)
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

// Path returns the database file path.
func (s *SQLiteStore) Path() string {
	return s.path
}

// Stats returns store statistics (entry count, DB file size).
func (s *SQLiteStore) Stats() (count int, dbSize int64, err error) {
	row := s.db.QueryRow("SELECT COUNT(*) FROM vectors")
	if err := row.Scan(&count); err != nil {
		return 0, 0, err
	}

	info, statErr := os.Stat(s.path)
	if statErr != nil {
		return count, 0, nil
	}
	return count, info.Size(), nil
}

// --- Vector encoding/decoding helpers ---

// encodeVectorBlob encodes a float64 slice into a JSON blob for SQLite storage.
func encodeVectorBlob(vec []float64) ([]byte, error) {
	return json.Marshal(vec)
}

// decodeVectorBlob decodes a JSON blob back into a float64 slice.
func decodeVectorBlob(blob []byte) ([]float64, error) {
	var vec []float64
	if err := json.Unmarshal(blob, &vec); err != nil {
		return nil, fmt.Errorf("decode vector: %w", err)
	}
	return vec, nil
}

// --- Document and Chunk persistence ---

// SaveDocument persists a document and its chunks to SQLite.
func (s *SQLiteStore) SaveDocument(doc *Document, chunks map[string]*Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunkIDs := make([]string, 0, len(doc.Chunks))
	for _, cid := range doc.Chunks {
		chunkIDs = append(chunkIDs, cid)
	}
	chunkIDsJSON, _ := json.Marshal(chunkIDs)
	docMetaJSON, _ := json.Marshal(doc.Metadata)

	_, err := s.db.Exec(
		`INSERT INTO documents (id, path, title, chunk_ids, indexed_at, metadata)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET path=excluded.path, title=excluded.title, chunk_ids=excluded.chunk_ids, indexed_at=excluded.indexed_at, metadata=excluded.metadata`,
		doc.ID, doc.Path, doc.Title, string(chunkIDsJSON), doc.IndexedAt.Format(time.RFC3339), string(docMetaJSON),
	)
	if err != nil {
		return fmt.Errorf("save document %s: %w", doc.ID, err)
	}

	// Save chunks
	for _, cid := range doc.Chunks {
		chunk, ok := chunks[cid]
		if !ok {
			continue
		}
		chunkMetaJSON, _ := json.Marshal(chunk.Metadata)
		_, err := s.db.Exec(
			`INSERT INTO chunks (id, content, metadata, doc_id)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(id) DO UPDATE SET content=excluded.content, metadata=excluded.metadata, doc_id=excluded.doc_id`,
			chunk.ID, chunk.Content, string(chunkMetaJSON), doc.ID,
		)
		if err != nil {
			return fmt.Errorf("save chunk %s: %w", chunk.ID, err)
		}
	}

	return nil
}

// LoadDocuments loads all documents from SQLite.
func (s *SQLiteStore) LoadDocuments() (map[string]*Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT id, path, title, chunk_ids, indexed_at, metadata FROM documents")
	if err != nil {
		return nil, fmt.Errorf("query documents: %w", err)
	}
	defer rows.Close()

	docs := make(map[string]*Document)
	for rows.Next() {
		var id, path, title, chunkIDsJSON, indexedAtStr, metaJSON string
		if err := rows.Scan(&id, &path, &title, &chunkIDsJSON, &indexedAtStr, &metaJSON); err != nil {
			continue
		}

		var chunkIDs []string
		json.Unmarshal([]byte(chunkIDsJSON), &chunkIDs)

		var metadata map[string]string
		json.Unmarshal([]byte(metaJSON), &metadata)

		indexedAt, _ := time.Parse(time.RFC3339, indexedAtStr)

		docs[id] = &Document{
			ID:        id,
			Path:      path,
			Title:     title,
			Chunks:    chunkIDs,
			IndexedAt: indexedAt,
			Metadata:  metadata,
		}
	}

	return docs, nil
}

// LoadChunks loads all chunks from SQLite.
func (s *SQLiteStore) LoadChunks() (map[string]*Chunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT id, content, metadata, doc_id FROM chunks")
	if err != nil {
		return nil, fmt.Errorf("query chunks: %w", err)
	}
	defer rows.Close()

	chunks := make(map[string]*Chunk)
	for rows.Next() {
		var id, content, metaJSON, docID string
		if err := rows.Scan(&id, &content, &metaJSON, &docID); err != nil {
			continue
		}

		var metadata map[string]string
		json.Unmarshal([]byte(metaJSON), &metadata)

		chunks[id] = &Chunk{
			ID:       id,
			Content:  content,
			Metadata: metadata,
		}
	}

	return chunks, nil
}

// DeleteDocument removes a document and its chunks from SQLite.
func (s *SQLiteStore) DeleteDocument(docID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// First get chunk IDs from the document
	var chunkIDsJSON string
	err := s.db.QueryRow("SELECT chunk_ids FROM documents WHERE id = ?", docID).Scan(&chunkIDsJSON)
	if err != nil {
		return fmt.Errorf("query document %s: %w", docID, err)
	}

	var chunkIDs []string
	json.Unmarshal([]byte(chunkIDsJSON), &chunkIDs)

	// Delete chunks
	for _, cid := range chunkIDs {
		s.db.Exec("DELETE FROM chunks WHERE id = ?", cid)
	}

	// Delete document
	_, err = s.db.Exec("DELETE FROM documents WHERE id = ?", docID)
	if err != nil {
		return fmt.Errorf("delete document %s: %w", docID, err)
	}

	return nil
}

// LoadIndexStats loads index statistics from SQLite.
func (s *SQLiteStore) LoadIndexStats() (IndexStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var docCount, chunkCount int
	s.db.QueryRow("SELECT COUNT(*) FROM documents").Scan(&docCount)
	s.db.QueryRow("SELECT COUNT(*) FROM chunks").Scan(&chunkCount)

	// Get source counts from document metadata
	rows, err := s.db.Query("SELECT path FROM documents")
	if err != nil {
		return IndexStats{}, err
	}
	defer rows.Close()

	sources := make(map[string]int)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err == nil {
			sources[path]++
		}
	}

	return IndexStats{
		DocumentCount: docCount,
		ChunkCount:    chunkCount,
		Sources:       sources,
	}, nil
}
