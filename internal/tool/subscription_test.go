package tool

import (
	"testing"
	"time"
)

func TestSubscriptionManagerSubscribe(t *testing.T) {
	sm := NewSubscriptionManager()

	err := sm.Subscribe("user1", SubPro, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	sub := sm.GetSubscription("user1")
	if sub == nil {
		t.Fatal("expected subscription")
	}
	if sub.Tier != SubPro {
		t.Errorf("expected pro, got %s", sub.Tier)
	}
	if !sub.IsActive() {
		t.Error("expected active subscription")
	}
}

func TestSubscriptionManagerExpired(t *testing.T) {
	sm := NewSubscriptionManager()

	// 订阅 1 毫秒后过期
	sm.Subscribe("user1", SubPro, 1*time.Millisecond)
	time.Sleep(2 * time.Millisecond)

	sub := sm.GetSubscription("user1")
	if sub == nil {
		t.Fatal("expected subscription record")
	}
	if sub.IsActive() {
		t.Error("expected expired subscription")
	}
}

func TestSubscriptionManagerUnsubscribe(t *testing.T) {
	sm := NewSubscriptionManager()

	sm.Subscribe("user1", SubBasic, 30*24*time.Hour)
	sm.Unsubscribe("user1")

	sub := sm.GetSubscription("user1")
	if sub != nil {
		t.Error("expected nil after unsubscribe")
	}
}

func TestSubscriptionManagerCanUseFree(t *testing.T) {
	sm := NewSubscriptionManager()
	r := NewRegistry()
	RegisterBuiltinTools(r)

	// 免费用户可以使用内置工具
	if !sm.CanUse("user1", "current_time") {
		t.Error("free user should be able to use builtin tools")
	}

	// 免费用户不能使用 Skill 工具
	if sm.CanUse("user1", "skill_search_query") {
		t.Error("free user should not be able to use skill tools")
	}

	// 免费用户不能使用 MCP 工具
	if sm.CanUse("user1", "mcp_server_tool") {
		t.Error("free user should not be able to use mcp tools")
	}
}

func TestSubscriptionManagerCanUsePro(t *testing.T) {
	sm := NewSubscriptionManager()

	sm.Subscribe("user1", SubPro, 30*24*time.Hour)

	// Pro 用户可以使用所有分类
	if !sm.CanUse("user1", "current_time") {
		t.Error("pro user should be able to use builtin tools")
	}
	if !sm.CanUse("user1", "skill_search_query") {
		t.Error("pro user should be able to use skill tools")
	}
	if !sm.CanUse("user1", "mcp_server_tool") {
		t.Error("pro user should be able to use mcp tools")
	}
	if !sm.CanUse("user1", "delegate_task") {
		t.Error("pro user should be able to use delegate tools")
	}
}

func TestSubscriptionManagerDailyLimit(t *testing.T) {
	sm := NewSubscriptionManager()

	// Free 用户每天 100 次
	for i := 0; i < 100; i++ {
		sm.RecordUsage("user1", "current_time")
	}

	// 第 101 次应该被拒绝
	if sm.CanUse("user1", "current_time") {
		t.Error("free user should be rate limited after 100 daily calls")
	}
}

func TestSubscriptionManagerExtend(t *testing.T) {
	sm := NewSubscriptionManager()

	sm.Subscribe("user1", SubBasic, 30*24*time.Hour)
	sub1 := sm.GetSubscription("user1")
	expiresAt1 := sub1.ExpiresAt

	// 延长订阅
	sm.Subscribe("user1", SubBasic, 30*24*time.Hour)
	sub2 := sm.GetSubscription("user1")

	if !sub2.ExpiresAt.After(expiresAt1) {
		t.Error("expected extended expiration")
	}
}

func TestSubscriptionManagerTierUpgrade(t *testing.T) {
	sm := NewSubscriptionManager()

	sm.Subscribe("user1", SubFree, 30*24*time.Hour)
	sm.Subscribe("user1", SubPro, 30*24*time.Hour)

	sub := sm.GetSubscription("user1")
	if sub.Tier != SubPro {
		t.Errorf("expected pro tier after upgrade, got %s", sub.Tier)
	}
}

func TestSubscriptionManagerListSubscriptions(t *testing.T) {
	sm := NewSubscriptionManager()

	sm.Subscribe("user1", SubBasic, 30*24*time.Hour)
	sm.Subscribe("user2", SubPro, 30*24*time.Hour)

	subs := sm.ListSubscriptions()
	if len(subs) != 2 {
		t.Fatalf("expected 2 subscriptions, got %d", len(subs))
	}
}

func TestParseSubTier(t *testing.T) {
	tests := []struct {
		input    string
		expected SubTier
		hasError bool
	}{
		{"free", SubFree, false},
		{"basic", SubBasic, false},
		{"pro", SubPro, false},
		{"enterprise", SubEnterprise, false},
		{"invalid", SubFree, true},
	}

	for _, tt := range tests {
		tier, err := ParseSubTier(tt.input)
		if tt.hasError {
			if err == nil {
				t.Errorf("expected error for %s", tt.input)
			}
		} else {
			if tier != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tier)
			}
		}
	}
}

func TestSubTierString(t *testing.T) {
	if SubFree.String() != "free" {
		t.Errorf("expected 'free', got '%s'", SubFree.String())
	}
	if SubPro.String() != "pro" {
		t.Errorf("expected 'pro', got '%s'", SubPro.String())
	}
}

func TestInferCategory(t *testing.T) {
	tests := []struct {
		toolName string
		expected Category
	}{
		{"terminal", CatBuiltin},
		{"file_read", CatBuiltin},
		{"current_time", CatBuiltin},
		{"mcp_server_tool", CatMCP},
		{"skill_search_query", CatSkill},
		{"delegate_task", CatDelegate},
		{"task_status", CatDelegate},
		{"list_tasks", CatDelegate},
	}

	for _, tt := range tests {
		result := inferCategory(tt.toolName)
		if result != tt.expected {
			t.Errorf("inferCategory(%s) = %s, expected %s", tt.toolName, result, tt.expected)
		}
	}
}

func TestDefaultTierConfigs(t *testing.T) {
	configs := DefaultTierConfigs()

	if len(configs) != 4 {
		t.Fatalf("expected 4 tier configs, got %d", len(configs))
	}

	// Free 只允许 builtin
	freeConfig := configs[SubFree]
	if len(freeConfig.AllowedCategories) != 1 || freeConfig.AllowedCategories[0] != CatBuiltin {
		t.Error("free tier should only allow builtin")
	}

	// Pro 允许所有
	proConfig := configs[SubPro]
	if len(proConfig.AllowedCategories) != 4 {
		t.Error("pro tier should allow all categories")
	}
}

func TestSubscriptionManagerSetTierConfig(t *testing.T) {
	sm := NewSubscriptionManager()

	customConfig := TierConfig{
		Tier:              SubFree,
		AllowedCategories: []Category{CatBuiltin, CatSkill},
		MaxCallsPerDay:    200,
		MaxCallsPerHour:   50,
		Priority:          0,
	}
	sm.SetTierConfig(SubFree, customConfig)

	config := sm.GetTierConfig(SubFree)
	if config.MaxCallsPerDay != 200 {
		t.Errorf("expected 200, got %d", config.MaxCallsPerDay)
	}
	if len(config.AllowedCategories) != 2 {
		t.Errorf("expected 2 categories, got %d", len(config.AllowedCategories))
	}
}
