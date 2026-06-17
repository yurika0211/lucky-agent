package rag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Persistence handles saving and loading RAG index data to/from disk as JSON.
// It is used when the SQLite store is unavailable.
type Persistence struct {
	dir string // directory for persistence files
}

// NewPersistence creates a new Persistence instance.
func NewPersistence(dir string) *Persistence {
	return &Persistence{dir: dir}
}

// persistData is the on-disk format for the RAG index.
type persistData struct {
	Version   int                  `json:"version"`
	SavedAt   time.Time            `json:"saved_at"`
	Documents map[string]*Document `json:"documents"`
	Chunks    map[string]*Chunk    `json:"chunks"`
	Vectors   map[string]*vecEntry `json:"vectors"`
	Stats     IndexStats           `json:"stats"`
}

// vecEntry is a JSON-serializable vector entry.
type vecEntry struct {
	ID       string            `json:"id"`
	Vector   []float64         `json:"vector"`
	Metadata map[string]string `json:"metadata"`
}

// Save persists the current RAG index state to disk.
func (p *Persistence) Save(m *RAGManager) error {
	if m == nil {
		return fmt.Errorf("rag manager is nil")
	}

	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return fmt.Errorf("create persistence dir: %w", err)
	}

	// Collect data from manager components
	m.mu.RLock()
	defer m.mu.RUnlock()

	indexer := m.Indexer()
	store := m.Store()

	// Get documents
	indexer.mu.RLock()
	docs := make(map[string]*Document, len(indexer.documents))
	for k, v := range indexer.documents {
		cp := *v
		cp.Chunks = append([]string{}, v.Chunks...)
		cp.Metadata = copyMap(v.Metadata)
		docs[k] = &cp
	}

	chunks := make(map[string]*Chunk, len(indexer.chunks))
	for k, v := range indexer.chunks {
		cp := *v
		cp.Metadata = copyMap(v.Metadata)
		chunks[k] = &cp
	}
	stats := indexer.stats
	stats.Sources = make(map[string]int)
	for k, v := range indexer.stats.Sources {
		stats.Sources[k] = v
	}
	indexer.mu.RUnlock()

	// Get vectors via interface methods
	ids := store.AllIDs()
	vectors := make(map[string]*vecEntry, len(ids))
	for _, id := range ids {
		entry, ok := store.Get(id)
		if !ok {
			continue
		}
		vec := make([]float64, len(entry.Vector))
		copy(vec, entry.Vector)
		vectors[id] = &vecEntry{
			ID:       entry.ID,
			Vector:   vec,
			Metadata: copyMap(entry.Metadata),
		}
	}
	dim := store.Dimension()

	data := persistData{
		Version:   1,
		SavedAt:   time.Now(),
		Documents: docs,
		Chunks:    chunks,
		Vectors:   vectors,
		Stats:     stats,
	}

	// Write to temp file first, then rename for atomicity
	tmpFile := filepath.Join(p.dir, ".rag-index.tmp")
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	enc := json.NewEncoder(f)
	if err := enc.Encode(data); err != nil {
		f.Close()
		return fmt.Errorf("encode data: %w", err)
	}
	f.Close()

	// Atomic rename
	finalPath := filepath.Join(p.dir, "rag-index.json")
	if err := os.Rename(tmpFile, finalPath); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	// Also save dimension info separately for quick loading
	metaPath := filepath.Join(p.dir, "rag-meta.json")
	meta := map[string]interface{}{
		"version":  1,
		"dim":      dim,
		"saved_at": data.SavedAt,
		"docs":     len(docs),
		"chunks":   len(chunks),
		"vectors":  len(vectors),
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	os.WriteFile(metaPath, metaBytes, 0600)

	return nil
}

// Load restores the RAG index state from disk.
// Returns the number of documents loaded, or an error.
func (p *Persistence) Load(m *RAGManager) (int, error) {
	if m == nil {
		return 0, fmt.Errorf("rag manager is nil")
	}

	finalPath := filepath.Join(p.dir, "rag-index.json")
	data, err := os.ReadFile(finalPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // no saved data, that's fine
		}
		return 0, fmt.Errorf("read persistence file: %w", err)
	}

	var pd persistData
	if err := json.Unmarshal(data, &pd); err != nil {
		return 0, fmt.Errorf("decode persistence data: %w", err)
	}

	if pd.Version != 1 {
		return 0, fmt.Errorf("unsupported persistence version: %d", pd.Version)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	indexer := m.Indexer()
	store := m.Store()

	// Restore vectors via interface methods
	for k, v := range pd.Vectors {
		vec := make([]float64, len(v.Vector))
		copy(vec, v.Vector)
		if err := store.Upsert(k, vec, copyMap(v.Metadata)); err != nil {
			continue // skip vectors with dimension mismatch
		}
	}

	// Restore documents and chunks
	indexer.mu.Lock()
	indexer.documents = make(map[string]*Document, len(pd.Documents))
	for k, v := range pd.Documents {
		cp := *v
		cp.Chunks = append([]string{}, v.Chunks...)
		cp.Metadata = copyMap(v.Metadata)
		indexer.documents[k] = &cp
	}
	indexer.chunks = make(map[string]*Chunk, len(pd.Chunks))
	for k, v := range pd.Chunks {
		cp := *v
		cp.Metadata = copyMap(v.Metadata)
		indexer.chunks[k] = &cp
	}
	indexer.stats = pd.Stats
	indexer.stats.Sources = make(map[string]int)
	for k, v := range pd.Stats.Sources {
		indexer.stats.Sources[k] = v
	}
	indexer.mu.Unlock()

	return len(pd.Documents), nil
}

// Exists returns true if a persisted index exists on disk.
func (p *Persistence) Exists() bool {
	finalPath := filepath.Join(p.dir, "rag-index.json")
	_, err := os.Stat(finalPath)
	return err == nil
}

// LastSaved returns the last save time, or zero time if never saved.
func (p *Persistence) LastSaved() time.Time {
	metaPath := filepath.Join(p.dir, "rag-meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return time.Time{}
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(data, &meta); err != nil {
		return time.Time{}
	}
	if savedAt, ok := meta["saved_at"].(string); ok {
		t, err := time.Parse(time.RFC3339, savedAt)
		if err == nil {
			return t
		}
	}
	return time.Time{}
}

// Clear removes all persisted data from disk.
func (p *Persistence) Clear() error {
	files := []string{"rag-index.json", "rag-meta.json", ".rag-index.tmp"}
	var firstErr error
	for _, f := range files {
		path := filepath.Join(p.dir, f)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
