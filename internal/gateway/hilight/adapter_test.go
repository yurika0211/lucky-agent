package hilight

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestResolveConnectWSURLAppendsUUID(t *testing.T) {
	got, err := resolveConnectWSURL("wss://example.com/open-apis/ws")
	if err != nil {
		t.Fatalf("resolveConnectWSURL: %v", err)
	}
	if !strings.HasPrefix(got, "wss://example.com/open-apis/ws/") {
		t.Fatalf("unexpected url: %s", got)
	}
	suffix := strings.TrimPrefix(got, "wss://example.com/open-apis/ws/")
	if len(suffix) < 32 {
		t.Fatalf("expected uuid suffix, got %q", suffix)
	}
}

func TestResolveConnectWSURLPlaceholder(t *testing.T) {
	got, err := resolveConnectWSURL("wss://example.com/path/{UUIDD}/tail")
	if err != nil {
		t.Fatalf("resolveConnectWSURL: %v", err)
	}
	if strings.Contains(got, "{UUIDD}") {
		t.Fatalf("placeholder not replaced: %s", got)
	}
	if !strings.HasPrefix(got, "wss://example.com/path/") || !strings.HasSuffix(got, "/tail") {
		t.Fatalf("unexpected url: %s", got)
	}
}

func TestIsDMAllowed(t *testing.T) {
	cfg := Config{DMPolicy: "open", AllowFrom: []string{"*"}}
	if !cfg.isDMAllowed("u1") {
		t.Fatal("open/* should allow")
	}
	cfg = Config{DMPolicy: "allowlist", AllowFrom: []string{"u1"}}
	if !cfg.isDMAllowed("u1") || cfg.isDMAllowed("u2") {
		t.Fatal("allowlist mismatch")
	}
	cfg = Config{DMPolicy: "disabled"}
	if cfg.isDMAllowed("u1") {
		t.Fatal("disabled should deny")
	}
}

func TestHandleInboundMsgAndReply(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	incoming := make(chan Envelope, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var env Envelope
			if err := json.Unmarshal(data, &env); err != nil {
				continue
			}
			incoming <- env
			if env.Action == "connected" {
				payload, _ := json.Marshal(msgPayload{UserID: "user-1", UserName: "Alice", Text: "hello hilight"})
				_ = conn.WriteJSON(Envelope{Context: "ctx-1", Action: "msg", Payload: payload})
			}
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	adapter := NewAdapter(Config{
		WSURL:                   wsURL,
		AuthToken:               "test-token",
		AccountID:               "acct-1",
		DMPolicy:                "open",
		AllowFrom:               []string{"*"},
		HeartbeatIntervalMS:     60_000,
		ReconnectIntervalMS:     100,
		MaxReconnectIntervalMS:  200,
	})

	handled := make(chan *gateway.Message, 1)
	adapter.SetHandler(func(ctx context.Context, msg *gateway.Message) error {
		handled <- msg
		return adapter.SendWithReply(ctx, msg.Chat.ID, msg.ID, "pong-from-agent")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adapter.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer adapter.Stop()

	select {
	case env := <-incoming:
		if env.Action != "connected" {
			t.Fatalf("expected connected, got %s", env.Action)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting connected")
	}

	var sawTyping, sawReply bool
	var msg *gateway.Message
	deadline := time.After(3 * time.Second)
	for !(sawTyping && sawReply && msg != nil) {
		select {
		case env := <-incoming:
			switch env.Action {
			case "typing":
				sawTyping = true
			case "reply":
				var p replyPayload
				if err := json.Unmarshal(env.Payload, &p); err != nil {
					t.Fatalf("decode reply: %v", err)
				}
				if p.UserID != "user-1" || p.Text != "pong-from-agent" || !p.Done {
					t.Fatalf("unexpected reply payload: %+v", p)
				}
				if env.Context != "ctx-1" {
					t.Fatalf("expected context ctx-1, got %s", env.Context)
				}
				sawReply = true
			}
		case m := <-handled:
			msg = m
		case <-deadline:
			t.Fatalf("timeout typing=%v reply=%v msg=%v", sawTyping, sawReply, msg != nil)
		}
	}

	if msg.Chat.ID != "user-1" || msg.Text != "hello hilight" || msg.ThreadID != "ctx-1" {
		t.Fatalf("unexpected inbound message: %+v", msg)
	}
}
