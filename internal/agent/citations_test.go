package agent

import (
	"strings"
	"testing"
)

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

func TestStringArgMissingKeyIsEmpty(t *testing.T) {
	if got := stringArg(map[string]any{"query": "x"}, "url"); got != "" {
		t.Fatalf("expected missing arg to be empty, got %q", got)
	}
}
