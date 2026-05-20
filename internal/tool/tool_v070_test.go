//go:build !integration
// +build !integration

package tool

import (
	"os"
	"testing"
)

// v0.70.0: tool 包测试补全 - 覆盖 handleWebSearch 边界情况

// TestHandleWebSearchQueryValidation 测试 handleWebSearch query 参数验证
func TestHandleWebSearchQueryValidation(t *testing.T) {
	cfg := &WebSearchConfig{
		Provider:   "brave",
		MaxResults: 5,
	}

	// 测试 query 缺失
	_, err := handleWebSearch(cfg, map[string]any{})
	if err == nil {
		t.Error("handleWebSearch with missing query: expected error, got nil")
	}

	// 测试 query 类型错误
	_, err = handleWebSearch(cfg, map[string]any{"query": 123})
	if err == nil {
		t.Error("handleWebSearch with non-string query: expected error, got nil")
	}

	// 注意：空字符串 query 在实现中是允许的（会搜索空字符串）
	// 这里只验证函数不会 panic
	if os.Getenv("LH_RUN_LIVE_SEARCH_TESTS") == "1" {
		_, _ = handleWebSearch(cfg, map[string]any{"query": ""})
	}
}

// TestHandleWebSearchCountParameter 测试 handleWebSearch count 参数处理
func TestHandleWebSearchCountParameter(t *testing.T) {
	skipLiveSearchTest(t)
	cfg := &WebSearchConfig{
		Provider:   "brave",
		MaxResults: 5,
	}

	tests := []struct {
		name     string
		count    any
		expected int
	}{
		{"no count", nil, 5}, // 使用默认 MaxResults
		{"count as int", 3, 3},
		{"count as float64", float64(7), 7},
		{"count too small", 0, 1},            // 最小值为 1
		{"count negative", -5, 1},            // 负数也限制为 1
		{"count too large", 15, 10},          // 最大值为 10
		{"count string (invalid)", "abc", 5}, // 无效类型使用默认值
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{"query": "test"}
			if tt.count != nil {
				args["count"] = tt.count
			}

			// 调用函数（可能因缺少 API key 失败，但参数处理逻辑会被执行）
			_, _ = handleWebSearch(cfg, args)
			// 这里无法直接验证 count 值，因为函数返回搜索结果字符串
			// 但测试确保函数不会因参数类型而 panic
		})
	}
}

// TestHandleWebSearchModeParameter 测试 handleWebSearch mode 参数处理
func TestHandleWebSearchModeParameter(t *testing.T) {
	skipLiveSearchTest(t)
	cfg := &WebSearchConfig{
		Provider:   "brave",
		MaxResults: 5,
	}

	tests := []struct {
		name string
		mode string
	}{
		{"mode quick", "quick"},
		{"mode deep", "deep"},
		{"mode DEEP uppercase", "DEEP"},
		{"mode Deep mixed", "Deep"},
		{"mode empty", ""},
		{"mode invalid", "invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"query": "test",
				"mode":  tt.mode,
			}

			// 调用函数（可能因缺少 API key 失败，但参数处理逻辑会被执行）
			_, _ = handleWebSearch(cfg, args)
			// 测试确保函数不会因 mode 参数而 panic
		})
	}
}

// TestHandleWebSearchProviderFallback 测试 handleWebSearch provider 配置
func TestHandleWebSearchProviderFallback(t *testing.T) {
	skipLiveSearchTest(t)
	tests := []struct {
		name     string
		provider string
	}{
		{"provider brave", "brave"},
		{"provider ddgs", "ddgs"},
		{"provider searxng", "searxng"},
		{"provider empty (default)", ""},
		{"provider unknown", "unknown"},
		{"provider with spaces", " brave "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &WebSearchConfig{
				Provider:   tt.provider,
				MaxResults: 5,
			}
			args := map[string]any{"query": "test"}

			// 调用函数（可能因缺少 API key 失败，但参数处理逻辑会被执行）
			_, _ = handleWebSearch(cfg, args)
			// 测试确保函数不会因 provider 配置而 panic
		})
	}
}
