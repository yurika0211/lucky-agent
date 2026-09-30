package websocket

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

func TestHandlerQueuesMessagesWithoutCancellingCurrentRun(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var calls []string
	runtime := &stubAgentRuntime{
		sessions: mgr,
		tools:    tool.NewRegistry(),
		chatStreamInputFn: func(ctx context.Context, sessionID string, input agent.UserTurnInput) (<-chan agent.ChatEvent, error) {
			mu.Lock()
			calls = append(calls, input.RoutingText)
			callNumber := len(calls)
			mu.Unlock()
			if callNumber == 1 {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					t.Fatalf("first run was cancelled: %v", ctx.Err())
				}
			}
			out := make(chan agent.ChatEvent, 1)
			out <- agent.ChatEvent{Type: agent.ChatEventDone, Content: input.RoutingText + " done"}
			close(out)
			return out, nil
		},
	}
	h := NewAgentHandler(runtime)
	client := &Client{SessionID: "queue-session", Send: make(chan *Message, 32)}

	handle := func(id, text string) {
		data, _ := json.Marshal(ChatData{Message: text, Stream: true})
		h.HandleMessage(client, &Message{Type: TypeChat, SessionID: client.SessionID, ID: id, Data: data})
	}
	handle("request-1", "first")
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first run did not start")
	}
	handle("request-2", "second")

	mu.Lock()
	if len(calls) != 1 {
		t.Fatalf("second message started before first completed: %#v", calls)
	}
	mu.Unlock()
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		complete := len(calls) == 2
		mu.Unlock()
		if complete {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued message did not run after the first message")
}

func TestHandlerEventsReplayFromCursor(t *testing.T) {
	mgr, err := session.NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := NewAgentHandler(&stubAgentRuntime{
		sessions: mgr,
		tools:    tool.NewRegistry(),
		chatStreamInputFn: func(context.Context, string, agent.UserTurnInput) (<-chan agent.ChatEvent, error) {
			out := make(chan agent.ChatEvent, 1)
			out <- agent.ChatEvent{Type: agent.ChatEventDone, Content: "done"}
			close(out)
			return out, nil
		},
	})
	client := &Client{SessionID: "replay-session", Send: make(chan *Message, 32)}
	data, _ := json.Marshal(ChatData{Message: "hello", Stream: true})
	h.HandleMessage(client, &Message{Type: TypeChat, SessionID: client.SessionID, ID: "request-replay", Data: data})
	h.WaitSession(client.SessionID, 2*time.Second)
	events := h.store.replay(client.SessionID, "")
	if len(events) < 3 {
		t.Fatalf("expected persisted lifecycle events, got %d", len(events))
	}
	remaining := h.store.replay(client.SessionID, events[0].ID)
	if len(remaining) != len(events)-1 {
		t.Fatalf("cursor replay returned %d events, want %d", len(remaining), len(events)-1)
	}
	if remaining[0].ID == events[0].ID {
		t.Fatal("cursor replay included the cursor event")
	}
}

func TestRunStoreReconnectReplaySkipsCompletedRunsWithoutCursor(t *testing.T) {
	store := newRunStore("")
	now := time.Now().UTC()
	completed := persistedRun{ID: "completed-run", SessionID: "reconnect-session", State: "completed", CreatedAt: now, UpdatedAt: now}
	active := persistedRun{ID: "active-run", SessionID: "reconnect-session", State: "running", CreatedAt: now, UpdatedAt: now}
	if err := store.upsertRun(completed); err != nil {
		t.Fatal(err)
	}
	if err := store.upsertRun(active); err != nil {
		t.Fatal(err)
	}
	completedEvent, _ := NewMessage(TypeStreamEnd, completed.SessionID, StreamEndData{FullResponse: "old answer"})
	completedEvent.RunID = completed.ID
	activeEvent, _ := NewMessage(TypeStreamChunk, active.SessionID, StreamChunkData{Content: "current answer"})
	activeEvent.RunID = active.ID
	if err := store.appendEvent(completed.SessionID, completed.ID, completedEvent); err != nil {
		t.Fatal(err)
	}
	if err := store.appendEvent(active.SessionID, active.ID, activeEvent); err != nil {
		t.Fatal(err)
	}

	events := store.replayForReconnect(active.SessionID, "")
	if len(events) != 1 || events[0].RunID != active.ID {
		t.Fatalf("empty cursor replayed %#v, want only active run", events)
	}
	events = store.replayForReconnect(active.SessionID, "missing-cursor")
	if len(events) != 1 || events[0].RunID != active.ID {
		t.Fatalf("stale cursor replayed %#v, want only active run", events)
	}
}

func TestHubDisconnectDoesNotCancelSession(t *testing.T) {
	handler := &cancelTrackingHandler{}
	hub := NewHub(handler, DefaultHubConfig())
	client := &Client{
		ID:        "client-1",
		SessionID: "disconnect-session",
		Send:      make(chan *Message, 1),
		Conn:      nil,
	}
	hub.clients[client.ID] = client
	hub.sessions[client.SessionID] = map[string]bool{client.ID: true}
	hub.unregisterClient(client)
	if handler.cancelled != 0 {
		t.Fatalf("disconnect cancelled %d runs", handler.cancelled)
	}
}

type cancelTrackingHandler struct {
	cancelled int
}

func (h *cancelTrackingHandler) HandleMessage(*Client, *Message) {}

func (h *cancelTrackingHandler) CancelSession(string) {
	h.cancelled++
}
