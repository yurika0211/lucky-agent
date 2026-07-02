package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	searchpkg "github.com/yurika0211/luckyagent/internal/tool/search"
	"github.com/yurika0211/luckyagent/internal/utils"
)

var currentTimeHTTPClient = &http.Client{Timeout: 8 * time.Second}
var currentTimeAPIBaseURL = "https://worldtimeapi.org/api/timezone"

type WebSearchConfig struct {
	Provider   string
	APIKey     string
	BaseURL    string
	MaxResults int
	Proxy      string
}

const (
	defaultWebSearchCount   = 5
	maxWebSearchCount       = 10
	defaultWebFetchMaxChars = 50000
	maxWebFetchChars        = 100000
	defaultWebSearchFormat  = "text"
	defaultWebFetchFormat   = "text"
	webSearchTimeout        = 8 * time.Second
)

type webSearchOptions struct {
	Query   string
	Count   int
	Mode    string
	Format  string
	Verbose bool
}

type webFetchOptions struct {
	URL      string
	MaxChars int
	Format   string
	Verbose  bool
}

func defaultWebSearchConfig() *WebSearchConfig {
	return &WebSearchConfig{Provider: "brave", MaxResults: defaultWebSearchCount}
}

func WebSearchTool(cfg *WebSearchConfig) *Tool {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	return &Tool{
		Name:        "web_search",
		Description: "Search the web when you need external or recent information, candidate sources, or multiple viewpoints before fetching a specific page. Use mode='deep' when cross-source validation matters.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"query":   {Type: "string", Description: "Search query phrased around the actual fact, identifier, or concept you need to verify.", Required: true},
			"count":   {Type: "number", Description: "Number of results to return (1-10). Use smaller values when you already know what you are looking for.", Required: false, Default: 5},
			"mode":    {Type: "string", Description: "Search mode: 'quick' for fast single-path lookup, 'deep' for multi-source cross-validation and merged evidence.", Required: false, Default: "quick"},
			"format":  {Type: "string", Description: "Return format: text or json.", Required: false, Default: "text"},
			"verbose": {Type: "boolean", Description: "Include engine attempts and failure diagnostics.", Required: false, Default: false},
		},
		Handler:      func(args map[string]any) (string, error) { return handleWebSearch(cfg, args) },
		ParallelSafe: true,
	}
}

func handleWebSearch(cfg *WebSearchConfig, args map[string]any) (string, error) {
	opts, err := parseWebSearchOptions(cfg, args)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "brave"
	}
	if opts.Mode == "deep" {
		return handleDeepSearchWithOptions(cfg, opts, provider)
	}

	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	searchCtx, cancel := context.WithTimeout(context.Background(), webSearchTimeout)
	defer cancel()
	result := manager.QuickSearchDiagnostics(searchCtx, opts.Query, opts.Count)
	result.Results = sanitizeSearchResults(result.Results, opts.Count)
	if opts.Format == "json" {
		return formatWebSearchJSON(opts, provider, result)
	}
	if len(result.Results) == 0 {
		return formatQuickSearchFailure(opts.Query, result, opts.Verbose), nil
	}
	out := formatEntries(opts.Query, toSearchEntries(result.Results), opts.Count)
	label := ""
	if len(result.Results) > 0 {
		label = sourceDisplayName(result.Results[0].Source)
	}
	if label == "" {
		label = sourceDisplayName(provider)
	}
	if label != "" {
		out = annotateSource(out, label)
	}
	if opts.Verbose {
		out += formatSearchAttempts(result)
	}
	return out, nil
}

func parseWebSearchOptions(cfg *WebSearchConfig, args map[string]any) (webSearchOptions, error) {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	query, ok := args["query"].(string)
	if !ok {
		return webSearchOptions{}, fmt.Errorf("query is required")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return webSearchOptions{}, fmt.Errorf("query must not be empty")
	}
	count := cfg.MaxResults
	if count <= 0 {
		count = defaultWebSearchCount
	}
	if c, ok := args["count"]; ok {
		switch v := c.(type) {
		case float64:
			count = int(v)
		case float32:
			count = int(v)
		case int:
			count = v
		case json.Number:
			if n, err := strconv.Atoi(v.String()); err == nil {
				count = n
			}
		}
	}
	if count < 1 {
		count = 1
	}
	if count > maxWebSearchCount {
		count = maxWebSearchCount
	}
	mode := "quick"
	if m, ok := args["mode"].(string); ok && strings.TrimSpace(m) != "" {
		mode = strings.ToLower(strings.TrimSpace(m))
	}
	if mode != "quick" && mode != "deep" {
		return webSearchOptions{}, fmt.Errorf("unsupported web_search mode %q (expected quick or deep)", mode)
	}
	format := defaultWebSearchFormat
	if f, ok := args["format"].(string); ok && strings.TrimSpace(f) != "" {
		format = strings.ToLower(strings.TrimSpace(f))
	}
	if format != "text" && format != "json" {
		return webSearchOptions{}, fmt.Errorf("unsupported web_search format %q (expected text or json)", format)
	}
	return webSearchOptions{
		Query:   query,
		Count:   count,
		Mode:    mode,
		Format:  format,
		Verbose: mapBoolArg(args, "verbose", false),
	}, nil
}

func quickSearchOrder(provider string, cfg *WebSearchConfig) []string {
	return webSearchEngineOrder(provider)
}

func handleDeepSearch(cfg *WebSearchConfig, query string, count int, provider string) (string, error) {
	opts := webSearchOptions{Query: strings.TrimSpace(query), Count: count, Mode: "deep", Format: "text"}
	if opts.Count <= 0 {
		opts.Count = defaultWebSearchCount
	}
	return handleDeepSearchWithOptions(cfg, opts, provider)
}

func handleDeepSearchWithOptions(cfg *WebSearchConfig, opts webSearchOptions, provider string) (string, error) {
	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	searchCtx, cancel := context.WithTimeout(context.Background(), webSearchTimeout)
	defer cancel()
	dr, err := manager.DeepSearch(searchCtx, opts.Query, opts.Count)
	if dr != nil {
		dr.Results = sanitizeSearchResults(dr.Results, opts.Count)
	}
	if opts.Format == "json" {
		return formatDeepSearchJSON(opts, provider, dr)
	}
	if err != nil || dr == nil || len(dr.Results) == 0 {
		return formatDeepSearchFailure(opts.Query, dr, opts.Verbose), nil
	}
	out := searchpkg.FormatDeepResults(opts.Query, dr)
	if opts.Verbose {
		out += "\nNext: use web_fetch on selected URLs before quoting or relying on page content.\n"
	}
	return out, nil
}

func buildSearchManagerConfig(cfg *WebSearchConfig, provider string) *searchpkg.SearchConfig {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	}
	if provider == "" {
		provider = "brave"
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("SEARXNG_BASE_URL"))
	}

	sc := searchpkg.DefaultSearchConfig()
	sc.DefaultProvider = provider
	sc.BraveAPIKey = resolveBraveAPIKey(cfg)
	sc.SearXNGBaseURL = baseURL
	sc.ExaAPIKey = resolveExaAPIKey(cfg)
	sc.JinaAPIKey = os.Getenv("JINA_API_KEY")
	sc.MaxResults = cfg.MaxResults
	sc.Proxy = cfg.Proxy
	if sc.MaxResults <= 0 {
		sc.MaxResults = defaultWebSearchCount
	}
	return searchpkg.SearchConfigFromEnv(sc)
}

func sourceDisplayName(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "searxng":
		return "SearXNG"
	case "exa":
		return "Exa"
	case "ddgs":
		return "DDG (ddgs)"
	case "ddg-lite":
		return "DDG Lite"
	case "brave":
		return "Brave"
	default:
		return ""
	}
}

func deepSearchOrder(provider string, cfg *WebSearchConfig) []string {
	return webSearchEngineOrder(provider)
}

type searchEntry struct {
	Title   string
	URL     string
	Snippet string
}

func toSearchEntries(results []searchpkg.SearchResult) []searchEntry {
	entries := make([]searchEntry, 0, len(results))
	for _, r := range results {
		entries = append(entries, searchEntry{Title: r.Title, URL: r.URL, Snippet: r.Snippet})
	}
	return entries
}

func webSearchEngineOrder(provider string) []string {
	base := []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
	provider = strings.ToLower(strings.TrimSpace(provider))
	out := make([]string, 0, len(base))
	for _, name := range base {
		if name == provider {
			out = append(out, name)
			break
		}
	}
	for _, name := range base {
		if name != provider {
			out = append(out, name)
		}
	}
	return out
}

func sanitizeSearchResults(results []searchpkg.SearchResult, count int) []searchpkg.SearchResult {
	if count <= 0 {
		count = defaultWebSearchCount
	}
	out := make([]searchpkg.SearchResult, 0, len(results))
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		r.URL = strings.TrimSpace(r.URL)
		if r.URL == "" {
			continue
		}
		key := utils.NormalizeURL(r.URL)
		if key == "" {
			key = r.URL
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		r.Title = strings.TrimSpace(r.Title)
		if r.Title == "" {
			r.Title = titleFromURL(r.URL)
		}
		r.Snippet = strings.TrimSpace(r.Snippet)
		if len(r.Snippet) > 500 {
			r.Snippet = r.Snippet[:500] + "... (truncated)"
		}
		r.Source = strings.TrimSpace(r.Source)
		out = append(out, r)
		if len(out) >= count {
			break
		}
	}
	return out
}

func titleFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return rawURL
}

func formatQuickSearchFailure(query string, result *searchpkg.QuickSearchResult, verbose bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("No results found for '%s'\n", query))
	if result != nil && len(result.Tried) > 0 {
		b.WriteString("Tried: " + strings.Join(result.Tried, ", ") + "\n")
	}
	if verbose && result != nil && len(result.Errors) > 0 {
		b.WriteString("Errors:\n")
		for _, e := range result.Errors {
			b.WriteString("  - " + e + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatDeepSearchFailure(query string, result *searchpkg.DeepSearchResult, verbose bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("No results found for '%s'\n", query))
	if result != nil && len(result.Sources) > 0 {
		b.WriteString("Sources: " + strings.Join(result.Sources, ", ") + "\n")
	}
	if verbose && result != nil && len(result.Errors) > 0 {
		b.WriteString("Errors:\n")
		for _, e := range result.Errors {
			b.WriteString("  - " + e + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatSearchAttempts(result *searchpkg.QuickSearchResult) string {
	if result == nil || (len(result.Tried) == 0 && len(result.Errors) == 0) {
		return ""
	}
	var b strings.Builder
	b.WriteString("Diagnostics:\n")
	if len(result.Tried) > 0 {
		b.WriteString("Tried: " + strings.Join(result.Tried, ", ") + "\n")
	}
	if len(result.Errors) > 0 {
		b.WriteString("Errors:\n")
		for _, e := range result.Errors {
			b.WriteString("  - " + e + "\n")
		}
	}
	b.WriteString("Next: use web_fetch on selected URLs before quoting or relying on page content.\n")
	return "\n" + b.String()
}

func formatWebSearchJSON(opts webSearchOptions, provider string, result *searchpkg.QuickSearchResult) (string, error) {
	payload := map[string]any{
		"query":    opts.Query,
		"mode":     opts.Mode,
		"provider": provider,
		"results":  result.Results,
		"tried":    result.Tried,
		"errors":   result.Errors,
		"next":     "use web_fetch on selected URLs before quoting or relying on page content",
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func formatDeepSearchJSON(opts webSearchOptions, provider string, result *searchpkg.DeepSearchResult) (string, error) {
	payload := map[string]any{
		"query":    opts.Query,
		"mode":     opts.Mode,
		"provider": provider,
		"results":  []searchpkg.SearchResult{},
		"sources":  []string{},
		"errors":   []string{},
		"next":     "use web_fetch on selected URLs before quoting or relying on page content",
	}
	if result != nil {
		payload["results"] = result.Results
		payload["sources"] = result.Sources
		payload["errors"] = result.Errors
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func resolveBraveAPIKey(cfg *WebSearchConfig) string {
	if cfg != nil && strings.TrimSpace(strings.ToLower(cfg.Provider)) == "brave" && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	if v := os.Getenv("BRAVE_API_KEY"); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	return ""
}

func annotateSource(result, source string) string {
	return strings.Replace(result, "Results for:", "[Source: "+source+"] Results for:", 1)
}

func searchWithBrave(cfg *WebSearchConfig, query string, count int) (string, error) {
	entries, err := searchWithBraveEntries(cfg, query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithBraveEntries(cfg *WebSearchConfig, query string, count int) ([]searchEntry, error) {
	engine := searchpkg.NewBraveEngine(resolveBraveAPIKey(cfg), cfg.Proxy)
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

func resolveExaAPIKey(cfg *WebSearchConfig) string {
	if cfg != nil && strings.TrimSpace(strings.ToLower(cfg.Provider)) == "exa" && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	if v := os.Getenv("LH_SEARCH_EXA_KEY"); v != "" {
		return v
	}
	if v := os.Getenv("EXA_API_KEY"); v != "" {
		return v
	}
	if cfg != nil && strings.TrimSpace(cfg.APIKey) != "" {
		return cfg.APIKey
	}
	return ""
}

func searchWithDDGS(query string, count int) (string, error) {
	entries, err := searchWithDDGSEntries(query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithDDGSEntries(query string, count int) ([]searchEntry, error) {
	engine := searchpkg.NewDDGSEngine()
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

func searchWithDDGLite(query string, count int) (string, error) {
	engine := searchpkg.NewDDGLiteEngine()
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, toSearchEntries(results), count), nil
}

func searchWithSearXNG(cfg *WebSearchConfig, query string, count int) (string, error) {
	entries, err := searchWithSearXNGEntries(cfg, query, count)
	if err != nil {
		return "", err
	}
	return formatEntries(query, entries, count), nil
}

func searchWithSearXNGEntries(cfg *WebSearchConfig, query string, count int) ([]searchEntry, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("SEARXNG_BASE_URL")
	}
	engine := searchpkg.NewSearXNGEngine(baseURL, cfg.Proxy)
	results, err := engine.Search(context.Background(), query, count)
	if err != nil {
		return nil, err
	}
	return toSearchEntries(results), nil
}

func formatEntries(query string, entries []searchEntry, count int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Results for: %s\n\n", query))
	for i, e := range entries {
		if i >= count {
			break
		}
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, e.Title, e.URL))
		if e.Snippet != "" {
			b.WriteString(fmt.Sprintf("   %s\n", e.Snippet))
		}
		b.WriteString("\n")
	}
	result := b.String()
	if len(result) > 8000 {
		result = result[:8000] + "\n... (truncated)"
	}
	return result
}

func parseDDGLiteHTML(html string, count int) string {
	var b strings.Builder
	b.WriteString("Results (DDG Lite):\n\n")

	linkRe := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)
	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

	n := len(links)
	if n > count {
		n = count
	}
	for i := 0; i < n; i++ {
		url := links[i][1]
		title := utils.StripHTMLTags(links[i][2])
		b.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, title, url))
		if i < len(snippets) {
			snippet := utils.StripHTMLTags(snippets[i][1])
			if snippet != "" {
				b.WriteString(fmt.Sprintf("   %s\n", snippet))
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func urlEncode(s string) string { return utils.URLEncode(s) }

func validateFetchURL(rawURL string) error { return searchpkg.ValidateFetchURL(rawURL) }

func WebFetchTool(cfg *WebSearchConfig) *Tool {
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	return &Tool{
		Name:        "web_fetch",
		Description: "Fetch and extract the readable content of a specific URL when you already have a target page and need the actual text, not just search snippets.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermApprove,
		Parameters: map[string]Param{
			"url":       {Type: "string", Description: "Exact URL to fetch and convert into readable text.", Required: true},
			"max_chars": {Type: "number", Description: "Maximum readable text to return. Lower this when you only need a focused excerpt.", Required: false, Default: defaultWebFetchMaxChars},
			"format":    {Type: "string", Description: "Return format: text or json.", Required: false, Default: "text"},
			"verbose":   {Type: "boolean", Description: "Include fetch engine, URL, title, and failure diagnostics.", Required: false, Default: false},
		},
		Handler:      func(args map[string]any) (string, error) { return handleWebFetch(cfg, args) },
		ParallelSafe: true,
	}
}

func handleWebFetch(cfg *WebSearchConfig, args map[string]any) (string, error) {
	opts, err := parseWebFetchOptions(args)
	if err != nil {
		return "", err
	}
	if cfg == nil {
		cfg = defaultWebSearchConfig()
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	fetchCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, attempts, err := manager.FetchURLWithDiagnostics(fetchCtx, opts.URL, opts.MaxChars)
	if opts.Format == "json" {
		return formatWebFetchJSON(opts, result, attempts, err)
	}
	if err != nil || result == nil || strings.TrimSpace(result.Content) == "" {
		return formatWebFetchFailure(opts.URL, attempts, opts.Verbose), nil
	}
	out := formatFetchResult(result, result.Source == "jina")
	out = truncateWebFetchContent(out, opts.MaxChars)
	if opts.Verbose {
		out = formatWebFetchMeta(result, attempts) + "\n\n" + out
	}
	return out, nil
}

func parseWebFetchOptions(args map[string]any) (webFetchOptions, error) {
	fetchURL, ok := args["url"].(string)
	if !ok {
		return webFetchOptions{}, fmt.Errorf("url is required")
	}
	fetchURL = strings.TrimSpace(fetchURL)
	if fetchURL == "" {
		return webFetchOptions{}, fmt.Errorf("url must not be empty")
	}
	if err := validateFetchURL(fetchURL); err != nil {
		return webFetchOptions{}, fmt.Errorf("url validation failed: %w", err)
	}
	maxChars := defaultWebFetchMaxChars
	if mc, ok := args["max_chars"]; ok {
		switch v := mc.(type) {
		case float64:
			maxChars = int(v)
		case float32:
			maxChars = int(v)
		case int:
			maxChars = v
		case json.Number:
			if n, err := strconv.Atoi(v.String()); err == nil {
				maxChars = n
			}
		}
	}
	if maxChars <= 0 {
		maxChars = defaultWebFetchMaxChars
	}
	if maxChars > maxWebFetchChars {
		maxChars = maxWebFetchChars
	}
	format := defaultWebFetchFormat
	if f, ok := args["format"].(string); ok && strings.TrimSpace(f) != "" {
		format = strings.ToLower(strings.TrimSpace(f))
	}
	if format != "text" && format != "json" {
		return webFetchOptions{}, fmt.Errorf("unsupported web_fetch format %q (expected text or json)", format)
	}
	return webFetchOptions{URL: fetchURL, MaxChars: maxChars, Format: format, Verbose: mapBoolArg(args, "verbose", false)}, nil
}

func fetchWithDefuddle(fetchURL string, maxChars int) (string, error) {
	result, err := searchpkg.NewDefuddleEngine().Fetch(context.Background(), fetchURL, maxChars)
	if err != nil {
		return "", err
	}
	return formatFetchResult(result, false), nil
}

func fetchWithJina(cfg *WebSearchConfig, url string, maxChars int) (string, error) {
	engine := searchpkg.NewJinaEngine(os.Getenv("JINA_API_KEY"), cfg.Proxy)
	result, err := engine.Fetch(context.Background(), url, maxChars)
	if err != nil {
		return "", err
	}
	return formatFetchResult(result, true), nil
}

func fetchWithCurl(cfg *WebSearchConfig, url string, maxChars int) (string, error) {
	result, err := searchpkg.NewCurlEngine(cfg.Proxy).Fetch(context.Background(), url, maxChars)
	if err != nil {
		return "", err
	}
	return formatFetchResult(result, false), nil
}

func formatFetchResult(result *searchpkg.FetchResult, includeTitle bool) string {
	if result == nil {
		return ""
	}
	if !includeTitle || strings.TrimSpace(result.Title) == "" {
		return result.Content
	}
	return fmt.Sprintf("# %s\n\n%s", result.Title, result.Content)
}

func formatWebFetchFailure(fetchURL string, attempts []searchpkg.FetchAttempt, verbose bool) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Failed to fetch %s\n", fetchURL))
	if len(attempts) > 0 {
		names := make([]string, 0, len(attempts))
		for _, a := range attempts {
			names = append(names, a.Engine)
		}
		b.WriteString("Tried: " + strings.Join(names, ", ") + "\n")
	}
	if verbose && len(attempts) > 0 {
		b.WriteString("Errors:\n")
		for _, a := range attempts {
			b.WriteString(fmt.Sprintf("  - %s: %s\n", a.Engine, a.Error))
		}
	}
	b.WriteString("If the page requires JavaScript or login, use opencli for browser/session extraction.")
	return strings.TrimRight(b.String(), "\n")
}

func formatWebFetchMeta(result *searchpkg.FetchResult, attempts []searchpkg.FetchAttempt) string {
	var b strings.Builder
	b.WriteString("URL: " + strings.TrimSpace(result.URL) + "\n")
	b.WriteString("Source: " + strings.TrimSpace(result.Source) + "\n")
	if strings.TrimSpace(result.Title) != "" {
		b.WriteString("Title: " + strings.TrimSpace(result.Title) + "\n")
	}
	if len(attempts) > 0 {
		names := make([]string, 0, len(attempts))
		for _, a := range attempts {
			names = append(names, a.Engine)
		}
		b.WriteString("Fallbacks: " + strings.Join(names, ", ") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncateWebFetchContent(content string, maxChars int) string {
	if maxChars > 0 && len(content) > maxChars {
		return content[:maxChars] + "\n... (truncated; increase max_chars to continue)"
	}
	return content
}

func formatWebFetchJSON(opts webFetchOptions, result *searchpkg.FetchResult, attempts []searchpkg.FetchAttempt, fetchErr error) (string, error) {
	payload := map[string]any{
		"url":       opts.URL,
		"max_chars": opts.MaxChars,
		"attempts":  attempts,
	}
	if result != nil {
		content := truncateWebFetchContent(result.Content, opts.MaxChars)
		payload["final_url"] = result.URL
		payload["title"] = result.Title
		payload["source"] = result.Source
		payload["content"] = content
		payload["truncated"] = len(content) != len(result.Content)
	}
	if fetchErr != nil {
		payload["error"] = fetchErr.Error()
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func mapBoolArg(args map[string]any, key string, def bool) bool {
	if args == nil {
		return def
	}
	raw, ok := args[key]
	if !ok {
		return def
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return def
}

func CurrentTimeTool() *Tool {
	return &Tool{
		Name:        "current_time",
		Description: "Get the current date and time. Optionally verify the time over the network for a specific location or timezone.",
		Category:    CatBuiltin,
		Source:      "builtin",
		Permission:  PermAuto,
		Parameters: map[string]Param{
			"location": {Type: "string", Description: "Optional city or region name such as 北京, Shanghai, Tokyo, or New York.", Required: false},
			"timezone": {Type: "string", Description: "Optional IANA timezone such as Asia/Shanghai. Overrides location mapping when provided.", Required: false},
		},
		Handler:      handleCurrentTime,
		ParallelSafe: true,
	}
}

func handleCurrentTime(args map[string]any) (string, error) {
	now := time.Now()
	location, _ := args["location"].(string)
	timezone, _ := args["timezone"].(string)
	location = strings.TrimSpace(location)
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = mapLocationToTimezone(location)
	}
	if timezone == "" {
		return fmt.Sprintf("Current time: %s (%s)", now.Format("2006-01-02 15:04:05"), now.Location()), nil
	}
	localTime, err := timeInTimezone(now, timezone)
	if err != nil {
		return fmt.Sprintf("Current time: %s (%s)", now.Format("2006-01-02 15:04:05"), now.Location()), nil
	}
	networkTime, err := fetchNetworkTimeForTimezone(timezone)
	if err != nil {
		return fmt.Sprintf("Current time: %s (%s, source: local, location: %s)", localTime.Format("2006-01-02 15:04:05"), timezone, fallbackLocationLabel(location, timezone)), nil
	}
	source := "local-verified"
	selected := localTime
	if absDuration(networkTime.Sub(localTime)) >= 2*time.Second {
		source = "network"
		selected = networkTime
	}
	return fmt.Sprintf("Current time: %s (%s, source: %s, location: %s)", selected.Format("2006-01-02 15:04:05"), timezone, source, fallbackLocationLabel(location, timezone)), nil
}

func fallbackLocationLabel(location, timezone string) string {
	if strings.TrimSpace(location) != "" {
		return strings.TrimSpace(location)
	}
	return timezone
}

func timeInTimezone(now time.Time, timezone string) (time.Time, error) {
	loc, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, err
	}
	return now.In(loc), nil
}

type worldTimeAPIResponse struct {
	DateTime string `json:"datetime"`
}

func fetchNetworkTimeForTimezone(timezone string) (time.Time, error) {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		return time.Time{}, fmt.Errorf("timezone is required")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(currentTimeAPIBaseURL, "/")+"/"+timezone, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("create time request: %w", err)
	}
	resp, err := currentTimeHTTPClient.Do(req)
	if err != nil {
		return time.Time{}, fmt.Errorf("fetch network time: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return time.Time{}, fmt.Errorf("network time API returned %d", resp.StatusCode)
	}
	var payload worldTimeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return time.Time{}, fmt.Errorf("decode network time response: %w", err)
	}
	if strings.TrimSpace(payload.DateTime) == "" {
		return time.Time{}, fmt.Errorf("network time response missing datetime")
	}
	parsed, err := time.Parse(time.RFC3339, payload.DateTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse network time: %w", err)
	}
	return parsed, nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func mapLocationToTimezone(location string) string {
	normalized := normalizeLocationKey(location)
	if normalized == "" {
		return ""
	}
	locationToTimezone := map[string]string{
		"beijing": "Asia/Shanghai", "北京": "Asia/Shanghai",
		"shanghai": "Asia/Shanghai", "上海": "Asia/Shanghai",
		"guangzhou": "Asia/Shanghai", "广州": "Asia/Shanghai",
		"shenzhen": "Asia/Shanghai", "深圳": "Asia/Shanghai",
		"hangzhou": "Asia/Shanghai", "杭州": "Asia/Shanghai",
		"chengdu": "Asia/Shanghai", "成都": "Asia/Shanghai",
		"hong kong": "Asia/Hong_Kong", "hongkong": "Asia/Hong_Kong", "香港": "Asia/Hong_Kong",
		"tokyo": "Asia/Tokyo", "东京": "Asia/Tokyo",
		"seoul": "Asia/Seoul", "首尔": "Asia/Seoul",
		"singapore": "Asia/Singapore", "新加坡": "Asia/Singapore",
		"taipei": "Asia/Taipei", "台北": "Asia/Taipei",
		"new york": "America/New_York", "newyork": "America/New_York", "纽约": "America/New_York",
		"los angeles": "America/Los_Angeles", "losangeles": "America/Los_Angeles",
		"san francisco": "America/Los_Angeles", "sanfrancisco": "America/Los_Angeles",
		"london": "Europe/London", "伦敦": "Europe/London",
		"paris": "Europe/Paris", "巴黎": "Europe/Paris",
		"berlin": "Europe/Berlin", "柏林": "Europe/Berlin",
		"sydney": "Australia/Sydney", "悉尼": "Australia/Sydney",
	}
	return locationToTimezone[normalized]
}

func normalizeLocationKey(location string) string {
	location = strings.TrimSpace(strings.ToLower(location))
	if location == "" {
		return ""
	}
	location = strings.ReplaceAll(location, "_", " ")
	location = strings.Join(strings.Fields(location), " ")
	return location
}

func CalculateTool() *Tool {
	return &Tool{
		Name:         "calculate",
		Description:  "Evaluate a small arithmetic expression locally. Useful for quick numeric checks without using a shell or external model call.",
		Category:     CatBuiltin,
		Source:       "builtin",
		Permission:   PermAuto,
		ParallelSafe: true,
		Parameters: map[string]Param{
			"expression": {Type: "string", Description: "Arithmetic expression such as (12.5*8)/3, sqrt(144), max(3,7,2), or 2^10.", Required: true},
		},
		Handler: handleCalculate,
	}
}

func handleCalculate(args map[string]any) (string, error) {
	expression, ok := args["expression"].(string)
	if !ok || strings.TrimSpace(expression) == "" {
		return "", fmt.Errorf("expression is required")
	}
	expr, err := parser.ParseExpr(strings.TrimSpace(expression))
	if err != nil {
		return "", fmt.Errorf("parse expression: %w", err)
	}
	value, err := evalNumericExpr(expr)
	if err != nil {
		return "", err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("expression produced non-finite result")
	}
	return strconv.FormatFloat(value, 'f', -1, 64), nil
}

func evalNumericExpr(expr ast.Expr) (float64, error) {
	switch n := expr.(type) {
	case *ast.BasicLit:
		if n.Kind != token.INT && n.Kind != token.FLOAT {
			return 0, fmt.Errorf("unsupported literal %q", n.Value)
		}
		v, err := strconv.ParseFloat(n.Value, 64)
		if err != nil {
			return 0, fmt.Errorf("parse number %q: %w", n.Value, err)
		}
		return v, nil
	case *ast.ParenExpr:
		return evalNumericExpr(n.X)
	case *ast.UnaryExpr:
		v, err := evalNumericExpr(n.X)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return v, nil
		case token.SUB:
			return -v, nil
		default:
			return 0, fmt.Errorf("unsupported unary operator %s", n.Op)
		}
	case *ast.BinaryExpr:
		left, err := evalNumericExpr(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNumericExpr(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, fmt.Errorf("modulo by zero")
			}
			return math.Mod(left, right), nil
		case token.XOR:
			return math.Pow(left, right), nil
		default:
			return 0, fmt.Errorf("unsupported binary operator %s", n.Op)
		}
	case *ast.CallExpr:
		ident, ok := n.Fun.(*ast.Ident)
		if !ok {
			return 0, fmt.Errorf("unsupported function call")
		}
		args := make([]float64, 0, len(n.Args))
		for _, arg := range n.Args {
			v, err := evalNumericExpr(arg)
			if err != nil {
				return 0, err
			}
			args = append(args, v)
		}
		return evalNumericFunc(ident.Name, args)
	default:
		return 0, fmt.Errorf("unsupported expression type %T", expr)
	}
}

func evalNumericFunc(name string, args []float64) (float64, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "sqrt":
		if len(args) != 1 {
			return 0, fmt.Errorf("sqrt expects 1 argument")
		}
		if args[0] < 0 {
			return 0, fmt.Errorf("sqrt of negative number")
		}
		return math.Sqrt(args[0]), nil
	case "abs":
		if len(args) != 1 {
			return 0, fmt.Errorf("abs expects 1 argument")
		}
		return math.Abs(args[0]), nil
	case "ceil":
		if len(args) != 1 {
			return 0, fmt.Errorf("ceil expects 1 argument")
		}
		return math.Ceil(args[0]), nil
	case "floor":
		if len(args) != 1 {
			return 0, fmt.Errorf("floor expects 1 argument")
		}
		return math.Floor(args[0]), nil
	case "round":
		if len(args) != 1 {
			return 0, fmt.Errorf("round expects 1 argument")
		}
		return math.Round(args[0]), nil
	case "min":
		if len(args) == 0 {
			return 0, fmt.Errorf("min expects at least 1 argument")
		}
		v := args[0]
		for _, arg := range args[1:] {
			v = math.Min(v, arg)
		}
		return v, nil
	case "max":
		if len(args) == 0 {
			return 0, fmt.Errorf("max expects at least 1 argument")
		}
		v := args[0]
		for _, arg := range args[1:] {
			v = math.Max(v, arg)
		}
		return v, nil
	case "pow":
		if len(args) != 2 {
			return 0, fmt.Errorf("pow expects 2 arguments")
		}
		return math.Pow(args[0], args[1]), nil
	default:
		return 0, fmt.Errorf("unsupported function %q", name)
	}
}
