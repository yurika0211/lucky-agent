// Package mcpclient manages MCP connections independently of the agent and tool
// packages. One Manager belongs to one runtime identity; it is not a multi-user
// credential pool.
package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrClosed    = errors.New("MCP manager is closed")
	ErrStaleTool = errors.New("MCP tool catalog changed; discover the tool again")
	serverName   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$`)
)

type Config struct {
	Servers map[string]ServerConfig `json:"servers,omitempty"`
}

type ServerConfig struct {
	Transport      string            `json:"transport,omitempty"`
	URL            string            `json:"url,omitempty"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	CWD            string            `json:"cwd,omitempty"`
	CredentialRef  string            `json:"credential_ref,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	Disabled       bool              `json:"disabled,omitempty"`
	// APIKey is only for the legacy, transient /mcp connection command.
	// Persistent configuration uses CredentialRef.
	APIKey string `json:"-"`
}

func (c ServerConfig) Kind() string {
	if c.Transport != "" {
		return c.Transport
	}
	if c.URL != "" {
		return "http"
	}
	return "stdio"
}

func (c ServerConfig) Validate(name string) error {
	if !serverName.MatchString(name) {
		return fmt.Errorf("invalid MCP server name %q", name)
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > 3600 {
		return fmt.Errorf("MCP timeout_seconds must be between 0 and 3600")
	}
	switch c.Kind() {
	case "stdio":
		if strings.TrimSpace(c.Command) == "" || c.URL != "" {
			return fmt.Errorf("MCP stdio requires command and no url")
		}
		if c.CredentialRef != "" || c.APIKey != "" {
			return fmt.Errorf("MCP bearer credentials require http transport")
		}
	case "http":
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("MCP http requires an http(s) URL without userinfo or fragment")
		}
		if c.Command != "" || len(c.Args) != 0 || len(c.Env) != 0 || c.CWD != "" {
			return fmt.Errorf("MCP http does not accept process settings")
		}
	default:
		return fmt.Errorf("unsupported MCP transport %q", c.Transport)
	}
	for k, v := range c.Env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fmt.Errorf("invalid MCP environment entry")
		}
	}
	return nil
}

func (c Config) Validate() error {
	for name, server := range c.Servers {
		if err := server.Validate(name); err != nil {
			return fmt.Errorf("MCP server %q: %w", name, err)
		}
	}
	return nil
}

func (c Config) Clone() Config {
	out := Config{}
	if c.Servers != nil {
		out.Servers = make(map[string]ServerConfig, len(c.Servers))
	}
	for name, s := range c.Servers {
		s.Args = append([]string(nil), s.Args...)
		if s.Env != nil {
			env := make(map[string]string, len(s.Env))
			for k, v := range s.Env {
				env[k] = v
			}
			s.Env = env
		}
		out.Servers[name] = s
	}
	return out
}

type ToolRef struct {
	Server     string `json:"server"`
	Name       string `json:"name"`
	Generation uint64 `json:"generation"`
}

type ToolDescriptor struct {
	Ref          ToolRef         `json:"ref"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

// CallResult retains the entire protocol result, including non-text content,
// without leaking SDK types into the host runtime.
type CallResult struct {
	Content           []json.RawMessage `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
	Raw               json.RawMessage   `json:"-"`
}

type ServerStatus struct {
	Name       string `json:"name"`
	Transport  string `json:"transport"`
	State      string `json:"state"`
	ToolCount  int    `json:"tool_count"`
	Generation uint64 `json:"generation"`
	// Error is deliberately a non-secret stage description.
	Error string `json:"error,omitempty"`
}

type Caller interface {
	CallTool(context.Context, ToolRef, map[string]any) (CallResult, error)
}

// Admin is the narrow control-plane interface consumed by CLI/HTTP/gateways.
type Admin interface {
	Connect(context.Context, string, ServerConfig) error
	ConnectConfigured(context.Context, string) error
	Disconnect(context.Context, string) error
	Refresh(context.Context, string) error
	Status() []ServerStatus
}

type CredentialResolver func(context.Context, string, func([]byte) error) error
