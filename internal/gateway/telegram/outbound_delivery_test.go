package telegram

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

type capturedTelegramSend struct {
	parseMode string
	replyTo   string
	threadID  string
	text      string
}

func newOutboundDeliveryAdapter(t *testing.T, maxMessageLen int, sent *[]capturedTelegramSend) *Adapter {
	t.Helper()

	bot, err := newMockBot(func(request *http.Request) map[string]any {
		if containsMethod(request.URL.Path, "sendMessage") {
			if err := request.ParseForm(); err != nil {
				t.Fatalf("parse Telegram request: %v", err)
			}
			*sent = append(*sent, capturedTelegramSend{
				parseMode: request.Form.Get("parse_mode"),
				replyTo:   request.Form.Get("reply_to_message_id"),
				threadID:  request.Form.Get("message_thread_id"),
				text:      request.Form.Get("text"),
			})
		}
		return defaultMockBotResponse(request)
	})
	if err != nil {
		t.Fatalf("create mock bot: %v", err)
	}

	adapter := NewAdapter(Config{Token: bot.Token, MaxMessageLen: maxMessageLen, RateLimit: 1000})
	adapter.bot = bot
	adapter.running = true
	return adapter
}

func TestSendWithReplyAnchorsEveryLongTextChunk(t *testing.T) {
	var sent []capturedTelegramSend
	adapter := newOutboundDeliveryAdapter(t, 80, &sent)
	adapter.rememberTelegramThread("12345", "777", "42")
	message := strings.Repeat("A complete answer stays in this topic. ", 8)

	if err := adapter.SendWithReply(context.Background(), "12345", "777", message); err != nil {
		t.Fatalf("SendWithReply() error = %v", err)
	}
	if len(sent) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(sent))
	}
	for index, request := range sent {
		if request.replyTo != "777" {
			t.Fatalf("chunk %d reply_to_message_id = %q, want 777", index, request.replyTo)
		}
		if request.parseMode != "HTML" {
			t.Fatalf("chunk %d parse_mode = %q, want HTML", index, request.parseMode)
		}
		if request.threadID != "42" {
			t.Fatalf("chunk %d message_thread_id = %q, want 42", index, request.threadID)
		}
	}
}

func TestSendWithReplyHTMLSplitsBalancedSafeChunks(t *testing.T) {
	var sent []capturedTelegramSend
	adapter := newOutboundDeliveryAdapter(t, 96, &sent)
	adapter.rememberTelegramThread("12345", "777", "42")
	message := `<blockquote expandable><b>Result</b> ` + strings.Repeat(`&lt;unsafe&gt; &amp; useful text `, 16) + `</blockquote>`

	if err := adapter.SendWithReplyHTML(context.Background(), "12345", "777", message); err != nil {
		t.Fatalf("SendWithReplyHTML() error = %v", err)
	}
	if len(sent) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(sent))
	}
	for index, request := range sent {
		if request.replyTo != "777" {
			t.Fatalf("chunk %d reply_to_message_id = %q, want 777", index, request.replyTo)
		}
		if request.parseMode != "HTML" {
			t.Fatalf("chunk %d parse_mode = %q, want HTML", index, request.parseMode)
		}
		if request.threadID != "42" {
			t.Fatalf("chunk %d message_thread_id = %q, want 42", index, request.threadID)
		}
		if len(request.text) > 96 {
			t.Fatalf("chunk %d length = %d, want <= 96", index, len(request.text))
		}
		if request.text != sanitizeTelegramHTML(request.text) {
			t.Fatalf("chunk %d is not balanced safe Telegram HTML: %q", index, request.text)
		}
	}
}

func TestSplitMessageDoesNotBreakUTF8Runes(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test", MaxMessageLen: 17})
	message := strings.Repeat("中文内容", 12)
	chunks := adapter.splitMessage(message)

	if strings.Join(chunks, "") != message {
		t.Fatalf("joined chunks = %q, want %q", strings.Join(chunks, ""), message)
	}
	for index, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d is not valid UTF-8: %q", index, chunk)
		}
		if len(chunk) > 17 {
			t.Fatalf("chunk %d length = %d, want <= 17", index, len(chunk))
		}
	}
}

func TestSanitizeTelegramHTMLEscapesUnsafeTagsAndLinks(t *testing.T) {
	message := `<b>Safe</b> <script>alert(1)</script> <a href="javascript:alert(1)">bad</a> <a href="https://example.com/?a=1&amp;b=2">good</a>`
	got := sanitizeTelegramHTML(message)

	if strings.Contains(got, "<script>") || strings.Contains(got, `<a href="javascript:`) {
		t.Fatalf("unsafe HTML was retained: %q", got)
	}
	if !strings.Contains(got, `&lt;script&gt;alert(1)&lt;/script&gt;`) {
		t.Fatalf("unsafe tag was not escaped: %q", got)
	}
	if !strings.Contains(got, `<a href="https://example.com/?a=1&amp;b=2">good</a>`) {
		t.Fatalf("safe link was not retained: %q", got)
	}
}
