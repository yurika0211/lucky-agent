package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeartbeatTriggerDefaultsToDryRun(t *testing.T) {
	calls := 0
	svc := NewHeartbeatToolService(func(args map[string]any) (string, error) {
		calls++
		return `{"executed_tasks":1}`, nil
	}, nil)

	out, err := svc.handleTrigger(map[string]any{})
	if err != nil {
		t.Fatalf("handleTrigger dry-run: %v", err)
	}
	if calls != 0 {
		t.Fatalf("dry-run should not call trigger handler, got calls=%d", calls)
	}
	payload := decodeHeartbeatToolJSON(t, out)
	if payload["dry_run"] != true || payload["executed"] != false {
		t.Fatalf("unexpected dry-run payload: %v", payload)
	}
}

func TestHeartbeatTriggerExecuteCallsHandler(t *testing.T) {
	calls := 0
	svc := NewHeartbeatToolService(func(args map[string]any) (string, error) {
		calls++
		return `{"executed_tasks":2,"due_tasks":2}`, nil
	}, nil)

	out, err := svc.handleTrigger(map[string]any{
		"dry_run": false,
		"execute": true,
	})
	if err != nil {
		t.Fatalf("handleTrigger execute: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected trigger handler once, got calls=%d", calls)
	}
	payload := decodeHeartbeatToolJSON(t, out)
	if payload["dry_run"] != false || payload["executed"] != true {
		t.Fatalf("unexpected execute payload: %v", payload)
	}
	if payload["executed_tasks"] != float64(2) {
		t.Fatalf("expected merged executed_tasks=2, got %v", payload["executed_tasks"])
	}
}

func TestHeartbeatStatusWrapsHandlerOutput(t *testing.T) {
	svc := NewHeartbeatToolService(nil, func(args map[string]any) (string, error) {
		return `{"enabled":true,"active_tasks":3}`, nil
	})

	out, err := svc.handleStatus(map[string]any{})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	payload := decodeHeartbeatToolJSON(t, out)
	if payload["ok"] != true || payload["action"] != "status" || payload["handler_configured"] != true {
		t.Fatalf("unexpected status payload: %v", payload)
	}
	if payload["enabled"] != true || payload["active_tasks"] != float64(3) {
		t.Fatalf("expected merged status fields, got %v", payload)
	}
}

func TestHeartbeatHandlersMissing(t *testing.T) {
	svc := NewHeartbeatToolService(nil, nil)

	if _, err := svc.handleTrigger(map[string]any{}); err == nil || !strings.Contains(err.Error(), "heartbeat trigger handler not configured") {
		t.Fatalf("expected trigger handler error, got %v", err)
	}
	if _, err := svc.handleStatus(map[string]any{}); err == nil || !strings.Contains(err.Error(), "heartbeat status handler not configured") {
		t.Fatalf("expected status handler error, got %v", err)
	}
}

func decodeHeartbeatToolJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("decode JSON %q: %v", out, err)
	}
	return payload
}
