package agent

import (
	"fmt"
	"strings"

	"github.com/yurika0211/luckyharness/internal/provider"
)

/**
 * UserTurnInput 将路由文本与结构化用户消息载荷分开
 */
type UserTurnInput struct {
	Message     provider.Message
	RoutingText string
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
