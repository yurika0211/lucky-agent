package task

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluatePolicyAllowsWithinLimits(t *testing.T) {
	decision := EvaluatePolicy(Policy{
		MaxChildren:   3,
		MaxConcurrent: 2,
		ToolAllowlist: []string{"read_file", "go_test"},
		ToolDenylist:  []string{"delete_file"},
		Timeout:       time.Minute,
	}, PolicyRequest{
		ChildCount:    2,
		MaxConcurrent: 2,
		Tools:         []string{"read_file"},
		Timeout:       30 * time.Second,
	})
	if !decision.Allowed {
		t.Fatalf("expected allowed, got %+v", decision)
	}
}

func TestEvaluatePolicyDeniesConstraintViolations(t *testing.T) {
	decision := EvaluatePolicy(Policy{
		MaxChildren:            1,
		MaxConcurrent:          1,
		MaxDebateRounds:        2,
		AllowRecursiveDelegate: false,
		ToolAllowlist:          []string{"read_file"},
		ToolDenylist:           []string{"delete_file"},
		Timeout:                time.Minute,
	}, PolicyRequest{
		ChildCount:    2,
		MaxConcurrent: 3,
		DebateRounds:  3,
		Recursive:     true,
		Tools:         []string{"read_file", "delete_file", "write_file"},
		Timeout:       2 * time.Minute,
	})
	if decision.Allowed {
		t.Fatal("expected policy denial")
	}
	text := strings.Join(decision.Reasons, "\n")
	for _, want := range []string{"child count", "concurrency", "debate rounds", "recursive", "timeout", "denied tools", "outside allowlist"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in reasons: %s", want, text)
		}
	}
	if err := decision.Error(); err == nil {
		t.Fatal("expected error for denied decision")
	}
}
