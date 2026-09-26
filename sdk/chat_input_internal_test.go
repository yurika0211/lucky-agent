package sdk

import (
	"strings"
	"testing"
)

func TestChatInputToTurnBuildsImageParts(t *testing.T) {
	in := ChatInput{
		Message: "describe",
		Attachments: []Attachment{{
			Type:     AttachmentImage,
			MimeType: "image/png",
			Data:     []byte{0x89, 0x50, 0x4e, 0x47},
		}},
	}
	if err := in.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	turn := in.toTurn().Normalize()
	if turn.RoutingText == "" {
		t.Fatal("expected routing text")
	}
	if len(turn.Attachments) != 1 {
		t.Fatalf("attachments=%d", len(turn.Attachments))
	}
	foundImage := false
	for _, part := range turn.Message.ContentParts {
		if part.Type == "image" && part.Image != nil {
			foundImage = true
			if part.Image.URL == "" && part.Image.FilePath == "" {
				t.Fatalf("image part empty: %+v", part.Image)
			}
			if part.Image.URL != "" && !strings.HasPrefix(part.Image.URL, "data:image/png;base64,") {
				t.Fatalf("unexpected data url: %s", part.Image.URL)
			}
		}
	}
	if !foundImage {
		t.Fatalf("expected image content part, parts=%+v", turn.Message.ContentParts)
	}
}
