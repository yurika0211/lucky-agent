package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type connection struct {
	config  ServerConfig
	session *mcp.ClientSession
	tools   []ToolDescriptor
	status  ServerStatus
}

type Manager struct {
	operations  sync.Mutex
	mu          sync.RWMutex
	connections map[string]*connection
	configured  Config
	resolve     CredentialResolver
	publish     func([]ToolDescriptor) error
	generation  uint64
	closed      bool
}

func NewManager(config Config, resolve CredentialResolver) *Manager {
	return &Manager{connections: map[string]*connection{}, configured: config.Clone(), resolve: resolve}
}

// Configure replaces the declarative startup/reconnect catalog without starting
// connections or changing active sessions. Callers may add verified plugin
// servers before the application's explicit Start. The manager owns a copy.
func (m *Manager) Configure(config Config) error {
	config = config.Clone()
	if err := config.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	m.configured = config
	return nil
}

// Configured returns an owned copy of the declarative startup/reconnect catalog.
// Applications can merge additions without discarding injected server settings.
func (m *Manager) Configured() Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configured.Clone()
}

// SetPublisher installs the host's catalog adapter. Publication runs under the
// manager lock so a newly visible handler can only execute against that same
// connection generation. The callback must not call back into the Manager.
func (m *Manager) SetPublisher(publish func([]ToolDescriptor) error) error {
	m.operations.Lock()
	defer m.operations.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if publish != nil {
		if err := publish(m.catalogLocked()); err != nil {
			return err
		}
	}
	m.publish = publish
	return nil
}

func (m *Manager) ConnectConfigured(ctx context.Context, name string) error {
	m.mu.RLock()
	cfg, ok := m.configured.Servers[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("MCP server %q is not configured", name)
	}
	return m.Connect(ctx, name, cfg)
}

// Start restores declaratively configured connections. Failures are visible
// independently in Status and do not discard other successfully loaded servers.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.RLock()
	configured := m.configured.Clone()
	m.mu.RUnlock()
	names := make([]string, 0, len(configured.Servers))
	for name := range configured.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		if err := m.Connect(ctx, name, configured.Servers[name]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Connect(ctx context.Context, name string, cfg ServerConfig) error {
	if err := cfg.Validate(name); err != nil {
		return err
	}
	m.operations.Lock()
	defer m.operations.Unlock()
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if cfg.Disabled {
		return m.disconnectLocked(name, "disabled", cfg.Kind())
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(cfg))
	defer cancel()
	session, err := dial(ctx, cfg, m.resolve)
	if err != nil {
		m.recordFailure(name, cfg, "connection failed")
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MCP server %q: connection failed", name)
	}
	m.generation++
	tools, err := discover(ctx, session, name, m.generation)
	if err != nil {
		_ = session.Close()
		m.recordFailure(name, cfg, "tool discovery failed")
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MCP server %q: tool discovery failed", name)
	}
	next := &connection{config: cfg, session: session, tools: tools, status: ServerStatus{Name: name, Transport: cfg.Kind(), State: "connected", ToolCount: len(tools), Generation: m.generation}}
	m.mu.Lock()
	old := m.connections[name]
	m.connections[name] = next
	if m.publish != nil {
		err = m.publish(m.catalogLocked())
	}
	if err != nil {
		if old == nil {
			delete(m.connections, name)
		} else {
			m.connections[name] = old
		}
	}
	m.mu.Unlock()
	if err != nil {
		_ = session.Close()
		return err
	}
	if old != nil && old.session != nil {
		_ = old.session.Close()
	}
	go m.watchConnection(name, session)
	return nil
}

func (m *Manager) watchConnection(name string, session *mcp.ClientSession) {
	_ = session.Wait()
	_ = session.Close()
	m.operations.Lock()
	defer m.operations.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.connections[name]
	if m.closed || entry == nil || entry.session != session {
		return
	}
	entry.session = nil
	entry.tools = nil
	entry.status.State = "failed"
	entry.status.Error = "connection closed"
	entry.status.ToolCount = 0
	if m.publish != nil {
		_ = m.publish(m.catalogLocked())
	}
}

func (m *Manager) recordFailure(name string, cfg ServerConfig, stage string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// A failed replacement leaves the last-good connection and catalog intact.
	if old := m.connections[name]; old != nil && old.session != nil {
		return
	}
	m.connections[name] = &connection{config: cfg, status: ServerStatus{Name: name, Transport: cfg.Kind(), State: "failed", Error: stage}}
}

func (m *Manager) Refresh(ctx context.Context, name string) error {
	m.operations.Lock()
	defer m.operations.Unlock()
	m.mu.RLock()
	entry := m.connections[name]
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		return ErrClosed
	}
	if entry == nil || entry.session == nil {
		return fmt.Errorf("MCP server %q is not connected", name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(entry.config))
	defer cancel()
	m.generation++
	tools, err := discover(ctx, entry.session, name, m.generation)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("MCP server %q: tool discovery failed", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	oldTools, oldStatus := entry.tools, entry.status
	entry.tools = tools
	entry.status.Generation = m.generation
	entry.status.ToolCount = len(tools)
	if m.publish != nil {
		if err = m.publish(m.catalogLocked()); err != nil {
			entry.tools, entry.status = oldTools, oldStatus
			return err
		}
	}
	return nil
}

func (m *Manager) Disconnect(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.operations.Lock()
	defer m.operations.Unlock()
	return m.disconnectLocked(name, "disconnected", "")
}

func (m *Manager) disconnectLocked(name, state, kind string) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	old := m.connections[name]
	if kind == "" && old != nil {
		kind = old.status.Transport
	}
	m.connections[name] = &connection{status: ServerStatus{Name: name, Transport: kind, State: state}}
	var err error
	if m.publish != nil {
		err = m.publish(m.catalogLocked())
	}
	if err != nil {
		if old == nil {
			delete(m.connections, name)
		} else {
			m.connections[name] = old
		}
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	if old != nil && old.session != nil {
		return old.session.Close()
	}
	return nil
}

func (m *Manager) CallTool(ctx context.Context, ref ToolRef, args map[string]any) (CallResult, error) {
	m.mu.RLock()
	entry := m.connections[ref.Server]
	if m.closed {
		m.mu.RUnlock()
		return CallResult{}, ErrClosed
	}
	if entry == nil || entry.session == nil {
		m.mu.RUnlock()
		return CallResult{}, fmt.Errorf("MCP server %q is not connected", ref.Server)
	}
	if entry.status.Generation != ref.Generation {
		m.mu.RUnlock()
		return CallResult{}, ErrStaleTool
	}
	found := false
	for _, tool := range entry.tools {
		if tool.Ref.Name == ref.Name {
			found = true
			break
		}
	}
	if !found {
		m.mu.RUnlock()
		return CallResult{}, ErrStaleTool
	}
	session, cfg := entry.session, entry.config
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, timeout(cfg))
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ref.Name, Arguments: args})
	if err != nil {
		if ctx.Err() != nil {
			return CallResult{}, ctx.Err()
		}
		return CallResult{}, fmt.Errorf("MCP server %q: tool call failed", ref.Server)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return CallResult{}, err
	}
	var out CallResult
	if err = json.Unmarshal(raw, &out); err != nil {
		return CallResult{}, err
	}
	out.Raw = raw
	return out, nil
}

func (m *Manager) Status() []ServerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	statuses := make([]ServerStatus, 0, len(m.connections))
	for _, entry := range m.connections {
		statuses = append(statuses, entry.status)
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
	return statuses
}

// Tools returns a detached snapshot for consumers that do not use the tool
// registry (for example another runtime or a backend service).
func (m *Manager) Tools() []ToolDescriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalogLocked()
}

func (m *Manager) catalogLocked() []ToolDescriptor {
	var tools []ToolDescriptor
	for _, entry := range m.connections {
		for _, t := range entry.tools {
			t.InputSchema = append(json.RawMessage(nil), t.InputSchema...)
			t.OutputSchema = append(json.RawMessage(nil), t.OutputSchema...)
			tools = append(tools, t)
		}
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Ref.Server != tools[j].Ref.Server {
			return tools[i].Ref.Server < tools[j].Ref.Server
		}
		return tools[i].Ref.Name < tools[j].Ref.Name
	})
	return tools
}

func (m *Manager) Close() error {
	m.operations.Lock()
	defer m.operations.Unlock()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	entries := m.connections
	m.connections = map[string]*connection{}
	var errs []error
	if m.publish != nil {
		if err := m.publish(nil); err != nil {
			errs = append(errs, err)
		}
	}
	m.mu.Unlock()
	for _, entry := range entries {
		if entry.session != nil {
			if err := entry.session.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func timeout(c ServerConfig) time.Duration {
	if c.TimeoutSeconds > 0 {
		return time.Duration(c.TimeoutSeconds) * time.Second
	}
	return 30 * time.Second
}
