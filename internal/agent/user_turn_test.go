package agent

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestTextUserTurnInputKeepsPlainText(t *testing.T) {
	input := TextUserTurnInput("hello").Normalize()
	if len(input.Message.ContentParts) != 0 {
		t.Fatalf("plain text should not have content parts: %#v", input.Message.ContentParts)
	}
	if input.Message.Content != "hello" {
		t.Fatalf("content = %q, want hello", input.Message.Content)
	}
}

func TestNormalizePreservesStructuredTextAndImageParts(t *testing.T) {
	input := UserTurnInput{
		Message: provider.Message{
			Role:    "user",
			Content: "caption",
			ContentParts: []provider.ContentPart{{
				Type:  "image",
				Image: &provider.ImagePart{URL: "https://example.com/image.png"},
			}},
		},
		RoutingText: "caption",
	}.Normalize()
	if len(input.Message.ContentParts) != 2 {
		t.Fatalf("expected text and image parts, got %#v", input.Message.ContentParts)
	}
	if input.Message.ContentParts[0].Type != "text" || input.Message.ContentParts[0].Text != "caption" {
		t.Fatalf("unexpected text part: %#v", input.Message.ContentParts[0])
	}
	if input.Message.ContentParts[1].Type != "image" {
		t.Fatalf("unexpected image part: %#v", input.Message.ContentParts[1])
	}
}
