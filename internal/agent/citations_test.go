package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/config"
)

func TestAppendNaturalCitationsHonorsConfig(t *testing.T) {
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatalf("NewManagerWithDir: %v", err)
	}
	a := &Agent{cfg: mgr}
	logs := []toolCallLog{{
		Name:      "file_read",
		Arguments: `{"path":"README.md"}`,
		Result:    "content",
	}}

	if got := a.appendNaturalCitationsIfEnabled("答案", logs); strings.Contains(got, naturalCitationHeader) {
		t.Fatalf("citations should be disabled by default, got %q", got)
	}
	if err := mgr.Set("agent.enable_citations", "true"); err != nil {
		t.Fatalf("enable citations: %v", err)
	}
	if got := a.appendNaturalCitationsIfEnabled("答案", logs); !strings.Contains(got, naturalCitationHeader) {
		t.Fatalf("citations should be enabled after config update, got %q", got)
	}
	if err := mgr.Set("agent.enable_citations", "false"); err != nil {
		t.Fatalf("disable citations: %v", err)
	}
	if got := a.appendNaturalCitationsIfEnabled("答案", logs); strings.Contains(got, naturalCitationHeader) {
		t.Fatalf("citations should be disabled after config update, got %q", got)
	}
}

func TestAppendNaturalCitationsAddsWebSearchFooter(t *testing.T) {
	got := appendNaturalCitations("最终答案", []toolCallLog{{
		Name:      "web_search",
		Arguments: `{"query":"Twilio pricing"}`,
		Result:    "Results for: Twilio pricing\n\n1. Twilio SMS Pricing [twilio]\nURL: https://www.twilio.com/sms/pricing\n2. Twilio Verify Pricing [twilio]\nURL: https://www.twilio.com/verify/pricing",
	}})

	for _, want := range []string{
		"最终答案",
		naturalCitationHeader,
		"[1] Web search. Query: \"Twilio pricing\".",
		"Sources:",
		"Twilio SMS Pricing",
		"https://www.twilio.com/sms/pricing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected citation output to contain %q, got:\n%s", want, got)
		}
	}
}

func TestAppendNaturalCitationsSkipsErrorsAndMutatingTools(t *testing.T) {
	got := appendNaturalCitations("完成", []toolCallLog{
		{Name: "web_search", Arguments: `{"query":"x"}`, Result: "Error: unavailable"},
		{Name: "remember", Arguments: `{"content":"x"}`, Result: "saved"},
	})
	if got != "完成" {
		t.Fatalf("expected no citations for skipped tools, got %q", got)
	}
}

func TestAppendNaturalCitationsClosesOpenFenceBeforeFooter(t *testing.T) {
	got := appendNaturalCitations("解释如下：\n```asm\nmov %rax, %rbx", []toolCallLog{{
		Name:      "file_read",
		Arguments: `{"path":"/tmp/README.md"}`,
		Result:    "# README\ncontent",
	}})

	if !strings.Contains(got, "mov %rax, %rbx\n```\n\nReferences:") {
		t.Fatalf("expected dangling code fence to close before references, got:\n%s", got)
	}
}

func TestAppendNaturalCitationsSummarizesUnknownTool(t *testing.T) {
	got := appendNaturalCitations("已完成", []toolCallLog{{
		Name:      "custom_report_tool",
		Arguments: `{"path":"reports/status.json"}`,
		Result:    "报告已生成，共 12 项。",
	}})

	if !strings.Contains(got, "Tool result. Tool: custom_report_tool.") {
		t.Fatalf("expected tool name in citation, got:\n%s", got)
	}
	if !strings.Contains(got, "调用了 custom_report_tool，已获得结果：报告已生成，共 12 项") {
		t.Fatalf("expected deterministic annotation and result hint, got:\n%s", got)
	}
}

func TestStringArgMissingKeyIsEmpty(t *testing.T) {
	if got := stringArg(map[string]any{"query": "x"}, "url"); got != "" {
		t.Fatalf("expected missing arg to be empty, got %q", got)
	}
}
