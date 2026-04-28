package rag

import (
	"fmt"
	"math"
	"sync"
)

// VectorEntry stores a vector with its metadata.
type VectorEntry struct {
	ID       string
	Vector   []float64
	Metadata map[string]string
}

// SearchResult is a single result from a vector search.
type SearchResult struct {
	ID       string
	Score    float64 // cosine similarity
	Metadata map[string]string
}

// VectorStore is an in-memory vector store with cosine similarity search.
type VectorStore struct {
	mu      sync.RWMutex
	entries map[string]*VectorEntry
	dim     int
}

func NewVectorStore(dim int) *VectorStore {
	return &VectorStore{
		entries: make(map[string]*VectorEntry),
		dim:    dim,
	}
}

// Dimension returns the expected vector dimension.
func (v *VectorStore) Dimension() int { return v.dim }

// Len returns the number of stored vectors.
func (v *VectorStore) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.entries)
}

// Upsert adds or updates a vector entry.
func (v *VectorStore) Upsert(id string, vector []float64, metadata map[string]string) error {
	if len(vector) != v.dim {
		return fmt.Errorf("vector dimension mismatch: got %d, want %d", len(vector), v.dim)
	}
	// Normalize the vector
	normalized := normalizeVector(vector)

	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[id] = &VectorEntry{
		ID:       id,
		Vector:   normalized,
		Metadata: metadata,
	}
	return nil
}

// Delete removes a vector entry.
func (v *VectorStore) Delete(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, exists := v.entries[id]
	if exists {
		delete(v.entries, id)
	}
	return exists
}

// Get retrieves a vector entry by ID.
func (v *VectorStore) Get(id string) (*VectorEntry, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	e, ok := v.entries[id]
	if !ok {
		return nil, false
	}
	// Return a copy
	cp := *e
	cp.Metadata = copyMap(e.Metadata)
	return &cp, true
}

// Search returns the top-K most similar vectors using cosine similarity.
func (v *VectorStore) Search(query []float64, topK int) []SearchResult {
	if topK <= 0 {
		return nil
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	return rankEntries(v.entries, query, topK, "", "")
}

// SearchWithFilter returns top-K results filtered by metadata key=value.
func (v *VectorStore) SearchWithFilter(query []float64, topK int, filterKey, filterValue string) []SearchResult {
	if topK <= 0 {
		return nil
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	return rankEntries(v.entries, query, topK, filterKey, filterValue)
}

// AllIDs returns all stored vector IDs.
func (v *VectorStore) AllIDs() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	ids := make([]string, 0, len(v.entries))
	for id := range v.entries {
		ids = append(ids, id)
	}
	return ids
}

// Clear removes all entries.
func (v *VectorStore) Clear() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries = make(map[string]*VectorEntry)
}

// --- helpers ---

func normalizeVector(v []float64) []float64 {
	norm := 0.0
	for _, x := range v {
		norm += x * x
	}
	norm = math.Sqrt(norm)
	if norm == 0 {
		return v
	}
	out := make([]float64, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) {
		return 0
	}
	dot := 0.0
	normA := 0.0
	normB := 0.0
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}
