package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/contextx"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

type requestCaptureProvider struct {
	messages []provider.Message
	opts     provider.CallOptions
	chat     func([]provider.Message, provider.CallOptions) (*provider.Response, error)
}

func (p *requestCaptureProvider) Name() string    { return "request-capture" }
func (p *requestCaptureProvider) Validate() error { return nil }
func (p *requestCaptureProvider) Chat(_ context.Context, messages []provider.Message) (*provider.Response, error) {
	p.messages, p.opts = messages, provider.CallOptions{}
	if p.chat != nil {
		return p.chat(messages, p.opts)
	}
	return &provider.Response{Content: "ok"}, nil
}
func (p *requestCaptureProvider) ChatWithOptions(_ context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	p.messages, p.opts = messages, opts
	if p.chat != nil {
		return p.chat(messages, opts)
	}
	return &provider.Response{Content: "ok"}, nil
}
func (p *requestCaptureProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return p.ChatStreamWithOptions(ctx, messages, provider.CallOptions{})
}
func (p *requestCaptureProvider) ChatStreamWithOptions(_ context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	p.messages, p.opts = messages, opts
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "ok", Done: true}
	close(ch)
	return ch, nil
}

func requestBudgetAgent(inputLimit int) *Agent {
	return &Agent{
		contextWin: contextx.NewContextWindow(contextx.WindowConfig{
			MaxTokens:      inputLimit + inputLimit/3,
			ReservedTokens: inputLimit / 3,
		}),
		contextEst: contextx.NewTokenEstimator(inputLimit),
	}
}

func TestEnsureRequestFitsCountsTextAlongsideStructuredParts(t *testing.T) {
	a := requestBudgetAgent(6000)
	messages := []provider.Message{{Role: "assistant", Content: strings.Repeat("x", 26000)}, {
		Role: "user",
		ContentParts: []provider.ContentPart{{
			Type: "text",
			Text: "attached image caption",
		}},
	}}

	if err := a.ensureRequestFits(messages, provider.CallOptions{}); err == nil {
		t.Fatal("expected oversized text to be rejected when a structured part is present")
	}
}

func TestEnsureRequestFitsEmptyPartsCannotHidePlainContent(t *testing.T) {
	a := requestBudgetAgent(6000)
	for _, part := range []provider.ContentPart{{Type: "text", Text: " "}, {Type: "text", Text: "short"}} {
		if err := a.ensureRequestFits([]provider.Message{{Role: "user", Content: strings.Repeat("plain content ", 5000), ContentParts: []provider.ContentPart{part}}}, provider.CallOptions{}); err == nil {
			t.Fatal("structured part bypassed plain-text lower bound")
		}
	}
}

func TestEnsureRequestFitsAppliesDedicatedToolSchemaBudget(t *testing.T) {
	a := requestBudgetAgent(6000)
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "oversized_tool",
			"description": strings.Repeat("schema ", 5000),
			"parameters":  map[string]any{"type": "object"},
		},
	}}

	err := a.ensureRequestFits([]provider.Message{{Role: "user", Content: "run the tool"}}, provider.CallOptions{Tools: tools})
	if err == nil || !strings.Contains(err.Error(), "tool schemas") {
		t.Fatalf("expected tool schema budget error, got %v", err)
	}
}

func TestEnsureRequestFitsCountsStructuredTextImagesAndSchemas(t *testing.T) {
	a := requestBudgetAgent(6000)
	messages := []provider.Message{{
		Role:    "user",
		Content: "describe this",
		ContentParts: []provider.ContentPart{
			{Type: "text", Text: "caption text"},
			{Type: "image", Image: &provider.ImagePart{Detail: "low"}},
		},
	}}
	tools := []map[string]any{{
		"type": "function",
		"function": map[string]any{
			"name":        "describe_image",
			"description": "Describe the supplied image.",
			"parameters":  map[string]any{"type": "object"},
		},
	}}

	if err := a.ensureRequestFits(messages, provider.CallOptions{Tools: tools}); err != nil {
		t.Fatalf("expected a small multimodal request to fit, got %v", err)
	}
}

func TestModelRequestBudgetFitsHistoryAndPreservesStructuredPayload(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "sync", true: "stream"}[stream], func(t *testing.T) {
			a := requestBudgetAgent(6000)
			p := &requestCaptureProvider{}
			messages := []provider.Message{
				{Role: "system", Content: "project rules"},
				{Role: "assistant", Content: strings.Repeat("old history ", 5000)},
				{Role: "user", Content: "caption", BlobHash: "payload-marker", ContentParts: []provider.ContentPart{{Type: "text", Text: "CURRENT_REQUEST"}, {Type: "image", Image: &provider.ImagePart{URL: "https://example.test/image.png", Detail: "low"}}}},
			}
			opts := provider.CallOptions{Tools: []map[string]any{{"type": "function", "function": map[string]any{"name": "read", "description": strings.Repeat("schema ", 1500)}}}}
			snapshot := providerSnapshot{provider: p}
			var err error
			if stream {
				_, err = a.streamLoopIteration(context.Background(), messages, opts, false, snapshot)
			} else {
				_, err = a.chatLoopIteration(context.Background(), messages, opts, false, snapshot)
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := estimateProviderRequest(a.contextEst, p.messages, p.opts.Tools); n > 6000 {
				t.Fatalf("provider received %d tokens", n)
			}
			last := p.messages[len(p.messages)-1]
			if last.BlobHash != "payload-marker" || len(last.ContentParts) != 2 || last.ContentParts[0].Text != "CURRENT_REQUEST" {
				t.Fatal("structured request or metadata lost")
			}
		})
	}
}

func TestModelRequestBudgetRejectsOversizedSchemasBeforeDispatch(t *testing.T) {
	a := requestBudgetAgent(6000)
	p := &requestCaptureProvider{}
	opts := provider.CallOptions{Tools: []map[string]any{{"description": strings.Repeat("schema ", 6000)}}}
	for _, stream := range []bool{false, true} {
		var err error
		if stream {
			_, err = a.streamLoopIteration(context.Background(), []provider.Message{{Role: "user", Content: "hi"}}, opts, false, providerSnapshot{provider: p})
		} else {
			_, err = a.chatLoopIteration(context.Background(), []provider.Message{{Role: "user", Content: "hi"}}, opts, false, providerSnapshot{provider: p})
		}
		if err == nil || p.messages != nil {
			t.Fatalf("oversized schema dispatched: %v", err)
		}
	}
}

func TestModelRequestBudgetPinsStructuredOnlyCurrentInput(t *testing.T) {
	a := requestBudgetAgent(6000)
	p := &requestCaptureProvider{}
	messages := []provider.Message{{Role: "assistant", Content: strings.Repeat("old ", 10000)}, {Role: "user", ContentParts: []provider.ContentPart{{Type: "text", Text: strings.Repeat("current ", 10000)}}}}
	_, err := a.chatLoopIteration(context.Background(), messages, provider.CallOptions{}, false, providerSnapshot{provider: p})
	if err == nil || p.messages != nil {
		t.Fatal("oversized structured-only current request was dropped or dispatched")
	}
}

func TestModelRequestBudgetSnapshotIsStableForTurn(t *testing.T) {
	a := requestBudgetAgent(6000)
	p := &requestCaptureProvider{}
	a.provider = p
	snapshot := a.providerSnapshotForTurn("task")
	a.contextWin = requestBudgetAgent(16000).contextWin
	messages := []provider.Message{{Role: "user", Content: strings.Repeat("current ", 4000)}}
	if _, err := a.chatLoopIteration(context.Background(), messages, provider.CallOptions{}, false, snapshot); err == nil {
		t.Fatal("running turn switched to a new request budget")
	}
	if _, err := a.chatLoopIteration(context.Background(), messages, provider.CallOptions{}, false, a.providerSnapshotForTurn("next")); err != nil {
		t.Fatal(err)
	}
}

func TestModelRequestBudgetSelectsSchemasAndRetainsTurnRequiredTools(t *testing.T) {
	a := requestBudgetAgent(6000)
	p := &requestCaptureProvider{}
	opts := provider.CallOptions{ToolChoice: "auto"}
	for i := 0; i < 100; i++ {
		opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{"name": fmt.Sprintf("optional_%d", i), "description": strings.Repeat("optional action ", 20)}})
	}
	opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{"name": "required_tool", "description": "Required task operation"}})
	snapshot := providerSnapshot{provider: p, requiredTools: []string{"required_tool"}}
	_, err := a.chatLoopIteration(context.Background(), []provider.Message{{Role: "user", Content: "Continue after reviewing progress"}}, opts, false, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.opts.Tools) >= len(opts.Tools) {
		t.Fatal("catalog was not bounded")
	}
	found := false
	for _, schema := range p.opts.Tools {
		found = found || functionToolNameFromSchema(schema) == "required_tool"
	}
	if !found {
		t.Fatal("original required tool was omitted after later user guidance")
	}
	if n := estimateProviderRequest(a.contextEst, p.messages, p.opts.Tools); n > 6000 {
		t.Fatalf("request exceeds budget: %d", n)
	}
}

func toolNamesIn(schemas []map[string]any) map[string]bool {
	names := make(map[string]bool, len(schemas))
	for _, schema := range schemas {
		names[functionToolNameFromSchema(schema)] = true
	}
	return names
}

func TestSelectRequestToolsKeepsEnabledToolsAheadOfPreferredList(t *testing.T) {
	a := requestBudgetAgent(6000)
	messages := []provider.Message{{Role: "user", Content: "继续处理当前任务"}}
	priorities := a.requestPriorities(messages)
	opts := provider.CallOptions{ToolChoice: "auto"}
	// Preferred names are registered first and are large. Enabled tools that
	// the user did not name must still be admitted while any of them fit.
	for _, name := range []string{"file_read", "file_list", "terminal", "web_search", "web_fetch", "ask_user", "context_history_read", "skill_read"} {
		opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
			"name": name, "description": strings.Repeat("core catalog entry ", 80),
		}})
	}
	opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
		"name": "subagent", "description": strings.Repeat("delegate schema ", 30),
	}})
	for i := 0; i < 40; i++ {
		opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
			"name": fmt.Sprintf("mcp_tool_%02d", i), "description": strings.Repeat("remote catalog ", 40),
		}})
	}

	got, err := a.selectRequestTools(a.requestBudget(), messages, priorities, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) >= len(opts.Tools) {
		t.Fatal("catalog was not bounded")
	}
	names := toolNamesIn(got.Tools)
	if !names["subagent"] {
		t.Fatal("enabled subagent schema was dropped before larger preferred schemas")
	}
	if !names["mcp_tool_00"] {
		t.Fatal("enabled non-preferred schema was dropped before larger preferred schemas")
	}
	if n := estimateProviderRequest(a.contextEst, messages, got.Tools); n > 6000 {
		t.Fatalf("request exceeds budget: %d", n)
	}
}

func TestSelectRequestToolsStillPrefersNamedToolWhenBudgetIsTight(t *testing.T) {
	a := requestBudgetAgent(6000)
	messages := []provider.Message{{Role: "user", Content: "请使用 subagent 改这段代码"}}
	priorities := a.requestPriorities(messages)
	opts := provider.CallOptions{}
	opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
		"name": "subagent", "description": strings.Repeat("delegate schema ", 400),
	}})
	for i := 0; i < 30; i++ {
		opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
			"name": fmt.Sprintf("filler_%02d", i), "description": strings.Repeat("unrelated filler ", 80),
		}})
	}

	a.tools = tool.NewRegistry()
	a.tools.Register(&tool.Tool{Name: "subagent", Description: "delegate", Enabled: true})
	got, err := a.selectRequestTools(a.requestBudget(), messages, priorities, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !toolNamesIn(got.Tools)["subagent"] {
		t.Fatal("named subagent schema was omitted")
	}
}

func TestCompactionCannotBypassRequestBudget(t *testing.T) {
	a := requestBudgetAgent(50)
	p := &requestCaptureProvider{}
	a.provider = p
	_, _, err := a.generateMapReduceCompactSummary(context.Background(), []provider.Message{{Role: "user", Content: strings.Repeat("past conversation ", 100)}}, a.baseProviderSnapshot(), CompactSessionOptions{})
	if err == nil || !strings.Contains(err.Error(), "context input budget") || p.messages != nil {
		t.Fatalf("compaction bypassed input budget: %v", err)
	}
}

func TestSelectRequestToolsKeepsRelevantLargeSchemaAheadOfUnrelatedSmallOnes(t *testing.T) {
	a := requestBudgetAgent(6000)
	messages := []provider.Message{{Role: "user", Content: "Analyze spectroscopy measurements"}}
	opts := provider.CallOptions{}
	for i := 0; i < 100; i++ {
		opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
			"name": fmt.Sprintf("unrelated_%d", i), "description": "Generic helper",
		}})
	}
	opts.Tools = append(opts.Tools, map[string]any{"type": "function", "function": map[string]any{
		"name": "spectral_analyzer", "description": strings.Repeat("Analyze spectroscopy measurements. ", 100),
	}})
	got, err := a.selectRequestTools(a.requestBudget(), messages, a.requestPriorities(messages), opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !toolNamesIn(got.Tools)["spectral_analyzer"] {
		t.Fatal("cheap unrelated schemas crowded out the relevant enabled tool")
	}
	if cost := estimateToolSchemas(a.contextEst, got.Tools); cost > 2000 {
		t.Fatalf("schema allocation exceeded: %d", cost)
	}
}

func TestSelectRequestToolsBudgetFailuresAndRequiredBorrowing(t *testing.T) {
	schema := func(name string, repeats int) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{
			"name": name, "description": strings.Repeat("schema details ", repeats),
		}}
	}
	for _, tt := range []struct {
		name, text, wantError string
		tools                 []map[string]any
		required              []string
	}{
		{"pinned input", strings.Repeat("pinned input ", 5000), "pinned instructions exceed", []map[string]any{schema("small", 1)}, nil},
		{"required overflow", "continue", "required tool schemas need", []map[string]any{schema("huge", 5000), schema("small", 1)}, []string{"huge"}},
		{"no optional fits", "continue", "no tool schema fits", []map[string]any{schema("one", 5000), schema("two", 5000)}, nil},
		{"required borrows", "continue", "", []map[string]any{schema("required", 700), schema("optional", 1000)}, []string{"required"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := requestBudgetAgent(6000)
			messages := []provider.Message{{Role: "user", Content: tt.text}}
			got, err := a.selectRequestTools(a.requestBudget(), messages, a.requestPriorities(messages), provider.CallOptions{Tools: tt.tools}, tt.required)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want %q", err, tt.wantError)
				}
				return
			}
			if err != nil || !toolNamesIn(got.Tools)["required"] {
				t.Fatalf("required schema lost: opts=%v err=%v", got, err)
			}
			cost := estimateToolSchemas(a.contextEst, got.Tools)
			if cost <= 2000 || estimateProviderRequest(a.contextEst, messages, got.Tools) > 6000 {
				t.Fatalf("fixture did not exercise safe borrowing: schema tokens=%d", cost)
			}
		})
	}
}
