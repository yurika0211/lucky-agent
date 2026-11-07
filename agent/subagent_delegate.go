package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/collab"
)

// prepareSubagentDelegate runs before a delegate task is created. An absent
// subagent_type leaves the published tool unchanged. A named role that is
// missing, has no name, or resolves to zero tools fails here so no task is
// recorded. The returned role is stamped onto the task context so the child
// loop does not look the file up a second time.
func (a *Agent) prepareSubagentDelegate(args map[string]any, workspace string) (subagentRole, error) {
	roleName := delegateStringArg(args, "subagent_type")
	if roleName == "" {
		return subagentRole{}, nil
	}
	role, err := loadSubagentRole(a.delegateHome(), workspace, roleName)
	if err != nil {
		return subagentRole{}, err
	}
	if _, err := a.loopConfigForRole(role, delegateBoolArg(args, "allow_recursive_delegate")); err != nil {
		return subagentRole{}, err
	}
	return role, nil
}

func (a *Agent) resolveSubagentRole(roleName, workspace string, allowRecursive bool) (LoopConfig, error) {
	role, err := loadSubagentRole(a.delegateHome(), workspace, roleName)
	if err != nil {
		return LoopConfig{}, err
	}
	return a.loopConfigForRole(role, allowRecursive)
}

func (a *Agent) loopConfigForRole(role subagentRole, allowRecursive bool) (LoopConfig, error) {
	delegateCfg := tool.DefaultDelegateConfig()
	if a != nil && a.delegate != nil {
		delegateCfg = a.delegate.Config()
	}
	recursive := allowRecursive && delegateCfg.ChildAllowRecursive
	cfg := buildDelegateChildLoopConfig(delegateCfg, recursive)
	var err error
	canonical := func(name string) string { return name }
	if a != nil {
		canonical = a.canonicalToolName
	}
	cfg, err = applySubagentRole(cfg, role, recursive, registeredToolNames(a.toolsOrNil()), canonical)
	if err != nil {
		return LoopConfig{}, err
	}
	cfg.subagentModel = role.Model
	return cfg, nil
}

func (a *Agent) toolsOrNil() *tool.Registry {
	if a == nil {
		return nil
	}
	return a.tools
}

// childLoopForDelegate builds the loop config for one delegated execution.
// Role metadata is read from the task context marker written at delegation
// time. Without that marker the config matches buildDelegateChildLoopConfig.
// The marker already carries the resolved prompt and model, so a role file
// that disappears between create and start still fails closed only when the
// name itself can no longer be loaded.
func (a *Agent) childLoopForDelegate(delegateCfg tool.DelegateConfig, allowRecursive bool, contextStr, workspace string) (LoopConfig, string, error) {
	roleName, model, prompt, markedWorkspace, clean := takeSubagentRoleMarker(contextStr)
	cfg := buildDelegateChildLoopConfig(delegateCfg, allowRecursive)
	if roleName == "" {
		return cfg, clean, nil
	}
	if markedWorkspace != "" {
		workspace = markedWorkspace
	}
	resolved, err := a.resolveSubagentRole(roleName, workspace, allowRecursive)
	if err != nil {
		return LoopConfig{}, clean, err
	}
	if strings.TrimSpace(prompt) != "" {
		resolved.subagentRolePrompt = prompt
	}
	if model != "" {
		resolved.subagentModel = model
	}
	return resolved, clean, nil
}

const subagentRoleMarkerPrefix = "\x00subagent_role:"

type subagentRoleMarker struct {
	Name      string `json:"name"`
	Model     string `json:"model,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Workspace string `json:"workspace,omitempty"`
}

// withSubagentRoleMarker appends a private trailer the child executor reads
// back off. The published task context shown to the model stays unchanged
// because the trailer is stripped before the prompt is built. Workspace is
// the parent directory the role was loaded from; the child session cwd is a
// fresh delegate workspace and must not replace it.
func withSubagentRoleMarker(contextStr string, role subagentRole, workspace string) string {
	payload, err := json.Marshal(subagentRoleMarker{Name: role.Name, Model: role.Model, Prompt: role.Prompt, Workspace: workspace})
	if err != nil {
		return contextStr
	}
	return contextStr + subagentRoleMarkerPrefix + string(payload)
}

func takeSubagentRoleMarker(contextStr string) (name, model, prompt, workspace, clean string) {
	at := strings.LastIndex(contextStr, subagentRoleMarkerPrefix)
	if at < 0 {
		return "", "", "", "", contextStr
	}
	var marker subagentRoleMarker
	if err := json.Unmarshal([]byte(contextStr[at+len(subagentRoleMarkerPrefix):]), &marker); err != nil || strings.TrimSpace(marker.Name) == "" {
		return "", "", "", "", contextStr
	}
	return marker.Name, strings.TrimSpace(marker.Model), strings.TrimSpace(marker.Prompt), strings.TrimSpace(marker.Workspace), contextStr[:at]
}

// installSubagentDelegateGuard wraps the registered subagent tool so
// action=delegate validates a role before the tools module creates a task.
// subagent_type is added to the schema the model sees. The tools module
// still accepts the key when a caller passes it without that advertisement.
func (a *Agent) installSubagentDelegateGuard() {
	if a == nil || a.tools == nil {
		return
	}
	current, ok := a.tools.Get("subagent")
	if !ok || current == nil || current.ContextDetailedHandler == nil {
		return
	}
	if current.Parameters == nil {
		current.Parameters = map[string]tool.Param{}
	}
	current.Parameters["subagent_type"] = tool.Param{
		Type:        "string",
		Description: "Optional role file name. Loads subagents/<name>.md from the project .luckyagent directory, then the user config directory. Omit to keep the default child tool set.",
		Required:    false,
	}
	inner := current.ContextDetailedHandler
	current.ContextDetailedHandler = func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
		if strings.EqualFold(delegateStringArg(args, "action"), "delegate") {
			workspace := subagentRoleWorkspace(a.delegateParentSession(exec.SessionID))
			role, err := a.prepareSubagentDelegate(args, workspace)
			if err != nil {
				return tool.ToolCallResult{}, err
			}
			if role.Name != "" {
				args = cloneDelegateArgs(args)
				args["context"] = withSubagentRoleMarker(delegateStringArg(args, "context"), role, workspace)
			}
		}
		return inner(exec, args)
	}
}

func (a *Agent) delegateHome() string {
	if a == nil || a.cfg == nil {
		return ""
	}
	return a.cfg.HomeDir()
}

func (a *Agent) delegateParentSession(id string) *session.Session {
	if a == nil || a.sessions == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	sess, ok := a.sessions.Get(id)
	if !ok {
		return nil
	}
	return sess
}

func cloneDelegateArgs(args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+1)
	for key, value := range args {
		out[key] = value
	}
	return out
}

func delegateStringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func delegateBoolArg(args map[string]any, key string) bool {
	if args == nil {
		return false
	}
	switch value := args[key].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

// childProviderForRole returns the parent snapshot unless the role named a
// model. The named model is built for this child only and does not replace
// the process default.
func (a *Agent) childProviderForRole(parent providerSnapshot, model string) (providerSnapshot, error) {
	model = strings.TrimSpace(model)
	if model == "" || model == strings.TrimSpace(parent.model) {
		return parent, nil
	}
	return a.collaborationProvider(collab.AgentRuntimeProfile{Model: model})
}

// runDelegatedChild executes one child loop with the role's tool policy and
// model already applied. The session profile is set before the loop starts.
func (a *Agent) runDelegatedChild(ctx context.Context, loop *Loop, sess *session.Session, prompt string, cfg LoopConfig) (*LoopResult, error) {
	profile := strings.TrimSpace(cfg.subagentRolePrompt)
	if profile == "" {
		profile = delegatedSubagentSystemPrompt()
	}
	role := "subagent"
	if strings.HasPrefix(cfg.ProfileSource, "subagent_role:") {
		role = strings.TrimPrefix(cfg.ProfileSource, "subagent_role:")
	}
	if sess != nil {
		sess.SetAgentProfile(role, profile)
	}
	snapshot := a.baseProviderSnapshot()
	if cfg.subagentModel != "" {
		resolved, err := a.childProviderForRole(snapshot, cfg.subagentModel)
		if err != nil {
			return nil, err
		}
		snapshot = resolved
	}
	return loop.RunWithProviderSnapshot(ctx, sess, TextUserTurnInput(prompt), cfg, snapshot)
}
