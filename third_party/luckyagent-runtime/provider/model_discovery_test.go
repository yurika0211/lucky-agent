package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestModelDiscoveryOpenAICompatibleUsesCredentialsAndCaches(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"zeta"},{"id":"alpha","display_name":"Alpha"}]}`))
	}))
	defer server.Close()

	discovery := NewModelDiscovery()
	models, err := discovery.Discover(context.Background(), ModelDiscoveryConfig{
		Provider: "openai-compatible",
		APIKey:   "test-key",
		APIBase:  server.URL + "/v1/chat/completions",
	}, false)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(models) != 2 || models[0].ID != "alpha" || models[0].DisplayName != "Alpha" {
		t.Fatalf("unexpected models %#v", models)
	}
	if _, err := discovery.Discover(context.Background(), ModelDiscoveryConfig{
		Provider: "openai-compatible",
		APIKey:   "test-key",
		APIBase:  server.URL + "/v1/chat/completions",
	}, false); err != nil {
		t.Fatalf("cached Discover: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected one network call, got %d", calls)
	}
	if _, err := discovery.Discover(context.Background(), ModelDiscoveryConfig{
		Provider: "openai-compatible",
		APIKey:   "test-key",
		APIBase:  server.URL + "/v1/chat/completions",
	}, true); err != nil {
		t.Fatalf("refreshed Discover: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected refresh to bypass cache, got %d calls", calls)
	}
	if _, err := discovery.Discover(context.Background(), ModelDiscoveryConfig{
		Provider: "openai-compatible",
		APIKey:   "test-key",
		APIBase:  server.URL + "/v1/chat/completions",
		Model:    "switched-model",
	}, false); err != nil {
		t.Fatalf("model-switched Discover: %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected model switch to invalidate cache, got %d calls", calls)
	}
}

func TestModelDiscoveryClassifiesHTTPFailuresWithoutCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	_, err := NewModelDiscovery().Discover(context.Background(), ModelDiscoveryConfig{
		Provider: "openai-compatible",
		APIKey:   "secret-key",
		APIBase:  server.URL + "/v1",
	}, true)
	var discoveryErr *ModelDiscoveryError
	if !errors.As(err, &discoveryErr) {
		t.Fatalf("expected ModelDiscoveryError, got %v", err)
	}
	if discoveryErr.Kind != ModelDiscoveryAuthentication {
		t.Fatalf("unexpected error kind %q", discoveryErr.Kind)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("credential leaked in error %q", err)
	}
}

func TestModelDiscoveryOllama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen:latest"}]}`))
	}))
	defer server.Close()

	discovery := NewModelDiscovery()
	discovery.cacheTTL = time.Minute
	models, err := discovery.Discover(context.Background(), ModelDiscoveryConfig{Provider: "ollama", APIBase: server.URL}, false)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(models) != 1 || models[0].ID != "qwen:latest" {
		t.Fatalf("unexpected models %#v", models)
	}
}

func TestModelEndpointStripsChatPath(t *testing.T) {
	endpoint, err := modelEndpoint("https://example.test/zen/v1/chat/completions", "openai-compatible")
	if err != nil {
		t.Fatalf("modelEndpoint: %v", err)
	}
	if endpoint != "https://example.test/zen/v1/models" {
		t.Fatalf("unexpected endpoint %q", endpoint)
	}
}
