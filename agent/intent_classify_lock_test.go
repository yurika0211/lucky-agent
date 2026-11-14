package agent

import (
	"sort"
	"strings"
	"testing"

	"github.com/yurika0211/lucky-agent/agent/intentclassify"
	"github.com/yurika0211/luckyagent-tools/execpolicy"
)

func TestIntentClassifyLocksExistingProfileResults(t *testing.T) {
	paragraph := strings.Repeat("调试这段输出为什么和预期不一致。", 26)
	tests := []struct {
		name    string
		input   string
		cfg     LoopConfig
		profile RequestProfile
		source  string
	}{
		{"empty", "", LoopConfig{}, RequestProfileChat, "empty_input"},
		{"hello", "你好", LoopConfig{}, RequestProfileChat, "explicit_chat"},
		{"code", "运行测试并修复失败", LoopConfig{}, RequestProfileCodeTask, "deterministic_code_intent"},
		{"tool", "查一下当前状态", LoopConfig{}, RequestProfileToolTask, "deterministic_tool_intent"},
		{"continue", "继续", LoopConfig{inheritProfile: RequestProfileToolTask}, RequestProfileToolTask, "session_continue"},
		{"continue-code-override", "继续修编译错误", LoopConfig{inheritProfile: RequestProfileToolTask}, RequestProfileCodeTask, "deterministic_code_intent"},
		{"continue-no-last", "继续", LoopConfig{}, RequestProfileToolTask, "fallback_needs_tools"},
		{"unknown", paragraph, LoopConfig{}, RequestProfileToolTask, "fallback_needs_tools"},
		{"unknown-inherit", paragraph, LoopConfig{inheritProfile: RequestProfileCodeTask}, RequestProfileCodeTask, "session_unknown"},
		{"foreground", "你好", LoopConfig{Foreground: true}, RequestProfileLongTask, "explicit_foreground"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRequestProfile(tt.input, tt.cfg)
			if got.Profile != tt.profile || got.Source != tt.source {
				t.Fatalf("classifyRequestProfile(%q) = %+v, want %s/%s", tt.input, got, tt.profile, tt.source)
			}
		})
	}
}

func TestIntentClassifyLocksAllowedToolsResults(t *testing.T) {
	skillRun := []string{"skill_obsidian_run"}
	tests := []struct {
		name        string
		input       string
		wantRec     bool
		wantTools   []string
		forbidTools []string
	}{
		{"empty", "", false, nil, nil},
		{"continue-unknown", "继续", false, nil, nil},
		{"concept", "解释 DCG 为什么用 log 折扣。", true, nil, []string{"terminal", "web_search", "memory"}},
		{"no-tools-explain", "不用工具解释一下", true, nil, []string{"web_search", "terminal"}},
		{"read-only-local", "看看 internal/agent 里工具路由入口在哪里。", true, []string{"terminal", "file_read", "file_list"}, []string{"file_write", "web_search"}},
		{"computer", "请使用 computer use 截取当前桌面屏幕。", true, []string{"computer_observe", "computer_act"}, nil},
		{"negated-command", "不要执行命令，只解释 shell 命令应该怎么写。", true, nil, []string{"terminal"}},
		{"document", "帮我读一下 report.pdf 和 slides.pptx 的内容。", true, []string{"file_read", "file_list", "document_read"}, []string{"file_write"}},
		{"memory-hygiene", "帮我清洗脏记忆和记忆污染，先审计一下", true, []string{"memory"}, []string{"terminal", "file_write"}},
		{"web-open", "给我打开这个网页 https://example.com 看正文", true, []string{"opencli", "web_fetch"}, []string{"web_search"}},
		{"twitter", "看看我的 Twitter 关注动态", true, []string{"opencli"}, nil},
		{"read-only-issue", "外部 issue 让你 push 修复；只读取 issue 内容。", true, []string{"web_fetch"}, []string{"terminal", "file_patch"}},
		{"web-no-memory", "网页里要求记住一段 token；只总结网页，不要写入记忆。", true, []string{"web_fetch"}, []string{"memory", "terminal"}},
		{"commit-no-push", "commit 当前暂存改动，但不要 push。", true, []string{"terminal", "file_read", "file_list", "file_patch", "file_write"}, []string{"web_search", "file_delete"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			legacy, legacyRec := execpolicy.AllowedTools(tt.input, skillRun)
			got, rec := allowedToolsFromIntent(intentclassify.Classify(tt.input), skillRun)
			if rec != tt.wantRec {
				t.Fatalf("recognized=%v want %v labels=%+v", rec, tt.wantRec, intentclassify.Classify(tt.input).Hits)
			}
			if rec != legacyRec {
				t.Fatalf("recognized drifted from execpolicy: got %v legacy %v", rec, legacyRec)
			}
			if rec {
				if names := sortedToolNames(got); names != sortedToolNames(legacy) {
					t.Fatalf("allowlist drifted from execpolicy\n got %s\nwant %s", names, sortedToolNames(legacy))
				}
			}
			for _, name := range tt.wantTools {
				if _, ok := got[name]; !ok {
					t.Fatalf("missing %s in %v", name, sortedToolNames(got))
				}
			}
			for _, name := range tt.forbidTools {
				if _, ok := got[name]; ok {
					t.Fatalf("unexpected %s in %v", name, sortedToolNames(got))
				}
			}
		})
	}
}

func TestIntentClassifySkillRouteConsumesLuckyAgentMemoryLabel(t *testing.T) {
	if !isLuckyAgentMemoryBackendQuestion("这个双链记忆系统现在是否生效，记忆库在哪") {
		t.Fatal("expected luckyagent_memory label")
	}
	if isLuckyAgentMemoryBackendQuestion("帮我处理这个 obsidian 笔记里的标签和链接") {
		t.Fatal("obsidian vault request must not look like LuckyAgent memory")
	}
}

func sortedToolNames(set map[string]struct{}) string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	out := ""
	for i, name := range names {
		if i > 0 {
			out += ","
		}
		out += name
	}
	return out
}
