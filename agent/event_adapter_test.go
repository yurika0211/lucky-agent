package agent

import (
	"errors"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/turn"
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
