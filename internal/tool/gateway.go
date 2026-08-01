package tool

import (
	"fmt"
	"time"

	"github.com/yurika0211/luckyagent/internal/utils"
)

// Gateway is the unified tool execution entry point.
type Gateway struct {
	registry *Registry
	tracker  *UsageTracker
	sub      *SubscriptionManager
	router   *ToolRouter
}

func NewGateway(registry *Registry) *Gateway {
	return &Gateway{
		registry: registry,
		tracker:  NewUsageTracker(),
		sub:      NewSubscriptionManager(),
		router:   NewToolRouter(registry),
	}
}

func (g *Gateway) Execute(name string, args map[string]any, userID string) (*GatewayResult, error) {
	start := time.Now()
	if err := g.checkExecutable(name, userID); err != nil {
		return nil, err
	}

	result, execErr := g.registry.CallDetailed(name, args)
	duration := time.Since(start)
	g.recordUsage(name, userID, duration, execErr == nil)

	return &GatewayResult{
		ToolName:  name,
		Output:    result.Output,
		Metadata:  result.Metadata,
		Duration:  duration,
		Success:   execErr == nil,
		Timestamp: start,
	}, execErr
}

func (g *Gateway) ExecuteWithShellContext(name string, args map[string]any, userID string, sc *ShellContext) (*GatewayResult, error) {
	start := time.Now()
	if err := g.checkExecutable(name, userID); err != nil {
		return nil, err
	}

	result, execErr := g.registry.CallDetailedWithShellContext(name, args, sc)
	duration := time.Since(start)
	g.recordUsage(name, userID, duration, execErr == nil)

	return &GatewayResult{
		ToolName:  name,
		Output:    result.Output,
		Metadata:  result.Metadata,
		Duration:  duration,
		Success:   execErr == nil,
		Timestamp: start,
	}, execErr
}

func (g *Gateway) checkExecutable(name, userID string) error {
	t, ok := g.registry.Get(name)
	if !ok {
		return ErrToolNotFound{name: name}
	}
	if !t.Enabled {
		return ErrToolDisabled{name: name}
	}

	perm, err := g.registry.CheckPermission(name)
	if err != nil {
		return err
	}
	if perm == PermDeny {
		return ErrToolDenied{name: name}
	}

	if userID != "" && !g.sub.CanUse(userID, name) {
		return ErrQuotaExceeded{
			Tool:   name,
			UserID: userID,
			Reason: "subscription does not allow this tool",
		}
	}
	if userID != "" && !g.tracker.CheckQuota(userID, name) {
		return ErrQuotaExceeded{
			Tool:   name,
			UserID: userID,
			Reason: "usage quota exceeded",
		}
	}
	return nil
}

func (g *Gateway) recordUsage(name, userID string, duration time.Duration, success bool) {
	if userID == "" {
		return
	}
	g.tracker.Record(userID, name, duration, success)
	g.sub.RecordUsage(userID, name)
}

func (g *Gateway) Tracker() *UsageTracker {
	return g.tracker
}

func (g *Gateway) Subscriptions() *SubscriptionManager {
	return g.sub
}

func (g *Gateway) Router() *ToolRouter {
	return g.router
}

type GatewayResult struct {
	ToolName  string
	Output    string
	Metadata  map[string]any
	Duration  time.Duration
	Success   bool
	Timestamp time.Time
}

func (r *GatewayResult) Format() string {
	status := "OK"
	if !r.Success {
		status = "ERR"
	}
	return fmt.Sprintf("%s [%s] %s (%v)", status, r.ToolName, utils.Truncate(r.Output, 100), r.Duration)
}

type ErrQuotaExceeded struct {
	Tool   string
	UserID string
	Reason string
}

func (e ErrQuotaExceeded) Error() string {
	return fmt.Sprintf("quota exceeded for tool %s (user: %s): %s", e.Tool, e.UserID, e.Reason)
}
