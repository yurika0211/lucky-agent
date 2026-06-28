package function

import (
	"fmt"
	"testing"

	"github.com/yurika0211/luckyagent/internal/tool"
)

func TestBuildTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "get_weather",
		Description: "Get weather for a city",
		Parameters: map[string]tool.Param{
			"city": {Type: "string", Description: "City name", Required: true},
		},
		Handler:    func(args map[string]any) (string, error) { return "sunny", nil },
		Permission: tool.PermAuto,
		Category:   tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	tools := mgr.BuildTools()

	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}

	fn, ok := tools[0]["function"].(map[string]any)
	if !ok {
		t.Fatal("expected function field")
	}
	if fn["name"] != "get_weather" {
		t.Errorf("expected name=get_weather, got %v", fn["name"])
	}
	if fn["description"] != "Get weather for a city" {
		t.Errorf("expected description, got %v", fn["description"])
	}
}

func TestBuildToolsEmpty(t *testing.T) {
	registry := tool.NewRegistry()
	mgr := NewManager(registry)
	tools := mgr.BuildTools()
	if tools != nil {
		t.Errorf("expected nil for empty registry, got %v", tools)
	}
}

func TestBuildToolsSkipsHiddenFromModel(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:            "visible_tool",
		Description:     "Visible tool",
		Parameters:      map[string]tool.Param{},
		Handler:         func(args map[string]any) (string, error) { return "ok", nil },
		Permission:      tool.PermAuto,
		Category:        tool.CatBuiltin,
		HiddenFromModel: false,
	})
	registry.Register(&tool.Tool{
		Name:            "hidden_tool",
		Description:     "Hidden tool",
		Parameters:      map[string]tool.Param{},
		Handler:         func(args map[string]any) (string, error) { return "ok", nil },
		Permission:      tool.PermAuto,
		Category:        tool.CatBuiltin,
		HiddenFromModel: true,
	})

	mgr := NewManager(registry)
	tools := mgr.BuildTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 visible tool, got %d", len(tools))
	}
	fn, _ := tools[0]["function"].(map[string]any)
	if fn["name"] != "visible_tool" {
		t.Fatalf("expected visible_tool, got %v", fn["name"])
	}
}

func TestBuildToolsSkipsHiddenAutonomyTools(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:            "autonomy_status",
		Description:     "hidden autonomy tool",
		Parameters:      map[string]tool.Param{},
		Handler:         func(args map[string]any) (string, error) { return "ok", nil },
		Permission:      tool.PermAuto,
		Category:        tool.CatDelegate,
		HiddenFromModel: true,
	})
	registry.Register(&tool.Tool{
		Name:            "rag_search",
		Description:     "visible rag tool",
		Parameters:      map[string]tool.Param{},
		Handler:         func(args map[string]any) (string, error) { return "ok", nil },
		Permission:      tool.PermAuto,
		Category:        tool.CatBuiltin,
		HiddenFromModel: false,
	})

	mgr := NewManager(registry)
	tools := mgr.BuildTools()
	if len(tools) != 1 {
		t.Fatalf("expected only visible tool, got %d", len(tools))
	}
	fn, _ := tools[0]["function"].(map[string]any)
	if fn["name"] != "rag_search" {
		t.Fatalf("expected rag_search, got %v", fn["name"])
	}
}

func TestExecuteCalls(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "add",
		Description: "Add two numbers",
		Parameters: map[string]tool.Param{
			"a": {Type: "number", Description: "First number", Required: true},
			"b": {Type: "number", Description: "Second number", Required: true},
		},
		Handler: func(args map[string]any) (string, error) {
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			return fmt.Sprintf("%.0f", a+b), nil
		},
		Permission: tool.PermAuto,
		Category:   tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "add", Arguments: `{"a": 1, "b": 2}`},
	}

	results := mgr.ExecuteCalls(calls, true)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Content != "3" {
		t.Errorf("expected 3, got %s", results[0].Content)
	}
	if results[0].IsError {
		t.Error("expected no error")
	}
}

func TestExecuteCallsNotFound(t *testing.T) {
	registry := tool.NewRegistry()
	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "nonexistent", Arguments: `{}`},
	}

	results := mgr.ExecuteCalls(calls, true)
	if !results[0].IsError {
		t.Error("expected error for nonexistent function")
	}
}

func TestExecuteCallsBadArgs(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "echo",
		Description: "Echo input",
		Parameters:  map[string]tool.Param{},
		Handler:     func(args map[string]any) (string, error) { return "ok", nil },
		Permission:  tool.PermAuto,
		Category:    tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "echo", Arguments: `invalid json`},
	}

	results := mgr.ExecuteCalls(calls, true)
	if !results[0].IsError {
		t.Error("expected error for invalid JSON")
	}
}

func TestExecuteCallsNeedsApproval(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "dangerous",
		Description: "A dangerous tool",
		Parameters:  map[string]tool.Param{},
		Handler:     func(args map[string]any) (string, error) { return "boom", nil },
		Permission:  tool.PermApprove,
		Category:    tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "dangerous", Arguments: `{}`},
	}

	// Without auto-approve
	results := mgr.ExecuteCalls(calls, false)
	if !results[0].IsError {
		t.Error("expected error when approval required but not auto-approved")
	}

	// With auto-approve
	mgr.ClearHistory()
	results = mgr.ExecuteCalls(calls, true)
	if results[0].IsError {
		t.Error("expected success when auto-approved")
	}
}

func TestGetResult(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "test",
		Description: "Test tool",
		Parameters:  map[string]tool.Param{},
		Handler:     func(args map[string]any) (string, error) { return "result", nil },
		Permission:  tool.PermAuto,
		Category:    tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "test", Arguments: `{}`},
	}

	mgr.ExecuteCalls(calls, true)

	result, ok := mgr.GetResult("call_1")
	if !ok {
		t.Fatal("expected to find result")
	}
	if result.Content != "result" {
		t.Errorf("expected 'result', got %s", result.Content)
	}

	_, ok = mgr.GetResult("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent result")
	}
}

func TestGetHistory(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "test",
		Description: "Test tool",
		Parameters:  map[string]tool.Param{},
		Handler:     func(args map[string]any) (string, error) { return "ok", nil },
		Permission:  tool.PermAuto,
		Category:    tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "test", Arguments: `{}`},
	}

	mgr.ExecuteCalls(calls, true)

	history := mgr.GetHistory()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Call.ID != "call_1" {
		t.Errorf("expected call_1, got %s", history[0].Call.ID)
	}
}

func TestClearHistory(t *testing.T) {
	registry := tool.NewRegistry()
	registry.Register(&tool.Tool{
		Name:        "test",
		Description: "Test tool",
		Parameters:  map[string]tool.Param{},
		Handler:     func(args map[string]any) (string, error) { return "ok", nil },
		Permission:  tool.PermAuto,
		Category:    tool.CatBuiltin,
	})

	mgr := NewManager(registry)
	calls := []FunctionCall{
		{ID: "call_1", Name: "test", Arguments: `{}`},
	}

	mgr.ExecuteCalls(calls, true)
	mgr.ClearHistory()

	if len(mgr.GetHistory()) != 0 {
		t.Error("expected empty history after clear")
	}
	_, ok := mgr.GetResult("call_1")
	if ok {
		t.Error("expected result to be cleared")
	}
}

func TestFormatResults(t *testing.T) {
	results := []FunctionResult{
		{CallID: "1", Name: "add", Content: "3", IsError: false},
		{CallID: "2", Name: "div", Content: "Error: division by zero", IsError: true},
	}

	output := FormatResults(results)
	if output == "" {
		t.Error("expected non-empty output")
	}
	if !contains(output, "✅") {
		t.Error("expected success marker")
	}
	if !contains(output, "❌") {
		t.Error("expected error marker")
	}
}

func TestBuildToolMessages(t *testing.T) {
	calls := []FunctionCall{
		{ID: "call_1", Name: "add", Arguments: `{"a":1,"b":2}`},
	}
	results := []FunctionResult{
		{CallID: "call_1", Name: "add", Content: "3", IsError: false},
	}

	messages := BuildToolMessages(calls, results)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}

	// First message should be assistant with tool_calls
	if messages[0].Role != "assistant" {
		t.Errorf("expected role=assistant, got %s", messages[0].Role)
	}
	if len(messages[0].ToolCalls) != 1 {
		t.Errorf("expected 1 tool_call, got %d", len(messages[0].ToolCalls))
	}

	// Second message should be tool result
	if messages[1].Role != "tool" {
		t.Errorf("expected role=tool, got %s", messages[1].Role)
	}
	if messages[1].ToolCallID != "call_1" {
		t.Errorf("expected tool_call_id=call_1, got %s", messages[1].ToolCallID)
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if opts.Mode != CallModeAuto {
		t.Errorf("expected CallModeAuto, got %d", opts.Mode)
	}
	if opts.MaxCalls != 5 {
		t.Errorf("expected MaxCalls=5, got %d", opts.MaxCalls)
	}
	if !opts.ParallelOK {
		t.Error("expected ParallelOK=true")
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
