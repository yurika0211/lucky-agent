package sdk_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/sdk"
)

func TestNewIsolatedHomeAndSession(t *testing.T) {
	home := t.TempDir()
	agent, err := sdk.New(sdk.Config{
		HomeDir:  home,
		Provider: "openai",
		Model:    "gpt-5.4-mini",
		APIKey:   "test-key-not-used",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer agent.Close()

	if got := agent.HomeDir(); got != mustAbs(t, home) {
		t.Fatalf("HomeDir=%q want %q", got, mustAbs(t, home))
	}

	sid, err := agent.NewSessionWithTitle("embed-smoke")
	if err != nil {
		t.Fatalf("NewSessionWithTitle: %v", err)
	}
	if sid == "" {
		t.Fatal("empty session id")
	}

	if err := agent.Remember("embed sdk smoke memory", "knowledge"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	hits := agent.Recall("embed sdk")
	if len(hits) == 0 {
		t.Fatal("expected at least one recall hit")
	}
}

func TestSessionsListGetDelete(t *testing.T) {
	agent, err := sdk.New(sdk.Config{
		HomeDir:  t.TempDir(),
		Provider: "openai",
		Model:    "gpt-5.4-mini",
		APIKey:   "test-key-not-used",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer agent.Close()

	sid, err := agent.NewSessionWithTitle("list-me")
	if err != nil {
		t.Fatalf("NewSessionWithTitle: %v", err)
	}

	list, err := agent.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == sid {
			found = true
			if item.Title != "list-me" {
				t.Fatalf("title=%q", item.Title)
			}
		}
	}
	if !found {
		t.Fatalf("session %s missing from list: %+v", sid, list)
	}

	sess, err := agent.GetSession(sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.ID != sid || sess.Title != "list-me" {
		t.Fatalf("unexpected session snapshot: %+v", sess)
	}

	if err := agent.DeleteSession(sid); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := agent.GetSession(sid); err == nil {
		t.Fatal("expected missing session error")
	}
}

func TestRegisterAndDisableTools(t *testing.T) {
	yes := true
	agent, err := sdk.New(sdk.Config{
		HomeDir:      t.TempDir(),
		Provider:     "openai",
		Model:        "gpt-5.4-mini",
		APIKey:       "test-key-not-used",
		AutoApprove:  &yes,
		DisableTools: []string{"terminal"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer agent.Close()

	// terminal should be present but disabled when the builtin exists
	for _, info := range agent.ListTools() {
		if info.Name == "terminal" && info.Enabled {
			t.Fatalf("terminal should be disabled")
		}
	}

	err = agent.RegisterTool(sdk.ToolSpec{
		Name:        "embed_echo",
		Description: "echo a value for sdk tests",
		Parameters: map[string]sdk.ToolParam{
			"text": {Type: "string", Description: "text", Required: true},
		},
		AutoApprove: true,
		Handler: func(args map[string]any) (string, error) {
			v, _ := args["text"].(string)
			return "echo:" + v, nil
		},
	})
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}

	found := false
	for _, info := range agent.ListTools() {
		if info.Name == "embed_echo" {
			found = true
			if !info.Enabled {
				t.Fatal("embed_echo should be enabled")
			}
			if info.Source != "embed-sdk" {
				t.Fatalf("source=%q", info.Source)
			}
		}
	}
	if !found {
		t.Fatal("embed_echo not listed")
	}

	if err := agent.DisableTool("embed_echo"); err != nil {
		t.Fatalf("DisableTool: %v", err)
	}
	if err := agent.EnableTool("embed_echo"); err != nil {
		t.Fatalf("EnableTool: %v", err)
	}
	if err := agent.UnregisterTool("embed_echo"); err != nil {
		t.Fatalf("UnregisterTool: %v", err)
	}
}

func TestSystemPromptWritesLocalSoul(t *testing.T) {
	home := t.TempDir()
	agent, err := sdk.New(sdk.Config{
		HomeDir:      home,
		Provider:     "openai",
		Model:        "gpt-5.4-mini",
		APIKey:       "test-key-not-used",
		SystemPrompt: "You are an embed test agent.",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer agent.Close()

	soulPath := filepath.Join(agent.HomeDir(), "memory", "prompts", "SOUL.md")
	raw, err := readFile(soulPath)
	if err != nil {
		t.Fatalf("read soul: %v", err)
	}
	if !strings.Contains(raw, "embed test agent") {
		t.Fatalf("soul content missing prompt: %q", raw)
	}
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
