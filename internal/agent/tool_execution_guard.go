package agent

import (
	"github.com/yurika0211/luckyagent-tools/execpolicy"
	"github.com/yurika0211/luckyagent/internal/provider"
)

// toolExecutionGuard adapts provider calls to the shared turn policy.
type toolExecutionGuard struct{ core *execpolicy.Guard }

func (g *toolExecutionGuard) blockMessage(call provider.ToolCall) (string, bool) {
	if g == nil {
		return "", false
	}
	return g.core.BlockMessage(execpolicy.Call{Name: call.Name, Arguments: call.Arguments})
}

var parseToolCallArgs = execpolicy.ParseArgs
var guardStringArg = execpolicy.StringArg
