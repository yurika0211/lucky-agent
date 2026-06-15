package tool

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildOpenCLIInvocationWebReadUsesStdoutMarkdown(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{
		Command:        "opencli",
		Args:           []string{"web", "read", "--url", "{url}"},
		TimeoutSeconds: 12,
		MaxChars:       2000,
	})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "web_read",
		"url":    "https://example.com",
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := []string{
		"web", "read", "--url", "https://example.com",
		"--stdout", "true", "--download-images", "false", "-f", "md",
	}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Fatalf("unexpected args:\n got %#v\nwant %#v", inv.Args, want)
	}
	if inv.Action != openCLIActionWebRead || inv.URL != "https://example.com" {
		t.Fatalf("unexpected invocation: %#v", inv)
	}
}

func TestBuildOpenCLIInvocationTwitterTimelineDefaultsToFollowing(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "twitter_timeline",
		"limit":  float64(5),
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := []string{"twitter", "timeline", "--type", "following", "--limit", "5", "-f", "md"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Fatalf("unexpected args:\n got %#v\nwant %#v", inv.Args, want)
	}
}

func TestBuildOpenCLIInvocationSiteTwitterTimelineDefaultsToFollowing(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action":  "site",
		"site":    "twitter",
		"command": "timeline",
		"limit":   3,
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := []string{"twitter", "timeline", "--type", "following", "--limit", "3", "-f", "md"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Fatalf("unexpected args:\n got %#v\nwant %#v", inv.Args, want)
	}
}

func TestBuildOpenCLIInvocationRawKeepsArgs(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "raw",
		"args":   []any{"doctor"},
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := []string{"doctor"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Fatalf("unexpected args:\n got %#v\nwant %#v", inv.Args, want)
	}
}

func TestBuildOpenCLIInvocationRawStripsOpenCLIBinary(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "raw",
		"args":   []any{"opencli", "doctor"},
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := []string{"doctor"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Fatalf("unexpected args:\n got %#v\nwant %#v", inv.Args, want)
	}
}

func TestBuildOpenCLIInvocationRawUnwrapsShellWrappedOpenCLI(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "raw",
		"args":   []any{"bash", "-lc", `opencli browser "main profile" state`},
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := []string{"browser", "main profile", "state"}
	if !reflect.DeepEqual(inv.Args, want) {
		t.Fatalf("unexpected args:\n got %#v\nwant %#v", inv.Args, want)
	}
}

func TestBuildOpenCLIInvocationRawRejectsShellCommand(t *testing.T) {
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	_, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "raw",
		"args":   []any{"bash", "-lc", "date"},
	})
	if err == nil {
		t.Fatal("expected shell command to be rejected")
	}
	if !strings.Contains(err.Error(), "use the shell/terminal tool") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadOpenCLISavedMarkdownFromTable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Example.md")
	if err := os.WriteFile(path, []byte("# Example\n\nBody"), 0o600); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	table := "| title | status | saved |\n| --- | --- | --- |\n| Example | success | " + path + " |"
	got := readOpenCLISavedMarkdown(table, 1000)
	if got != "# Example\n\nBody" {
		t.Fatalf("unexpected markdown: %q", got)
	}
}

func TestNormalizeOpenCLIOutputStripsUpdateNotice(t *testing.T) {
	input := "payload\n\n  Update available: v1.8.0 -> v1.8.3\n  Run: npm install -g @jackwener/opencli\n"
	if got := normalizeOpenCLIOutput(input); got != "payload" {
		t.Fatalf("unexpected normalized output: %q", got)
	}
}
