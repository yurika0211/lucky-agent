package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	"github.com/yurika0211/luckyharness/internal/rag"
)

type namedImageTestProvider struct{}

func (namedImageTestProvider) Name() string { return "named-image-provider" }
func (namedImageTestProvider) SupportedModalities() []multimodal.Modality {
	return []multimodal.Modality{multimodal.ModalityImage}
}
func (namedImageTestProvider) Analyze(ctx context.Context, input *multimodal.Input) (*multimodal.AnalysisResult, error) {
	return &multimodal.AnalysisResult{
		InputID:    input.ID,
		Modality:   input.Modality,
		Text:       "named provider text",
		Summary:    "named provider summary",
		Labels:     []string{"named"},
		Confidence: 0.99,
		Metadata: map[string]string{
			"source": "named-provider",
		},
	}, nil
}
func (namedImageTestProvider) AnalyzeStream(ctx context.Context, input *multimodal.Input) (<-chan multimodal.StreamChunk, error) {
	ch := make(chan multimodal.StreamChunk, 1)
	close(ch)
	return ch, nil
}
func (namedImageTestProvider) Validate() error { return nil }

func TestBuiltinToolsRegistration(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	expected := []string{"terminal", "shell", "file_read", "file_write", "file_patch", "file_list", "web_search", "web_fetch", "current_time", "calculate", "image_analyze", "remember", "recall", "rag_search", "rag_index"}
	for _, name := range expected {
		tool, ok := r.Get(name)
		if !ok {
			t.Errorf("builtin tool %s not registered", name)
			continue
		}
		if tool.Category != CatBuiltin {
			t.Errorf("expected CatBuiltin for %s, got %s", name, tool.Category)
		}
		if tool.Source != "builtin" {
			t.Errorf("expected source=builtin for %s, got %s", name, tool.Source)
		}
	}

	if r.Count() != len(expected) {
		t.Errorf("expected %d builtin tools, got %d", len(expected), r.Count())
	}

	visible := r.ListModelVisible()
	foundTerminal := false
	foundShell := false
	for _, tool := range visible {
		if tool.Name == "terminal" {
			foundTerminal = true
		}
		if tool.Name == "shell" {
			foundShell = true
		}
	}
	if !foundTerminal {
		t.Error("expected terminal to be model-visible")
	}
	if foundShell {
		t.Error("expected shell compatibility tool to be hidden from model")
	}
}

func TestCurrentTimeTool(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	result, err := r.Call("current_time", map[string]any{})
	if err != nil {
		t.Fatalf("current_time call: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty time result")
	}
}

func TestCalculateTool(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	result, err := r.Call("calculate", map[string]any{
		"expression": "sqrt(144)+pow(2,3)",
	})
	if err != nil {
		t.Fatalf("calculate call: %v", err)
	}
	if result != "20" {
		t.Fatalf("expected 20, got %q", result)
	}
}

func TestImageAnalyzeTool(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityImage,
		multimodal.ModalityDocument,
	), true); err != nil {
		t.Fatalf("register provider: %v", err)
	}

	r := NewRegistry()
	RegisterBuiltinTools(r, processor)

	result, err := r.Call("image_analyze", map[string]any{
		"base64_data": "ZmFrZS1pbWFnZS1ieXRlcw==",
		"mime_type":   "image/png",
	})
	if err != nil {
		t.Fatalf("image_analyze call: %v", err)
	}
	if !strings.Contains(result, "Modality: image") {
		t.Fatalf("expected image modality, got %q", result)
	}
	if !strings.Contains(result, "Summary:") {
		t.Fatalf("expected summary, got %q", result)
	}
}

func TestImageAnalyzeToolUsesConfiguredDefaultProvider(t *testing.T) {
	processor := multimodal.NewProcessor()
	if err := processor.RegisterProvider(multimodal.NewLocalProvider(
		multimodal.ModalityImage,
	), true); err != nil {
		t.Fatalf("register local provider: %v", err)
	}
	if err := processor.RegisterProvider(namedImageTestProvider{}, false); err != nil {
		t.Fatalf("register named provider: %v", err)
	}

	r := NewRegistry()
	r.Register(ImageAnalyzeTool(processor, "named-image-provider"))

	result, err := r.Call("image_analyze", map[string]any{
		"base64_data": "ZmFrZS1pbWFnZS1ieXRlcw==",
		"mime_type":   "image/png",
	})
	if err != nil {
		t.Fatalf("image_analyze call: %v", err)
	}
	if !strings.Contains(result, "named provider summary") {
		t.Fatalf("expected configured provider output, got %q", result)
	}
}

func TestFileReadWriteTool(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	// 创建临时目录
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	// 写文件
	writeResult, err := r.Call("file_write", map[string]any{
		"path":    testFile,
		"content": "Hello, LuckyHarness!",
	})
	if err != nil {
		t.Fatalf("file_write: %v", err)
	}
	if writeResult == "" {
		t.Error("expected write result")
	}

	// 读文件
	readResult, err := r.Call("file_read", map[string]any{
		"path": testFile,
	})
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	if readResult == "" {
		t.Error("expected read result")
	}
}

func TestFilePatchTool(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch.txt")
	if err := os.WriteFile(testFile, []byte("alpha\nbeta\nbeta\ngamma\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := r.Call("file_patch", map[string]any{
		"path":       testFile,
		"match":      "beta",
		"replace":    "delta",
		"occurrence": 2,
	})
	if err != nil {
		t.Fatalf("file_patch: %v", err)
	}
	if !strings.Contains(result, "Patched") {
		t.Fatalf("expected patch result, got %q", result)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "alpha\nbeta\ndelta\ngamma\n" {
		t.Fatalf("unexpected patched content: %q", got)
	}
}

func TestFilePatchToolReplaceAll(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch-all.txt")
	if err := os.WriteFile(testFile, []byte("foo bar foo\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := r.Call("file_patch", map[string]any{
		"path":        testFile,
		"match":       "foo",
		"replace":     "baz",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("file_patch replace_all: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "baz bar baz\n" {
		t.Fatalf("unexpected patched content: %q", got)
	}
}

func TestFilePatchToolErrors(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch-error.txt")
	if err := os.WriteFile(testFile, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := r.Call("file_patch", map[string]any{
		"path":    testFile,
		"match":   "missing",
		"replace": "x",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing match error, got %v", err)
	}

	_, err = r.Call("file_patch", map[string]any{
		"path":       testFile,
		"match":      "two",
		"replace":    "x",
		"occurrence": 2,
	})
	if err == nil || !strings.Contains(err.Error(), "occurrence") {
		t.Fatalf("expected occurrence error, got %v", err)
	}
}

func TestFileListTool(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	// 创建临时目录和文件
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("b"), 0644)

	result, err := r.Call("file_list", map[string]any{
		"path": tmpDir,
	})
	if err != nil {
		t.Fatalf("file_list: %v", err)
	}
	if result == "" {
		t.Error("expected list result")
	}
}

func TestFileListToolTruncatesRecursiveOutput(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(tmpDir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0644); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	result, err := r.Call("file_list", map[string]any{
		"path":        tmpDir,
		"recursive":   true,
		"max_entries": 5,
	})
	if err != nil {
		t.Fatalf("file_list recursive: %v", err)
	}
	if !strings.Contains(result, "truncated after 5 entries") {
		t.Fatalf("expected truncation marker, got %q", result)
	}
}

func TestMemoryToolServiceRememberAndRecall(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	result, err := svc.HandleRemember(map[string]any{
		"content":  "用户喜欢Python",
		"category": "preference",
	})
	if err != nil {
		t.Fatalf("HandleRemember: %v", err)
	}
	if !strings.Contains(result, "已保存") {
		t.Fatalf("unexpected remember result: %q", result)
	}

	recall, err := svc.HandleRecall(map[string]any{"query": "Python"})
	if err != nil {
		t.Fatalf("HandleRecall: %v", err)
	}
	if !strings.Contains(recall, "Python") {
		t.Fatalf("unexpected recall result: %q", recall)
	}
}

func TestMemoryToolServiceRememberLongTerm(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	svc := NewMemoryToolService(store)

	result, err := svc.HandleRemember(map[string]any{
		"content":   "重要密码",
		"category":  "security",
		"long_term": true,
	})
	if err != nil {
		t.Fatalf("HandleRemember: %v", err)
	}
	if !strings.Contains(result, "长期记忆") {
		t.Fatalf("unexpected remember result: %q", result)
	}
}

func TestRAGToolServiceSearchAndIndex(t *testing.T) {
	emb := rag.NewMockEmbedder(8)
	mgr := rag.NewRAGManager(emb, rag.DefaultRAGConfig())
	svc := NewRAGToolService(mgr)

	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Demo\n\nalpha beta gamma"), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	indexResult, err := svc.HandleIndex(map[string]any{"path": path})
	if err != nil {
		t.Fatalf("HandleIndex: %v", err)
	}
	if !strings.Contains(indexResult, "Indexed") {
		t.Fatalf("unexpected index result: %q", indexResult)
	}

	searchResult, err := svc.HandleSearch(map[string]any{"query": "alpha", "top_k": 3})
	if err != nil {
		t.Fatalf("HandleSearch: %v", err)
	}
	if !strings.Contains(searchResult, "alpha") && !strings.Contains(searchResult, "Demo") {
		t.Fatalf("unexpected search result: %q", searchResult)
	}
}

func TestShellTool(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	result, err := r.Call("terminal", map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("terminal call: %v", err)
	}
	if result == "" {
		t.Error("expected terminal result")
	}
}

func TestPathTraversal(t *testing.T) {
	err := validatePath("../../etc/passwd")
	if err == nil {
		t.Error("expected path traversal error")
	}

	err = validatePath("/tmp/safe/path")
	if err != nil {
		t.Errorf("safe path should pass: %v", err)
	}
}

func TestToolPermissions(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	// 只读工具应该是 auto
	readPerm, _ := r.CheckPermission("file_read")
	if readPerm != PermAuto {
		t.Errorf("file_read should be auto, got %s", readPerm)
	}

	// 写操作应该是 approve
	writePerm, _ := r.CheckPermission("file_write")
	if writePerm != PermApprove {
		t.Errorf("file_write should be approve, got %s", writePerm)
	}
	patchPerm, _ := r.CheckPermission("file_patch")
	if patchPerm != PermApprove {
		t.Errorf("file_patch should be approve, got %s", patchPerm)
	}

	// shell 应该是 approve
	terminalPerm, _ := r.CheckPermission("terminal")
	if terminalPerm != PermApprove {
		t.Errorf("terminal should be approve, got %s", terminalPerm)
	}
	shellPerm, _ := r.CheckPermission("shell")
	if shellPerm != PermApprove {
		t.Errorf("shell compatibility tool should be approve, got %s", shellPerm)
	}

	// current_time 应该是 auto
	timePerm, _ := r.CheckPermission("current_time")
	if timePerm != PermAuto {
		t.Errorf("current_time should be auto, got %s", timePerm)
	}
}

func TestSandboxPathValidation(t *testing.T) {
	home, _ := os.UserHomeDir()
	lhDir := filepath.Join(home, ".luckyharness")

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"luckyharness dir allowed", lhDir, false},
		{"luckyharness subfile allowed", filepath.Join(lhDir, "memory.json"), false},
		{"tmp allowed", "/tmp/test.txt", false},
		{"nanobot denied", filepath.Join(home, ".nanobot", "config.json"), true},
		{"ssh denied", filepath.Join(home, ".ssh", "id_rsa"), true},
		{"etc shadow denied", "/etc/shadow", true},
		{"root home denied", home + "/.bashrc", true},
		{"path traversal denied", filepath.Join(lhDir, "../../../etc/passwd"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePath(%q) error = %v, wantErr %v", tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestSandboxPathValidationAllowsProjectLocalHome(t *testing.T) {
	projectHome := filepath.Join(t.TempDir(), ".lh-home")
	if err := os.MkdirAll(filepath.Join(projectHome, ".luckyharness"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", projectHome)

	if err := validatePath(filepath.Join(projectHome, "skills")); err != nil {
		t.Fatalf("expected project-local lh home to be allowed, got %v", err)
	}
}

func TestShellSandboxValidation(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		wantErr bool
	}{
		{"ls luckyharness ok", "ls ~/.luckyharness/", false},
		{"cat nanobot denied", "cat ~/.nanobot/config.json", true},
		{"grep ssh denied", "grep key ~/.ssh/id_rsa", true},
		{"echo OPENAI_API_KEY denied", "echo $OPENAI_API_KEY", true},
		{"cat FILEBROWSER denied", "echo $FILEBROWSER_TOKEN", true},
		{"normal command ok", "ls -la /tmp/", false},
		{"python script ok", "python3 /tmp/test.py", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateShellSandbox(tt.cmd)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateShellSandbox(%q) error = %v, wantErr %v", tt.cmd, err, tt.wantErr)
			}
		})
	}
}

func TestResolveExaAPIKey(t *testing.T) {
	t.Setenv("LH_SEARCH_EXA_KEY", "")
	t.Setenv("EXA_API_KEY", "")

	cfg := &WebSearchConfig{Provider: "exa", APIKey: "cfg-key"}
	if got := resolveExaAPIKey(cfg); got != "cfg-key" {
		t.Fatalf("expected cfg key, got %q", got)
	}

	t.Setenv("LH_SEARCH_EXA_KEY", "lh-exa-key")
	if got := resolveExaAPIKey(&WebSearchConfig{}); got != "lh-exa-key" {
		t.Fatalf("expected LH_SEARCH_EXA_KEY, got %q", got)
	}

	t.Setenv("LH_SEARCH_EXA_KEY", "")
	t.Setenv("EXA_API_KEY", "env-exa-key")
	if got := resolveExaAPIKey(&WebSearchConfig{}); got != "env-exa-key" {
		t.Fatalf("expected EXA_API_KEY, got %q", got)
	}
}

func TestQuickSearchOrderPrefersExa(t *testing.T) {
	order := quickSearchOrder("searxng", &WebSearchConfig{BaseURL: "https://search.shiokou.asia"})
	if len(order) == 0 || order[0] != "exa" {
		t.Fatalf("expected exa first, got %v", order)
	}
}

func TestDeepSearchOrderPrefersExa(t *testing.T) {
	t.Setenv("EXA_API_KEY", "env-exa-key")
	order := deepSearchOrder("searxng", &WebSearchConfig{BaseURL: "https://search.shiokou.asia"})
	if len(order) == 0 || order[0] != "exa" {
		t.Fatalf("expected exa first, got %v", order)
	}
}
