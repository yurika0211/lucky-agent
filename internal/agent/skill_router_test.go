package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/function"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/tool"
)

func TestBuildSkillRouteSystemHint_ExplicitSkillMention(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "skill_obsidian_run", Enabled: true})

	a := &Agent{
		tools: reg,
		skills: []*tool.SkillInfo{
			{
				Name:        "obsidian",
				Description: "Use when working with Obsidian vault notes and structures.",
				Summary:     "Use when the task involves Obsidian notes, vault editing, links, tags, or note workflows.",
			},
		},
	}

	hint := a.buildSkillRouteSystemHint("帮我处理这个 obsidian 笔记里的标签和链接")
	if !strings.Contains(hint, `matches the "obsidian" skill`) {
		t.Fatalf("expected obsidian skill hint, got %q", hint)
	}
	if !strings.Contains(hint, "skill_read(name=\"obsidian\")") {
		t.Fatalf("expected skill_read guidance, got %q", hint)
	}
	if !strings.Contains(hint, "skill_obsidian_run") {
		t.Fatalf("expected preferred run tool, got %q", hint)
	}
}

func TestBuildSkillRouteSystemHint_KeywordMatch(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "skill_deep-research_run", Enabled: true})

	a := &Agent{
		tools: reg,
		skills: []*tool.SkillInfo{
			{
				Name:        "deep-research",
				Description: "Research workflow.",
				Summary:     "Use when the task needs deep research, source collection, synthesis, and evidence-backed reporting.",
			},
		},
	}

	hint := a.buildSkillRouteSystemHint("please do deep research and build an evidence-backed report")
	if hint == "" {
		t.Fatal("expected non-empty hint for keyword match")
	}
	if !strings.Contains(hint, "deep-research") {
		t.Fatalf("expected deep-research hint, got %q", hint)
	}
}

func TestBuildSkillRouteSystemHint_CrossLanguageKeywordMatch(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "skill_deep-research_run", Enabled: true})

	a := &Agent{
		tools: reg,
		skills: []*tool.SkillInfo{
			{
				Name:        "deep-research",
				Description: "Research workflow.",
				Summary:     "Use when the task needs deep research, source collection, synthesis, and evidence-backed reporting.",
			},
		},
	}

	hint := a.buildSkillRouteSystemHint("请帮我做一个有来源支撑的深度调研报告")
	if hint == "" {
		t.Fatal("expected non-empty hint for cross-language keyword match")
	}
	if !strings.Contains(hint, "deep-research") {
		t.Fatalf("expected deep-research hint, got %q", hint)
	}
}

func TestBuildSkillRouteSystemHint_NoStrongMatch(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})

	a := &Agent{
		tools: reg,
		skills: []*tool.SkillInfo{
			{
				Name:        "weather",
				Description: "Weather workflow.",
				Summary:     "Use when the user wants weather forecasts.",
			},
		},
	}

	hint := a.buildSkillRouteSystemHint("你好")
	if hint != "" {
		t.Fatalf("expected empty hint, got %q", hint)
	}
}

func TestBuildFunctionCallOptionsForInput_ExplicitSkillForcesSkillRead(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "skill_obsidian_run", Enabled: true})
	reg.Register(&tool.Tool{Name: "web_search", Enabled: true})

	a := &Agent{
		tools: reg,
		skills: []*tool.SkillInfo{
			{
				Name:        "obsidian",
				Description: "Obsidian workflow",
				Summary:     "Use when the task involves obsidian vault notes, tags, and links.",
			},
		},
	}

	tools := function.NewManager(reg).BuildTools()
	opts := a.buildFunctionCallOptionsForInput("帮我处理 obsidian 里的标签和链接", tools)

	tc, ok := opts.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("expected forced tool choice, got %T", opts.ToolChoice)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "skill_read" {
		t.Fatalf("expected tool_choice skill_read, got %#v", opts.ToolChoice)
	}
	if len(opts.Tools) < 2 {
		t.Fatalf("expected prioritized tools, got %#v", opts.Tools)
	}
	if functionToolName(opts.Tools[0]) != "skill_read" {
		t.Fatalf("expected first tool to be skill_read, got %q", functionToolName(opts.Tools[0]))
	}
}

func TestRelaxForcedSkillToolChoice_ReleasesAfterSkillRead(t *testing.T) {
	opts := provider.CallOptions{
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "skill_read",
			},
		},
	}

	messages := []provider.Message{
		{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{
				{Name: "skill_read"},
			},
		},
		{
			Role: "tool",
			Name: "skill_read",
		},
	}

	got := relaxForcedSkillToolChoice(messages, opts)
	if got.ToolChoice != "auto" {
		t.Fatalf("expected auto tool choice after skill_read, got %#v", got.ToolChoice)
	}
}
