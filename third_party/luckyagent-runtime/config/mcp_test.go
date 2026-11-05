package config

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/mcpclient"
)

func TestMCPConfigRoundTripAndIsolation(t *testing.T) {
	m, _ := NewManagerWithDir(t.TempDir())
	value := `{"local":{"transport":"stdio","command":"mcp-fixture","args":["serve"],"env":{"MODE":"test"}},"remote":{"url":"https://example.com/mcp","credential_ref":"mcp-token","timeout_seconds":15}}`
	if err := m.Set("mcp.servers", value); err != nil {
		t.Fatal(err)
	}
	copy := m.Get()
	local := copy.MCP.Servers["local"]
	local.Args[0] = "changed"
	local.Env["MODE"] = "changed"
	copy.MCP.Servers["local"] = local
	if got := m.Get().MCP.Servers["local"]; got.Args[0] != "serve" || got.Env["MODE"] != "test" {
		t.Fatal("MCP config snapshot aliases maps/slices")
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	if got := m.Get().MCP.Servers["remote"].CredentialRef; got != "mcp-token" {
		t.Fatalf("reference not persisted: %s", got)
	}
	before := m.Get()
	if err := m.Set("mcp.servers", `{"broken":{"transport":"stdio","url":"https://example.com"}}`); err == nil {
		t.Fatal("invalid config accepted")
	}
	if len(m.Get().MCP.Servers) != 2 {
		t.Fatal("invalid set replaced config")
	}
	next := m.Get()
	next.MCP.Servers["remote"] = mcpclient.ServerConfig{URL: "https://other.example/mcp"}
	_, restart := ReloadClassification(before, next)
	if !slices.Contains(restart, "runtime_services") {
		t.Fatalf("MCP config misclassified: %v", restart)
	}
	if err := os.WriteFile(m.ConfigFile(), []byte(`{"mcp":{"servers":{"broken":{"url":"file:///tmp/mcp"}}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(); err == nil {
		t.Fatal("invalid transport loaded")
	}
	if len(m.Get().MCP.Servers) != 2 {
		t.Fatal("invalid reload changed active config")
	}
}

func TestMCPTransientTokenNeverSerialized(t *testing.T) {
	c := DefaultConfig()
	c.MCP = mcpclient.Config{Servers: map[string]mcpclient.ServerConfig{"remote": {URL: "https://example.com/mcp", APIKey: "transient-secret"}}}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "transient-secret") {
		t.Fatal("transient token persisted")
	}
}
