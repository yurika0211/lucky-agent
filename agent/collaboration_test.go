package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/collab"
)

type roleModelTestProvider struct {
	mockProvider
	model string
}

func (p *roleModelTestProvider) Chat(context.Context, []provider.Message) (*provider.Response, error) {
	return &provider.Response{Content: p.model}, nil
}

func TestCollaborationModelDoesNotChangeConcurrentDefault(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("provider", "openai"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("model", "main-model"); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.RegisterFactory("openai", func(c provider.Config) provider.Provider {
		return &roleModelTestProvider{model: c.LlmProvider.Model}
	})
	defaultProvider := &mockProvider{name: "default-provider"}
	a := &Agent{cfg: cfg, registry: registry, provider: defaultProvider, activeModel: "main-model"}
	snapshot, err := a.collaborationProvider(collab.AgentRuntimeProfile{Model: "review-model"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := snapshot.provider.Chat(context.Background(), nil)
	if err != nil || response.Content != "review-model" {
		t.Fatalf("role model ignored: response=%+v err=%v", response, err)
	}
	if a.baseProviderSnapshot().provider != defaultProvider || a.baseProviderSnapshot().model != "main-model" || cfg.Get().Model != "main-model" {
		t.Fatal("role changed default provider or saved configuration")
	}
}

func TestCollaborationProviderDoesNotInheritAnotherEndpointsCredentials(t *testing.T) {
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("provider", "openai"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("api_key", "chat-only-test-key"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Set("model", "main-model"); err != nil {
		t.Fatal(err)
	}
	c := config.Clone(cfg.Get())
	if c.Models.Endpoints == nil {
		c.Models.Endpoints = make(map[config.ModelKind]config.ModelEndpointConfig)
	}
	c.Models.Endpoints[config.ModelKindCompact] = config.ModelEndpointConfig{Provider: "ollama", APIBase: "http://127.0.0.1:11434"}
	if err := cfg.Replace(c); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	var captured provider.Config
	registry.RegisterFactory("ollama", func(c provider.Config) provider.Provider { captured = c; return &mockProvider{name: "local"} })
	a := &Agent{cfg: cfg, registry: registry, provider: &mockProvider{name: "default"}, activeModel: "main-model"}
	if _, err := a.collaborationProvider(collab.AgentRuntimeProfile{Provider: "ollama", Model: "local-review"}); err != nil {
		t.Fatal(err)
	}
	if captured.LlmProvider.APIKey != "" || captured.LlmProvider.BaseURL != "http://127.0.0.1:11434" || captured.LlmProvider.Model != "local-review" {
		t.Fatalf("role inherited a different endpoint's settings: provider=%q model=%q has_key=%t", captured.LlmProvider.Name, captured.LlmProvider.Model, captured.LlmProvider.APIKey != "")
	}
}

func TestCollaborationPolicyCannotElevateChildPrivileges(t *testing.T) {
	limits := tool.DefaultDelegateConfig()
	a := &Agent{delegate: tool.NewDelegateManager(limits)}
	cfg, err := a.collaborationLoopConfig(collab.AgentRuntimeProfile{MaxIterations: 2, Timeout: time.Second, ToolAllowlist: []string{}, AllowDelegate: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxIterations != 2 || cfg.Timeout != time.Second || !cfg.RestrictTools || len(cfg.AllowedTools) != 0 {
		t.Fatalf("role limits ignored: %+v", cfg)
	}
	if !strings.Contains(strings.Join(cfg.DisabledTools, ","), "subagent") {
		t.Fatal("role enabled recursive delegation despite global prohibition")
	}
	if _, err := a.collaborationLoopConfig(collab.AgentRuntimeProfile{ApprovalPolicy: "auto"}); err == nil {
		t.Fatal("role elevated approval policy")
	}
	if _, err := a.collaborationLoopConfig(collab.AgentRuntimeProfile{MemoryNamespace: "private-role"}); err == nil {
		t.Fatal("unsupported namespace silently accepted")
	}
}

func TestCollaborationAllowlistBlocksUnadvertisedAndGeneratedTools(t *testing.T) {
	cfg := LoopConfig{RestrictTools: true, AllowedTools: []string{"file_read"}}
	opts := provider.CallOptions{Tools: []map[string]any{
		{"function": map[string]any{"name": "file_read"}},
		{"function": map[string]any{"name": "generated_shell"}},
	}}
	filtered := applyToolAllowlist(opts, cfg)
	if len(filtered.Tools) != 1 || functionToolNameFromSchema(filtered.Tools[0]) != "file_read" {
		t.Fatal("generated tool escaped schema allowlist")
	}
	guard := newTurnToolGuard("", nil, nil)
	guard.restrictTools, guard.allowedTools = true, makeToolNameSet(cfg.AllowedTools)
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "generated_shell"}); !blocked {
		t.Fatal("unadvertised call escaped runtime guard")
	}
	if _, blocked := guard.blockMessage(provider.ToolCall{Name: "file_read"}); blocked {
		t.Fatal("allowed tool was blocked")
	}
	if tools := applyToolAllowlist(opts, LoopConfig{RestrictTools: true}); len(tools.Tools) != 0 {
		t.Fatal("empty allowlist must deny all tools")
	}
}

func TestCollaborationCWDStaysInsideParentWorkspace(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "nested")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := validateCollaborationCWD(parent, child)
	if err != nil || got != child {
		t.Fatalf("nested role cwd rejected: %q %v", got, err)
	}
	outside := t.TempDir()
	if _, err := validateCollaborationCWD(parent, outside); err == nil {
		t.Fatal("outside role cwd was accepted")
	}
	link := filepath.Join(parent, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := validateCollaborationCWD(parent, link); err == nil {
		t.Fatal("symlink escape was accepted")
	}
}

func TestCollaborationStructuredReviewRejectsInvalidVerdict(t *testing.T) {
	// A non-JSON answer cannot accidentally become a successful acceptance.
	sessions, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{provider: &mockProvider{}, sessions: sessions}
	err = a.verifyCollaborationSubTask(context.Background(), &collab.SubTask{Description: "deliver proof", ExecutionSessionID: "child"}, "looks fine")
	if err == nil || !strings.Contains(err.Error(), "invalid verifier response") {
		t.Fatalf("invalid review accepted: %v", err)
	}
}
