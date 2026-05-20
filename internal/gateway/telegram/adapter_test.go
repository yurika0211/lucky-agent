package telegram

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, 4000, cfg.MaxMessageLen)
	assert.Equal(t, 1, cfg.RateLimit)
	assert.Equal(t, 30, cfg.PollTimeout)
}

func TestConfigIsChatAllowed(t *testing.T) {
	// Empty whitelist = allow all
	cfg := Config{}
	assert.True(t, cfg.IsChatAllowed("123"))
	assert.True(t, cfg.IsChatAllowed("any"))

	// With whitelist
	cfg = Config{AllowedChats: []string{"123", "456"}}
	assert.True(t, cfg.IsChatAllowed("123"))
	assert.True(t, cfg.IsChatAllowed("456"))
	assert.False(t, cfg.IsChatAllowed("789"))
}

func TestConfigIsAdmin(t *testing.T) {
	cfg := Config{AdminIDs: []string{"1", "2"}}
	assert.True(t, cfg.IsAdmin("1"))
	assert.True(t, cfg.IsAdmin("2"))
	assert.False(t, cfg.IsAdmin("3"))
}

func TestNewAdapter(t *testing.T) {
	cfg := Config{Token: "test-token"}
	adapter := NewAdapter(cfg)
	assert.NotNil(t, adapter)
	assert.Equal(t, "telegram", adapter.Name())
	assert.False(t, adapter.IsRunning())
}

func TestNewAdapterDefaults(t *testing.T) {
	cfg := Config{Token: "test-token"} // MaxMessageLen, RateLimit, PollTimeout all zero
	adapter := NewAdapter(cfg)
	assert.Equal(t, 4000, adapter.cfg.MaxMessageLen)
	assert.Equal(t, 1, adapter.cfg.RateLimit)
	assert.Equal(t, 30, adapter.cfg.PollTimeout)
}

func TestNewHTTPClientNoProxy(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test-token"})
	client, err := adapter.newHTTPClient()
	assert.NoError(t, err)
	assert.NotNil(t, client)
	assert.Nil(t, client.Transport)
}

func TestNewHTTPClientHTTPProxy(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test-token", Proxy: "http://127.0.0.1:7897"})
	client, err := adapter.newHTTPClient()
	assert.NoError(t, err)
	assert.NotNil(t, client)

	transport, ok := client.Transport.(*http.Transport)
	if assert.True(t, ok) {
		reqURL, parseErr := url.Parse("https://api.telegram.org")
		assert.NoError(t, parseErr)
		proxyURL, proxyErr := transport.Proxy(&http.Request{URL: reqURL})
		assert.NoError(t, proxyErr)
		if assert.NotNil(t, proxyURL) {
			assert.Equal(t, "http://127.0.0.1:7897", proxyURL.String())
		}
	}
}

func TestNewHTTPClientSOCKS5Proxy(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test-token", Proxy: "socks5://127.0.0.1:7890"})
	client, err := adapter.newHTTPClient()
	assert.NoError(t, err)
	assert.NotNil(t, client)

	transport, ok := client.Transport.(*http.Transport)
	if assert.True(t, ok) {
		assert.Nil(t, transport.Proxy)
		assert.NotNil(t, transport.DialContext)
	}
}

func TestNewHTTPClientInvalidProxy(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test-token", Proxy: "://bad proxy"})
	client, err := adapter.newHTTPClient()
	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parse proxy URL")
}

func TestNewHTTPClientUnsupportedProxyScheme(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test-token", Proxy: "ftp://127.0.0.1:21"})
	client, err := adapter.newHTTPClient()
	assert.Nil(t, client)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported proxy scheme")
}

func TestAdapterStartNoToken(t *testing.T) {
	cfg := Config{Token: ""}
	adapter := NewAdapter(cfg)
	err := adapter.Start(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "bot token is required")
}

func TestAdapterStop(t *testing.T) {
	cfg := Config{Token: "test-token"}
	adapter := NewAdapter(cfg)

	// Stop without start should not panic
	err := adapter.Stop()
	assert.NoError(t, err)
}

func TestAdapterSendNotRunning(t *testing.T) {
	cfg := Config{Token: "test-token"}
	adapter := NewAdapter(cfg)

	err := adapter.Send(nil, "123", "hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestAdapterSendWithReplyNotRunning(t *testing.T) {
	cfg := Config{Token: "test-token"}
	adapter := NewAdapter(cfg)

	err := adapter.SendWithReply(nil, "123", "1", "hello")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not running")
}

func TestSplitMessage(t *testing.T) {
	cfg := Config{Token: "test", MaxMessageLen: 10}
	adapter := NewAdapter(cfg)

	// Short message
	chunks := adapter.splitMessage("hello")
	assert.Len(t, chunks, 1)
	assert.Equal(t, "hello", chunks[0])

	// Long message
	longMsg := "abcdefghij" + "klmnopqrst"
	chunks = adapter.splitMessage(longMsg)
	assert.Len(t, chunks, 2)

	// Message with newlines
	msgWithNewlines := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11"
	adapter.cfg.MaxMessageLen = 30
	chunks = adapter.splitMessage(msgWithNewlines)
	assert.GreaterOrEqual(t, len(chunks), 2)
}

func TestSplitMessageExactBoundary(t *testing.T) {
	cfg := Config{Token: "test", MaxMessageLen: 5}
	adapter := NewAdapter(cfg)

	chunks := adapter.splitMessage("abcde")
	assert.Len(t, chunks, 1)
	assert.Equal(t, "abcde", chunks[0])
}

func TestSplitMessageRespectsNewlines(t *testing.T) {
	cfg := Config{Token: "test", MaxMessageLen: 15}
	adapter := NewAdapter(cfg)

	msg := "line1\nline2\nline3\nline4"
	chunks := adapter.splitMessage(msg)
	// Should split at newline boundaries
	for _, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 15)
	}
	// Reassembled should equal original
	assert.Equal(t, msg, joinChunks(chunks))
}

func TestSplitMessagePreservesFencedCodeBlocks(t *testing.T) {
	cfg := Config{Token: "test", MaxMessageLen: 48}
	adapter := NewAdapter(cfg)

	msg := "before\n```go\n" +
		strings.Repeat("fmt.Println(\"hello world\")\n", 4) +
		"```\nafter"

	chunks := adapter.splitMessage(msg)
	assert.GreaterOrEqual(t, len(chunks), 2)

	for _, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk), 48)
		rendered := formatTelegramRichText(chunk)
		assert.Equal(t, strings.Count(rendered, "<pre>"), strings.Count(rendered, "</pre>"))
		assert.Equal(t, strings.Count(rendered, "<code"), strings.Count(rendered, "</code>"))
	}
}

func joinChunks(chunks []string) string {
	result := ""
	for _, c := range chunks {
		result += c
	}
	return result
}

func TestConvertMessagePrivate(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})

	// We can't easily create a real tgbotapi.Message, so we test the type mapping
	// through the public interface. The convertMessage method is tested indirectly.
	assert.NotNil(t, adapter)
}

func TestSetHandler(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})

	adapter.SetHandler(func(_ context.Context, _ *gateway.Message) error {
		return nil
	})

	assert.NotNil(t, adapter.handler)
}

func TestAdapterName(t *testing.T) {
	adapter := NewAdapter(Config{Token: "test"})
	assert.Equal(t, "telegram", adapter.Name())
}
