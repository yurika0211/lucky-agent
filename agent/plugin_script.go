package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	pluginpkg "github.com/yurika0211/lucky-agent/agent/plugin"
	plugins "github.com/yurika0211/luckyagent-plugins"
	"github.com/yurika0211/luckyagent-runtime/sandbox"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/execpolicy"
)

// pluginSkillHandler never invokes the legacy SkillExecutor: that entry point
// starts unbounded processes without a request context or sandbox metadata.
func (a *Agent) pluginSkillHandler(skill *tool.SkillInfo, definition tool.SkillToolDef) (func(tool.ExecutionContext, map[string]any) (tool.ToolCallResult, error), error) {
	if skill == nil {
		return nil, fmt.Errorf("plugin skill is nil")
	}
	script, err := pluginpkg.SelectScript(skill.Dir, definition)
	if err != nil {
		return nil, fmt.Errorf("plugin skill %s: %w", skill.Name, err)
	}
	name := pluginResourceName("plugin_" + strings.ReplaceAll(skill.Name, ":", "_") + "_" + definition.Name)
	commandTokens := append([]string(nil), definition.Command...)
	return func(execContext tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
		if !execContext.AutoApprove {
			return tool.ToolCallResult{}, &tool.ApprovalRequiredError{Tool: name, Reason: "execute an installed plugin skill"}
		}
		if (execContext.Sandbox != nil && execContext.Sandbox.Restricted()) || (execContext.Sandbox == nil && a != nil && a.SandboxMode() != sandbox.ModeDev) {
			return tool.ToolCallResult{}, fmt.Errorf("plugin scripts cannot run in restricted sandbox mode")
		}
		ctx := execContext.Context
		if ctx == nil {
			ctx = context.Background()
		}
		if err := ctx.Err(); err != nil {
			return tool.ToolCallResult{}, err
		}
		if policy, ok := pluginPolicyFromContext(ctx); ok {
			encoded, err := json.Marshal(args)
			if err != nil {
				return tool.ToolCallResult{}, fmt.Errorf("plugin tool arguments: %w", err)
			}
			guard := execpolicy.New(policy.UserRequest, execpolicy.Options{DisabledTools: policy.Config.DisabledTools, BlockedActions: policy.Config.BlockedActions})
			// Check the registered name before classifying it as a skill run.
			// The plugin namespace must not bypass the turn's noSkillRun rule.
			for _, policyName := range []string{name, "skill_" + strings.ReplaceAll(skill.Name, ":", "_") + "_run"} {
				if reason := guard.BlockReason(execpolicy.Call{Name: policyName, Arguments: string(encoded)}); reason != "" {
					return tool.ToolCallResult{}, fmt.Errorf("plugin tool %s blocked by execution policy: %s", name, reason)
				}
			}
		}
		if script == "" {
			return tool.ToolCallResult{Output: "[Skill: " + skill.Name + "]\n" + plugins.PromptBody(skill.Summary)}, nil
		}
		timeout := pluginpkg.ScriptTimeout
		if policy, ok := pluginPolicyFromContext(ctx); ok && policy.Config.Timeout > 0 && policy.Config.Timeout < timeout {
			timeout = policy.Config.Timeout
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		argv, err := pluginpkg.Command(script, commandTokens)
		if err != nil {
			return tool.ToolCallResult{}, err
		}
		if raw, ok := args["args"].(string); ok {
			argv = append(argv, strings.Fields(raw)...)
		}
		env, err := pluginpkg.Environment(args)
		if err != nil {
			return tool.ToolCallResult{}, err
		}
		command := exec.CommandContext(ctx, argv[0], argv[1:]...)
		command.Dir = skill.Dir
		command.Env = env
		command.Stdin = nil
		command.WaitDelay = pluginpkg.ScriptWaitDelay
		cleanup, err := pluginpkg.PrepareProcess(command)
		if err != nil {
			return tool.ToolCallResult{}, err
		}
		defer cleanup()
		output := &pluginpkg.BoundedOutput{Limit: pluginpkg.ScriptOutputLimit}
		command.Stdout, command.Stderr = output, output
		err = command.Run()
		result := tool.ToolCallResult{Output: output.String()}
		if ctx.Err() != nil {
			return result, fmt.Errorf("plugin script canceled or timed out: %w", ctx.Err())
		}
		if err != nil {
			return result, fmt.Errorf("plugin script failed: %w", err)
		}
		return result, nil
	}, nil
}
