package mcpclient

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestConfigureIsInertAtomicAndOwnsConfiguration(t *testing.T) {
	var requests atomic.Int32
	endpoint := startHTTP(t, fixtureServer(), true, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			next.ServeHTTP(w, r)
		})
	})
	manager := NewManager(Config{}, nil)
	t.Cleanup(func() { _ = manager.Close() })
	config := Config{Servers: map[string]ServerConfig{"plugin.docs": {URL: endpoint}}}
	if err := manager.Configure(config); err != nil {
		t.Fatal(err)
	}
	config.Servers["plugin.docs"] = ServerConfig{URL: "http://127.0.0.1:1"}
	snapshot := manager.Configured()
	snapshot.Servers["plugin.docs"] = ServerConfig{URL: "http://127.0.0.1:1"}
	if requests.Load() != 0 || len(manager.Status()) != 0 {
		t.Fatal("configuration started a connection")
	}
	if err := manager.Configure(Config{Servers: map[string]ServerConfig{"invalid": {}}}); err == nil {
		t.Fatal("invalid configuration accepted")
	}
	if err := manager.Start(context.Background()); err != nil {
		t.Fatalf("failed validation or caller mutation changed the catalog: %v", err)
	}
	if requests.Load() == 0 || len(manager.Tools()) != 2 {
		t.Fatal("explicit startup did not connect configured servers")
	}
	if err := manager.Disconnect(context.Background(), "plugin.docs"); err != nil {
		t.Fatal(err)
	}
	if err := manager.ConnectConfigured(context.Background(), "plugin.docs"); err != nil {
		t.Fatalf("configured reconnect failed: %v", err)
	}
	ref := manager.Tools()[0].Ref
	if err := manager.Configure(Config{}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CallTool(context.Background(), ref, map[string]any{"value": "ok"}); err != nil {
		t.Fatalf("configuration replacement stopped an active session: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Configure(Config{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed manager accepted configuration: %v", err)
	}
}
