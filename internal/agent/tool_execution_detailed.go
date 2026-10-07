package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/sandbox"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type detailedToolExecutionResult struct {
	Output       string
	Metadata     map[string]any
	Observations []tool.Observation
}

// canonicalToolName converts a model-facing OpenAI-compatible name back to
// the registry name used by policies and hooks. The registry remains the
// single source of truth for this mapping.
func (a *Agent) canonicalToolName(name string) string {
	name = stringsTrimSpace(name)
	if a == nil || a.tools == nil {
		return name
	}
	if resolved, ok := a.tools.Get(name); ok && resolved != nil {
		return resolved.Name
	}
	return name
}

func (a *Agent) executeToolMaybeDedupDetailed(
	name, arguments string,
	autoApprove bool,
	sess *session.Session,
	toolURLRepeatCount map[string]int,
	toolURLLastResult map[string]string,
	duplicateFetchLimit int,
	sourceOpt ...string,
) (detailedToolExecutionResult, error) {
	if key := normalizedToolTarget(name, arguments); key != "" && toolURLRepeatCount[key] > duplicateFetchLimit {
		if cached := stringsTrimSpace(toolURLLastResult[key]); cached != "" {
			return detailedToolExecutionResult{Output: "Skipped duplicate " + name + " for " + key + ". Reuse previous fetched content.\n\n" + cached}, nil
		}
		return detailedToolExecutionResult{Output: "Skipped duplicate " + name + " for " + key + ". Reuse earlier fetched content."}, nil
	}
	return a.executeToolWithSessionDetailed(name, arguments, autoApprove, sess, sourceOpt...)
}

func (a *Agent) executeToolWithSessionDetailed(name, arguments string, autoApprove bool, sess *session.Session, sourceOpt ...string) (out detailedToolExecutionResult, err error) {
	return a.executeToolWithSessionDetailedContext(context.Background(), name, arguments, autoApprove, sess, sourceOpt...)
}

func (a *Agent) executeToolWithSessionDetailedContext(ctx context.Context, name, arguments string, autoApprove bool, sess *session.Session, sourceOpt ...string) (out detailedToolExecutionResult, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	source := "cli"
	if len(sourceOpt) > 0 && stringsTrimSpace(sourceOpt[0]) != "" {
		source = stringsTrimSpace(sourceOpt[0])
	}
	sessionID := ""
	if sess != nil {
		sessionID = sess.ID
	}

	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			args = map[string]any{"raw": arguments}
		}
	}

	// ask_user always waits for the host even when AutoApprove is on: the
	// model is explicitly requesting free-form human input.
	switch a.canonicalToolName(name) {
	case "ask_user":
		return a.executeAskUserHITL(ctx, sessionID, args)
	case "request_credential":
		return a.executeRequestCredentialHITL(ctx, sessionID, scrubCredentialArgs(args))
	}

	if allowed, meta, gateErr := a.gateToolApproval(ctx, sessionID, name, args, autoApprove); !allowed {
		if gateErr != nil {
			return detailedToolExecutionResult{Metadata: meta}, gateErr
		}
		return detailedToolExecutionResult{
			Output:   "Error: tool execution denied by user",
			Metadata: meta,
		}, fmt.Errorf("tool %s denied by user", name)
	} else if meta != nil {
		// Allowed after interactive approval: force AutoApprove so nested
		// computer_act gates do not re-prompt for the same action.
		autoApprove = true
	}

	var sc *tool.ShellContext
	snapshot := sandbox.SnapshotFromContext(ctx)
	if sess != nil {
		cwd := sess.GetCwd()
		env := sess.GetEnv()
		if cwd != "" || len(env) > 0 {
			sc = &tool.ShellContext{
				Cwd: cwd,
				Env: env,
			}
		}
	}
	if snapshot != nil && snapshot.Isolated() {
		if sc == nil {
			sc = &tool.ShellContext{}
		}
		sc.Cwd = snapshot.Root
	}
	if snapshot != nil && !snapshot.Isolated() {
		if args == nil {
			args = make(map[string]any)
		}
		args["_sandbox_mode"] = snapshot.Mode.String()
	}

	var result *tool.GatewayResult
	userRequest := ""
	if len(sourceOpt) > 1 {
		userRequest = stringsTrimSpace(sourceOpt[1])
	}
	exec := tool.ExecutionContext{
		Context: ctx, SessionID: sessionID,
		// The CLI/TUI loop is the local trusted entry point. Remote servers
		// should set an explicit allowed_sources policy before enabling control.
		Source: source, UserID: "", UserRequest: userRequest, AutoApprove: autoApprove, Sandbox: snapshot,
	}
	if sc != nil {
		result, err = a.gateway.ExecuteWithShellExecutionContext(name, args, "", sc, exec)
	} else {
		result, err = a.gateway.ExecuteWithContext(name, args, "", exec)
	}
	if err != nil {
		var approvalErr *tool.ApprovalRequiredError
		if !autoApprove && errors.As(err, &approvalErr) {
			allowed, meta, gateErr := a.waitHITLApproval(ctx, sessionID, name, args, approvalErr.Reason, string(approvalErr.Action.Kind))
			if gateErr != nil {
				return detailedToolExecutionResult{Metadata: meta}, gateErr
			}
			if !allowed {
				return detailedToolExecutionResult{Output: "Error: tool execution denied by user", Metadata: meta}, fmt.Errorf("tool %s denied by user", name)
			}
			exec.AutoApprove = true
			if sc != nil {
				result, err = a.gateway.ExecuteWithShellExecutionContext(name, args, "", sc, exec)
			} else {
				result, err = a.gateway.ExecuteWithContext(name, args, "", exec)
			}
		}
		if err != nil {
			return detailedToolExecutionResult{}, err
		}
	}

	output := result.Output
	if sess != nil && (name == "terminal" || a.canonicalToolName(name) == "terminal") {
		a.updateShellContext(sess, arguments, output)
	}
	if a.hooks.Enabled() {
		output = a.hooks.RunPost(a.canonicalToolName(name), arguments, "", sessionID, output, nil)
	}
	return detailedToolExecutionResult{Output: output, Metadata: result.Metadata, Observations: result.Observations}, nil
}

// gateToolApproval returns allowed=true when the tool may run. When the host
// must confirm a PermApprove tool, it blocks until resolved.
func (a *Agent) gateToolApproval(ctx context.Context, sessionID, name string, args map[string]any, autoApprove bool) (allowed bool, meta map[string]any, err error) {
	if autoApprove || a == nil || a.tools == nil {
		return true, nil, nil
	}
	canonical := a.canonicalToolName(name)
	if !requiresInteractiveApproval(canonical, args) {
		return true, nil, nil
	}
	reason := toolApprovalReason(canonical, args)
	return a.waitHITLApproval(ctx, sessionID, canonical, args, reason, "")
}

// requiresInteractiveApproval is the set of tools that change local state,
// run shell commands, or delegate work. Read-only lookups stay automatic so
// ordinary questions do not stall on a confirmation card.
func requiresInteractiveApproval(name string, args map[string]any) bool {
	switch name {
	case "terminal", "file_write", "file_mkdir", "file_move", "file_delete", "file_patch",
		"computer_act", "image_generate", "text_to_speech",
		"delegate_task", "delegate_cancel", "delegate_parallel", "delegate_to_skill", "delegate_to_mcp",
		"cron_add", "cron_remove", "cron_pause", "cron_resume",
		"autonomy", "autonomy_worker_spawn",
		"codex.start_turn", "codex.steer_turn", "codex.respond_approval",
		"grok.start_turn", "grok.respond_approval":
		return true
	case "http_request":
		if args == nil {
			return false
		}
		if allow, ok := args["allow_mutation"].(bool); ok && allow {
			return true
		}
		method, _ := args["method"].(string)
		switch strings.ToUpper(strings.TrimSpace(method)) {
		case "", "GET", "HEAD", "OPTIONS":
			return false
		default:
			return true
		}
	case "memory_hygiene":
		action, _ := args["action"].(string)
		action = strings.ToLower(strings.TrimSpace(action))
		return action == "quarantine" || action == "delete" || action == "restore"
	case "opencli":
		action, _ := args["action"].(string)
		action = strings.ToLower(strings.TrimSpace(action))
		return action == "browser" || action == "raw"
	default:
		return false
	}
}

func (a *Agent) waitHITLApproval(ctx context.Context, sessionID, name string, args map[string]any, reason, action string) (bool, map[string]any, error) {
	if a == nil || a.hitl == nil {
		return false, nil, fmt.Errorf("approval required for %s but hitl gate is unavailable", name)
	}
	canonical := a.canonicalToolName(name)
	summary := fmt.Sprintf("需要批准工具调用: %s", canonical)
	if strings.TrimSpace(reason) != "" {
		summary = summary + " — " + strings.TrimSpace(reason)
	}
	res, err := a.hitl.RequestAndWait(ctx, hitlRequest{
		Kind:      hitlKindApproval,
		SessionID: sessionID,
		Tool:      canonical,
		Action:    action,
		Reason:    reason,
		Summary:   summary,
		Args:      args,
	}, hitlEmitterFrom(ctx))
	meta := map[string]any{
		"approval_required": map[string]any{
			"tool":   canonical,
			"action": action,
			"reason": reason,
		},
	}
	if err != nil {
		return false, meta, err
	}
	if isAllowDecision(res.Decision) {
		return true, meta, nil
	}
	return false, meta, nil
}

func (a *Agent) executeAskUserHITL(ctx context.Context, sessionID string, args map[string]any) (detailedToolExecutionResult, error) {
	if a == nil || a.hitl == nil {
		return detailedToolExecutionResult{}, fmt.Errorf("ask_user requires the runtime hitl gate")
	}
	prompt := ""
	if args != nil {
		if v, ok := args["prompt"].(string); ok {
			prompt = strings.TrimSpace(v)
		}
		if prompt == "" {
			if v, ok := args["question"].(string); ok {
				prompt = strings.TrimSpace(v)
			}
		}
	}
	if prompt == "" {
		prompt = "请补充完成任务所需的信息"
	}
	res, err := a.hitl.RequestAndWait(ctx, hitlRequest{
		Kind:      hitlKindInput,
		SessionID: sessionID,
		Tool:      "ask_user",
		Prompt:    prompt,
		Reason:    prompt,
		Summary:   "需要你补充信息: " + prompt,
		Args:      args,
	}, hitlEmitterFrom(ctx))
	meta := map[string]any{
		"approval_required": map[string]any{
			"tool":   "ask_user",
			"action": hitlKindInput,
			"reason": prompt,
			"kind":   hitlKindInput,
			"prompt": prompt,
		},
	}
	if err != nil {
		return detailedToolExecutionResult{Metadata: meta}, err
	}
	if isDenyDecision(res.Decision) && strings.TrimSpace(res.Input) == "" {
		return detailedToolExecutionResult{
			Output:   "User canceled the input request.",
			Metadata: meta,
		}, nil
	}
	answer := strings.TrimSpace(res.Input)
	if answer == "" {
		answer = strings.TrimSpace(res.Decision)
	}
	if answer == "" {
		return detailedToolExecutionResult{
			Output:   "User submitted an empty answer.",
			Metadata: meta,
		}, nil
	}
	return detailedToolExecutionResult{
		Output:   "User reply: " + answer,
		Metadata: meta,
	}, nil
}

func toolApprovalReason(name string, args map[string]any) string {
	if args == nil {
		return name
	}
	if r, ok := args["reason"].(string); ok && strings.TrimSpace(r) != "" {
		return strings.TrimSpace(r)
	}
	if cmd, ok := args["command"].(string); ok && strings.TrimSpace(cmd) != "" {
		return "command: " + strings.TrimSpace(cmd)
	}
	if path, ok := args["path"].(string); ok && strings.TrimSpace(path) != "" {
		return "path: " + strings.TrimSpace(path)
	}
	return name
}

func scrubCredentialArgs(args map[string]any) map[string]any {
	if args == nil {
		return nil
	}
	cleaned := make(map[string]any, len(args))
	for key, value := range args {
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "value", "secret", "password", "token", "api_key", "apikey", "credential":
			continue
		default:
			cleaned[key] = value
		}
	}
	return cleaned
}

func stringsTrimSpace(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\t' || value[0] == '\r' || value[0] == '\n') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\t' && last != '\r' && last != '\n' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}
