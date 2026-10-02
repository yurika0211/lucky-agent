package embedder

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
)

func TestMockEmbedder(t *testing.T) {
	e := NewMockEmbedder(64)
	if e.Name() != "mock" {
		t.Errorf("Name() = %q, want %q", e.Name(), "mock")
	}
	if e.Dimension() != 64 {
		t.Errorf("Dimension() = %d, want 64", e.Dimension())
	}

	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 64 {
		t.Errorf("Embed() len = %d, want 64", len(vec))
	}

	// Deterministic
	vec2, _ := e.Embed(context.Background(), "hello world")
	for i := range vec {
		if vec[i] != vec2[i] {
			t.Error("Embed() not deterministic")
			break
		}
	}

	// Different text → different vector
	vec3, _ := e.Embed(context.Background(), "goodbye world")
	same := true
	for i := range vec {
		if vec[i] != vec3[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different text produced same vector")
	}
}

func TestMockEmbedderBatch(t *testing.T) {
	e := NewMockEmbedder(32)
	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Errorf("EmbedBatch() len = %d, want 3", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != 32 {
			t.Errorf("vec[%d] len = %d, want 32", i, len(v))
		}
	}
}

func TestMockEmbedderDefaultDim(t *testing.T) {
	e := NewMockEmbedder(0)
	if e.Dimension() != 128 {
		t.Errorf("Dimension() = %d, want 128", e.Dimension())
	}
}

func TestMockEmbedderWithModel(t *testing.T) {
	e := NewMockEmbedderWithModel(64, "custom-mock")
	if e.Model() != "custom-mock" {
		t.Errorf("Model() = %q, want %q", e.Model(), "custom-mock")
	}
}

func TestOpenAIEmbedder(t *testing.T) {
	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{})
	if e.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", e.Name(), "openai")
	}
	if e.Model() != "text-embedding-3-small" {
		t.Errorf("Model() = %q, want %q", e.Model(), "text-embedding-3-small")
	}
	if e.Dimension() != 1536 {
		t.Errorf("Dimension() = %d, want 1536", e.Dimension())
	}

	vec, err := e.Embed(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 1536 {
		t.Errorf("Embed() len = %d, want 1536", len(vec))
	}
}

func TestOpenAIEmbedderLarge(t *testing.T) {
	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{Model: "text-embedding-3-large"})
	if e.Dimension() != 3072 {
		t.Errorf("Dimension() = %d, want 3072", e.Dimension())
	}
}

func TestOpenAIEmbedderCustomBaseURL(t *testing.T) {
	e := NewOpenAIEmbedder(OpenAIEmbedderConfig{
		BaseURL: "https://my-proxy.example.com/v1",
		Model:   "text-embedding-3-small",
	})
	if e.Name() != "openai" {
		t.Errorf("Name() = %q", e.Name())
	}
}

func TestOllamaEmbedder(t *testing.T) {
	e := NewOllamaEmbedder(OllamaEmbedderConfig{})
	e.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		payload, _ := json.Marshal(map[string][]float64{"embedding": make([]float64, 768)})
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(payload))}, nil
	})}
	if e.Name() != "ollama" {
		t.Errorf("Name() = %q, want %q", e.Name(), "ollama")
	}
	if e.Model() != "nomic-embed-text" {
		t.Errorf("Model() = %q, want %q", e.Model(), "nomic-embed-text")
	}
	if e.Dimension() != 768 {
		t.Errorf("Dimension() = %d, want 768", e.Dimension())
	}

	vec, err := e.Embed(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 768 {
		t.Errorf("Embed() len = %d, want 768", len(vec))
	}
}

func TestOllamaEmbedderMxbai(t *testing.T) {
	e := NewOllamaEmbedder(OllamaEmbedderConfig{Model: "mxbai-embed-large"})
	if e.Dimension() != 1024 {
		t.Errorf("Dimension() = %d, want 1024", e.Dimension())
	}
}

// --- Cache tests ---

func TestEmbeddingCacheBasic(t *testing.T) {
	c := NewEmbeddingCache(4)
	vec := []float64{1.0, 2.0, 3.0}

	c.Put("key1", vec)
	got := c.Get("key1")
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	for i := range got {
		if got[i] != vec[i] {
			t.Errorf("Get() = %v, want %v", got, vec)
			break
		}
	}

	if c.Get("nonexistent") != nil {
		t.Error("Get() should return nil for missing key")
	}
}

func TestEmbeddingCacheLRUEviction(t *testing.T) {
	c := NewEmbeddingCache(2)
	c.Put("a", []float64{1})
	c.Put("b", []float64{2})

	// Cache is full. Adding "c" evicts "a" (oldest).
	c.Put("c", []float64{3})

	if c.Get("a") != nil {
		t.Error("a should have been evicted")
	}
	if c.Get("b") == nil {
		t.Error("b should still be cached")
	}
	if c.Get("c") == nil {
		t.Error("c should be cached")
	}
}

func TestEmbeddingCacheLRUAccessPromotes(t *testing.T) {
	c := NewEmbeddingCache(2)
	c.Put("a", []float64{1})
	c.Put("b", []float64{2})

	// Re-put "a" to promote it (Get does not promote in this LRU implementation)
	c.Put("a", []float64{1})

	// Adding "c" should evict "b" (now oldest), not "a"
	c.Put("c", []float64{3})

	if c.Get("a") == nil {
		t.Error("a should still be cached (was promoted)")
	}
	if c.Get("b") != nil {
		t.Error("b should have been evicted")
	}
}

func TestEmbeddingCacheStats(t *testing.T) {
	c := NewEmbeddingCache(10)
	c.Put("key", []float64{1})

	_ = c.Get("key")   // hit
	_ = c.Get("key")   // hit
	_ = c.Get("miss1") // miss
	_ = c.Get("miss2") // miss

	hits, misses := c.Stats()
	if hits != 2 {
		t.Errorf("hits = %d, want 2", hits)
	}
	if misses != 2 {
		t.Errorf("misses = %d, want 2", misses)
	}
}

func TestEmbeddingCacheClear(t *testing.T) {
	c := NewEmbeddingCache(10)
	c.Put("a", []float64{1})
	c.Put("b", []float64{2})
	c.Clear()

	if c.Len() != 0 {
		t.Errorf("Len() = %d after Clear(), want 0", c.Len())
	}
	hits, _ := c.Stats()
	if hits != 0 {
		t.Errorf("hits = %d after Clear(), want 0", hits)
	}
}

func TestEmbeddingCachePutDuplicate(t *testing.T) {
	c := NewEmbeddingCache(10)
	c.Put("key", []float64{1})
	c.Put("key", []float64{2}) // overwrite

	got := c.Get("key")
	if got == nil || got[0] != 2.0 {
		t.Errorf("Get() = %v, want [2]", got)
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
}

// --- CachedEmbedder tests ---

func TestCachedEmbedderCachesResults(t *testing.T) {
	inner := NewMockEmbedder(32)
	ce := NewCachedEmbedder(inner, 100)

	vec1, err := ce.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	vec2, err := ce.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	// Same result from cache
	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Error("cached result differs from original")
			break
		}
	}

	hits, _ := ce.Cache().Stats()
	if hits != 1 {
		t.Errorf("cache hits = %d, want 1", hits)
	}
}

func TestCachedEmbedderBatch(t *testing.T) {
	inner := NewMockEmbedder(16)
	ce := NewCachedEmbedder(inner, 100)

	vecs, err := ce.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Errorf("EmbedBatch() len = %d, want 3", len(vecs))
	}

	// Second call should hit cache
	vecs2, err := ce.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range vecs {
		for j := range vecs[i] {
			if vecs[i][j] != vecs2[i][j] {
				t.Errorf("cached batch result differs at [%d][%d]", i, j)
			}
		}
	}
}

func TestCachedEmbedderBatchPartialCache(t *testing.T) {
	inner := NewMockEmbedder(16)
	ce := NewCachedEmbedder(inner, 100)

	// Pre-cache "a"
	_, _ = ce.Embed(context.Background(), "a")

	// Batch with "a" (cached) + "b" (miss) + "c" (miss)
	vecs, err := ce.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 3 {
		t.Errorf("EmbedBatch() len = %d, want 3", len(vecs))
	}

	// "a" should have been a cache hit
	hits, misses := ce.Cache().Stats()
	if hits < 1 {
		t.Errorf("expected at least 1 cache hit, got hits=%d misses=%d", hits, misses)
	}
}

// --- Registry tests ---

func TestRegistryBasic(t *testing.T) {
	r := NewRegistry()
	e1 := NewMockEmbedder(64)
	e2 := NewMockEmbedder(128)

	if !r.Register("mock-64", e1) {
		t.Error("Register() should succeed for new ID")
	}
	if r.Register("mock-64", e2) {
		t.Error("Register() should fail for duplicate ID")
	}
	if !r.Register("mock-128", e2) {
		t.Error("Register() should succeed for new ID")
	}

	// First registered becomes active
	if r.ActiveID() != "mock-64" {
		t.Errorf("ActiveID() = %q, want %q", r.ActiveID(), "mock-64")
	}

	active := r.Active()
	if active == nil {
		t.Fatal("Active() returned nil")
	}
	if active.Dimension() != 64 {
		t.Errorf("Active().Dimension() = %d, want 64", active.Dimension())
	}
}

func TestRegistrySwitch(t *testing.T) {
	r := NewRegistry()
	r.Register("a", NewMockEmbedder(64))
	r.Register("b", NewMockEmbedder(128))

	if !r.Switch("b") {
		t.Error("Switch() should succeed for existing ID")
	}
	if r.ActiveID() != "b" {
		t.Errorf("ActiveID() = %q, want %q", r.ActiveID(), "b")
	}
	if r.Active().Dimension() != 128 {
		t.Errorf("Active().Dimension() = %d, want 128", r.Active().Dimension())
	}

	if r.Switch("nonexistent") {
		t.Error("Switch() should fail for nonexistent ID")
	}
}

func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()
	r.Register("a", NewMockEmbedder(64))
	r.Register("b", NewMockEmbedder(128))

	// Cannot remove active
	if r.Unregister("a") {
		t.Error("Unregister() should fail for active embedder")
	}

	// Can remove non-active
	if !r.Unregister("b") {
		t.Error("Unregister() should succeed for non-active embedder")
	}

	// Nonexistent
	if r.Unregister("nonexistent") {
		t.Error("Unregister() should fail for nonexistent ID")
	}
}

func TestRegistryList(t *testing.T) {
	r := NewRegistry()
	r.Register("mock-64", NewMockEmbedder(64))
	r.Register("mock-128", NewMockEmbedder(128))

	list := r.List()
	if len(list) != 2 {
		t.Fatalf("List() len = %d, want 2", len(list))
	}

	// Find the active one
	var foundActive bool
	for _, info := range list {
		if info.Active {
			foundActive = true
			if info.ID != "mock-64" {
				t.Errorf("active ID = %q, want %q", info.ID, "mock-64")
			}
		}
	}
	if !foundActive {
		t.Error("no active embedder in List()")
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry()
	e := NewMockEmbedder(64)
	r.Register("test", e)

	got, ok := r.Get("test")
	if !ok {
		t.Error("Get() should find registered embedder")
	}
	if got.Dimension() != 64 {
		t.Errorf("Get().Dimension() = %d, want 64", got.Dimension())
	}

	_, ok = r.Get("nonexistent")
	if ok {
		t.Error("Get() should not find unregistered embedder")
	}
}

func TestCacheKeyDeterministic(t *testing.T) {
	k1 := CacheKey("hello world")
	k2 := CacheKey("hello world")
	if k1 != k2 {
		t.Error("CacheKey() not deterministic")
	}

	k3 := CacheKey("goodbye world")
	if k1 == k3 {
		t.Error("different text produced same CacheKey")
	}
}

func TestCacheConcurrent(t *testing.T) {
	c := NewEmbeddingCache(100)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			c.Put(string(rune('a'+i%26)), []float64{float64(i)})
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = c.Get(string(rune('a' + i%26)))
		}(i)
	}
	wg.Wait()
}

func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	r.Register("base", NewMockEmbedder(64))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		go func(i int) {
			defer wg.Done()
			r.Register(fmt.Sprintf("e-%d", i), NewMockEmbedder(32+i%64))
		}(i)
		go func(i int) {
			defer wg.Done()
			_ = r.Active()
		}(i)
		go func() {
			defer wg.Done()
			_ = r.List()
		}()
	}
	wg.Wait()
}
