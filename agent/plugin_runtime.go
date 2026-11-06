package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	plugins "github.com/yurika0211/luckyagent-plugins"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/hook"
	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/mcpclient"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"gopkg.in/yaml.v3"
)

const pluginCategory tool.Category = "plugin"

// A runtime consumes one verified installation snapshot at startup. CLI changes
// intentionally require restart, so running turns cannot lose their resources.
type pluginRuntime struct {
	bundles  []*plugins.Bundle
	commands map[string]plugins.Prompt
	agents   map[string]plugins.Prompt
	trusted  map[string]bool
}

func (a *Agent) preparePlugins() error {
	if a == nil || a.cfg == nil {
		return nil
	}
	root := filepath.Join(a.cfg.HomeDir(), "plugins")
	// Binding injected dependencies must not create runtime files. A normal
	// installation already has both directories; an absent root means no plugins.
	a.plugins = &pluginRuntime{commands: map[string]plugins.Prompt{}, agents: map[string]plugins.Prompt{}, trusted: map[string]bool{}}
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	for _, dir := range []string{root, filepath.Join(root, "bundles")} {
		info, err := os.Lstat(dir)
		if err != nil {
			return fmt.Errorf("read plugin directory: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin directory must be a real directory: %s", dir)
		}
	}
	mgr, err := plugins.NewManager(root)
	if err != nil {
		return err
	}
	installed, err := mgr.List()
	if err != nil {
		return err
	}
	runtime := &pluginRuntime{commands: map[string]plugins.Prompt{}, agents: map[string]plugins.Prompt{}, trusted: map[string]bool{}}
	var skills []*tool.SkillInfo
	for _, item := range installed {
		if !item.Entry.Enabled {
			continue
		}
		if item.Error != "" || item.Bundle == nil {
			logger.Warn("plugin quarantined", "plugin", item.Entry.Name, "error", item.Error)
			continue
		}
		bundle := item.Bundle
		if err := validatePluginPrompts(bundle); err != nil {
			return err
		}
		runtime.bundles = append(runtime.bundles, bundle)
		runtime.trusted[bundle.Manifest.Name] = item.Entry.Trusted
		for _, p := range bundle.Commands {
			runtime.commands[bundle.Manifest.Name+":"+p.Name] = p
		}
		for _, p := range bundle.Agents {
			runtime.agents[bundle.Manifest.Name+":"+p.Name] = p
		}
		for _, p := range bundle.Skills {
			// Loading never probes --help; installing a plugin cannot cause code
			// to run while merely discovering its skill descriptions.
			info, loadErr := tool.NewSkillLoader(bundle.Root).WithCLIInspect(false, 0).Load(p.Path)
			if loadErr != nil {
				return fmt.Errorf("plugin %s skill %s: %w", bundle.Manifest.Name, p.Name, loadErr)
			}
			info.Name = bundle.Manifest.Name + ":" + p.Name
			info.Aliases = nil // aliases must not shadow user skills or another plugin
			if !item.Entry.Trusted {
				info.Tools = nil
			}
			skills = append(skills, info)
		}
	}
	a.plugins = runtime
	a.skillMu.Lock()
	a.pluginSkills = skills
	a.skillMu.Unlock()
	return nil
}

// Prompt metadata that changes execution policy must not silently be ignored.
// v0.1 supports inherited-model instruction presets, not foreign permission modes.
func validatePluginPrompts(bundle *plugins.Bundle) error {
	for _, p := range append(append([]plugins.Prompt(nil), bundle.Commands...), bundle.Agents...) {
		text := strings.ReplaceAll(p.Content, "\r\n", "\n")
		if !strings.HasPrefix(text, "---\n") {
			continue
		}
		end := strings.Index(text[4:], "\n---\n")
		if end < 0 {
			return fmt.Errorf("plugin %s prompt %s has unterminated frontmatter", bundle.Manifest.Name, p.Name)
		}
		var metadata map[string]any
		if err := yaml.Unmarshal([]byte(text[4:4+end]), &metadata); err != nil {
			return fmt.Errorf("plugin prompt %s: %w", p.Name, err)
		}
		for key := range metadata {
			switch key {
			case "name", "description", "argument-hint", "title", "author", "license", "tags":
			default:
				return fmt.Errorf("plugin %s prompt %s: metadata %s is not supported in v0.1", bundle.Manifest.Name, p.Name, key)
			}
		}
	}
	return nil
}

func (a *Agent) pluginMCPConfig(base mcpclient.Config) mcpclient.Config {
	cfg := base.Clone()
	if a.plugins == nil {
		return cfg
	}
	if cfg.Servers == nil {
		cfg.Servers = map[string]mcpclient.ServerConfig{}
	}
	for _, b := range a.plugins.bundles {
		if !a.plugins.trusted[b.Manifest.Name] {
			continue
		}
		for name, s := range b.MCPServers {
			id := pluginResourceName(b.Manifest.Name + "." + name)
			// A configured server always wins; a plugin never replaces it.
			if _, exists := cfg.Servers[id]; exists {
				logger.Warn("plugin MCP name conflicts with host config", "server", id)
				continue
			}
			cfg.Servers[id] = mcpclient.ServerConfig{Transport: s.Transport, URL: s.URL, Command: s.Command, Args: s.Args, Env: s.Env, CWD: s.CWD, CredentialRef: s.CredentialRef, TimeoutSeconds: s.TimeoutSeconds, Disabled: s.Disabled}
		}
	}
	return cfg
}

func (a *Agent) pluginHookConfig(c *config.Config) hook.Config {
	cfg := buildHookRuntimeConfig(c)
	if a == nil || a.plugins == nil {
		return cfg
	}
	if !cfg.Enabled {
		cfg.PreToolUse = nil
		cfg.PostToolUse = nil
	}
	for _, b := range a.plugins.bundles {
		if !a.plugins.trusted[b.Manifest.Name] {
			continue
		}
		for _, h := range b.Hooks.PreToolUse {
			cfg.PreToolUse = append(cfg.PreToolUse, hook.Spec{Match: h.Match, Sources: h.Sources, Command: h.Command, Script: h.Script})
		}
		for _, h := range b.Hooks.PostToolUse {
			cfg.PostToolUse = append(cfg.PostToolUse, hook.Spec{Match: h.Match, Sources: h.Sources, Command: h.Command, Script: h.Script})
		}
		if len(b.Hooks.PreToolUse)+len(b.Hooks.PostToolUse) > 0 {
			cfg.Enabled = true
			cfg.FailClosed = true
		}
	}
	return cfg
}

func pluginResourceName(name string) string {
	if len(name) <= 64 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return name[:47] + "_" + hex.EncodeToString(digest[:8])
}

func (a *Agent) registerPluginTools() error {
	if a.plugins == nil || len(a.plugins.bundles) == 0 {
		return nil
	}
	var tools []*tool.Tool
	for _, skill := range a.pluginSkills {
		for _, definition := range skill.Tools {
			handler, err := a.pluginSkillHandler(skill, definition)
			if err != nil {
				return err
			}
			id := strings.ReplaceAll(skill.Name, ":", "_")
			tools = append(tools, &tool.Tool{Name: pluginResourceName("plugin_" + id + "_" + definition.Name), Description: definition.Description, Category: pluginCategory, Source: skill.Name, Permission: tool.PermApprove, ContextDetailedHandler: handler, Handler: func(args map[string]any) (string, error) {
				result, err := handler(tool.ExecutionContext{Context: context.Background()}, args)
				return result.Output, err
			}, Enabled: true, Parameters: definition.Parameters, HiddenFromModel: !definition.ExposeToModel && definition.Name != "run"})
		}
	}
	tools = append(tools, &tool.Tool{Name: "plugin_info", Description: "List installed active plugins, namespaced commands and agent instruction presets. Changes to installations require a runtime restart.", Category: pluginCategory, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) {
		names := make([]string, 0, len(a.plugins.bundles))
		for _, b := range a.plugins.bundles {
			names = append(names, b.Manifest.Name+"@"+b.Manifest.Version)
		}
		data, err := json.Marshal(map[string]any{"plugins": names, "commands": a.PluginCommands(), "agents": a.PluginAgents()})
		return string(data), err
	}})
	if len(a.plugins.agents) > 0 {
		tools = append(tools, &tool.Tool{Name: "plugin_agent", Description: "Run an installed plugin's agent instruction preset in a separate child session. Inherits caller execution boundaries and host model; recursive delegation is disabled.", Category: pluginCategory, Permission: tool.PermApprove, Parameters: map[string]tool.Param{"name": {Type: "string", Required: true, Description: "Namespaced preset from plugin_info, e.g. starter:reviewer"}, "task": {Type: "string", Required: true, Description: "Concrete task; parent chat history is not copied"}}, ContextDetailedHandler: func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			name, _ := args["name"].(string)
			task, _ := args["task"].(string)
			result, err := a.runPluginAgent(exec, name, task)
			return tool.ToolCallResult{Output: result}, err
		}, Handler: func(map[string]any) (string, error) {
			return "", fmt.Errorf("plugin_agent requires a context-aware call with the parent turn policy")
		}})
	}
	modelNames := map[string]string{}
	for _, existing := range a.tools.List() {
		if existing.Category != pluginCategory {
			modelNames[tool.ToOpenAIName(existing.Name)] = existing.Name
		}
	}
	for _, candidate := range tools {
		modelName := tool.ToOpenAIName(candidate.Name)
		if previous, exists := modelNames[modelName]; exists {
			return fmt.Errorf("plugin tool %s conflicts with model tool name %s", candidate.Name, previous)
		}
		modelNames[modelName] = candidate.Name
	}
	if err := a.tools.ReplaceCategory(pluginCategory, tools); err != nil {
		return err
	}
	tool.NewSkillToolService(a.snapshotSkills()).RegisterReadTool(a.tools)
	return nil
}

func (a *Agent) PluginCommands() []string {
	var names []string
	if a != nil && a.plugins != nil {
		for name := range a.plugins.commands {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (a *Agent) PluginAgents() []string {
	var names []string
	if a != nil && a.plugins != nil {
		for name := range a.plugins.agents {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (a *Agent) HasPluginCommand(input string) bool {
	fields := strings.Fields(input)
	if a == nil || a.plugins == nil || len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return false
	}
	_, ok := a.plugins.commands[strings.TrimPrefix(fields[0], "/")]
	return ok
}

func (a *Agent) expandPluginCommand(input UserTurnInput) UserTurnInput {
	input = input.Normalize()
	if !a.HasPluginCommand(input.OriginalText) {
		return input
	}
	fields := strings.Fields(input.OriginalText)
	name := strings.TrimPrefix(fields[0], "/")
	marker := "[Plugin command " + name + "]\n"
	if strings.Contains(input.RoutingText, marker) {
		return input
	}
	arguments := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(input.OriginalText), fields[0]))
	expanded := marker + plugins.ExpandCommand(a.plugins.commands[name], arguments)
	original := strings.TrimSpace(input.OriginalText)
	if strings.Contains(input.RoutingText, original) {
		// Gateway reply context and delivery instructions surround the original
		// request. Expand only that request, preserving its enclosing context.
		input.RoutingText = strings.Replace(input.RoutingText, original, expanded, 1)
	} else {
		input.RoutingText = expanded + "\n\n[Turn context]\n" + input.RoutingText
	}
	return input.Normalize()
}

func (a *Agent) runPluginAgent(exec tool.ExecutionContext, name, task string) (string, error) {
	if a == nil || a.plugins == nil || a.sessions == nil {
		return "", fmt.Errorf("plugin runtime unavailable")
	}
	p, ok := a.plugins.agents[name]
	if !ok {
		return "", fmt.Errorf("unknown plugin agent %q", name)
	}
	if strings.TrimSpace(exec.SessionID) == "" || strings.TrimSpace(task) == "" {
		return "", fmt.Errorf("plugin_agent requires a parent session and task")
	}
	policy, hasPolicy := pluginPolicyFromContext(exec.Context)
	if !hasPolicy {
		return "", fmt.Errorf("plugin_agent requires the parent turn execution policy")
	}
	parent, found := a.sessions.Get(exec.SessionID)
	if !found {
		return "", fmt.Errorf("plugin_agent parent session does not exist")
	}
	if policy.Config.SessionID != "" && policy.Config.SessionID != exec.SessionID {
		return "", fmt.Errorf("plugin_agent parent session does not match the turn policy")
	}
	role, _ := parent.AgentProfile()
	if policy.Depth >= 1 || strings.HasPrefix(role, "plugin-agent:") || strings.HasPrefix(exec.Source, "plugin-agent:") {
		return "", fmt.Errorf("recursive plugin agents are disabled")
	}
	if !exec.AutoApprove {
		return "", &tool.ApprovalRequiredError{Tool: "plugin_agent", Reason: "run a plugin agent instruction preset in a child session"}
	}
	ctx := pluginChildContext(exec.Context)
	cfg := policy.Config
	cfg.DisabledTools = append([]string(nil), policy.Config.DisabledTools...)
	cfg.Execution = nil
	cfg.Foreground = false
	cfg.Ephemeral = true
	// Approval for starting this child is one action. It must never enable
	// automatic approval for the child's subsequent tool calls.
	cfg.AutoApprove = policy.Config.AutoApprove
	cfg.emit = nil
	cfg.eventContext = nil
	if cfg.Source == "" {
		cfg.Source = exec.Source
	}
	if cfg.Sandbox == nil {
		cfg.Sandbox = exec.Sandbox
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	child := a.sessions.NewWithTitle("plugin-agent:" + name)
	child.SetCwd(parent.GetCwd())
	child.SetAgentProfile("plugin-agent:"+name, plugins.PromptBody(p.Content))
	cfg.SessionID = child.ID
	cfg.DisabledTools = append(cfg.DisabledTools, "plugin_agent", "subagent", "delegate_task", "delegate_parallel", "delegate_to_skill", "delegate_to_mcp", "autonomy", "collab", "cron", "create_ephemeral_credential_tool", "create_ephemeral_credential_terminal_tool")
	if a.tools != nil {
		for _, registered := range a.tools.List() {
			if registered == nil {
				continue
			}
			toolName := registered.Name
			if registered.Category == tool.CatDelegate || strings.HasPrefix(toolName, "delegate_") || strings.HasPrefix(toolName, "autonomy_") || strings.HasPrefix(toolName, "collab_") || strings.HasPrefix(toolName, "cron_") || strings.HasPrefix(toolName, "tmp_credential_") || strings.HasPrefix(toolName, "tmp_terminal_credential_") {
				cfg.DisabledTools = append(cfg.DisabledTools, toolName)
			}
		}
	}
	cfg.DisabledTools = normalizeToolNameList(cfg.DisabledTools)
	request := "[Plugin agent task]\nParent user request and constraints (remain binding):\n" + policy.UserRequest + "\n\nAssigned child task:\n" + strings.TrimSpace(task)
	input := TextUserTurnInput(request).WithScope(policy.Scope)
	ctx = withPluginCallPolicy(ctx, cfg, policy.Scope, policy.UserRequest)
	result, err := a.RunLoopWithSessionInput(ctx, child, input, cfg)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", fmt.Errorf("plugin agent returned no result")
	}
	return result.Response, nil
}
