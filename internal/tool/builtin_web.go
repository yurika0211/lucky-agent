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

func defaultWebSearchConfig() *WebSearchConfig {
	return &WebSearchConfig{Provider: "brave", MaxResults: 5}
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
			"query": {Type: "string", Description: "Search query phrased around the actual fact, identifier, or concept you need to verify.", Required: true},
			"count": {Type: "number", Description: "Number of results to return (1-10). Use smaller values when you already know what you are looking for.", Required: false, Default: 5},
			"mode":  {Type: "string", Description: "Search mode: 'quick' for fast single-path lookup, 'deep' for multi-source cross-validation and merged evidence.", Required: false, Default: "quick"},
		},
		Handler:      func(args map[string]any) (string, error) { return handleWebSearch(cfg, args) },
		ParallelSafe: true,
	}
}

func handleWebSearch(cfg *WebSearchConfig, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok {
		return "", fmt.Errorf("query is required")
	}
	count := cfg.MaxResults
	if count <= 0 {
		count = 5
	}
	if c, ok := args["count"]; ok {
		switch v := c.(type) {
		case float64:
			count = int(v)
		case int:
			count = v
		}
	}
	if count < 1 {
		count = 1
	}
	if count > 10 {
		count = 10
	}

	mode := "quick"
	if m, ok := args["mode"].(string); ok {
		mode = strings.ToLower(m)
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = "brave"
	}
	if mode == "deep" {
		return handleDeepSearch(cfg, query, count, provider)
	}

	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	searchCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	results, err := manager.QuickSearch(searchCtx, query, count)
	if err != nil || len(results) == 0 {
		return fmt.Sprintf("No results found for '%s' (all search sources failed)", query), nil
	}

	out := formatEntries(query, toSearchEntries(results), count)
	label := ""
	if len(results) > 0 {
		label = sourceDisplayName(results[0].Source)
	}
	if label == "" {
		label = sourceDisplayName(provider)
	}
	if label != "" {
		out = annotateSource(out, label)
	}
	return out, nil
}

func quickSearchOrder(provider string, cfg *WebSearchConfig) []string {
	return []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
}

func handleDeepSearch(cfg *WebSearchConfig, query string, count int, provider string) (string, error) {
	manager := searchpkg.NewManager(buildSearchManagerConfig(cfg, provider))
	searchCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dr, err := manager.DeepSearch(searchCtx, query, count)
	if err != nil || dr == nil || len(dr.Results) == 0 {
		return fmt.Sprintf("No results found for '%s' (all search sources failed)", query), nil
	}
	return searchpkg.FormatDeepResults(query, dr), nil
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

	sc := &searchpkg.SearchConfig{
		DefaultProvider: provider,
		BraveAPIKey:     resolveBraveAPIKey(cfg),
		SearXNGBaseURL:  baseURL,
		ExaAPIKey:       resolveExaAPIKey(cfg),
		JinaAPIKey:      os.Getenv("JINA_API_KEY"),
		MaxResults:      cfg.MaxResults,
		Proxy:           cfg.Proxy,
		CacheTTL:        10 * time.Minute,
		CacheSize:       100,
	}
	if sc.MaxResults <= 0 {
		sc.MaxResults = 5
	}
	return sc
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
	order := []string{"exa", "ddgs", "searxng", "ddg-lite", "brave"}
	if resolveExaAPIKey(cfg) == "" {
		order = []string{"ddgs", "searxng", "ddg-lite", "brave"}
	}
	if cfg == nil || strings.TrimSpace(cfg.BaseURL) == "" {
		filtered := make([]string, 0, len(order))
		for _, name := range order {
			if name == "searxng" {
				continue
			}
			filtered = append(filtered, name)
		}
		return filtered
	}
	return order
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
			"max_chars": {Type: "number", Description: "Maximum readable text to return. Lower this when you only need a focused excerpt.", Required: false, Default: 50000},
		},
		Handler:      func(args map[string]any) (string, error) { return handleWebFetch(cfg, args) },
		ParallelSafe: true,
	}
}

func handleWebFetch(cfg *WebSearchConfig, args map[string]any) (string, error) {
	fetchURL, ok := args["url"].(string)
	if !ok {
		return "", fmt.Errorf("url is required")
	}
	if err := validateFetchURL(fetchURL); err != nil {
		return "", fmt.Errorf("url validation failed: %w", err)
	}
	maxChars := 50000
	if mc, ok := args["max_chars"]; ok {
		switch v := mc.(type) {
		case float64:
			maxChars = int(v)
		case int:
			maxChars = v
		}
	}
	if result, err := fetchWithDefuddle(fetchURL, maxChars); err == nil && result != "" {
		return result, nil
	}
	if result, err := fetchWithJina(cfg, fetchURL, maxChars); err == nil && result != "" {
		return result, nil
	}
	if result, err := fetchWithCurl(cfg, fetchURL, maxChars); err == nil && result != "" {
		return result, nil
	}
	return fmt.Sprintf("Failed to fetch %s (all methods failed)", fetchURL), nil
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
