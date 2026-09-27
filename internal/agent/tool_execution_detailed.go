package agent

import (
	"context"
	"encoding/json"

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
		return detailedToolExecutionResult{}, err
	}

	output := result.Output
	if sess != nil && name == "terminal" {
		a.updateShellContext(sess, arguments, output)
	}
	if a.hooks.Enabled() {
		output = a.hooks.RunPost(a.canonicalToolName(name), arguments, "", sessionID, output, nil)
	}
	return detailedToolExecutionResult{Output: output, Metadata: result.Metadata, Observations: result.Observations}, nil
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
