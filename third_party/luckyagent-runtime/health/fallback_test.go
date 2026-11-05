package health

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

type statusStub struct {
	status provider.ChainStatus
}

func (s statusStub) Status() provider.ChainStatus { return s.status }

func TestFallbackCheckReportsNodeCooldown(t *testing.T) {
	check := FallbackCheck(statusStub{status: provider.ChainStatus{
		ActiveIndex: 1,
		Nodes: []provider.NodeStatus{
			{
				Index: 0, Provider: "openai", Model: "grok-4.7",
				Endpoint: "https://gateway.example/v1", Credential: "abcd1234",
				ModelError: true, CooldownUntil: "2026-10-23T12:00:00Z",
				CircuitState: "Closed",
			},
			{
				Index: 1, Provider: "openai", Model: "claude",
				Endpoint: "https://other.example/v1", Credential: "ffff0000",
				Available: true, CircuitState: "Closed",
			},
		},
	}})
	result := check()
	if result.Status != StatusDegraded {
		t.Fatalf("status=%s", result.Status)
	}
	for _, want := range []string{"grok-4.7", "credential=abcd1234", "model cooling", "claude", "available"} {
		if !strings.Contains(result.Error, want) {
			t.Fatalf("missing %q in %s", want, result.Error)
		}
	}
}

func TestFallbackCheckUnhealthyWhenEveryNodeIsBlocked(t *testing.T) {
	check := FallbackCheck(statusStub{status: provider.ChainStatus{
		Nodes: []provider.NodeStatus{{
			Provider: "openai", Model: "grok-4.7", CircuitState: "Open", CircuitFailures: 5,
		}},
	}})
	result := check()
	if result.Status != StatusUnhealthy {
		t.Fatalf("status=%s error=%s", result.Status, result.Error)
	}
	if !strings.Contains(result.Error, "circuit=open failures=5") {
		t.Fatalf("error=%s", result.Error)
	}
}
