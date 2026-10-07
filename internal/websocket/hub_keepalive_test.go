package websocket

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestClientPingKeepsReadDeadlineAlive(t *testing.T) {
	hub := NewHub(&mockHandler{}, DefaultHubConfig())
	go hub.Run()
	defer hub.Stop()
	server := httptest.NewServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/ws?session=keepalive"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hub.ClientCount() != 1 {
		t.Fatalf("client count=%d", hub.ClientCount())
	}

	// Idle longer than the old 60s server read timeout. The server pings every
	// 20s; receiving those control frames proves the socket stayed open.
	done := make(chan struct{})
	defer close(done)
	ws.SetPingHandler(func(payload string) error {
		return ws.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
	})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if _, _, err := ws.NextReader(); err != nil {
				return
			}
		}
	}()
	time.Sleep(65 * time.Second)
	if hub.ClientCount() != 1 {
		t.Fatalf("server dropped idle client, count=%d", hub.ClientCount())
	}
}

func TestNewHubClampsSlowPingPeriod(t *testing.T) {
	hub := NewHub(&mockHandler{}, HubConfig{
		PongWait:   30 * time.Second,
		PingPeriod: 60 * time.Second,
	})
	defer hub.Stop()
	if hub.pingPeriod <= 0 || hub.pingPeriod >= hub.pongWait {
		t.Fatalf("ping=%s pong=%s", hub.pingPeriod, hub.pongWait)
	}
	if hub.pingPeriod != 20*time.Second {
		t.Fatalf("ping=%s", hub.pingPeriod)
	}
}
