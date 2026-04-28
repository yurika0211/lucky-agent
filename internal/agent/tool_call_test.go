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

func TestParseTextToolCalls(t *testing.T) {
	content := "```tool\n{\"name\": \"web_search\", \"arguments\": {\"query\": \"Go\"}}\n```\n"
	calls := parseTextToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("expected 1, got %d", len(calls))
	}
	if calls[0].Name != "web_search" {
		t.Errorf("expected web_search, got %s", calls[0].Name)
	}
}

func TestParseTextToolCallsRejectsMixedNarration(t *testing.T) {
	content := "Let me search.\n```tool\n{\"name\": \"web_search\", \"arguments\": {\"query\": \"Go\"}}\n```\n"
	calls := parseTextToolCalls(content)
	if len(calls) != 0 {
		t.Fatalf("expected 0 tool calls for mixed narration, got %d", len(calls))
	}
}

func TestParseToolCallsXMLRequiresPurePayload(t *testing.T) {
	resp := &provider.Response{
		Content: "我先确认一下。\n<tool_call>{\"name\":\"cron_status\",\"arguments\":{}}</tool_call>",
	}
	calls := ParseToolCalls(resp)
	if len(calls) != 0 {
		t.Fatalf("expected 0 tool calls for mixed XML payload, got %d", len(calls))
	}
}

func TestExtractCodeBlocks(t *testing.T) {
	content := "```tool\n{\"name\": \"a\"}\n```\nSome text\n```tool\n{\"name\": \"b\"}\n```"
	blocks := extractCodeBlocks(content, "tool")
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
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
