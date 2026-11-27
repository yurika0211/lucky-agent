package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/function"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/memory"
	memorydiagnostic "github.com/yurika0211/luckyagent-tools/memory/diagnostic"
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
	if !strings.Contains(hint, `skill_read(action="read", name="obsidian", detail="summary")`) {
		t.Fatalf("expected skill_read guidance, got %q", hint)
	}
	if !strings.Contains(hint, "skill_obsidian_run") {
		t.Fatalf("expected preferred run tool, got %q", hint)
	}
}

type preferenceMemory struct {
	entries []memory.Entry
	err     error
	saved   []string
}

func (m *preferenceMemory) ByTierWithOptions(memory.Tier, memory.RouteOptions) []memory.Entry {
	return m.entries
}
func (m *preferenceMemory) ByTierWithOptionsErr(memory.Tier, memory.RouteOptions) ([]memory.Entry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.entries, nil
}
func (m *preferenceMemory) Recent(int) []memory.Entry                         { return nil }
func (m *preferenceMemory) RouteWithOptions(string, memory.RouteOptions) memory.RouteAnalysis {
	return memory.RouteAnalysis{}
}
func (m *preferenceMemory) RecordActivationFeedback(string, []memory.Entry, string, float64, time.Time) {
}
func (m *preferenceMemory) Recall(context.Context, memory.RecallRequest) ([]memory.SearchResult, error) {
	return nil, nil
}
func (m *preferenceMemory) Remember(_ context.Context, req memory.RememberRequest) (memory.RememberResult, error) {
	m.saved = append(m.saved, req.Content)
	return memory.RememberResult{}, nil
}
func (m *preferenceMemory) AnalyzeMemoryContent(string) []memory.HygieneIssue { return nil }
func (m *preferenceMemory) AuditHygiene(memory.HygieneOptions) memory.HygieneReport {
	return memory.HygieneReport{}
}
func (m *preferenceMemory) QuarantineDirty(memory.HygieneOptions) (memory.HygieneReport, error) {
	return memory.HygieneReport{}, nil
}
func (m *preferenceMemory) DeleteDirty(memory.HygieneOptions) (memory.HygieneReport, error) {
	return memory.HygieneReport{}, nil
}
func (m *preferenceMemory) RestoreHygiene(memory.HygieneOptions) (memory.HygieneReport, error) {
	return memory.HygieneReport{}, nil
}
func (m *preferenceMemory) RecordTurn(string) memory.MaintenanceEvent { return memory.MaintenanceEvent{} }
func (m *preferenceMemory) ForgetSession(string)                      {}
func (m *preferenceMemory) Decay(float64) int                         { return 0 }
func (m *preferenceMemory) Expire() int                               { return 0 }
func (m *preferenceMemory) Promote(string) error                      { return nil }
func (m *preferenceMemory) CompactShortTerm(context.Context, int, int) (int, error) {
	return 0, nil
}
func (m *preferenceMemory) Count() int                                 { return len(m.entries) }
func (m *preferenceMemory) Stats() map[memory.Tier]int                 { return nil }
func (m *preferenceMemory) Get(string) (*memory.Entry, error)          { return nil, nil }
func (m *preferenceMemory) GraphTopology(memory.GraphTopologyOptions) memory.GraphTopology {
	return memory.GraphTopology{}
}
func (m *preferenceMemory) Source() memory.SourceInfo { return memory.SourceInfo{} }
func (m *preferenceMemory) BuildSearchTrace(string, string, string, memory.SearchOptions, []memory.SearchResult, time.Duration) memory.SearchTrace {
	return memory.SearchTrace{}
}
func (m *preferenceMemory) TidalStats() (memorydiagnostic.TidalStoreStats, bool, error) {
	return memorydiagnostic.TidalStoreStats{}, false, nil
}
func (m *preferenceMemory) Close() error { return nil }

func TestPreferredSkillLookupFailureIsNotNoPreference(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	store := &preferenceMemory{err: errors.New("disk read failed")}
	a := &Agent{
		tools: reg, memory: store,
		skills: []*tool.SkillInfo{{Name: "web-search", Description: "Search the web", Dir: t.TempDir()}},
	}
	hint := a.buildSkillRouteSystemHint("请用 web-search 搜索")
	if !strings.Contains(hint, "could not be read") {
		t.Fatalf("read failure was treated as no preference: %q", hint)
	}
	if strings.Contains(hint, "No long-term preference is available") {
		t.Fatalf("read failure offered the no-preference path: %q", hint)
	}
	blocked, notice, err := a.resolvePreferredSkillInstall(context.Background(), "s", "请用 web-search 搜索")
	if !blocked || err == nil || !errors.Is(err, ErrPreferenceLookup) {
		t.Fatalf("blocked=%v notice=%q err=%v", blocked, notice, err)
	}
}

func TestPreferredMissingSkillAsksAndRefusalDoesNotSubstitute(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	installed := &tool.SkillInfo{Name: "research", Description: "Research papers", Dir: t.TempDir()}
	missing := &tool.SkillInfo{Name: "web-search", Description: "Search the web"}
	store := &preferenceMemory{entries: []memory.Entry{{
		Category: "preference", Content: "用户偏好使用 web-search skill", Importance: 0.9, Status: "active",
	}}}
	installedCalled := false
	a := &Agent{
		tools: reg, memory: store, hitl: newHITLGate(), skills: []*tool.SkillInfo{installed, missing},
		skillInstaller: func(context.Context, string, string) error { installedCalled = true; return nil },
	}
	hint := a.skillRouteHint("请用 web-search 搜索资料", skillRouteOptions{}, false)
	if !strings.Contains(hint, "not installed") || strings.Contains(hint, "research") && strings.Contains(hint, "matches the \"research\"") {
		t.Fatalf("missing preference substituted another skill: %q", hint)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		blocked, notice, err := a.resolvePreferredSkillInstall(context.Background(), "s1", "请用 web-search 搜索资料")
		if !blocked || err != nil || installedCalled {
			t.Errorf("refusal installed or substituted: blocked=%v notice=%q err=%v installed=%v", blocked, notice, err, installedCalled)
		}
		if !strings.Contains(notice, "不会改用其他技能") {
			t.Errorf("notice=%q", notice)
		}
	}()
	deadline := time.Now().Add(2 * time.Second)
	var pending []tool.PendingApproval
	for time.Now().Before(deadline) {
		pending = a.hitl.list()
		if len(pending) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pending) != 1 || !strings.Contains(pending[0].Reason, "web-search") {
		t.Fatalf("approval prompt=%#v", pending)
	}
	if _, err := a.hitl.Resolve(pending[0].ID, "deny", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("install wait did not finish")
	}
}

func TestPreferredSkillFromLongTermMemoryWinsAmongMatchingSkills(t *testing.T) {
	skills := []*tool.SkillInfo{
		{Name: "web-search", Description: "Search the web"},
		{Name: "research", Description: "Research papers"},
	}
	entries := []memory.Entry{{Category: "preference", Content: "用户偏好使用 web-search skill", Importance: 0.9, Status: "active"}}
	got := preferredSkillFromEntries("请用 web-search 帮我搜索一下最新资料", skills, entries)
	if got == nil || got.Name != "web-search" {
		t.Fatalf("long-term skill preference was not selected: %#v", got)
	}
}

func TestBuildSkillRouteSystemHint_HonorsDisabledSkillTools(t *testing.T) {
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

	hint := a.buildSkillRouteSystemHintWithOptions("帮我处理这个 obsidian 笔记里的标签和链接", skillRouteOptions{
		DisabledTools: []string{"skill_read", "skill_obsidian_run"},
	})
	if !strings.Contains(hint, `matches the "obsidian" skill`) {
		t.Fatalf("expected obsidian skill hint, got %q", hint)
	}
	if strings.Contains(hint, "skill_read") || strings.Contains(hint, "skill_obsidian_run") {
		t.Fatalf("did not expect disabled skill tools in hint, got %q", hint)
	}
}

func TestBuildSkillRouteSystemHint_LuckyAgentMemoryDoesNotRouteToExternalObsidianVault(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "skill_obsidian_run", Enabled: true})

	a := &Agent{
		tools: reg,
		skills: []*tool.SkillInfo{
			{
				Name:        "obsidian",
				Description: "Use when working with Obsidian vault notes and structures.",
				Summary:     "Use when the task involves Obsidian vault notes, tags, and links.",
			},
		},
	}

	hint := a.buildSkillRouteSystemHint("这个双链记忆系统现在是否生效，记忆库在哪")
	if !strings.Contains(hint, "LuckyAgent memory backend") {
		t.Fatalf("expected LuckyAgent memory backend guardrail, got %q", hint)
	}
	if strings.Contains(hint, `skill_read(action="read", name="obsidian"`) || strings.Contains(hint, "skill_obsidian_run") {
		t.Fatalf("expected no external obsidian skill routing, got %q", hint)
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

func TestBuildSkillRouteSystemHintMentionsSafeComparisonCandidates(t *testing.T) {
	a := &Agent{skills: []*tool.SkillInfo{
		{Name: "alpha", Description: "research evidence sources report"},
		{Name: "beta", Description: "research evidence"},
	}}
	hint := a.buildSkillRouteSystemHint("collect research evidence report")
	if !strings.Contains(hint, "No long-term preference") || !strings.Contains(hint, "cost × time") {
		t.Fatalf("expected safe comparison guidance, got %q", hint)
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

func TestBuildFunctionCallOptionsForInput_LuckyAgentMemoryKeepsAutoTools(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Register(&tool.Tool{Name: "skill_read", Enabled: true})
	reg.Register(&tool.Tool{Name: "skill_obsidian_run", Enabled: true})
	reg.Register(&tool.Tool{Name: "recall", Enabled: true})

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
	opts := a.buildFunctionCallOptionsForInput("双链记忆系统是不是已经生效了", tools)
	if opts.ToolChoice != "auto" {
		t.Fatalf("expected auto tool choice for LuckyAgent memory backend question, got %#v", opts.ToolChoice)
	}
}

func TestConstrainForcedToolChoiceLimitsToolsBeforeSkillRead(t *testing.T) {
	opts := provider.CallOptions{
		Tools: []map[string]any{
			{"function": map[string]any{"name": "skill_read"}},
			{"function": map[string]any{"name": "file_write"}},
		},
		ToolChoice: map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": "skill_read",
			},
		},
	}

	got := constrainForcedToolChoice(nil, opts)
	if len(got.Tools) != 1 || functionToolName(got.Tools[0]) != "skill_read" {
		t.Fatalf("expected only skill_read before forced call, got %#v", got.Tools)
	}
	if forcedToolChoiceName(got.ToolChoice) != "skill_read" {
		t.Fatalf("expected forced skill_read to remain, got %#v", got.ToolChoice)
	}
}

func TestConstrainForcedToolChoiceReleasesAfterSkillRead(t *testing.T) {
	opts := provider.CallOptions{
		Tools: []map[string]any{
			{"function": map[string]any{"name": "skill_read"}},
			{"function": map[string]any{"name": "file_write"}},
		},
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

	got := constrainForcedToolChoice(messages, opts)
	if got.ToolChoice != "auto" {
		t.Fatalf("expected auto tool choice after skill_read, got %#v", got.ToolChoice)
	}
	if len(got.Tools) != 2 {
		t.Fatalf("expected full tool list after skill_read, got %#v", got.Tools)
	}
}
