package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

const tokenRefreshSkew = time.Minute

type tenantTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

type botInfo struct {
	OpenID string `json:"open_id"`
}

type botInfoResponse struct {
	Code int     `json:"code"`
	Msg  string  `json:"msg"`
	Bot  botInfo `json:"bot"`
	Data struct {
		Bot botInfo `json:"bot"`
	} `json:"data"`
}

type sendMessageRequest struct {
	ReceiveID string `json:"receive_id,omitempty"`
	MsgType   string `json:"msg_type"`
	Content   string `json:"content"`
}

type sendMessageResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		MessageID string `json:"message_id"`
	} `json:"data"`
}

func (a *Adapter) Send(ctx context.Context, chatID string, message string) error {
	_, err := a.SendWithReceipt(ctx, chatID, message)
	return err
}

func (a *Adapter) SendWithReceipt(ctx context.Context, chatID string, message string) (gateway.SentMessage, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return gateway.SentMessage{}, fmt.Errorf("feishu: chat id is required")
	}
	payload, err := newTextMessageRequest(chatID, message)
	if err != nil {
		return gateway.SentMessage{}, err
	}
	var response sendMessageResponse
	query := url.Values{"receive_id_type": []string{"chat_id"}}
	if err := a.authorizedJSON(ctx, http.MethodPost, "/open-apis/im/v1/messages", query, payload, &response); err != nil {
		return gateway.SentMessage{}, fmt.Errorf("feishu: send message: %w", err)
	}
	return a.recordSentMessage(chatID, response)
}

func (a *Adapter) SendWithReply(ctx context.Context, chatID string, replyToMsgID string, message string) error {
	_, err := a.SendWithReplyReceipt(ctx, chatID, replyToMsgID, message)
	return err
}

func (a *Adapter) SendWithReplyReceipt(ctx context.Context, chatID string, replyToMsgID string, message string) (gateway.SentMessage, error) {
	replyToMsgID = strings.TrimSpace(replyToMsgID)
	if replyToMsgID == "" {
		return a.SendWithReceipt(ctx, chatID, message)
	}
	payload, err := newTextMessageRequest("", message)
	if err != nil {
		return gateway.SentMessage{}, err
	}
	var response sendMessageResponse
	path := "/open-apis/im/v1/messages/" + url.PathEscape(replyToMsgID) + "/reply"
	if err := a.authorizedJSON(ctx, http.MethodPost, path, nil, payload, &response); err == nil {
		if receipt, receiptErr := a.recordSentMessage(chatID, response); receiptErr == nil {
			return receipt, nil
		} else {
			log.Printf("[feishu] reply response was invalid, falling back to chat send: %v", receiptErr)
		}
	} else {
		log.Printf("[feishu] reply message failed, falling back to chat send: %v", err)
	}
	return a.SendWithReceipt(ctx, chatID, message)
}

func (a *Adapter) recordSentMessage(chatID string, response sendMessageResponse) (gateway.SentMessage, error) {
	messageID := strings.TrimSpace(response.Data.MessageID)
	if messageID == "" {
		return gateway.SentMessage{}, fmt.Errorf("feishu: message API returned an empty message_id")
	}
	a.outboundMessages.add(messageID, a.now())
	return gateway.SentMessage{ID: messageID, ChatID: strings.TrimSpace(chatID)}, nil
}

func newTextMessageRequest(receiveID, message string) (sendMessageRequest, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = " "
	}
	content, err := json.Marshal(map[string]string{"text": message})
	if err != nil {
		return sendMessageRequest{}, fmt.Errorf("feishu: encode text content: %w", err)
	}
	return sendMessageRequest{
		ReceiveID: receiveID,
		MsgType:   "text",
		Content:   string(content),
	}, nil
}

func (a *Adapter) ensureTenantAccessToken(ctx context.Context) (string, error) {
	a.tokenMu.Lock()
	defer a.tokenMu.Unlock()

	now := a.now()
	if token := strings.TrimSpace(a.accessToken); token != "" && now.Before(a.tokenExpiry.Add(-tokenRefreshSkew)) {
		return token, nil
	}
	payload := map[string]string{
		"app_id":     strings.TrimSpace(a.cfg.AppID),
		"app_secret": strings.TrimSpace(a.cfg.AppSecret),
	}
	var response tenantTokenResponse
	if err := a.rawJSON(ctx, http.MethodPost, "/open-apis/auth/v3/tenant_access_token/internal", nil, "", payload, &response); err != nil {
		return "", fmt.Errorf("feishu: request tenant access token: %w", err)
	}
	if response.Code != 0 {
		return "", fmt.Errorf("feishu: token API code %d: %s", response.Code, strings.TrimSpace(response.Msg))
	}
	token := strings.TrimSpace(response.TenantAccessToken)
	if token == "" {
		return "", fmt.Errorf("feishu: token API returned an empty tenant_access_token")
	}
	expiresIn := response.Expire
	if expiresIn <= 0 {
		expiresIn = 7200
	}
	a.accessToken = token
	a.tokenExpiry = now.Add(time.Duration(expiresIn) * time.Second)
	return token, nil
}

func (a *Adapter) resolveBotOpenID(ctx context.Context) error {
	a.identityMu.Lock()
	defer a.identityMu.Unlock()

	a.mu.RLock()
	botOpenID := strings.TrimSpace(a.botOpenID)
	a.mu.RUnlock()
	if botOpenID != "" {
		return nil
	}
	token, err := a.ensureTenantAccessToken(ctx)
	if err != nil {
		return err
	}
	var response botInfoResponse
	if err := a.rawJSON(ctx, http.MethodGet, "/open-apis/bot/v3/info", nil, token, nil, &response); err != nil {
		return fmt.Errorf("request bot info: %w", err)
	}
	if response.Code != 0 {
		return fmt.Errorf("bot info API code %d: %s", response.Code, strings.TrimSpace(response.Msg))
	}
	botOpenID = firstNonEmpty(response.Bot.OpenID, response.Data.Bot.OpenID)
	if botOpenID == "" {
		return fmt.Errorf("bot info API returned an empty open_id")
	}
	a.mu.Lock()
	a.botOpenID = botOpenID
	a.mu.Unlock()
	return nil
}

func (a *Adapter) authorizedJSON(ctx context.Context, method, path string, query url.Values, input, output any) error {
	token, err := a.ensureTenantAccessToken(ctx)
	if err != nil {
		return err
	}
	if err := a.rawJSON(ctx, method, path, query, token, input, output); err != nil {
		return err
	}
	if response, ok := output.(*sendMessageResponse); ok && response.Code != 0 {
		return fmt.Errorf("API code %d: %s", response.Code, strings.TrimSpace(response.Msg))
	}
	return nil
}

func (a *Adapter) rawJSON(ctx context.Context, method, path string, query url.Values, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	endpoint, err := url.Parse(a.cfg.normalizedAPIBaseURL() + path)
	if err != nil {
		return fmt.Errorf("build API URL: %w", err)
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		limited, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP status %d: %s", resp.StatusCode, strings.TrimSpace(string(limited)))
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(output); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

var _ gateway.ReceiptGateway = (*Adapter)(nil)
