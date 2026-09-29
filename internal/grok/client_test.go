package grok

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManagerSessionTurnAndApproval(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-grok.sh")
	body := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$FAKE_GROK_LOG"
  case "$line" in
    *'"method":"initialize"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
      printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"protocolVersion\":1}}"
      ;;
    *'"method":"session/new"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
      printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"sessionId\":\"sess-1\"}}"
      ;;
    *'"method":"session/prompt"'*)
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
      printf '%s\n' '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess-1","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello"}}}}'
      printf '%s\n' '{"jsonrpc":"2.0","id":"perm-1","method":"session/request_permission","params":{"sessionId":"sess-1","title":"run tests","toolCall":{"title":"bash"}}}'
      IFS= read -r decision
      printf '%s\n' "$decision" >> "$FAKE_GROK_LOG"
      printf '%s\n' "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"stopReason\":\"end_turn\"}}"
      ;;
  esac
done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "rpc.log")
	t.Setenv("FAKE_GROK_LOG", logPath)

	mgr := NewManager(Config{
		Command:      []string{"sh", script},
		ApprovalMode: "gateway",
		CWDAllowlist: []string{dir},
		MaxEvents:    32,
	})
	t.Cleanup(func() { _ = mgr.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := mgr.StartSession(ctx, dir, "")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if session.ID != "sess-1" {
		t.Fatalf("session id = %q", session.ID)
	}

	done := make(chan error, 1)
	go func() {
		_, err := mgr.StartTurn(ctx, session.ID, "say hello", "")
		done <- err
	}()

	var approvalID string
	deadline := time.Now().Add(3 * time.Second)
	for approvalID == "" && time.Now().Before(deadline) {
		summary, err := mgr.Summary(session.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if pending, ok := summary["pending_approvals"].([]Approval); ok && len(pending) > 0 {
			approvalID = pending[0].ID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if approvalID == "" {
		t.Fatal("approval was not recorded")
	}
	if _, err := mgr.RespondApproval(ctx, approvalID, "allow"); err != nil {
		t.Fatalf("respond approval: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("start turn: %v", err)
	}

	summary, err := mgr.Summary(session.ID, "turn-1")
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := summary["turn"].(Turn)
	if !ok {
		t.Fatalf("summary turn missing: %#v", summary["turn"])
	}
	if turn.Status != "completed" || turn.Output != "hello" {
		t.Fatalf("turn = %+v", turn)
	}
	events, err := mgr.Events(session.ID, "", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if !strings.Contains(string(raw), "agent_message_chunk") {
		t.Fatalf("events missing message chunk: %s", raw)
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logged), `"outcome":"selected"`) {
		t.Fatalf("approval response was not sent: %s", logged)
	}
}

func TestCWDAllowlist(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(Config{CWDAllowlist: []string{dir}})
	if _, err := mgr.validateCWD(filepath.Join(dir, "..")); err == nil {
		t.Fatal("expected cwd outside allowlist to fail")
	}
	got, err := mgr.validateCWD(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir && !strings.HasPrefix(got, dir) {
		t.Fatalf("validated cwd = %s", got)
	}
}
