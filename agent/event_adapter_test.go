package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-runtime/turn"
	"github.com/yurika0211/luckyagent-tools/autonomy"
)

func TestTurnChatEventDurationRoundTrip(t *testing.T) {
	created := time.Date(2026, 10, 24, 1, 2, 3, 0, time.UTC)
	original := ChatEvent{
		Type:      ChatEventToolResult,
		Name:      "terminal",
		Result:    "命令超时",
		Duration:  30 * time.Second,
		Round:     2,
		CreatedAt: &created,
		Err:       errors.New("command timed out"),
	}

	neutral := ToTurnChatEvent(original)
	if neutral.Duration != 30*time.Second {
		t.Fatalf("neutral duration = %s", neutral.Duration)
	}
	if neutral.Type != turn.ChatEventToolResult || neutral.Result != "命令超时" {
		t.Fatalf("neutral event = %#v", neutral)
	}

	restored := FromTurnChatEvent(neutral)
	if restored.Duration != original.Duration || restored.Result != original.Result || restored.Err.Error() != original.Err.Error() {
		t.Fatalf("restored = %#v", restored)
	}

	unmeasured := ToTurnChatEvent(ChatEvent{Type: ChatEventToolCall, Name: "terminal"})
	if unmeasured.Duration != 0 {
		t.Fatalf("unmeasured duration = %s", unmeasured.Duration)
	}
}

func TestTurnChatEventStreamPreservesDuration(t *testing.T) {
	in := make(chan ChatEvent, 1)
	in <- ChatEvent{Type: ChatEventToolResult, Name: "terminal", Duration: 1250 * time.Millisecond}
	close(in)

	out := ToTurnChatEventStream(context.Background(), in)
	event, ok := <-out
	if !ok {
		t.Fatal("adapter closed before forwarding the event")
	}
	if event.Duration != 1250*time.Millisecond {
		t.Fatalf("stream duration = %s", event.Duration)
	}
	if _, ok := <-out; ok {
		t.Fatal("adapter did not close after the source")
	}
}

func TestDurableToolDurationMatchesPersistedResult(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed execution", true: "completed replay"}[replay], func(t *testing.T) {
			a, sess, q, _, cfg := durableHarness(t, pendingCheckpoint())
			calls := 0
			a.tools.Register(&tool.Tool{Name: "perform_operation", Permission: tool.PermAuto,
				Handler: func(map[string]any) (string, error) {
					calls++
					<-time.After(2 * time.Millisecond)
					return "", errors.New("command timed out")
				}})
			if replay {
				op, err := cfg.Execution.BeginOperation(turn.DurableOperation{ID: "step-1-1", Name: "perform_operation", Arguments: "{}"})
				if err != nil {
					t.Fatal(err)
				}
				evidence, err := json.Marshal(durableToolEvidence{Result: detailedToolExecutionResult{
					Output: "saved result", Duration: 1500 * time.Millisecond,
				}, Arguments: "{}"})
				if err != nil {
					t.Fatal(err)
				}
				if err := cfg.Execution.FinishOperation(op.ID, "saved result", false, evidence); err != nil {
					t.Fatal(err)
				}
				resumeDurableQueue(t, q, &cfg)
			}
			var results []ChatEvent
			cfg.MaxIterations = 1
			cfg.eventContext = context.Background()
			cfg.emit = func(_ context.Context, event ChatEvent) {
				if event.Type == ChatEventToolResult {
					results = append(results, event)
				}
			}
			_, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
			if !errors.Is(err, autonomy.ErrYield) || len(results) != 1 {
				t.Fatalf("result events=%v error=%v", results, err)
			}
			event := results[0]
			if event.Duration <= 0 || ToTurnChatEvent(event).Duration != event.Duration {
				t.Fatalf("duration lost at event boundary: %+v", event)
			}
			if replay && (calls != 0 || event.Duration != 1500*time.Millisecond) {
				t.Fatalf("replay re-executed or re-timed: calls=%d duration=%s", calls, event.Duration)
			}
			if !replay && (calls != 1 || !strings.Contains(event.Result, "command timed out")) {
				t.Fatalf("failure result lost: calls=%d event=%+v", calls, event)
			}
			messages, err := sess.GetMessagesWithError()
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range messages {
				if message.Role == "tool" && message.Name == event.Name {
					if message.DurationMS != event.Duration.Milliseconds() {
						t.Fatalf("stored duration=%d event=%s", message.DurationMS, event.Duration)
					}
					return
				}
			}
			t.Fatal("no persisted tool result")
		})
	}
}
