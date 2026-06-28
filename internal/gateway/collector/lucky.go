package collector

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/gateway"
)

type LuckyAction string

const (
	LuckyActionUnknown LuckyAction = ""
	LuckyActionOn      LuckyAction = "on"
	LuckyActionOff     LuckyAction = "off"
	LuckyActionStatus  LuckyAction = "status"
	LuckyActionCancel  LuckyAction = "cancel"
)

var (
	ErrInvalidKey    = errors.New("lucky collector key is empty")
	ErrAlreadyActive = errors.New("lucky collector is already active")
	ErrInactive      = errors.New("lucky collector is not active")
	ErrEmptyBatch    = errors.New("lucky collector batch is empty")
)

type Lucky struct {
	mu      sync.Mutex
	buffers map[string]*luckyBuffer
	now     func() time.Time
}

type luckyBuffer struct {
	startedAt time.Time
	updatedAt time.Time
	segments  []LuckySegment
}

type LuckyStatus struct {
	Active          bool
	StartedAt       time.Time
	UpdatedAt       time.Time
	SegmentCount    int
	AttachmentCount int
	LastMessageID   string
}

type LuckyBatch struct {
	Key        string
	StartedAt  time.Time
	FinishedAt time.Time
	Segments   []LuckySegment
}

type LuckySegment struct {
	MessageID   string
	Chat        gateway.Chat
	Sender      gateway.User
	Text        string
	Timestamp   time.Time
	ReplyToID   string
	ReplyToText string
	Attachments []gateway.Attachment
}

func NewLucky() *Lucky {
	return NewLuckyWithClock(time.Now)
}

func NewLuckyWithClock(now func() time.Time) *Lucky {
	if now == nil {
		now = time.Now
	}
	return &Lucky{
		buffers: make(map[string]*luckyBuffer),
		now:     now,
	}
}

func ParseLuckyAction(args string) LuckyAction {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(args)))
	if len(fields) == 0 {
		return LuckyActionStatus
	}
	switch fields[0] {
	case "on", "start", "begin":
		return LuckyActionOn
	case "off", "done", "finish", "send":
		return LuckyActionOff
	case "status", "state", "ls", "list":
		return LuckyActionStatus
	case "cancel", "clear", "reset", "discard":
		return LuckyActionCancel
	default:
		return LuckyActionUnknown
	}
}

func KeyForMessage(platform string, msg *gateway.Message) string {
	platform = strings.TrimSpace(platform)
	if platform == "" {
		platform = "gateway"
	}
	chatID := "unknown-chat"
	if msg != nil && strings.TrimSpace(msg.Chat.ID) != "" {
		chatID = strings.TrimSpace(msg.Chat.ID)
	}
	key := platform + "|chat:" + chatID
	if msg == nil {
		return key
	}
	if msg.Chat.Type == gateway.ChatGroup || msg.Chat.Type == gateway.ChatSuperGroup {
		senderID := strings.TrimSpace(msg.Sender.ID)
		if senderID == "" {
			senderID = strings.TrimSpace(msg.Sender.DisplayName())
		}
		if senderID == "" {
			senderID = "unknown-user"
		}
		key += "|sender:" + senderID
	}
	return key
}

func (l *Lucky) Start(key string) (LuckyStatus, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return LuckyStatus{}, ErrInvalidKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buffers == nil {
		l.buffers = make(map[string]*luckyBuffer)
	}
	if buf := l.buffers[key]; buf != nil {
		return statusFromBuffer(buf), ErrAlreadyActive
	}
	now := l.now()
	buf := &luckyBuffer{
		startedAt: now,
		updatedAt: now,
	}
	l.buffers[key] = buf
	return statusFromBuffer(buf), nil
}

func (l *Lucky) Append(key string, msg *gateway.Message) (LuckyStatus, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return LuckyStatus{}, ErrInvalidKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	buf := l.buffers[key]
	if buf == nil {
		return LuckyStatus{}, ErrInactive
	}
	segment := segmentFromMessage(msg)
	buf.segments = append(buf.segments, segment)
	if !segment.Timestamp.IsZero() {
		buf.updatedAt = segment.Timestamp
	} else {
		buf.updatedAt = l.now()
	}
	return statusFromBuffer(buf), nil
}

func (l *Lucky) Finish(key string) (LuckyBatch, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return LuckyBatch{}, ErrInvalidKey
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	buf := l.buffers[key]
	if buf == nil {
		return LuckyBatch{}, ErrInactive
	}
	delete(l.buffers, key)
	if len(buf.segments) == 0 {
		return LuckyBatch{
			Key:        key,
			StartedAt:  buf.startedAt,
			FinishedAt: l.now(),
		}, ErrEmptyBatch
	}
	return LuckyBatch{
		Key:        key,
		StartedAt:  buf.startedAt,
		FinishedAt: l.now(),
		Segments:   append([]LuckySegment(nil), buf.segments...),
	}, nil
}

func (l *Lucky) Cancel(key string) (LuckyStatus, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return LuckyStatus{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	buf := l.buffers[key]
	if buf == nil {
		return LuckyStatus{}, false
	}
	delete(l.buffers, key)
	return statusFromBuffer(buf), true
}

func (l *Lucky) Status(key string) LuckyStatus {
	key = strings.TrimSpace(key)
	if key == "" {
		return LuckyStatus{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return statusFromBuffer(l.buffers[key])
}

func (b LuckyBatch) Text() string {
	var sb strings.Builder
	sb.WriteString("[Collected gateway message batch]\n")
	if !b.StartedAt.IsZero() {
		sb.WriteString("Started at: ")
		sb.WriteString(b.StartedAt.Format(time.RFC3339))
		sb.WriteString("\n")
	}
	if !b.FinishedAt.IsZero() {
		sb.WriteString("Finished at: ")
		sb.WriteString(b.FinishedAt.Format(time.RFC3339))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	for i, seg := range b.Segments {
		sb.WriteString(fmt.Sprintf("Segment %d\n", i+1))
		if sender := strings.TrimSpace(seg.Sender.DisplayName()); sender != "" {
			sb.WriteString("Sender: ")
			sb.WriteString(sender)
			sb.WriteString("\n")
		}
		if !seg.Timestamp.IsZero() {
			sb.WriteString("Time: ")
			sb.WriteString(seg.Timestamp.Format(time.RFC3339))
			sb.WriteString("\n")
		}
		if strings.TrimSpace(seg.MessageID) != "" {
			sb.WriteString("Message ID: ")
			sb.WriteString(strings.TrimSpace(seg.MessageID))
			sb.WriteString("\n")
		}
		if strings.TrimSpace(seg.ReplyToText) != "" {
			sb.WriteString("Reply context")
			if strings.TrimSpace(seg.ReplyToID) != "" {
				sb.WriteString(" (message_id=")
				sb.WriteString(strings.TrimSpace(seg.ReplyToID))
				sb.WriteString(")")
			}
			sb.WriteString(":\n")
			sb.WriteString(strings.TrimSpace(seg.ReplyToText))
			sb.WriteString("\n")
		}
		sb.WriteString("Text:\n")
		text := strings.TrimSpace(seg.Text)
		if text == "" {
			text = "(no text)"
		}
		sb.WriteString(text)
		sb.WriteString("\n")
		if len(seg.Attachments) > 0 {
			sb.WriteString("Attachments:\n")
			for j, att := range seg.Attachments {
				sb.WriteString("- ")
				sb.WriteString(describeAttachment(att, j+1))
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}
	sb.WriteString("[User intent]\n")
	sb.WriteString("Treat all collected segments above as one complete user request. Preserve the original order and message boundaries. Attachments belong to the segment where they appeared.")
	return strings.TrimSpace(sb.String())
}

func (b LuckyBatch) Attachments() []gateway.Attachment {
	var attachments []gateway.Attachment
	for _, seg := range b.Segments {
		attachments = append(attachments, seg.Attachments...)
	}
	return append([]gateway.Attachment(nil), attachments...)
}

func (b LuckyBatch) UserTurnInput() agent.UserTurnInput {
	return agent.MultimodalUserTurnInput(b.Text(), b.Attachments())
}

func statusFromBuffer(buf *luckyBuffer) LuckyStatus {
	if buf == nil {
		return LuckyStatus{}
	}
	status := LuckyStatus{
		Active:       true,
		StartedAt:    buf.startedAt,
		UpdatedAt:    buf.updatedAt,
		SegmentCount: len(buf.segments),
	}
	if len(buf.segments) > 0 {
		status.LastMessageID = buf.segments[len(buf.segments)-1].MessageID
	}
	for _, seg := range buf.segments {
		status.AttachmentCount += len(seg.Attachments)
	}
	return status
}

func segmentFromMessage(msg *gateway.Message) LuckySegment {
	if msg == nil {
		return LuckySegment{}
	}
	seg := LuckySegment{
		MessageID:   strings.TrimSpace(msg.ID),
		Chat:        msg.Chat,
		Sender:      msg.Sender,
		Text:        strings.TrimSpace(msg.Text),
		Timestamp:   msg.Timestamp,
		Attachments: append([]gateway.Attachment(nil), msg.Attachments...),
	}
	if msg.ReplyTo != nil {
		seg.ReplyToID = strings.TrimSpace(msg.ReplyTo.ID)
		seg.ReplyToText = strings.TrimSpace(msg.ReplyTo.Text)
	}
	return seg
}

func describeAttachment(att gateway.Attachment, index int) string {
	parts := []string{fmt.Sprintf("%s %d", strings.TrimSpace(string(att.Type)), index)}
	if strings.TrimSpace(string(att.Type)) == "" {
		parts[0] = fmt.Sprintf("attachment %d", index)
	}
	if strings.TrimSpace(att.FileName) != "" {
		parts = append(parts, "name="+strings.TrimSpace(att.FileName))
	}
	if strings.TrimSpace(att.MimeType) != "" {
		parts = append(parts, "mime="+strings.TrimSpace(att.MimeType))
	}
	if strings.TrimSpace(att.FilePath) != "" {
		parts = append(parts, "local_path="+strings.TrimSpace(att.FilePath))
	}
	if strings.TrimSpace(att.FileURL) != "" {
		parts = append(parts, "url="+strings.TrimSpace(att.FileURL))
	}
	if att.FileSize > 0 {
		parts = append(parts, fmt.Sprintf("size=%d", att.FileSize))
	}
	return strings.Join(parts, ", ")
}
