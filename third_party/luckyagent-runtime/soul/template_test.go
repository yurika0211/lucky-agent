package soul

import (
	"strings"
	"testing"
)

// TestNewTemplateManager 测试模板管理器创建
func TestNewTemplateManager(t *testing.T) {
	tm := NewTemplateManager()
	if tm == nil {
		t.Fatal("expected non-nil TemplateManager")
	}

	templates := tm.ListTemplates()
	if len(templates) == 0 {
		t.Error("expected at least one builtin template")
	}
}

// TestListTemplates 测试列出模板
func TestListTemplates(t *testing.T) {
	tm := NewTemplateManager()
	templates := tm.ListTemplates()

	// 应该有内置模板
	foundDefault := false
	for _, tmpl := range templates {
		if tmpl.ID == "default" {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Error("expected default template to be present")
	}
}

// TestListByLanguage 测试按语言列出模板
func TestListByLanguage(t *testing.T) {
	tm := NewTemplateManager()

	zhTemplates := tm.ListByLanguage("zh")
	if len(zhTemplates) == 0 {
		t.Error("expected at least one Chinese template")
	}

	enTemplates := tm.ListByLanguage("en")
	if len(enTemplates) == 0 {
		t.Error("expected at least one English template")
	}

	jaTemplates := tm.ListByLanguage("ja")
	if len(jaTemplates) == 0 {
		t.Error("expected at least one Japanese template")
	}

	koTemplates := tm.ListByLanguage("ko")
	if len(koTemplates) == 0 {
		t.Error("expected at least one Korean template")
	}

	// 不存在的语言应返回空
	xxTemplates := tm.ListByLanguage("xx")
	if len(xxTemplates) != 0 {
		t.Error("expected no templates for unknown language")
	}
}

// TestGetTemplate 测试获取模板
func TestGetTemplate(t *testing.T) {
	tm := NewTemplateManager()

	tmpl, err := tm.GetTemplate("default")
	if err != nil {
		t.Fatalf("GetTemplate error: %v", err)
	}
	if tmpl.ID != "default" {
		t.Errorf("expected ID 'default', got %q", tmpl.ID)
	}
	if tmpl.Language != "en" {
		t.Errorf("expected language 'en', got %q", tmpl.Language)
	}

	// 不存在的模板
	_, err = tm.GetTemplate("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent template")
	}
}

// TestAddTemplate 测试添加自定义模板
func TestAddTemplate(t *testing.T) {
	tm := NewTemplateManager()

	customTmpl := &Template{
		ID:          "custom-test",
		Name:        "Custom Test",
		Language:    "en",
		Description: "A custom test template",
		Content:     "You are a custom test assistant.",
		Variables:   map[string]string{"name": "Test"},
		Tags:        []string{"test"},
	}

	err := tm.AddTemplate(customTmpl)
	if err != nil {
		t.Fatalf("AddTemplate error: %v", err)
	}

	retrieved, err := tm.GetTemplate("custom-test")
	if err != nil {
		t.Fatalf("GetTemplate error: %v", err)
	}
	if retrieved.Name != "Custom Test" {
		t.Errorf("expected name 'Custom Test', got %q", retrieved.Name)
	}
}

// TestAddTemplateEmptyID 测试添加空 ID 模板
func TestAddTemplateEmptyID(t *testing.T) {
	tm := NewTemplateManager()

	err := tm.AddTemplate(&Template{ID: "", Name: "No ID"})
	if err == nil {
		t.Error("expected error for empty ID")
	}
}

// TestRemoveTemplate 测试移除自定义模板
func TestRemoveTemplate(t *testing.T) {
	tm := NewTemplateManager()

	customTmpl := &Template{
		ID:       "removable",
		Name:     "Removable",
		Language: "en",
		Content:  "You are removable.",
	}
	tm.AddTemplate(customTmpl)

	err := tm.RemoveTemplate("removable")
	if err != nil {
		t.Fatalf("RemoveTemplate error: %v", err)
	}

	_, err = tm.GetTemplate("removable")
	if err == nil {
		t.Error("expected error after removal")
	}
}

// TestRemoveBuiltinTemplate 测试移除内置模板（应该失败）
func TestRemoveBuiltinTemplate(t *testing.T) {
	tm := NewTemplateManager()

	err := tm.RemoveTemplate("default")
	if err == nil {
		t.Error("expected error when removing builtin template")
	}
}

// TestRemoveNonexistentTemplate 测试移除不存在的模板
func TestRemoveNonexistentTemplate(t *testing.T) {
	tm := NewTemplateManager()

	err := tm.RemoveTemplate("nonexistent")
	if err == nil {
		t.Error("expected error when removing nonexistent template")
	}
}

// TestRender 测试模板渲染
func TestRender(t *testing.T) {
	tmpl := &Template{
		ID:        "test-render",
		Name:      "Test Render",
		Language:  "en",
		Content:   "Hello, {{name}}! You are a {{role}}.",
		Variables: map[string]string{"name": "User", "role": "assistant"},
	}

	// 使用默认变量
	rendered := tmpl.Render(nil)
	if !strings.Contains(rendered, "Hello, User!") {
		t.Errorf("expected default variable substitution, got %q", rendered)
	}
	if !strings.Contains(rendered, "You are a assistant.") {
		t.Errorf("expected default variable substitution, got %q", rendered)
	}

	// 使用自定义变量覆盖
	rendered = tmpl.Render(map[string]string{"name": "Alice", "role": "coder"})
	if !strings.Contains(rendered, "Hello, Alice!") {
		t.Errorf("expected custom variable substitution, got %q", rendered)
	}
	if !strings.Contains(rendered, "You are a coder.") {
		t.Errorf("expected custom variable substitution, got %q", rendered)
	}
}

// TestRenderNoVariables 测试无变量模板渲染
func TestRenderNoVariables(t *testing.T) {
	tmpl := &Template{
		ID:       "no-vars",
		Name:     "No Vars",
		Language: "en",
		Content:  "You are a helpful assistant.",
	}

	rendered := tmpl.Render(nil)
	if rendered != "You are a helpful assistant." {
		t.Errorf("expected unchanged content, got %q", rendered)
	}
}

// TestDetectLanguage 测试语言检测
func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"你好世界", "zh"},
		{"Hello world", "en"},
		{"こんにちは", "ja"},
		{"안녕하세요", "ko"},
		{"这是中文文本", "zh"},
		{"This is English text", "en"},
		{"今日は良い天気ですね", "ja"},
		{"대한민국", "ko"},
		{"1234567890", "en"}, // 纯数字默认英文
		{"", "en"},           // 空字符串默认英文
	}

	for _, tt := range tests {
		result := DetectLanguage(tt.input)
		if result != tt.expected {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestSupportedLanguages 测试支持的语言列表
func TestSupportedLanguages(t *testing.T) {
	langs := SupportedLanguages()
	if len(langs) < 4 {
		t.Errorf("expected at least 4 languages, got %d", len(langs))
	}

	found := make(map[string]bool)
	for _, l := range langs {
		found[l] = true
	}
	for _, expected := range []string{"zh", "en", "ja", "ko"} {
		if !found[expected] {
			t.Errorf("expected language %q in supported languages", expected)
		}
	}
}

// TestLanguageName 测试语言名称
func TestLanguageName(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"zh", "中文"},
		{"en", "English"},
		{"ja", "日本語"},
		{"ko", "한국어"},
		{"xx", "xx"}, // 未知代码返回代码本身
	}

	for _, tt := range tests {
		result := LanguageName(tt.code)
		if result != tt.expected {
			t.Errorf("LanguageName(%q) = %q, want %q", tt.code, result, tt.expected)
		}
	}
}

// TestParseTemplate 测试模板解析
func TestParseTemplate(t *testing.T) {
	// 有 frontmatter 的模板
	raw := `---
id: test-parse
name: Test Parse
language: en
description: A test template
tags: test, parse
---
You are a test assistant.`
	tmpl := parseTemplate(raw)
	if tmpl == nil {
		t.Fatal("expected non-nil template")
	}
	if tmpl.ID != "test-parse" {
		t.Errorf("expected ID 'test-parse', got %q", tmpl.ID)
	}
	if tmpl.Name != "Test Parse" {
		t.Errorf("expected name 'Test Parse', got %q", tmpl.Name)
	}
	if tmpl.Language != "en" {
		t.Errorf("expected language 'en', got %q", tmpl.Language)
	}
	if tmpl.Content != "You are a test assistant." {
		t.Errorf("expected content 'You are a test assistant.', got %q", tmpl.Content)
	}
}

// TestParseTemplateNoFrontmatter 测试无 frontmatter 的模板
func TestParseTemplateNoFrontmatter(t *testing.T) {
	raw := "You are a simple assistant."
	tmpl := parseTemplate(raw)
	if tmpl == nil {
		t.Fatal("expected non-nil template")
	}
	if tmpl.ID != "default" {
		t.Errorf("expected ID 'default', got %q", tmpl.ID)
	}
	if tmpl.Content != "You are a simple assistant." {
		t.Errorf("unexpected content: %q", tmpl.Content)
	}
}

// TestBuiltinTemplatesLoaded 测试内置模板加载
func TestBuiltinTemplatesLoaded(t *testing.T) {
	tm := NewTemplateManager()

	expectedIDs := []string{"default", "zh-assistant", "ja-assistant", "ko-assistant", "zh-coder", "en-coder"}
	for _, id := range expectedIDs {
		tmpl, err := tm.GetTemplate(id)
		if err != nil {
			t.Errorf("expected builtin template %q to be loaded: %v", id, err)
			continue
		}
		if tmpl.Content == "" {
			t.Errorf("template %q has empty content", id)
		}
	}
}

// TestTemplateRenderWithBuiltin 测试内置模板渲染
func TestTemplateRenderWithBuiltin(t *testing.T) {
	tm := NewTemplateManager()

	tmpl, err := tm.GetTemplate("default")
	if err != nil {
		t.Fatalf("GetTemplate error: %v", err)
	}

	rendered := tmpl.Render(nil)
	if rendered == "" {
		t.Error("expected non-empty rendered content")
	}
}
