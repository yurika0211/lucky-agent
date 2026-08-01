package feishu

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestConvertEventPrivateText(t *testing.T) {
	a := NewAdapter(DefaultConfig())
	fallback := time.Unix(10, 0)
	a.now = func() time.Time { return fallback }

	msg, err := a.convertEvent(messageEvent{
		Sender: eventSender{SenderID: eventUserID{OpenID: "ou_sender", UserID: "u_sender"}},
		Message: eventMessage{
			MessageID:   "om_private",
			CreateTime:  "1710000000123",
			ChatID:      "oc_private",
			ChatType:    "p2p",
			MessageType: "text",
			Content:     "{\"text\":\"/ping now\"}",
		},
	})
	if err != nil {
		t.Fatalf("convertEvent() error = %v", err)
	}
	if msg == nil || msg.Chat.Type != gateway.ChatPrivate || msg.Chat.ID != "oc_private" {
		t.Fatalf("unexpected private message: %#v", msg)
	}
	if msg.Sender.ID != "ou_sender" || !msg.IsCommand || msg.Command != "/ping" || msg.Args != "now" {
		t.Fatalf("unexpected sender or command fields: %#v", msg)
	}
	if got := msg.Timestamp.UnixMilli(); got != 1710000000123 {
		t.Fatalf("timestamp = %d", got)
	}
}

func TestConvertEventGroupMentionRemovesOnlyBotMarker(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BotOpenID = "ou_bot"
	a := NewAdapter(cfg)

	event := groupTextEvent("@_user_1 ask @_user_2", []eventMention{
		{Key: "@_user_1", ID: eventUserID{OpenID: "ou_bot"}, Name: "LuckyAgent"},
		{Key: "@_user_2", ID: eventUserID{OpenID: "ou_human"}, Name: "Alice"},
	})
	msg, err := a.convertEvent(event)
	if err != nil {
		t.Fatalf("convertEvent() error = %v", err)
	}
	if msg == nil || !msg.IsGroupTrigger || msg.TriggerType != "mention" {
		t.Fatalf("expected mention-triggered group message, got %#v", msg)
	}
	if msg.Text != "ask @_user_2" {
		t.Fatalf("text = %q, want only bot marker removed", msg.Text)
	}
}

func TestConvertEventGroupTriggerModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		mentions []eventMention
		want     bool
	}{
		{name: "mention without mention", mode: "mention", want: false},
		{name: "mention with mention", mode: "mention", mentions: []eventMention{{Key: "@_user_1"}}, want: true},
		{name: "all", mode: "all", want: true},
		{name: "none", mode: "none", mentions: []eventMention{{Key: "@_user_1"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BotOpenID = "ou_bot"
			cfg.GroupTriggerMode = tt.mode
			for i := range tt.mentions {
				tt.mentions[i].ID.OpenID = "ou_bot"
			}
			a := NewAdapter(cfg)
			msg, err := a.convertEvent(groupTextEvent("hello", tt.mentions))
			if err != nil {
				t.Fatalf("convertEvent() error = %v", err)
			}
			if (msg != nil) != tt.want {
				t.Fatalf("message presence = %v, want %v", msg != nil, tt.want)
			}
		})
	}
}

func TestConvertEventEnforcesChatAndUserAllowlists(t *testing.T) {
	event := groupTextEvent("@_user_1 hello", []eventMention{{Key: "@_user_1", ID: eventUserID{OpenID: "ou_bot"}}})

	cfg := DefaultConfig()
	cfg.BotOpenID = "ou_bot"
	cfg.AllowedChats = []string{"oc_other"}
	msg, err := NewAdapter(cfg).convertEvent(event)
	if err != nil || msg != nil {
		t.Fatalf("denied chat returned msg=%#v err=%v", msg, err)
	}

	cfg.AllowedChats = []string{"oc_group"}
	cfg.AllowedUsers = []string{"u_sender"}
	msg, err = NewAdapter(cfg).convertEvent(event)
	if err != nil || msg == nil {
		t.Fatalf("allowed user_id returned msg=%#v err=%v", msg, err)
	}

	cfg.AllowedUsers = []string{"ou_other"}
	msg, err = NewAdapter(cfg).convertEvent(event)
	if err != nil || msg != nil {
		t.Fatalf("denied user returned msg=%#v err=%v", msg, err)
	}
}

func TestConvertEventGroupReplyToKnownBotMessage(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BotOpenID = "ou_bot"
	a := NewAdapter(cfg)
	a.outboundMessages.add("om_bot", a.now())
	event := groupTextEvent("reply text", nil)
	event.Message.ParentID = "om_bot"

	msg, err := a.convertEvent(event)
	if err != nil {
		t.Fatalf("convertEvent() error = %v", err)
	}
	if msg == nil || !msg.IsGroupTrigger || msg.TriggerType != "reply" {
		t.Fatalf("expected reply-triggered group message, got %#v", msg)
	}
	if msg.ReplyTo == nil || msg.ReplyTo.ID != "om_bot" {
		t.Fatalf("reply metadata = %#v", msg.ReplyTo)
	}
}

func TestConvertEventIgnoresNonUserSender(t *testing.T) {
	event := groupTextEvent("@_user_1 hello", []eventMention{{Key: "@_user_1", ID: eventUserID{OpenID: "ou_bot"}}})
	event.Sender.SenderType = "app"
	cfg := DefaultConfig()
	cfg.BotOpenID = "ou_bot"
	msg, err := NewAdapter(cfg).convertEvent(event)
	if err != nil || msg != nil {
		t.Fatalf("app sender returned msg=%#v err=%v", msg, err)
	}
}

func groupTextEvent(text string, mentions []eventMention) messageEvent {
	content, _ := json.Marshal(map[string]string{"text": text})
	return messageEvent{
		Sender: eventSender{SenderID: eventUserID{OpenID: "ou_sender", UserID: "u_sender"}},
		Message: eventMessage{
			MessageID:   "om_group",
			CreateTime:  "1710000000123",
			ChatID:      "oc_group",
			ChatType:    "group",
			MessageType: "text",
			Content:     string(content),
			Mentions:    mentions,
		},
	}
}
