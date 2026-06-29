package tool

import (
	"context"
	"os"
	"os/exec"
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

func TestBuildOpenCLIInvocationUsesFixedDownloadDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action": "raw",
		"args":   []any{"doctor"},
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := filepath.Join(home, ".luckyagent", "workspace", "downloads", "opencli")
	if inv.WorkDir != want {
		t.Fatalf("unexpected work dir: got %q want %q", inv.WorkDir, want)
	}
}

func TestBuildOpenCLIInvocationRejectsDownloadDirOutsideWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	_, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action":       "raw",
		"args":         []any{"doctor"},
		"download_dir": filepath.Join(home, ".luckyagent", "data", "opencli"),
	})
	if err == nil {
		t.Fatal("expected download_dir outside workspace to be rejected")
	}
	if !strings.Contains(err.Error(), "download_dir") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildOpenCLIInvocationResolvesRelativeDownloadDirUnderWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := normalizeOpenCLIConfig(&OpenCLIConfig{Command: "opencli"})

	inv, err := buildOpenCLIInvocation(cfg, map[string]any{
		"action":       "raw",
		"args":         []any{"doctor"},
		"download_dir": "custom-downloads",
	})
	if err != nil {
		t.Fatalf("buildOpenCLIInvocation: %v", err)
	}

	want := filepath.Join(home, ".luckyagent", "workspace", "custom-downloads")
	if inv.WorkDir != want {
		t.Fatalf("unexpected work dir: got %q want %q", inv.WorkDir, want)
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
	if !strings.Contains(err.Error(), "use the terminal tool") {
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
	got := readOpenCLISavedMarkdown(table, 1000, "")
	if got != "# Example\n\nBody" {
		t.Fatalf("unexpected markdown: %q", got)
	}
}

func TestReadOpenCLISavedMarkdownRelativeToDownloadDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Example.md"), []byte("# Example\n\nBody"), 0o600); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	table := "| title | status | saved |\n| --- | --- | --- |\n| Example | success | Example.md |"
	got := readOpenCLISavedMarkdown(table, 1000, dir)
	if got != "# Example\n\nBody" {
		t.Fatalf("unexpected markdown: %q", got)
	}
}

func TestRunOpenCLIUsesDownloadDir(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".luckyagent", "workspace", "opencli-test")

	output, err := runOpenCLI(context.Background(), "sh", []string{"-c", "pwd; printf data > out.txt"}, 1000, dir)
	if err != nil {
		t.Fatalf("runOpenCLI: %v", err)
	}
	if !strings.Contains(filepath.ToSlash(output), filepath.ToSlash(dir)) {
		t.Fatalf("expected output to include workdir %q, got %q", dir, output)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if string(data) != "data" {
		t.Fatalf("unexpected output file data: %q", string(data))
	}
}

func TestNormalizeOpenCLIOutputStripsUpdateNotice(t *testing.T) {
	input := "payload\n\n  Update available: v1.8.0 -> v1.8.3\n  Run: npm install -g @jackwener/opencli\n"
	if got := normalizeOpenCLIOutput(input); got != "payload" {
		t.Fatalf("unexpected normalized output: %q", got)
	}
}
