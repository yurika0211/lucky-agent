package agent

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yurika0211/luckyharness/internal/provider"
)

func TestParseToolCallsEmpty(t *testing.T) {
	resp := &provider.Response{Content: "Hello, no tools here"}
	calls := ParseToolCalls(resp)
	if len(calls) != 0 {
		t.Errorf("expected 0 tool calls, got %d", len(calls))
	}
}

func TestParseToolCallsStructured(t *testing.T) {
	resp := &provider.Response{
		ToolCalls: []provider.ToolCall{
			{Name: "search", Arguments: `{"query": "test"}`},
		},
	}
	calls := ParseToolCalls(resp)
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "search" {
		t.Errorf("expected search, got %s", calls[0].Name)
	}
}

func TestParseToolCallsIgnoresTextProtocolLeakage(t *testing.T) {
	resp := &provider.Response{
		Content: "我先确认一下。\n<tool_call>{\"name\":\"cron_status\",\"arguments\":{}}</tool_call>\n{\"name\":\"cron_list\",\"arguments\":{}}",
	}
	calls := ParseToolCalls(resp)
	if len(calls) != 0 {
		t.Fatalf("expected 0 tool calls for text protocol leakage, got %d", len(calls))
	}
}

func TestExtractTextToolCallsDSML(t *testing.T) {
	content := `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="web_search">
<｜｜DSML｜｜parameter name="count" string="false">5</｜｜DSML｜｜parameter>
<｜｜DSML｜｜parameter name="query" string="true">家庭宽带 运行网站</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
<｜｜DSML｜｜invoke name="current_time">
<｜｜DSML｜｜parameter name="location" string="true">Asia/Shanghai</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`

	cleaned, calls := extractTextToolCalls(content)
	if cleaned != "" {
		t.Fatalf("expected DSML block to be removed, got %q", cleaned)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Name != "web_search" {
		t.Fatalf("expected web_search, got %s", calls[0].Name)
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(calls[0].Arguments), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["query"] != "家庭宽带 运行网站" {
		t.Fatalf("unexpected query: %#v", args["query"])
	}
	if args["count"] != float64(5) {
		t.Fatalf("expected numeric count 5, got %#v", args["count"])
	}
}

func TestApplyTextToolCallsFiltersDisabledTools(t *testing.T) {
	resp := &provider.Response{Content: `<｜｜DSML｜｜tool_calls>
<｜｜DSML｜｜invoke name="autonomy">
<｜｜DSML｜｜parameter name="action" string="true">status</｜｜DSML｜｜parameter>
</｜｜DSML｜｜invoke>
</｜｜DSML｜｜tool_calls>`}

	if !applyTextToolCallsToResponse(resp, []string{"autonomy"}) {
		t.Fatal("expected text tool call protocol to be detected")
	}
	if resp.Content != "" {
		t.Fatalf("expected protocol content to be stripped, got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("expected disabled tool call to be filtered, got %#v", resp.ToolCalls)
	}
}

func TestFormatToolResult(t *testing.T) {
	result := FormatToolResult("search", "found 3 items", nil)
	if result != "[Tool Result: search] found 3 items" {
		t.Errorf("unexpected format: %s", result)
	}

	errResult := FormatToolResult("search", "", fmt.Errorf("timeout"))
	if errResult != "[Tool Error: search] timeout" {
		t.Errorf("unexpected error format: %s", errResult)
	}
}
