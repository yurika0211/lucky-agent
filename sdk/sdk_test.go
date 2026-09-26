package sdk_test

import (
	"context"
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

func TestRenameSession(t *testing.T) {
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

	sid, err := agent.NewSessionWithTitle("old-title")
	if err != nil {
		t.Fatalf("NewSessionWithTitle: %v", err)
	}
	if err := agent.RenameSession(sid, "new-title"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	sess, err := agent.GetSession(sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Title != "new-title" {
		t.Fatalf("title=%q", sess.Title)
	}
}

func TestCurrentAndListModels(t *testing.T) {
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

	cur, ok := agent.CurrentModel()
	if !ok {
		t.Fatal("expected current model")
	}
	if cur.ID == "" || !cur.Current {
		t.Fatalf("unexpected current model: %+v", cur)
	}

	models, err := agent.ListModels("chat")
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected chat models")
	}
	if _, err := agent.ListModels("not-a-kind"); err == nil {
		t.Fatal("expected invalid kind error")
	}
}

func TestRAGIndexSearchRemove(t *testing.T) {
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

	ctx := context.Background()
	doc, err := agent.IndexText(ctx, "sdk:test", "SDK Test Doc", "LuckyAgent embed SDK indexes host knowledge for retrieval.")
	if err != nil {
		t.Fatalf("IndexText: %v", err)
	}
	if doc == nil || doc.ID == "" {
		t.Fatalf("unexpected doc: %+v", doc)
	}

	stats, err := agent.RAGStats()
	if err != nil {
		t.Fatalf("RAGStats: %v", err)
	}
	if stats.DocumentCount < 1 {
		t.Fatalf("expected documents, got %+v", stats)
	}

	ids, err := agent.ListDocuments()
	if err != nil {
		t.Fatalf("ListDocuments: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == doc.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("doc %s not listed: %v", doc.ID, ids)
	}

	hits, err := agent.SearchRAG(ctx, "embed SDK knowledge retrieval", &sdk.RAGSearchOptions{TopK: 5, MinScore: 0})
	if err != nil {
		t.Fatalf("SearchRAG: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("expected at least one rag hit")
	}

	removed, err := agent.RemoveDocument(doc.ID)
	if err != nil {
		t.Fatalf("RemoveDocument: %v", err)
	}
	if !removed {
		t.Fatal("expected document removed")
	}
}

func TestChatInputValidation(t *testing.T) {
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

	ctx := context.Background()
	if _, err := agent.ChatWithInput(ctx, sdk.ChatInput{}); err == nil {
		t.Fatal("expected empty input error")
	}
	if _, err := agent.ChatWithInput(ctx, sdk.ChatInput{
		Message: "see image",
		Attachments: []sdk.Attachment{{
			Type: sdk.AttachmentImage,
		}},
	}); err == nil {
		t.Fatal("expected attachment body error")
	}
	if _, err := agent.ChatSessionWithInput(ctx, "", sdk.ChatInput{Message: "x"}); err == nil {
		t.Fatal("expected empty session id error")
	}
}

func TestCompactSessionForceLocal(t *testing.T) {
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

	sid, err := agent.NewSessionWithTitle("compact-me")
	if err != nil {
		t.Fatalf("NewSessionWithTitle: %v", err)
	}
	inner := agent.Underlying()
	if inner == nil {
		t.Fatal("Underlying is nil")
	}
	sess, ok := inner.Sessions().Get(sid)
	if !ok || sess == nil {
		t.Fatalf("session %s missing", sid)
	}
	sess.AddMessage("user", "Please summarize the long debugging session about context compaction.")
	sess.AddMessage("assistant", "I inspected CompactSession options and prepared a local fallback summary path.")
	sess.AddToolMessage("terminal", "go test ./sdk failed before force-local compact coverage existed")

	dry, err := agent.CompactSession(context.Background(), sid, "dry", &sdk.CompactOptions{ForceLocal: true, DryRun: true})
	if err != nil {
		t.Fatalf("CompactSession dry-run: %v", err)
	}
	if dry == nil || !dry.DryRun || dry.SummarySource != "local" {
		t.Fatalf("expected dry-run result: %+v", dry)
	}

	result, err := agent.CompactSession(context.Background(), sid, "test", &sdk.CompactOptions{ForceLocal: true})
	if err != nil {
		t.Fatalf("CompactSession: %v", err)
	}
	if result == nil || result.BoundaryID == "" || result.SummarySource != "local" || result.DryRun {
		t.Fatalf("unexpected compact result: %+v", result)
	}
}

func TestLoadSkillsFromDir(t *testing.T) {
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

	root := t.TempDir()
	skillDir := filepath.Join(root, "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\nname: demo_skill\ndescription: Demo skill for embed sdk tests.\n---\n\n# Demo Skill\n\nReturn a short ping.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	n, err := agent.LoadSkills(root)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected loaded skills, got %d", n)
	}
	found := false
	for _, info := range agent.ListSkills() {
		if info.Name == "demo_skill" || strings.Contains(info.Name, "demo") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("demo skill not listed: %+v", agent.ListSkills())
	}
}
