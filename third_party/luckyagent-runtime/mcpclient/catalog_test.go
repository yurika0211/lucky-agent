package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDiscoveryPaginationAndFailedRefresh(t *testing.T) {
	s := fixtureServer()
	var repeat atomic.Bool
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			params := req.GetParams().(*mcp.ListToolsParams)
			name, cursor := "first", "page2"
			if params.Cursor != "" {
				name = "second"
				cursor = ""
			}
			if repeat.Load() {
				cursor = "page2"
			}
			return &mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: name, InputSchema: map[string]any{"type": "object"}}}, NextCursor: cursor}, nil
		}
	})
	url := startHTTP(t, s, true, nil)
	mgr := NewManager(Config{}, nil)
	defer mgr.Close()
	if err := mgr.Connect(context.Background(), "pages", ServerConfig{URL: url}); err != nil {
		t.Fatal(err)
	}
	tools := mgr.Tools()
	if len(tools) != 2 || tools[0].Ref.Name != "first" || tools[1].Ref.Name != "second" {
		t.Fatalf("pagination lost: %v", tools)
	}
	// Returned snapshots must not let an unrelated consumer mutate the catalog.
	tools[0].InputSchema[0] = 'x'
	if !json.Valid(mgr.Tools()[0].InputSchema) {
		t.Fatal("catalog snapshot aliases internal schema")
	}
	oldRef := mgr.Tools()[0].Ref
	repeat.Store(true)
	if err := mgr.Refresh(context.Background(), "pages"); err == nil {
		t.Fatal("repeating cursor accepted")
	}
	if mgr.Tools()[0].Ref != oldRef {
		t.Fatal("failed discovery replaced last-good catalog")
	}
}

func TestCommandsReturnConnectionErrors(t *testing.T) {
	mgr := NewManager(Config{}, nil)
	defer mgr.Close()
	if message, err := RunCommand(context.Background(), mgr, "broken http://127.0.0.1:1 transient-secret"); err == nil || message != "" {
		t.Fatalf("failed connection reported success: %q %v", message, err)
	}
	if _, err := RunCommand(context.Background(), mgr, "connect missing"); err == nil {
		t.Fatal("unknown config reported success")
	}
	if _, err := RunCommand(context.Background(), nil, "status"); err == nil {
		t.Fatal("nil admin reported success")
	}
	if _, err := RunCommand(context.Background(), mgr, "status"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RunCommand(ctx, mgr, "broken http://127.0.0.1:1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("connect cancellation lost: %v", err)
	}
}

func TestServerWithoutToolsCapability(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "resources-only", Version: "1"}, nil)
	url := startHTTP(t, s, true, nil)
	mgr := NewManager(Config{}, nil)
	defer mgr.Close()
	if err := mgr.Connect(context.Background(), "empty", ServerConfig{URL: url}); err != nil {
		t.Fatal(err)
	}
	if len(mgr.Tools()) != 0 || mgr.Status()[0].State != "connected" {
		t.Fatalf("empty server misreported: %v", mgr.Status())
	}
}

func TestUnexpectedStdioExitRevokesCatalog(t *testing.T) {
	mgr := NewManager(Config{}, nil)
	defer mgr.Close()
	err := mgr.Connect(context.Background(), "exit", ServerConfig{Command: os.Args[0], Args: []string{"-test.run=^TestStdioExitHelper$"}, Env: map[string]string{"LUCKY_MCP_TEST_EXIT": "1", "GORACE": "atexit_sleep_ms=0"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range mgr.Tools() {
		if tool.Ref.Name == "exit" {
			_, _ = mgr.CallTool(context.Background(), tool.Ref, nil)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(mgr.Tools()) == 0 && mgr.Status()[0].State == "failed" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("unexpected connection exit retained catalog: %v", mgr.Status())
}

func TestStdioExitHelper(t *testing.T) {
	if os.Getenv("LUCKY_MCP_TEST_EXIT") != "1" {
		return
	}
	s := fixtureServer()
	s.AddTool(&mcp.Tool{Name: "exit", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { os.Exit(0); return nil, nil })
	_ = s.Run(context.Background(), &mcp.StdioTransport{})
	os.Exit(0)
}
