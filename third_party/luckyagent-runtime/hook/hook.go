package hook

import (
	"context"
	"time"
)

// Event identifies a lifecycle point at which hooks fire.
type Event string

const (
	// PreToolUse fires before a tool call is executed. A hook may allow,
	// block, or rewrite the tool arguments.
	PreToolUse Event = "PreToolUse"
	// PostToolUse fires after a tool call returns, before the result is fed
	// back into context. A hook may rewrite or redact the output.
	PostToolUse Event = "PostToolUse"
)

// Decision verbs returned by a hook.
const (
	DecisionAllow  = "allow"
	DecisionBlock  = "block"
	DecisionModify = "modify"
)

// Payload is the JSON document handed to a hook (on stdin for external
// command hooks). Output and Error are only populated for PostToolUse.
type Payload struct {
	Event     Event  `json:"event"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments"`
	Source    string `json:"source,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Decision is the JSON document a hook returns (on stdout for external
// command hooks). An empty or unrecognized Decision string is treated as
// allow.
type Decision struct {
	Decision          string `json:"decision"`
	Reason            string `json:"reason,omitempty"`
	ModifiedArguments string `json:"modified_arguments,omitempty"`
	ModifiedOutput    string `json:"modified_output,omitempty"`
}

// Hook is a single matchable handler bound to one event.
type Hook interface {
	Event() Event
	// Matches reports whether this hook applies to the given tool and source
	// ("" source matches any).
	Matches(toolName, source string) bool
	// Run evaluates the hook against the payload and returns its decision.
	Run(ctx context.Context, payload Payload) (Decision, error)
}

// Spec declares a single external-command hook in configuration terms. It is
// the runtime-side mirror of config.HookSpec, kept here so the hook package
// stays free of a dependency on the config package.
type Spec struct {
	Match   []string // tool names; empty matches all tools
	Sources []string // gateway sources (cli/telegram/qq/...); empty matches all
	Command string   // shell command, run via `sh -c`
	Script  string   // or a script path, run by file extension
}

// Config configures a Runner. It is built from config.HooksConfig by the
// agent package (see buildHookRuntimeConfig), mirroring how the autonomy kit
// is configured.
type Config struct {
	Enabled     bool
	Timeout     time.Duration
	FailClosed  bool // when a hook errors/times out: true blocks, false allows
	MaxOutput   int  // cap on bytes read from a hook's stdout
	PreToolUse  []Spec
	PostToolUse []Spec
}
