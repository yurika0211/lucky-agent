package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/provider"
)

func TestFailureRepeatKeyGroupsSamePathAcrossCommands(t *testing.T) {
	first := failureRepeatKey("terminal", "access denied: command references restricted path (~/.luckyagent/config.json)")
	second := failureRepeatKey("terminal", "access denied: command references restricted path (/home/user/.luckyagent/config.json)")
	if first == "" || first != second {
		t.Fatalf("home-relative and absolute denials should share a key, got %q and %q", first, second)
	}
	other := failureRepeatKey("terminal", "access denied: command references restricted path (~/.ssh/id_rsa)")
	if other == "" || other == first {
		t.Fatalf("different paths must not share a key, got %q and %q", first, other)
	}
	same := failureRepeatKey("terminal", "access denied: command references restricted path (~/.luckyagent/config.json)")
	if first != same {
		t.Fatalf("same denial should share a key, got %q and %q", first, same)
	}
	missing := failureRepeatKey("file_read", "open notes.md: no such file or directory")
	if missing == "" || !contains(missing, "missing") {
		t.Fatalf("missing file key = %q", missing)
	}
	if failureRepeatKey("web_fetch", "command timed out after 30 seconds") != "" {
		t.Fatal("web_fetch timeout must stay on the URL dedupe path")
	}
	if failureRepeatKey("terminal", "hello from echo") != "" {
		t.Fatal("successful output must not form a failure key")
	}
}

func TestTrackToolCallPatternStopsSameFailureWithChangedArguments(t *testing.T) {
	state := &streamConvergenceState{repeatToolCallLimit: 150, toolOnlyIterationLimit: 150}
	calls := []string{
		`{"command":"cat ~/.luckyagent/config.json"}`,
		`{"command":"head -n 20 ~/.luckyagent/config.json"}`,
		`{"command":"sed -n '1,20p' ~/.luckyagent/config.json"}`,
	}
	result := "access denied: command references restricted path (~/.luckyagent/config.json)"
	for i, args := range calls {
		stopped, _ := state.trackToolCallPattern(providerToolCall("terminal", args), "trying another command")
		if stopped {
			t.Fatalf("stopped before the failure repeated, round %d", i)
		}
		state.rememberToolCallResult("terminal", args, result, 0)
	}
	stopped, _ := state.trackToolCallPattern(providerToolCall("terminal", `{"command":"sed -n 1,5p ~/.luckyagent/config.json"}`), "")
	if !stopped {
		t.Fatal("same sandbox denial with a new command was not stopped")
	}
	other, _ := state.trackToolCallPattern(providerToolCall("terminal", `{"command":"cat README.md"}`), "")
	if other {
		t.Fatal("a different path must keep running")
	}
}

func TestFormatRepeatedToolStopIncludesCanonicalArguments(t *testing.T) {
	sig := toolCallSignature("file_read", `{"path":"notes/today.md","line_start":4}`)
	got := formatRepeatedToolStop([]string{sig}, map[string]string{sig: "path is outside sandbox"})
	if !strings.Contains(got, `- file_read {"line_start":4,"path":"notes/today.md"}: path is outside sandbox`) {
		t.Fatalf("repeated-call stop omitted the call arguments: %q", got)
	}
}

func providerToolCall(name, arguments string) []provider.ToolCall {
	return []provider.ToolCall{{Name: name, Arguments: arguments}}
}

func contains(text, part string) bool {
	return strings.Contains(text, part)
}
