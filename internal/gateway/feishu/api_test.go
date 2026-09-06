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

func TestSplitFeishuTextPreservesWhitespaceAndUnicode(t *testing.T) {
	message := " \n" + strings.Repeat("你", feishuTextChunkLimit) + "\n "
	chunks := splitFeishuText(message)
	if got := strings.Join(chunks, ""); got != message {
		t.Fatalf("reassembled chunks = %q, want %q", got, message)
	}
	for _, chunk := range chunks {
		if got := len([]rune(chunk)); got > feishuTextChunkLimit {
			t.Fatalf("chunk rune length = %d, limit %d", got, feishuTextChunkLimit)
		}
	}
}

func TestNewMessageRequestPreservesSurroundingWhitespace(t *testing.T) {
	const message = " \ncomplete text\n "
	request, err := newMessageRequest("oc_chat", message)
	if err != nil {
		t.Fatalf("newMessageRequest() error = %v", err)
	}
	if request.MsgType != "text" {
		t.Fatalf("MsgType = %q, want text", request.MsgType)
	}
	var content map[string]string
	if err := json.Unmarshal([]byte(request.Content), &content); err != nil {
		t.Fatalf("decode text content: %v", err)
	}
	if got := content["text"]; got != message {
		t.Fatalf("text content = %q, want %q", got, message)
	}
}

func TestSendRendersMarkdownAndLongURLsAsFeishuPost(t *testing.T) {
	const markdownURL = "https://platform.example.com/docs/agents/quickstart?utm_source=luckyagent&utm_medium=feishu&utm_campaign=long-link"
	const bareURL = "https://downloads.example.com/releases/2026/09/luckyagent-linux-amd64.tar.gz?signature=abcdefghijklmnopqrstuvwxyz0123456789"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case "/open-apis/im/v1/messages":
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.MsgType != "post" {
				t.Fatalf("MsgType = %q, want post", request.MsgType)
			}
			var content feishuPostContent
			if err := json.Unmarshal([]byte(request.Content), &content); err != nil {
				t.Fatalf("decode post content: %v", err)
			}
			links := postLinks(content)
			if len(links) != 2 {
				t.Fatalf("link count = %d, want 2; content=%#v", len(links), content)
			}
			if links[0].Href != markdownURL || links[0].Text != "Agent quickstart" {
				t.Fatalf("markdown link = %#v", links[0])
			}
			if links[1].Href != bareURL {
				t.Fatalf("bare link target = %q", links[1].Href)
			}
			if links[1].Text == bareURL || !strings.HasPrefix(links[1].Text, "downloads.example.com/releases/") {
				t.Fatalf("bare link label was not compacted: %q", links[1].Text)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_post"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	message := "文档：[Agent quickstart](" + markdownURL + ")\n下载地址：" + bareURL
	receipt, err := apiTestAdapter(server).SendWithReceipt(context.Background(), "oc_chat", message)
	if err != nil {
		t.Fatalf("SendWithReceipt() error = %v", err)
	}
	if receipt.ID != "om_post" {
		t.Fatalf("receipt = %#v", receipt)
	}
}

func TestPostFormattingLeavesURLsInsideCodeUntouched(t *testing.T) {
	request, err := newMessageRequest("oc_chat", "先看文档 https://example.com/guide\n\n`https://example.com/not-a-link`")
	if err != nil {
		t.Fatalf("newMessageRequest() error = %v", err)
	}
	if request.MsgType != "post" {
		t.Fatalf("MsgType = %q, want post", request.MsgType)
	}
	var content feishuPostContent
	if err := json.Unmarshal([]byte(request.Content), &content); err != nil {
		t.Fatalf("decode post content: %v", err)
	}
	if links := postLinks(content); len(links) != 1 || links[0].Href != "https://example.com/guide" {
		t.Fatalf("links = %#v", links)
	}
	if !postHasText(content, "https://example.com/not-a-link") {
		t.Fatalf("code URL should remain text: %#v", content)
	}
}

func TestPostFormattingExcludesChineseTrailingPunctuationFromURL(t *testing.T) {
	const destination = "https://example.com/releases/luckyagent?build=20260904"
	request, err := newMessageRequest("oc_chat", "更新地址："+destination+"。")
	if err != nil {
		t.Fatalf("newMessageRequest() error = %v", err)
	}
	var content feishuPostContent
	if err := json.Unmarshal([]byte(request.Content), &content); err != nil {
		t.Fatalf("decode post content: %v", err)
	}
	links := postLinks(content)
	if len(links) != 1 || links[0].Href != destination {
		t.Fatalf("links = %#v", links)
	}
	if !postHasText(content, "。") {
		t.Fatalf("trailing punctuation was lost: %#v", content)
	}
}

func postLinks(content feishuPostContent) []feishuPostElement {
	var links []feishuPostElement
	for _, row := range content.ZhCN.Content {
		for _, element := range row {
			if element.Tag == "a" {
				links = append(links, element)
			}
		}
	}
	return links
}

func postHasText(content feishuPostContent, want string) bool {
	for _, row := range content.ZhCN.Content {
		for _, element := range row {
			if element.Tag == "text" && strings.Contains(element.Text, want) {
				return true
			}
		}
	}
	return false
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

func TestSendWithReplyReceiptDeliversAllChunks(t *testing.T) {
	var replyTexts, chatTexts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "token", "expire": 3600})
		case r.URL.Path == "/open-apis/im/v1/messages/om_parent/reply":
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			var content map[string]string
			_ = json.Unmarshal([]byte(request.Content), &content)
			replyTexts = append(replyTexts, content["text"])
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_reply"}})
		case r.URL.Path == "/open-apis/im/v1/messages":
			var request sendMessageRequest
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request.ReceiveID != "oc_chat" {
				t.Errorf("follow-up receive_id = %q, want oc_chat", request.ReceiveID)
			}
			var content map[string]string
			_ = json.Unmarshal([]byte(request.Content), &content)
			chatTexts = append(chatTexts, content["text"])
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]string{"message_id": "om_followup"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	message := strings.Repeat("你", feishuTextChunkLimit) + "尾"
	receipt, err := apiTestAdapter(server).SendWithReplyReceipt(context.Background(), "oc_chat", "om_parent", message)
	if err != nil {
		t.Fatalf("SendWithReplyReceipt() error = %v", err)
	}
	if len(replyTexts) != 1 || len(chatTexts) != 1 {
		t.Fatalf("reply chunks=%d chat chunks=%d, want 1 and 1", len(replyTexts), len(chatTexts))
	}
	if got := replyTexts[0] + chatTexts[0]; got != message {
		t.Fatalf("delivered text = %q, want %q", got, message)
	}
	if receipt.ID != "om_followup" || receipt.ChatID != "oc_chat" {
		t.Fatalf("receipt = %#v, want final follow-up receipt", receipt)
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
