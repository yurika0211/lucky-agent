package provider

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ModelInfo 描述一个可用模型
type ModelInfo struct {
	ID            string   `json:"id"`              // 模型 ID (e.g. "gpt-5.4-mini", "claude-sonnet-4-20250514")
	Provider      string   `json:"provider"`        // 所属 provider
	DisplayName   string   `json:"display_name"`    // 显示名称
	Capabilities  []string `json:"capabilities"`    // 能力标签: "chat", "streaming", "tools", "vision"
	ContextWindow int      `json:"context_window"`  // 上下文窗口大小
	CostPer1kIn   float64  `json:"cost_per_1k_in"`  // 输入每 1k token 价格 (USD)
	CostPer1kOut  float64  `json:"cost_per_1k_out"` // 输出每 1k token 价格 (USD)
}

// ModelCatalog 管理可用模型列表
type ModelCatalog struct {
	mu     sync.RWMutex
	models map[string]*ModelInfo // key: model ID
}

// NewModelCatalog 创建模型目录
func NewModelCatalog() *ModelCatalog {
	catalog := &ModelCatalog{
		models: make(map[string]*ModelInfo),
	}
	// 注册默认模型
	catalog.registerDefaults()
	return catalog
}

// registerDefaults 注册已知模型
func (mc *ModelCatalog) registerDefaults() {
	defaults := []ModelInfo{
		// OpenAI
		{ID: "gpt-5.4-mini", Provider: "openai", DisplayName: "gpt-5.4-mini", Capabilities: []string{"chat", "streaming", "tools", "vision"}, ContextWindow: 128000, CostPer1kIn: 0.0025, CostPer1kOut: 0.01},
		{ID: "gpt-5.4-mini-mini", Provider: "openai", DisplayName: "gpt-5.4-mini Mini", Capabilities: []string{"chat", "streaming", "tools"}, ContextWindow: 128000, CostPer1kIn: 0.00015, CostPer1kOut: 0.0006},
		{ID: "gpt-4-turbo", Provider: "openai", DisplayName: "GPT-4 Turbo", Capabilities: []string{"chat", "streaming", "tools", "vision"}, ContextWindow: 128000, CostPer1kIn: 0.01, CostPer1kOut: 0.03},
		{ID: "gpt-3.5-turbo", Provider: "openai", DisplayName: "GPT-3.5 Turbo", Capabilities: []string{"chat", "streaming", "tools"}, ContextWindow: 16385, CostPer1kIn: 0.0005, CostPer1kOut: 0.0015},
		// Anthropic
		{ID: "claude-sonnet-4-20250514", Provider: "anthropic", DisplayName: "Claude Sonnet 4", Capabilities: []string{"chat", "streaming", "tools", "vision"}, ContextWindow: 200000, CostPer1kIn: 0.003, CostPer1kOut: 0.015},
		{ID: "claude-3-5-sonnet-20241022", Provider: "anthropic", DisplayName: "Claude 3.5 Sonnet", Capabilities: []string{"chat", "streaming", "tools", "vision"}, ContextWindow: 200000, CostPer1kIn: 0.003, CostPer1kOut: 0.015},
		{ID: "claude-3-haiku-20240307", Provider: "anthropic", DisplayName: "Claude 3 Haiku", Capabilities: []string{"chat", "streaming", "tools"}, ContextWindow: 200000, CostPer1kIn: 0.00025, CostPer1kOut: 0.00125},
		// Ollama (本地模型，无费用)
		{ID: "llama3", Provider: "ollama", DisplayName: "Llama 3", Capabilities: []string{"chat", "streaming"}, ContextWindow: 8192, CostPer1kIn: 0, CostPer1kOut: 0},
		{ID: "llama3:70b", Provider: "ollama", DisplayName: "Llama 3 70B", Capabilities: []string{"chat", "streaming"}, ContextWindow: 8192, CostPer1kIn: 0, CostPer1kOut: 0},
		{ID: "mistral", Provider: "ollama", DisplayName: "Mistral", Capabilities: []string{"chat", "streaming"}, ContextWindow: 32768, CostPer1kIn: 0, CostPer1kOut: 0},
		{ID: "qwen2", Provider: "ollama", DisplayName: "Qwen 2", Capabilities: []string{"chat", "streaming"}, ContextWindow: 32768, CostPer1kIn: 0, CostPer1kOut: 0},
		// OpenRouter (聚合)
		{ID: "openai/gpt-5.4-mini", Provider: "openrouter", DisplayName: "gpt-5.4-mini (via OpenRouter)", Capabilities: []string{"chat", "streaming", "tools"}, ContextWindow: 128000, CostPer1kIn: 0.005, CostPer1kOut: 0.015},
		{ID: "anthropic/claude-3.5-sonnet", Provider: "openrouter", DisplayName: "Claude 3.5 Sonnet (via OpenRouter)", Capabilities: []string{"chat", "streaming", "tools"}, ContextWindow: 200000, CostPer1kIn: 0.003, CostPer1kOut: 0.015},
		{ID: "google/gemini-pro-1.5", Provider: "openrouter", DisplayName: "Gemini Pro 1.5 (via OpenRouter)", Capabilities: []string{"chat", "streaming", "vision"}, ContextWindow: 1000000, CostPer1kIn: 0.00125, CostPer1kOut: 0.005},
		{ID: "meta-llama/llama-3-70b-instruct", Provider: "openrouter", DisplayName: "Llama 3 70B (via OpenRouter)", Capabilities: []string{"chat", "streaming"}, ContextWindow: 8192, CostPer1kIn: 0.0008, CostPer1kOut: 0.0008},
	}

	for _, m := range defaults {
		mc.models[m.ID] = &m
	}
}

// Register 注册一个模型
func (mc *ModelCatalog) Register(model ModelInfo) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.models[model.ID] = &model
}

// Get 获取模型信息
func (mc *ModelCatalog) Get(id string) (*ModelInfo, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	m, ok := mc.models[id]
	if !ok {
		return nil, fmt.Errorf("model not found: %s", id)
	}
	return m, nil
}

// List 列出所有模型
func (mc *ModelCatalog) List() []ModelInfo {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelInfo, 0, len(mc.models))
	for _, m := range mc.models {
		result = append(result, *m)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Provider != result[j].Provider {
			return result[i].Provider < result[j].Provider
		}
		return result[i].ID < result[j].ID
	})
	return result
}

// ListByProvider 列出指定 provider 的模型
func (mc *ModelCatalog) ListByProvider(provider string) []ModelInfo {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelInfo, 0)
	for _, m := range mc.models {
		if m.Provider == provider {
			result = append(result, *m)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// FindByCapability 按能力筛选模型
func (mc *ModelCatalog) FindByCapability(capability string) []ModelInfo {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := make([]ModelInfo, 0)
	for _, m := range mc.models {
		for _, cap := range m.Capabilities {
			if cap == capability {
				result = append(result, *m)
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// ResolveProvider 根据 model ID 推断应该使用哪个 provider
func (mc *ModelCatalog) ResolveProvider(modelID string) (string, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	m, ok := mc.models[modelID]
	if !ok {
		// 未知模型，尝试从 ID 前缀推断
		switch {
		case strings.HasPrefix(modelID, "gpt-"), strings.HasPrefix(modelID, "o1-"), strings.HasPrefix(modelID, "o3-"):
			return "openai", nil
		case strings.HasPrefix(modelID, "claude-"):
			return "anthropic", nil
		case strings.Contains(modelID, "/"):
			return "openrouter", nil // OpenRouter 使用 "provider/model" 格式
		default:
			return "ollama", nil // 默认尝试 Ollama
		}
	}
	return m.Provider, nil
}
