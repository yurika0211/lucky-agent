package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestStoryModeStaysOutUntilEntered(t *testing.T) {
	a, sess := storyFixture(t)
	prompt := a.buildSystemPrompt(sess)
	if strings.Contains(prompt, "林夏") || strings.Contains(prompt, "剧情模式") {
		t.Fatalf("character leaked before enter: %s", prompt)
	}
	cfg := DefaultLoopConfig()
	a.applyStoryModePolicy(sess, &cfg, "你好")
	if cfg.RestrictTools {
		t.Fatal("tools were suppressed before enter")
	}
	opts := a.buildLoopCallOptions("你好", cfg)
	if !storySchemaHas(opts.Tools, "file_read") {
		t.Fatal("file_read missing from the normal schema")
	}
}

func TestStoryModeEnterUsesCardAndHidesTools(t *testing.T) {
	a, sess := storyFixture(t)
	if _, err := a.handleStoryMode(map[string]any{
		"action": "enter", "character_id": "linxia", "session_id": sess.ID,
	}); err != nil {
		t.Fatal(err)
	}
	prompt := a.buildSystemPrompt(sess)
	for _, want := range []string{"林夏", "短句", "码头", "别提现实工作"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Tool-use policy") || strings.Contains(prompt, "research-oriented") {
		t.Fatalf("agent voice remained in story prompt: %s", prompt)
	}
	cfg := DefaultLoopConfig()
	a.applyStoryModePolicy(sess, &cfg, "继续剧情")
	opts := applyToolAllowlist(a.buildLoopCallOptions("继续剧情", cfg), cfg)
	if storySchemaHas(opts.Tools, "file_read") || storySchemaHas(opts.Tools, "terminal") || storySchemaHas(opts.Tools, "memory") {
		t.Fatalf("story schema still exposes tools: %v", storySchemaNames(opts.Tools))
	}
	if !storySchemaHas(opts.Tools, storyToolName) {
		t.Fatal("story_mode itself was hidden")
	}
}

func TestStoryWorkRequestLeavesThenAllowsTools(t *testing.T) {
	a, sess := storyFixture(t)
	if _, err := a.enterStoryMode(sess.ID, "linxia"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultLoopConfig()
	a.applyStoryModePolicy(sess, &cfg, "读一下 README 文件")
	if _, active := a.storyActive(sess.ID); active {
		t.Fatal("file request stayed in story mode")
	}
	if cfg.RestrictTools {
		t.Fatal("tools stayed restricted after leaving")
	}
	opts := applyToolAllowlist(a.buildLoopCallOptions("读一下 README 文件", cfg), cfg)
	if !storySchemaHas(opts.Tools, "file_read") {
		t.Fatal("file_read unavailable after leaving story mode")
	}
	next := a.buildSystemPrompt(sess)
	if strings.Contains(next, "林夏") {
		t.Fatalf("next turn still used the character: %s", next)
	}
}

func TestStoryFactsStayOutOfLongTermMemory(t *testing.T) {
	a, sess := storyFixture(t)
	if _, err := a.enterStoryMode(sess.ID, "linxia"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.advanceStory(sess.ID, "identity", "user", "你是码头学徒", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.advanceStory(sess.ID, "relation", "linxia", "她认你作徒弟", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := a.advanceStory(sess.ID, "scene", "place", "夜航码头", nil); err != nil {
		t.Fatal(err)
	}
	// A story turn that would normally be stored as identity or preference
	// must not reach the memory writer. There is no store here; a write panics.
	a.saveConversationMemoryFromSession(sess, TextUserTurnInput("我是码头学徒，我喜欢这个身份"), "夜航还没结束。")

	cfg := DefaultLoopConfig()
	a.applyStoryModePolicy(sess, &cfg, "请把这个事实记到长期记忆：用户喜欢夜航")
	if !cfg.RestrictTools || !storyNamesContain(cfg.AllowedTools, "remember") {
		t.Fatalf("explicit save did not open the memory tool: %+v", cfg.AllowedTools)
	}
	raw, err := os.ReadFile(filepath.Join(a.sessions.Dir(), sess.ID, storySessionFile))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, want := range []string{"码头学徒", "夜航码头", "徒弟"} {
		if !strings.Contains(text, want) {
			t.Fatalf("story file missing %q", want)
		}
	}
	if strings.Contains(text, "memory") {
		t.Fatalf("story file mentions the memory store: %s", text)
	}
}

func TestStoryCardUpdateDoesNotRewriteEnteredSnapshot(t *testing.T) {
	a, sess := storyFixture(t)
	if _, err := a.enterStoryMode(sess.ID, "linxia"); err != nil {
		t.Fatal(err)
	}
	updated := storyCard()
	updated.Speech = "改成了长篇演讲"
	updated.Greeting = "新的开场不该出现"
	if err := a.storyStore().SaveCard(updated); err != nil {
		t.Fatal(err)
	}
	prompt := a.buildSystemPrompt(sess)
	if strings.Contains(prompt, "长篇演讲") || strings.Contains(prompt, "新的开场") {
		t.Fatalf("entered session followed the card file: %s", prompt)
	}
	if !strings.Contains(prompt, "短句") {
		t.Fatalf("snapshot speech missing: %s", prompt)
	}
	other := session.NewSession("other", a.sessions.Dir())
	if _, err := a.enterStoryMode(other.ID, "linxia"); err != nil {
		t.Fatal(err)
	}
	if prompt := a.buildSystemPrompt(other); !strings.Contains(prompt, "长篇演讲") {
		t.Fatalf("new session did not see the updated card: %s", prompt)
	}
}

func TestStoryLeaveRestoresNormalAgent(t *testing.T) {
	a, sess := storyFixture(t)
	if _, err := a.enterStoryMode(sess.ID, "linxia"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleStoryMode(map[string]any{"action": "leave", "session_id": sess.ID}); err != nil {
		t.Fatal(err)
	}
	prompt := a.buildSystemPrompt(sess)
	if strings.Contains(prompt, "林夏") || strings.Contains(prompt, "剧情模式") {
		t.Fatalf("character voice remained after leave: %s", prompt)
	}
	cfg := DefaultLoopConfig()
	a.applyStoryModePolicy(sess, &cfg, "总结仓库结构")
	if cfg.RestrictTools {
		t.Fatal("tools stayed suppressed after leave")
	}
}

func TestStoryFactExpiryDropsFromState(t *testing.T) {
	a, sess := storyFixture(t)
	if _, err := a.enterStoryMode(sess.ID, "linxia"); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	if _, err := a.advanceStory(sess.ID, "event", "storm", "暴风雨已经过去", &past); err != nil {
		t.Fatal(err)
	}
	state, err := a.loadStorySession(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Facts) != 0 {
		t.Fatalf("expired fact remained: %+v", state.Facts)
	}
}

func storyNamesContain(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

func storyFixture(t *testing.T) (*Agent, *session.Session) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("LUCKYAGENT_HOME", home)
	cfg, err := config.NewManagerWithDir(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewManager(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	sess := sessions.New()
	registry := tool.NewRegistry()
	for _, name := range []string{"file_read", "terminal", "web_search", "image_generate", "memory", "remember"} {
		registry.Register(&tool.Tool{Name: name, Enabled: true, Category: tool.CatBuiltin})
	}
	a := &Agent{cfg: cfg, sessions: sessions, tools: registry}
	a.registerStoryModeTool()
	if err := a.storyStore().SaveCard(storyCard()); err != nil {
		t.Fatal(err)
	}
	return a, sess
}

func storyCard() CharacterCard {
	return CharacterCard{
		ID:          "linxia",
		Name:        "林夏",
		Personality: "冷静，观察力强",
		Speech:      "短句",
		World:       "夜航码头",
		Relations:   "对用户保持距离",
		Greeting:    "雾比昨天更低。",
		Forbidden:   "别提现实工作",
	}
}

func storySchemaHas(tools []map[string]any, name string) bool {
	for _, schema := range tools {
		if functionToolNameFromSchema(schema) == name {
			return true
		}
	}
	return false
}

func storySchemaNames(tools []map[string]any) []string {
	names := make([]string, 0, len(tools))
	for _, schema := range tools {
		names = append(names, functionToolNameFromSchema(schema))
	}
	return names
}

func TestCharacterCardsLiveOutsideSessions(t *testing.T) {
	a, _ := storyFixture(t)
	cardPath := filepath.Join(a.storyStore().charactersDir(), "linxia.md")
	info, err := os.Stat(cardPath)
	if err != nil || info.IsDir() {
		t.Fatalf("card path %s: %v", cardPath, err)
	}
	text, err := os.ReadFile(cardPath)
	if err != nil || !strings.Contains(string(text), "name: 林夏") {
		t.Fatalf("card file = %s err=%v", text, err)
	}
}
