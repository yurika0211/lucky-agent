package intentclassify

import (
	"strings"
	"testing"
)

func TestClassifyMultiLabelAndNegation(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []Label
		wantNot []Label
		rule    string
	}{
		{
			name:    "explicit chat",
			input:   "你好",
			want:    []Label{LabelChat},
			wantNot: []Label{LabelCode, LabelTool},
			rule:    RuleExplicitChat,
		},
		{
			name:    "no tools explain",
			input:   "不用工具解释一下",
			want:    []Label{LabelNoTools, LabelExplain, LabelNegation, LabelChat},
			wantNot: []Label{LabelCode, LabelLocal},
			rule:    RuleNoTools,
		},
		{
			name:  "code keywords",
			input: "运行测试并修复失败",
			want:  []Label{LabelCode},
			rule:  RuleProfileCode,
		},
		{
			name:  "tool keywords",
			input: "查一下当前状态",
			want:  []Label{LabelTool},
			rule:  RuleProfileTool,
		},
		{
			name:    "negated command",
			input:   "不要执行命令，只解释 shell 命令应该怎么写。",
			want:    []Label{LabelNoExecute, LabelExplain, LabelNegation, LabelLocal, LabelTool},
			wantNot: []Label{LabelNeedsTerminal},
			rule:    RuleNoExecute,
		},
		{
			name:  "computer use",
			input: "请使用 computer use 截取当前桌面屏幕。",
			want:  []Label{LabelComputer},
			rule:  RuleComputer,
		},
		{
			name:  "memory hygiene",
			input: "帮我清洗脏记忆和记忆污染，先审计一下",
			want:  []Label{LabelMemory, LabelMemoryHygiene},
			rule:  RuleMemoryHygiene,
		},
		{
			name:  "luckyagent memory backend",
			input: "这个双链记忆系统现在是否生效，记忆库在哪",
			want:  []Label{LabelLuckyAgentMemory},
			rule:  RuleLuckyAgentMemory,
		},
		{
			name:    "web without search",
			input:   "给我打开这个网页 https://example.com 看正文",
			want:    []Label{LabelWeb, LabelWebFetch},
			wantNot: []Label{LabelWebSearch},
			rule:    RuleWeb,
		},
		{
			name:  "continuation",
			input: "继续。",
			want:  []Label{LabelContinuation},
			rule:  RuleContinuation,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.input)
			for _, label := range tt.want {
				if !got.Has(label) {
					t.Fatalf("missing %s in %+v", label, got.Hits)
				}
			}
			for _, label := range tt.wantNot {
				if got.Has(label) {
					t.Fatalf("unexpected %s in %+v", label, got.Hits)
				}
			}
			if tt.rule != "" && !got.HasRule(tt.rule) {
				t.Fatalf("missing rule %s in %+v", tt.rule, got.Hits)
			}
			if got.Empty() {
				t.Fatal("non-empty input classified empty")
			}
		})
	}
}

func TestClassifyEmpty(t *testing.T) {
	got := Classify("   ")
	if !got.Empty() || got.Recognized() || len(got.Hits) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestClassifyDoesNotReturnSideEffects(t *testing.T) {
	got := Classify("运行测试并修复失败")
	blob := strings.ToLower(strings.Join(func() []string {
		out := make([]string, 0, len(got.Hits))
		for _, hit := range got.Hits {
			out = append(out, string(hit.Label), hit.Rule, hit.Term)
		}
		return out
	}(), " "))
	for _, banned := range []string{"file_write", "disabledtools", "obsidian", "maxiterations"} {
		if strings.Contains(blob, banned) {
			t.Fatalf("classifier leaked side effect %q via %+v", banned, got.Hits)
		}
	}
}
