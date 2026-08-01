package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSendUsesCachedTenantTokenAndFeishuMessageAPI(t *testing.T) {
	var tokenRequests atomic.Int32
	var sendRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenRequests.Add(1)
			var credentials map[string]string
			_ = json.NewDecoder(r.Body).Decode(&credentials)
			if credentials["app_id"] != "cli_app" || credentials["app_secret"] != "secret" {
				t.Errorf("unexpected credentials: %#v", credentials)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "tenant_access_token": "tenant-token", "expire": 3600,
			})
		case "/open-apis/im/v1/messages":
			sendRequests.Add(1)
			if r.URL.Query().Get("receive_id_type") != "chat_id" {
				t.Errorf("receive_id_type = %q", r.URL.Query().Get("receive_id_type"))
			}
			if r.Header.Get("Authorization") != "Bearer tenant-token" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.ReceiveID != "oc_chat" || request.MsgType != "text" {
				t.Errorf("unexpected send request: %#v", request)
			}
			var content map[string]string
			if err := json.Unmarshal([]byte(request.Content), &content); err != nil || content["text"] != "hello \"Feishu\"" {
				t.Errorf("unexpected nested content %q: %v", request.Content, err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok", "data": map[string]string{"message_id": "om_sent"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	for i := 0; i < 2; i++ {
		receipt, err := a.SendWithReceipt(context.Background(), "oc_chat", "hello \"Feishu\"")
		if err != nil {
			t.Fatalf("SendWithReceipt() error = %v", err)
		}
		if receipt.ID != "om_sent" || receipt.ChatID != "oc_chat" {
			t.Fatalf("unexpected receipt: %#v", receipt)
		}
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("token requests = %d, want 1", got)
	}
	if got := sendRequests.Load(); got != 2 {
		t.Fatalf("send requests = %d, want 2", got)
	}
	if !a.outboundMessages.contains("om_sent", a.now()) {
		t.Fatal("successful outbound message id was not tracked")
	}
}

func TestSendWithReplyUsesReplyEndpoint(t *testing.T) {
	var replyRequest sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/messages/om_parent/reply":
			_ = json.NewDecoder(r.Body).Decode(&replyRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "data": map[string]string{"message_id": "om_reply"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	receipt, err := apiTestAdapter(server).SendWithReplyReceipt(context.Background(), "oc_chat", "om_parent", "reply text")
	if err != nil {
		t.Fatalf("SendWithReplyReceipt() error = %v", err)
	}
	if receipt.ID != "om_reply" || replyRequest.ReceiveID != "" || replyRequest.MsgType != "text" {
		t.Fatalf("unexpected reply receipt/request: %#v %#v", receipt, replyRequest)
	}
}

func TestSendWithReplyFallsBackToChatSend(t *testing.T) {
	var ordinarySend atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case strings.HasSuffix(r.URL.Path, "/reply"):
			http.Error(w, "reply unavailable", http.StatusBadGateway)
		case r.URL.Path == "/open-apis/im/v1/messages":
			ordinarySend.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_fallback"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	receipt, err := apiTestAdapter(server).SendWithReplyReceipt(context.Background(), "oc_chat", "om_parent", "reply text")
	if err != nil {
		t.Fatalf("fallback send error = %v", err)
	}
	if receipt.ID != "om_fallback" || ordinarySend.Load() != 1 {
		t.Fatalf("fallback receipt=%#v requests=%d", receipt, ordinarySend.Load())
	}
}

func TestSendRejectsEmptyMessageID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/messages":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	if err := apiTestAdapter(server).Send(context.Background(), "oc_chat", "hello"); err == nil || !strings.Contains(err.Error(), "empty message_id") {
		t.Fatalf("Send() error = %v", err)
	}
}

func TestStartResolvesBotOpenIDAndCachesTenantToken(t *testing.T) {
	var tokenRequests atomic.Int32
	var botInfoRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			tokenRequests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/bot/v3/info":
			botInfoRequests.Add(1)
			if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer token" {
				t.Errorf("unexpected bot info request: method=%s auth=%q", r.Method, r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "bot": map[string]string{"open_id": "ou_resolved_bot"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := callbackTestConfig()
	cfg.BotOpenID = ""
	cfg.APIBaseURL = server.URL
	cfg.HTTPClient = server.Client()
	a := NewAdapter(cfg)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer a.Stop()
	a.mu.RLock()
	botOpenID := a.botOpenID
	a.mu.RUnlock()
	if botOpenID != "ou_resolved_bot" {
		t.Fatalf("resolved bot open id = %q", botOpenID)
	}
	if tokenRequests.Load() != 1 || botInfoRequests.Load() != 1 {
		t.Fatalf("token requests=%d bot info requests=%d", tokenRequests.Load(), botInfoRequests.Load())
	}
	if _, err := a.ensureTenantAccessToken(context.Background()); err != nil {
		t.Fatalf("cached token lookup error = %v", err)
	}
	if tokenRequests.Load() != 1 {
		t.Fatalf("tenant token was not cached: requests=%d", tokenRequests.Load())
	}
}

func TestResolveBotOpenIDSupportsDataEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/bot/v3/info":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"bot": map[string]string{"open_id": "ou_nested_bot"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	a := apiTestAdapter(server)
	a.mu.Lock()
	a.botOpenID = ""
	a.mu.Unlock()
	if err := a.resolveBotOpenID(context.Background()); err != nil {
		t.Fatalf("resolveBotOpenID() error = %v", err)
	}
	a.mu.RLock()
	got := a.botOpenID
	a.mu.RUnlock()
	if got != "ou_nested_bot" {
		t.Fatalf("resolved nested bot open id = %q", got)
	}
}

func TestStartFailsWhenBotIdentityCannotBeResolved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/bot/v3/info":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "bot": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := callbackTestConfig()
	cfg.BotOpenID = ""
	cfg.APIBaseURL = server.URL
	cfg.HTTPClient = server.Client()
	a := NewAdapter(cfg)
	err := a.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "empty open_id") {
		t.Fatalf("Start() error = %v", err)
	}
	if a.IsRunning() {
		t.Fatal("adapter started without a resolved bot identity")
	}
}

func apiTestAdapter(server *httptest.Server) *Adapter {
	cfg := callbackTestConfig()
	cfg.APIBaseURL = server.URL
	cfg.HTTPClient = server.Client()
	return NewAdapter(cfg)
}
