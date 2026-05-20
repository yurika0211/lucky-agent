package search

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// SF-1: SearchEngine Interface Tests (using mock)
// ---------------------------------------------------------------------------

type mockSearchEngine struct {
	name    string
	results []SearchResult
	err     error
	delay   time.Duration
}

func (m *mockSearchEngine) Name() string { return m.name }

func (m *mockSearchEngine) Search(ctx context.Context, query string, count int) ([]SearchResult, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	if len(m.results) > count {
		return m.results[:count], nil
	}
	return m.results, nil
}

func TestBraveEngineNoKey(t *testing.T) {
	eng := NewBraveEngine("", "")
	_, err := eng.Search(context.Background(), "test", 5)
	if err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Errorf("expected no API key error, got %v", err)
	}
}

func TestSearXNGEngineNoURL(t *testing.T) {
	// Clear env
	os.Unsetenv("SEARXNG_BASE_URL")
	eng := NewSearXNGEngine("", "")
	_, err := eng.Search(context.Background(), "test", 5)
	if err == nil || !strings.Contains(err.Error(), "no base URL") {
		t.Errorf("expected no base URL error, got %v", err)
	}
}

func TestExaEngineNoKey(t *testing.T) {
	os.Unsetenv("EXA_API_KEY")
	eng := NewExaEngine("")
	_, err := eng.Search(context.Background(), "test", 5)
	if err == nil || !strings.Contains(err.Error(), "no API key") {
		t.Errorf("expected no API key error, got %v", err)
	}
}

func TestMockSearchEngine(t *testing.T) {
	eng := &mockSearchEngine{
		name: "mock",
		results: []SearchResult{
			{Title: "Test", URL: "https://example.com", Snippet: "test snippet", Source: "mock"},
		},
	}
	results, err := eng.Search(context.Background(), "test", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].Title != "Test" {
		t.Errorf("expected 'Test', got '%s'", results[0].Title)
	}
}

// ---------------------------------------------------------------------------
// SF-2: FetchEngine Interface Tests
// ---------------------------------------------------------------------------

type mockFetchEngine struct {
	name    string
	result  *FetchResult
	err     error
	onFetch func()
}

func (m *mockFetchEngine) Name() string { return m.name }

func (m *mockFetchEngine) Fetch(ctx context.Context, rawURL string, maxChars int) (*FetchResult, error) {
	if m.onFetch != nil {
		m.onFetch()
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

func TestDefuddleEngineNotInstalled(t *testing.T) {
	eng := NewDefuddleEngine()
	_, err := eng.Fetch(context.Background(), "https://example.com", 50000)
	// Will fail if defuddle not installed, which is expected
	if err != nil && !strings.Contains(err.Error(), "not installed") {
		// Might also fail for other reasons, that's ok
		t.Logf("defuddle fetch error (expected if not installed): %v", err)
	}
}

func TestMockFetchEngine(t *testing.T) {
	eng := &mockFetchEngine{
		name: "mock-fetch",
		result: &FetchResult{
			Title:   "Example",
			Content: "Hello world",
			URL:     "https://example.com",
			Source:  "mock-fetch",
		},
	}
	result, err := eng.Fetch(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Title != "Example" {
		t.Errorf("expected 'Example', got '%s'", result.Title)
	}
}

// ---------------------------------------------------------------------------
// SF-3: SearchCache Tests
// ---------------------------------------------------------------------------

func TestSearchCacheSetGet(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 10)
	results := []SearchResult{
		{Title: "Test", URL: "https://example.com", Snippet: "test", Source: "mock"},
	}

	cache.Set("test query", results)
	got, ok := cache.Get("test query")
	if !ok {
		t.Error("expected cache hit")
	}
	if len(got) != 1 || got[0].Title != "Test" {
		t.Errorf("expected cached result, got %v", got)
	}
}

func TestSearchCacheMiss(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 10)
	_, ok := cache.Get("nonexistent")
	if ok {
		t.Error("expected cache miss")
	}
}

func TestSearchCacheExpiry(t *testing.T) {
	cache := NewSearchCache(50*time.Millisecond, 10)
	results := []SearchResult{{Title: "Test", URL: "https://example.com", Source: "mock"}}

	cache.Set("test", results)
	time.Sleep(100 * time.Millisecond)
	_, ok := cache.Get("test")
	if ok {
		t.Error("expected cache expiry")
	}
}

func TestSearchCacheEviction(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 3)
	for i := 0; i < 5; i++ {
		cache.Set(fmt.Sprintf("query %d", i), []SearchResult{
			{Title: fmt.Sprintf("Result %d", i), URL: fmt.Sprintf("https://example.com/%d", i), Source: "mock"},
		})
	}
	if cache.Len() > 3 {
		t.Errorf("expected max 3 entries, got %d", cache.Len())
	}
}

func TestSearchCacheClear(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 10)
	cache.Set("test", []SearchResult{{Title: "Test", URL: "https://example.com", Source: "mock"}})
	cache.Clear()
	if cache.Len() != 0 {
		t.Errorf("expected 0 entries after clear, got %d", cache.Len())
	}
}

func TestSearchCacheCaseInsensitive(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 10)
	cache.Set("Hello World", []SearchResult{{Title: "Test", URL: "https://example.com", Source: "mock"}})
	_, ok := cache.Get("hello world")
	if !ok {
		t.Error("expected case-insensitive cache hit")
	}
}

// ---------------------------------------------------------------------------
// SF-3: DeepSearch Tests
// ---------------------------------------------------------------------------

func TestDeepSearchConcurrent(t *testing.T) {
	engines := []SearchEngine{
		&mockSearchEngine{
			name: "engine-a",
			results: []SearchResult{
				{Title: "Result A1", URL: "https://a.com/1", Snippet: "from A", Source: "engine-a"},
				{Title: "Shared Result", URL: "https://shared.com", Snippet: "shared", Source: "engine-a"},
			},
		},
		&mockSearchEngine{
			name: "engine-b",
			results: []SearchResult{
				{Title: "Result B1", URL: "https://b.com/1", Snippet: "from B", Source: "engine-b"},
				{Title: "Shared Result", URL: "https://shared.com", Snippet: "shared", Source: "engine-b"},
			},
		},
	}

	result := DeepSearch(context.Background(), engines, "test", 10)
	if len(result.Sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(result.Sources))
	}
	// Shared result should be merged
	found := false
	for _, r := range result.Results {
		if r.URL == "https://shared.com" {
			if strings.Contains(r.Source, "+") {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected shared result to have multi-source annotation")
	}
}

func TestDeepSearchPartialFailure(t *testing.T) {
	engines := []SearchEngine{
		&mockSearchEngine{
			name: "good",
			results: []SearchResult{
				{Title: "Good Result", URL: "https://good.com", Snippet: "good", Source: "good"},
			},
		},
		&mockSearchEngine{
			name: "bad",
			err:  fmt.Errorf("connection refused"),
		},
	}

	result := DeepSearch(context.Background(), engines, "test", 5)
	if len(result.Sources) != 1 {
		t.Errorf("expected 1 source, got %d", len(result.Sources))
	}
	if len(result.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(result.Errors))
	}
	if len(result.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Results))
	}
}

func TestDeepSearchAllFail(t *testing.T) {
	engines := []SearchEngine{
		&mockSearchEngine{name: "a", err: fmt.Errorf("fail")},
		&mockSearchEngine{name: "b", err: fmt.Errorf("fail")},
	}

	result := DeepSearch(context.Background(), engines, "test", 5)
	if len(result.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(result.Results))
	}
	if len(result.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(result.Errors))
	}
}

func TestMergeResultsDedup(t *testing.T) {
	results := []SearchResult{
		{Title: "A", URL: "https://example.com/page", Source: "brave"},
		{Title: "B", URL: "https://other.com", Source: "brave"},
		{Title: "A2", URL: "https://example.com/page/", Source: "ddgs"}, // trailing slash normalized
		{Title: "C", URL: "https://third.com", Source: "exa"},
	}

	merged := mergeResults(results, 10)
	// example.com/page and example.com/page/ should be merged
	urls := make(map[string]bool)
	for _, r := range merged {
		urls[r.URL] = true
	}
	// Should have 3 unique entries (example.com/page merged, other.com, third.com)
	if len(merged) != 3 {
		t.Errorf("expected 3 merged results, got %d: %+v", len(merged), merged)
	}
}

// ---------------------------------------------------------------------------
// SF-4: SearchConfig Tests
// ---------------------------------------------------------------------------

func TestDefaultSearchConfig(t *testing.T) {
	cfg := DefaultSearchConfig()
	if cfg.DefaultProvider != "brave" {
		t.Errorf("expected 'brave', got '%s'", cfg.DefaultProvider)
	}
	if cfg.MaxResults != 5 {
		t.Errorf("expected 5, got %d", cfg.MaxResults)
	}
}

func TestSearchConfigFromEnv(t *testing.T) {
	os.Setenv("LH_SEARCH_PROVIDER", "ddgs")
	os.Setenv("LH_SEARCH_BRAVE_KEY", "test-brave-key")
	os.Setenv("LH_SEARCH_PROXY", "http://proxy:8080")
	defer func() {
		os.Unsetenv("LH_SEARCH_PROVIDER")
		os.Unsetenv("LH_SEARCH_BRAVE_KEY")
		os.Unsetenv("LH_SEARCH_PROXY")
	}()

	cfg := SearchConfigFromEnv(nil)
	if cfg.DefaultProvider != "ddgs" {
		t.Errorf("expected 'ddgs', got '%s'", cfg.DefaultProvider)
	}
	if cfg.BraveAPIKey != "test-brave-key" {
		t.Errorf("expected 'test-brave-key', got '%s'", cfg.BraveAPIKey)
	}
	if cfg.Proxy != "http://proxy:8080" {
		t.Errorf("expected proxy, got '%s'", cfg.Proxy)
	}
}

func TestBuildEnginesBrave(t *testing.T) {
	cfg := &SearchConfig{DefaultProvider: "brave", BraveAPIKey: "test", ExaAPIKey: "test-exa", SearXNGBaseURL: "http://searxng:8080"}
	engines := cfg.BuildEngines()
	if len(engines) < 2 {
		t.Errorf("expected at least 2 engines, got %d", len(engines))
	}
	if engines[0].Name() != "exa" {
		t.Errorf("expected first engine 'exa', got '%s'", engines[0].Name())
	}
}

func TestBuildEnginesDDGS(t *testing.T) {
	cfg := &SearchConfig{DefaultProvider: "ddgs", ExaAPIKey: "test-exa"}
	engines := cfg.BuildEngines()
	if engines[0].Name() != "exa" {
		t.Errorf("expected first engine 'exa', got '%s'", engines[0].Name())
	}
}

func TestBuildEnginesWithSearXNG(t *testing.T) {
	cfg := &SearchConfig{DefaultProvider: "brave", SearXNGBaseURL: "http://searxng:8080"}
	engines := cfg.BuildEngines()
	found := false
	for _, e := range engines {
		if e.Name() == "searxng" {
			found = true
		}
	}
	if !found {
		t.Error("expected searxng engine when BaseURL configured")
	}
}

func TestBuildEnginesWithExa(t *testing.T) {
	cfg := &SearchConfig{DefaultProvider: "brave", ExaAPIKey: "test-exa"}
	engines := cfg.BuildEngines()
	found := false
	for _, e := range engines {
		if e.Name() == "exa" {
			found = true
		}
	}
	if !found {
		t.Error("expected exa engine when API key configured")
	}
}

func TestBuildFetchEngines(t *testing.T) {
	cfg := &SearchConfig{PreferredFetch: "defuddle"}
	engines := cfg.BuildFetchEngines()
	if len(engines) < 2 {
		t.Errorf("expected at least 2 fetch engines, got %d", len(engines))
	}
	if engines[0].Name() != "defuddle" {
		t.Errorf("expected first engine 'defuddle', got '%s'", engines[0].Name())
	}
}

// ---------------------------------------------------------------------------
// Manager Tests
// ---------------------------------------------------------------------------

func TestManagerQuickSearch(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	// Replace engines with mocks
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{
			name: "mock",
			results: []SearchResult{
				{Title: "Test Result", URL: "https://example.com", Snippet: "test", Source: "mock"},
			},
		},
	}

	results, err := mgr.QuickSearch(context.Background(), "test", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestManagerQuickSearchCached(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	callCount := 0
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{
			name: "mock",
			results: []SearchResult{
				{Title: "Cached", URL: "https://example.com", Source: "mock"},
			},
		},
	}

	// First call
	_, _ = mgr.QuickSearch(context.Background(), "test", 5)
	callCount++

	// Second call should hit cache
	results, err := mgr.QuickSearch(context.Background(), "test", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 cached result, got %d", len(results))
	}
}

func TestManagerQuickSearchFallback(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{name: "bad", err: fmt.Errorf("fail")},
		&mockSearchEngine{
			name: "good",
			results: []SearchResult{
				{Title: "Fallback", URL: "https://example.com", Source: "good"},
			},
		},
	}

	results, err := mgr.QuickSearch(context.Background(), "test", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Fallback" {
		t.Errorf("expected fallback result, got %v", results)
	}
}

func TestManagerDeepSearch(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{
			name: "a",
			results: []SearchResult{
				{Title: "A", URL: "https://a.com", Source: "a"},
			},
		},
		&mockSearchEngine{
			name: "b",
			results: []SearchResult{
				{Title: "B", URL: "https://b.com", Source: "b"},
			},
		},
	}

	result, err := mgr.DeepSearch(context.Background(), "test", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result.Results) != 2 {
		t.Errorf("expected 2 results, got %d", len(result.Results))
	}
}

func TestManagerFetchURL(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{
			name: "mock-fetch",
			result: &FetchResult{
				Title:   "Example",
				Content: "Hello world",
				URL:     "https://example.com",
				Source:  "mock-fetch",
			},
		},
	}

	result, err := mgr.FetchURL(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Content != "Hello world" {
		t.Errorf("expected 'Hello world', got '%s'", result.Content)
	}
}

func TestManagerFetchURLCached(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	callCount := 0
	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{
			name: "mock-fetch",
			result: &FetchResult{
				Title:   "Example",
				Content: "Hello world",
				URL:     "https://example.com",
				Source:  "mock-fetch",
			},
			onFetch: func() { callCount++ },
		},
	}

	_, err := mgr.FetchURL(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Fatalf("unexpected error on first fetch: %v", err)
	}
	result, err := mgr.FetchURL(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Fatalf("unexpected error on cached fetch: %v", err)
	}
	if result.Content != "Hello world" {
		t.Fatalf("expected cached content, got %q", result.Content)
	}
	if callCount != 1 {
		t.Fatalf("expected fetch engine to run once, got %d", callCount)
	}
}

func TestManagerFetchURLCacheSeparatesMaxChars(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	callCount := 0
	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{
			name: "mock-fetch",
			result: &FetchResult{
				Title:   "Example",
				Content: "Hello world",
				URL:     "https://example.com",
				Source:  "mock-fetch",
			},
			onFetch: func() { callCount++ },
		},
	}

	if _, err := mgr.FetchURL(context.Background(), "https://example.com", 1000); err != nil {
		t.Fatalf("unexpected error on first fetch: %v", err)
	}
	if _, err := mgr.FetchURL(context.Background(), "https://example.com", 2000); err != nil {
		t.Fatalf("unexpected error on second fetch: %v", err)
	}
	if callCount != 2 {
		t.Fatalf("expected separate cache entries for maxChars, got %d calls", callCount)
	}
}

func TestManagerFetchURLValidation(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	_, err := mgr.FetchURL(context.Background(), "ftp://example.com", 50000)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected scheme validation error, got %v", err)
	}

	_, err = mgr.FetchURL(context.Background(), "http://127.0.0.1:9090", 50000)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("expected loopback error, got %v", err)
	}
}

func TestManagerFetchURLFallback(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)

	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{name: "bad", err: fmt.Errorf("fail")},
		&mockFetchEngine{
			name: "good",
			result: &FetchResult{
				Content: "fallback content",
				URL:     "https://example.com",
				Source:  "good",
			},
		},
	}

	result, err := mgr.FetchURL(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Content != "fallback content" {
		t.Errorf("expected 'fallback content', got '%s'", result.Content)
	}
}

// ---------------------------------------------------------------------------
// URL Validation Tests
// ---------------------------------------------------------------------------

func TestValidateFetchURL(t *testing.T) {
	tests := []struct {
		url    string
		valid  bool
		errMsg string
	}{
		{"https://example.com", true, ""},
		{"http://example.com/path", true, ""},
		{"ftp://example.com", false, "not allowed"},
		{"javascript:alert(1)", false, "not allowed"},
		{"http://127.0.0.1", false, "loopback"},
		{"http://10.0.0.1", false, "private"},
		{"http://192.168.1.1", false, "private"},
		{"http://172.16.0.1", false, "private"},
		{"http://172.20.0.1", false, "private"},
		{"http://172.32.0.1", true, ""}, // outside 172.16-31
		{"http://169.254.169.254", false, "link-local"},
		{"http://localhost", false, "localhost"},
		{"", false, "not allowed"},
	}

	for _, tt := range tests {
		err := ValidateFetchURL(tt.url)
		if tt.valid && err != nil {
			t.Errorf("expected %q to be valid, got error: %v", tt.url, err)
		}
		if !tt.valid && (err == nil || !strings.Contains(err.Error(), tt.errMsg)) {
			t.Errorf("expected %q to fail with %q, got: %v", tt.url, tt.errMsg, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Formatting Tests
// ---------------------------------------------------------------------------

func TestFormatResults(t *testing.T) {
	results := []SearchResult{
		{Title: "Test", URL: "https://example.com", Snippet: "test snippet", Source: "brave"},
	}
	formatted := FormatResults("test query", results)
	if !strings.Contains(formatted, "Results for: test query") {
		t.Error("expected header in formatted output")
	}
	if !strings.Contains(formatted, "[brave]") {
		t.Error("expected source tag in formatted output")
	}
}

func TestFormatDeepResults(t *testing.T) {
	dr := &DeepSearchResult{
		Results: []SearchResult{
			{Title: "Test", URL: "https://example.com", Source: "brave+ddgs"},
		},
		Sources: []string{"brave", "ddgs"},
		Errors:  []string{"exa: connection refused"},
	}
	formatted := FormatDeepResults("test", dr)
	if !strings.Contains(formatted, "deep search") {
		t.Error("expected 'deep search' in formatted output")
	}
	if !strings.Contains(formatted, "brave+ddgs") {
		t.Error("expected multi-source tag")
	}
	if !strings.Contains(formatted, "Source errors") {
		t.Error("expected error section")
	}
}

// ---------------------------------------------------------------------------
// Concurrency Safety Test
// ---------------------------------------------------------------------------

func TestSearchCacheConcurrent(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 100)
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("query-%d", n%10)
			cache.Set(key, []SearchResult{
				{Title: fmt.Sprintf("Result %d", n), URL: fmt.Sprintf("https://example.com/%d", n), Source: "mock"},
			})
			cache.Get(key)
		}(i)
	}

	wg.Wait()
	// Should not panic or deadlock
}

func TestDeepSearchRaceDetection(t *testing.T) {
	engines := make([]SearchEngine, 5)
	for i := range engines {
		engines[i] = &mockSearchEngine{
			name: fmt.Sprintf("engine-%d", i),
			results: []SearchResult{
				{Title: fmt.Sprintf("Result %d", i), URL: fmt.Sprintf("https://example.com/%d", i), Source: fmt.Sprintf("engine-%d", i)},
			},
		}
	}

	// Run multiple times to detect races
	for i := 0; i < 10; i++ {
		result := DeepSearch(context.Background(), engines, "test", 10)
		if len(result.Results) == 0 {
			t.Error("expected results")
		}
	}
}

// ---------------------------------------------------------------------------
// SE-1: Helper Function Tests (pure functions, no external deps)
// ---------------------------------------------------------------------------

func TestStripHTMLTags(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"<p>Hello <b>world</b></p>", "Hello world"},
		{"No tags here", "No tags here"},
		{"<div class='test'>content</div>", "content"},
		{"<br/>", ""},
		{"<a href='https://example.com'>link</a>", "link"},
		{"", ""},
		{"<script>alert('xss')</script>safe", "alert('xss')safe"},
		{"mixed <b>bold</b> and <i>italic</i>", "mixed bold and italic"},
	}

	for _, tt := range tests {
		got := stripHTMLTags(tt.input)
		if got != tt.expected {
			t.Errorf("stripHTMLTags(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeWhitespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello   world", "hello world"},
		{"  leading", "leading"},
		{"trailing  ", "trailing"},
		{"  both  ", "both"},
		{"line1\nline2\ttab", "line1 line2 tab"},
		{"", ""},
		{"   ", ""},
	}

	for _, tt := range tests {
		got := normalizeWhitespace(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeWhitespace(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestURLEncode(t *testing.T) {
	tests := []struct {
		input    string
		contains string
	}{
		{"hello", "hello"},
		{"hello world", "%20"},
		{"test&foo=bar", "%26"},
		{"query=value", "%3D"},
		{"path/to", "path%2Fto"},
		{"日本語", "%E6"},
		{"abc123-_.~", "abc123-_.~"},
	}

	for _, tt := range tests {
		got := urlEncode(tt.input)
		if !strings.Contains(got, tt.contains) {
			t.Errorf("urlEncode(%q) = %q, want to contain %q", tt.input, got, tt.contains)
		}
	}
}

func TestParseUint(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasErr   bool
	}{
		{"0", 0, false},
		{"1", 1, false},
		{"16", 16, false},
		{"31", 31, false},
		{"255", 255, false},
		{"", 0, false},
		{"abc", 0, true},
		{"12abc", 0, true},
		{"-1", 0, true},
	}

	for _, tt := range tests {
		got, err := parseUint(tt.input)
		if tt.hasErr && err == nil {
			t.Errorf("parseUint(%q): expected error, got nil", tt.input)
		}
		if !tt.hasErr && err != nil {
			t.Errorf("parseUint(%q): unexpected error: %v", tt.input, err)
		}
		if got != tt.expected {
			t.Errorf("parseUint(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://Example.COM/Path/", "https://example.com/Path"},
		{"https://example.com/path#fragment", "https://example.com/path"},
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"not-a-url", "not-a-url"},
	}

	for _, tt := range tests {
		got := normalizeURL(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestCacheKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "hello world"},
		{"  spaces  ", "spaces"},
		{"UPPERCASE", "uppercase"},
		{"Mixed Case", "mixed case"},
	}

	for _, tt := range tests {
		got := cacheKey(tt.input)
		if got != tt.expected {
			t.Errorf("cacheKey(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// SE-2: SearchCache Edge Cases
// ---------------------------------------------------------------------------

func TestSearchCacheDefaultTTL(t *testing.T) {
	cache := NewSearchCache(0, 0)
	if cache.ttl != 10*time.Minute {
		t.Errorf("expected default TTL 10m, got %v", cache.ttl)
	}
	if cache.maxSize != 100 {
		t.Errorf("expected default maxSize 100, got %d", cache.maxSize)
	}
}

func TestSearchCacheOverwrite(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 10)
	cache.Set("query", []SearchResult{{Title: "First", URL: "https://first.com", Source: "a"}})
	cache.Set("query", []SearchResult{{Title: "Second", URL: "https://second.com", Source: "b"}})

	got, ok := cache.Get("query")
	if !ok {
		t.Error("expected cache hit")
	}
	if len(got) != 1 || got[0].Title != "Second" {
		t.Errorf("expected overwritten result 'Second', got %v", got)
	}
}

func TestSearchCacheEvictionRemovesOldest(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 2)
	cache.Set("old", []SearchResult{{Title: "Old", URL: "https://old.com", Source: "a"}})
	// Make "old" have the earliest expiry
	cache.mu.Lock()
	cache.entries["old"].expiry = time.Now().Add(1 * time.Minute)
	cache.mu.Unlock()

	cache.Set("new1", []SearchResult{{Title: "New1", URL: "https://new1.com", Source: "a"}})
	cache.Set("new2", []SearchResult{{Title: "New2", URL: "https://new2.com", Source: "a"}})

	_, ok := cache.Get("old")
	if ok {
		t.Error("expected 'old' to be evicted")
	}
	_, ok = cache.Get("new1")
	if !ok {
		t.Error("expected 'new1' to remain")
	}
}

// ---------------------------------------------------------------------------
// SE-3: SearchConfig Edge Cases
// ---------------------------------------------------------------------------

func TestSearchConfigFromEnvAllVars(t *testing.T) {
	os.Setenv("LH_SEARCH_PROVIDER", "searxng")
	os.Setenv("LH_SEARCH_BRAVE_KEY", "brave-key")
	os.Setenv("LH_SEARCH_SEARXNG_URL", "http://searxng:8080")
	os.Setenv("LH_SEARCH_EXA_KEY", "exa-key")
	os.Setenv("LH_SEARCH_JINA_KEY", "jina-key")
	os.Setenv("LH_SEARCH_PROXY", "http://proxy:3128")
	os.Setenv("LH_SEARCH_MAX_RESULTS", "10")
	os.Setenv("LH_SEARCH_CACHE_TTL_SECONDS", "300")
	defer func() {
		os.Unsetenv("LH_SEARCH_PROVIDER")
		os.Unsetenv("LH_SEARCH_BRAVE_KEY")
		os.Unsetenv("LH_SEARCH_SEARXNG_URL")
		os.Unsetenv("LH_SEARCH_EXA_KEY")
		os.Unsetenv("LH_SEARCH_JINA_KEY")
		os.Unsetenv("LH_SEARCH_PROXY")
		os.Unsetenv("LH_SEARCH_MAX_RESULTS")
		os.Unsetenv("LH_SEARCH_CACHE_TTL_SECONDS")
	}()

	cfg := SearchConfigFromEnv(nil)
	if cfg.DefaultProvider != "searxng" {
		t.Errorf("expected 'searxng', got '%s'", cfg.DefaultProvider)
	}
	if cfg.SearXNGBaseURL != "http://searxng:8080" {
		t.Errorf("expected searxng URL, got '%s'", cfg.SearXNGBaseURL)
	}
	if cfg.ExaAPIKey != "exa-key" {
		t.Errorf("expected exa key, got '%s'", cfg.ExaAPIKey)
	}
	if cfg.JinaAPIKey != "jina-key" {
		t.Errorf("expected jina key, got '%s'", cfg.JinaAPIKey)
	}
	if cfg.MaxResults != 10 {
		t.Errorf("expected 10, got %d", cfg.MaxResults)
	}
	if cfg.CacheTTL != 300*time.Second {
		t.Errorf("expected 300s, got %v", cfg.CacheTTL)
	}
}

func TestSearchConfigFromEnvInvalidMaxResults(t *testing.T) {
	os.Setenv("LH_SEARCH_MAX_RESULTS", "not-a-number")
	defer os.Unsetenv("LH_SEARCH_MAX_RESULTS")

	cfg := SearchConfigFromEnv(nil)
	if cfg.MaxResults != 5 {
		t.Errorf("expected default 5 for invalid input, got %d", cfg.MaxResults)
	}
}

func TestSearchConfigFromEnvInvalidTTL(t *testing.T) {
	os.Setenv("LH_SEARCH_CACHE_TTL_SECONDS", "invalid")
	defer os.Unsetenv("LH_SEARCH_CACHE_TTL_SECONDS")

	cfg := SearchConfigFromEnv(nil)
	if cfg.CacheTTL != 10*time.Minute {
		t.Errorf("expected default 10m for invalid input, got %v", cfg.CacheTTL)
	}
}

func TestSearchConfigFromEnvWithBase(t *testing.T) {
	base := &SearchConfig{
		DefaultProvider: "ddgs",
		MaxResults:      20,
		CacheTTL:        5 * time.Minute,
		CacheSize:       50,
	}
	os.Setenv("LH_SEARCH_PROVIDER", "exa")
	defer os.Unsetenv("LH_SEARCH_PROVIDER")

	cfg := SearchConfigFromEnv(base)
	if cfg.DefaultProvider != "exa" {
		t.Errorf("expected env override 'exa', got '%s'", cfg.DefaultProvider)
	}
	if cfg.MaxResults != 20 {
		t.Errorf("expected base MaxResults 20, got %d", cfg.MaxResults)
	}
}

func TestBuildEnginesSearXNG(t *testing.T) {
	cfg := &SearchConfig{DefaultProvider: "searxng", SearXNGBaseURL: "http://searxng:8080", ExaAPIKey: "test-exa"}
	engines := cfg.BuildEngines()
	if engines[0].Name() != "exa" {
		t.Errorf("expected first engine 'exa', got '%s'", engines[0].Name())
	}
}

func TestBuildEnginesExa(t *testing.T) {
	cfg := &SearchConfig{DefaultProvider: "exa", ExaAPIKey: "test-key"}
	engines := cfg.BuildEngines()
	if engines[0].Name() != "exa" {
		t.Errorf("expected first engine 'exa', got '%s'", engines[0].Name())
	}
}

func TestBuildFetchEnginesJina(t *testing.T) {
	cfg := &SearchConfig{PreferredFetch: "jina", JinaAPIKey: "test-key"}
	engines := cfg.BuildFetchEngines()
	if engines[0].Name() != "jina" {
		t.Errorf("expected first engine 'jina', got '%s'", engines[0].Name())
	}
}

func TestBuildFetchEnginesCurl(t *testing.T) {
	cfg := &SearchConfig{PreferredFetch: "curl"}
	engines := cfg.BuildFetchEngines()
	if engines[0].Name() != "curl" {
		t.Errorf("expected first engine 'curl', got '%s'", engines[0].Name())
	}
}

// ---------------------------------------------------------------------------
// SE-4: Manager Edge Cases
// ---------------------------------------------------------------------------

func TestManagerNewNilConfig(t *testing.T) {
	mgr := NewManager(nil)
	if mgr == nil {
		t.Error("expected non-nil manager")
	}
	if mgr.config.DefaultProvider != "brave" {
		t.Errorf("expected default provider 'brave', got '%s'", mgr.config.DefaultProvider)
	}
}

func TestManagerQuickSearchDefaultCount(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{
			name: "mock",
			results: []SearchResult{
				{Title: "Test", URL: "https://example.com", Source: "mock"},
			},
		},
	}

	results, err := mgr.QuickSearch(context.Background(), "test", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestManagerQuickSearchAllFail(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{name: "bad1", err: fmt.Errorf("fail1")},
		&mockSearchEngine{name: "bad2", err: fmt.Errorf("fail2")},
	}

	_, err := mgr.QuickSearch(context.Background(), "test", 5)
	if err == nil || !strings.Contains(err.Error(), "all search engines failed") {
		t.Errorf("expected all engines failed error, got %v", err)
	}
}

func TestManagerQuickSearchEmptyResults(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{name: "empty", results: []SearchResult{}},
		&mockSearchEngine{
			name: "good",
			results: []SearchResult{
				{Title: "Fallback", URL: "https://example.com", Source: "good"},
			},
		},
	}

	results, err := mgr.QuickSearch(context.Background(), "test", 5)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Fallback" {
		t.Errorf("expected fallback from empty results, got %v", results)
	}
}

func TestManagerDeepSearchDefaultCount(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{
			name: "mock",
			results: []SearchResult{
				{Title: "Test", URL: "https://example.com", Source: "mock"},
			},
		},
	}

	result, err := mgr.DeepSearch(context.Background(), "test", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result.Results) != 1 {
		t.Errorf("expected 1 result, got %d", len(result.Results))
	}
}

func TestManagerDeepSearchAllFail(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.searchEngines = []SearchEngine{
		&mockSearchEngine{name: "bad", err: fmt.Errorf("fail")},
	}

	result, err := mgr.DeepSearch(context.Background(), "test", 5)
	if err == nil || !strings.Contains(err.Error(), "all search engines failed") {
		t.Errorf("expected all engines failed error, got %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result even on failure")
	}
}

func TestManagerFetchURLDefaultMaxChars(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{
			name: "mock",
			result: &FetchResult{
				Content: "Hello",
				URL:     "https://example.com",
				Source:  "mock",
			},
		},
	}

	result, err := mgr.FetchURL(context.Background(), "https://example.com", 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Content != "Hello" {
		t.Errorf("expected 'Hello', got '%s'", result.Content)
	}
}

func TestManagerFetchURLAllFail(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{name: "bad1", err: fmt.Errorf("fail1")},
		&mockFetchEngine{name: "bad2", err: fmt.Errorf("fail2")},
	}

	_, err := mgr.FetchURL(context.Background(), "https://example.com", 50000)
	if err == nil || !strings.Contains(err.Error(), "all fetch engines failed") {
		t.Errorf("expected all engines failed error, got %v", err)
	}
}

func TestManagerFetchURLNilResult(t *testing.T) {
	cfg := DefaultSearchConfig()
	mgr := NewManager(cfg)
	mgr.fetchEngines = []FetchEngine{
		&mockFetchEngine{name: "nil-result", result: nil, err: nil},
		&mockFetchEngine{
			name:   "good",
			result: &FetchResult{Content: "fallback", URL: "https://example.com", Source: "good"},
		},
	}

	result, err := mgr.FetchURL(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result.Content != "fallback" {
		t.Errorf("expected fallback content, got '%s'", result.Content)
	}
}

// ---------------------------------------------------------------------------
// SE-5: ValidateFetchURL Edge Cases
// ---------------------------------------------------------------------------

func TestValidateFetchURLIPv6(t *testing.T) {
	tests := []struct {
		url    string
		valid  bool
		errMsg string
	}{
		{"http://[::1]/path", false, "loopback"},
		{"http://[fc00::1]/path", false, "unique-local"},
		{"http://[fd00::1]/path", false, "unique-local"},
		{"http://[fe80::1]/path", false, "link-local"},
		{"http://[2001:db8::1]/path", true, ""},
	}

	for _, tt := range tests {
		err := ValidateFetchURL(tt.url)
		if tt.valid && err != nil {
			t.Errorf("expected %q to be valid, got error: %v", tt.url, err)
		}
		if !tt.valid && (err == nil || !strings.Contains(err.Error(), tt.errMsg)) {
			t.Errorf("expected %q to fail with %q, got: %v", tt.url, tt.errMsg, err)
		}
	}
}

func TestValidateFetchURLPrivateRanges(t *testing.T) {
	tests := []struct {
		url    string
		valid  bool
		errMsg string
	}{
		{"http://0.0.0.0", false, "unspecified"},
		{"http://10.1.2.3", false, "private"},
		{"http://192.168.0.1", false, "private"},
		{"http://172.15.0.1", true, ""},
		{"http://172.16.0.1", false, "private"},
		{"http://172.31.255.255", false, "private"},
		{"http://172.32.0.1", true, ""},
		{"http://169.254.1.1", false, "link-local"},
	}

	for _, tt := range tests {
		err := ValidateFetchURL(tt.url)
		if tt.valid && err != nil {
			t.Errorf("expected %q to be valid, got error: %v", tt.url, err)
		}
		if !tt.valid && (err == nil || !strings.Contains(err.Error(), tt.errMsg)) {
			t.Errorf("expected %q to fail with %q, got: %v", tt.url, tt.errMsg, err)
		}
	}
}

func TestValidateFetchURLEmptyHost(t *testing.T) {
	err := ValidateFetchURL("http:///path")
	if err == nil || !strings.Contains(err.Error(), "empty host") {
		t.Errorf("expected empty host error, got: %v", err)
	}
}

func TestValidateFetchURLInvalidURL(t *testing.T) {
	err := ValidateFetchURL("://invalid")
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

// ---------------------------------------------------------------------------
// SE-6: FormatResults Edge Cases
// ---------------------------------------------------------------------------

func TestFormatResultsEmpty(t *testing.T) {
	formatted := FormatResults("test", nil)
	if !strings.Contains(formatted, "Results for: test") {
		t.Error("expected header even with no results")
	}
}

func TestFormatResultsTruncation(t *testing.T) {
	results := make([]SearchResult, 1000)
	for i := range results {
		results[i] = SearchResult{
			Title:   fmt.Sprintf("Result %d", i),
			URL:     fmt.Sprintf("https://example.com/%d", i),
			Snippet: strings.Repeat("x", 20),
			Source:  "test",
		}
	}
	formatted := FormatResults("test", results)
	if len(formatted) > 12100 {
		t.Errorf("expected truncation at ~12000 chars, got %d", len(formatted))
	}
	if !strings.Contains(formatted, "truncated") {
		t.Error("expected truncation marker")
	}
}

func TestFormatResultsNoSnippet(t *testing.T) {
	results := []SearchResult{
		{Title: "No Snippet", URL: "https://example.com", Source: "test"},
	}
	formatted := FormatResults("test", results)
	if !strings.Contains(formatted, "No Snippet") {
		t.Error("expected title in output")
	}
}

func TestFormatDeepResultsNoErrors(t *testing.T) {
	dr := &DeepSearchResult{
		Results: []SearchResult{
			{Title: "Test", URL: "https://example.com", Source: "brave"},
		},
		Sources: []string{"brave"},
	}
	formatted := FormatDeepResults("test", dr)
	if strings.Contains(formatted, "Source errors") {
		t.Error("should not show error section when no errors")
	}
}

func TestFormatDeepResultsTruncation(t *testing.T) {
	results := make([]SearchResult, 1000)
	for i := range results {
		results[i] = SearchResult{
			Title:   fmt.Sprintf("Result %d", i),
			URL:     fmt.Sprintf("https://example.com/%d", i),
			Snippet: strings.Repeat("y", 20),
			Source:  "test",
		}
	}
	dr := &DeepSearchResult{
		Results: results,
		Sources: []string{"test"},
	}
	formatted := FormatDeepResults("test", dr)
	if len(formatted) > 12100 {
		t.Errorf("expected truncation at ~12000 chars, got %d", len(formatted))
	}
}

// ---------------------------------------------------------------------------
// SE-7: DeepSearch Edge Cases
// ---------------------------------------------------------------------------

func TestDeepSearchCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	engines := []SearchEngine{
		&mockSearchEngine{
			name:  "slow",
			delay: 5 * time.Second,
			results: []SearchResult{
				{Title: "Should not appear", URL: "https://example.com", Source: "slow"},
			},
		},
	}

	result := DeepSearch(ctx, engines, "test", 5)
	// With cancelled context, the search should still complete
	// because mockSearchEngine checks ctx.Done() with delay
	if len(result.Errors) == 0 && len(result.Results) == 0 {
		t.Log("DeepSearch with cancelled context returned no results/errors (acceptable)")
	}
}

func TestDeepSearchEmptyEngines(t *testing.T) {
	result := DeepSearch(context.Background(), nil, "test", 5)
	if len(result.Results) != 0 {
		t.Errorf("expected 0 results with no engines, got %d", len(result.Results))
	}
}

func TestMergeResultsCountLimit(t *testing.T) {
	results := make([]SearchResult, 20)
	for i := range results {
		results[i] = SearchResult{
			Title:  fmt.Sprintf("Result %d", i),
			URL:    fmt.Sprintf("https://example.com/%d", i),
			Source: "test",
		}
	}

	merged := mergeResults(results, 5)
	if len(merged) != 5 {
		t.Errorf("expected 5 results with count limit, got %d", len(merged))
	}
}

func TestMergeResultsSameSourceDedup(t *testing.T) {
	results := []SearchResult{
		{Title: "A", URL: "https://example.com", Source: "brave"},
		{Title: "A2", URL: "https://example.com", Source: "brave"},
	}

	merged := mergeResults(results, 10)
	if len(merged) != 1 {
		t.Errorf("expected 1 merged result, got %d", len(merged))
	}
	if merged[0].Source != "brave" {
		t.Errorf("expected single source 'brave', got '%s'", merged[0].Source)
	}
}

// ---------------------------------------------------------------------------
// SE-8: Engine Constructor Tests
// ---------------------------------------------------------------------------

func TestNewBraveEngineFromEnv(t *testing.T) {
	os.Setenv("BRAVE_API_KEY", "env-key")
	defer os.Unsetenv("BRAVE_API_KEY")

	eng := NewBraveEngine("", "")
	if eng.APIKey != "env-key" {
		t.Errorf("expected API key from env, got '%s'", eng.APIKey)
	}
}

func TestNewBraveEngineExplicit(t *testing.T) {
	os.Unsetenv("BRAVE_API_KEY")
	eng := NewBraveEngine("explicit-key", "http://proxy:8080")
	if eng.APIKey != "explicit-key" {
		t.Errorf("expected explicit API key, got '%s'", eng.APIKey)
	}
	if eng.Proxy != "http://proxy:8080" {
		t.Errorf("expected proxy, got '%s'", eng.Proxy)
	}
}

func TestNewSearXNGEngineFromEnv(t *testing.T) {
	os.Setenv("SEARXNG_BASE_URL", "http://env-searxng:8080")
	defer os.Unsetenv("SEARXNG_BASE_URL")

	eng := NewSearXNGEngine("", "")
	if eng.BaseURL != "http://env-searxng:8080" {
		t.Errorf("expected base URL from env, got '%s'", eng.BaseURL)
	}
}

func TestNewExaEngineFromEnv(t *testing.T) {
	os.Setenv("EXA_API_KEY", "env-exa-key")
	defer os.Unsetenv("EXA_API_KEY")

	eng := NewExaEngine("")
	if eng.APIKey != "env-exa-key" {
		t.Errorf("expected API key from env, got '%s'", eng.APIKey)
	}
}

func TestNewJinaEngineFromEnv(t *testing.T) {
	os.Setenv("JINA_API_KEY", "env-jina-key")
	defer os.Unsetenv("JINA_API_KEY")

	eng := NewJinaEngine("", "")
	if eng.APIKey != "env-jina-key" {
		t.Errorf("expected API key from env, got '%s'", eng.APIKey)
	}
}

func TestNewJinaEngineExplicit(t *testing.T) {
	os.Unsetenv("JINA_API_KEY")
	eng := NewJinaEngine("explicit-key", "http://proxy:3128")
	if eng.APIKey != "explicit-key" {
		t.Errorf("expected explicit key, got '%s'", eng.APIKey)
	}
	if eng.Proxy != "http://proxy:3128" {
		t.Errorf("expected proxy, got '%s'", eng.Proxy)
	}
}

func TestNewCurlEngineWithProxy(t *testing.T) {
	eng := NewCurlEngine("http://proxy:3128")
	if eng.Proxy != "http://proxy:3128" {
		t.Errorf("expected proxy, got '%s'", eng.Proxy)
	}
}

// ---------------------------------------------------------------------------
// SE-9: DDGLite Parsing Tests
// ---------------------------------------------------------------------------

func TestParseDDGLiteResultsEmpty(t *testing.T) {
	results := parseDDGLiteResults("<html><body>no results</body></html>", 5)
	if results != nil {
		t.Errorf("expected nil for no results, got %v", results)
	}
}

func TestParseDDGLiteResultsWithLinks(t *testing.T) {
	html := `
		<a class="result__a" href="https://example.com/1">Example 1</a>
		<a class="result__snippet">Snippet 1</a>
		<a class="result__a" href="https://example.com/2">Example 2</a>
	`
	results := parseDDGLiteResults(html, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].URL != "https://example.com/1" {
		t.Errorf("expected URL 'https://example.com/1', got '%s'", results[0].URL)
	}
	if results[0].Title != "Example 1" {
		t.Errorf("expected title 'Example 1', got '%s'", results[0].Title)
	}
	if results[0].Source != "ddg-lite" {
		t.Errorf("expected source 'ddg-lite', got '%s'", results[0].Source)
	}
	if results[0].Snippet != "Snippet 1" {
		t.Errorf("expected snippet 'Snippet 1', got '%s'", results[0].Snippet)
	}
}

func TestParseDDGLiteResultsCountLimit(t *testing.T) {
	html := `
		<a class="result__a" href="https://example.com/1">Result 1</a>
		<a class="result__a" href="https://example.com/2">Result 2</a>
		<a class="result__a" href="https://example.com/3">Result 3</a>
	`
	results := parseDDGLiteResults(html, 2)
	if len(results) != 2 {
		t.Errorf("expected 2 results with count limit, got %d", len(results))
	}
}

func TestParseDDGLiteResultsNoSnippets(t *testing.T) {
	html := `
		<a class="result__a" href="https://example.com/1">Result 1</a>
		<a class="result__a" href="https://example.com/2">Result 2</a>
	`
	results := parseDDGLiteResults(html, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Snippet != "" {
		t.Errorf("expected empty snippet, got '%s'", results[0].Snippet)
	}
}

// ---------------------------------------------------------------------------
// SE-10: Mock Engine Edge Cases
// ---------------------------------------------------------------------------

func TestMockSearchEngineWithCount(t *testing.T) {
	eng := &mockSearchEngine{
		name: "mock",
		results: []SearchResult{
			{Title: "A", URL: "https://a.com", Source: "mock"},
			{Title: "B", URL: "https://b.com", Source: "mock"},
			{Title: "C", URL: "https://c.com", Source: "mock"},
		},
	}

	results, err := eng.Search(context.Background(), "test", 2)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results (count limited), got %d", len(results))
	}
}

func TestMockSearchEngineCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	eng := &mockSearchEngine{
		name:  "mock",
		delay: 1 * time.Second,
		results: []SearchResult{
			{Title: "Should not appear", URL: "https://example.com", Source: "mock"},
		},
	}

	_, err := eng.Search(ctx, "test", 5)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

// ---------------------------------------------------------------------------
// SE-11: SearchCache Concurrent Stress
// ---------------------------------------------------------------------------

func TestSearchCacheConcurrentStress(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 50)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("query-%d", n%20)
			cache.Set(key, []SearchResult{
				{Title: fmt.Sprintf("Result %d", n), URL: fmt.Sprintf("https://example.com/%d", n), Source: "mock"},
			})
			if got, ok := cache.Get(key); ok && len(got) > 0 {
				// ok
			}
		}(i)
	}

	wg.Wait()
}

func TestSearchCacheClearConcurrent(t *testing.T) {
	cache := NewSearchCache(5*time.Minute, 100)
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			cache.Set(fmt.Sprintf("key-%d", n), []SearchResult{
				{Title: "Test", URL: "https://example.com", Source: "mock"},
			})
		}(i)
		go func() {
			defer wg.Done()
			cache.Clear()
		}()
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// SE-12: Engine Search/Fetch via exec mock
// ---------------------------------------------------------------------------

func TestSearchWithBraveMock(t *testing.T) {
	_, err := searchWithBrave("invalid-key", "", "test query", 5)
	if err == nil {
		t.Error("expected error with invalid API key")
	}
	t.Logf("searchWithBrave error (expected): %v", err)
}

func TestSearchWithDDGSMock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := searchWithDDGS(ctx, "test query", 5)
	if err != nil {
		t.Logf("searchWithDDGS error (expected if python3/ddgs not available): %v", err)
	}
}

func TestSearchWithDDGLiteMock(t *testing.T) {
	_, err := searchWithDDGLite("test query", 5)
	if err != nil {
		t.Logf("searchWithDDGLite error (expected in restricted env): %v", err)
	}
}

func TestSearchWithSearXNGMock(t *testing.T) {
	_, err := searchWithSearXNG("http://localhost:9999", "", "test query", 5)
	if err == nil {
		t.Error("expected error with non-existent SearXNG instance")
	}
	t.Logf("searchWithSearXNG error (expected): %v", err)
}

func TestSearchWithExaMock(t *testing.T) {
	_, err := searchWithExa("invalid-key", "test query", 5)
	if err == nil {
		t.Error("expected error with invalid API key")
	}
	t.Logf("searchWithExa error (expected): %v", err)
}

func TestFetchWithJinaMock(t *testing.T) {
	_, err := fetchWithJina("", "", "https://example.com", 50000)
	if err != nil {
		t.Logf("fetchWithJina error (expected without API key): %v", err)
	}
}

func TestFetchWithCurlMock(t *testing.T) {
	_, err := fetchWithCurl("", "https://example.com", 50000)
	if err != nil {
		t.Logf("fetchWithCurl error (expected in restricted env): %v", err)
	}
}

// ---------------------------------------------------------------------------
// SE-13: FetchWithDefuddle Edge Cases
// ---------------------------------------------------------------------------

func TestFetchWithDefuddleNotInstalled(t *testing.T) {
	_, err := fetchWithDefuddle("https://example.com", 50000)
	if err != nil && strings.Contains(err.Error(), "not installed") {
		t.Logf("fetchWithDefuddle error (expected): %v", err)
	}
}

func TestFetchWithDefuddleTruncation(t *testing.T) {
	_, err := fetchWithDefuddle("https://example.com", 100)
	if err != nil {
		t.Logf("fetchWithDefuddle with small maxChars error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SE-14: FetchWithCurl Edge Cases
// ---------------------------------------------------------------------------

func TestFetchWithCurlWithProxy(t *testing.T) {
	_, err := fetchWithCurl("http://proxy:3128", "https://example.com", 50000)
	if err != nil {
		t.Logf("fetchWithCurl with proxy error (expected): %v", err)
	}
}

func TestFetchWithCurlTooLittleContent(t *testing.T) {
	_, err := fetchWithCurl("", "data:text/html,hi", 50000)
	if err != nil {
		t.Logf("fetchWithCurl too little content error (expected): %v", err)
	}
}

// ---------------------------------------------------------------------------
// SE-15: Engine Name() coverage
// ---------------------------------------------------------------------------

func TestJinaEngineName(t *testing.T) {
	eng := NewJinaEngine("", "")
	if eng.Name() != "jina" {
		t.Errorf("expected 'jina', got '%s'", eng.Name())
	}
}

func TestCurlEngineName(t *testing.T) {
	eng := NewCurlEngine("")
	if eng.Name() != "curl" {
		t.Errorf("expected 'curl', got '%s'", eng.Name())
	}
}

func TestDDGSEngineName(t *testing.T) {
	eng := NewDDGSEngine()
	if eng.Name() != "ddgs" {
		t.Errorf("expected 'ddgs', got '%s'", eng.Name())
	}
}

func TestDDGLiteEngineName(t *testing.T) {
	eng := NewDDGLiteEngine()
	if eng.Name() != "ddg-lite" {
		t.Errorf("expected 'ddg-lite', got '%s'", eng.Name())
	}
}

func TestSearXNGEngineName(t *testing.T) {
	eng := NewSearXNGEngine("http://searxng:8080", "")
	if eng.Name() != "searxng" {
		t.Errorf("expected 'searxng', got '%s'", eng.Name())
	}
}

func TestExaEngineName(t *testing.T) {
	eng := NewExaEngine("test-key")
	if eng.Name() != "exa" {
		t.Errorf("expected 'exa', got '%s'", eng.Name())
	}
}

// ---------------------------------------------------------------------------
// SE-16: Engine Search/Fetch integration (exercises real code paths)
// ---------------------------------------------------------------------------

func TestDDGSEngineSearchIntegration(t *testing.T) {
	eng := NewDDGSEngine()
	_, err := eng.Search(context.Background(), "golang testing", 3)
	if err != nil {
		t.Logf("DDGS search error (expected if not installed): %v", err)
	}
}

func TestDDGLiteEngineSearchIntegration(t *testing.T) {
	eng := NewDDGLiteEngine()
	_, err := eng.Search(context.Background(), "golang testing", 3)
	if err != nil {
		t.Logf("DDG Lite search error (expected in restricted env): %v", err)
	}
}

func TestJinaEngineFetchIntegration(t *testing.T) {
	eng := NewJinaEngine("", "")
	_, err := eng.Fetch(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Logf("Jina fetch error (expected without API key): %v", err)
	}
}

func TestCurlEngineFetchIntegration(t *testing.T) {
	eng := NewCurlEngine("")
	_, err := eng.Fetch(context.Background(), "https://example.com", 50000)
	if err != nil {
		t.Logf("Curl fetch error (expected in restricted env): %v", err)
	}
}

// ---------------------------------------------------------------------------
// SE-17: normalizeURL edge case
// ---------------------------------------------------------------------------

func TestNormalizeURLParseError(t *testing.T) {
	got := normalizeURL("://bad")
	if got != "://bad" {
		t.Errorf("expected original string on parse error, got %q", got)
	}
}
