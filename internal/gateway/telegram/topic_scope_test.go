package telegram

import (
	"context"
	"testing"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestTelegramConversationScopeIsolatesTopics(t *testing.T) {
	base := gateway.Chat{ID: "-100123", Type: gateway.ChatSuperGroup}
	general := &gateway.Message{Chat: base}
	topicA := &gateway.Message{Chat: base, ThreadID: "42"}
	topicB := &gateway.Message{Chat: base, ThreadID: "84"}

	if got := telegramConversationScope(general); got != "-100123" {
		t.Fatalf("general scope = %q, want chat ID", got)
	}
	if got, want := telegramConversationScope(topicA), "-100123::thread::42"; got != want {
		t.Fatalf("topic A scope = %q, want %q", got, want)
	}
	if got, want := telegramConversationScope(topicB), "-100123::thread::84"; got != want {
		t.Fatalf("topic B scope = %q, want %q", got, want)
	}
	if telegramConversationScope(topicA) == telegramConversationScope(topicB) {
		t.Fatal("distinct Telegram topics share a conversation scope")
	}
}

func TestTopicScopesKeepSessionQueueAndTaskStateIndependent(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
		tasks:    make(map[string]*chatTask),
		queues:   make(map[string]*chatQueue),
	}
	base := gateway.Chat{ID: "-100123", Type: gateway.ChatSuperGroup}
	topicA := telegramConversationScope(&gateway.Message{Chat: base, ThreadID: "42"})
	topicB := telegramConversationScope(&gateway.Message{Chat: base, ThreadID: "84"})

	h.setSessionID(topicA, "session-topic-a")
	h.setSessionID(topicB, "session-topic-b")
	if got := h.currentSessionID(topicA); got != "session-topic-a" {
		t.Fatalf("topic A session = %q", got)
	}
	if got := h.currentSessionID(topicB); got != "session-topic-b" {
		t.Fatalf("topic B session = %q", got)
	}

	if position, startWorker := h.enqueueChatRequest(topicA, &queuedChatRequest{}); position != 1 || !startWorker {
		t.Fatalf("topic A queue = position %d, start %t; want 1, true", position, startWorker)
	}
	if position, startWorker := h.enqueueChatRequest(topicB, &queuedChatRequest{}); position != 1 || !startWorker {
		t.Fatalf("topic B queue = position %d, start %t; want 1, true", position, startWorker)
	}

	_, taskA := h.beginChatTask(topicA, context.Background())
	_, taskB := h.beginChatTask(topicB, context.Background())
	defer h.finishChatTask(topicA, taskA)
	defer h.finishChatTask(topicB, taskB)
	if running, queued := h.queueStatus(topicA); !running || queued != 1 {
		t.Fatalf("topic A state = running %t, queued %d; want true, 1", running, queued)
	}
	if running, queued := h.queueStatus(topicB); !running || queued != 1 {
		t.Fatalf("topic B state = running %t, queued %d; want true, 1", running, queued)
	}
}

func TestTopicSessionCommandsOnlyAffectTheirTopic(t *testing.T) {
	h, server := newHandlerWithMockAgent(t)
	defer server.Close()

	base := gateway.Chat{ID: "-100123", Type: gateway.ChatSuperGroup}
	topicA := &gateway.Message{ID: "1", Chat: base, ThreadID: "42"}
	topicB := &gateway.Message{ID: "2", Chat: base, ThreadID: "84"}
	scopeA := telegramConversationScope(topicA)
	scopeB := telegramConversationScope(topicB)

	if err := h.handleNew(context.Background(), topicA); err != nil {
		t.Fatalf("start topic A session: %v", err)
	}
	if err := h.handleNew(context.Background(), topicB); err != nil {
		t.Fatalf("start topic B session: %v", err)
	}
	sessionA := h.currentSessionID(scopeA)
	sessionB := h.currentSessionID(scopeB)
	if sessionA == "" || sessionB == "" || sessionA == sessionB {
		t.Fatalf("topic sessions are not isolated: A=%q B=%q", sessionA, sessionB)
	}
	if got := h.currentSessionID(base.ID); got != "" {
		t.Fatalf("topic command wrote unscoped chat session %q", got)
	}

	if err := h.handleReset(context.Background(), topicA); err != nil {
		t.Fatalf("reset topic A session: %v", err)
	}
	if got := h.currentSessionID(scopeA); got == sessionA || got == "" {
		t.Fatalf("topic A reset session = %q, want a new session", got)
	}
	if got := h.currentSessionID(scopeB); got != sessionB {
		t.Fatalf("topic A reset changed topic B session: got %q, want %q", got, sessionB)
	}

	if err := h.handleRename(context.Background(), topicBWithArgs(topicB, "Topic B")); err != nil {
		t.Fatalf("rename topic B session: %v", err)
	}
	sess, ok := h.sessionManager().Get(sessionB)
	if !ok || sess.Title != "Topic B" {
		t.Fatalf("topic B rename did not target its session: %#v", sess)
	}

	target := h.sessionManager().NewWithTitle("Topic A target")
	if err := h.handleSessionSwitch(context.Background(), topicA, target.ID); err != nil {
		t.Fatalf("switch topic A session: %v", err)
	}
	if got := h.currentSessionID(scopeA); got != target.ID {
		t.Fatalf("topic A selected %q, want %q", got, target.ID)
	}
	if got := h.currentSessionID(scopeB); got != sessionB {
		t.Fatalf("topic A switch changed topic B session: got %q, want %q", got, sessionB)
	}

	if err := h.handleSession(context.Background(), topicA); err != nil {
		t.Fatalf("inspect topic A session: %v", err)
	}
	if err := h.handleSessions(context.Background(), topicB); err != nil {
		t.Fatalf("list sessions from topic B: %v", err)
	}
	if err := h.handleStatus(context.Background(), topicB); err != nil {
		t.Fatalf("inspect topic B status: %v", err)
	}
}

func TestTopicStopCommandOnlyCancelsItsTopicTask(t *testing.T) {
	h, server := newHandlerWithMockAgent(t)
	defer server.Close()

	base := gateway.Chat{ID: "-100123", Type: gateway.ChatSuperGroup}
	topicA := &gateway.Message{ID: "1", Chat: base, ThreadID: "42"}
	topicB := &gateway.Message{ID: "2", Chat: base, ThreadID: "84"}
	scopeA := telegramConversationScope(topicA)
	scopeB := telegramConversationScope(topicB)

	ctxA, taskA := h.beginChatTask(scopeA, context.Background())
	ctxB, taskB := h.beginChatTask(scopeB, context.Background())
	defer h.finishChatTask(scopeA, taskA)
	defer h.finishChatTask(scopeB, taskB)

	if err := h.handleStop(context.Background(), topicA); err != nil {
		t.Fatalf("stop topic A task: %v", err)
	}
	select {
	case <-ctxA.Done():
	default:
		t.Fatal("topic A task was not cancelled")
	}
	select {
	case <-ctxB.Done():
		t.Fatal("topic A stop also cancelled topic B task")
	default:
	}
}

func topicBWithArgs(msg *gateway.Message, args string) *gateway.Message {
	copy := *msg
	copy.Args = args
	return &copy
}
