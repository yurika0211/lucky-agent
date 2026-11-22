package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestSkillUsageRecordsRouteReadAndExec(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# obsidian\n\nsecret-skill-body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := []*tool.SkillInfo{{
		Name:        "obsidian",
		Description: "Use when working with Obsidian vault notes and structures.",
		Summary:     "Use when the task involves Obsidian notes, vault editing, links, tags, or note workflows.",
		Dir:         skillDir,
	}}
	registry := tool.NewRegistry()
	tool.NewSkillToolService(skills).RegisterReadTool(registry)
	registry.Register(&tool.Tool{
		Name:    "skill_find-nearby_run",
		Enabled: true,
		Handler: func(args map[string]any) (string, error) {
			return "ran", nil
		},
	})
	a := &Agent{
		cfg:     cfg,
		tools:   registry,
		gateway: tool.NewGateway(registry),
		skills:  skills,
	}

	query := "帮我处理这个 obsidian 笔记里的标签和链接"
	hint := a.buildSkillRouteSystemHint(query)
	if !strings.Contains(hint, `matches the "obsidian" skill`) {
		t.Fatalf("hint = %q", hint)
	}
	a.buildFunctionCallOptionsForInput(query, nil)
	read := runSkillTool(t, a, "skill_read", map[string]any{"name": "obsidian", "action": "read"})
	if strings.HasPrefix(read.Result, "Error:") {
		t.Fatalf("skill_read = %s", read.Result)
	}
	if !strings.Contains(read.Result, "secret-skill-body") {
		t.Fatalf("expected production skill_read body, got %s", read.Result)
	}
	run := runSkillTool(t, a, "skill_find-nearby_run", map[string]any{"query": "coffee"})
	if strings.HasPrefix(run.Result, "Error:") {
		t.Fatalf("run = %s", run.Result)
	}

	snap := bySkillAction(a.SkillUsageSnapshot())
	if snap["obsidian/route"].OK != 1 {
		t.Fatalf("route = %#v", snap["obsidian/route"])
	}
	if snap["obsidian/read"].OK != 1 {
		t.Fatalf("read = %#v", snap["obsidian/read"])
	}
	if snap["find-nearby/run"].OK != 1 {
		t.Fatalf("run = %#v", snap["find-nearby/run"])
	}

	raw, err := os.ReadFile(filepath.Join(cfg.HomeDir(), "runtime", "skill_usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, banned := range []string{"secret-skill-body", "coffee", "query", "arguments"} {
		if strings.Contains(text, banned) {
			t.Fatalf("payload leaked %q in %s", banned, text)
		}
	}
}

func TestSkillUsageRecordsFailedExecAndSkipsNamelessRead(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry()
	tool.NewSkillToolService(nil).RegisterReadTool(registry)
	registry.Register(&tool.Tool{
		Name:    "skill_find-nearby_run",
		Enabled: true,
		Handler: func(args map[string]any) (string, error) {
			return "", errSkillBoom
		},
	})
	a := &Agent{cfg: cfg, tools: registry, gateway: tool.NewGateway(registry)}
	list := runSkillTool(t, a, "skill_read", map[string]any{"action": "list"})
	if strings.HasPrefix(list.Result, "Error:") {
		t.Fatalf("list = %s", list.Result)
	}
	failed := runSkillTool(t, a, "skill_find-nearby_run", map[string]any{})
	if !strings.Contains(failed.Result, "Error:") {
		t.Fatalf("failed run = %s", failed.Result)
	}
	snap := bySkillAction(a.SkillUsageSnapshot())
	if _, ok := snap["/read"]; ok {
		t.Fatalf("nameless read recorded: %#v", snap)
	}
	if snap["find-nearby/run"].Failed != 1 || snap["find-nearby/run"].OK != 0 {
		t.Fatalf("failed exec = %#v", snap["find-nearby/run"])
	}
}

func TestSkillUsagePersistsAndReloadsWithoutPayload(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{cfg: cfg}
	a.recordSkillRoute("obsidian")
	a.recordSkillToolUsage("skill_read", `{"name":"obsidian","action":"read","detail":"full"}`, "body", 12*time.Millisecond, nil)
	a.recordSkillToolUsage("skill_find-nearby_run", `{"query":"secret"}`, "ran", 40*time.Millisecond, nil)

	path := filepath.Join(cfg.HomeDir(), "runtime", "skill_usage.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, banned := range []string{"secret", "detail", "full", "query", "body", "arguments"} {
		if strings.Contains(text, banned) {
			t.Fatalf("payload leaked %q in %s", banned, text)
		}
	}
	var first skillUsageEvent
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(text), "\n")[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Skill != "obsidian" || first.Action != "route" || first.Result != "ok" {
		t.Fatalf("first event = %#v", first)
	}

	reloaded := &Agent{cfg: cfg}
	snap := bySkillAction(reloaded.ensureSkillUsageStore().snapshot())
	if snap["obsidian/route"].OK != 1 || snap["obsidian/read"].OK != 1 || snap["find-nearby/run"].OK != 1 {
		t.Fatalf("reloaded = %#v", snap)
	}
	if snap["obsidian/read"].Duration < 12*time.Millisecond {
		t.Fatalf("read duration = %s", snap["obsidian/read"].Duration)
	}
}

func TestSkillUsageDedupesSameRouteInOneTurn(t *testing.T) {
	a := &Agent{
		skills: []*tool.SkillInfo{{
			Name:        "obsidian",
			Description: "Use when working with Obsidian vault notes and structures.",
			Summary:     "Use when the task involves Obsidian notes, vault editing, links, tags, or note workflows.",
		}},
	}
	query := "帮我处理这个 obsidian 笔记里的标签和链接"
	if hint := a.buildSkillRouteSystemHint(query); !strings.Contains(hint, `matches the "obsidian" skill`) {
		t.Fatalf("hint = %q", hint)
	}
	a.buildFunctionCallOptionsForInput(query, nil)
	snap := bySkillAction(a.SkillUsageSnapshot())
	if snap["obsidian/route"].OK != 1 {
		t.Fatalf("route counted twice: %#v", snap["obsidian/route"])
	}
}

func TestSkillUsageSkipsObsidianMemoryBackendRoute(t *testing.T) {
	a := &Agent{
		skills: []*tool.SkillInfo{{
			Name:        "obsidian",
			Description: "Use when working with Obsidian vault notes and structures.",
			Summary:     "Use when the task involves Obsidian notes, vault editing, links, tags, or note workflows.",
		}},
	}
	hint := a.buildSkillRouteSystemHint("这个双链记忆系统现在是否生效，记忆库在哪")
	if !strings.Contains(hint, "LuckyAgent memory backend") {
		t.Fatalf("hint = %q", hint)
	}
	a.buildFunctionCallOptionsForInput("这个双链记忆系统现在是否生效，记忆库在哪", nil)
	if rows := a.SkillUsageSnapshot(); len(rows) != 0 {
		t.Fatalf("memory-backend route recorded: %#v", rows)
	}
}

func TestAnnounceSkillUseNamesActualSkillAndDeduplicates(t *testing.T) {
	a := &Agent{}
	if got := a.announceSkillUse("skill_read", `{"name":"obsidian","action":"read"}`); got != "已采用 skill obsidian 的指导。" {
		t.Fatalf("unexpected first notice: %q", got)
	}
	if got := a.announceSkillUse("skill_obsidian_run", `{}`); got != "" {
		t.Fatalf("repeated skill notice was not deduplicated: %q", got)
	}
	if got := a.announceSkillUse("skill_deep-research_run", `{}`); got != "已采用 skill deep-research。" {
		t.Fatalf("skill switch notice missing: %q", got)
	}
}

var errSkillBoom = errString("boom")

type errString string

func (e errString) Error() string { return string(e) }

func runSkillTool(t *testing.T, a *Agent, name string, args map[string]any) executedToolCall {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	results := a.executeToolCallsOrdered([]provider.ToolCall{{
		ID:        name + "-1",
		Name:      name,
		Arguments: string(raw),
	}}, true, nil, map[string]int{}, map[string]string{}, 0, false)
	if len(results) != 1 {
		t.Fatalf("%s results = %d", name, len(results))
	}
	return results[0]
}

func bySkillAction(rows []SkillUsageSnapshot) map[string]SkillUsageSnapshot {
	out := map[string]SkillUsageSnapshot{}
	for _, row := range rows {
		out[row.Skill+"/"+row.Action] = row
	}
	return out
}
