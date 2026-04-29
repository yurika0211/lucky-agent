package agent

import (
	"encoding/json"
	"hash/fnv"
	"sync"
	"time"

	"github.com/yurika0211/luckyharness/internal/logger"
	"github.com/yurika0211/luckyharness/internal/provider"
)

/*
contextCacheEntry 表示一次上下文构建结果的缓存条目。
*/
type contextCacheEntry struct {
	messages     []provider.Message
	storedAt     time.Time
	totalTokens  int
	bucketTokens map[string]int
}

/*
contextMessageCache 是上下文消息构建结果的短时缓存。
*/
type contextMessageCache struct {
	mu         sync.RWMutex
	maxEntries int
	ttl        time.Duration
	entries    map[uint64]contextCacheEntry
	order      []uint64
}

/*
newContextMessageCache 创建一个新的上下文消息缓存。
*/
func newContextMessageCache(maxEntries int) *contextMessageCache {
	if maxEntries <= 0 {
		maxEntries = 64
	}
	return &contextMessageCache{
		maxEntries: maxEntries,
		ttl:        30 * time.Second,
		entries:    make(map[uint64]contextCacheEntry, maxEntries),
		order:      make([]uint64, 0, maxEntries),
	}
}

/*
Get 按键读取缓存中的上下文消息结果。
*/
func (c *contextMessageCache) Get(key uint64) ([]provider.Message, contextCacheEntry, bool) {
	if c == nil {
		return nil, contextCacheEntry{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok {
		logger.Debug("context cache miss", "key", key)
		return nil, contextCacheEntry{}, false
	}
	if c.ttl > 0 && time.Since(entry.storedAt) > c.ttl {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		logger.Debug("context cache expired", "key", key)
		return nil, contextCacheEntry{}, false
	}
	out := make([]provider.Message, len(entry.messages))
	copy(out, entry.messages)
	logger.Debug("context cache hit", "key", key, "messages", len(out), "tokens_total", entry.totalTokens)
	return out, entry, true
}

/*
Set 写入一条上下文缓存记录，并在必要时执行淘汰。
*/
func (c *contextMessageCache) Set(key uint64, entry contextCacheEntry) {
	if c == nil {
		return
	}
	cp := make([]provider.Message, len(entry.messages))
	copy(cp, entry.messages)
	entry.messages = cp

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.entries[key]; !ok {
		c.order = append(c.order, key)
	}
	entry.storedAt = time.Now()
	c.entries[key] = entry
	logger.Debug("context cache store", "key", key, "messages", len(cp), "tokens_total", entry.totalTokens)

	if len(c.entries) <= c.maxEntries {
		return
	}

	evictKey := c.order[0]
	c.order = c.order[1:]
	delete(c.entries, evictKey)
}

/*
makeContextCacheKey 为任意可序列化负载生成稳定的缓存键。
*/
func makeContextCacheKey(payload any) uint64 {
	data, _ := json.Marshal(payload)
	h := fnv.New64a()
	_, _ = h.Write(data)
	return h.Sum64()
}
