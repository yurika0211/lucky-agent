// Package search provides a composable web search and fetch engine
// with multi-source fallback, caching, and concurrent deep search.
package search

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/utils"
)

// ---------------------------------------------------------------------------
// SF-1: SearchEngine Interface + Implementations
// ---------------------------------------------------------------------------

// SearchResult is a single search result entry.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
	Source  string // which engine produced this result
}

// SearchEngine is the interface for a search backend.
type SearchEngine interface {
	// Name returns the engine identifier (e.g. "brave", "ddgs").
	Name() string

	// Search performs a search and returns results.
	Search(ctx context.Context, query string, count int) ([]SearchResult, error)
}

// --- BraveEngine ---

// BraveEngine searches via the Brave Search API.
type BraveEngine struct {
	APIKey string
	Proxy  string
}

// NewBraveEngine creates a Brave search engine.
// API key from param, BRAVE_API_KEY env, or empty (will fail on Search).
func NewBraveEngine(apiKey, proxy string) *BraveEngine {
	if apiKey == "" {
		apiKey = os.Getenv("BRAVE_API_KEY")
	}
	return &BraveEngine{APIKey: apiKey, Proxy: proxy}
}

func (e *BraveEngine) Name() string { return "brave" }

func (e *BraveEngine) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if e.APIKey == "" {
		return nil, fmt.Errorf("brave: no API key")
	}
	return searchWithBrave(e.APIKey, e.Proxy, query, count)
}

// --- DDGSEngine ---

// DDGSEngine searches via the ddgs Python package.
type DDGSEngine struct{}

// NewDDGSEngine creates a DDGS search engine.
func NewDDGSEngine() *DDGSEngine { return &DDGSEngine{} }

func (e *DDGSEngine) Name() string { return "ddgs" }

func (e *DDGSEngine) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	return searchWithDDGS(ctx, query, count)
}

// --- DDGLiteEngine ---

// DDGLiteEngine searches via DuckDuckGo Lite HTML.
type DDGLiteEngine struct{}

// NewDDGLiteEngine creates a DDG Lite search engine.
func NewDDGLiteEngine() *DDGLiteEngine { return &DDGLiteEngine{} }

func (e *DDGLiteEngine) Name() string { return "ddg-lite" }

func (e *DDGLiteEngine) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	return searchWithDDGLite(query, count)
}

// --- SearXNGEngine ---

// SearXNGEngine searches via a self-hosted SearXNG instance.
type SearXNGEngine struct {
	BaseURL string
	Proxy   string
}

// NewSearXNGEngine creates a SearXNG search engine.
func NewSearXNGEngine(baseURL, proxy string) *SearXNGEngine {
	if baseURL == "" {
		baseURL = os.Getenv("SEARXNG_BASE_URL")
	}
	return &SearXNGEngine{BaseURL: baseURL, Proxy: proxy}
}

func (e *SearXNGEngine) Name() string { return "searxng" }

func (e *SearXNGEngine) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if e.BaseURL == "" {
		return nil, fmt.Errorf("searxng: no base URL")
	}
	return searchWithSearXNG(e.BaseURL, e.Proxy, query, count)
}

// --- ExaEngine ---

// ExaEngine searches via the Exa AI search API.
type ExaEngine struct {
	APIKey string
}

// NewExaEngine creates an Exa search engine.
func NewExaEngine(apiKey string) *ExaEngine {
	if apiKey == "" {
		apiKey = os.Getenv("EXA_API_KEY")
	}
	return &ExaEngine{APIKey: apiKey}
}

func (e *ExaEngine) Name() string { return "exa" }

func (e *ExaEngine) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if e.APIKey == "" {
		return nil, fmt.Errorf("exa: no API key")
	}
	return searchWithExa(e.APIKey, query, count)
}

// ---------------------------------------------------------------------------
// SF-2: FetchEngine Interface + Implementations
// ---------------------------------------------------------------------------

// FetchResult is the result of fetching a URL.
type FetchResult struct {
	Title   string
	Content string
	URL     string
	Source  string // which engine produced this result
}

// FetchEngine is the interface for a URL content extractor.
type FetchEngine interface {
	// Name returns the engine identifier.
	Name() string

	// Fetch extracts readable content from a URL.
	Fetch(ctx context.Context, rawURL string, maxChars int) (*FetchResult, error)
}

// --- DefuddleEngine ---

// DefuddleEngine extracts content using the Defuddle CLI.
type DefuddleEngine struct{}

// NewDefuddleEngine creates a Defuddle fetch engine.
func NewDefuddleEngine() *DefuddleEngine { return &DefuddleEngine{} }

func (e *DefuddleEngine) Name() string { return "defuddle" }

func (e *DefuddleEngine) Fetch(ctx context.Context, rawURL string, maxChars int) (*FetchResult, error) {
	return fetchWithDefuddle(rawURL, maxChars)
}

// --- JinaEngine ---

// JinaEngine extracts content using the Jina Reader API.
type JinaEngine struct {
	APIKey string
	Proxy  string
}

// NewJinaEngine creates a Jina fetch engine.
func NewJinaEngine(apiKey, proxy string) *JinaEngine {
	if apiKey == "" {
		apiKey = os.Getenv("JINA_API_KEY")
	}
	return &JinaEngine{APIKey: apiKey, Proxy: proxy}
}

func (e *JinaEngine) Name() string { return "jina" }

func (e *JinaEngine) Fetch(ctx context.Context, rawURL string, maxChars int) (*FetchResult, error) {
	return fetchWithJina(e.APIKey, e.Proxy, rawURL, maxChars)
}

// --- CurlEngine ---

// CurlEngine extracts content using curl + HTML stripping.
type CurlEngine struct {
	Proxy string
}

// NewCurlEngine creates a curl fetch engine.
func NewCurlEngine(proxy string) *CurlEngine { return &CurlEngine{Proxy: proxy} }

func (e *CurlEngine) Name() string { return "curl" }

func (e *CurlEngine) Fetch(ctx context.Context, rawURL string, maxChars int) (*FetchResult, error) {
	return fetchWithCurl(e.Proxy, rawURL, maxChars)
}

// ---------------------------------------------------------------------------
// SF-3: SearchCache + Concurrent Search
// ---------------------------------------------------------------------------

// SearchCache provides TTL-based caching for search results.
type SearchCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	maxSize int
}

type cacheEntry struct {
	results []SearchResult
	expiry  time.Time
}

// FetchCache provides TTL-based caching for fetched URL content.
type FetchCache struct {
	mu      sync.RWMutex
	entries map[string]*fetchCacheEntry
	ttl     time.Duration
	maxSize int
}

type fetchCacheEntry struct {
	result *FetchResult
	expiry time.Time
}

// NewSearchCache creates a search cache with the given TTL and max size.
func NewSearchCache(ttl time.Duration, maxSize int) *SearchCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 100
	}
	return &SearchCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// NewFetchCache creates a fetch cache with the given TTL and max size.
func NewFetchCache(ttl time.Duration, maxSize int) *FetchCache {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 100
	}
	return &FetchCache{
		entries: make(map[string]*fetchCacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// Get retrieves cached results for a query.
func (c *SearchCache) Get(query string) ([]SearchResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[cacheKey(query)]
	if !ok || time.Now().After(entry.expiry) {
		return nil, false
	}
	return entry.results, true
}

// Set stores search results for a query.
func (c *SearchCache) Set(query string, results []SearchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}

	c.entries[cacheKey(query)] = &cacheEntry{
		results: results,
		expiry:  time.Now().Add(c.ttl),
	}
}

// Clear removes all cached entries.
func (c *SearchCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// Len returns the number of cached entries.
func (c *SearchCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Get retrieves cached fetch results for a URL.
func (c *FetchCache) Get(rawURL string, maxChars int) (*FetchResult, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[fetchCacheKey(rawURL, maxChars)]
	if !ok || time.Now().After(entry.expiry) {
		return nil, false
	}
	if entry.result == nil {
		return nil, false
	}
	copy := *entry.result
	return &copy, true
}

// Set stores fetched content for a URL.
func (c *FetchCache) Set(rawURL string, maxChars int, result *FetchResult) {
	if result == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}
	copy := *result
	c.entries[fetchCacheKey(rawURL, maxChars)] = &fetchCacheEntry{
		result: &copy,
		expiry: time.Now().Add(c.ttl),
	}
}

func (c *FetchCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range c.entries {
		if oldestKey == "" || v.expiry.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.expiry
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func (c *SearchCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range c.entries {
		if oldestKey == "" || v.expiry.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.expiry
		}
	}
	if oldestKey != "" {
		delete(c.entries, oldestKey)
	}
}

func cacheKey(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

func fetchCacheKey(rawURL string, maxChars int) string {
	normalized := utils.NormalizeURL(strings.TrimSpace(rawURL))
	return fmt.Sprintf("%s|%d", normalized, maxChars)
}

// --- Concurrent Deep Search ---

// DeepSearchResult is the result of a deep (multi-source) search.
type DeepSearchResult struct {
	Results []SearchResult
	Sources []string // which sources were used
	Errors  []string // per-source errors
}

// DeepSearch performs concurrent searches across multiple engines and merges results.
func DeepSearch(ctx context.Context, engines []SearchEngine, query string, count int) *DeepSearchResult {
	type engineResult struct {
		engine  string
		results []SearchResult
		err     error
	}

	ch := make(chan engineResult, len(engines))
	var wg sync.WaitGroup

	for _, eng := range engines {
		wg.Add(1)
		go func(e SearchEngine) {
			defer wg.Done()
			results, err := e.Search(ctx, query, count)
			ch <- engineResult{engine: e.Name(), results: results, err: err}
		}(eng)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	result := &DeepSearchResult{}
	for er := range ch {
		if er.err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", er.engine, er.err))
			continue
		}
		result.Sources = append(result.Sources, er.engine)
		for i := range er.results {
			er.results[i].Source = er.engine
		}
		result.Results = append(result.Results, er.results...)
	}

	// Merge by URL, keeping multi-source annotation
	result.Results = mergeResults(result.Results, count)
	return result
}

// mergeResults deduplicates by normalized URL and sorts by source count.
func mergeResults(results []SearchResult, count int) []SearchResult {
	type merged struct {
		result  SearchResult
		sources []string
	}

	seen := make(map[string]*merged)
	var order []string

	for _, r := range results {
		norm := utils.NormalizeURL(r.URL)
		if m, ok := seen[norm]; ok {
			// Add source if not already present
			found := false
			for _, s := range m.sources {
				if s == r.Source {
					found = true
					break
				}
			}
			if !found {
				m.sources = append(m.sources, r.Source)
			}
		} else {
			seen[norm] = &merged{
				result:  r,
				sources: []string{r.Source},
			}
			order = append(order, norm)
		}
	}

	// Sort by number of sources (more sources = higher confidence)
	sort.Slice(order, func(i, j int) bool {
		return len(seen[order[i]].sources) > len(seen[order[j]].sources)
	})

	out := make([]SearchResult, 0, count)
	for i, key := range order {
		if i >= count {
			break
		}
		m := seen[key]
		r := m.result
		r.Source = strings.Join(m.sources, "+")
		out = append(out, r)
	}
	return out
}

// ---------------------------------------------------------------------------
// SF-4: SearchConfig + Environment Variable Override
// ---------------------------------------------------------------------------

// SearchConfig holds all search and fetch configuration.
type SearchConfig struct {
	// Search providers
	DefaultProvider string // brave, ddgs, searxng, exa (default: brave)
	BraveAPIKey     string
	SearXNGBaseURL  string
	ExaAPIKey       string
	JinaAPIKey      string

	// Fetch providers
	PreferredFetch string // defuddle, jina, curl (default: defuddle)

	// General
	MaxResults int
	Proxy      string
	CacheTTL   time.Duration
	CacheSize  int
}

// DefaultSearchConfig returns a config with sensible defaults.
func DefaultSearchConfig() *SearchConfig {
	return &SearchConfig{
		DefaultProvider: "brave",
		PreferredFetch:  "defuddle",
		MaxResults:      5,
		CacheTTL:        10 * time.Minute,
		CacheSize:       100,
	}
}

// SearchConfigFromEnv creates a config with environment variable overrides.
// LH_SEARCH_PROVIDER, LH_SEARCH_BRAVE_KEY, LH_SEARCH_SEARXNG_URL,
// LH_SEARCH_EXA_KEY, LH_SEARCH_JINA_KEY, LH_SEARCH_PROXY,
// LH_SEARCH_MAX_RESULTS, LH_SEARCH_CACHE_TTL_SECONDS
func SearchConfigFromEnv(base *SearchConfig) *SearchConfig {
	if base == nil {
		base = DefaultSearchConfig()
	}

	if v := os.Getenv("LH_SEARCH_PROVIDER"); v != "" {
		base.DefaultProvider = strings.ToLower(v)
	}
	if v := os.Getenv("LH_SEARCH_BRAVE_KEY"); v != "" {
		base.BraveAPIKey = v
	}
	if v := os.Getenv("LH_SEARCH_SEARXNG_URL"); v != "" {
		base.SearXNGBaseURL = v
	}
	if v := os.Getenv("LH_SEARCH_EXA_KEY"); v != "" {
		base.ExaAPIKey = v
	}
	if v := os.Getenv("LH_SEARCH_JINA_KEY"); v != "" {
		base.JinaAPIKey = v
	}
	if v := os.Getenv("LH_SEARCH_PROXY"); v != "" {
		base.Proxy = v
	}
	if v := os.Getenv("LH_SEARCH_MAX_RESULTS"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &base.MaxResults); n == 1 && err == nil {
			// ok
		}
	}
	if v := os.Getenv("LH_SEARCH_CACHE_TTL_SECONDS"); v != "" {
		if secs, err := time.ParseDuration(v + "s"); err == nil {
			base.CacheTTL = secs
		}
	}

	return base
}

// BuildEngines creates the search engine chain from config.
func (c *SearchConfig) BuildEngines() []SearchEngine {
	order := []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
	engines := make([]SearchEngine, 0, len(order))
	seen := make(map[string]bool, len(order))

	for _, name := range order {
		if seen[name] {
			continue
		}
		seen[name] = true
		switch name {
		case "exa":
			if strings.TrimSpace(c.ExaAPIKey) != "" {
				engines = append(engines, NewExaEngine(c.ExaAPIKey))
			}
		case "ddgs":
			engines = append(engines, NewDDGSEngine())
		case "searxng":
			if strings.TrimSpace(c.SearXNGBaseURL) != "" {
				engines = append(engines, NewSearXNGEngine(c.SearXNGBaseURL, c.Proxy))
			}
		case "ddg-lite":
			engines = append(engines, NewDDGLiteEngine())
		case "brave":
			engines = append(engines, NewBraveEngine(c.BraveAPIKey, c.Proxy))
		}
	}

	return engines
}

// BuildFetchEngines creates the fetch engine chain from config.
func (c *SearchConfig) BuildFetchEngines() []FetchEngine {
	var engines []FetchEngine

	switch c.PreferredFetch {
	case "jina":
		engines = append(engines, NewJinaEngine(c.JinaAPIKey, c.Proxy))
	case "curl":
		engines = append(engines, NewCurlEngine(c.Proxy))
	default: // defuddle
		engines = append(engines, NewDefuddleEngine())
	}

	// Add fallbacks
	if c.PreferredFetch != "jina" {
		engines = append(engines, NewJinaEngine(c.JinaAPIKey, c.Proxy))
	}
	if c.PreferredFetch != "curl" {
		engines = append(engines, NewCurlEngine(c.Proxy))
	}

	return engines
}

// --- Manager ---

// Manager orchestrates search and fetch operations.
type Manager struct {
	config        *SearchConfig
	searchEngines []SearchEngine
	fetchEngines  []FetchEngine
	cache         *SearchCache
	fetchCache    *FetchCache
}

// NewManager creates a search manager from config.
func NewManager(cfg *SearchConfig) *Manager {
	if cfg == nil {
		cfg = DefaultSearchConfig()
	}
	return &Manager{
		config:        cfg,
		searchEngines: cfg.BuildEngines(),
		fetchEngines:  cfg.BuildFetchEngines(),
		cache:         NewSearchCache(cfg.CacheTTL, cfg.CacheSize),
		fetchCache:    NewFetchCache(cfg.CacheTTL, cfg.CacheSize),
	}
}

// QuickSearch performs a quick search using the first available engine.
func (m *Manager) QuickSearch(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if count <= 0 {
		count = m.config.MaxResults
	}

	// Check cache first
	if cached, ok := m.cache.Get(query); ok {
		return cached, nil
	}

	// Try engines in order
	for _, eng := range m.searchEngines {
		results, err := eng.Search(ctx, query, count)
		if err == nil && len(results) > 0 {
			m.cache.Set(query, results)
			return results, nil
		}
	}

	return nil, fmt.Errorf("all search engines failed for query: %s", query)
}

// DeepSearch performs a concurrent multi-source search.
func (m *Manager) DeepSearch(ctx context.Context, query string, count int) (*DeepSearchResult, error) {
	if count <= 0 {
		count = m.config.MaxResults
	}
	result := DeepSearch(ctx, m.searchEngines, query, count)
	if len(result.Results) == 0 {
		return result, fmt.Errorf("all search engines failed for query: %s", query)
	}
	return result, nil
}

// FetchURL fetches and extracts content from a URL using the engine chain.
func (m *Manager) FetchURL(ctx context.Context, rawURL string, maxChars int) (*FetchResult, error) {
	if maxChars <= 0 {
		maxChars = 50000
	}

	// Validate URL (SSRF protection)
	if err := ValidateFetchURL(rawURL); err != nil {
		return nil, fmt.Errorf("url validation failed: %w", err)
	}

	if cached, ok := m.fetchCache.Get(rawURL, maxChars); ok {
		return cached, nil
	}

	for _, eng := range m.fetchEngines {
		result, err := eng.Fetch(ctx, rawURL, maxChars)
		if err == nil && result != nil {
			m.fetchCache.Set(rawURL, maxChars, result)
			return result, nil
		}
	}

	return nil, fmt.Errorf("all fetch engines failed for URL: %s", rawURL)
}

// ValidateFetchURL validates a URL for safe fetching (SSRF protection).
func ValidateFetchURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q not allowed (only http/https)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host")
	}

	privateRanges := []struct {
		prefix string
		name   string
	}{
		{"127.", "loopback"},
		{"10.", "private"},
		{"192.168.", "private"},
		{"169.254.", "link-local/metadata"},
		{"0.", "unspecified"},
		{"::1", "loopback"},
		{"fc", "unique-local"},
		{"fd", "unique-local"},
		{"fe80", "link-local"},
	}
	lowerHost := strings.ToLower(host)
	for _, r := range privateRanges {
		if strings.HasPrefix(lowerHost, r.prefix) {
			return fmt.Errorf("host %q is %s address (not allowed)", host, r.name)
		}
	}

	if strings.HasPrefix(host, "172.") {
		parts := strings.SplitN(host, ".", 3)
		if len(parts) >= 2 {
			if second, err := parseUint(parts[1]); err == nil && second >= 16 && second <= 31 {
				return fmt.Errorf("host %q is private address (not allowed)", host)
			}
		}
	}

	if lowerHost == "localhost" {
		return fmt.Errorf("localhost not allowed")
	}

	return nil
}

func parseUint(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// FormatResults formats search results as readable text.
func FormatResults(query string, results []SearchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Results for: %s\n\n", query))
	for i, r := range results {
		b.WriteString(fmt.Sprintf("%d. %s [%s]\n   %s\n", i+1, r.Title, r.Source, r.URL))
		if r.Snippet != "" {
			b.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		b.WriteString("\n")
	}
	result := b.String()
	if len(result) > 12000 {
		result = result[:12000] + "\n... (truncated)"
	}
	return result
}

// FormatDeepResults formats deep search results.
func FormatDeepResults(query string, dr *DeepSearchResult) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Results for: %s (deep search, %d sources)\n\n", query, len(dr.Sources)))
	for i, r := range dr.Results {
		b.WriteString(fmt.Sprintf("%d. %s [%s]\n   %s\n", i+1, r.Title, r.Source, r.URL))
		if r.Snippet != "" {
			b.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		b.WriteString("\n")
	}
	if len(dr.Errors) > 0 {
		b.WriteString("Source errors:\n")
		for _, e := range dr.Errors {
			b.WriteString(fmt.Sprintf("  - %s\n", e))
		}
	}
	result := b.String()
	if len(result) > 12000 {
		result = result[:12000] + "\n... (truncated)"
	}
	return result
}
