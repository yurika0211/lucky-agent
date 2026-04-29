package agent

import (
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
