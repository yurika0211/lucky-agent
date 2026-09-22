package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yurika0211/luckyagent/internal/gateway"
)

type restartTestSender struct {
	mu        sync.Mutex
	messages  []string
	running   bool
	startErr  error
	stopErr   error
	startWait time.Duration
	startN    int
	stopN     int
}

func (s *restartTestSender) Name() string { return "telegram-test" }
func (s *restartTestSender) Start(ctx context.Context) error {
	if s.startWait > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.startWait):
		}
	}
	s.mu.Lock()
	s.startN++
	s.mu.Unlock()
	if s.startErr != nil {
		return s.startErr
	}
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	return nil
}
func (s *restartTestSender) Stop() error {
	s.mu.Lock()
	s.stopN++
	s.running = false
	s.mu.Unlock()
	return s.stopErr
}
func (s *restartTestSender) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
func (s *restartTestSender) Send(_ context.Context, _ string, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}
func (s *restartTestSender) SendWithReply(ctx context.Context, chatID string, _ string, message string) error {
	return s.Send(ctx, chatID, message)
}
func (s *restartTestSender) SendStream(context.Context, string, string) (gateway.StreamSender, error) {
	return nil, assert.AnError
}
func (s *restartTestSender) SendPhoto(context.Context, string, string, string, string) error {
	return nil
}
func (s *restartTestSender) SendDocument(context.Context, string, string, string, string) error {
	return nil
}
func (s *restartTestSender) SendHTML(ctx context.Context, chatID string, message string) error {
	return s.Send(ctx, chatID, message)
}
func (s *restartTestSender) SendWithReplyHTML(ctx context.Context, chatID string, _ string, message string) error {
	return s.Send(ctx, chatID, message)
}
func (s *restartTestSender) SendTypingLoop(context.Context, string) {}
func (s *restartTestSender) ReactToMessage(string, string, string)  {}

func (s *restartTestSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.messages))
	copy(out, s.messages)
	return out
}

func TestCancelAllChatTasks(t *testing.T) {
	h := NewHandler(NewAdapter(Config{Token: "t"}), nil)
	ctx1, _ := h.beginChatTask("c1", context.Background())
	ctx2, _ := h.beginChatTask("c2", context.Background())

	h.mu.Lock()
	h.queues["c1"] = &chatQueue{items: []*queuedChatRequest{{}}}
	h.mu.Unlock()

	cancelled := h.cancelAllChatTasks()
	assert.Equal(t, 2, cancelled)
	assert.Error(t, ctx1.Err())
	assert.Error(t, ctx2.Err())

	h.mu.RLock()
	defer h.mu.RUnlock()
	assert.Empty(t, h.tasks)
	assert.Empty(t, h.queues)
}

func TestHandleRestartSuccess(t *testing.T) {
	sender := &restartTestSender{running: true}
	h := NewHandler(NewAdapter(Config{Token: "t"}), nil)
	h.adapter = sender

	taskCtx, _ := h.beginChatTask("chat-1", context.Background())

	msg := &gateway.Message{
		Chat:   gateway.Chat{ID: "chat-1"},
		Sender: gateway.User{ID: "u1"},
	}
	require.NoError(t, h.handleRestart(context.Background(), msg))

	waitFor(t, 2*time.Second, func() bool {
		msgs := sender.snapshot()
		if len(msgs) < 2 {
			return false
		}
		return strings.Contains(msgs[len(msgs)-1], "已重连")
	})
	assert.Error(t, taskCtx.Err())
	assert.True(t, sender.IsRunning())
	assert.False(t, h.restarting)
	assert.GreaterOrEqual(t, sender.stopN, 1)
	assert.GreaterOrEqual(t, sender.startN, 1)
}

func TestHandleRestartAdminDenied(t *testing.T) {
	adapter := NewAdapter(Config{Token: "t", AdminIDs: []string{"admin-1"}})
	assert.False(t, adapter.cfg.IsAdmin("stranger"))
	assert.True(t, adapter.cfg.IsAdmin("admin-1"))

	h := NewHandler(adapter, nil)
	// No admin list / non-concrete adapter paths remain permissive for backward compatibility.
	sender := &restartTestSender{running: true}
	hLoose := NewHandler(NewAdapter(Config{Token: "t"}), nil)
	hLoose.adapter = sender
	require.NoError(t, hLoose.restartAdminGate(context.Background(), &gateway.Message{
		Chat:   gateway.Chat{ID: "c"},
		Sender: gateway.User{ID: "anyone"},
	}))

	// Concrete *Adapter with AdminIDs must deny non-admins. Send may fail without a live bot,
	// but the deny path must still execute and return an error instead of nil.
	err := h.restartAdminGate(context.Background(), &gateway.Message{
		Chat:   gateway.Chat{ID: "c"},
		Sender: gateway.User{ID: "stranger"},
	})
	require.Error(t, err)
}

func TestHandleRestartFailureNotifiesUser(t *testing.T) {
	sender := &restartTestSender{running: true, startErr: assert.AnError}
	h := NewHandler(NewAdapter(Config{Token: "t"}), nil)
	h.adapter = sender

	msg := &gateway.Message{Chat: gateway.Chat{ID: "c1"}, Sender: gateway.User{ID: "u"}}
	require.NoError(t, h.handleRestart(context.Background(), msg))
	waitFor(t, 2*time.Second, func() bool {
		msgs := sender.snapshot()
		if len(msgs) < 2 {
			return false
		}
		return strings.Contains(msgs[len(msgs)-1], "重启失败")
	})
	assert.False(t, h.restarting)
}

func TestHandleRestartInProgress(t *testing.T) {
	sender := &restartTestSender{running: true, startWait: 300 * time.Millisecond}
	h := NewHandler(NewAdapter(Config{Token: "t"}), nil)
	h.adapter = sender

	msg := &gateway.Message{Chat: gateway.Chat{ID: "c1"}, Sender: gateway.User{ID: "u"}}
	require.NoError(t, h.handleRestart(context.Background(), msg))

	// Give first restart a moment to flip the flag.
	waitFor(t, time.Second, func() bool {
		h.mu.RLock()
		defer h.mu.RUnlock()
		return h.restarting
	})
	require.NoError(t, h.handleRestart(context.Background(), msg))
	msgs := sender.snapshot()
	require.NotEmpty(t, msgs)
	assert.Contains(t, msgs[len(msgs)-1], "正在重启中")
	waitFor(t, 2*time.Second, func() bool { return !h.restarting })
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
