package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type usageTotals struct {
	PromptTokens          int
	CachedPromptTokens    int
	CacheCreation5MTokens int
	CacheCreation1HTokens int
	CompletionTokens      int
	TotalTokens           int
}

type capturedUsage struct {
	usageTotals
	HasUsage bool
}

type openAIResponse struct {
	Usage *openAIUsage `json:"usage,omitempty"`
}

type openAIUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"prompt_tokens_details,omitempty"`
	InputTokensDetails *struct {
		CachedTokens int `json:"cached_tokens,omitempty"`
	} `json:"input_tokens_details,omitempty"`
	ClaudeCacheCreation5MTokens int `json:"claude_cache_creation_5_m_tokens,omitempty"`
	ClaudeCacheCreation1HTokens int `json:"claude_cache_creation_1_h_tokens,omitempty"`
}

func scanCapturePrefixes(dir string) (map[string]struct{}, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, fmt.Errorf("scan capture dir: %w", err)
	}
	out := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".request.json") {
			prefix := strings.TrimSuffix(filepath.Join(dir, name), ".request.json")
			out[prefix] = struct{}{}
		}
	}
	return out, nil
}

func diffCapturePrefixes(before, after map[string]struct{}) []string {
	var out []string
	for prefix := range after {
		if _, ok := before[prefix]; !ok {
			out = append(out, prefix)
		}
	}
	sort.Strings(out)
	return out
}

func aggregateCaptureUsage(prefixes []string) (usageTotals, []string, int) {
	var totals usageTotals
	var files []string
	missing := 0
	for _, prefix := range prefixes {
		usage, file := readCaptureUsage(prefix)
		if file != "" {
			files = append(files, file)
		}
		if !usage.HasUsage {
			missing++
			continue
		}
		totals.PromptTokens += usage.PromptTokens
		totals.CachedPromptTokens += usage.CachedPromptTokens
		totals.CacheCreation5MTokens += usage.CacheCreation5MTokens
		totals.CacheCreation1HTokens += usage.CacheCreation1HTokens
		totals.CompletionTokens += usage.CompletionTokens
		totals.TotalTokens += usage.TotalTokens
	}
	return totals, files, missing
}

func countCaptureErrors(prefixes []string) int {
	count := 0
	for _, prefix := range prefixes {
		if _, err := os.Stat(prefix + ".error.txt"); err == nil {
			count++
		}
	}
	return count
}

func readCaptureUsage(prefix string) (capturedUsage, string) {
	if usage, ok := readJSONCaptureUsage(prefix + ".response.body.txt"); ok {
		return usage, prefix + ".response.body.txt"
	}
	if usage, ok := readSSECaptureUsage(prefix + ".response.sse.txt"); ok {
		return usage, prefix + ".response.sse.txt"
	}
	return capturedUsage{}, ""
}

func readJSONCaptureUsage(path string) (capturedUsage, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return capturedUsage{}, false
	}
	var resp openAIResponse
	if err := json.Unmarshal(data, &resp); err != nil || resp.Usage == nil {
		return capturedUsage{}, true
	}
	return capturedUsageFromOpenAI(resp.Usage), true
}

func readSSECaptureUsage(path string) (capturedUsage, bool) {
	f, err := os.Open(path)
	if err != nil {
		return capturedUsage{}, false
	}
	defer f.Close()

	var last capturedUsage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var resp openAIResponse
		if err := json.Unmarshal([]byte(payload), &resp); err != nil || resp.Usage == nil {
			continue
		}
		usage := capturedUsageFromOpenAI(resp.Usage)
		if usage.HasUsage {
			last = usage
		}
	}
	return last, true
}

func capturedUsageFromOpenAI(usage *openAIUsage) capturedUsage {
	if usage == nil {
		return capturedUsage{}
	}
	cached := 0
	if usage.PromptTokensDetails != nil && usage.PromptTokensDetails.CachedTokens > cached {
		cached = usage.PromptTokensDetails.CachedTokens
	}
	if usage.InputTokensDetails != nil && usage.InputTokensDetails.CachedTokens > cached {
		cached = usage.InputTokensDetails.CachedTokens
	}
	prompt := usage.PromptTokens
	if prompt == 0 {
		prompt = usage.InputTokens
	}
	completion := usage.CompletionTokens
	if completion == 0 {
		completion = usage.OutputTokens
	}
	total := usage.TotalTokens
	if total == 0 {
		total = prompt + completion
	}
	return capturedUsage{
		usageTotals: usageTotals{
			PromptTokens:          prompt,
			CachedPromptTokens:    cached,
			CacheCreation5MTokens: usage.ClaudeCacheCreation5MTokens,
			CacheCreation1HTokens: usage.ClaudeCacheCreation1HTokens,
			CompletionTokens:      completion,
			TotalTokens:           total,
		},
		HasUsage: prompt > 0 || cached > 0 || completion > 0 || total > 0,
	}
}
