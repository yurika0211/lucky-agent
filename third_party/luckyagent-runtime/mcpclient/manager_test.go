package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func fixtureServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	s.AddTool(&mcp.Tool{Name: "echo", Description: "nested schema", InputSchema: map[string]any{
		"type": "object", "$defs": map[string]any{"input": map[string]any{"type": "string", "enum": []any{"ok"}}},
		"properties": map[string]any{"value": map[string]any{"$ref": "#/$defs/input"}}, "required": []any{"value"},
	}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}, &mcp.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}, StructuredContent: map[string]any{"nested": map[string]any{"value": "ok"}}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "wait", InputSchema: map[string]any{"type": "object"}}, func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	return s
}

func startHTTP(t *testing.T, s *mcp.Server, stateless bool, wrap func(http.Handler) http.Handler) string {
	t.Helper()
	var handler http.Handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: stateless, JSONResponse: true})
	if wrap != nil {
		handler = wrap(handler)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

func TestHTTPManagerLifecycle(t *testing.T) {
	for _, stateless := range []bool{false, true} {
		t.Run(map[bool]string{false: "stateful", true: "stateless"}[stateless], func(t *testing.T) {
			s := fixtureServer()
			url := startHTTP(t, s, stateless, nil)
			mgr := NewManager(Config{Servers: map[string]ServerConfig{"test": {URL: url}}}, nil)
			defer mgr.Close()
			var published []ToolDescriptor
			if err := mgr.SetPublisher(func(tools []ToolDescriptor) error { published = tools; return nil }); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if err := mgr.Start(ctx); err != nil {
				t.Fatal(err)
			}
			cancel() // startup context must not own session lifetime
			if len(published) != 2 || mgr.Status()[0].State != "connected" {
				t.Fatalf("catalog/status: %v %v", published, mgr.Status())
			}
			ref := published[0].Ref
			if !strings.Contains(string(published[0].InputSchema), "$defs") {
				t.Fatalf("schema lost: %s", published[0].InputSchema)
			}
			result, err := mgr.CallTool(context.Background(), ref, map[string]any{"value": "ok"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Content) != 2 || !strings.Contains(string(result.StructuredContent), "nested") {
				t.Fatalf("result lost: %s", result.Raw)
			}
			// A failed reconnect retains the old usable session and tool generation.
			if err := mgr.Connect(context.Background(), "test", ServerConfig{URL: "http://127.0.0.1:1"}); err == nil {
				t.Fatal("failed connection reported success")
			}
			if _, err := mgr.CallTool(context.Background(), ref, map[string]any{"value": "ok"}); err != nil {
				t.Fatalf("last-good session lost: %v", err)
			}
			s.RemoveTools("echo")
			if err := mgr.Refresh(context.Background(), "test"); err != nil {
				t.Fatal(err)
			}
			if len(published) != 1 || published[0].Ref.Name != "wait" {
				t.Fatalf("stale tools remain: %v", published)
			}
			if _, err := mgr.CallTool(context.Background(), ref, nil); !errors.Is(err, ErrStaleTool) {
				t.Fatalf("stale ref accepted: %v", err)
			}
			if err := mgr.Disconnect(context.Background(), "test"); err != nil {
				t.Fatal(err)
			}
			if len(published) != 0 || mgr.Status()[0].State != "disconnected" {
				t.Fatal("disconnect did not clear catalog")
			}
			if err := mgr.Close(); err != nil {
				t.Fatal(err)
			}
			if err := mgr.ConnectConfigured(context.Background(), "test"); !errors.Is(err, ErrClosed) {
				t.Fatalf("closed manager accepted connection: %v", err)
			}
		})
	}
}

func TestCredentialsAndCancellation(t *testing.T) {
	var token atomic.Value
	token.Store("first-secret")
	var denied atomic.Int32
	url := startHTTP(t, fixtureServer(), false, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token.Load().(string) {
				denied.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	mgr := NewManager(Config{}, func(ctx context.Context, ref string, use func([]byte) error) error {
		if ref != "vault-id" {
			t.Errorf("unexpected credential reference: %s", ref)
		}
		return use([]byte(token.Load().(string)))
	})
	defer mgr.Close()
	if err := mgr.Connect(context.Background(), "auth", ServerConfig{URL: url, CredentialRef: "vault-id"}); err != nil {
		t.Fatal(err)
	}
	token.Store("rotated-secret")
	if _, err := mgr.CallTool(context.Background(), mgr.Tools()[0].Ref, map[string]any{"value": "ok"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := mgr.CallTool(ctx, mgr.Tools()[1].Ref, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := mgr.CallTool(context.Background(), mgr.Tools()[0].Ref, map[string]any{"value": "ok"}); err != nil {
		t.Fatalf("cancellation killed session: %v", err)
	}
	if denied.Load() != 0 {
		t.Fatal("credential rotation was not applied per request")
	}
	encoded, _ := json.Marshal(mgr.Status())
	if strings.Contains(string(encoded), "secret") {
		t.Fatal("status leaked credential")
	}
}

func TestPublisherRollbackAndStartupFailures(t *testing.T) {
	url := startHTTP(t, fixtureServer(), true, nil)
	mgr := NewManager(Config{Servers: map[string]ServerConfig{"ok": {URL: url}, "disabled": {URL: url, Disabled: true}, "bad": {URL: "http://127.0.0.1:1"}}}, nil)
	defer mgr.Close()
	if err := mgr.Start(context.Background()); err == nil {
		t.Fatal("failed startup reported success")
	}
	if len(mgr.Tools()) != 2 || len(mgr.Status()) != 3 {
		t.Fatalf("partial startup lost: %v", mgr.Status())
	}
	old := mgr.Tools()[0].Ref
	reject := false
	if err := mgr.SetPublisher(func([]ToolDescriptor) error {
		if reject {
			return errors.New("catalog rejected")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reject = true
	if err := mgr.Refresh(context.Background(), "ok"); err == nil {
		t.Fatal("publication failure lost")
	}
	if mgr.Tools()[0].Ref != old {
		t.Fatal("failed publication changed generation")
	}
	reject = false
	if _, err := mgr.CallTool(context.Background(), old, map[string]any{"value": "ok"}); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectDoesNotForwardCredential(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	mgr := NewManager(Config{}, nil)
	defer mgr.Close()
	if err := mgr.Connect(context.Background(), "redirect", ServerConfig{URL: redirect.URL, APIKey: "transient-secret"}); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked.Load() {
		t.Fatal("bearer credential followed redirect")
	}
}

func TestStdioHelper(t *testing.T) {
	if os.Getenv("LUCKY_MCP_TEST_HELPER") != "1" {
		return
	}
	err := fixtureServer().Run(context.Background(), &mcp.StdioTransport{})
	if marker := os.Getenv("LUCKY_MCP_TEST_CLOSED"); marker != "" {
		_ = os.WriteFile(marker, []byte("closed"), 0600)
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestStdioLifecycle(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "closed")
	mgr := NewManager(Config{}, nil)
	defer mgr.Close()
	ctx, cancel := context.WithCancel(context.Background())
	err := mgr.Connect(ctx, "local", ServerConfig{Command: os.Args[0], Args: []string{"-test.run=^TestStdioHelper$"}, Env: map[string]string{"LUCKY_MCP_TEST_HELPER": "1", "LUCKY_MCP_TEST_CLOSED": marker, "GORACE": "atexit_sleep_ms=0"}})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := mgr.CallTool(context.Background(), mgr.Tools()[0].Ref, map[string]any{"value": "ok"}); err != nil {
		t.Fatalf("startup cancellation killed subprocess: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("stdio subprocess was not shut down cleanly: %v", err)
	}
	if len(mgr.Tools()) != 0 {
		t.Fatal("close retained catalog")
	}
}
