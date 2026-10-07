package websocket

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestHandleLuckyCollectsChatUntilOff(t *testing.T) {
	h := NewAgentHandler(&stubAgentRuntime{})
	client := &Client{ID: "android", SessionID: "sess-lucky", Send: make(chan *Message, 16)}

	on, _ := NewMessage(TypeLucky, client.SessionID, LuckyData{Action: "on"})
	h.HandleMessage(client, on)
	chat, _ := NewMessage(TypeChat, client.SessionID, ChatData{Message: "第一段", Stream: true, Attachments: []gateway.Attachment{{Type: "image", FileName: "a.png"}}})
	chat.ID = "seg-1"
	h.HandleMessage(client, chat)

	if status := h.luckyCollector().Status(luckySessionKey(client.SessionID)); !status.Active || status.SegmentCount != 1 || status.AttachmentCount != 1 {
		t.Fatalf("collector status = %+v", status)
	}
	if len(h.runners) != 0 {
		t.Fatalf("collected chat started a run: %+v", h.runners)
	}

	off, _ := NewMessage(TypeLucky, client.SessionID, LuckyData{Action: "off"})
	off.ID = "off-1"
	h.HandleMessage(client, off)

	h.mu.Lock()
	runner := h.runners[client.SessionID]
	h.mu.Unlock()
	if runner == nil || (runner.active == nil && len(runner.queue) == 0) {
		t.Fatal("lucky off did not enqueue a chat run")
	}
	run := runner.active
	if run == nil && len(runner.queue) > 0 {
		run = runner.queue[0]
	}
	if run == nil || !containsAll(run.data.Message, "第一段", "[Collected gateway message batch]") {
		t.Fatalf("queued message = %#v", run)
	}
	if len(run.data.Attachments) != 1 {
		t.Fatalf("attachments = %+v", run.data.Attachments)
	}
	if h.luckyCollector().Status(luckySessionKey(client.SessionID)).Active {
		t.Fatal("collector stayed active after off")
	}
}

func TestHandleLuckyCancelDropsCollectedChat(t *testing.T) {
	h := NewAgentHandler(&stubAgentRuntime{})
	client := &Client{ID: "android", SessionID: "sess-cancel", Send: make(chan *Message, 8)}
	on, _ := NewMessage(TypeLucky, client.SessionID, LuckyData{Action: "on"})
	h.HandleMessage(client, on)
	chat, _ := NewMessage(TypeChat, client.SessionID, ChatData{Message: "丢掉"})
	h.HandleMessage(client, chat)
	cancel, _ := NewMessage(TypeLucky, client.SessionID, LuckyData{Action: "cancel"})
	h.HandleMessage(client, cancel)
	if status := h.luckyCollector().Status(luckySessionKey(client.SessionID)); status.Active || status.SegmentCount != 0 {
		t.Fatalf("status after cancel = %+v", status)
	}
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
