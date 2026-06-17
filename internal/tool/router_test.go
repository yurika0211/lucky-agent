package tool

import (
	"testing"
)

func TestToolRouterResolve(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	// 直接解析
	name, err := router.Resolve("current_time")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if name != "current_time" {
		t.Errorf("expected current_time, got %s", name)
	}
}

func TestToolRouterAlias(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	// 添加别名
	err := router.AddAlias("time", "current_time")
	if err != nil {
		t.Fatalf("add alias: %v", err)
	}

	// 通过别名解析
	name, err := router.Resolve("time")
	if err != nil {
		t.Fatalf("resolve alias: %v", err)
	}
	if name != "current_time" {
		t.Errorf("expected current_time, got %s", name)
	}
}

func TestToolRouterAliasNotFound(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	// 别名指向不存在的工具
	err := router.AddAlias("time", "nonexistent_tool")
	if err == nil {
		t.Error("expected error for alias to nonexistent tool")
	}
}

func TestToolRouterRemoveAlias(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	router.AddAlias("time", "current_time")
	router.RemoveAlias("time")

	aliases := router.ListAliases()
	if _, ok := aliases["time"]; ok {
		t.Error("alias should be removed")
	}
}

func TestToolRouterRoute(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	// 添加路由规则：search → web_search
	router.AddRoute(RouteRule{
		Name:        "search-redirect",
		Priority:    10,
		ToolPattern: "search",
		Target:      "web_search",
		Enabled:     true,
	})

	name, err := router.Resolve("search")
	if err != nil {
		t.Fatalf("resolve route: %v", err)
	}
	if name != "web_search" {
		t.Errorf("expected web_search, got %s", name)
	}
}

func TestToolRouterWildcardPattern(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	// 通配符路由
	router.AddRoute(RouteRule{
		Name:        "web-redirect",
		Priority:    5,
		ToolPattern: "web_*",
		Target:      "web_search",
		Enabled:     true,
	})

	// web_fetch 应该匹配 web_* 模式
	name, err := router.Resolve("web_fetch")
	if err != nil {
		t.Fatalf("resolve wildcard: %v", err)
	}
	if name != "web_search" {
		t.Errorf("expected web_search, got %s", name)
	}
}

func TestToolRouterPriority(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	// 两个路由规则，高优先级优先
	router.AddRoute(RouteRule{
		Name:        "low-priority",
		Priority:    1,
		ToolPattern: "search",
		Target:      "file_read",
		Enabled:     true,
	})
	router.AddRoute(RouteRule{
		Name:        "high-priority",
		Priority:    10,
		ToolPattern: "search",
		Target:      "web_search",
		Enabled:     true,
	})

	name, err := router.Resolve("search")
	if err != nil {
		t.Fatalf("resolve priority: %v", err)
	}
	if name != "web_search" {
		t.Errorf("expected web_search (high priority), got %s", name)
	}
}

func TestToolRouterDisableRoute(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	router.AddRoute(RouteRule{
		Name:        "search-redirect",
		Priority:    10,
		ToolPattern: "search",
		Target:      "web_search",
		Enabled:     true,
	})

	// 禁用路由
	err := router.DisableRoute("search-redirect")
	if err != nil {
		t.Fatalf("disable route: %v", err)
	}

	// 禁用后应该找不到 search（因为 search 本身不存在于 registry）
	_, err = router.Resolve("search")
	if err == nil {
		t.Error("expected error when route is disabled and tool doesn't exist")
	}
}

func TestToolRouterEnableRoute(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	router.AddRoute(RouteRule{
		Name:        "search-redirect",
		Priority:    10,
		ToolPattern: "search",
		Target:      "web_search",
		Enabled:     false,
	})

	// 启用路由
	err := router.EnableRoute("search-redirect")
	if err != nil {
		t.Fatalf("enable route: %v", err)
	}

	name, err := router.Resolve("search")
	if err != nil {
		t.Fatalf("resolve after enable: %v", err)
	}
	if name != "web_search" {
		t.Errorf("expected web_search, got %s", name)
	}
}

func TestToolRouterRemoveRoute(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	router.AddRoute(RouteRule{
		Name:        "test-route",
		Priority:    5,
		ToolPattern: "search",
		Target:      "web_search",
		Enabled:     true,
	})

	router.RemoveRoute("test-route")

	routes := router.ListRoutes()
	if len(routes) != 0 {
		t.Errorf("expected 0 routes after removal, got %d", len(routes))
	}
}

func TestToolRouterFormatRoutes(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)
	router := NewToolRouter(r)

	router.AddRoute(RouteRule{
		Name:        "test-route",
		Priority:    5,
		ToolPattern: "search",
		Target:      "web_search",
		Enabled:     true,
	})
	router.AddAlias("time", "current_time")

	formatted := router.FormatRoutes()
	if formatted == "" {
		t.Error("expected non-empty format")
	}
}

func TestToolRouterResolveNotFound(t *testing.T) {
	r := NewRegistry()
	router := NewToolRouter(r)

	_, err := router.Resolve("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent tool")
	}
}

func TestMatchPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		name     string
		expected bool
	}{
		{"*", "anything", true},
		{"terminal", "terminal", true},
		{"terminal", "bash", false},
		{"web_*", "web_search", true},
		{"web_*", "web_fetch", true},
		{"web_*", "terminal", false},
		{"*_search", "web_search", true},
		{"*_search", "file_read", false},
	}

	for _, tt := range tests {
		result := matchPattern(tt.pattern, tt.name)
		if result != tt.expected {
			t.Errorf("matchPattern(%q, %q) = %v, expected %v", tt.pattern, tt.name, result, tt.expected)
		}
	}
}
