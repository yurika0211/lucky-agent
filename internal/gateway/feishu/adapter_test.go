package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

func TestCallbackChallengeAndVerificationToken(t *testing.T) {
	a := NewAdapter(callbackTestConfig())

	status, response := callbackRequest(t, a, map[string]any{
		"type":      "url_verification",
		"token":     "verify-me",
		"challenge": "challenge-value",
	})
	if status != http.StatusOK || response["challenge"] != "challenge-value" {
		t.Fatalf("challenge status=%d response=%#v", status, response)
	}

	status, _ = callbackRequest(t, a, map[string]any{
		"type":      "url_verification",
		"token":     "wrong",
		"challenge": "challenge-value",
	})
	if status != http.StatusForbidden {
		t.Fatalf("wrong token status = %d", status)
	}
}

func TestCallbackRejectsEncryptedEvents(t *testing.T) {
	a := NewAdapter(callbackTestConfig())
	status, _ := callbackRequest(t, a, map[string]any{"encrypt": "ciphertext"})
	if status != http.StatusNotImplemented {
		t.Fatalf("encrypted callback status = %d", status)
	}
}

func TestCallbackDispatchesSchemaV2TextMessage(t *testing.T) {
	a := NewAdapter(callbackTestConfig())
	received := make(chan *gateway.Message, 1)
	a.SetHandler(func(_ context.Context, msg *gateway.Message) error {
		received <- msg
		return nil
	})

	status, response := callbackRequest(t, a, schemaV2Payload("p2p", "hello", nil))
	if status != http.StatusOK || response["code"] != float64(0) {
		t.Fatalf("callback status=%d response=%#v", status, response)
	}
	select {
	case msg := <-received:
		if msg.ID != "om_message" || msg.Chat.ID != "oc_chat" || msg.Text != "hello" {
			t.Fatalf("unexpected gateway message: %#v", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("message handler was not called")
	}
}

func TestCallbackDeduplicatesEventID(t *testing.T) {
	a := NewAdapter(callbackTestConfig())
	received := make(chan struct{}, 2)
	a.SetHandler(func(_ context.Context, _ *gateway.Message) error {
		received <- struct{}{}
		return nil
	})
	payload := schemaV2Payload("p2p", "hello", nil)
	for i := 0; i < 2; i++ {
		status, _ := callbackRequest(t, a, payload)
		if status != http.StatusOK {
			t.Fatalf("callback %d status = %d", i+1, status)
		}
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("first event was not delivered")
	}
	select {
	case <-received:
		t.Fatal("duplicate event was delivered twice")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCallbackRejectsMismatchedAppID(t *testing.T) {
	a := NewAdapter(callbackTestConfig())
	payload := schemaV2Payload("p2p", "hello", nil)
	payload["header"].(map[string]any)["app_id"] = "cli_other"
	status, _ := callbackRequest(t, a, payload)
	if status != http.StatusForbidden {
		t.Fatalf("mismatched app id status = %d", status)
	}
}

func TestCallbackAcknowledgesBeforeHandlerCompletes(t *testing.T) {
	a := NewAdapter(callbackTestConfig())
	started := make(chan struct{})
	release := make(chan struct{})
	a.SetHandler(func(_ context.Context, _ *gateway.Message) error {
		close(started)
		<-release
		return nil
	})

	begin := time.Now()
	status, _ := callbackRequest(t, a, schemaV2Payload("p2p", "hello", nil))
	if status != http.StatusOK {
		t.Fatalf("callback status = %d", status)
	}
	if elapsed := time.Since(begin); elapsed > 250*time.Millisecond {
		t.Fatalf("callback waited for handler: %v", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("message handler did not start")
	}
	close(release)
}

func TestStartStopAndContextCancellation(t *testing.T) {
	cfg := callbackTestConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.Path = "callbacks"
	a := NewAdapter(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !a.IsRunning() || a.Path() != "/callbacks" {
		t.Fatalf("adapter did not start: running=%v path=%q", a.IsRunning(), a.Path())
	}
	if err := a.Start(ctx); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	resp, err := http.Get("http://" + a.ListenAddr() + a.Path())
	if err != nil {
		t.Fatalf("GET callback health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", resp.StatusCode)
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for a.IsRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.IsRunning() {
		t.Fatal("adapter remained running after context cancellation")
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("idempotent Stop() error = %v", err)
	}
}

func TestStartRejectsEncryptedConfiguration(t *testing.T) {
	cfg := callbackTestConfig()
	cfg.EncryptKey = "configured-key"
	a := NewAdapter(cfg)
	if err := a.Start(context.Background()); err == nil {
		t.Fatal("expected encrypted callback configuration to fail")
	}
}

func TestPhaseOneMediaMethodsFailClearly(t *testing.T) {
	a := NewAdapter(callbackTestConfig())
	if err := a.SendPhoto(context.Background(), "oc", "om", "image.png", "caption"); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("SendPhoto() error = %v", err)
	}
	if err := a.SendDocument(context.Background(), "oc", "om", "report.pdf", "caption"); !errors.Is(err, ErrUnsupportedMedia) {
		t.Fatalf("SendDocument() error = %v", err)
	}
}

func callbackRequest(t *testing.T, a *Adapter, payload any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal callback payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://callback.test"+a.Path(), bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	a.handleCallback(recorder, req)
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode callback response %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, decoded
}

func callbackTestConfig() Config {
	return Config{
		AppID:             "cli_app",
		AppSecret:         "secret",
		VerificationToken: "verify-me",
		BotOpenID:         "ou_bot",
		ListenAddr:        "127.0.0.1:0",
		Path:              "/feishu/events",
		GroupTriggerMode:  "mention",
		RemoveAt:          true,
	}
}

func schemaV2Payload(chatType, text string, mentions []map[string]any) map[string]any {
	content, _ := json.Marshal(map[string]string{"text": text})
	return map[string]any{
		"schema": "2.0",
		"header": map[string]any{
			"event_id":   "event-id",
			"event_type": "im.message.receive_v1",
			"token":      "verify-me",
			"app_id":     "cli_app",
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_id": map[string]any{"open_id": "ou_sender", "user_id": "u_sender"},
			},
			"message": map[string]any{
				"message_id":   "om_message",
				"create_time":  "1710000000123",
				"chat_id":      "oc_chat",
				"chat_type":    chatType,
				"message_type": "text",
				"content":      string(content),
				"mentions":     mentions,
			},
		},
	}
}
