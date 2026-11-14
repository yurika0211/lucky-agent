package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	pluginpkg "github.com/yurika0211/lucky-agent/agent/plugin"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func requirePluginScriptProcessGroups(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "aix", "darwin", "dragonfly", "freebsd", "linux", "netbsd", "openbsd", "solaris":
	default:
		t.Skip("plugin script process groups are unsupported on this platform")
	}
}

func TestPluginScriptCancellationKillsBackgroundChildren(t *testing.T) {
	requirePluginScriptProcessGroups(t)
	a, name, _ := newPluginScriptTestAgent(t, "(sleep 0.35; printf survived > \"$SKILL_ARG_MARKER\") &\nwait\n")
	marker := filepath.Join(t.TempDir(), "escaped-after-cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := a.Tools().CallDetailedWithContext(name, map[string]any{"marker": marker}, tool.ExecutionContext{Context: ctx, AutoApprove: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	// The background write was scheduled well after the deadline. Poll until
	// that scheduled time has passed so a surviving child would have written.
	assertMarkerAbsent(t, marker, 700*time.Millisecond, "background child survived canceled plugin call")
}

func TestPluginScriptCompletionKillsBackgroundChildren(t *testing.T) {
	requirePluginScriptProcessGroups(t)
	a, name, _ := newPluginScriptTestAgent(t, "(sleep 0.35; printf survived > \"$SKILL_ARG_MARKER\") >/dev/null 2>&1 &\nprintf finished\n")
	marker := filepath.Join(t.TempDir(), "escaped-after-return")
	result, err := a.Tools().CallDetailedWithContext(name, map[string]any{"marker": marker}, tool.ExecutionContext{Context: context.Background(), AutoApprove: true})
	if err != nil || result.Output != "finished" {
		t.Fatalf("result=%q error=%v", result.Output, err)
	}
	assertMarkerAbsent(t, marker, 700*time.Millisecond, "background child survived completed plugin call")
}

func TestPluginScriptWindowsExecutionIsExplicitlyUnsupported(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only error contract")
	}
	for _, script := range []string{"check.py", "check.js", "check.sh"} {
		if _, err := pluginpkg.Command(script, nil); err == nil || !strings.Contains(err.Error(), "process-tree cancellation") {
			t.Fatalf("Windows script %s did not fail explicitly: %v", script, err)
		}
	}
}

func assertMarkerAbsent(t *testing.T, marker string, wait time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Fatalf("%s: %v", msg, err)
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
