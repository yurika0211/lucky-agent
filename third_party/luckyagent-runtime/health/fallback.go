package health

import (
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

// FallbackStatusSource is the fallback chain snapshot used by health checks.
type FallbackStatusSource interface {
	Status() provider.ChainStatus
}

// FallbackCheck reports each fallback node's cooldown and circuit state.
// Liveness stays process-level. Readiness and detail include this check when
// the host registers it.
func FallbackCheck(source FallbackStatusSource) CheckFunc {
	return func() CheckResult {
		result := CheckResult{Name: "fallback", Status: StatusHealthy}
		if source == nil {
			result.Status = StatusDegraded
			result.Error = "fallback chain is not configured"
			return result
		}
		status := source.Status()
		if len(status.Nodes) == 0 {
			result.Status = StatusDegraded
			result.Error = "fallback chain is empty"
			return result
		}
		parts := make([]string, 0, len(status.Nodes))
		available := 0
		for _, node := range status.Nodes {
			if node.Available && !circuitOpen(node.CircuitState) {
				available++
			} else {
				result.Status = StatusDegraded
			}
			parts = append(parts, formatNode(node))
		}
		if available == 0 {
			result.Status = StatusUnhealthy
		}
		result.Error = fmt.Sprintf("active=%d %s", status.ActiveIndex, strings.Join(parts, "; "))
		return result
	}
}

func circuitOpen(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "open", "halfopen":
		return true
	default:
		return false
	}
}

func formatNode(node provider.NodeStatus) string {
	state := "available"
	if !node.Available {
		state = "cooling"
		if node.CooldownUntil != "" {
			if until, err := time.Parse(time.RFC3339, node.CooldownUntil); err == nil {
				state = "cooling until " + until.UTC().Format(time.RFC3339)
			}
		}
	}
	if node.ModelError && !node.Available {
		state = "model " + state
	}
	circuit := ""
	if node.CircuitState != "" {
		circuit = fmt.Sprintf(" circuit=%s failures=%d", strings.ToLower(node.CircuitState), node.CircuitFailures)
	}
	return fmt.Sprintf("#%d %s model=%s endpoint=%s credential=%s %s%s",
		node.Index, emptyDash(node.Provider), emptyDash(node.Model), emptyDash(node.Endpoint), emptyDash(node.Credential), state, circuit)
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
