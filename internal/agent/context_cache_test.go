package agent

import (
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/session"
)

func TestNewContextMessageCacheDefaults(t *testing.T) {
	cache := newContextMessageCache(0)
	if cache.maxEntries != 256 {
		t.Fatalf("expected default maxEntries 256, got %d", cache.maxEntries)
	}
	if cache.ttl != 5*time.Minute {
		t.Fatalf("expected default ttl 5m, got %v", cache.ttl)
	}
}

func TestContextMessageCacheHitRefreshesLRUOrder(t *testing.T) {
	cache := newContextMessageCache(2)
	cache.Set(1, contextCacheEntry{messages: []provider.Message{{Role: "user", Content: "one"}}})
	cache.Set(2, contextCacheEntry{messages: []provider.Message{{Role: "user", Content: "two"}}})

	if _, _, ok := cache.Get(1); !ok {
		t.Fatal("expected key 1 cache hit")
	}

	cache.Set(3, contextCacheEntry{messages: []provider.Message{{Role: "user", Content: "three"}}})

	if _, _, ok := cache.Get(1); !ok {
		t.Fatal("expected key 1 to stay after LRU refresh")
	}
	if _, _, ok := cache.Get(2); ok {
		t.Fatal("expected key 2 to be evicted as oldest")
	}
	stats := cache.Stats()
	if stats.Evictions != 1 {
		t.Fatalf("expected 1 eviction, got %d", stats.Evictions)
	}
}

func TestSessionLastMessageSignatureIgnoresSessionUpdatedAtChanges(t *testing.T) {
	sess := session.NewSession("cache-test", t.TempDir())
	sess.AddProviderMessage(provider.Message{Role: "user", Content: "hello"})
	sig1 := sessionLastMessageSignature(sess)
	if sig1 == "" {
		t.Fatal("expected non-empty signature")
	}

	sess.UpdatedAt = sess.UpdatedAt.Add(10 * time.Minute)
	sess.SetEnv("FOO", "bar")
	sig2 := sessionLastMessageSignature(sess)
	if sig1 != sig2 {
		t.Fatalf("expected same signature after non-message session mutation, got %q vs %q", sig1, sig2)
	}
}

func TestContextPlannerCacheKeyChangesWhenSessionHistoryChanges(t *testing.T) {
	planner := &contextPlanner{
		agent: &Agent{
			contextCache: newContextMessageCache(8),
		},
		options: defaultContextBuildOptions(),
		budget: contextBudget{
			System:     256,
			Memory:     128,
			RAG:        256,
			History:    256,
			ToolResult: 256,
		},
	}
	sess := session.NewSession("cache-test", t.TempDir())
	sess.AddProviderMessage(provider.Message{Role: "user", Content: "hello"})

	key1, ok := planner.cacheKey(sess, "same input")
	if !ok {
		t.Fatal("expected cache key")
	}

	sess.AddProviderMessage(provider.Message{Role: "assistant", Content: "new history"})
	key2, ok := planner.cacheKey(sess, "same input")
	if !ok {
		t.Fatal("expected cache key after session mutation")
	}
	if key1 == key2 {
		t.Fatal("expected cache key to change after session history changes")
	}
}
