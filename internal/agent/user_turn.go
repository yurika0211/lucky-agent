package agent

import (
	"fmt"
	"strings"

	"github.com/yurika0211/luckyharness/internal/provider"
)

// UserTurnInput separates routing text from the structured user message payload.
type UserTurnInput struct {
	Message     provider.Message
	RoutingText string
}

// TextUserTurnInput builds a text-only user turn input.
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

// Normalize fills the minimum fields required by the agent loop and providers.
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
		routingText = "User sent a multimodal message. Use the provided attachment(s) to answer."
	}
	msg.Content = routingText

	if len(msg.ContentParts) > 0 {
		parts := append([]provider.ContentPart(nil), msg.ContentParts...)
		hasTextPart := false
		for i := range parts {
			if parts[i].Type == "text" {
				hasTextPart = true
				if i == 0 {
					parts[i].Text = routingText
				}
				break
			}
		}
		if !hasTextPart {
			parts = append([]provider.ContentPart{{
				Type: "text",
				Text: routingText,
			}}, parts...)
		}
		msg.ContentParts = parts
	}

	return UserTurnInput{
		Message:     msg,
		RoutingText: routingText,
	}
}

// WithRoutingText rewrites the routing text and keeps the message payload aligned.
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
