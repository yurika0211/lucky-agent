package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyharness/internal/memory"
	"github.com/yurika0211/luckyharness/internal/provider"
	"github.com/yurika0211/luckyharness/internal/tool"
)

func TestMemoryGateAutoExecutesRequiredToolsBeforeDirectAnswer(t *testing.T) {
	mem, err := memory.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if err := mem.SaveWithOptions("我的女儿被诊断出有花粉过敏", "family", memory.TierLong, 0.95, memory.SaveOptions{
		Tags:       []string{"family", "health"},
		Links:      []string{"Daughter", "Pollen Allergy", "Outdoor Plan"},
		Aliases:    []string{"女儿", "花粉过敏"},
		StateKey:   "pollen",
		StateValue: "active",
		Confidence: 0.95,
	}); err != nil {
		t.Fatalf("SaveWithOptions() error = %v", err)
	}

	reg := tool.NewRegistry()
	var callsMu sync.Mutex
	var timeCalls int
	reg.Register(&tool.Tool{
		Name:        "current_time",
		Description: "test current time",
		Permission:  tool.PermAuto,
		Parameters:  map[string]tool.Param{},
		Handler: func(args map[string]any) (string, error) {
			callsMu.Lock()
			timeCalls++
			callsMu.Unlock()
			return "Current time: 2026-05-19 14:00:00 (Asia/Shanghai)", nil
		},
		ParallelSafe: true,
	})
	var searchQueries []string
	reg.Register(&tool.Tool{
		Name:        "web_search",
		Description: "test web search",
		Permission:  tool.PermAuto,
		Parameters: map[string]tool.Param{
			"query": {Type: "string", Required: true},
		},
		Handler: func(args map[string]any) (string, error) {
			query, _ := args["query"].(string)
			callsMu.Lock()
			searchQueries = append(searchQueries, query)
			callsMu.Unlock()
			return fmt.Sprintf("Results for: %s\n\n1. pollen forecast high\n2. wind mild\n3. AQI moderate", query), nil
		},
		ParallelSafe: true,
	})

	prov := &directThenFinalProvider{}
	a := &Agent{
		provider: prov,
		memory:   mem,
		tools:    reg,
		gateway:  tool.NewGateway(reg),
	}

	result, err := a.RunLoopWithSessionInput(context.Background(), nil, TextUserTurnInput("今天下午适合和女儿出门吗"), LoopConfig{
		MaxIterations:          3,
		Timeout:                time.Second,
		AutoApprove:            true,
		RepeatToolCallLimit:    3,
		ToolOnlyIterationLimit: 3,
		DuplicateFetchLimit:    1,
	})
	if err != nil {
		t.Fatalf("RunLoopWithSessionInput() error = %v", err)
	}
	if !strings.Contains(result.Response, "checked answer") {
		t.Fatalf("expected checked final answer, got %q", result.Response)
	}
	if !strings.Contains(result.Response, naturalCitationHeader) {
		t.Fatalf("expected final answer to include natural citations, got %q", result.Response)
	}
	if !strings.Contains(result.Response, "[1] Current time tool") || !strings.Contains(result.Response, "[2] Web search.") {
		t.Fatalf("expected citations for current_time and web_search, got %q", result.Response)
	}
	if prov.callCount != 2 {
		t.Fatalf("expected provider to be called twice, got %d", prov.callCount)
	}
	callsMu.Lock()
	gotTimeCalls := timeCalls
	gotSearchQueries := append([]string(nil), searchQueries...)
	callsMu.Unlock()
	if gotTimeCalls != 1 {
		t.Fatalf("expected current_time to execute once, got %d", gotTimeCalls)
	}
	if len(gotSearchQueries) == 0 {
		t.Fatal("expected web_search to execute")
	}
	if !toolLogContains(result.ToolCalls, "current_time") || !toolLogContains(result.ToolCalls, "web_search") {
		t.Fatalf("expected result tool log to include current_time and web_search, got %#v", result.ToolCalls)
	}
}

type directThenFinalProvider struct {
	callCount int
}

func (p *directThenFinalProvider) Name() string { return "direct-then-final" }

func (p *directThenFinalProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return nil, fmt.Errorf("unexpected Chat call")
}

func (p *directThenFinalProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected ChatStream call")
}

func (p *directThenFinalProvider) Validate() error { return nil }

func (p *directThenFinalProvider) ChatWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (*provider.Response, error) {
	p.callCount++
	if p.callCount == 1 {
		return &provider.Response{Content: "不用查，直接出门就行。"}, nil
	}
	if !messagesContainTool(messages, "current_time") || !messagesContainTool(messages, "web_search") {
		return &provider.Response{Content: "missing gate evidence"}, nil
	}
	return &provider.Response{Content: "checked answer"}, nil
}

func (p *directThenFinalProvider) ChatStreamWithOptions(ctx context.Context, messages []provider.Message, opts provider.CallOptions) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("unexpected ChatStreamWithOptions call")
}

func messagesContainTool(messages []provider.Message, name string) bool {
	for _, msg := range messages {
		if msg.Role == "tool" && strings.TrimSpace(msg.Name) == name {
			return true
		}
	}
	return false
}

func toolLogContains(logs []toolCallLog, name string) bool {
	for _, log := range logs {
		if log.Name == name {
			return true
		}
	}
	return false
}
