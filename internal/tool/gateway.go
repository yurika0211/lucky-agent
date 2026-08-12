package tool

import (
	"context"
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
	return g.ExecuteWithContext(name, args, userID, ExecutionContext{Context: context.Background(), UserID: userID})
}

// ExecuteWithContext is the request-aware gateway entry point. It preserves
// the legacy Execute behavior while allowing desktop tools to receive session,
// source, cancellation, and approval metadata.
func (g *Gateway) ExecuteWithContext(name string, args map[string]any, userID string, exec ExecutionContext) (*GatewayResult, error) {
	start := time.Now()
	if err := g.checkExecutable(name, userID); err != nil {
		return nil, err
	}

	if exec.Context == nil {
		exec.Context = context.Background()
	}
	if exec.UserID == "" {
		exec.UserID = userID
	}
	result, execErr := g.registry.CallDetailedWithContext(name, args, exec)
	duration := time.Since(start)
	g.recordUsage(name, userID, duration, execErr == nil)

	return &GatewayResult{
		ToolName:     name,
		Output:       result.Output,
		Metadata:     result.Metadata,
		Observations: result.Observations,
		Duration:     duration,
		Success:      execErr == nil,
		Timestamp:    start,
	}, execErr
}

func (g *Gateway) ExecuteWithShellContext(name string, args map[string]any, userID string, sc *ShellContext) (*GatewayResult, error) {
	return g.ExecuteWithShellExecutionContext(name, args, userID, sc, ExecutionContext{Context: context.Background(), UserID: userID})
}

// ExecuteWithShellExecutionContext combines shell injection with request-aware
// context dispatch.
func (g *Gateway) ExecuteWithShellExecutionContext(name string, args map[string]any, userID string, sc *ShellContext, exec ExecutionContext) (*GatewayResult, error) {
	start := time.Now()
	if err := g.checkExecutable(name, userID); err != nil {
		return nil, err
	}

	if exec.Context == nil {
		exec.Context = context.Background()
	}
	if exec.UserID == "" {
		exec.UserID = userID
	}
	result, execErr := g.registry.CallDetailedWithShellExecutionContext(name, args, sc, exec)
	duration := time.Since(start)
	g.recordUsage(name, userID, duration, execErr == nil)

	return &GatewayResult{
		ToolName:     name,
		Output:       result.Output,
		Metadata:     result.Metadata,
		Observations: result.Observations,
		Duration:     duration,
		Success:      execErr == nil,
		Timestamp:    start,
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
	ToolName     string
	Output       string
	Metadata     map[string]any
	Observations []Observation
	Duration     time.Duration
	Success      bool
	Timestamp    time.Time
}

func (r *GatewayResult) Format() string {
	status := "✅"
	if !r.Success {
		status = "❌"
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
