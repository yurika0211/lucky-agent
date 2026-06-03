package gateway

import "context"

// Gateway is the interface that all messaging platform adapters must implement.
type Gateway interface {
	// Name returns the platform name (e.g., "telegram", "discord")
	Name() string

	// Start starts the gateway connection
	Start(ctx context.Context) error

	// Stop gracefully shuts down the gateway
	Stop() error

	// Send sends a message to a chat
	Send(ctx context.Context, chatID string, message string) error

	// SendWithReply sends a message as a reply to a specific message
	SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error

	// IsRunning returns whether the gateway is currently connected
	IsRunning() bool
}

// SentMessage describes a message emitted by a gateway.
type SentMessage struct {
	ID     string
	ChatID string
}

// ReceiptGateway extends Gateway with message receipts for reply chaining.
// Callers should type-assert this interface only when they need the outbound
// platform message ID; plain Gateway implementations remain valid.
type ReceiptGateway interface {
	Gateway

	SendWithReceipt(ctx context.Context, chatID string, message string) (SentMessage, error)
	SendWithReplyReceipt(ctx context.Context, chatID string, replyToMsgID string, message string) (SentMessage, error)
}

// StreamGateway extends Gateway with streaming message support.
// Adapters that support real-time message editing (like Telegram) should implement this.
type StreamGateway interface {
	Gateway

	// SendStream sends a message and returns a StreamSender for live updates.
	// The caller writes chunks to the sender; the adapter handles rendering.
	SendStream(ctx context.Context, chatID string, replyToMsgID string) (StreamSender, error)
}

// StreamSender allows incremental message updates for streaming output.
type StreamSender interface {
	// Append adds content to the current message and updates the platform.
	Append(content string) error

	// SetThinking shows a "thinking" indicator (e.g., 🧠 Thinking...).
	SetThinking(label string) error

	// SetToolCall shows a tool call indicator (e.g., 🔧 Calling shell...).
	SetToolCall(name, args string) error

	// SetResult replaces the thinking/tool-call indicator with the final result.
	SetResult(content string) error

	// Finish finalizes the message. No more updates allowed.
	Finish() error

	// MessageID returns the platform message ID (for reply chaining).
	MessageID() string
}
