package agent_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/lucky-agent/agent"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// embeddingProvider supplies a fixed reply without a backend or credentials.
type embeddingProvider struct{}

func (embeddingProvider) Name() string    { return "embedding-fixture" }
func (embeddingProvider) Validate() error { return nil }
func (embeddingProvider) Chat(context.Context, []provider.Message) (*provider.Response, error) {
	return &provider.Response{Content: "Hello from the embedded Agent."}, nil
}
func (embeddingProvider) ChatStream(context.Context, []provider.Message) (<-chan provider.StreamChunk, error) {
	return nil, errors.New("unexpected stream request")
}

func minimalInputs(t *testing.T) agent.Inputs {
	t.Helper()
	dir := t.TempDir()
	configManager, err := config.NewManagerWithDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := session.NewManager(filepath.Join(dir, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	return agent.Inputs{
		Config:   configManager,
		Provider: embeddingProvider{},
		Sessions: sessions,
		Tools:    tool.NewRegistry(),
	}
}

func TestNewWithInputsMinimalChat(t *testing.T) {
	in := minimalInputs(t)
	instance, err := agent.NewWithInputs(agent.Options{}, &in)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := instance.Close(); err != nil {
			t.Error(err)
		}
	})
	sess := in.Sessions.New()
	reply, err := instance.ChatWithSession(t.Context(), sess.ID, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "Hello from the embedded Agent." {
		t.Fatalf("got %q", reply)
	}
	messages := sess.GetMessages()
	if len(messages) == 0 || messages[len(messages)-1].Role != "assistant" || messages[len(messages)-1].Content != reply {
		t.Fatal("the supplied session manager did not retain the assistant reply")
	}
}

func TestNewWithInputsMissingRequiredCapability(t *testing.T) {
	base := minimalInputs(t)
	for _, tc := range []struct {
		name string
		omit func(*agent.Inputs)
	}{
		{"configuration", func(in *agent.Inputs) { in.Config = nil }},
		{"provider", func(in *agent.Inputs) { in.Provider = nil }},
		{"sessions", func(in *agent.Inputs) { in.Sessions = nil }},
		{"tools", func(in *agent.Inputs) { in.Tools = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			tc.omit(&in)
			in.CloseResources = func() error {
				t.Error("failed construction closed caller resources")
				return nil
			}
			instance, err := agent.NewWithInputs(agent.Options{TakeOwnership: true}, &in)
			if instance != nil || err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("missing %s: agent=%v err=%v", tc.name, instance != nil, err)
			}
		})
	}
}

func TestNewWithInputsOwnershipNeedsCloser(t *testing.T) {
	in := minimalInputs(t)
	instance, err := agent.NewWithInputs(agent.Options{TakeOwnership: true}, &in)
	if instance != nil || err == nil || !strings.Contains(err.Error(), "CloseResources") {
		t.Fatalf("missing CloseResources: agent=%v err=%v", instance != nil, err)
	}
}

func TestNewWithInputsCloseCallback(t *testing.T) {
	for _, tc := range []struct {
		name string
		own  bool
	}{
		{"borrow", false}, {"transfer", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := minimalInputs(t)
			closeErr := errors.New("fixture resource close failed")
			calls := 0
			in.CloseResources = func() error {
				calls++
				return closeErr
			}
			instance, err := agent.NewWithInputs(agent.Options{TakeOwnership: tc.own}, &in)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				err := instance.Close()
				if tc.own {
					if !errors.Is(err, closeErr) || calls != 1 {
						t.Fatalf("owned close: err=%v calls=%d", err, calls)
					}
				} else if err != nil || calls != 0 {
					t.Fatalf("borrowed close: err=%v calls=%d", err, calls)
				}
			}
		})
	}
}
