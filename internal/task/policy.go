package task

import (
	"fmt"
	"strings"
	"time"
)

type Policy struct {
	MaxChildren            int
	MaxConcurrent          int
	MaxDebateRounds        int
	AllowRecursiveDelegate bool
	ToolAllowlist          []string
	ToolDenylist           []string
	CWD                    string
	ApprovalPolicy         string
	Timeout                time.Duration
}

type PolicyRequest struct {
	Source        Source
	Mode          Mode
	ChildCount    int
	MaxConcurrent int
	DebateRounds  int
	Recursive     bool
	Tools         []string
	CWD           string
	Timeout       time.Duration
}

type PolicyDecision struct {
	Allowed bool     `json:"allowed"`
	Reasons []string `json:"reasons,omitempty"`
}

func (d PolicyDecision) Error() error {
	if d.Allowed {
		return nil
	}
	return fmt.Errorf("task policy denied: %s", strings.Join(d.Reasons, "; "))
}

func EvaluatePolicy(policy Policy, req PolicyRequest) PolicyDecision {
	var reasons []string
	if policy.MaxChildren > 0 && req.ChildCount > policy.MaxChildren {
		reasons = append(reasons, fmt.Sprintf("child count %d exceeds max %d", req.ChildCount, policy.MaxChildren))
	}
	if policy.MaxConcurrent > 0 && req.MaxConcurrent > policy.MaxConcurrent {
		reasons = append(reasons, fmt.Sprintf("concurrency %d exceeds max %d", req.MaxConcurrent, policy.MaxConcurrent))
	}
	if policy.MaxDebateRounds > 0 && req.DebateRounds > policy.MaxDebateRounds {
		reasons = append(reasons, fmt.Sprintf("debate rounds %d exceeds max %d", req.DebateRounds, policy.MaxDebateRounds))
	}
	if req.Recursive && !policy.AllowRecursiveDelegate {
		reasons = append(reasons, "recursive delegate is not allowed")
	}
	if policy.Timeout > 0 && req.Timeout > policy.Timeout {
		reasons = append(reasons, fmt.Sprintf("timeout %s exceeds max %s", req.Timeout, policy.Timeout))
	}
	if strings.TrimSpace(policy.CWD) != "" && strings.TrimSpace(req.CWD) != "" && !strings.HasPrefix(req.CWD, policy.CWD) {
		reasons = append(reasons, fmt.Sprintf("cwd %q is outside policy root %q", req.CWD, policy.CWD))
	}
	if denied := deniedTools(policy.ToolDenylist, req.Tools); len(denied) > 0 {
		reasons = append(reasons, "denied tools requested: "+strings.Join(denied, ", "))
	}
	if missing := missingAllowlistedTools(policy.ToolAllowlist, req.Tools); len(missing) > 0 {
		reasons = append(reasons, "tools outside allowlist: "+strings.Join(missing, ", "))
	}
	return PolicyDecision{
		Allowed: len(reasons) == 0,
		Reasons: reasons,
	}
}

func deniedTools(denylist []string, tools []string) []string {
	denied := make(map[string]struct{}, len(denylist))
	for _, tool := range denylist {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			denied[tool] = struct{}{}
		}
	}
	var out []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if _, ok := denied[tool]; ok {
			out = append(out, tool)
		}
	}
	return out
}

func missingAllowlistedTools(allowlist []string, tools []string) []string {
	if len(allowlist) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(allowlist))
	for _, tool := range allowlist {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			allowed[tool] = struct{}{}
		}
	}
	var out []string
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if _, ok := allowed[tool]; !ok {
			out = append(out, tool)
		}
	}
	return out
}
