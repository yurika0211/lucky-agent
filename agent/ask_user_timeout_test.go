package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAskUserReplyBeforeOwnTimeout(t *testing.T) {
	agent := &Agent{hitl: newHITLGate()}
	parent, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	ctx := withHITLEmitter(parent, func(ChatEvent) {})

	type outcome struct {
		result detailedToolExecutionResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := agent.executeAskUserHITL(ctx, "s1", map[string]any{"prompt": "消融范围？"})
		done <- outcome{result, err}
	}()

	deadline := time.Now().Add(2 * time.Second)
	var pendingID string
	for time.Now().Before(deadline) {
		pending := agent.hitl.list()
		if len(pending) == 1 {
			pendingID = pending[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pendingID == "" {
		t.Fatal("ask_user did not register a pending input request")
	}
	if _, err := agent.hitl.Resolve(pendingID, "submit", "只跑主实验"); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("reply returned error: %v", got.err)
		}
		if !strings.Contains(got.result.Output, "只跑主实验") {
			t.Fatalf("output = %q", got.result.Output)
		}
		if _, ok := got.result.Metadata["ask_user"]; ok {
			t.Fatal("a reply must not be marked no_reply")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ask_user did not return after the reply")
	}
}

func TestAskUserTimeoutIsNoReplyNotToolError(t *testing.T) {
	agent := &Agent{hitl: newHITLGate()}
	// Parent deadline is longer than the tool wait. ask_user must still stop
	// on its own limit and must not surface context deadline exceeded.
	parent, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()

	var notices []string
	ctx := withHITLEmitter(parent, func(ev ChatEvent) {
		if ev.Type == ChatEventThinking && ev.Name == "ask_user" {
			notices = append(notices, ev.Content)
		}
	})

	prev := askUserWaitTimeout
	askUserWaitTimeout = 40 * time.Millisecond
	t.Cleanup(func() { askUserWaitTimeout = prev })

	start := time.Now()
	result, err := agent.executeAskUserHITL(ctx, "s1", map[string]any{"question": "睡眠数据？"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("timeout must not be a tool error: %v", err)
	}
	if parent.Err() != nil {
		t.Fatal("parent context was consumed by the ask_user wait")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("waited %s, want the tool's own limit", elapsed)
	}
	if len(notices) != 1 {
		t.Fatalf("waiting notices = %d (%q), want exactly one", len(notices), notices)
	}
	if !strings.Contains(notices[0], "正在等你回复") || !strings.Contains(notices[0], "秒") {
		t.Fatalf("notice = %q", notices[0])
	}
	status, _ := result.Metadata["ask_user"].(map[string]any)
	if status["status"] != "no_reply" || status["timed_out"] != true {
		t.Fatalf("metadata = %#v", result.Metadata["ask_user"])
	}
	if !strings.Contains(result.Output, "User did not reply") || !strings.Contains(result.Output, "Do not retry") {
		t.Fatalf("output = %q", result.Output)
	}
	if strings.Contains(result.Output, "context deadline exceeded") || strings.HasPrefix(result.Output, "Error:") {
		t.Fatalf("output looks like a retriable tool failure: %q", result.Output)
	}
	if len(agent.hitl.list()) != 0 {
		t.Fatal("timed-out ask_user left a pending request")
	}
}

func TestAskUserParentCancelStaysAnError(t *testing.T) {
	agent := &Agent{hitl: newHITLGate()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := agent.executeAskUserHITL(ctx, "s1", map[string]any{"prompt": "还要范围"})
		done <- err
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(agent.hitl.list()) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled ask_user did not return")
	}
}

func TestAskUserWaitBudgetLeavesParentMargin(t *testing.T) {
	if got := askUserWaitBudget(context.Background(), 10*time.Minute); got != 10*time.Minute {
		t.Fatalf("no deadline: %s", got)
	}
	short, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if got := askUserWaitBudget(short, 10*time.Minute); got != 0 {
		t.Fatalf("deadline inside margin: %s", got)
	}
	medium, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	got := askUserWaitBudget(medium, 10*time.Minute)
	if got < 25*time.Second || got > 30*time.Second {
		t.Fatalf("budget = %s, want just under the parent deadline", got)
	}
}
