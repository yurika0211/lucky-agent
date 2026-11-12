package agent

import (
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func TestMeasuredMillisKeepsAFastRound(t *testing.T) {
	if got := measuredMillis(0); got != 0 {
		t.Fatalf("unmeasured = %d", got)
	}
	if got := measuredMillis(time.Microsecond); got != 1 {
		t.Fatalf("sub-millisecond = %d", got)
	}
	if got := measuredMillis(1500 * time.Millisecond); got != 1500 {
		t.Fatalf("duration = %d", got)
	}
}

func TestAssistantUsageKeepsZeroTokenObject(t *testing.T) {
	if assistantUsage(nil) != nil {
		t.Fatal("nil usage should stay nil")
	}
	got := assistantUsage(&provider.TokenUsage{Model: "grok-4.7"})
	if got == nil || got.Model != "grok-4.7" || got.TotalTokens != 0 {
		t.Fatalf("usage = %#v", got)
	}
}

func TestStreamRoundStampsDurationAndTTFT(t *testing.T) {
	state := &streamConvergenceState{}
	state.noteRoundStart()
	time.Sleep(2 * time.Millisecond)
	state.noteFirstChunk()
	state.roundUsage = &provider.TokenUsage{Model: "m"}
	time.Sleep(2 * time.Millisecond)
	msg := provider.Message{Role: "assistant", Content: "answer"}
	state.stampAssistant(&msg)
	if msg.DurationMS < 1 {
		t.Fatalf("duration = %d", msg.DurationMS)
	}
	if msg.TTFTMS < 1 || msg.TTFTMS > msg.DurationMS {
		t.Fatalf("ttft %d duration %d", msg.TTFTMS, msg.DurationMS)
	}
	if msg.Usage == nil || msg.Usage.Model != "m" {
		t.Fatalf("usage = %#v", msg.Usage)
	}
	again := state.noteFirstChunk
	again()
	if state.roundTTFTMS() != msg.TTFTMS {
		t.Fatal("first chunk was overwritten")
	}
}
