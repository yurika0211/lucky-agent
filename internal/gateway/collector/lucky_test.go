package collector

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestLuckyCollectsSegmentsIntoUserTurn(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	lucky := NewLuckyWithClock(func() time.Time { return now })

	status, err := lucky.Start("telegram|chat:123")
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if !status.Active || status.SegmentCount != 0 {
		t.Fatalf("unexpected start status: %+v", status)
	}

	_, err = lucky.Append("telegram|chat:123", &gateway.Message{
		ID:        "m1",
		Chat:      gateway.Chat{ID: "123", Type: gateway.ChatPrivate},
		Sender:    gateway.User{ID: "u1", Username: "alice"},
		Text:      "first part",
		Timestamp: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Append first error = %v", err)
	}
	status, err = lucky.Append("telegram|chat:123", &gateway.Message{
		ID:        "m2",
		Chat:      gateway.Chat{ID: "123", Type: gateway.ChatPrivate},
		Sender:    gateway.User{ID: "u1", Username: "alice"},
		Text:      "second part",
		Timestamp: now.Add(2 * time.Minute),
		Attachments: []gateway.Attachment{{
			Type:     gateway.AttachmentImage,
			FileName: "photo.jpg",
			MimeType: "image/jpeg",
			FilePath: "/tmp/photo.jpg",
		}},
	})
	if err != nil {
		t.Fatalf("Append second error = %v", err)
	}
	if status.SegmentCount != 2 || status.AttachmentCount != 1 || status.LastMessageID != "m2" {
		t.Fatalf("unexpected append status: %+v", status)
	}

	batch, err := lucky.Finish("telegram|chat:123")
	if err != nil {
		t.Fatalf("Finish error = %v", err)
	}
	text := batch.Text()
	for _, want := range []string{
		"[Collected gateway message batch]",
		"Segment 1",
		"@alice",
		"first part",
		"Segment 2",
		"second part",
		"image 1",
		"local_path=/tmp/photo.jpg",
		"[User intent]",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("batch text missing %q:\n%s", want, text)
		}
	}

	input := batch.UserTurnInput()
	if input.RoutingText != text {
		t.Fatalf("expected routing text to match batch text")
	}
	if got := len(input.Attachments); got != 1 {
		t.Fatalf("expected one attachment, got %d", got)
	}
	if lucky.Status("telegram|chat:123").Active {
		t.Fatal("expected collector to be inactive after finish")
	}
}

func TestLuckyCancelAndEmptyFinish(t *testing.T) {
	lucky := NewLucky()
	if _, err := lucky.Finish("k"); !errors.Is(err, ErrInactive) {
		t.Fatalf("expected ErrInactive, got %v", err)
	}
	if _, err := lucky.Start("k"); err != nil {
		t.Fatalf("Start error = %v", err)
	}
	if _, err := lucky.Start("k"); !errors.Is(err, ErrAlreadyActive) {
		t.Fatalf("expected ErrAlreadyActive, got %v", err)
	}
	status, ok := lucky.Cancel("k")
	if !ok || !status.Active {
		t.Fatalf("expected cancel to report active buffer, status=%+v ok=%v", status, ok)
	}

	if _, err := lucky.Start("empty"); err != nil {
		t.Fatalf("Start empty error = %v", err)
	}
	if _, err := lucky.Finish("empty"); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("expected ErrEmptyBatch, got %v", err)
	}
	if lucky.Status("empty").Active {
		t.Fatal("expected empty finish to clear buffer")
	}
}

func TestLuckyKeyForMessageSeparatesGroupSenders(t *testing.T) {
	privateKey := KeyForMessage("telegram", &gateway.Message{
		Chat:   gateway.Chat{ID: "c1", Type: gateway.ChatPrivate},
		Sender: gateway.User{ID: "u1"},
	})
	if privateKey != "telegram|chat:c1" {
		t.Fatalf("unexpected private key: %s", privateKey)
	}

	groupA := KeyForMessage("telegram", &gateway.Message{
		Chat:   gateway.Chat{ID: "g1", Type: gateway.ChatGroup},
		Sender: gateway.User{ID: "u1"},
	})
	groupB := KeyForMessage("telegram", &gateway.Message{
		Chat:   gateway.Chat{ID: "g1", Type: gateway.ChatGroup},
		Sender: gateway.User{ID: "u2"},
	})
	if groupA == groupB {
		t.Fatalf("expected group sender keys to differ, both were %q", groupA)
	}
	if !strings.Contains(groupA, "sender:u1") || !strings.Contains(groupB, "sender:u2") {
		t.Fatalf("unexpected group keys: %q %q", groupA, groupB)
	}
}

func TestParseLuckyAction(t *testing.T) {
	cases := map[string]LuckyAction{
		"":       LuckyActionStatus,
		"on":     LuckyActionOn,
		"start":  LuckyActionOn,
		"off":    LuckyActionOff,
		"done":   LuckyActionOff,
		"status": LuckyActionStatus,
		"cancel": LuckyActionCancel,
		"wat":    LuckyActionUnknown,
	}
	for input, want := range cases {
		if got := ParseLuckyAction(input); got != want {
			t.Fatalf("ParseLuckyAction(%q) = %q, want %q", input, got, want)
		}
	}
}
