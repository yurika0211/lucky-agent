// Package search — engine implementations (curl-based, no external Go deps).
package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/utils"
)

// ---------------------------------------------------------------------------
// Brave Search API
// ---------------------------------------------------------------------------

func searchWithBrave(apiKey, proxy, query string, count int) ([]SearchResult, error) {
	reqURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		urlEncode(query), count)

	args := []string{"-s", "-L", reqURL,
		"-H", "Accept: application/json",
		"-H", "X-Subscription-Token: " + apiKey,
		"--max-time", "10",
	}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	cmd := exec.Command("curl", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("brave api request failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("brave: empty response")
	}

	var resp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("brave: parse response failed: %w", err)
	}

	if len(resp.Web.Results) == 0 {
		return nil, fmt.Errorf("brave: no results")
	}

	results := make([]SearchResult, 0, len(resp.Web.Results))
	for _, r := range resp.Web.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Description, Source: "brave"})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// ddgs Python Package
// ---------------------------------------------------------------------------

func searchWithDDGS(ctx context.Context, query string, count int) ([]SearchResult, error) {
	script := fmt.Sprintf(
		`import json; from ddgs import DDGS; ddgs=DDGS(timeout=10); results=ddgs.text(%q, max_results=%d); print(json.dumps(results, ensure_ascii=False))`,
		query, count,
	)

	cmd := exec.CommandContext(ctx, "python3", "-c", script)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ddgs search failed: %w", err)
	}

	output := stdout.String()
	if output == "" || output == "[]" {
		return nil, fmt.Errorf("ddgs returned empty results")
	}

	var rawResults []map[string]any
	if err := json.Unmarshal([]byte(output), &rawResults); err != nil {
		return nil, fmt.Errorf("ddgs: parse failed: %w", err)
	}

	if len(rawResults) == 0 {
		return nil, fmt.Errorf("ddgs returned empty results")
	}

	results := make([]SearchResult, 0, len(rawResults))
	for _, r := range rawResults {
		title, _ := r["title"].(string)
		href, _ := r["href"].(string)
		body, _ := r["body"].(string)
		results = append(results, SearchResult{Title: title, URL: href, Snippet: body, Source: "ddgs"})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// DDG Lite (HTML scraping)
// ---------------------------------------------------------------------------

func searchWithDDGLite(query string, count int) ([]SearchResult, error) {
	cmd := exec.Command("curl", "-s", "-L",
		"https://lite.duckduckgo.com/lite/",
		"-d", "q="+urlEncode(query),
		"-d", "kl=cn-zh",
		"--max-time", "10",
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("curl ddg lite failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("ddg lite returned empty response")
	}

	// Detect captcha
	if strings.Contains(output, "challenge-form") ||
		strings.Contains(output, "anomaly-modal") ||
		strings.Contains(output, "confirm this search was made by a human") {
		return nil, fmt.Errorf("ddg lite returned captcha/challenge page")
	}

	return parseDDGLiteResults(output, count), nil
}

func parseDDGLiteResults(html string, count int) []SearchResult {
	linkRe := regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	snippetRe := regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>(.*?)</a>`)

	links := linkRe.FindAllStringSubmatch(html, -1)
	snippets := snippetRe.FindAllStringSubmatch(html, -1)

	n := len(links)
	if n > count {
		n = count
	}

	results := make([]SearchResult, 0, n)
	for i := 0; i < n; i++ {
		r := SearchResult{
			URL:    links[i][1],
			Title:  utils.StripHTMLTags(links[i][2]),
			Source: "ddg-lite",
		}
		if i < len(snippets) {
			r.Snippet = utils.StripHTMLTags(snippets[i][1])
		}
		results = append(results, r)
	}

	if len(results) == 0 {
		return nil
	}
	return results
}

// ---------------------------------------------------------------------------
// SearXNG
// ---------------------------------------------------------------------------

func searchWithSearXNG(baseURL, proxy, query string, count int) ([]SearchResult, error) {
	reqURL := fmt.Sprintf("%s/search?q=%s&format=json&limit=%d",
		strings.TrimRight(baseURL, "/"), urlEncode(query), count)

	args := []string{"-s", "-L", reqURL,
		"-H", "User-Agent: RightClaw/1.0",
		"--max-time", "10",
	}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	cmd := exec.Command("curl", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("searxng request failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("searxng: empty response")
	}

	var resp struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("searxng: parse response failed: %w", err)
	}

	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("searxng: no results")
	}

	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content, Source: "searxng"})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Exa AI Search
// ---------------------------------------------------------------------------

func searchWithExa(apiKey, query string, count int) ([]SearchResult, error) {
	payload, _ := json.Marshal(map[string]any{
		"query":      query,
		"numResults": count,
		"type":       "auto",
		"contents": map[string]any{
			"text": map[string]any{
				"maxCharacters": 200,
			},
		},
	})

	cmd := exec.Command("curl", "-s", "-L",
		"https://api.exa.ai/search",
		"-H", "Content-Type: application/json",
		"-H", "x-api-key: "+apiKey,
		"-d", string(payload),
		"--max-time", "15",
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("exa search failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("exa: empty response")
	}

	var resp struct {
		Results []struct {
			Title string  `json:"title"`
			URL   string  `json:"url"`
			Text  string  `json:"text"`
			Score float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("exa: parse response failed: %w", err)
	}

	if len(resp.Results) == 0 {
		return nil, fmt.Errorf("exa: no results")
	}

	results := make([]SearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		results = append(results, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Text, Source: "exa"})
	}
	return results, nil
}

// ---------------------------------------------------------------------------
// Fetch: Defuddle CLI
// ---------------------------------------------------------------------------

func fetchWithDefuddle(rawURL string, maxChars int) (*FetchResult, error) {
	// Check if defuddle is available
	checkCmd := exec.Command("which", "defuddle")
	if err := checkCmd.Run(); err != nil {
		return nil, fmt.Errorf("defuddle not installed")
	}

	cmd := exec.Command("defuddle", "parse", rawURL, "--md")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("defuddle parse failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("defuddle: empty result")
	}

	if len(output) > maxChars {
		output = output[:maxChars] + "\n... (truncated)"
	}

	return &FetchResult{
		Content: output,
		URL:     rawURL,
		Source:  "defuddle",
	}, nil
}

// ---------------------------------------------------------------------------
// Fetch: Jina Reader
// ---------------------------------------------------------------------------

func fetchWithJina(apiKey, proxy, rawURL string, maxChars int) (*FetchResult, error) {
	args := []string{"-s", "-L",
		"https://r.jina.ai/" + rawURL,
		"-H", "Accept: application/json",
		"-H", "User-Agent: RightClaw/1.0",
		"--max-time", "20",
	}
	if apiKey != "" {
		args = append(args, "-H", "Authorization: Bearer "+apiKey)
	}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	cmd := exec.Command("curl", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("jina fetch failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("jina: empty response")
	}

	var resp struct {
		Data struct {
			Title   string `json:"title"`
			Content string `json:"content"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		return nil, fmt.Errorf("jina: parse failed: %w", err)
	}

	if resp.Data.Content == "" {
		return nil, fmt.Errorf("jina: no content extracted")
	}

	content := resp.Data.Content
	if len(content) > maxChars {
		content = content[:maxChars] + "\n... (truncated)"
	}

	return &FetchResult{
		Title:   resp.Data.Title,
		Content: content,
		URL:     resp.Data.URL,
		Source:  "jina",
	}, nil
}

// ---------------------------------------------------------------------------
// Fetch: curl + strip HTML
// ---------------------------------------------------------------------------

func fetchWithCurl(proxy, rawURL string, maxChars int) (*FetchResult, error) {
	args := []string{"-s", "-L", rawURL,
		"-H", "User-Agent: Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7_2) AppleWebKit/537.36",
		"--max-time", "15",
	}
	if proxy != "" {
		args = append(args, "--proxy", proxy)
	}

	cmd := exec.Command("curl", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("curl fetch failed: %w", err)
	}

	output := stdout.String()
	if output == "" {
		return nil, fmt.Errorf("curl: empty response")
	}

	text := utils.StripHTMLTags(output)
	text = utils.NormalizeWhitespace(text)

	if len(text) < 50 {
		return nil, fmt.Errorf("curl: too little content extracted")
	}

	if len(text) > maxChars {
		text = text[:maxChars] + "\n... (truncated)"
	}

	return &FetchResult{
		Content: text,
		URL:     rawURL,
		Source:  "curl",
	}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func urlEncode(s string) string {
	return utils.URLEncode(s)
}

// Ensure unused import hint
var _ = time.Second
