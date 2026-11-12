package agent

import (
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

// measuredMillis returns a persisted duration. A finished round that was
// faster than 1ms is still recorded as 1 so a missing field stays distinct
// from a measured round.
func measuredMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	ms := d.Milliseconds()
	if ms < 1 {
		return 1
	}
	return ms
}

// assistantUsage returns the usage to persist. A provider object is kept
// even when every token counter is zero.
func assistantUsage(usage *provider.TokenUsage) *provider.TokenUsage {
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

func (s *streamConvergenceState) stampAssistant(msg *provider.Message) {
	if s == nil || msg == nil {
		return
	}
	msg.DurationMS = s.roundDurationMS()
	msg.TTFTMS = s.roundTTFTMS()
	msg.Usage = assistantUsage(s.roundUsage)
}
