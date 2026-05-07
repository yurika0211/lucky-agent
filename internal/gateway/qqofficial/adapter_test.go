package qqofficial

import (
	"encoding/json"
	"testing"

	"github.com/yurika0211/luckyharness/internal/gateway"
)

func TestBuildIntentBits(t *testing.T) {
	bits := buildIntentBits([]string{"group_messages", "c2c_messages"})
	if bits&(1<<25) == 0 {
		t.Fatal("expected group message intent bit")
	}
	if bits&(1<<26) == 0 {
		t.Fatal("expected c2c message intent bit")
	}
}

func TestParseCommand(t *testing.T) {
	isCommand, cmd, args := parseCommand("/hello world")
	if !isCommand || cmd != "/hello" || args != "world" {
		t.Fatalf("unexpected parse result: %v %q %q", isCommand, cmd, args)
	}
}

func TestStripLeadingQQMention(t *testing.T) {
	got := stripLeadingQQMention("<@!123456> 你好")
	if got != "你好" {
		t.Fatalf("stripLeadingQQMention() = %q", got)
	}
}

func TestConvertDispatchGroupAtMessage(t *testing.T) {
	a := NewAdapter(DefaultConfig())
	raw, _ := json.Marshal(incomingMessageEvent{
		ID:          "msg-1",
		Content:     "<@!123456> /ping now",
		GroupOpenID: "group-1",
		Author: messageAuthor{
			ID:       "user-1",
			Username: "tester",
		},
	})

	msg := a.convertDispatch("GROUP_AT_MESSAGE_CREATE", raw)
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Chat.Type != gateway.ChatGroup || msg.Chat.ID != "group:group-1" {
		t.Fatalf("unexpected chat: %+v", msg.Chat)
	}
	if !msg.IsGroupTrigger || msg.TriggerType != "mention" {
		t.Fatalf("expected group trigger metadata: %+v", msg)
	}
	if !msg.IsCommand || msg.Command != "/ping" || msg.Args != "now" {
		t.Fatalf("unexpected command parsing: %+v", msg)
	}
}

func TestConvertDispatchC2CMessage(t *testing.T) {
	a := NewAdapter(DefaultConfig())
	raw, _ := json.Marshal(incomingMessageEvent{
		ID:      "msg-2",
		Content: "hello there",
		Author: messageAuthor{
			ID:       "user-2",
			Username: "tester2",
		},
	})

	msg := a.convertDispatch("C2C_MESSAGE_CREATE", raw)
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Chat.Type != gateway.ChatPrivate || msg.Chat.ID != "c2c:user-2" {
		t.Fatalf("unexpected chat: %+v", msg.Chat)
	}
	if msg.Text != "hello there" {
		t.Fatalf("unexpected text: %q", msg.Text)
	}
}
