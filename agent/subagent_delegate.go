package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	thoroughness := delegateStringArg(args, "thoroughness")
	if err := validateDelegateThoroughness(roleName, thoroughness); err != nil {
		return subagentRole{}, err
	}
	if roleName == "" {
		return subagentRole{}, nil
	}
	role, err := a.loadDelegateRole(workspace, roleName)
	if err != nil {
		return subagentRole{}, err
	}
	if _, err := a.loopConfigForRole(role, delegateBoolArg(args, "allow_recursive_delegate")); err != nil {
		return subagentRole{}, err
	}
	if thoroughness != "" {
		role.Prompt = strings.TrimSpace(role.Prompt + "\n\n" + exploreThoroughnessNote(thoroughness))
	}
	return role, nil
}

// loadDelegateRole prefers a project or user file. Builtin explore and plan
// fill in only when no file uses that name and delegate.builtin_agents is on.
func (a *Agent) loadDelegateRole(workspace, roleName string) (subagentRole, error) {
	role, err := loadSubagentRole(a.delegateHome(), workspace, roleName)
	if err == nil {
		return role, nil
	}
	if !isMissingSubagentRole(err) {
		return subagentRole{}, err
	}
	if !a.builtinAgentsEnabled() {
		return subagentRole{}, fmt.Errorf("subagent role %q was not found", roleName)
	}
	builtin, ok := loadBuiltinSubagentRole(roleName)
	if !ok {
		return subagentRole{}, err
	}
	return builtin, nil
}

func (a *Agent) resolveSubagentRole(roleName, workspace string, allowRecursive bool) (LoopConfig, error) {
	role, err := a.loadDelegateRole(workspace, roleName)
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
		Description: "Optional role. explore and plan are built in and read-only. A project or user file subagents/<name>.md with the same name replaces the builtin. Omit to keep the default child tool set.",
		Required:    false,
	}
	current.Parameters["thoroughness"] = tool.Param{
		Type:        "string",
		Description: "Only for subagent_type=explore. quick, medium, or very_thorough. Changes how widely explore searches. Rejected for plan and for a delegate without a type.",
		Required:    false,
	}
	inner := current.ContextDetailedHandler
	current.ContextDetailedHandler = func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
		if strings.EqualFold(delegateStringArg(args, "action"), "status") {
			result, err := inner(exec, args)
			if err != nil {
				return result, err
			}
			result.Output = a.annotateDelegateStatus(result.Output)
			return result, nil
		}
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

// builtinSubagentDir is the repo copy of explore and plan. Tests and a
// source checkout read it directly. A released binary falls back to the
// copies embedded below, which match those files.
func builtinSubagentDir() string {
	candidates := []string{
		filepath.Join("agent", "subagents"),
		"subagents",
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "agent", "subagents"),
			filepath.Join(cwd, "subagents"),
		)
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "subagents"))
	}
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, "explore.md")); err == nil && !info.IsDir() {
			return dir
		}
	}
	return ""
}

func loadBuiltinSubagentRole(name string) (subagentRole, bool) {
	name = strings.TrimSpace(name)
	if dir := builtinSubagentDir(); dir != "" {
		role, err := parseSubagentRoleFile(filepath.Join(dir, name+".md"))
		if err == nil && role.Name == name {
			role.Path = "builtin:" + name
			return role, true
		}
	}
	body, ok := embeddedBuiltinSubagent[name]
	if !ok {
		return subagentRole{}, false
	}
	path := filepath.Join(os.TempDir(), "luckyagent-builtin-subagents", name+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return subagentRole{}, false
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return subagentRole{}, false
	}
	role, err := parseSubagentRoleFile(path)
	if err != nil || role.Name != name {
		return subagentRole{}, false
	}
	role.Path = "builtin:" + name
	return role, true
}

// embeddedBuiltinSubagent mirrors agent/subagents/*.md for a binary that is
// not sitting next to the source tree.
var embeddedBuiltinSubagent = map[string]string{
	"explore": "---\nname: explore\ndescription: Read-only search of the workspace. Returns paths, conclusions, and gaps.\ntools: [file_read, file_list, rag_search, memory, web_search, web_fetch]\n---\nSearch, then read. Do not edit files, run commands, or delegate.\n\nReturn three parts:\n- Paths you actually read, with why each one matters.\n- Conclusions that those paths support.\n- Gaps that a later change would still need to check.\n\nMemory is recall only. Do not write a new memory. Thoroughness changes how widely you search, not which tools you may call.\n",
	"plan":    "---\nname: plan\ndescription: Read-only change plan with evidence. Does not edit.\ntools: [file_read, file_list, rag_search, memory, web_search, web_fetch]\n---\nWrite a change plan. Do not edit files, run commands, or delegate.\n\nThe plan names the files to change, the evidence for each change, and what you did not verify. Memory is recall only. Do not write a new memory.\n",
}

func validateDelegateThoroughness(roleName, thoroughness string) error {
	if thoroughness == "" {
		return nil
	}
	if roleName != "explore" {
		return fmt.Errorf("thoroughness is only valid for subagent_type explore")
	}
	switch thoroughness {
	case "quick", "medium", "very_thorough":
		return nil
	default:
		return fmt.Errorf("thoroughness %q is not valid (use quick, medium, or very_thorough)", thoroughness)
	}
}

func exploreThoroughnessNote(level string) string {
	switch level {
	case "quick":
		return "Search scope: quick. Check the nearest matching files and stop once the question is answered."
	case "very_thorough":
		return "Search scope: very thorough. Search the whole workspace and follow related references before you close a gap."
	default:
		return "Search scope: medium. Search the area around the question and read the files that can confirm it."
	}
}

// builtinAgentsEnabled reads delegate.builtin_agents from the config file.
// The published config struct does not carry this switch, so a missing file
// or a missing key stays enabled. false is the only off value.
func (a *Agent) builtinAgentsEnabled() bool {
	if a == nil || a.cfg == nil {
		return true
	}
	path := filepath.Join(a.cfg.HomeDir(), "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return true
	}
	delegate, _ := raw["delegate"].(map[string]any)
	if delegate == nil {
		return true
	}
	value, ok := delegate["builtin_agents"]
	if !ok {
		return true
	}
	enabled, ok := value.(bool)
	if !ok {
		return true
	}
	return enabled
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
