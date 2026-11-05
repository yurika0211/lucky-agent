package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func dial(ctx context.Context, cfg ServerConfig, resolver CredentialResolver) (*mcp.ClientSession, error) {
	var transport mcp.Transport
	if cfg.Kind() == "stdio" {
		// CommandTransport owns process shutdown; a connection-attempt timeout
		// must not become the lifetime of a successfully connected process.
		cmd := exec.Command(cfg.Command, cfg.Args...)
		cmd.Dir = cfg.CWD
		cmd.Env = os.Environ()
		keys := make([]string, 0, len(cfg.Env))
		for k := range cfg.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd.Env = append(cmd.Env, k+"="+cfg.Env[k])
		}
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}
	} else {
		transport = &mcp.StreamableClientTransport{
			Endpoint: cfg.URL,
			HTTPClient: &http.Client{
				Transport: credentialTransport{base: http.DefaultTransport, cfg: cfg, resolve: resolver},
				// Authorization is injected by the transport, so never follow a
				// redirect and accidentally attach it to a different endpoint.
				CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("MCP redirects are disabled") },
			},
			// Reconnecting a notification stream must not replay a tool call.
			MaxRetries: -1,
		}
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "LuckyAgent", Version: "1"}, &mcp.ClientOptions{Capabilities: &mcp.ClientCapabilities{}})
	return client.Connect(ctx, transport, nil)
}

type credentialTransport struct {
	base    http.RoundTripper
	cfg     ServerConfig
	resolve CredentialResolver
}

func (t credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	if t.cfg.CredentialRef == "" {
		if t.cfg.APIKey != "" {
			r.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)
		}
		return t.base.RoundTrip(r)
	}
	if t.resolve == nil {
		return nil, fmt.Errorf("MCP credential resolver is unavailable")
	}
	var response *http.Response
	err := t.resolve(r.Context(), t.cfg.CredentialRef, func(secret []byte) error {
		r.Header.Set("Authorization", "Bearer "+string(secret))
		var err error
		response, err = t.base.RoundTrip(r)
		return err
	})
	return response, err
}

func discover(ctx context.Context, session *mcp.ClientSession, name string, generation uint64) ([]ToolDescriptor, error) {
	if init := session.InitializeResult(); init != nil && (init.Capabilities == nil || init.Capabilities.Tools == nil) {
		return nil, nil
	}
	var tools []ToolDescriptor
	seenNames := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	for range 1000 {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range result.Tools {
			if t.Name == "" || seenNames[t.Name] {
				return nil, fmt.Errorf("MCP server returned an empty or duplicate tool name")
			}
			seenNames[t.Name] = true
			input, err := json.Marshal(t.InputSchema)
			if err != nil {
				return nil, err
			}
			var schema map[string]any
			if json.Unmarshal(input, &schema) != nil || schema == nil || schema["type"] != "object" {
				return nil, fmt.Errorf("MCP tool has invalid inputSchema")
			}
			var output json.RawMessage
			if t.OutputSchema != nil {
				output, err = json.Marshal(t.OutputSchema)
				if err != nil {
					return nil, err
				}
			}
			tools = append(tools, ToolDescriptor{Ref: ToolRef{Server: name, Name: t.Name, Generation: generation}, Description: t.Description, InputSchema: input, OutputSchema: output})
		}
		if result.NextCursor == "" {
			sort.Slice(tools, func(i, j int) bool { return tools[i].Ref.Name < tools[j].Ref.Name })
			return tools, nil
		}
		if seenCursors[result.NextCursor] {
			return nil, fmt.Errorf("MCP tools/list repeated a pagination cursor")
		}
		seenCursors[result.NextCursor] = true
		cursor = result.NextCursor
	}
	return nil, fmt.Errorf("MCP tools/list exceeded pagination limit")
}
