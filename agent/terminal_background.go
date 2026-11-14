package agent

import (
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

const backgroundTerminalPollEvery = 250 * time.Millisecond

// installBackgroundTerminalNotify wraps the published terminal handler so a
// background start returns immediately, then a watcher notifies the current
// session when the process exits or is killed. Production registration in
// bindOptionalToolServices calls this after RegisterCoreTools.
func (a *Agent) installBackgroundTerminalNotify() {
	if a == nil || a.tools == nil {
		return
	}
	current, ok := a.tools.Get("terminal")
	if !ok || current == nil {
		return
	}
	a.ensureBackgroundTerminalWatch()
	if current.ContextDetailedHandler != nil {
		inner := current.ContextDetailedHandler
		current.ContextDetailedHandler = func(exec tool.ExecutionContext, args map[string]any) (tool.ToolCallResult, error) {
			result, err := inner(exec, args)
			a.watchBackgroundTerminalStart(exec.SessionID, args, result.Output, err)
			return result, err
		}
		return
	}
	if current.Handler == nil {
		return
	}
	inner := current.Handler
	current.Handler = func(args map[string]any) (string, error) {
		out, err := inner(args)
		a.watchBackgroundTerminalStart("", args, out, err)
		return out, err
	}
}

func (a *Agent) ensureBackgroundTerminalWatch() {
	if a == nil {
		return
	}
	a.backgroundTerminalOnce.Do(func() {
		a.backgroundTerminalStop = make(chan struct{})
	})
}

func (a *Agent) watchBackgroundTerminalStart(sessionID string, args map[string]any, output string, err error) {
	if a == nil || err != nil || !terminalBackgroundRequested(args) {
		return
	}
	taskID := backgroundTerminalTaskID(output)
	if taskID == "" {
		return
	}
	a.ensureBackgroundTerminalWatch()
	a.backgroundTerminalWG.Add(1)
	go a.watchBackgroundTerminalTask(sessionID, taskID)
}

func (a *Agent) watchBackgroundTerminalTask(sessionID, taskID string) {
	defer a.backgroundTerminalWG.Done()
	ticker := time.NewTicker(backgroundTerminalPollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-a.backgroundTerminalStop:
			return
		case <-ticker.C:
			state, snapshot, ok := a.snapshotBackgroundTerminal(taskID)
			if !ok {
				return
			}
			if state != "exited" && state != "killed" {
				continue
			}
			a.notifyBackgroundTerminalComplete(sessionID, taskID, state, snapshot)
			return
		}
	}
}

func (a *Agent) snapshotBackgroundTerminal(taskID string) (state, snapshot string, ok bool) {
	if a == nil || a.tools == nil {
		return "", "", false
	}
	out, err := a.tools.Call("terminal_output", map[string]any{
		"task_id": taskID,
		"timeout": 0,
	})
	if err != nil {
		logger.Debug("background terminal snapshot skipped", "task_id", taskID, "error", err)
		return "", "", false
	}
	return backgroundTerminalState(out), out, true
}

func (a *Agent) notifyBackgroundTerminalComplete(sessionID, taskID, state, snapshot string) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	message := formatBackgroundTerminalNotice(taskID, state, snapshot)
	if err := a.deliverSessionNotice(sessionID, message); err != nil {
		logger.Debug("background terminal notice skipped", "session_id", sessionID, "task_id", taskID, "error", err)
	}
}

func (a *Agent) stopBackgroundTerminalWatchers() {
	if a == nil {
		return
	}
	a.ensureBackgroundTerminalWatch()
	a.backgroundTerminalStopOnce.Do(func() {
		if a.backgroundTerminalStop != nil {
			close(a.backgroundTerminalStop)
		}
	})
	done := make(chan struct{})
	go func() {
		a.backgroundTerminalWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

func formatBackgroundTerminalNotice(taskID, state, snapshot string) string {
	var b strings.Builder
	b.WriteString("Background terminal ")
	b.WriteString(taskID)
	b.WriteString(" ")
	b.WriteString(state)
	b.WriteString(".")
	body := strings.TrimSpace(stripBackgroundTerminalHeader(snapshot))
	if body != "" {
		b.WriteString("\n")
		b.WriteString(body)
	}
	return b.String()
}

func stripBackgroundTerminalHeader(snapshot string) string {
	var rest []string
	for _, line := range strings.Split(snapshot, "\n") {
		if strings.HasPrefix(line, "task_id=") || strings.HasPrefix(line, "state=") {
			continue
		}
		rest = append(rest, line)
	}
	return strings.Join(rest, "\n")
}

func terminalBackgroundRequested(args map[string]any) bool {
	if args == nil {
		return false
	}
	switch v := args["background"].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes":
			return true
		}
	case float64:
		return v != 0
	}
	return false
}

func backgroundTerminalState(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "state=") {
			return strings.TrimSpace(strings.TrimPrefix(line, "state="))
		}
	}
	return ""
}

