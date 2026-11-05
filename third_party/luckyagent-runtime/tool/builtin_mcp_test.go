package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yurika0211/luckyagent-runtime/mcpclient"
)

func TestMCPAdapterIntegration(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "adapter-test", Version: "1"}, nil)
	schema := map[string]any{"type": "object", "additionalProperties": false, "$defs": map[string]any{"value": map[string]any{"type": "string"}}, "properties": map[string]any{"input": map[string]any{"$ref": "#/$defs/value"}}}
	s.AddTool(&mcp.Tool{Name: "lookup", InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "result"}, &mcp.ImageContent{Data: []byte("image"), MIMEType: "image/png"}}, StructuredContent: map[string]any{"count": 1}}, nil
	})
	s.AddTool(&mcp.Tool{Name: "fail", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "failed"}}}, nil
	})
	h := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer h.Close()
	r := NewRegistry()
	r.Register(&Tool{Name: "keep", Category: CatBuiltin, Handler: func(map[string]any) (string, error) { return "builtin", nil }})
	mgr := mcpclient.NewManager(mcpclient.Config{}, nil)
	defer mgr.Close()
	adapter := NewMCPAdapter(r, mgr)
	if err := mgr.SetPublisher(adapter.Sync); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Connect(context.Background(), "remote", mcpclient.ServerConfig{URL: h.URL}); err != nil {
		t.Fatal(err)
	}
	name := MCPToolName("remote", "lookup")
	registered, ok := r.Get(name)
	if !ok {
		t.Fatal("tool not published")
	}
	params := registered.ToOpenAIFormat()["function"].(map[string]any)["parameters"]
	encoded, _ := json.Marshal(params)
	if !strings.Contains(string(encoded), "$defs") || !strings.Contains(string(encoded), `"additionalProperties":false`) {
		t.Fatalf("root schema lost: %s", encoded)
	}
	result, err := r.CallDetailedWithContext(name, map[string]any{"input": "test"}, ExecutionContext{Context: context.Background()})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "result\n{\"count\":1}\n[MCP returned 1 image(s)]" || len(result.Observations) != 1 || result.Observations[0].Kind != "mcp_image" || result.Metadata["mcp_result"] == nil {
		t.Fatalf("result lost: %+v", result)
	}
	if _, err := r.Call(name, nil); err != nil {
		t.Fatalf("legacy registry dispatch must call context handler: %v", err)
	}
	failure, err := r.CallDetailed(MCPToolName("remote", "fail"), nil)
	if err == nil || failure.Metadata["mcp_is_error"] != true {
		t.Fatalf("isError reported success: %+v %v", failure, err)
	}
	if err := r.SetPermissionOverride(name, PermDeny); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Refresh(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Call(name, nil); err == nil {
		t.Fatal("refresh discarded permission override")
	}
	if err := r.Disable(name); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Refresh(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	if updated, _ := r.Get(name); updated.Enabled {
		t.Fatal("refresh enabled a disabled tool")
	}
	if err := r.Enable(name); err != nil {
		t.Fatal(err)
	}
	if err := r.SetPermissionOverride(name, PermAuto); err != nil {
		t.Fatal(err)
	}
	// Exercise publication racing real dispatch; stale snapshots may fail, but
	// they must never call the wrong tool or race registry permission maps.
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for range 20 {
			_, _ = r.Call(name, nil)
		}
	}()
	for range 5 {
		if err := mgr.Refresh(context.Background(), "remote"); err != nil {
			t.Fatal(err)
		}
	}
	group.Wait()
	if err := mgr.Disconnect(context.Background(), "remote"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get(name); ok {
		t.Fatal("disconnect retained stale tool")
	}
	if out, err := r.Call("keep", nil); err != nil || out != "builtin" {
		t.Fatal("catalog replacement removed builtin")
	}
}

func TestMCPNamesAndAtomicPublication(t *testing.T) {
	a, b := MCPToolName("a-b", "x.y"), MCPToolName("a-b", "x/y")
	if a == b || len(a) > 64 || len(MCPToolName(strings.Repeat("a", 64), strings.Repeat("b", 100))) > 64 {
		t.Fatal("MCP names collide or exceed provider limit")
	}
	r := NewRegistry()
	r.Register(&Tool{Name: a, Category: CatBuiltin})
	adapter := NewMCPAdapter(r, nil)
	err := adapter.Sync([]mcpclient.ToolDescriptor{{Ref: mcpclient.ToolRef{Server: "a-b", Name: "x.y"}, InputSchema: json.RawMessage(`{"type":"object"}`)}})
	if err == nil {
		t.Fatal("catalog collision accepted")
	}
	if len(r.List()) != 1 {
		t.Fatal("failed publication changed registry")
	}
}
