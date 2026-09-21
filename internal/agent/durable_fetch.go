package agent

import (
	"github.com/yurika0211/luckyagent/internal/provider"
)

// Fetch deduplication uses persisted observations, so slice boundaries do not
// reset the existing duplicate_fetch_limit protection.
func durableCachedFetch(cfg LoopConfig, call provider.ToolCall) (string, bool, error) {
	key := normalizedToolTarget(call.Name, call.Arguments)
	if key == "" || cfg.DuplicateFetchLimit <= 0 {
		return "", false, nil
	}
	task, err := cfg.Execution.Snapshot()
	if err != nil {
		return "", false, err
	}
	count, last := 0, ""
	for _, op := range task.Operations {
		if op.State != "completed" || op.Failed || normalizedToolTarget(op.Name, op.Arguments) != key {
			continue
		}
		count++
		last = op.Output
	}
	if count < cfg.DuplicateFetchLimit {
		return "", false, nil
	}
	// Keep the confirmed observation unchanged, including on later replays.
	return last, true, nil
}
