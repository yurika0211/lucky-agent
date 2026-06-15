package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// hookKillGrace bounds how long Run waits, after the timeout fires and the
// command is killed, before force-closing its pipes and returning. It keeps a
// hook that spawns a slow child from blocking the tool path past the timeout.
const hookKillGrace = 500 * time.Millisecond

// ExternalCommandHook runs an external command or script for one event. The
// Payload is passed as JSON on stdin; a Decision is decoded from stdout.
//
// Command building mirrors internal/tool/shell_exec.go: a Command runs via the
// platform shell, a Script runs through an interpreter chosen by file
// extension. The package is kept standalone (no dependency on internal/tool)
// because that helper set is unexported and tool is a heavy package.
type ExternalCommandHook struct {
	event      Event
	match      []string
	sources    []string
	command    string
	script     string
	timeout    time.Duration
	maxOutput  int
	failClosed bool
}

// newExternalCommandHook builds a hook from a Spec for the given event. It
// returns nil when the spec declares neither a command nor a script.
func newExternalCommandHook(event Event, spec Spec, timeout time.Duration, maxOutput int, failClosed bool) *ExternalCommandHook {
	if strings.TrimSpace(spec.Command) == "" && strings.TrimSpace(spec.Script) == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxOutput <= 0 {
		maxOutput = 1 << 20
	}
	return &ExternalCommandHook{
		event:      event,
		match:      spec.Match,
		sources:    spec.Sources,
		command:    strings.TrimSpace(spec.Command),
		script:     strings.TrimSpace(spec.Script),
		timeout:    timeout,
		maxOutput:  maxOutput,
		failClosed: failClosed,
	}
}

// Event returns the lifecycle event this hook is bound to.
func (h *ExternalCommandHook) Event() Event { return h.event }

// Matches reports whether the hook applies to the tool and source. An empty
// match/sources list matches anything; matching is exact and case-sensitive
// on tool name, case-insensitive on source.
func (h *ExternalCommandHook) Matches(toolName, source string) bool {
	if !listMatches(h.match, toolName, false) {
		return false
	}
	if !listMatches(h.sources, source, true) {
		return false
	}
	return true
}

// listMatches returns true when filter is empty (match-all) or value is in
// filter. When fold is true the comparison is case-insensitive.
func listMatches(filter []string, value string, fold bool) bool {
	if len(filter) == 0 {
		return true
	}
	for _, item := range filter {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if fold {
			if strings.EqualFold(item, value) {
				return true
			}
		} else if item == value {
			return true
		}
	}
	return false
}

// Run executes the external command with the payload on stdin and decodes the
// Decision from stdout. The tool execution path carries no context.Context, so
// the timeout is self-managed here (mirroring skill_sandbox's time-bounded
// execution). On command failure the decision is allow (fail-open) unless the
// hook is configured fail-closed, in which case it blocks.
func (h *ExternalCommandHook) Run(ctx context.Context, payload Payload) (Decision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	input, err := json.Marshal(payload)
	if err != nil {
		return h.onError(fmt.Errorf("marshal payload: %w", err))
	}

	cmd, err := h.buildCmd(runCtx)
	if err != nil {
		return h.onError(err)
	}
	// Without WaitDelay, a command that spawns a child (e.g. `sh -c "sleep 5"`)
	// keeps Run blocked until the orphaned child exits, because the child
	// inherits the stdout pipe — so the context deadline alone would not bound
	// execution. WaitDelay (Go 1.20+) force-closes the pipes shortly after the
	// deadline fires. See the hookKillGrace const.
	cmd.WaitDelay = hookKillGrace
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return h.onError(fmt.Errorf("hook command failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String())))
	}

	out := stdout.Bytes()
	if h.maxOutput > 0 && len(out) > h.maxOutput {
		out = out[:h.maxOutput]
	}
	trimmed := bytes.TrimSpace(out)
	if len(trimmed) == 0 {
		// No output is an explicit allow.
		return Decision{Decision: DecisionAllow}, nil
	}

	var decision Decision
	if err := json.Unmarshal(trimmed, &decision); err != nil {
		return h.onError(fmt.Errorf("decode hook decision: %w", err))
	}
	if strings.TrimSpace(decision.Decision) == "" {
		decision.Decision = DecisionAllow
	}
	return decision, nil
}

// onError converts a hook execution failure into a decision according to the
// fail-open/fail-closed policy, returning the original error for auditing.
func (h *ExternalCommandHook) onError(err error) (Decision, error) {
	if h.failClosed {
		return Decision{Decision: DecisionBlock, Reason: fmt.Sprintf("hook error (fail-closed): %v", err)}, err
	}
	return Decision{Decision: DecisionAllow}, err
}

// buildCmd constructs the exec.Cmd for the command or script. Mirrors the
// interpreter selection in internal/tool/shell_exec.go.
func (h *ExternalCommandHook) buildCmd(ctx context.Context) (*exec.Cmd, error) {
	if h.command != "" {
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd", "/C", h.command), nil
		}
		return exec.CommandContext(ctx, "sh", "-c", h.command), nil
	}

	ext := strings.ToLower(filepath.Ext(h.script))
	switch ext {
	case ".py":
		return exec.CommandContext(ctx, "python3", h.script), nil
	case ".js":
		return exec.CommandContext(ctx, "node", h.script), nil
	case ".sh":
		return exec.CommandContext(ctx, "sh", h.script), nil
	default:
		return exec.CommandContext(ctx, "sh", h.script), nil
	}
}
