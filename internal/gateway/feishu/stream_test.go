package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendStreamUsesCardKitAndUpdatesOneInteractiveCard(t *testing.T) {
	type contentUpdate struct {
		Content  string `json:"content"`
		Sequence int    `json:"sequence"`
	}
	type closeUpdate struct {
		Settings string `json:"settings"`
		Sequence int    `json:"sequence"`
	}
	var updates []contentUpdate
	var closed bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/cardkit/v1/cards":
			var request createCardRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.Type != "card_json" {
				t.Fatalf("card type = %q", request.Type)
			}
			var card feishuStreamingCard
			if err := json.Unmarshal([]byte(request.Data), &card); err != nil {
				t.Fatalf("decode card JSON: %v", err)
			}
			if card.Schema != "2.0" || !card.Config.StreamingMode || !card.Config.UpdateMulti || len(card.Body.Elements) != 1 || card.Body.Elements[0].ElementID != feishuStreamElementID {
				t.Fatalf("unexpected streaming card: %#v", card)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"card_id": "card_stream"}})
		case "/open-apis/im/v1/messages/om_source/reply":
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.ReceiveID != "" || request.MsgType != "interactive" {
				t.Fatalf("unexpected interactive reply: %#v", request)
			}
			var content map[string]string
			if err := json.Unmarshal([]byte(request.Content), &content); err != nil || content["card_id"] != "card_stream" {
				t.Fatalf("unexpected interactive content %q: %v", request.Content, err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_stream"}})
		case "/open-apis/cardkit/v1/cards/card_stream/elements/" + feishuStreamElementID + "/content":
			var update contentUpdate
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Fatalf("decode content update: %v", err)
			}
			updates = append(updates, update)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		case "/open-apis/cardkit/v1/cards/card_stream/settings":
			var update closeUpdate
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Fatalf("decode close update: %v", err)
			}
			var settings map[string]any
			if err := json.Unmarshal([]byte(update.Settings), &settings); err != nil || settings["config"].(map[string]any)["streaming_mode"] != false {
				t.Fatalf("unexpected close settings %q: %v", update.Settings, err)
			}
			if update.Sequence != 4 {
				t.Fatalf("close sequence = %d, want 4", update.Sequence)
			}
			closed = true
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	a.cfg.CardProgress = true
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	stream, err := a.SendStream(context.Background(), "oc_chat", "om_source")
	if err != nil {
		t.Fatalf("SendStream() error = %v", err)
	}
	if stream.MessageID() != "om_stream" {
		t.Fatalf("stream message id = %q", stream.MessageID())
	}
	if err := stream.Append("第一段"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := stream.SetResult("最终结果"); err != nil {
		t.Fatalf("SetResult() error = %v", err)
	}
	if err := stream.Finish(); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	if len(updates) != 3 {
		t.Fatalf("update count = %d, want 3; updates=%#v", len(updates), updates)
	}
	if !closed {
		t.Fatal("streaming mode was not closed after Finish")
	}
	want := []string{"第一段", "最终结果", "最终结果"}
	for index, update := range updates {
		if update.Sequence != index+1 || update.Content != want[index] {
			t.Fatalf("update %d = %#v, want sequence=%d content=%q", index, update, index+1, want[index])
		}
	}
}

func TestSendStreamSilentlyFallsBackWhenDisabled(t *testing.T) {
	stream, err := NewAdapter(Config{AppID: "cli_app", AppSecret: "secret"}).SendStream(context.Background(), "oc_chat", "")
	if err != nil || stream != nil {
		t.Fatalf("disabled SendStream() = (%v, %v), want (nil, nil)", stream, err)
	}
}

func TestSendStreamFallsBackToChatWhenReplyEndpointFailsBeforeSending(t *testing.T) {
	var chatSends int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/cardkit/v1/cards":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"card_id": "card_fallback"}})
		case "/open-apis/im/v1/messages/om_source/reply":
			http.Error(w, "reply unavailable", http.StatusBadGateway)
		case "/open-apis/im/v1/messages":
			chatSends++
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.ReceiveID != "oc_chat" || request.MsgType != "interactive" {
				t.Fatalf("unexpected fallback chat request: %#v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_chat"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	a.cfg.CardProgress = true
	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	stream, err := a.SendStream(context.Background(), "oc_chat", "om_source")
	if err != nil {
		t.Fatalf("SendStream() fallback error = %v", err)
	}
	if stream.MessageID() != "om_chat" || chatSends != 1 {
		t.Fatalf("fallback stream=%q chatSends=%d", stream.MessageID(), chatSends)
	}
}

func TestStreamSenderDoesNotAppendToThinkingPlaceholder(t *testing.T) {
	sender := &feishuStreamSender{content: "正在思考..."}
	sender.hasContent = false
	sender.lastUpdate = time.Now()
	if err := sender.Append("第一段"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if sender.content != "第一段" || !sender.hasContent {
		t.Fatalf("placeholder was not replaced: %#v", sender)
	}
	if strings.Contains(sender.content, "正在思考") {
		t.Fatalf("placeholder leaked into content: %q", sender.content)
	}
}
