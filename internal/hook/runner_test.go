package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available on this platform")
	}
}

func newTestRunner(event Event, spec Spec, failClosed bool) *Runner {
	cfg := Config{
		Enabled:    true,
		Timeout:    2 * time.Second,
		MaxOutput:  1 << 20,
		FailClosed: failClosed,
	}
	if event == PreToolUse {
		cfg.PreToolUse = []Spec{spec}
	} else {
		cfg.PostToolUse = []Spec{spec}
	}
	return NewRunner(cfg)
}

func TestMatches(t *testing.T) {
	h := newExternalCommandHook(PreToolUse, Spec{
		Match:   []string{"file_delete"},
		Sources: []string{"cli"},
		Command: "true",
	}, time.Second, 1024, false)
	if h == nil {
		t.Fatal("expected non-nil hook")
	}

	cases := []struct {
		tool, source string
		want         bool
	}{
		{"file_delete", "cli", true},
		{"file_delete", "telegram", false}, // source not in filter
		{"file_write", "cli", false},       // tool not in filter
		{"file_delete", "", false},         // unknown source does not match scoped filter
	}
	for _, c := range cases {
		if got := h.Matches(c.tool, c.source); got != c.want {
			t.Errorf("Matches(%q,%q)=%v want %v", c.tool, c.source, got, c.want)
		}
	}

	// Empty filters match anything.
	open := newExternalCommandHook(PreToolUse, Spec{Command: "true"}, time.Second, 1024, false)
	if !open.Matches("anything", "") || !open.Matches("x", "telegram") {
		t.Error("empty match/sources should match all")
	}
}

func TestNewExternalCommandHookEmptySpec(t *testing.T) {
	if h := newExternalCommandHook(PreToolUse, Spec{}, time.Second, 1024, false); h != nil {
		t.Error("spec with neither command nor script should yield nil hook")
	}
}

func TestRunPreAllow(t *testing.T) {
	requireSh(t)
	r := newTestRunner(PreToolUse, Spec{Command: `echo '{"decision":"allow"}'`}, false)
	args, blocked, msg := r.RunPre("file_write", `{"path":"a"}`, "", "s1")
	if blocked {
		t.Fatalf("should not block: %s", msg)
	}
	if args != `{"path":"a"}` {
		t.Errorf("args should be unchanged, got %s", args)
	}
}

func TestRunPreBlock(t *testing.T) {
	requireSh(t)
	r := newTestRunner(PreToolUse, Spec{Command: `echo '{"decision":"block","reason":"protected path"}'`}, false)
	_, blocked, msg := r.RunPre("file_delete", "{}", "", "s1")
	if !blocked {
		t.Fatal("should block")
	}
	if !strings.Contains(msg, "protected path") {
		t.Errorf("block message missing reason: %s", msg)
	}
	if !strings.Contains(msg, "file_delete") {
		t.Errorf("block message missing tool name: %s", msg)
	}
}

func TestRunPreModify(t *testing.T) {
	requireSh(t)
	r := newTestRunner(PreToolUse, Spec{Command: `echo '{"decision":"modify","modified_arguments":"{\"path\":\"safe\"}"}'`}, false)
	args, blocked, _ := r.RunPre("file_write", `{"path":"danger"}`, "", "s1")
	if blocked {
		t.Fatal("should not block")
	}
	if args != `{"path":"safe"}` {
		t.Errorf("args not rewritten, got %s", args)
	}
}

func TestRunPreFailOpenVsClosed(t *testing.T) {
	requireSh(t)
	open := newTestRunner(PreToolUse, Spec{Command: `exit 3`}, false)
	if _, blocked, _ := open.RunPre("x", "{}", "", ""); blocked {
		t.Error("fail-open: hook error should allow")
	}
	closed := newTestRunner(PreToolUse, Spec{Command: `exit 3`}, true)
	if _, blocked, _ := closed.RunPre("x", "{}", "", ""); !blocked {
		t.Error("fail-closed: hook error should block")
	}
}

func TestRunPreTimeout(t *testing.T) {
	requireSh(t)
	r := NewRunner(Config{
		Enabled:    true,
		Timeout:    100 * time.Millisecond,
		FailClosed: true,
		PreToolUse: []Spec{{Command: "sleep 5"}},
	})
	start := time.Now()
	_, blocked, _ := r.RunPre("x", "{}", "", "")
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout not enforced, took %s", elapsed)
	}
	if !blocked {
		t.Error("fail-closed timeout should block")
	}
}

func TestRunPostModify(t *testing.T) {
	requireSh(t)
	r := newTestRunner(PostToolUse, Spec{Command: `echo '{"decision":"modify","modified_output":"[redacted]"}'`}, false)
	if out := r.RunPost("shell", "{}", "", "s1", "secret token=abc", nil); out != "[redacted]" {
		t.Errorf("output not rewritten, got %s", out)
	}
}

func TestRunPostBlockRedacts(t *testing.T) {
	requireSh(t)
	r := newTestRunner(PostToolUse, Spec{Command: `echo '{"decision":"block","reason":"sensitive"}'`}, false)
	out := r.RunPost("shell", "{}", "", "s1", "secret", nil)
	if strings.Contains(out, "secret") {
		t.Errorf("original output leaked: %s", out)
	}
	if !strings.Contains(out, "sensitive") {
		t.Errorf("redaction reason missing: %s", out)
	}
}

func TestDisabledRunnerPassthrough(t *testing.T) {
	r := NewRunner(Config{Enabled: false, PreToolUse: []Spec{{Command: `echo '{"decision":"block"}'`}}})
	args, blocked, _ := r.RunPre("x", `{"a":1}`, "", "")
	if blocked || args != `{"a":1}` {
		t.Error("disabled runner must passthrough")
	}
	if r.Enabled() {
		t.Error("runner should report disabled")
	}
}

func TestNilRunnerSafe(t *testing.T) {
	var r *Runner
	args, blocked, _ := r.RunPre("x", "args", "", "")
	if blocked || args != "args" {
		t.Error("nil runner RunPre must passthrough")
	}
	if got := r.RunPost("x", "{}", "", "", "out", nil); got != "out" {
		t.Error("nil runner RunPost must passthrough")
	}
	if r.Enabled() {
		t.Error("nil runner must report disabled")
	}
}

func TestPayloadDeliveredOnStdin(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "payload.json")
	r := newTestRunner(PreToolUse, Spec{Command: "cat > " + out + `; echo '{"decision":"allow"}'`}, false)

	if _, blocked, _ := r.RunPre("file_delete", `{"k":"v"}`, "cli", "sess-1"); blocked {
		t.Fatal("unexpected block")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read captured payload: %v", err)
	}
	for _, want := range []string{`"event":"PreToolUse"`, `"tool":"file_delete"`, `"source":"cli"`, `"session_id":"sess-1"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("payload missing %q: %s", want, data)
		}
	}
}

func TestAuditRecorded(t *testing.T) {
	requireSh(t)
	r := newTestRunner(PreToolUse, Spec{Command: `echo '{"decision":"allow"}'`}, false)
	r.RunPre("x", "{}", "", "")
	entries := r.AuditLog()
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Event != PreToolUse || entries[0].Tool != "x" || entries[0].Decision != DecisionAllow {
		t.Errorf("unexpected audit entry: %+v", entries[0])
	}
}

func TestReloadSwapsHooksLive(t *testing.T) {
	requireSh(t)
	// Start disabled: passthrough.
	r := NewRunner(Config{Enabled: false})
	if _, blocked, _ := r.RunPre("x", "{}", "", ""); blocked {
		t.Fatal("disabled runner should not block")
	}

	// Reload with an enabled blocking hook — takes effect without rebuilding.
	r.Reload(Config{
		Enabled:    true,
		Timeout:    2 * time.Second,
		PreToolUse: []Spec{{Command: `echo '{"decision":"block","reason":"now blocked"}'`}},
	})
	_, blocked, msg := r.RunPre("x", "{}", "", "")
	if !blocked {
		t.Fatal("after reload the runner should block")
	}
	if !strings.Contains(msg, "now blocked") {
		t.Errorf("unexpected block message: %s", msg)
	}

	// Reload back to disabled: passthrough again.
	r.Reload(Config{Enabled: false})
	if _, blocked, _ := r.RunPre("x", "{}", "", ""); blocked {
		t.Error("after disabling reload the runner should passthrough")
	}
}
