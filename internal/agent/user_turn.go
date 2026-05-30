package agent

import (
	"fmt"
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

	parts := append([]provider.ContentPart(nil), msg.ContentParts...)
	if len(parts) == 0 && routingText != "" {
		parts = append(parts, provider.ContentPart{
			Type: "text",
			Text: routingText,
		})
	}
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
	}
}

/**
 * WithRoutingText 重写路由文本并保持消息载荷一致
 */
func (in UserTurnInput) WithRoutingText(text string) UserTurnInput {
	in.RoutingText = strings.TrimSpace(text)
	return in.Normalize()
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

func attachmentFromContentPart(part provider.ContentPart) (gateway.Attachment, bool) {
	if part.Type != "image" || part.Image == nil {
		return gateway.Attachment{}, false
	}
	return gateway.Attachment{
		Type:     gateway.AttachmentImage,
		FilePath: strings.TrimSpace(part.Image.FilePath),
		MimeType: strings.TrimSpace(part.Image.MimeType),
	}, true
}

func contentPartFromAttachment(att gateway.Attachment) (provider.ContentPart, bool) {
	if att.Type != gateway.AttachmentImage {
		return provider.ContentPart{}, false
	}
	return provider.ContentPart{
		Type: "image",
		Image: &provider.ImagePart{
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
