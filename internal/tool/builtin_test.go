package tool

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/multimodal"
	"github.com/yurika0211/luckyharness/internal/rag"
)

type namedImageTestProvider struct{}
type fakeImageGenerator struct {
	lastReq multimodal.ImageGenerationRequest
	result  *multimodal.ImageGenerationResult
}
type fakeSpeechSynthesizer struct {
	lastReq multimodal.SpeechSynthesisRequest
	result  *multimodal.SpeechSynthesisResult
}

func (g *fakeImageGenerator) Name() string { return "fake-image-generator" }
func (g *fakeImageGenerator) GenerateImage(ctx context.Context, req multimodal.ImageGenerationRequest) (*multimodal.ImageGenerationResult, error) {
	g.lastReq = req
	if g.result != nil {
		return g.result, nil
	}
	return &multimodal.ImageGenerationResult{
		Provider: "fake-image-generator",
		Model:    "fake-model",
		Images: []multimodal.GeneratedImage{
			{Data: []byte("png-bytes"), MimeType: "image/png"},
		},
	}, nil
}
func (s *fakeSpeechSynthesizer) Name() string { return "fake-speech-synthesizer" }
func (s *fakeSpeechSynthesizer) SynthesizeSpeech(ctx context.Context, req multimodal.SpeechSynthesisRequest) (*multimodal.SpeechSynthesisResult, error) {
	s.lastReq = req
	if s.result != nil {
		return s.result, nil
	}
	return &multimodal.SpeechSynthesisResult{
		Provider: "fake-speech-synthesizer",
		Model:    "fake-tts-model",
		Voice:    "fake-voice",
		Audio:    []byte("mp3-bytes"),
		MimeType: "audio/mpeg",
	}, nil
}

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

	expected := []string{"terminal", "shell", "file_read", "file_write", "file_mkdir", "file_move", "file_delete", "file_patch", "file_list", "web_search", "web_fetch", "current_time", "calculate", "image_analyze", "image_generate", "text_to_speech", "log_tail", "log_grep", "http_request", "json_query", "yaml_query", "csv_query", "sql_query", "db_schema", "remember", "recall", "rag_search", "rag_index"}
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

func TestCurrentTimeToolUsesNetworkTimeForLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/Asia/Shanghai" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"datetime":"2026-05-14T10:00:00+08:00"}`))
	}))
	defer server.Close()

	oldClient := currentTimeHTTPClient
	oldBase := currentTimeAPIBaseURL
	currentTimeHTTPClient = server.Client()
	currentTimeAPIBaseURL = server.URL
	defer func() {
		currentTimeHTTPClient = oldClient
		currentTimeAPIBaseURL = oldBase
	}()

	r := NewRegistry()
	RegisterBuiltinTools(r)

	result, err := r.Call("current_time", map[string]any{
		"location": "北京",
	})
	if err != nil {
		t.Fatalf("current_time call: %v", err)
	}
	if !strings.Contains(result, "Asia/Shanghai") {
		t.Fatalf("expected timezone in result, got %q", result)
	}
	if !strings.Contains(result, "location: 北京") {
		t.Fatalf("expected location label, got %q", result)
	}
}

func TestCurrentTimeToolFallsBackToLocalOnNetworkFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	oldClient := currentTimeHTTPClient
	oldBase := currentTimeAPIBaseURL
	currentTimeHTTPClient = server.Client()
	currentTimeAPIBaseURL = server.URL
	defer func() {
		currentTimeHTTPClient = oldClient
		currentTimeAPIBaseURL = oldBase
	}()

	r := NewRegistry()
	RegisterBuiltinTools(r)

	result, err := r.Call("current_time", map[string]any{
		"location": "北京",
	})
	if err != nil {
		t.Fatalf("current_time call: %v", err)
	}
	if !strings.Contains(result, "source: local") {
		t.Fatalf("expected local fallback, got %q", result)
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

func TestLogTailAndLogGrepTools(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "app.log")
	content := "one\nerror first\ntwo\nthree\nerror second\nfour\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	tail, err := r.Call("log_tail", map[string]any{"path": testFile, "lines": 2})
	if err != nil {
		t.Fatalf("log_tail: %v", err)
	}
	if !strings.Contains(tail, "error second") || !strings.Contains(tail, "four") {
		t.Fatalf("unexpected tail output: %q", tail)
	}

	grep, err := r.Call("log_grep", map[string]any{"path": testFile, "pattern": "error", "before": 1, "after": 0})
	if err != nil {
		t.Fatalf("log_grep: %v", err)
	}
	if !strings.Contains(grep, "> 2| error first") || !strings.Contains(grep, "> 5| error second") {
		t.Fatalf("unexpected grep output: %q", grep)
	}
}

func TestHTTPRequestToolValidation(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	_, err := r.Call("http_request", map[string]any{
		"url": "http://localhost:9999/test",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "localhost") {
		t.Fatalf("expected localhost validation error, got %v", err)
	}
}

func TestStructuredQueryTools(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()

	jsonPath := filepath.Join(tmpDir, "sample.json")
	if err := os.WriteFile(jsonPath, []byte(`{"user":{"name":"Ada"},"items":[{"id":1},{"id":2}]}`), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	jsonResult, err := r.Call("json_query", map[string]any{"path": jsonPath, "query": "items[1].id"})
	if err != nil {
		t.Fatalf("json_query: %v", err)
	}
	if strings.TrimSpace(jsonResult) != "2" {
		t.Fatalf("unexpected json_query output: %q", jsonResult)
	}

	yamlPath := filepath.Join(tmpDir, "sample.yaml")
	if err := os.WriteFile(yamlPath, []byte("service:\n  name: api\n"), 0644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}
	yamlResult, err := r.Call("yaml_query", map[string]any{"path": yamlPath, "query": "service.name"})
	if err != nil {
		t.Fatalf("yaml_query: %v", err)
	}
	if strings.TrimSpace(yamlResult) != `"api"` {
		t.Fatalf("unexpected yaml_query output: %q", yamlResult)
	}

	csvPath := filepath.Join(tmpDir, "sample.csv")
	if err := os.WriteFile(csvPath, []byte("name,role\nAda,admin\nBob,user\n"), 0644); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	csvResult, err := r.Call("csv_query", map[string]any{"path": csvPath, "column": "role", "equals": "admin"})
	if err != nil {
		t.Fatalf("csv_query: %v", err)
	}
	if !strings.Contains(csvResult, `"name": "Ada"`) {
		t.Fatalf("unexpected csv_query output: %q", csvResult)
	}
}

func TestSQLiteTools(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "sample.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO users(name) VALUES ('Ada'), ('Bob');`); err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}

	schemaResult, err := r.Call("db_schema", map[string]any{"path": dbPath, "table": "users"})
	if err != nil {
		t.Fatalf("db_schema: %v", err)
	}
	if !strings.Contains(schemaResult, `"name": "id"`) || !strings.Contains(schemaResult, `"name": "name"`) {
		t.Fatalf("unexpected db_schema output: %q", schemaResult)
	}

	queryResult, err := r.Call("sql_query", map[string]any{"path": dbPath, "query": "SELECT name FROM users ORDER BY id"})
	if err != nil {
		t.Fatalf("sql_query: %v", err)
	}
	if !strings.Contains(queryResult, `"name": "Ada"`) || !strings.Contains(queryResult, `"name": "Bob"`) {
		t.Fatalf("unexpected sql_query output: %q", queryResult)
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

func TestImageGenerateToolSavesOutputAndSupportsImageToImage(t *testing.T) {
	gen := &fakeImageGenerator{
		result: &multimodal.ImageGenerationResult{
			Provider:      "fake-image-generator",
			Model:         "gpt-image-1.5",
			RevisedPrompt: "refined prompt",
			Images: []multimodal.GeneratedImage{
				{Data: []byte("generated-image-bytes"), MimeType: "image/png"},
			},
		},
	}

	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(inputPath, []byte("input-image-bytes"), 0o644); err != nil {
		t.Fatalf("write input image: %v", err)
	}

	r := NewRegistry()
	r.Register(ImageGenerateTool(gen, ImageGenerationDefaults{}))

	result, err := r.CallWithShellContext("image_generate", map[string]any{
		"prompt":        "turn this into a watercolor postcard",
		"input_path":    inputPath,
		"output_dir":    tmpDir,
		"count":         1,
		"output_format": "png",
	}, &ShellContext{Cwd: tmpDir})
	if err != nil {
		t.Fatalf("image_generate call: %v", err)
	}

	if gen.lastReq.Prompt != "turn this into a watercolor postcard" {
		t.Fatalf("unexpected prompt: %q", gen.lastReq.Prompt)
	}
	if len(gen.lastReq.InputImages) != 1 {
		t.Fatalf("expected 1 input image, got %d", len(gen.lastReq.InputImages))
	}
	if string(gen.lastReq.InputImages[0].Data) != "input-image-bytes" {
		t.Fatalf("unexpected input image bytes: %q", string(gen.lastReq.InputImages[0].Data))
	}

	var payload struct {
		Paths []string `json:"paths"`
		Model string   `json:"model"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal tool output: %v", err)
	}
	if payload.Model != "gpt-image-1.5" {
		t.Fatalf("unexpected model in output: %q", payload.Model)
	}
	if len(payload.Paths) != 1 {
		t.Fatalf("expected 1 saved path, got %d", len(payload.Paths))
	}
	written, err := os.ReadFile(payload.Paths[0])
	if err != nil {
		t.Fatalf("read generated output: %v", err)
	}
	if string(written) != "generated-image-bytes" {
		t.Fatalf("unexpected written bytes: %q", string(written))
	}
}

func TestImageGenerateToolUsesConfiguredDefaults(t *testing.T) {
	gen := &fakeImageGenerator{}
	r := NewRegistry()
	r.Register(ImageGenerateTool(gen, ImageGenerationDefaults{
		Model:        "gpt-image-1.5",
		Size:         "1536x1024",
		Quality:      "high",
		Background:   "transparent",
		OutputFormat: "webp",
	}))

	tmpDir := t.TempDir()
	_, err := r.CallWithShellContext("image_generate", map[string]any{
		"prompt":     "a minimal poster",
		"output_dir": tmpDir,
	}, &ShellContext{Cwd: tmpDir})
	if err != nil {
		t.Fatalf("image_generate call: %v", err)
	}

	if gen.lastReq.Model != "gpt-image-1.5" {
		t.Fatalf("expected default model, got %q", gen.lastReq.Model)
	}
	if gen.lastReq.Size != "1536x1024" {
		t.Fatalf("expected default size, got %q", gen.lastReq.Size)
	}
	if gen.lastReq.Quality != "high" {
		t.Fatalf("expected default quality, got %q", gen.lastReq.Quality)
	}
	if gen.lastReq.Background != "transparent" {
		t.Fatalf("expected default background, got %q", gen.lastReq.Background)
	}
	if gen.lastReq.OutputFormat != "webp" {
		t.Fatalf("expected default output format, got %q", gen.lastReq.OutputFormat)
	}
}

func TestTextToSpeechToolSavesOutput(t *testing.T) {
	synth := &fakeSpeechSynthesizer{
		result: &multimodal.SpeechSynthesisResult{
			Provider: "fake-speech-synthesizer",
			Model:    "gpt-4o-mini-tts",
			Voice:    "alloy",
			Audio:    []byte("speech-bytes"),
			MimeType: "audio/mpeg",
		},
	}
	tmpDir := t.TempDir()

	r := NewRegistry()
	r.Register(TextToSpeechTool(synth, TTSDefaults{}))

	result, err := r.CallWithShellContext("text_to_speech", map[string]any{
		"text":       "hello from luckyharness",
		"output_dir": tmpDir,
	}, &ShellContext{Cwd: tmpDir})
	if err != nil {
		t.Fatalf("text_to_speech call: %v", err)
	}

	if synth.lastReq.Text != "hello from luckyharness" {
		t.Fatalf("unexpected text: %q", synth.lastReq.Text)
	}

	var payload struct {
		Path  string `json:"path"`
		Model string `json:"model"`
		Voice string `json:"voice"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("unmarshal tool output: %v", err)
	}
	if payload.Model != "gpt-4o-mini-tts" || payload.Voice != "alloy" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	written, err := os.ReadFile(payload.Path)
	if err != nil {
		t.Fatalf("read synthesized output: %v", err)
	}
	if string(written) != "speech-bytes" {
		t.Fatalf("unexpected written bytes: %q", string(written))
	}
}

func TestTextToSpeechToolUsesConfiguredDefaults(t *testing.T) {
	synth := &fakeSpeechSynthesizer{}
	r := NewRegistry()
	r.Register(TextToSpeechTool(synth, TTSDefaults{
		Model:  "gpt-4o-mini-tts",
		Voice:  "alloy",
		Format: "wav",
		Speed:  1.25,
	}))

	tmpDir := t.TempDir()
	_, err := r.CallWithShellContext("text_to_speech", map[string]any{
		"text":       "test defaults",
		"output_dir": tmpDir,
	}, &ShellContext{Cwd: tmpDir})
	if err != nil {
		t.Fatalf("text_to_speech call: %v", err)
	}

	if synth.lastReq.Model != "gpt-4o-mini-tts" {
		t.Fatalf("expected default model, got %q", synth.lastReq.Model)
	}
	if synth.lastReq.Voice != "alloy" {
		t.Fatalf("expected default voice, got %q", synth.lastReq.Voice)
	}
	if synth.lastReq.Format != "wav" {
		t.Fatalf("expected default format, got %q", synth.lastReq.Format)
	}
	if synth.lastReq.Speed != 1.25 {
		t.Fatalf("expected default speed, got %f", synth.lastReq.Speed)
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

func TestFileMkdirMoveDeleteTools(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	root := filepath.Join(tmpDir, "ops")
	nested := filepath.Join(root, "a", "b")

	mkdirResult, err := r.Call("file_mkdir", map[string]any{
		"path": nested,
	})
	if err != nil {
		t.Fatalf("file_mkdir: %v", err)
	}
	if !strings.Contains(mkdirResult, "Created directory") {
		t.Fatalf("unexpected mkdir result: %q", mkdirResult)
	}

	src := filepath.Join(nested, "note.txt")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	dst := filepath.Join(root, "renamed.txt")

	moveResult, err := r.Call("file_move", map[string]any{
		"src": src,
		"dst": dst,
	})
	if err != nil {
		t.Fatalf("file_move: %v", err)
	}
	if !strings.Contains(moveResult, "Moved file") {
		t.Fatalf("unexpected move result: %q", moveResult)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected moved file at destination: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source to be gone, got %v", err)
	}

	deleteResult, err := r.Call("file_delete", map[string]any{
		"path": dst,
	})
	if err != nil {
		t.Fatalf("file_delete file: %v", err)
	}
	if !strings.Contains(deleteResult, "Deleted") {
		t.Fatalf("unexpected delete result: %q", deleteResult)
	}

	_, err = r.Call("file_delete", map[string]any{
		"path": root,
	})
	if err == nil {
		t.Fatal("expected non-recursive directory delete to fail")
	}

	_, err = r.Call("file_delete", map[string]any{
		"path":      root,
		"recursive": true,
	})
	if err != nil {
		t.Fatalf("file_delete recursive: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected root to be deleted, got %v", err)
	}

	missingResult, err := r.Call("file_delete", map[string]any{
		"path":       root,
		"missing_ok": true,
	})
	if err != nil {
		t.Fatalf("file_delete missing_ok: %v", err)
	}
	if !strings.Contains(missingResult, "already absent") {
		t.Fatalf("unexpected missing_ok result: %q", missingResult)
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

func TestFilePatchToolDiffMode(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch-diff.txt")
	if err := os.WriteFile(testFile, []byte("alpha\nbeta\ngamma\ndelta\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := r.Call("file_patch", map[string]any{
		"path": testFile,
		"diff": "@@\n alpha\n-beta\n+beta-2\n gamma\n@@\n gamma\n+gamma-half\n delta\n",
	})
	if err != nil {
		t.Fatalf("file_patch diff: %v", err)
	}
	if !strings.Contains(result, "2 hunks") {
		t.Fatalf("expected hunk count in result, got %q", result)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "alpha\nbeta-2\ngamma\ngamma-half\ndelta\n" {
		t.Fatalf("unexpected diff patched content: %q", got)
	}
}

func TestFilePatchToolUnifiedDiffHeaders(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch-unified.txt")
	if err := os.WriteFile(testFile, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := r.Call("file_patch", map[string]any{
		"path": testFile,
		"diff": "--- a/patch-unified.txt\n+++ b/patch-unified.txt\n@@ -1,3 +1,3 @@\n one\n-two\n+two-updated\n three\n",
	})
	if err != nil {
		t.Fatalf("file_patch unified diff: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if got := string(data); got != "one\ntwo-updated\nthree\n" {
		t.Fatalf("unexpected unified diff content: %q", got)
	}
}

func TestFilePatchToolDiffModeErrors(t *testing.T) {
	r := NewRegistry()
	RegisterBuiltinTools(r)

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "patch-diff-error.txt")
	if err := os.WriteFile(testFile, []byte("one\ntwo\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := r.Call("file_patch", map[string]any{
		"path": testFile,
		"diff": "two\n+three\n",
	})
	if err == nil || !strings.Contains(err.Error(), "must start with space") {
		t.Fatalf("expected invalid diff syntax error, got %v", err)
	}

	_, err = r.Call("file_patch", map[string]any{
		"path": testFile,
		"diff": "@@\n missing\n+three\n",
	})
	if err == nil || !strings.Contains(err.Error(), "did not match") {
		t.Fatalf("expected missing hunk match error, got %v", err)
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
	mkdirPerm, _ := r.CheckPermission("file_mkdir")
	if mkdirPerm != PermApprove {
		t.Errorf("file_mkdir should be approve, got %s", mkdirPerm)
	}
	movePerm, _ := r.CheckPermission("file_move")
	if movePerm != PermApprove {
		t.Errorf("file_move should be approve, got %s", movePerm)
	}
	deletePerm, _ := r.CheckPermission("file_delete")
	if deletePerm != PermApprove {
		t.Errorf("file_delete should be approve, got %s", deletePerm)
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
	calcPerm, _ := r.CheckPermission("calculate")
	if calcPerm != PermAuto {
		t.Errorf("calculate should be auto, got %s", calcPerm)
	}
	logTailPerm, _ := r.CheckPermission("log_tail")
	if logTailPerm != PermAuto {
		t.Errorf("log_tail should be auto, got %s", logTailPerm)
	}
	logGrepPerm, _ := r.CheckPermission("log_grep")
	if logGrepPerm != PermAuto {
		t.Errorf("log_grep should be auto, got %s", logGrepPerm)
	}
	httpPerm, _ := r.CheckPermission("http_request")
	if httpPerm != PermApprove {
		t.Errorf("http_request should be approve, got %s", httpPerm)
	}
	sqlPerm, _ := r.CheckPermission("sql_query")
	if sqlPerm != PermApprove {
		t.Errorf("sql_query should be approve, got %s", sqlPerm)
	}
	schemaPerm, _ := r.CheckPermission("db_schema")
	if schemaPerm != PermAuto {
		t.Errorf("db_schema should be auto, got %s", schemaPerm)
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
