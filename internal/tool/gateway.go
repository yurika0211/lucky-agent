package tool

import (
	"fmt"
	"time"

	"github.com/yurika0211/luckyagent/internal/utils"
)

// Gateway 统一工具网关
// 提供统一的工具调度入口，支持路由、计量、配额和订阅管理
type Gateway struct {
	registry *Registry
	tracker  *UsageTracker
	sub      *SubscriptionManager
	router   *ToolRouter
}

// NewGateway 创建工具网关
func NewGateway(registry *Registry) *Gateway {
	g := &Gateway{
		registry: registry,
		tracker:  NewUsageTracker(),
		sub:      NewSubscriptionManager(),
		router:   NewToolRouter(registry),
	}
	return g
}

// Execute 通过网关执行工具调用
// 统一入口：路由 → 权限 → 配额 → 计量 → 执行
func (g *Gateway) Execute(name string, args map[string]any, userID string) (*GatewayResult, error) {
	start := time.Now()

	// 1. 路由：查找工具
	t, ok := g.registry.Get(name)
	if !ok {
		return nil, ErrToolNotFound{name: name}
	}
	if !t.Enabled {
		return nil, ErrToolDisabled{name: name}
	}

	// 2. 权限检查
	perm, err := g.registry.CheckPermission(name)
	if err != nil {
		return nil, err
	}
	if perm == PermDeny {
		return nil, ErrToolDenied{name: name}
	}

	// 3. 订阅检查
	if userID != "" {
		if !g.sub.CanUse(userID, name) {
			return nil, ErrQuotaExceeded{
				Tool:   name,
				UserID: userID,
				Reason: "subscription does not allow this tool",
			}
		}
	}

	// 4. 配额检查
	if userID != "" {
		if !g.tracker.CheckQuota(userID, name) {
			return nil, ErrQuotaExceeded{
				Tool:   name,
				UserID: userID,
				Reason: "usage quota exceeded",
			}
		}
	}

	// 5. 执行
	result, execErr := g.registry.Call(name, args)
	duration := time.Since(start)

	// 6. 计量记录
	if userID != "" {
		g.tracker.Record(userID, name, duration, execErr == nil)
	}

	// 7. 订阅扣减
	if userID != "" {
		g.sub.RecordUsage(userID, name)
	}

	return &GatewayResult{
		ToolName:  name,
		Output:    result,
		Duration:  duration,
		Success:   execErr == nil,
		Timestamp: start,
	}, execErr
}

// ExecuteWithShellContext 执行工具并注入 shell 上下文
func (g *Gateway) ExecuteWithShellContext(name string, args map[string]any, userID string, sc *ShellContext) (*GatewayResult, error) {
	start := time.Now()

	// 1. 路由：查找工具
	t, ok := g.registry.Get(name)
	if !ok {
		return nil, ErrToolNotFound{name: name}
	}
	if !t.Enabled {
		return nil, ErrToolDisabled{name: name}
	}

	// 2. 权限检查
	perm, err := g.registry.CheckPermission(name)
	if err != nil {
		return nil, err
	}
	if perm == PermDeny {
		return nil, ErrToolDenied{name: name}
	}

	// 3. 订阅检查
	if userID != "" {
		if !g.sub.CanUse(userID, name) {
			return nil, ErrQuotaExceeded{
				Tool:   name,
				UserID: userID,
				Reason: "subscription does not allow this tool",
			}
		}
	}

	// 4. 配额检查
	if userID != "" {
		if !g.tracker.CheckQuota(userID, name) {
			return nil, ErrQuotaExceeded{
				Tool:   name,
				UserID: userID,
				Reason: "usage quota exceeded",
			}
		}
	}

	// 5. 执行（带 shell 上下文）
	result, execErr := g.registry.CallWithShellContext(name, args, sc)
	duration := time.Since(start)

	// 6. 计量记录
	if userID != "" {
		g.tracker.Record(userID, name, duration, execErr == nil)
	}

	// 7. 订阅扣减
	if userID != "" {
		g.sub.RecordUsage(userID, name)
	}

	return &GatewayResult{
		ToolName:  name,
		Output:    result,
		Duration:  duration,
		Success:   execErr == nil,
		Timestamp: start,
	}, execErr
}

// Tracker 返回用量追踪器
func (g *Gateway) Tracker() *UsageTracker {
	return g.tracker
}

// Subscriptions 返回订阅管理器
func (g *Gateway) Subscriptions() *SubscriptionManager {
	return g.sub
}

// Router 返回工具路由器
func (g *Gateway) Router() *ToolRouter {
	return g.router
}

// GatewayResult 网关执行结果
type GatewayResult struct {
	ToolName  string
	Output    string
	Duration  time.Duration
	Success   bool
	Timestamp time.Time
}

// Format 格式化结果
func (r *GatewayResult) Format() string {
	status := "✅"
	if !r.Success {
		status = "❌"
	}
	return fmt.Sprintf("%s [%s] %s (%v)", status, r.ToolName, utils.Truncate(r.Output, 100), r.Duration)
}

// ErrQuotaExceeded 配额超限错误
type ErrQuotaExceeded struct {
	Tool   string
	UserID string
	Reason string
}

func (e ErrQuotaExceeded) Error() string {
	return fmt.Sprintf("quota exceeded for tool %s (user: %s): %s", e.Tool, e.UserID, e.Reason)
}
