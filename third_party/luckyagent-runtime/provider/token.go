package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TokenEntry 存储一个 provider 的 token 信息
type TokenEntry struct {
	Provider     string    `json:"provider"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scope        string    `json:"scope,omitempty"`
}

// IsExpired 检查 token 是否已过期
func (t *TokenEntry) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return false // 无过期时间的 token 永不过期
	}
	return time.Now().After(t.ExpiresAt)
}

// IsExpiringSoon 检查 token 是否即将过期（5 分钟内）
func (t *TokenEntry) IsExpiringSoon() bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(t.ExpiresAt.Add(-5 * time.Minute))
}

// TokenStore 管理 OAuth token 的持久化存储
type TokenStore struct {
	mu     sync.RWMutex
	tokens map[string]*TokenEntry
	path   string
}

// NewTokenStore 创建 token 存储
func NewTokenStore(dir string) (*TokenStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create token dir: %w", err)
	}

	ts := &TokenStore{
		tokens: make(map[string]*TokenEntry),
		path:   filepath.Join(dir, "tokens.json"),
	}

	// 加载已有 token
	if err := ts.load(); err != nil {
		// 文件不存在是正常的
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("load tokens: %w", err)
		}
	}

	return ts, nil
}

// Get 获取指定 provider 的 token
func (ts *TokenStore) Get(provider string) (*TokenEntry, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	entry, ok := ts.tokens[provider]
	if !ok {
		return nil, fmt.Errorf("no token for provider: %s", provider)
	}

	if entry.IsExpired() {
		return nil, fmt.Errorf("token expired for provider: %s (expired at %s)", provider, entry.ExpiresAt.Format(time.RFC3339))
	}

	return entry, nil
}

// Set 保存指定 provider 的 token
func (ts *TokenStore) Set(entry *TokenEntry) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.tokens[entry.Provider] = entry
	return ts.saveLocked()
}

// Delete 删除指定 provider 的 token
func (ts *TokenStore) Delete(provider string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	delete(ts.tokens, provider)
	return ts.saveLocked()
}

// List 列出所有 token（不暴露实际 token 值）
func (ts *TokenStore) List() []TokenEntry {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	result := make([]TokenEntry, 0, len(ts.tokens))
	for _, entry := range ts.tokens {
		// 脱敏
		safe := *entry
		if len(safe.AccessToken) > 8 {
			safe.AccessToken = safe.AccessToken[:8] + "..."
		}
		if len(safe.RefreshToken) > 8 {
			safe.RefreshToken = safe.RefreshToken[:8] + "..."
		}
		result = append(result, safe)
	}
	return result
}

// RefreshIfNeeded 检查并刷新即将过期的 token
// 返回 true 表示 token 已刷新或不需要刷新
func (ts *TokenStore) RefreshIfNeeded(provider string, refreshFn func(refreshToken string) (*TokenEntry, error)) (bool, error) {
	ts.mu.RLock()
	entry, ok := ts.tokens[provider]
	ts.mu.RUnlock()

	if !ok || !entry.IsExpiringSoon() {
		return true, nil // 不需要刷新
	}

	if entry.RefreshToken == "" {
		return false, fmt.Errorf("token expiring soon but no refresh token for provider: %s", provider)
	}

	// 执行刷新
	newEntry, err := refreshFn(entry.RefreshToken)
	if err != nil {
		return false, fmt.Errorf("refresh token for %s: %w", provider, err)
	}

	if err := ts.Set(newEntry); err != nil {
		return false, fmt.Errorf("save refreshed token: %w", err)
	}

	return true, nil
}

// load 从磁盘加载 token
func (ts *TokenStore) load() error {
	data, err := os.ReadFile(ts.path)
	if err != nil {
		return err
	}

	var entries []*TokenEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parse tokens: %w", err)
	}

	for _, entry := range entries {
		ts.tokens[entry.Provider] = entry
	}

	return nil
}

// saveLocked 保存 token 到磁盘（调用者需持有锁）
func (ts *TokenStore) saveLocked() error {
	entries := make([]*TokenEntry, 0, len(ts.tokens))
	for _, entry := range ts.tokens {
		entries = append(entries, entry)
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}

	return os.WriteFile(ts.path, data, 0600)
}
