package agent

import (
	"encoding/base64"
	"testing"

	"github.com/yurika0211/luckyagent/internal/gateway"
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

func TestNormalizeImageAttachmentSources(t *testing.T) {
	imageData := []byte("\x89PNG\r\n\x1a\nimage-bytes")
	for _, tc := range []struct {
		name string
		att  gateway.Attachment
		url  string
		path string
	}{
		{name: "inline bytes", att: gateway.Attachment{Data: imageData}, url: "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageData)},
		{name: "local file", att: gateway.Attachment{FilePath: "/tmp/photo.png"}, path: "/tmp/photo.png"},
		{name: "remote URL", att: gateway.Attachment{FileURL: "https://example.test/photo.png"}, url: "https://example.test/photo.png"},
		{name: "bytes override stale path and URL", att: gateway.Attachment{Data: imageData, FilePath: "/missing.png", FileURL: "https://example.test/old.png"}, url: "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageData)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.att.Type = gateway.AttachmentImage
			input := MultimodalUserTurnInput("caption", []gateway.Attachment{tc.att})
			// Scope and routing updates normalize the same turn repeatedly.
			for i := 0; i < 3; i++ {
				input = input.Normalize()
				if len(input.Message.ContentParts) != 2 {
					t.Fatalf("expected caption plus one image, got %+v", input.Message.ContentParts)
				}
				img := input.Message.ContentParts[1].Image
				if img == nil || img.URL != tc.url || img.FilePath != tc.path {
					t.Fatalf("image payload = %+v, want url=%s path=%s", img, tc.url, tc.path)
				}
			}
		})
	}
}

func TestNormalizeUnavailableImageDoesNotSendEmptyPayload(t *testing.T) {
	input := MultimodalUserTurnInput("describe it", []gateway.Attachment{{
		Type: gateway.AttachmentImage, FileID: "not-downloaded", FileName: "missing.png",
	}}).Normalize()
	if countImageParts([]provider.Message{input.Message}) != 0 {
		t.Fatal("unavailable image must not create an invalid provider payload")
	}
	if len(input.Attachments) != 1 {
		t.Fatal("unavailable attachment must remain visible to the evidence manifest")
	}
}
