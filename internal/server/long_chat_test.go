package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/config"
)

func longChatServer(t *testing.T, upstream http.HandlerFunc) *httptest.Server {
	t.Helper()
	provider := httptest.NewServer(upstream)
	t.Cleanup(provider.Close)
	cfg, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"provider": "openai", "api_key": "test", "api_base": provider.URL, "model": "gpt-test", "stream_mode": "native"} {
		if err := cfg.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	a, err := agent.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	s := New(a, DefaultServerConfig())
	httpServer := httptest.NewUnstartedServer(s.loggingMiddleware(http.HandlerFunc(s.handleChat)))
	httpServer.Config.WriteTimeout = 20 * time.Millisecond
	httpServer.Start()
	t.Cleanup(httpServer.Close)
	return httpServer
}

func TestLongChatOutlivesServerWriteDeadline(t *testing.T) {
	s := longChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"long task result\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	})
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(s.URL, "application/json", strings.NewReader(`{"message":"answer this task"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "long task result") || !strings.Contains(string(body), `"type":"complete"`) {
		t.Fatalf("SSE truncated by ordinary response deadline: %s", body)
	}
}

func TestLongChatDisconnectCancelsProvider(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	s := longChatServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	})
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(s.URL, "application/json", strings.NewReader(`{"message":"answer this task"}`))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		resp.Body.Close()
		t.Fatal("provider did not start")
	}
	resp.Body.Close()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider survived disconnected request")
	}
}
