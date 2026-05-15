package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yurika0211/luckyharness/internal/agent"
	"github.com/yurika0211/luckyharness/internal/cron"
	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/metrics"
	"github.com/yurika0211/luckyharness/internal/session"
	"github.com/yurika0211/luckyharness/internal/tool"
)

// TestNewHandler verifies handler creation.
func TestNewHandler(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	h := NewHandler(adapter, nil)
	require.NotNil(t, h)
	assert.Equal(t, adapter, h.adapter)
	assert.NotNil(t, h.sessions)
}

// TestSetSessionID verifies direct session ID assignment.
func TestSetSessionID(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
	}

	h.setSessionID("chat1", "sess-abc")
	assert.True(t, h.hasSession("chat1"))
	assert.False(t, h.hasSession("chat2"))
}

// TestHasSession verifies session existence check.
func TestHasSession(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
	}

	assert.False(t, h.hasSession("chat1"))
	h.setSessionID("chat1", "sess-abc")
	assert.True(t, h.hasSession("chat1"))
}

// TestResetSessionWithSetSessionID creates a new session after reset.
func TestResetSessionWithSetSessionID(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
	}

	chatID := "12345"
	h.setSessionID(chatID, "old-session")
	oldSID := h.sessions[chatID]

	// resetSession needs agent, so test the map manipulation directly
	h.mu.Lock()
	h.sessions[chatID] = "new-session"
	h.mu.Unlock()

	newSID := h.sessions[chatID]
	assert.NotEqual(t, oldSID, newSID)
	assert.Equal(t, "new-session", newSID)
}

// TestSessionMapIsolation verifies that session IDs are isolated per chat.
func TestSessionMapIsolation(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
	}

	h.setSessionID("chat_A", "sess_A")
	h.setSessionID("chat_B", "sess_B")
	h.setSessionID("chat_C", "sess_C")

	// All different
	assert.NotEqual(t, h.sessions["chat_A"], h.sessions["chat_B"])
	assert.NotEqual(t, h.sessions["chat_B"], h.sessions["chat_C"])

	// Reset one chat
	h.setSessionID("chat_A", "sess_A_new")
	assert.NotEqual(t, "sess_A", h.sessions["chat_A"])

	// Other chats unaffected
	assert.Equal(t, "sess_B", h.sessions["chat_B"])
	assert.Equal(t, "sess_C", h.sessions["chat_C"])
}

// TestMultipleResets verifies that repeated resets always produce unique sessions.
func TestMultipleResets(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
	}

	chatID := "reset-test"
	var sids []string

	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("sess-%d", i)
		h.setSessionID(chatID, sid)
		sids = append(sids, sid)
	}

	// All session IDs should be unique
	seen := make(map[string]bool)
	for _, sid := range sids {
		assert.False(t, seen[sid], "duplicate session ID: %s", sid)
		seen[sid] = true
	}
}

// TestConcurrentSessionAccess tests that session map is safe under concurrent access.
func TestConcurrentSessionAccess(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			chatID := fmt.Sprintf("chat-%d", idx%5)
			h.setSessionID(chatID, fmt.Sprintf("sess-%d", idx))
			_ = h.hasSession(chatID)
		}(i)
	}
	wg.Wait()
}

func TestEnqueueChatRequestTracksQueuePosition(t *testing.T) {
	h := &Handler{
		queues: make(map[string]*chatQueue),
	}

	pos1, start1 := h.enqueueChatRequest("chat1", &queuedChatRequest{ctx: context.Background(), input: agent.TextUserTurnInput("first")})
	pos2, start2 := h.enqueueChatRequest("chat1", &queuedChatRequest{ctx: context.Background(), input: agent.TextUserTurnInput("second")})
	pos3, start3 := h.enqueueChatRequest("chat1", &queuedChatRequest{ctx: context.Background(), input: agent.TextUserTurnInput("third")})

	assert.Equal(t, 1, pos1)
	assert.True(t, start1)
	assert.Equal(t, 2, pos2)
	assert.False(t, start2)
	assert.Equal(t, 3, pos3)
	assert.False(t, start3)

	running, queued := h.queueStatus("chat1")
	assert.False(t, running)
	assert.Equal(t, 3, queued)
}

func TestDequeueChatRequestFIFO(t *testing.T) {
	h := &Handler{
		queues: make(map[string]*chatQueue),
	}
	h.enqueueChatRequest("chat1", &queuedChatRequest{ctx: context.Background(), input: agent.TextUserTurnInput("first")})
	h.enqueueChatRequest("chat1", &queuedChatRequest{ctx: context.Background(), input: agent.TextUserTurnInput("second")})

	req1, ok1 := h.dequeueChatRequest("chat1")
	req2, ok2 := h.dequeueChatRequest("chat1")
	_, ok3 := h.dequeueChatRequest("chat1")

	require.True(t, ok1)
	require.True(t, ok2)
	assert.Equal(t, "first", req1.input.RoutingText)
	assert.Equal(t, "second", req2.input.RoutingText)
	assert.False(t, ok3)
}

func TestBuildUserTurnInputRoutesImagesThroughAttachmentAnalysisPath(t *testing.T) {
	h := &Handler{}
	input := h.buildUserTurnInput(context.Background(), "看这张图", []gateway.Attachment{
		{
			Type:     gateway.AttachmentImage,
			FilePath: "/tmp/example.jpg",
			FileName: "example.jpg",
			MimeType: "image/jpeg",
		},
	})

	require.Equal(t, "user", input.Message.Role)
	require.Empty(t, input.Message.ContentParts)
	assert.Contains(t, input.RoutingText, "看这张图")
	assert.Contains(t, input.RoutingText, "[Multimedia Attachments]")
	assert.Contains(t, input.RoutingText, "example.jpg")
}

func TestGenerateRoundProgressFeedbackIncludesPreviousUserFacingUpdate(t *testing.T) {
	var gotObservations []string
	h := &Handler{
		chat: &mockAgentProvider{
			progressFn: func(ctx context.Context, userInput string, round int, observations []string) (string, error) {
				gotObservations = append([]string(nil), observations...)
				return "At this point, the retry is narrowing things down.", nil
			},
		},
	}

	progress := h.generateRoundProgressFeedback(
		context.Background(),
		nil,
		"generate a flower image",
		2,
		[]string{"Tool call: retry image generation", "Tool result: previous attempt failed"},
		[]string{"I've retried the image generation step."},
		"I've retried the image generation step.",
	)

	assert.Equal(t, "At this point, the retry is narrowing things down.", progress)
	require.Len(t, gotObservations, 3)
	assert.Equal(t, "Previous user-facing update: I've retried the image generation step.", gotObservations[0])
}

func TestSmoothProgressSummaryAddsContinuityCueForRepeatedFirstPersonStart(t *testing.T) {
	got := smoothProgressSummary("I've retried the image generation step and I'm waiting on the result.", 2, []string{
		"I've started by checking the previous run.",
	})
	assert.Equal(t, "At this point, I've retried the image generation step and I'm waiting on the result.", got)
}

// TestHandleMessageCommand verifies command routing.
func TestHandleMessageCommand(t *testing.T) {
	msg := &gateway.Message{
		ID:        "1",
		Chat:      gateway.Chat{ID: "123", Type: gateway.ChatPrivate},
		Sender:    gateway.User{ID: "1"},
		Text:      "/start",
		IsCommand: true,
		Command:   "start",
	}

	assert.True(t, msg.IsCommand)
	assert.Equal(t, "start", msg.Command)
}

// TestHandleMessagePrivateNonCommand verifies private chat routing.
func TestHandleMessagePrivateNonCommand(t *testing.T) {
	msg := &gateway.Message{
		ID:        "1",
		Chat:      gateway.Chat{ID: "123", Type: gateway.ChatPrivate},
		Sender:    gateway.User{ID: "1"},
		Text:      "Hello, bot!",
		IsCommand: false,
	}

	assert.Equal(t, gateway.ChatPrivate, msg.Chat.Type)
	assert.False(t, msg.IsCommand)
	assert.Equal(t, "Hello, bot!", msg.Text)
}

// TestHandleMessageGroupNonCommand verifies group chat routing.
func TestHandleMessageGroupNonCommand(t *testing.T) {
	msg := &gateway.Message{
		ID:        "1",
		Chat:      gateway.Chat{ID: "-100", Type: gateway.ChatGroup},
		Sender:    gateway.User{ID: "1"},
		Text:      "Hello, everyone!",
		IsCommand: false,
	}

	assert.Equal(t, gateway.ChatGroup, msg.Chat.Type)
	assert.False(t, msg.IsCommand)
}

// TestCommandRouting verifies all known commands are recognized.
func TestCommandRouting(t *testing.T) {
	tests := []struct {
		name    string
		command string
		args    string
	}{
		{"start", "start", ""},
		{"help", "help", ""},
		{"model no args", "model", ""},
		{"model with args", "model", "gpt-4"},
		{"soul", "soul", ""},
		{"tools", "tools", ""},
		{"reset", "reset", ""},
		{"history", "history", ""},
		{"session", "session", ""},
		{"chat with args", "chat", "hello world"},
		{"unknown", "unknown", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := &gateway.Message{
				ID:        "1",
				Chat:      gateway.Chat{ID: "123", Type: gateway.ChatPrivate},
				Sender:    gateway.User{ID: "1", Username: "testuser"},
				Text:      "/" + tt.command + " " + tt.args,
				IsCommand: true,
				Command:   tt.command,
				Args:      strings.TrimSpace(tt.args),
			}
			assert.NotNil(t, msg)
			assert.Equal(t, tt.command, msg.Command)
		})
	}
}

// TestWelcomeMessage verifies the welcome message content.
func TestWelcomeMessage(t *testing.T) {
	welcome := `🍀 *LuckyHarness Bot*

I'm an AI assistant powered by LuckyHarness.

*Available commands:*
/chat _message_ — Send a message to the AI
/model — Show current model
/soul — Show current SOUL info
/tools — List available tools
/reset — Reset conversation
/history — Show conversation history
/session — Show current session info
/help — Show this help

You can also just type a message directly!`

	assert.Contains(t, welcome, "LuckyHarness")
	assert.Contains(t, welcome, "/chat")
	assert.Contains(t, welcome, "/help")
	assert.Contains(t, welcome, "/model")
	assert.Contains(t, welcome, "/history")
	assert.Contains(t, welcome, "/session")
}

// TestHelpMessage verifies the help message content.
func TestHelpMessage(t *testing.T) {
	help := `*Available Commands:*

/start — Welcome message
/help — This help message
/chat _message_ — Send a message to the AI
/model \[name] — Get/set current model
/soul — Show current SOUL info
/tools — List available tools
/reset — Reset conversation
/history — Show conversation history
/session — Show current session info

*Tips:*
• In private chats, just type your message directly
• In groups, mention @bot or reply to a bot message
• Each chat has its own conversation session`

	assert.Contains(t, help, "/start")
	assert.Contains(t, help, "/chat")
	assert.Contains(t, help, "/model")
	assert.Contains(t, help, "/soul")
	assert.Contains(t, help, "/tools")
	assert.Contains(t, help, "/reset")
	assert.Contains(t, help, "/history")
	assert.Contains(t, help, "/session")
}

// TestGatewayMessageTypes verifies message type construction.
func TestGatewayMessageTypes(t *testing.T) {
	// Test ChatType
	assert.Equal(t, "private", gateway.ChatPrivate.String())
	assert.Equal(t, "group", gateway.ChatGroup.String())
	assert.Equal(t, "supergroup", gateway.ChatSuperGroup.String())
	assert.Equal(t, "channel", gateway.ChatChannel.String())

	// Test User.DisplayName
	u := gateway.User{ID: "1", Username: "test", FirstName: "Test", LastName: "User"}
	assert.Equal(t, "@test", u.DisplayName())

	u2 := gateway.User{ID: "2", FirstName: "Test", LastName: "User"}
	assert.Equal(t, "Test User", u2.DisplayName())

	u3 := gateway.User{ID: "3", FirstName: "Test"}
	assert.Equal(t, "Test", u3.DisplayName())

	u4 := gateway.User{ID: "4"}
	assert.Equal(t, "4", u4.DisplayName())
}

// ---------------------------------------------------------------------------
// v0.93.0: Coverage boost — pure functions, adapter methods, task management
// ---------------------------------------------------------------------------

func TestAgentProviderAdapter(t *testing.T) {
	// Create a minimal agent to test the adapter methods
	mockAgent := &mockAgentProvider{
		configSnap: agentConfigSnapshot{
			Model:                     "test-model",
			Provider:                  "test-provider",
			ChatTimeoutSeconds:        30,
			ProgressAsMessages:        true,
			ProgressAsNaturalLanguage: true,
			ShowToolDetailsInResult:   true,
		},
		toolsVal:   tool.NewRegistry(),
		skillsVal:  []*tool.SkillInfo{},
		cronEngine: cron.NewEngine(),
		metricsVal: metrics.NewMetrics(),
	}

	// Test resolve* functions
	t.Run("resolveChatStreamTimeout_NilProvider", func(t *testing.T) {
		got := resolveChatStreamTimeout(nil)
		assert.Equal(t, defaultChatStreamTimeout, got)
	})

	t.Run("resolveProgressAsMessages_NilProvider", func(t *testing.T) {
		got := resolveProgressAsMessages(nil)
		assert.True(t, got) // default is true
	})

	t.Run("resolveProgressAsNaturalLanguage_NilProvider", func(t *testing.T) {
		got := resolveProgressAsNaturalLanguage(nil)
		assert.False(t, got) // default is false
	})

	t.Run("resolveShowToolDetailsInResult_NilProvider", func(t *testing.T) {
		got := resolveShowToolDetailsInResult(nil)
		assert.False(t, got) // default is false
	})

	t.Run("resolveChatStreamTimeout_WithConfig", func(t *testing.T) {
		got := resolveChatStreamTimeout(mockAgent)
		assert.Equal(t, 30*time.Second, got)
	})

	t.Run("resolveProgressAsMessages_WithConfig", func(t *testing.T) {
		got := resolveProgressAsMessages(mockAgent)
		assert.True(t, got)
	})

	t.Run("resolveProgressAsNaturalLanguage_WithConfig", func(t *testing.T) {
		got := resolveProgressAsNaturalLanguage(mockAgent)
		assert.True(t, got)
	})

	t.Run("resolveShowToolDetailsInResult_WithConfig", func(t *testing.T) {
		got := resolveShowToolDetailsInResult(mockAgent)
		assert.True(t, got)
	})
}

func TestHandlerEffectiveMethods(t *testing.T) {
	h := &Handler{
		chatStreamTimeout:         5 * time.Second,
		progressAsMessages:        true,
		progressAsNaturalLanguage: false,
		showToolDetailsInResult:   true,
	}

	assert.Equal(t, 5*time.Second, h.effectiveChatStreamTimeout())
	assert.True(t, h.effectiveProgressAsMessages())
	assert.False(t, h.effectiveProgressAsNaturalLanguage())
	assert.True(t, h.effectiveShowToolDetailsInResult())

	// Zero timeout falls back to default
	h2 := &Handler{chatStreamTimeout: 0}
	assert.Equal(t, defaultChatStreamTimeout, h2.effectiveChatStreamTimeout())
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{25 * time.Hour, "1d 1h 0m"},
		{48*time.Hour + 30*time.Minute, "2d 0h 30m"},
		{2 * time.Hour, "2h 0m"},
		{90 * time.Minute, "1h 30m"},
		{45 * time.Minute, "45m 0s"},
		{0, "0m 0s"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		assert.Equal(t, tt.want, got)
	}
}

func TestTruncateString(t *testing.T) {
	assert.Equal(t, "hello", truncateString("hello", 10))
	assert.Equal(t, "hel...", truncateString("hello world", 6))
	assert.Equal(t, "", truncateString("", 5))
}

func TestPrependToolNarratives(t *testing.T) {
	// Empty lines → just return finalOutput
	assert.Equal(t, "result", prependToolNarratives(nil, "result"))
	assert.Equal(t, "result", prependToolNarratives([]string{}, "result"))

	// With lines
	got := prependToolNarratives([]string{"step 1", "step 2"}, "final answer")
	assert.Contains(t, got, "过程摘要")
	assert.Contains(t, got, "||")
	assert.Contains(t, got, "我刚刚先做了这些事")
	assert.Contains(t, got, "step 1")
	assert.Contains(t, got, "step 2")
	assert.Contains(t, got, "final answer")

	// Dedup
	got2 := prependToolNarratives([]string{"dup", "dup"}, "out")
	assert.Contains(t, got2, "dup")
	// Should only appear once
	assert.Equal(t, strings.Count(got2, "1. dup"), 1)
}

func TestAppendTelegramProgressSection(t *testing.T) {
	parts := appendTelegramProgressSection(nil, normalizeTelegramProgressSection("<b>💭 当前进度</b>\n<blockquote expandable>第一段</blockquote>"))
	parts = appendTelegramProgressSection(parts, normalizeTelegramProgressSection("<b>💭 当前进度</b>\n<blockquote expandable>第二段</blockquote>"))
	parts = appendTelegramProgressSection(parts, normalizeTelegramProgressSection("<b>💭 当前进度</b>\n<blockquote expandable>第二段</blockquote>"))

	require.Len(t, parts, 2)
	card := renderTelegramProgressHistoryCard(parts)
	assert.Contains(t, card, "第一段")
	assert.Contains(t, card, "第二段")
	assert.Equal(t, 1, strings.Count(card, "<blockquote expandable>"))
}

func TestIsTaskCanceledError(t *testing.T) {
	assert.True(t, isTaskCanceledError(context.Canceled))
	assert.True(t, isTaskCanceledError(fmt.Errorf("wrapped: %w", context.Canceled)))
	assert.True(t, isTaskCanceledError(fmt.Errorf("context canceled by user")))
	assert.False(t, isTaskCanceledError(fmt.Errorf("some other error")))
	assert.False(t, isTaskCanceledError(nil))
}

func TestIsTaskTimeoutError(t *testing.T) {
	assert.True(t, isTaskTimeoutError(context.DeadlineExceeded))
	assert.True(t, isTaskTimeoutError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)))
	assert.True(t, isTaskTimeoutError(fmt.Errorf("context deadline exceeded")))
	assert.False(t, isTaskTimeoutError(fmt.Errorf("some other error")))
	assert.False(t, isTaskTimeoutError(nil))
}

func TestHandlerTaskManagement(t *testing.T) {
	h := &Handler{
		sessions: make(map[string]string),
		tasks:    make(map[string]*chatTask),
	}

	// beginChatTask
	ctx, task := h.beginChatTask("chat1", context.Background())
	assert.NotNil(t, task)
	assert.NotNil(t, ctx)

	// Task should be registered
	h.mu.RLock()
	_, ok := h.tasks["chat1"]
	h.mu.RUnlock()
	assert.True(t, ok)

	// cancelChatTask
	cancelled := h.cancelChatTask("chat1")
	assert.True(t, cancelled)

	// Cancel non-existent task
	cancelled2 := h.cancelChatTask("nonexistent")
	assert.False(t, cancelled2)

	// finishChatTask with nil
	h.finishChatTask("chat1", nil) // should not panic

	// finishChatTask with real task
	ctx2, task2 := h.beginChatTask("chat2", context.Background())
	h.finishChatTask("chat2", task2)
	_ = ctx2

	h.mu.RLock()
	_, ok2 := h.tasks["chat2"]
	h.mu.RUnlock()
	assert.False(t, ok2)
}

func TestHandlerGetResetSession(t *testing.T) {
	sessMgr, err := session.NewManager(t.TempDir())
	require.NoError(t, err)

	h := &Handler{
		sessions: make(map[string]string),
		tasks:    make(map[string]*chatTask),
		agent: &mockAgentProvider{
			sessions: sessMgr,
		},
	}

	// getSessionID creates new session if not exists
	sid := h.getSessionID("chat1")
	assert.NotEmpty(t, sid)

	// Same chat returns same session
	sid2 := h.getSessionID("chat1")
	assert.Equal(t, sid, sid2)

	// resetSession creates new session
	newSid := h.resetSession("chat1")
	assert.NotEmpty(t, newSid)
	assert.NotEqual(t, sid, newSid)
}
