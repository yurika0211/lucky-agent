package hook

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const defaultMaxAudit = 1000

// AuditEntry records a single hook invocation for observability.
type AuditEntry struct {
	Time       time.Time `json:"time"`
	Event      Event     `json:"event"`
	Tool       string    `json:"tool"`
	Source     string    `json:"source,omitempty"`
	Decision   string    `json:"decision"`
	DurationMs int64     `json:"duration_ms"`
	Err        string    `json:"error,omitempty"`
}

// Runner holds the configured hooks for each event and evaluates them at the
// tool-execution boundary. A nil or disabled Runner is a transparent
// passthrough, so callers never need to nil-check before invoking it.
type Runner struct {
	mu       sync.RWMutex
	enabled  bool
	pre      []Hook
	post     []Hook
	auditLog []AuditEntry
	maxAudit int
}

// NewRunner builds a Runner from cfg. When cfg.Enabled is false the runner is
// inert (every RunPre/RunPost is a passthrough), preserving existing behavior
// for runtimes that configure no hooks.
func NewRunner(cfg Config) *Runner {
	r := &Runner{
		enabled:  cfg.Enabled,
		maxAudit: defaultMaxAudit,
	}
	r.pre = buildHooks(PreToolUse, cfg.PreToolUse, cfg)
	r.post = buildHooks(PostToolUse, cfg.PostToolUse, cfg)
	return r
}

func buildHooks(event Event, specs []Spec, cfg Config) []Hook {
	hooks := make([]Hook, 0, len(specs))
	for _, spec := range specs {
		if h := newExternalCommandHook(event, spec, cfg.Timeout, cfg.MaxOutput, cfg.FailClosed); h != nil {
			hooks = append(hooks, h)
		}
	}
	return hooks
}

// Reload swaps in a freshly built hook set under lock, for config hot-reload.
func (r *Runner) Reload(cfg Config) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.enabled = cfg.Enabled
	r.pre = buildHooks(PreToolUse, cfg.PreToolUse, cfg)
	r.post = buildHooks(PostToolUse, cfg.PostToolUse, cfg)
}

// Enabled reports whether the runner has hooks active. A nil or disabled
// runner returns false, letting callers keep their fast path when no hooks are
// configured.
func (r *Runner) Enabled() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.enabled
}

// RunPre evaluates PreToolUse hooks for a tool call. It returns the (possibly
// rewritten) arguments, whether the call is blocked, and a model-facing block
// message. The first hook to block short-circuits the chain.
func (r *Runner) RunPre(toolName, arguments, source, sessionID string) (finalArgs string, blocked bool, blockMsg string) {
	if r == nil || !r.enabled {
		return arguments, false, ""
	}
	r.mu.RLock()
	hooks := r.pre
	r.mu.RUnlock()

	finalArgs = arguments
	for _, h := range hooks {
		if !h.Matches(toolName, source) {
			continue
		}
		payload := Payload{
			Event:     PreToolUse,
			Tool:      toolName,
			Arguments: finalArgs,
			Source:    source,
			SessionID: sessionID,
		}
		decision, _ := r.invoke(h, payload)
		switch decision.Decision {
		case DecisionBlock:
			return finalArgs, true, preBlockMessage(toolName, decision.Reason)
		case DecisionModify:
			if decision.ModifiedArguments != "" {
				finalArgs = decision.ModifiedArguments
			}
		}
	}
	return finalArgs, false, ""
}

// RunPost evaluates PostToolUse hooks for a completed tool call, returning the
// (possibly rewritten or redacted) output.
func (r *Runner) RunPost(toolName, arguments, source, sessionID, output string, runErr error) string {
	if r == nil || !r.enabled {
		return output
	}
	r.mu.RLock()
	hooks := r.post
	r.mu.RUnlock()

	errStr := ""
	if runErr != nil {
		errStr = runErr.Error()
	}
	finalOutput := output
	for _, h := range hooks {
		if !h.Matches(toolName, source) {
			continue
		}
		payload := Payload{
			Event:     PostToolUse,
			Tool:      toolName,
			Arguments: arguments,
			Source:    source,
			SessionID: sessionID,
			Output:    finalOutput,
			Error:     errStr,
		}
		decision, _ := r.invoke(h, payload)
		switch decision.Decision {
		case DecisionModify:
			if decision.ModifiedOutput != "" {
				finalOutput = decision.ModifiedOutput
			}
		case DecisionBlock:
			finalOutput = postBlockMessage(toolName, decision.Reason)
		}
	}
	return finalOutput
}

// invoke runs a single hook and records an audit entry.
func (r *Runner) invoke(h Hook, payload Payload) (Decision, error) {
	start := time.Now()
	decision, err := h.Run(context.Background(), payload)
	entry := AuditEntry{
		Time:       start,
		Event:      h.Event(),
		Tool:       payload.Tool,
		Source:     payload.Source,
		Decision:   decision.Decision,
		DurationMs: time.Since(start).Milliseconds(),
	}
	if err != nil {
		entry.Err = err.Error()
	}
	r.recordAudit(entry)
	return decision, err
}

func (r *Runner) recordAudit(entry AuditEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditLog = append(r.auditLog, entry)
	if r.maxAudit > 0 && len(r.auditLog) > r.maxAudit {
		r.auditLog = r.auditLog[len(r.auditLog)-r.maxAudit:]
	}
}

// AuditLog returns a copy of the recorded hook invocations.
func (r *Runner) AuditLog() []AuditEntry {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]AuditEntry, len(r.auditLog))
	copy(out, r.auditLog)
	return out
}

func preBlockMessage(toolName, reason string) string {
	if reason == "" {
		reason = "the call was blocked by a PreToolUse hook"
	}
	return fmt.Sprintf("Blocked by PreToolUse hook: %s. The %s tool call was not executed.", reason, toolName)
}

func postBlockMessage(toolName, reason string) string {
	if reason == "" {
		reason = "withheld by a PostToolUse hook"
	}
	return fmt.Sprintf("[%s output withheld by PostToolUse hook: %s]", toolName, reason)
}
