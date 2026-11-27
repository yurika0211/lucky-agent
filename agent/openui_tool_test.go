package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestPresentUIKeepsTableAndStripsTreeFromModelNote(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok || registered.Handler == nil {
		t.Fatal("present_ui was not registered")
	}
	out, err := registered.Handler(map[string]any{
		"tree": map[string]any{
			"tree_id": "compare",
			"state":   "complete",
			"nodes": []any{
				map[string]any{"id": "tbl", "type": "Table", "props": map[string]any{"columns": []any{"name"}, "rows": []any{[]any{"a"}}}},
				map[string]any{"id": "bad", "type": "Carousel", "props": map[string]any{"items": []any{}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta, note := takeOpenUIDelta(presentUIToolName, out)
	if delta == nil || len(delta.Nodes) != 1 || delta.Nodes[0].Type != "Table" || delta.State != "complete" {
		t.Fatalf("delta=%#v", delta)
	}
	if strings.Contains(note, "tree_id") || strings.Contains(note, openUIMetadataKey) {
		t.Fatalf("model note still contains the tree: %s", note)
	}
	if _, plain := takeOpenUIDelta("file_read", out); plain != out {
		t.Fatal("other tools must keep their result")
	}
}

func TestPresentUIDescriptionPointsAtOpenUISkill(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok {
		t.Fatal("present_ui was not registered")
	}
	desc := registered.Description
	for _, want := range []string{"tree", "skill_read", "openui"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q: %s", want, desc)
		}
	}
	for _, absent := range []string{
		"OpenUI policy:",
		"Do not invent Box",
		"values.window",
		"bar|line|area|pie|scatter",
	} {
		if strings.Contains(desc, absent) {
			t.Fatalf("description still contains policy text %q: %s", absent, desc)
		}
	}
}

func TestOpenUISkillCarriesNodeRules(t *testing.T) {
	path := filepath.Join("..", "skills", "openui", "SKILL.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{
		"name: openui",
		"Table: columns, rows",
		"bar|line|area|pie|scatter",
		"waiting_input",
		"Do not invent Box, Row, Carousel, AppBlock, or HTML as the tree.",
		"Do not repeat an accepted Table, Chart, or List",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("skill %s missing %q", path, want)
		}
	}
	if strings.Contains(text, "## Tools") || strings.Contains(text, "## tools") {
		t.Fatal("openui skill must not declare a Tools section")
	}

	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok {
		t.Fatal("present_ui was not registered")
	}
	if strings.Contains(registered.Description, "Do not invent Box") || strings.Contains(registered.Description, "OpenUI policy:") {
		t.Fatalf("tool description still contains the skill policy: %s", registered.Description)
	}
}

func TestOpenUISkillRouteMatchesChartAndTable(t *testing.T) {
	info, err := tool.NewSkillLoader(filepath.Join("..", "skills")).Load(filepath.Join("..", "skills", "openui", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "openui" {
		t.Fatalf("loaded name = %q", info.Name)
	}
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	a := &Agent{tools: reg, skills: []*tool.SkillInfo{info}}
	hint := a.buildSkillRouteSystemHint("draw a chart and a table of the results")
	if !strings.Contains(hint, `matches the "openui" skill`) {
		t.Fatalf("expected keyword match for openui, got %q", hint)
	}
	if strings.Contains(strings.ToLower(hint), "explicitly mentions") {
		t.Fatalf("chart and table should match by description overlap, got %q", hint)
	}
}

func TestPresentUIEmptyTreeStaysMarkdown(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok {
		t.Fatal("present_ui was not registered")
	}
	out, err := registered.Handler(map[string]any{
		"tree": map[string]any{
			"tree_id": "t1",
			"nodes":   []any{map[string]any{"id": "x", "type": "Carousel", "props": map[string]any{"items": []any{}}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	delta, note := takeOpenUIDelta(presentUIToolName, out)
	if delta != nil {
		t.Fatalf("expected no tree, got %#v", delta)
	}
	if !strings.Contains(note, "Markdown") {
		t.Fatalf("note=%s", note)
	}
}

func TestPresentUIReceiptFollowsChannel(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, ok := a.tools.Get(presentUIToolName)
	if !ok || registered.ContextDetailedHandler == nil {
		t.Fatal("present_ui context handler was not registered")
	}
	tree := map[string]any{
		"tree_id": "memory-recall-quality",
		"state":   "complete",
		"nodes": []any{
			map[string]any{"id": "lib", "type": "Table", "props": map[string]any{"columns": []any{"name"}, "rows": []any{[]any{"a"}}}},
			map[string]any{"id": "probes", "type": "Table", "props": map[string]any{"columns": []any{"q"}, "rows": []any{[]any{"b"}}}},
			map[string]any{"id": "src", "type": "Citation", "props": map[string]any{"source": "recall"}},
		},
	}
	cases := []struct {
		source       string
		wantRepeat   bool
		wantMarkdown bool
	}{
		{source: "http", wantRepeat: true},
		{source: "websocket", wantRepeat: true},
		{source: "android", wantRepeat: true},
		{source: "telegram", wantMarkdown: true},
		{source: "cli", wantMarkdown: true},
		{source: "tui", wantMarkdown: true},
		{source: "qq", wantMarkdown: true},
		{source: "", wantMarkdown: true},
	}
	for _, tc := range cases {
		result, err := registered.ContextDetailedHandler(tool.ExecutionContext{Source: tc.source}, map[string]any{"tree": tree})
		if err != nil {
			t.Fatalf("source %q: %v", tc.source, err)
		}
		_, note := takeOpenUIDelta(presentUIToolName, result.Output)
		if strings.Contains(note, "Continue with the Markdown answer.") {
			t.Fatalf("source %q still uses the unconditional receipt: %s", tc.source, note)
		}
		if tc.wantRepeat {
			if !strings.Contains(note, "on screen") || !strings.Contains(note, "Do not repeat Table") {
				t.Fatalf("source %q note=%s", tc.source, note)
			}
			if strings.Contains(note, "Do not repeat Citation") || strings.Contains(note, "no OpenUI") {
				t.Fatalf("source %q over-blocked non-tabular nodes: %s", tc.source, note)
			}
		}
		if tc.wantMarkdown {
			if !strings.Contains(note, "no OpenUI") || !strings.Contains(note, "Markdown") {
				t.Fatalf("source %q note=%s", tc.source, note)
			}
			if strings.Contains(note, "Do not repeat") {
				t.Fatalf("source %q dropped the markdown table: %s", tc.source, note)
			}
		}
	}

	plain, err := registered.Handler(map[string]any{"tree": tree})
	if err != nil {
		t.Fatal(err)
	}
	_, plainNote := takeOpenUIDelta(presentUIToolName, plain)
	if !strings.Contains(plainNote, "no OpenUI") {
		t.Fatalf("handler without a channel must keep markdown: %s", plainNote)
	}
}

func TestPresentUIOpenUIChannelKeepsMarkdownForNonTabularTree(t *testing.T) {
	a := &Agent{tools: tool.NewRegistry()}
	a.registerPresentUITool()
	registered, _ := a.tools.Get(presentUIToolName)
	result, err := registered.ContextDetailedHandler(tool.ExecutionContext{Source: "android"}, map[string]any{
		"tree": map[string]any{
			"tree_id": "pick",
			"state":   "waiting_input",
			"nodes": []any{
				map[string]any{"id": "choices", "type": "Actions", "props": map[string]any{"items": []any{"a", "b"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, note := takeOpenUIDelta(presentUIToolName, result.Output)
	if strings.Contains(note, "Do not repeat") || !strings.Contains(note, "on screen") || !strings.Contains(note, "Markdown") {
		t.Fatalf("note=%s", note)
	}
}
