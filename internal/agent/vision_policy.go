package agent

import (
	"github.com/yurika0211/luckyagent-tools/execpolicy"
	"github.com/yurika0211/luckyagent/internal/sandbox"
)

// applyVisionToolPolicy uses the same decision as context packing, per turn.
// Registry entries are not mutated: concurrent sessions may use different models.
func (a *Agent) applyVisionToolPolicy(cfg *LoopConfig, snapshot providerSnapshot) {
	blocked := "read"
	if newContextPlannerWithProvider(a, defaultContextBuildOptions(), snapshot).supportsImageContentParts() {
		blocked = "analyze"
	}
	if cfg.BlockedActions == nil {
		cfg.BlockedActions = map[string][]string{}
	}
	cfg.BlockedActions["image"] = append(cfg.BlockedActions["image"], blocked)
}

func newTurnToolGuard(text string, disabled []string, blocked map[string][]string, mode ...sandbox.Mode) *toolExecutionGuard {
	opts := execpolicy.Options{DisabledTools: disabled, BlockedActions: blocked}
	opts.RelaxReadOnly = len(mode) > 0 && mode[0] == sandbox.ModeDev
	return &toolExecutionGuard{core: execpolicy.New(text, opts)}
}
