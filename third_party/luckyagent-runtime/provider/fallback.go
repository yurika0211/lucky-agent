package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// modelErrorCooldown is how long a model-level 404 stays out of the chain
// before one probe. Account groups can start serving that model again, so
// the node is not closed for the process lifetime.
const modelErrorCooldown = 30 * time.Minute

// FallbackConfig 定义降级链中一个节点的配置
type FallbackConfig struct {
	Name             string // provider name (openai, anthropic, ollama, openrouter, openai-compatible)
	APIKey           string
	APIBase          string
	Model            string
	Protocol         string
	ReasoningSummary string
	ReasoningEffort  string
	PromptCacheKey   string
}

// FallbackChain 实现 Provider 自动降级链
// primary → fallback → local (Ollama)
// 当 primary 失败时自动切换到下一个 provider
type FallbackChain struct {
	chain      []Provider
	nodes      []fallbackNode
	mu         sync.RWMutex
	active     int // 当前活跃的 provider 索引
	failCounts []int
	maxFails   int           // 连续失败多少次后降级
	cooldown   time.Duration // 降级冷却时间
	cooldownAt []time.Time   // 每个 provider 的冷却截止时间
	modelError []bool        // 冷却来自模型级错误，到期后只放行一次探测
	onSwitch   func(from, to string)
	now        func() time.Time
	modelCool  time.Duration
}

type fallbackNode struct {
	name       string
	model      string
	endpoint   string
	credential string
}

// NodeStatus is one fallback entry as exposed to health checks.
type NodeStatus struct {
	Index           int    `json:"index"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	Endpoint        string `json:"endpoint"`
	Credential      string `json:"credential"`
	Available       bool   `json:"available"`
	FailCount       int    `json:"fail_count"`
	CooldownUntil   string `json:"cooldown_until,omitempty"`
	ModelError      bool   `json:"model_error"`
	CircuitState    string `json:"circuit_state,omitempty"`
	CircuitFailures int    `json:"circuit_failures,omitempty"`
}

// ChainStatus is the fallback chain snapshot used by health and readiness.
type ChainStatus struct {
	ActiveIndex int          `json:"active_index"`
	Nodes       []NodeStatus `json:"nodes"`
}

// NewFallbackChain 创建降级链
func NewFallbackChain(configs []FallbackConfig, registry *Registry) (*FallbackChain, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("fallback chain requires at least one provider config")
	}

	chain := make([]Provider, 0, len(configs))
	nodes := make([]fallbackNode, 0, len(configs))
	failCounts := make([]int, len(configs))
	cooldownAt := make([]time.Time, len(configs))
	modelError := make([]bool, len(configs))

	for _, fc := range configs {
		name := strings.TrimSpace(fc.Name)
		if name == "" {
			name = "openai"
		}
		pCfg := Config{
			LlmProvider: LlmProvider{
				Name:             name,
				BaseURL:          fc.APIBase,
				Model:            fc.Model,
				APIKey:           fc.APIKey,
				Protocol:         fc.Protocol,
				ReasoningSummary: fc.ReasoningSummary,
				ReasoningEffort:  fc.ReasoningEffort,
				PromptCacheKey:   fc.PromptCacheKey,
			},
		}
		p, err := registry.Create(name, pCfg)
		if err != nil {
			return nil, fmt.Errorf("resolve provider %s: %w", name, err)
		}
		if wrapped := wrapNodeResilience(p, registry); wrapped != nil {
			p = wrapped
		}
		chain = append(chain, p)
		nodes = append(nodes, fallbackNode{
			name:       name,
			model:      strings.TrimSpace(fc.Model),
			endpoint:   strings.TrimRight(strings.TrimSpace(fc.APIBase), "/"),
			credential: credentialLabel(fc.APIKey),
		})
	}

	return &FallbackChain{
		chain:      chain,
		nodes:      nodes,
		failCounts: failCounts,
		cooldownAt: cooldownAt,
		modelError: modelError,
		maxFails:   3,
		cooldown:   5 * time.Minute,
		modelCool:  modelErrorCooldown,
		now:        time.Now,
	}, nil
}

// NodeResilience wraps one fallback entry with its own middleware.
// The chain installs this so a breaker counts failures of that model and
// endpoint, not of the whole fallback chain.
type NodeResilience func(Provider) Provider

// SetNodeResilience installs the per-node wrapper used by later chains.
// Tests and hosts that do not configure a breaker leave nodes unwrapped.
func (r *Registry) SetNodeResilience(wrap NodeResilience) {
	if r == nil {
		return
	}
	r.nodeResilience = wrap
}

func wrapNodeResilience(p Provider, registry *Registry) Provider {
	if registry == nil || registry.nodeResilience == nil || p == nil {
		return p
	}
	wrapped := registry.nodeResilience(p)
	if wrapped == nil {
		return p
	}
	return wrapped
}

func credentialLabel(apiKey string) string {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(trimmed))
	return hex.EncodeToString(sum[:4])
}

func (fc *FallbackChain) clock() time.Time {
	if fc.now != nil {
		return fc.now()
	}
	return time.Now()
}

func (n fallbackNode) label() string {
	model := n.model
	if model == "" {
		model = "-"
	}
	endpoint := n.endpoint
	if endpoint == "" {
		endpoint = "-"
	}
	return fmt.Sprintf("%s model=%s endpoint=%s credential=%s", n.name, model, endpoint, n.credential)
}

func (fc *FallbackChain) nodeLabel(idx int) string {
	if idx < 0 || idx >= len(fc.nodes) {
		if idx >= 0 && idx < len(fc.chain) {
			return fc.chain[idx].Name()
		}
		return fmt.Sprintf("index %d", idx)
	}
	return fc.nodes[idx].label()
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

// Model returns the model of the currently active provider.
func (fc *FallbackChain) Model() string {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	if modeler, ok := fc.chain[fc.active].(interface{ Model() string }); ok {
		return modeler.Model()
	}
	return ""
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

	var lastErr error
	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		resp, err := fc.chain[i].Chat(ctx, messages)
		if err != nil {
			lastErr = err
			if !fc.shouldTryNextProvider(i, err) {
				return nil, err
			}
			log.Printf("[fallback] %s failed: %v, trying next", fc.nodeLabel(i), err)
			continue
		}

		// 成功：重置失败计数，如果不在 active 位置则切换
		fc.recordSuccess(i)
		return resp, nil
	}

	return nil, fallbackChainError(len(fc.chain), lastErr)
}

// ChatStream 流式发送消息，自动降级
func (fc *FallbackChain) ChatStream(ctx context.Context, messages []Message) (<-chan StreamChunk, error) {
	startIdx := fc.nextAvailable()
	if startIdx < 0 {
		return nil, fmt.Errorf("all providers in fallback chain are unavailable")
	}

	var lastErr error
	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		ch, err := fc.chain[i].ChatStream(ctx, messages)
		if err != nil {
			lastErr = err
			if !fc.shouldTryNextProvider(i, err) {
				return nil, err
			}
			log.Printf("[fallback] %s stream failed: %v, trying next", fc.nodeLabel(i), err)
			continue
		}

		fc.recordSuccess(i)
		return ch, nil
	}

	return nil, fallbackChainError(len(fc.chain), lastErr)
}

func fallbackChainError(chainLen int, lastErr error) error {
	if lastErr != nil {
		return fmt.Errorf("all %d providers failed in fallback chain: %w", chainLen, lastErr)
	}
	return fmt.Errorf("all %d providers failed in fallback chain", chainLen)
}

// isModelLevelError reports a failure of this model on an otherwise reachable
// provider. The same provider's other models stay usable, and the node is
// cooled down instead of closed for the process lifetime.
func isModelLevelError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "model_not_found") || strings.Contains(msg, "model not found") {
		return true
	}
	if strings.Contains(msg, "not supported by any configured account") {
		return true
	}
	for _, marker := range []string{
		"api error 404",
		"unexpected status 404",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func (fc *FallbackChain) shouldTryNextProvider(idx int, err error) bool {
	if isModelLevelError(err) {
		fc.coolModel(idx, err)
		return fc.hasDifferentModel(idx)
	}
	if !shouldFallbackOnError(err) {
		return false
	}
	if fc.sharesEndpoint(idx) {
		fc.recordSharedFailure(idx, err)
		return false
	}
	fc.recordFailure(idx, err)
	return true
}

// sharesEndpoint reports whether a later node uses the same address and
// credential. Site-wide failures stay on the current node in that case.
func (fc *FallbackChain) sharesEndpoint(idx int) bool {
	if idx < 0 || idx >= len(fc.nodes) {
		return false
	}
	current := fc.nodes[idx]
	if current.endpoint == "" {
		return false
	}
	for i := idx + 1; i < len(fc.nodes); i++ {
		next := fc.nodes[i]
		if next.endpoint == current.endpoint && next.credential == current.credential {
			return true
		}
	}
	return false
}

func (fc *FallbackChain) hasDifferentModel(idx int) bool {
	if idx < 0 || idx >= len(fc.nodes) {
		return true
	}
	current := fc.nodes[idx].model
	for i := idx + 1; i < len(fc.nodes); i++ {
		if fc.nodes[i].model != current {
			return true
		}
	}
	return false
}

func shouldFallbackOnError(err error) bool {
	if err == nil {
		return false
	}

	msg := strings.ToLower(err.Error())
	if msg == "context deadline exceeded" || msg == "context canceled" {
		return true
	}
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate limit") || strings.Contains(msg, "too many requests") {
		return true
	}
	if strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return true
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "timeout") {
		return true
	}
	if strings.Contains(msg, "temporary") || strings.Contains(msg, "temporarily unavailable") {
		return true
	}
	if strings.Contains(msg, "eof") || strings.Contains(msg, "tls") {
		return true
	}
	return false
}

// Validate 验证降级链中至少有一个可用 provider
func (fc *FallbackChain) Validate() error {
	hasValid := false
	for i, p := range fc.chain {
		if err := p.Validate(); err != nil {
			log.Printf("[fallback] %s invalid: %v", fc.nodeLabel(i), err)
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

	// 每个新请求都从最高优先级开始。冷却中的节点会被跳过；普通冷却
	// 结束后主节点获得一次恢复探测。模型级冷却到期后只放行一次探测。
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
	if !fc.cooldownAt[idx].IsZero() && fc.clock().Before(fc.cooldownAt[idx]) {
		return false
	}
	return true
}

// coolModel parks one model after a model-level error. A later node with a
// different model may still be tried. The cooldown expires into a single probe.
func (fc *FallbackChain) coolModel(idx int, err error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if idx < 0 || idx >= len(fc.chain) {
		return
	}

	cool := fc.modelCool
	if cool <= 0 {
		cool = modelErrorCooldown
	}
	fc.modelError[idx] = true
	fc.failCounts[idx] = 0
	fc.cooldownAt[idx] = fc.clock().Add(cool)
	log.Printf("[fallback] %s model cooldown for %v after: %v", fc.nodeLabel(idx), cool, err)
	fc.switchAwayLocked(idx)
}

// recordSharedFailure counts a site-wide error without moving to another
// model that uses the same address and credential.
func (fc *FallbackChain) recordSharedFailure(idx int, err error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if idx < 0 || idx >= len(fc.chain) {
		return
	}

	fc.modelError[idx] = false
	fc.failCounts[idx]++
	log.Printf("[fallback] %s shared endpoint failure %d/%d: %v", fc.nodeLabel(idx), fc.failCounts[idx], fc.maxFails, err)
	if fc.failCounts[idx] < fc.maxFails {
		return
	}
	fc.cooldownAt[idx] = fc.clock().Add(fc.cooldown)
	log.Printf("[fallback] %s entering cooldown for %v", fc.nodeLabel(idx), fc.cooldown)
}

// recordFailure 记录失败
func (fc *FallbackChain) recordFailure(idx int, err error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.modelError[idx] = false
	fc.failCounts[idx]++
	log.Printf("[fallback] %s fail count: %d/%d", fc.nodeLabel(idx), fc.failCounts[idx], fc.maxFails)

	if fc.failCounts[idx] >= fc.maxFails {
		fc.cooldownAt[idx] = fc.clock().Add(fc.cooldown)
		log.Printf("[fallback] %s entering cooldown for %v", fc.nodeLabel(idx), fc.cooldown)
		fc.switchAwayLocked(idx)
	}
}

func (fc *FallbackChain) switchAwayLocked(idx int) {
	if idx != fc.active {
		return
	}
	oldName := fc.nodeLabel(idx)
	for i := 0; i < len(fc.chain); i++ {
		if i == idx || !fc.isAvailableLocked(i) {
			continue
		}
		fc.active = i
		newName := fc.nodeLabel(i)
		log.Printf("[fallback] switching from %s to %s", oldName, newName)
		if fc.onSwitch != nil {
			from, to := oldName, newName
			go fc.onSwitch(from, to)
		}
		return
	}
}

// recordSuccess 记录成功
func (fc *FallbackChain) recordSuccess(idx int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	fc.failCounts[idx] = 0
	fc.modelError[idx] = false
	fc.cooldownAt[idx] = time.Time{}

	if idx == fc.active {
		return
	}
	oldName := fc.nodeLabel(fc.active)
	higher := idx < fc.active
	fc.active = idx
	newName := fc.nodeLabel(idx)
	if higher {
		log.Printf("[fallback] switching back from %s to %s (higher priority)", oldName, newName)
	} else {
		log.Printf("[fallback] switching from %s to %s", oldName, newName)
	}
	if fc.onSwitch != nil {
		go fc.onSwitch(oldName, newName)
	}
}

// ResetCooldown 重置指定 provider 的冷却状态
func (fc *FallbackChain) ResetCooldown(idx int) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	if idx >= 0 && idx < len(fc.chain) {
		fc.failCounts[idx] = 0
		fc.modelError[idx] = false
		fc.cooldownAt[idx] = time.Time{}
	}
}

// ResetAllCooldowns 重置所有 provider 的冷却状态
func (fc *FallbackChain) ResetAllCooldowns() {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	for i := range fc.chain {
		fc.failCounts[i] = 0
		fc.modelError[i] = false
		fc.cooldownAt[i] = time.Time{}
	}
	fc.active = 0
}

// Status returns each node's identity and cooldown without secrets.
func (fc *FallbackChain) Status() ChainStatus {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	status := ChainStatus{
		ActiveIndex: fc.active,
		Nodes:       make([]NodeStatus, len(fc.chain)),
	}
	for i := range fc.chain {
		node := fallbackNode{}
		if i < len(fc.nodes) {
			node = fc.nodes[i]
		}
		item := NodeStatus{
			Index:      i,
			Provider:   node.name,
			Model:      node.model,
			Endpoint:   node.endpoint,
			Credential: node.credential,
			Available:  fc.isAvailableLocked(i),
			FailCount:  fc.failCounts[i],
			ModelError: fc.modelError[i],
		}
		if !fc.cooldownAt[i].IsZero() && fc.clock().Before(fc.cooldownAt[i]) {
			item.CooldownUntil = fc.cooldownAt[i].UTC().Format(time.RFC3339)
		}
		if breaker, ok := fc.chain[i].(interface{ CircuitStatus() (string, int) }); ok {
			item.CircuitState, item.CircuitFailures = breaker.CircuitStatus()
		}
		status.Nodes[i] = item
	}
	return status
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

	var lastErr error
	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		p := fc.chain[i]
		if fcProvider, ok := p.(FunctionCallingProvider); ok && len(opts.Tools) > 0 {
			resp, err := fcProvider.ChatWithOptions(ctx, messages, opts)
			if err != nil {
				lastErr = err
				if !fc.shouldTryNextProvider(i, err) {
					return nil, err
				}
				log.Printf("[fallback] %s ChatWithOptions failed: %v, trying next", fc.nodeLabel(i), err)
				continue
			}
			fc.recordSuccess(i)
			return resp, nil
		}

		// Provider 不支持 function calling，回退到普通 Chat
		resp, err := p.Chat(ctx, messages)
		if err != nil {
			lastErr = err
			if !fc.shouldTryNextProvider(i, err) {
				return nil, err
			}
			log.Printf("[fallback] %s Chat failed: %v, trying next", fc.nodeLabel(i), err)
			continue
		}
		fc.recordSuccess(i)
		return resp, nil
	}

	return nil, fallbackChainError(len(fc.chain), lastErr)
}

// ChatStreamWithOptions 流式发送消息（支持 function calling），自动降级
func (fc *FallbackChain) ChatStreamWithOptions(ctx context.Context, messages []Message, opts CallOptions) (<-chan StreamChunk, error) {
	startIdx := fc.nextAvailable()
	if startIdx < 0 {
		return nil, fmt.Errorf("all providers in fallback chain are unavailable")
	}

	var lastErr error
	for i := startIdx; i < len(fc.chain); i++ {
		if !fc.isAvailable(i) {
			continue
		}

		p := fc.chain[i]
		if fcProvider, ok := p.(FunctionCallingProvider); ok && len(opts.Tools) > 0 {
			ch, err := fcProvider.ChatStreamWithOptions(ctx, messages, opts)
			if err != nil {
				lastErr = err
				if !fc.shouldTryNextProvider(i, err) {
					return nil, err
				}
				log.Printf("[fallback] %s ChatStreamWithOptions failed: %v, trying next", fc.nodeLabel(i), err)
				continue
			}
			fc.recordSuccess(i)
			return ch, nil
		}

		// Provider 不支持 function calling，回退到普通 ChatStream
		ch, err := p.ChatStream(ctx, messages)
		if err != nil {
			lastErr = err
			if !fc.shouldTryNextProvider(i, err) {
				return nil, err
			}
			log.Printf("[fallback] %s ChatStream failed: %v, trying next", fc.nodeLabel(i), err)
			continue
		}
		fc.recordSuccess(i)
		return ch, nil
	}

	return nil, fallbackChainError(len(fc.chain), lastErr)
}
