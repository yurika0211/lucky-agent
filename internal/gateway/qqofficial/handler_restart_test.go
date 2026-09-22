package qqofficial

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

type restartSender struct {
	mu       sync.Mutex
	messages []string
	running  bool
	startErr error
	startN   int
	stopN    int
}

func (s *restartSender) Name() string { return "qq-test" }
func (s *restartSender) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startN++
	if s.startErr != nil {
		return s.startErr
	}
	s.running = true
	return nil
}
func (s *restartSender) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopN++
	s.running = false
	return nil
}
func (s *restartSender) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}
func (s *restartSender) Send(_ context.Context, _ string, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, message)
	return nil
}
func (s *restartSender) SendWithReply(ctx context.Context, chatID, _, message string) error {
	return s.Send(ctx, chatID, message)
}
func (s *restartSender) SendPhoto(context.Context, string, string, string, string) error {
	return nil
}
func (s *restartSender) SendDocument(context.Context, string, string, string, string) error {
	return nil
}
func (s *restartSender) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.messages...)
	return out
}

func TestCancelAllChatTasksQQ(t *testing.T) {
	h := NewHandler(&restartSender{running: true}, nil)
	ctx1, _ := h.beginChatTask("c1", context.Background())
	ctx2, _ := h.beginChatTask("c2", context.Background())
	h.mu.Lock()
	h.queues["c1"] = &chatQueue{requests: []*queuedChatRequest{{}}}
	h.mu.Unlock()

	n := h.cancelAllChatTasks()
	assert.Equal(t, 2, n)
	assert.Error(t, ctx1.Err())
	assert.Error(t, ctx2.Err())
	h.mu.RLock()
	defer h.mu.RUnlock()
	assert.Empty(t, h.tasks)
	assert.Empty(t, h.queues)
}

func TestHandleRestartQQSuccess(t *testing.T) {
	sender := &restartSender{running: true}
	h := NewHandler(sender, nil)
	msg := &gateway.Message{Chat: gateway.Chat{ID: "c1"}, Sender: gateway.User{ID: "u1"}}
	require.NoError(t, h.handleRestart(context.Background(), msg))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		if len(msgs) >= 2 && strings.Contains(msgs[len(msgs)-1], "已重连") {
			assert.False(t, h.restarting)
			assert.GreaterOrEqual(t, sender.stopN, 1)
			assert.GreaterOrEqual(t, sender.startN, 1)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages=%v", sender.snapshot())
}

func TestHandleRestartQQFailure(t *testing.T) {
	sender := &restartSender{running: true, startErr: assert.AnError}
	h := NewHandler(sender, nil)
	msg := &gateway.Message{Chat: gateway.Chat{ID: "c1"}, Sender: gateway.User{ID: "u1"}}
	require.NoError(t, h.handleRestart(context.Background(), msg))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		if len(msgs) >= 2 && strings.Contains(msgs[len(msgs)-1], "重启失败") {
			assert.False(t, h.restarting)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("messages=%v", sender.snapshot())
}

func TestRestartAllowedUsesAdapterAllowlist(t *testing.T) {
	adapter := NewAdapter(Config{AppID: "a", AppSecret: "b", AllowedUsers: []string{"u-ok"}})
	h := NewHandler(adapter, nil)
	assert.True(t, h.restartAllowed(&gateway.Message{Sender: gateway.User{ID: "u-ok"}}))
	assert.False(t, h.restartAllowed(&gateway.Message{Sender: gateway.User{ID: "u-no"}}))
}
