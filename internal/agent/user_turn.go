package agent

import (
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/yurika0211/luckyharness/internal/gateway"
	"github.com/yurika0211/luckyharness/internal/provider"
)

/**
 * UserTurnInput 将路由文本与结构化用户消息载荷分开
 */
type UserTurnInput struct {
	Message     provider.Message
	RoutingText string
	Attachments []gateway.Attachment
	Scope       TurnScope
}

// TurnScope identifies the messaging scope for a user turn. It is intentionally
// stored as tags on memory entries so older memory notes remain compatible.
type TurnScope struct {
	Platform    string
	ChatID      string
	ChatType    string
	SenderID    string
	SenderName  string
	DisplayName string
}

/**
 * TextUserTurnInput 构建仅包含文本的用户轮次输入
 */
func TextUserTurnInput(text string) UserTurnInput {
	text = strings.TrimSpace(text)
	return UserTurnInput{
		RoutingText: text,
		Message: provider.Message{
			Role:    "user",
			Content: text,
		},
	}
}

// MultimodalUserTurnInput builds a user turn that includes attachments.
func MultimodalUserTurnInput(text string, attachments []gateway.Attachment) UserTurnInput {
	text = strings.TrimSpace(text)
	msg := provider.Message{
		Role:    "user",
		Content: text,
	}
	return UserTurnInput{
		Message:     msg,
		RoutingText: text,
		Attachments: append([]gateway.Attachment(nil), attachments...),
	}
}

// WithScope returns a copy of the input bound to a messaging scope.
func (in UserTurnInput) WithScope(scope TurnScope) UserTurnInput {
	in.Scope = scope.Normalize()
	return in.Normalize()
}

/**
 * Normalize 填充 agent loop 和 provider 所需的最小字段
 */
func (in UserTurnInput) Normalize() UserTurnInput {
	msg := in.Message
	if strings.TrimSpace(msg.Role) == "" {
		msg.Role = "user"
	}

	routingText := strings.TrimSpace(in.RoutingText)
	if routingText == "" {
		routingText = deriveRoutingTextFromMessage(msg)
	}
	if routingText == "" {
		if len(in.Attachments) > 0 {
			routingText = attachmentRoutingText(in.Attachments)
		}
	}
	if routingText == "" {
		routingText = "User sent a multimodal message. Use the provided attachment(s) to answer."
	}
	msg.Content = routingText

	parts := contentPartsWithRoutingText(msg.ContentParts, routingText)
	parts = filterAttachmentContentParts(parts, in.Attachments)
	for _, att := range in.Attachments {
		if part, ok := contentPartFromAttachment(att); ok {
			parts = append(parts, part)
		}
	}
	msg.ContentParts = parts

	return UserTurnInput{
		Message:     msg,
		RoutingText: routingText,
		Attachments: append([]gateway.Attachment(nil), in.Attachments...),
		Scope:       in.Scope.Normalize(),
	}
}

// Normalize returns a stable, lowercase scope for matching memory tags.
func (s TurnScope) Normalize() TurnScope {
	s.Platform = strings.ToLower(strings.TrimSpace(s.Platform))
	s.ChatID = strings.TrimSpace(s.ChatID)
	s.ChatType = strings.ToLower(strings.TrimSpace(s.ChatType))
	s.SenderID = strings.TrimSpace(s.SenderID)
	s.SenderName = strings.TrimSpace(s.SenderName)
	s.DisplayName = strings.TrimSpace(s.DisplayName)
	return s
}

// HasSender reports whether this turn can be scoped to a concrete platform user.
func (s TurnScope) HasSender() bool {
	s = s.Normalize()
	return s.Platform != "" && s.SenderID != ""
}

// IsGroup reports whether the turn came from a multi-user chat.
func (s TurnScope) IsGroup() bool {
	switch s.Normalize().ChatType {
	case "group", "supergroup", "channel":
		return true
	default:
		return false
	}
}

// UserTag is the stable memory tag for this sender.
func (s TurnScope) UserTag() string {
	s = s.Normalize()
	if !s.HasSender() {
		return ""
	}
	return "scope:user:" + s.Platform + ":" + hashScopeValue(s.SenderID)
}

// PrivateTag is the tag used for personal memory in private chats.
func (s TurnScope) PrivateTag() string {
	s = s.Normalize()
	if !s.HasSender() {
		return ""
	}
	return "scope:private:" + s.Platform + ":" + hashScopeValue(s.SenderID)
}

// GroupTag is the tag used for shared group memory.
func (s TurnScope) GroupTag() string {
	s = s.Normalize()
	if s.Platform == "" || s.ChatID == "" || !s.IsGroup() {
		return ""
	}
	return "scope:group:" + s.Platform + ":" + hashScopeValue(s.ChatID)
}

// MemoryTags returns tags for new memories created from this turn.
func (s TurnScope) MemoryTags() []string {
	s = s.Normalize()
	if !s.HasSender() {
		return nil
	}
	tags := []string{"scope:personal", s.UserTag()}
	if s.IsGroup() {
		tags = append(tags, s.GroupTag())
	} else {
		tags = append(tags, s.PrivateTag())
	}
	return filterNonEmptyStrings(tags)
}

func hashScopeValue(value string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("%x", h.Sum64())
}

func filterNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

/**
 * WithRoutingText 重写路由文本并保持消息载荷一致
 */
func (in UserTurnInput) WithRoutingText(text string) UserTurnInput {
	in.RoutingText = strings.TrimSpace(text)
	return in.Normalize()
}

func contentPartsWithRoutingText(parts []provider.ContentPart, routingText string) []provider.ContentPart {
	out := append([]provider.ContentPart(nil), parts...)
	routingText = strings.TrimSpace(routingText)
	if routingText == "" {
		return out
	}
	for i := range out {
		if out[i].Type == "text" {
			out[i].Text = routingText
			return out
		}
	}
	return append([]provider.ContentPart{{
		Type: "text",
		Text: routingText,
	}}, out...)
}

func filterAttachmentContentParts(parts []provider.ContentPart, attachments []gateway.Attachment) []provider.ContentPart {
	if len(parts) == 0 || len(attachments) == 0 {
		return parts
	}
	out := parts[:0]
	for _, part := range parts {
		if contentPartMatchesAttachment(part, attachments) {
			continue
		}
		out = append(out, part)
	}
	return out
}

func contentPartMatchesAttachment(part provider.ContentPart, attachments []gateway.Attachment) bool {
	for _, att := range attachments {
		attPart, ok := contentPartFromAttachment(att)
		if !ok {
			continue
		}
		if sameContentPart(part, attPart) {
			return true
		}
	}
	return false
}

func sameContentPart(a, b provider.ContentPart) bool {
	if a.Type != b.Type {
		return false
	}
	switch a.Type {
	case "text":
		return strings.TrimSpace(a.Text) == strings.TrimSpace(b.Text)
	case "image":
		if a.Image == nil || b.Image == nil {
			return a.Image == nil && b.Image == nil
		}
		return strings.TrimSpace(a.Image.URL) == strings.TrimSpace(b.Image.URL) &&
			strings.TrimSpace(a.Image.FilePath) == strings.TrimSpace(b.Image.FilePath) &&
			strings.TrimSpace(a.Image.MimeType) == strings.TrimSpace(b.Image.MimeType) &&
			strings.TrimSpace(a.Image.Detail) == strings.TrimSpace(b.Image.Detail)
	default:
		return a == b
	}
}

func deriveRoutingTextFromMessage(msg provider.Message) string {
	if text := strings.TrimSpace(msg.Content); text != "" {
		return text
	}
	if len(msg.ContentParts) == 0 {
		return ""
	}

	textParts := make([]string, 0, len(msg.ContentParts))
	imageCount := 0
	for _, part := range msg.ContentParts {
		switch part.Type {
		case "text":
			if text := strings.TrimSpace(part.Text); text != "" {
				textParts = append(textParts, text)
			}
		case "image":
			imageCount++
		}
	}
	if len(textParts) > 0 {
		return strings.Join(textParts, "\n")
	}
	if imageCount > 0 {
		if imageCount == 1 {
			return "User sent an image. Use the image to answer."
		}
		return fmt.Sprintf("User sent %d images. Use the images to answer.", imageCount)
	}
	return ""
}

func contentPartFromAttachment(att gateway.Attachment) (provider.ContentPart, bool) {
	if att.Type != gateway.AttachmentImage {
		return provider.ContentPart{}, false
	}
	return provider.ContentPart{
		Type: "image",
		Image: &provider.ImagePart{
			URL:      strings.TrimSpace(att.FileURL),
			FilePath: strings.TrimSpace(att.FilePath),
			MimeType: strings.TrimSpace(att.MimeType),
		},
	}, true
}

func attachmentRoutingText(attachments []gateway.Attachment) string {
	if len(attachments) == 0 {
		return ""
	}
	imageCount := 0
	docCount := 0
	for _, att := range attachments {
		switch att.Type {
		case gateway.AttachmentImage:
			imageCount++
		default:
			docCount++
		}
	}
	parts := make([]string, 0, 2)
	if imageCount > 0 {
		if imageCount == 1 {
			parts = append(parts, "1 image attached")
		} else {
			parts = append(parts, fmt.Sprintf("%d images attached", imageCount))
		}
	}
	if docCount > 0 {
		if docCount == 1 {
			parts = append(parts, "1 file attached")
		} else {
			parts = append(parts, fmt.Sprintf("%d files attached", docCount))
		}
	}
	return "User sent " + strings.Join(parts, " and ") + ". Use the attachments to answer."
}
