package agent

import (
	"os"
	"strings"

	"github.com/yurika0211/luckyagent-tools/execpolicy"
)

const toolIntentGatingEnv = "LH_TOOL_INTENT_GATING"

func (a *Agent) applyIntentToolGating(loopCfg *LoopConfig, routingText string) {
	if !toolIntentGatingEnabled() {
		return
	}
	if loopCfg == nil || a == nil || a.tools == nil {
		return
	}
	visibleTools := a.tools.ListModelVisible()
	if len(visibleTools) == 0 {
		return
	}

	allowed, recognized := a.intentAllowedTools(routingText)
	if !recognized {
		return
	}

	explicit := makeToolNameSet(a.extractRequiredToolNames(routingText))
	for name := range explicit {
		allowed[name] = struct{}{}
	}

	disabled := append([]string(nil), loopCfg.DisabledTools...)
	disabledSet := makeToolNameSet(disabled)
	if disabledSet == nil {
		disabledSet = map[string]struct{}{}
	}
	for _, t := range visibleTools {
		if t == nil {
			continue
		}
		name := strings.TrimSpace(t.Name)
		if name == "" || toolNameInSet(disabledSet, name) {
			continue
		}
		if _, ok := allowed[name]; ok {
			continue
		}
		disabled = append(disabled, name)
		disabledSet[name] = struct{}{}
	}
	loopCfg.DisabledTools = normalizeToolNameList(disabled)
}

func toolIntentGatingEnabled() bool {
	value := strings.TrimSpace(os.Getenv(toolIntentGatingEnv))
	if value == "" {
		return false
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on", "enable", "enabled":
		return true
	default:
		return false
	}
}

func (a *Agent) intentAllowedTools(input string) (map[string]struct{}, bool) {
	var skillRunTools []string
	if a != nil && a.tools != nil {
		for _, t := range a.tools.ListModelVisible() {
			if t == nil {
				continue
			}
			name := strings.TrimSpace(t.Name)
			if strings.HasPrefix(name, "skill_") && strings.HasSuffix(name, "_run") {
				skillRunTools = append(skillRunTools, name)
			}
		}
	}
	return execpolicy.AllowedTools(input, skillRunTools)
}

var intentTextContainsAny = execpolicy.ContainsAny
