package embedder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"sync/atomic"
)

// Embedder is the interface for embedding providers.
// It produces vector representations of text for semantic search and RAG.
type Embedder interface {
	// Embed returns the embedding vector for the given text.
	Embed(ctx context.Context, text string) ([]float64, error)
	// EmbedBatch returns embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float64, error)
	// Dimension returns the dimension of the embedding vectors.
	Dimension() int
	// Name returns the provider name (e.g. "openai", "ollama").
	Name() string
	// Model returns the model identifier (e.g. "text-embedding-3-small").
	Model() string
}

// CacheKey generates a deterministic cache key from text.
func CacheKey(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// EmbeddingCache provides an LRU cache for embedding results.
// Same input text always produces the same vector, so caching avoids redundant API calls.
type EmbeddingCache struct {
	mu    sync.RWMutex
	items map[string][]float64
	order []string // front = oldest, back = newest
	max   int
	hits  int64
	miss  int64
}

// NewEmbeddingCache creates a new LRU cache with the given capacity.
// If max <= 0, defaults to 1024.
func NewEmbeddingCache(max int) *EmbeddingCache {
	if max <= 0 {
		max = 1024
	}
	return &EmbeddingCache{
		items: make(map[string][]float64, max),
		order: make([]string, 0, max),
		max:   max,
	}
}

// Get retrieves a cached embedding. Returns nil if not found.
// Does NOT promote the entry (LRU promotion only on write).
func (c *EmbeddingCache) Get(key string) []float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	vec, ok := c.items[key]
	if !ok {
		atomic.AddInt64(&c.miss, 1)
		return nil
	}
	atomic.AddInt64(&c.hits, 1)
	return vec
}

// Put stores an embedding in the cache, evicting the oldest entry if at capacity.
// If the key already exists, the value is updated and the entry is promoted.
func (c *EmbeddingCache) Put(key string, vec []float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.items[key]; exists {
		// Update value and move to end (promote)
		c.items[key] = vec
		c.moveToEnd(key)
		return
	}

	// Evict oldest if at capacity
	if len(c.order) >= c.max {
		oldest := c.order[0]
		delete(c.items, oldest)
		c.order = c.order[1:]
	}

	c.items[key] = vec
	c.order = append(c.order, key)
}

// Stats returns cache hit/miss statistics.
func (c *EmbeddingCache) Stats() (hits, misses int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.miss
}

// Len returns the number of cached entries.
func (c *EmbeddingCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.items)
}

// Clear removes all entries from the cache.
func (c *EmbeddingCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = make(map[string][]float64, c.max)
	c.order = c.order[:0]
	c.hits = 0
	c.miss = 0
}

func (c *EmbeddingCache) moveToEnd(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, key)
			return
		}
	}
}

// CachedEmbedder wraps an Embedder with an LRU cache.
type CachedEmbedder struct {
	inner Embedder
	cache *EmbeddingCache
}

// NewCachedEmbedder wraps an Embedder with caching.
func NewCachedEmbedder(inner Embedder, cacheSize int) *CachedEmbedder {
	return &CachedEmbedder{
		inner: inner,
		cache: NewEmbeddingCache(cacheSize),
	}
}

// Cache returns the underlying cache for statistics.
func (ce *CachedEmbedder) Cache() *EmbeddingCache {
	return ce.cache
}

func (ce *CachedEmbedder) Name() string   { return ce.inner.Name() }
func (ce *CachedEmbedder) Model() string  { return ce.inner.Model() }
func (ce *CachedEmbedder) Dimension() int { return ce.inner.Dimension() }

func (ce *CachedEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	key := CacheKey(text)
	if vec := ce.cache.Get(key); vec != nil {
		return vec, nil
	}
	vec, err := ce.inner.Embed(ctx, text)
	if err != nil {
		return nil, err
	}
	ce.cache.Put(key, vec)
	return vec, nil
}

func (ce *CachedEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	// Check cache for each text, batch the misses
	results := make([][]float64, len(texts))
	misses := make([]int, 0) // indices of uncached texts
	missTexts := make([]string, 0)

	for i, text := range texts {
		key := CacheKey(text)
		if vec := ce.cache.Get(key); vec != nil {
			results[i] = vec
		} else {
			misses = append(misses, i)
			missTexts = append(missTexts, text)
		}
	}

	if len(missTexts) == 0 {
		return results, nil
	}

	// Batch embed the misses
	vecs, err := ce.inner.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}

	for j, idx := range misses {
		results[idx] = vecs[j]
		ce.cache.Put(CacheKey(missTexts[j]), vecs[j])
	}

	return results, nil
}

// Registry manages multiple embedder providers with registration and switching.
type Registry struct {
	mu        sync.RWMutex
	embedders map[string]Embedder
	active    string // ID of the active embedder
}

// NewRegistry creates a new embedder registry.
func NewRegistry() *Registry {
	return &Registry{
		embedders: make(map[string]Embedder),
	}
}

// Register adds an embedder to the registry.
// The id must be unique. Returns false if id already exists.
func (r *Registry) Register(id string, e Embedder) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.embedders[id]; exists {
		return false
	}
	r.embedders[id] = e
	if r.active == "" {
		r.active = id
	}
	return true
}

// Unregister removes an embedder from the registry.
// Cannot remove the active embedder.
func (r *Registry) Unregister(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == r.active {
		return false // cannot remove active
	}
	if _, exists := r.embedders[id]; !exists {
		return false
	}
	delete(r.embedders, id)
	return true
}

// Get returns an embedder by ID.
func (r *Registry) Get(id string) (Embedder, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.embedders[id]
	return e, ok
}

// Active returns the currently active embedder.
func (r *Registry) Active() Embedder {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.embedders[r.active]
}

// ActiveID returns the ID of the active embedder.
func (r *Registry) ActiveID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.active
}

// Switch changes the active embedder. Returns false if id not found.
func (r *Registry) Switch(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.embedders[id]; !exists {
		return false
	}
	r.active = id
	return true
}

// List returns all registered embedder IDs and their info.
type EmbedderInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Model     string `json:"model"`
	Dimension int    `json:"dimension"`
	Active    bool   `json:"active"`
}

func (r *Registry) List() []EmbedderInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]EmbedderInfo, 0, len(r.embedders))
	for id, e := range r.embedders {
		result = append(result, EmbedderInfo{
			ID:        id,
			Name:      e.Name(),
			Model:     e.Model(),
			Dimension: e.Dimension(),
			Active:    id == r.active,
		})
	}
	return result
}
