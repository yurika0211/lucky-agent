package gateway

import (
	"context"
	"time"
)

// ChatType represents the type of chat.
type ChatType int

const (
	ChatPrivate ChatType = iota
	ChatGroup
	ChatSuperGroup
	ChatChannel
)

// String returns a human-readable name for the ChatType.
func (ct ChatType) String() string {
	switch ct {
	case ChatPrivate:
		return "private"
	case ChatGroup:
		return "group"
	case ChatSuperGroup:
		return "supergroup"
	case ChatChannel:
		return "channel"
	default:
		return "unknown"
	}
}

// Chat represents a chat conversation.
type Chat struct {
	ID       string
	Type     ChatType
	Title    string
	Username string
}

// User represents a messaging platform user.
type User struct {
	ID        string
	Username  string
	FirstName string
	LastName  string
}

// DisplayName returns the best available display name for the user.
func (u User) DisplayName() string {
	if u.Username != "" {
		return "@" + u.Username
	}
	if u.FirstName != "" {
		if u.LastName != "" {
			return u.FirstName + " " + u.LastName
		}
		return u.FirstName
	}
	return u.ID
}

// Message represents an incoming message from a messaging platform.
type Message struct {
	ID        string
	Chat      Chat
	Sender    User
	Text      string
	ReplyTo   *Message // if this is a reply
	Timestamp time.Time
	IsCommand bool
	Command   string // e.g., "/start"
	Args      string // everything after the command

	// v0.36.0: 多媒体附件
	Attachments []Attachment

	// v0.44.0: 群聊触发信息
	IsGroupTrigger bool   // 是否在群聊中被触发（@或回复bot）
	TriggerType    string // 触发方式: "mention" | "reply"
}

// Attachment represents a media attachment in a message.
type Attachment struct {
	Type     AttachmentType // image, audio, video, document
	FileID   string         // platform-specific file ID
	FileURL  string         // download URL (if available)
	FilePath string         // downloaded local file path (if available)
	FileName string         // original filename
	MimeType string         // MIME type
	FileSize int64          // file size in bytes
	Data     []byte         // downloaded file data (populated on demand)
}

// AttachmentType represents the type of media attachment.
type AttachmentType string

const (
	AttachmentImage    AttachmentType = "image"
	AttachmentAudio    AttachmentType = "audio"
	AttachmentVideo    AttachmentType = "video"
	AttachmentDocument AttachmentType = "document"
)

// MessageHandler is the callback type for handling incoming messages.
type MessageHandler func(ctx context.Context, msg *Message) error
