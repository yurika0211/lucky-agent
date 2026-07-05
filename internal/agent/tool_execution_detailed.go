package agent

import (
	"encoding/json"

	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type detailedToolExecutionResult struct {
	Output   string
	Metadata map[string]any
}

func (a *Agent) executeToolMaybeDedupDetailed(
	name, arguments string,
	autoApprove bool,
	sess *session.Session,
	toolURLRepeatCount map[string]int,
	toolURLLastResult map[string]string,
	duplicateFetchLimit int,
) (detailedToolExecutionResult, error) {
	if key := normalizedToolTarget(name, arguments); key != "" && toolURLRepeatCount[key] > duplicateFetchLimit {
		if cached := stringsTrimSpace(toolURLLastResult[key]); cached != "" {
			return detailedToolExecutionResult{Output: "Skipped duplicate " + name + " for " + key + ". Reuse previous fetched content.\n\n" + cached}, nil
		}
		return detailedToolExecutionResult{Output: "Skipped duplicate " + name + " for " + key + ". Reuse earlier fetched content."}, nil
	}
	return a.executeToolWithSessionDetailed(name, arguments, autoApprove, sess)
}

func (a *Agent) executeToolWithSessionDetailed(name, arguments string, autoApprove bool, sess *session.Session) (out detailedToolExecutionResult, err error) {
	_ = autoApprove
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

	var result *tool.GatewayResult
	if sc != nil {
		result, err = a.gateway.ExecuteWithShellContext(name, args, "", sc)
	} else {
		result, err = a.gateway.Execute(name, args, "")
	}
	if err != nil {
		return detailedToolExecutionResult{}, err
	}

	output := result.Output
	if sess != nil && name == "terminal" {
		a.updateShellContext(sess, arguments, output)
	}
	if a.hooks.Enabled() {
		output = a.hooks.RunPost(name, arguments, "", sessionID, output, nil)
	}
	return detailedToolExecutionResult{Output: output, Metadata: result.Metadata}, nil
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
