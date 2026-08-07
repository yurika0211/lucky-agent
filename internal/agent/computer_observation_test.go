package agent

import (
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/tool"
)

func TestAppendLatestComputerObservationKeepsOnlyNewestFrame(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "request"},
		{Role: "user", Content: "[Computer Observation old]", ContentParts: []provider.ContentPart{{Type: "image", Image: &provider.ImagePart{FilePath: "/tmp/old.png"}}}},
	}
	executed := []executedToolCall{{
		ToolCall: provider.ToolCall{Name: "computer_act"},
		Observations: []tool.Observation{
			{FrameID: "frame-1", FilePath: "/tmp/frame-1.png", MimeType: "image/png"},
			{FrameID: "frame-2", FilePath: "/tmp/frame-2.png", MimeType: "image/png"},
		},
	}}

	got := appendLatestComputerObservation(messages, executed)
	var observations []provider.Message
	for _, msg := range got {
		if isTransientComputerObservation(msg) {
			observations = append(observations, msg)
		}
	}
	if len(observations) != 1 {
		t.Fatalf("expected one transient observation, got %d: %#v", len(observations), got)
	}
	if observations[0].Content != "[Computer Observation frame-2]" {
		t.Fatalf("expected newest frame label, got %q", observations[0].Content)
	}
	if observations[0].ContentParts[0].Image.FilePath != "/tmp/frame-2.png" {
		t.Fatalf("expected newest frame path, got %#v", observations[0].ContentParts)
	}
}

func TestRemoveTransientComputerObservationsPreservesUserImages(t *testing.T) {
	messages := []provider.Message{
		{Role: "user", Content: "[Computer Observation frame-1]", ContentParts: []provider.ContentPart{{Type: "image", Image: &provider.ImagePart{FilePath: "/tmp/frame.png"}}}},
		{Role: "user", Content: "uploaded image", ContentParts: []provider.ContentPart{{Type: "image", Image: &provider.ImagePart{FilePath: "/tmp/upload.png"}}}},
	}
	got := removeTransientComputerObservations(messages)
	if len(got) != 1 || got[0].Content != "uploaded image" {
		t.Fatalf("unexpected retained messages: %#v", got)
	}
}
