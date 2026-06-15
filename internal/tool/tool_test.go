package tool

import (
	"testing"
)

func TestPermissionLevelString(t *testing.T) {
	tests := []struct {
		perm     PermissionLevel
		expected string
	}{
		{PermAuto, "auto"},
		{PermApprove, "approve"},
		{PermDeny, "deny"},
	}
	for _, tt := range tests {
		if got := tt.perm.String(); got != tt.expected {
			t.Errorf("PermissionLevel(%d).String() = %q, want %q", tt.perm, got, tt.expected)
		}
	}
}

func TestRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "echo",
		Description: "Echo back the input",
		Category:    CatBuiltin,
		Permission:  PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return args["text"].(string), nil
		},
	})

	tool, ok := r.Get("echo")
	if !ok {
		t.Error("tool not found")
	}
	if tool.Name != "echo" {
		t.Errorf("expected echo, got %s", tool.Name)
	}
	if tool.Category != CatBuiltin {
		t.Errorf("expected builtin category, got %s", tool.Category)
	}
	if tool.Permission != PermAuto {
		t.Errorf("expected auto permission, got %s", tool.Permission)
	}
}

func TestCall(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "add",
		Description: "Add numbers",
		Category:    CatBuiltin,
		Permission:  PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return "result", nil
		},
	})

	result, err := r.Call("add", map[string]any{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != "result" {
		t.Errorf("expected result, got %s", result)
	}
}

func TestCallNotFound(t *testing.T) {
	r := NewRegistry()
	_, err := r.Call("nonexistent", nil)
	if err == nil {
		t.Error("expected error for nonexistent tool")
	}
}

func TestCallDisabled(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "disabled_tool",
		Description: "A disabled tool",
		Permission:  PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return "should not reach", nil
		},
	})
	r.Disable("disabled_tool")

	_, err := r.Call("disabled_tool", nil)
	if err == nil {
		t.Error("expected error for disabled tool")
	}
	if _, ok := err.(ErrToolDisabled); !ok {
		t.Errorf("expected ErrToolDisabled, got %T", err)
	}
}

func TestCallDenied(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "denied_tool",
		Description: "A denied tool",
		Permission:  PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return "should not reach", nil
		},
	})
	r.SetPermissionOverride("denied_tool", PermDeny)

	_, err := r.Call("denied_tool", nil)
	if err == nil {
		t.Error("expected error for denied tool")
	}
	if _, ok := err.(ErrToolDenied); !ok {
		t.Errorf("expected ErrToolDenied, got %T", err)
	}
}

func TestList(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{Name: "b", Category: CatBuiltin})
	r.Register(&Tool{Name: "a", Category: CatBuiltin})
	r.Register(&Tool{Name: "c", Category: CatSkill})

	tools := r.List()
	if len(tools) != 3 {
		t.Errorf("expected 3 tools, got %d", len(tools))
	}
	// Should be sorted
	if tools[0].Name != "a" || tools[1].Name != "b" || tools[2].Name != "c" {
		t.Error("tools not sorted by name")
	}
}

func TestListByCategory(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{Name: "a", Category: CatBuiltin})
	r.Register(&Tool{Name: "b", Category: CatSkill})
	r.Register(&Tool{Name: "c", Category: CatBuiltin})

	builtin := r.ListByCategory(CatBuiltin)
	if len(builtin) != 2 {
		t.Errorf("expected 2 builtin tools, got %d", len(builtin))
	}

	skill := r.ListByCategory(CatSkill)
	if len(skill) != 1 {
		t.Errorf("expected 1 skill tool, got %d", len(skill))
	}
}

func TestListEnabled(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{Name: "a", Category: CatBuiltin})
	r.Register(&Tool{Name: "b", Category: CatBuiltin})
	r.Disable("b")

	enabled := r.ListEnabled()
	if len(enabled) != 1 {
		t.Errorf("expected 1 enabled tool, got %d", len(enabled))
	}
	if enabled[0].Name != "a" {
		t.Errorf("expected a, got %s", enabled[0].Name)
	}
}

func TestEnableDisable(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{Name: "test", Category: CatBuiltin})

	if err := r.Disable("test"); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	tool, _ := r.Get("test")
	if tool.Enabled {
		t.Error("tool should be disabled")
	}

	if err := r.Enable("test"); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	tool, _ = r.Get("test")
	if !tool.Enabled {
		t.Error("tool should be enabled")
	}
}

func TestPermissionOverride(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:       "test",
		Permission: PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return "ok", nil
		},
	})

	// 原始权限
	perm, err := r.CheckPermission("test")
	if err != nil || perm != PermAuto {
		t.Errorf("expected auto, got %s, err=%v", perm, err)
	}

	// 覆盖为 deny
	r.SetPermissionOverride("test", PermDeny)
	perm, err = r.CheckPermission("test")
	if err != nil || perm != PermDeny {
		t.Errorf("expected deny after override, got %s", perm)
	}

	// 调用应该被拒绝
	_, err = r.Call("test", nil)
	if err == nil {
		t.Error("expected call to be denied")
	}
}

func TestUnregister(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{Name: "temp"})
	r.Unregister("temp")

	_, ok := r.Get("temp")
	if ok {
		t.Error("tool should be unregistered")
	}
}

func TestCount(t *testing.T) {
	r := NewRegistry()
	if r.Count() != 0 {
		t.Errorf("expected 0, got %d", r.Count())
	}
	r.Register(&Tool{Name: "a"})
	r.Register(&Tool{Name: "b"})
	if r.Count() != 2 {
		t.Errorf("expected 2, got %d", r.Count())
	}
}

func TestToOpenAIFormat(t *testing.T) {
	tool := &Tool{
		Name:        "search",
		Description: "Search the web",
		Parameters: map[string]Param{
			"query": {
				Type:        "string",
				Description: "Search query",
				Required:    true,
			},
			"count": {
				Type:        "number",
				Description: "Number of results",
				Required:    false,
				Default:     5,
			},
		},
	}

	fmt := tool.ToOpenAIFormat()
	fn, ok := fmt["function"].(map[string]any)
	if !ok {
		t.Fatal("expected function key")
	}
	if fn["name"] != "search" {
		t.Errorf("expected name=search, got %v", fn["name"])
	}
}

func TestCallResolvesOpenAICompatibleName(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "skill_web_search___联网搜索总入口_run",
		Description: "skill",
		Permission:  PermAuto,
		Handler: func(args map[string]any) (string, error) {
			return "ok", nil
		},
	})

	result, err := r.Call("skill_web_search___________run", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %q", result)
	}
}

func TestCallWithShellContextResolvesOpenAICompatibleName(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "skill_web_search___联网搜索总入口_run",
		Description: "skill",
		Permission:  PermAuto,
		ShellAware:  true,
		Handler: func(args map[string]any) (string, error) {
			if _, ok := args["_cwd"]; !ok {
				t.Fatal("expected injected cwd")
			}
			return "ok", nil
		},
	})

	result, err := r.CallWithShellContext("skill_web_search___________run", nil, &ShellContext{Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("CallWithShellContext: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected ok, got %q", result)
	}
}

func TestFormatToolList(t *testing.T) {
	r := NewRegistry()
	r.Register(&Tool{
		Name:        "shell",
		Description: "Execute shell commands",
		Category:    CatBuiltin,
		Permission:  PermApprove,
	})
	r.Register(&Tool{
		Name:        "skill_search",
		Description: "Search skill",
		Category:    CatSkill,
		Permission:  PermAuto,
	})

	list := r.FormatToolList()
	if list == "" {
		t.Error("expected non-empty tool list")
	}
	if !contains(list, "shell") {
		t.Error("expected shell in list")
	}
	if !contains(list, "skill_search") {
		t.Error("expected skill_search in list")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestOpenCLIToolCoerceStringSlice(t *testing.T) {
	got, err := coerceStringSlice([]any{"doctor", "list"})
	if err != nil {
		t.Fatalf("coerceStringSlice: %v", err)
	}
	if len(got) != 2 || got[0] != "doctor" || got[1] != "list" {
		t.Fatalf("unexpected args: %#v", got)
	}
}
