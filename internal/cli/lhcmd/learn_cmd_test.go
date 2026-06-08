package lhcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearnCommandFlow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	out := runLearnTestCommand(t, "learn", "list")
	if !strings.Contains(out, "lh-agent-systems") {
		t.Fatalf("expected builtin course in list, got %q", out)
	}

	out = runLearnTestCommand(t, "learn", "start", "lh-agent-systems")
	if !strings.Contains(out, "Started: LuckyHarness Agent Systems") {
		t.Fatalf("expected start output, got %q", out)
	}

	out = runLearnTestCommand(t, "learn", "lab")
	if !strings.Contains(out, "lab-tool-trace-formatting") || !strings.Contains(out, "go test ./internal/gateway/telegram") {
		t.Fatalf("expected first lab details, got %q", out)
	}

	out = runLearnTestCommand(t, "learn", "submit", "go test ./internal/gateway/telegram passed")
	if !strings.Contains(out, "Accepted evidence") || !strings.Contains(out, "m2-context-packer") {
		t.Fatalf("expected submit to advance module, got %q", out)
	}

	out = runLearnTestCommand(t, "learn", "progress")
	if !strings.Contains(out, "Completion: 1/4") || !strings.Contains(out, "m1-tool-trace\tcompleted") {
		t.Fatalf("expected progress output, got %q", out)
	}

	progressPath := filepath.Join(home, ".luckyharness", "learning", "progress.json")
	if _, err := os.Stat(progressPath); err != nil {
		t.Fatalf("expected progress file at %s: %v", progressPath, err)
	}
}

func runLearnTestCommand(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("command %v failed: %v\noutput=%s", args, err, out.String())
	}
	return out.String()
}
