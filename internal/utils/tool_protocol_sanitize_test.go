package utils

import (
	"strings"
	"testing"
)

func TestSanitizeToolProtocolOutput_RemovesProtocolAndMetaLeak(t *testing.T) {
	in := `我先查一下当前的定时任务列表。to=cron_list
{"name":"cron_list","arguments":{}}
<tool_call>
{"name":"cron_list","arguments":{}}
</tool_call>
Maybe the tool call must be in a special commentary channel.
我这边当前没看到已配置的定时任务。`

	got := SanitizeToolProtocolOutput(in)
	if !strings.Contains(got, "我先查一下当前的定时任务列表。") || !strings.Contains(got, "我这边当前没看到已配置的定时任务。") {
		t.Fatalf("unexpected sanitized text: %q", got)
	}
	if strings.Contains(strings.ToLower(got), "to=cron_list") || strings.Contains(strings.ToLower(got), "<tool_call>") {
		t.Fatalf("protocol leakage should be removed, got %q", got)
	}
}

func TestSanitizeToolProtocolOutput_FallbackWhenOnlyProtocolRemains(t *testing.T) {
	in := `to=cron_list
{"name":"cron_list","arguments":{}}
<tool_call>{"name":"cron_list","arguments":{}}</tool_call>`

	got := SanitizeToolProtocolOutput(in)
	if got != ToolProtocolFilteredFallback {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestSanitizeToolProtocolOutput_RemovesDSMLToolCalls(t *testing.T) {
	in := `before
<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="web_search">
<｜｜DSML｜｜parameter name="query" string="true">test</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>
after`

	got := SanitizeToolProtocolOutput(in)
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Fatalf("expected surrounding text to remain, got %q", got)
	}
	if strings.Contains(got, "DSML") || strings.Contains(got, "web_search") {
		t.Fatalf("expected DSML protocol to be removed, got %q", got)
	}
}
