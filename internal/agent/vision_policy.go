package agent

import "github.com/yurika0211/luckyagent/internal/sandbox"

// applyVisionToolPolicy uses the same decision as context packing, per turn.
// Registry entries are not mutated: concurrent sessions may use different models.
func (a *Agent) applyVisionToolPolicy(cfg *LoopConfig, snapshot providerSnapshot) {
	blocked := "image_read"
	if newContextPlannerWithProvider(a, defaultContextBuildOptions(), snapshot).supportsImageContentParts() {
		blocked = "image_analyze"
	}
	cfg.DisabledTools = normalizeToolNameList(append(cfg.DisabledTools, blocked))
}

func newTurnToolGuard(text string, disabled []string, mode ...sandbox.Mode) *toolExecutionGuard {
	guard := newToolExecutionGuard(text)
	if guard == nil {
		guard = &toolExecutionGuard{}
	}
	guard.disabledTools = append([]string(nil), disabled...)
	if len(mode) > 0 && mode[0] == sandbox.ModeDev {
		guard.relaxReadOnly = true
	}
	return guard
}
