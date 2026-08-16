package telegram

import (
	"errors"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestTelegramAutoReactionPolicy(t *testing.T) {
	group := &gateway.Message{ID: "1", Chat: gateway.Chat{ID: "-100", Type: gateway.ChatSuperGroup}}
	if emoji, ok := telegramAutoReaction(group, false); !ok || emoji != "👍" {
		t.Fatalf("group reaction = %q, %t", emoji, ok)
	}

	group.ReplyTo = &gateway.Message{ID: "previous"}
	if emoji, ok := telegramAutoReaction(group, false); !ok || emoji != "👀" {
		t.Fatalf("reply reaction = %q, %t", emoji, ok)
	}

	private := &gateway.Message{ID: "2", Chat: gateway.Chat{ID: "user", Type: gateway.ChatPrivate}}
	if _, ok := telegramAutoReaction(private, false); ok {
		t.Fatal("private messages must not be auto-reacted")
	}
	if _, ok := telegramAutoReaction(group, true); ok {
		t.Fatal("disabled auto reaction must not react")
	}
}

func TestTelegramChatErrorFeedback(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.New("billing_error: invalid subscription and insufficient balance"), "余额不足"},
		{errors.New("invalid api key"), "认证失败"},
		{errors.New("HTTP 429 rate limit"), "请求过于频繁"},
		{errors.New("protocol_not_supported: model does not support chat completions"), "Responses API"},
		{errors.New("context deadline exceeded"), "请求超时"},
	}
	for _, test := range tests {
		if got := telegramChatErrorFeedback(test.err); !strings.Contains(got, test.want) {
			t.Fatalf("telegramChatErrorFeedback(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
