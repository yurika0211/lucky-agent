package tool

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// SubTier 订阅等级
type SubTier int

const (
	SubFree       SubTier = iota // 免费级：基础工具
	SubBasic                     // 基础级：常用工具
	SubPro                       // 专业级：全部工具
	SubEnterprise                // 企业级：全部工具 + 优先级
)

func (t SubTier) String() string {
	switch t {
	case SubFree:
		return "free"
	case SubBasic:
		return "basic"
	case SubPro:
		return "pro"
	case SubEnterprise:
		return "enterprise"
	default:
		return "unknown"
	}
}

// ParseSubTier 从字符串解析订阅等级
func ParseSubTier(s string) (SubTier, error) {
	switch s {
	case "free":
		return SubFree, nil
	case "basic":
		return SubBasic, nil
	case "pro":
		return SubPro, nil
	case "enterprise":
		return SubEnterprise, nil
	default:
		return SubFree, fmt.Errorf("unknown subscription tier: %s", s)
	}
}

// TierConfig 订阅等级配置
type TierConfig struct {
	Tier              SubTier
	AllowedCategories []Category // 允许的工具分类
	MaxCallsPerDay    int        // 每日最大调用次数 (0 = 无限)
	MaxCallsPerHour   int        // 每小时最大调用次数 (0 = 无限)
	Priority          int        // 优先级 (越高越优先)
}

// DefaultTierConfigs 默认订阅等级配置
func DefaultTierConfigs() map[SubTier]TierConfig {
	return map[SubTier]TierConfig{
		SubFree: {
			Tier:              SubFree,
			AllowedCategories: []Category{CatBuiltin},
			MaxCallsPerDay:    100,
			MaxCallsPerHour:   20,
			Priority:          0,
		},
		SubBasic: {
			Tier:              SubBasic,
			AllowedCategories: []Category{CatBuiltin, CatSkill},
			MaxCallsPerDay:    500,
			MaxCallsPerHour:   100,
			Priority:          1,
		},
		SubPro: {
			Tier:              SubPro,
			AllowedCategories: []Category{CatBuiltin, CatSkill, CatMCP, CatDelegate},
			MaxCallsPerDay:    0, // 无限
			MaxCallsPerHour:   0,
			Priority:          2,
		},
		SubEnterprise: {
			Tier:              SubEnterprise,
			AllowedCategories: []Category{CatBuiltin, CatSkill, CatMCP, CatDelegate},
			MaxCallsPerDay:    0,
			MaxCallsPerHour:   0,
			Priority:          3,
		},
	}
}

// UserSubscription 用户订阅
type UserSubscription struct {
	UserID    string    `json:"user_id"`
	Tier      SubTier   `json:"tier"`
	StartedAt time.Time `json:"started_at"`
	ExpiresAt time.Time `json:"expires_at"`
	AutoRenew bool      `json:"auto_renew"`
}

// IsActive 检查订阅是否有效
func (s *UserSubscription) IsActive() bool {
	return time.Now().Before(s.ExpiresAt)
}

// SubscriptionManager 订阅管理器
type SubscriptionManager struct {
	mu      sync.RWMutex
	subs    map[string]*UserSubscription // userID -> subscription
	configs map[SubTier]TierConfig       // tier -> config
	usage   map[string]*subUsage         // userID -> usage counter
}

// subUsage 订阅使用计数
type subUsage struct {
	DailyCount  int       `json:"daily_count"`
	HourlyCount int       `json:"hourly_count"`
	DayReset    time.Time `json:"day_reset"`
	HourReset   time.Time `json:"hour_reset"`
}

// NewSubscriptionManager 创建订阅管理器
func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		subs:    make(map[string]*UserSubscription),
		configs: DefaultTierConfigs(),
		usage:   make(map[string]*subUsage),
	}
}

// Subscribe 订阅
func (sm *SubscriptionManager) Subscribe(userID string, tier SubTier, duration time.Duration) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	sub := &UserSubscription{
		UserID:    userID,
		Tier:      tier,
		StartedAt: now,
		ExpiresAt: now.Add(duration),
		AutoRenew: false,
	}

	// 如果已有订阅，延长
	if existing, ok := sm.subs[userID]; ok && existing.IsActive() {
		sub.ExpiresAt = existing.ExpiresAt.Add(duration)
		sub.StartedAt = existing.StartedAt
	}

	sm.subs[userID] = sub
	return nil
}

// Unsubscribe 取消订阅
func (sm *SubscriptionManager) Unsubscribe(userID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.subs, userID)
	delete(sm.usage, userID)
}

// GetSubscription 获取用户订阅
func (sm *SubscriptionManager) GetSubscription(userID string) *UserSubscription {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sub, ok := sm.subs[userID]; ok {
		subCopy := *sub
		return &subCopy
	}
	return nil
}

// CanUse 检查用户是否可以使用某工具
func (sm *SubscriptionManager) CanUse(userID, toolName string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// 获取用户订阅等级
	tier := SubFree
	if sub, ok := sm.subs[userID]; ok && sub.IsActive() {
		tier = sub.Tier
	}

	config, ok := sm.configs[tier]
	if !ok {
		config = sm.configs[SubFree]
	}

	// 检查分类权限
	// 需要从 registry 获取工具分类，但 SubscriptionManager 不持有 registry
	// 这里简化为：检查工具名前缀
	category := inferCategory(toolName)
	if !isCategoryAllowed(config.AllowedCategories, category) {
		return false
	}

	// 检查使用计数
	usage := sm.getOrCreateUsage(userID)
	sm.resetUsageIfNeeded(usage)

	if config.MaxCallsPerDay > 0 && usage.DailyCount >= config.MaxCallsPerDay {
		return false
	}
	if config.MaxCallsPerHour > 0 && usage.HourlyCount >= config.MaxCallsPerHour {
		return false
	}

	return true
}

// RecordUsage 记录使用
func (sm *SubscriptionManager) RecordUsage(userID, toolName string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	usage := sm.getOrCreateUsage(userID)
	sm.resetUsageIfNeeded(usage)
	usage.DailyCount++
	usage.HourlyCount++
}

// SetTierConfig 设置订阅等级配置
func (sm *SubscriptionManager) SetTierConfig(tier SubTier, config TierConfig) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	config.Tier = tier
	sm.configs[tier] = config
}

// GetTierConfig 获取订阅等级配置
func (sm *SubscriptionManager) GetTierConfig(tier SubTier) TierConfig {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if c, ok := sm.configs[tier]; ok {
		return c
	}
	return TierConfig{}
}

// ListSubscriptions 列出所有订阅
func (sm *SubscriptionManager) ListSubscriptions() []UserSubscription {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	var subs []UserSubscription
	for _, s := range sm.subs {
		subs = append(subs, *s)
	}

	sort.Slice(subs, func(i, j int) bool {
		return subs[i].UserID < subs[j].UserID
	})

	return subs
}

// getOrCreateUsage 获取或创建使用计数
func (sm *SubscriptionManager) getOrCreateUsage(userID string) *subUsage {
	if u, ok := sm.usage[userID]; ok {
		return u
	}
	now := time.Now()
	u := &subUsage{
		DayReset:  time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location()),
		HourReset: now.Add(time.Hour),
	}
	sm.usage[userID] = u
	return u
}

// resetUsageIfNeeded 检查并重置使用计数
func (sm *SubscriptionManager) resetUsageIfNeeded(u *subUsage) {
	now := time.Now()
	if now.After(u.DayReset) {
		u.DailyCount = 0
		u.DayReset = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	}
	if now.After(u.HourReset) {
		u.HourlyCount = 0
		u.HourReset = now.Add(time.Hour)
	}
}

// inferCategory 从工具名推断分类
func inferCategory(toolName string) Category {
	if len(toolName) >= 4 && toolName[:4] == "mcp_" {
		return CatMCP
	}
	if len(toolName) >= 6 && toolName[:6] == "skill_" {
		return CatSkill
	}
	if toolName == "delegate_task" || toolName == "task_status" || toolName == "list_tasks" || toolName == "delegate_cancel" {
		return CatDelegate
	}
	return CatBuiltin
}

// isCategoryAllowed 检查分类是否在允许列表中
func isCategoryAllowed(allowed []Category, cat Category) bool {
	for _, a := range allowed {
		if a == cat {
			return true
		}
	}
	return false
}
