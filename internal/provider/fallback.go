package provider

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// FallbackConfig 定义降级链中一个节点的配置
type FallbackConfig struct {
	Name    string // provider name (openai, anthropic, ollama, openrouter, openai-compatible)
	APIKey  string
	APIBase string
	Model   string
}

// FallbackChain 实现 Provider 自动降级链
// primary → fallback → local (Ollama)
// 当 primary 失败时自动切换到下一个 provider
type FallbackChain struct {
	chain      []Provider
	mu         sync.RWMutex
	active     int // 当前活跃的 provider 索引
	failCounts []int
	maxFails   int           // 连续失败多少次后降级
	cooldown   time.Duration // 降级冷却时间
	cooldownAt []time.Time   // 每个 provider 的冷却截止时间
	onSwitch   func(from, to string)
}

// NewFallbackChain 创建降级链
func NewFallbackChain(configs []FallbackConfig, registry *Registry) (*FallbackChain, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("fallback chain requires at least one provider config")
	}

	chain := make([]Provider, 0, len(configs))
	failCounts := make([]int, len(configs))
	cooldownAt := make([]time.Time, len(configs))

	for _, fc := range configs {
		pCfg := Config{
			LlmProvider: LlmProvider{
				Name:    fc.Name,
				BaseURL: fc.APIBase,
				Model:   fc.Model,
				APIKey:  fc.APIKey,
			},
		}
		p, err := registry.Resolve(pCfg)
		if err != nil {
			return nil, fmt.Errorf("resolve provider %s: %w", fc.Name, err)
		}
		chain = append(chain, p)
	}

	return &FallbackChain{
		chain:      chain,
		failCounts: failCounts,
		cooldownAt: cooldownAt,
		maxFails:   3,
		cooldown:   5 * time.Minute,
	}, nil
}

// Name 返回当前活跃 provider 的名称
func (fc *FallbackChain) Name() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.chain[fc.active].Name()
}

// ActiveProvider 返回当前活跃的 Provider
func (fc *FallbackChain) ActiveProvider() Provider {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.chain[fc.active]
}

// ActiveIndex 返回当前活跃 provider 的索引
func (fc *FallbackChain) ActiveIndex() int {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.active
}

// ChainLen 返回降级链长度
func (fc *FallbackChain) ChainLen() int {
	return len(fc.chain)
}

// ChainNames 返回降级链中所有 provider 名称
func (fc *FallbackChain) ChainNames() []string {
	names := make([]string, len(fc.chain))
	for i, p := range fc.chain {
		names[i] = p.Name()
	}
	return names
}

// SetOnSwitch 设置降级切换回调
func (fc *FallbackChain) SetOnSwitch(fn func(from, to string)) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.onSwitch = fn
}

// Chat 发送消息，自动降级
func (fc *FallbackChain) Chat(ctx context.Context, messages []Message) (*Response, error) {
	startIdx := fc.nextAvailable()
	if startIdx < 0 {
		return nil, fmt.Errorf("all providers in fallback chain are unavailable")
	}

	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		resp, err := fc.chain[i].Chat(ctx, messages)
		if err != nil {
			fc.recordFailure(i, err)
			log.Printf("[fallback] provider %s failed: %v, trying next", fc.chain[i].Name(), err)
			continue
		}

		// 成功：重置失败计数，如果不在 active 位置则切换
		fc.recordSuccess(i)
		return resp, nil
	}

	return nil, fmt.Errorf("all %d providers failed in fallback chain", len(fc.chain))
}

// ChatStream 流式发送消息，自动降级
func (fc *FallbackChain) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	startIdx := fc.nextAvailable()
	if startIdx < 0 {
		return nil, fmt.Errorf("all providers in fallback chain are unavailable")
	}

	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		ch, err := fc.chain[i].ChatStream(ctx, messages)
		if err != nil {
			fc.recordFailure(i, err)
			log.Printf("[fallback] provider %s stream failed: %v, trying next", fc.chain[i].Name(), err)
			continue
		}

		fc.recordSuccess(i)
		return ch, nil
	}

	return nil, fmt.Errorf("all %d providers failed in fallback chain", len(fc.chain))
}

// Validate 验证降级链中至少有一个可用 provider
func (fc *FallbackChain) Validate() error {
	hasValid := false
	for i, p := range fc.chain {
		if err := p.Validate(); err != nil {
			log.Printf("[fallback] provider %s invalid: %v", p.Name(), err)
			continue
		}
		hasValid = true
		_ = i
	}
	if !hasValid {
		return fmt.Errorf("no valid provider in fallback chain")
	}
	return nil
}

// nextAvailable 找到下一个可用的 provider 索引
func (fc *FallbackChain) nextAvailable() int {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	// 先尝试当前 active
	if fc.isAvailableLocked(fc.active) {
		return fc.active
	}

	// 从头找
	for i := 0; i < len(fc.chain); i++ {
		if fc.isAvailableLocked(i) {
			return i
		}
	}
	return -1
}

// isAvailable 检查指定索引的 provider 是否可用
func (fc *FallbackChain) isAvailable(idx int) bool {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.isAvailableLocked(idx)
}

// isAvailableLocked 不加锁的可用性检查
func (fc *FallbackChain) isAvailableLocked(idx int) bool {
	if idx < 0 || idx >= len(fc.chain) {
		return false
	}
	// 冷却期内不可用
	if !fc.cooldownAt[idx].IsZero() && time.Now().Before(fc.cooldownAt[idx]) {
		return false
	}
	return true
}

// recordFailure 记录失败
func (fc *FallbackChain) recordFailure(idx int, err error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.failCounts[idx]++
	log.Printf("[fallback] provider %s fail count: %d/%d", fc.chain[idx].Name(), fc.failCounts[idx], fc.maxFails)

	if fc.failCounts[idx] >= fc.maxFails {
		// 设置冷却
		fc.cooldownAt[idx] = time.Now().Add(fc.cooldown)
		log.Printf("[fallback] provider %s entering cooldown for %v", fc.chain[idx].Name(), fc.cooldown)

		// 如果是当前 active，切换到下一个
		if idx == fc.active {
			oldName := fc.chain[idx].Name()
			for i := idx + 1; i < len(fc.chain); i++ {
				if fc.isAvailableLocked(i) {
					fc.active = i
					newName := fc.chain[i].Name()
					log.Printf("[fallback] switching from %s to %s", oldName, newName)
					cb := fc.onSwitch // read under lock
					if cb != nil {
						go cb(oldName, newName)
					}
					break
				}
			}
		}
	}
}

// recordSuccess 记录成功
func (fc *FallbackChain) recordSuccess(idx int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.failCounts[idx] = 0
	fc.cooldownAt[idx] = time.Time{} // 清除冷却

	// 如果成功的不在 active 位置，切换回来（优先使用更靠前的 provider）
	if idx < fc.active {
		oldName := fc.chain[fc.active].Name()
		fc.active = idx
		newName := fc.chain[idx].Name()
		log.Printf("[fallback] switching back from %s to %s (higher priority)", oldName, newName)
		cb := fc.onSwitch
		if cb != nil {
			go cb(oldName, newName)
		}
	} else if idx != fc.active {
		oldName := fc.chain[fc.active].Name()
		fc.active = idx
		newName := fc.chain[idx].Name()
		log.Printf("[fallback] switching from %s to %s", oldName, newName)
		cb := fc.onSwitch
		if cb != nil {
			go cb(oldName, newName)
		}
	}
}

// ResetCooldown 重置指定 provider 的冷却状态
func (fc *FallbackChain) ResetCooldown(idx int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if idx >= 0 && idx < len(fc.chain) {
		fc.failCounts[idx] = 0
		fc.cooldownAt[idx] = time.Time{}
	}
}

// ResetAllCooldowns 重置所有 provider 的冷却状态
func (fc *FallbackChain) ResetAllCooldowns() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	for i := range fc.chain {
		fc.failCounts[i] = 0
		fc.cooldownAt[i] = time.Time{}
	}
	fc.active = 0
}

// Ensure FallbackChain implements Provider and FunctionCallingProvider
var (
	_ Provider                = (*FallbackChain)(nil)
	_ FunctionCallingProvider = (*FallbackChain)(nil)
)

// ChatWithOptions 发送消息（支持 function calling），自动降级
func (fc *FallbackChain) ChatWithOptions(ctx context.Context, messages []Message, opts CallOptions) (*Response, error) {
	startIdx := fc.nextAvailable()
	if startIdx < 0 {
		return nil, fmt.Errorf("all providers in fallback chain are unavailable")
	}

	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		p := fc.chain[i]
		if fcProvider, ok := p.(FunctionCallingProvider); ok && len(opts.Tools) > 0 {
			resp, err := fcProvider.ChatWithOptions(ctx, messages, opts)
			if err != nil {
				fc.recordFailure(i, err)
				log.Printf("[fallback] provider %s ChatWithOptions failed: %v, trying next", p.Name(), err)
				continue
			}
			fc.recordSuccess(i)
			return resp, nil
		}

		// Provider 不支持 function calling，回退到普通 Chat
		resp, err := p.Chat(ctx, messages)
		if err != nil {
			fc.recordFailure(i, err)
			log.Printf("[fallback] provider %s Chat failed: %v, trying next", p.Name(), err)
			continue
		}
		fc.recordSuccess(i)
		return resp, nil
	}

	return nil, fmt.Errorf("all %d providers failed in fallback chain", len(fc.chain))
}

// ChatStreamWithOptions 流式发送消息（支持 function calling），自动降级
func (fc *FallbackChain) ChatStreamWithOptions(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	startIdx := fc.nextAvailable()
	if startIdx < 0 {
		return nil, fmt.Errorf("all providers in fallback chain are unavailable")
	}

	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		p := fc.chain[i]
		if fcProvider, ok := p.(FunctionCallingProvider); ok && len(opts.Tools) > 0 {
			ch, err := fcProvider.ChatStreamWithOptions(ctx, messages, opts)
			if err != nil {
				fc.recordFailure(i, err)
				log.Printf("[fallback] provider %s ChatStreamWithOptions failed: %v, trying next", p.Name(), err)
				continue
			}
			fc.recordSuccess(i)
			return ch, nil
		}

		// Provider 不支持 function calling，回退到普通 ChatStream
		ch, err := p.ChatStream(ctx, messages)
		if err != nil {
			fc.recordFailure(i, err)
			log.Printf("[fallback] provider %s ChatStream failed: %v, trying next", p.Name(), err)
			continue
		}
		fc.recordSuccess(i)
		return ch, nil
	}

	return nil, fmt.Errorf("all %d providers failed in fallback chain", len(fc.chain))
}
