package agent

import (
	"context"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/tool"
)

func TestHITLGateApprovalRoundTrip(t *testing.T) {
	gate := newHITLGate()
	done := make(chan HITLResolution, 1)
	go func() {
		res, err := gate.RequestAndWait(context.Background(), hitlRequest{
			Kind:      hitlKindApproval,
			SessionID: "s1",
			Tool:      "terminal",
			Reason:    "command: ls",
			Summary:   "需要批准工具调用: terminal",
		}, nil)
		if err != nil {
			t.Errorf("wait: %v", err)
			return
		}
		done <- res
	}()

	deadline := time.Now().Add(2 * time.Second)
	var pending []tool.PendingApproval
	for time.Now().Before(deadline) {
		pending = gate.list()
		if len(pending) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 {
		t.Fatalf("pending=%d", len(pending))
	}
	if _, err := gate.Resolve(pending[0].ID, "allow", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-done:
		if !isAllowDecision(res.Decision) {
			t.Fatalf("decision=%q", res.Decision)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for resolution")
	}
	if left := gate.list(); len(left) != 0 {
		t.Fatalf("expected empty gate, got %d", len(left))
	}
}

func TestHITLResolveTextInput(t *testing.T) {
	gate := newHITLGate()
	done := make(chan string, 1)
	go func() {
		res, err := gate.RequestAndWait(context.Background(), hitlRequest{
			Kind:      hitlKindInput,
			SessionID: "chat-1",
			Tool:      "ask_user",
			Prompt:    "目标路径？",
		}, nil)
		if err != nil {
			t.Errorf("wait: %v", err)
			return
		}
		done <- res.Input
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && len(gate.list()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	resolved, handled, err := gate.ResolveText("chat-1", "/tmp/project")
	if err != nil || !handled {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if resolved.Method != hitlKindInput {
		t.Fatalf("method=%s", resolved.Method)
	}
	select {
	case got := <-done:
		if got != "/tmp/project" {
			t.Fatalf("input=%q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out")
	}
}

func TestRequiresInteractiveApproval(t *testing.T) {
	if !requiresInteractiveApproval("terminal", nil) || !requiresInteractiveApproval("file_write", nil) {
		t.Fatal("state-changing tools must wait for the user")
	}
	if requiresInteractiveApproval("web_search", nil) || requiresInteractiveApproval("file_read", nil) {
		t.Fatal("read-only tools must not stall")
	}
	if requiresInteractiveApproval("http_request", map[string]any{"method": "GET"}) {
		t.Fatal("GET http_request must not stall")
	}
	if !requiresInteractiveApproval("http_request", map[string]any{"method": "POST"}) {
		t.Fatal("POST http_request must wait")
	}
	if requiresInteractiveApproval("memory_hygiene", map[string]any{"action": "audit"}) {
		t.Fatal("audit must not stall")
	}
	if !requiresInteractiveApproval("memory_hygiene", map[string]any{"action": "delete"}) {
		t.Fatal("memory delete must wait")
	}
}

func TestInterpretHITLReply(t *testing.T) {
	decision, _, ok := interpretHITLReply(hitlKindApproval, "允许")
	if !ok || decision != "allow" {
		t.Fatalf("allow parse: %q %v", decision, ok)
	}
	decision, _, ok = interpretHITLReply(hitlKindApproval, "随便说说")
	if ok {
		t.Fatal("unrelated approval text should not resolve")
	}
	decision, input, ok := interpretHITLReply(hitlKindInput, "用这个目录")
	if !ok || decision != "submit" || input != "用这个目录" {
		t.Fatalf("input parse: %q %q %v", decision, input, ok)
	}
}
