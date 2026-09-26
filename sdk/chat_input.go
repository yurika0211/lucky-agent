package sdk

import (
	"context"
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/agent"
	"github.com/yurika0211/luckyagent/internal/gateway"
)

// AttachmentType classifies a multimodal host attachment.
type AttachmentType string

const (
	AttachmentImage    AttachmentType = "image"
	AttachmentAudio    AttachmentType = "audio"
	AttachmentVideo    AttachmentType = "video"
	AttachmentDocument AttachmentType = "document"
)

// Attachment is host-supplied multimodal content for one user turn.
//
// Prefer FilePath or Data for local embeds. FileURL is passed through when the
// runtime/provider can fetch it. Only image attachments become model vision
// parts today; other types are still attached for routing/media analysis.
type Attachment struct {
	Type     AttachmentType
	FilePath string
	FileURL  string
	FileName string
	MimeType string
	FileSize int64
	Data     []byte
}

// ChatInput is a single user turn that may include attachments.
type ChatInput struct {
	Message     string
	Attachments []Attachment
}

func (in ChatInput) validate() error {
	msg := strings.TrimSpace(in.Message)
	if msg == "" && len(in.Attachments) == 0 {
		return fmt.Errorf("sdk: empty message")
	}
	for i, att := range in.Attachments {
		if strings.TrimSpace(string(att.Type)) == "" {
			return fmt.Errorf("sdk: attachment[%d] type is empty", i)
		}
		hasBody := strings.TrimSpace(att.FilePath) != "" ||
			strings.TrimSpace(att.FileURL) != "" ||
			len(att.Data) > 0
		if !hasBody {
			return fmt.Errorf("sdk: attachment[%d] needs FilePath, FileURL, or Data", i)
		}
	}
	return nil
}

func (in ChatInput) toTurn() agent.UserTurnInput {
	atts := make([]gateway.Attachment, 0, len(in.Attachments))
	for _, att := range in.Attachments {
		meta := gateway.Attachment{
			Type:     gateway.AttachmentType(strings.TrimSpace(string(att.Type))),
			FilePath: strings.TrimSpace(att.FilePath),
			FileURL:  strings.TrimSpace(att.FileURL),
			FileName: strings.TrimSpace(att.FileName),
			MimeType: strings.TrimSpace(att.MimeType),
			FileSize: att.FileSize,
		}
		if len(att.Data) > 0 {
			meta.Data = append([]byte(nil), att.Data...)
		}
		atts = append(atts, meta)
	}
	return agent.MultimodalUserTurnInput(in.Message, atts)
}

// ChatWithInput starts a fresh session with an optional multimodal turn.
func (a *Agent) ChatWithInput(ctx context.Context, input ChatInput) (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	if err := input.validate(); err != nil {
		return "", err
	}
	sess := a.inner.Sessions().New()
	if sess == nil {
		return "", fmt.Errorf("sdk: create session failed")
	}
	return a.inner.ChatWithSessionInput(ctx, sess.ID, input.toTurn())
}

// ChatSessionWithInput continues a session with an optional multimodal turn.
func (a *Agent) ChatSessionWithInput(ctx context.Context, sessionID string, input ChatInput) (string, error) {
	if err := a.require(); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", fmt.Errorf("sdk: empty session id")
	}
	if err := input.validate(); err != nil {
		return "", err
	}
	return a.inner.ChatWithSessionInput(ctx, sessionID, input.toTurn())
}

// ChatStreamWithInput starts a fresh session and streams a multimodal turn.
func (a *Agent) ChatStreamWithInput(ctx context.Context, input ChatInput) (sessionID string, events <-chan Event, err error) {
	if err := a.require(); err != nil {
		return "", nil, err
	}
	if err := input.validate(); err != nil {
		return "", nil, err
	}
	sess := a.inner.Sessions().New()
	if sess == nil {
		return "", nil, fmt.Errorf("sdk: create session failed")
	}
	ch, err := a.inner.ChatWithSessionStreamInput(ctx, sess.ID, input.toTurn())
	if err != nil {
		return "", nil, err
	}
	return sess.ID, mapEvents(ch), nil
}

// ChatSessionStreamWithInput continues a session and streams a multimodal turn.
func (a *Agent) ChatSessionStreamWithInput(ctx context.Context, sessionID string, input ChatInput) (<-chan Event, error) {
	if err := a.require(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("sdk: empty session id")
	}
	if err := input.validate(); err != nil {
		return nil, err
	}
	ch, err := a.inner.ChatWithSessionStreamInput(ctx, sessionID, input.toTurn())
	if err != nil {
		return nil, err
	}
	return mapEvents(ch), nil
}
