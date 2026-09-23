package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yurika0211/luckyagent/internal/gateway"
)

const (
	feishuStreamElementID      = "luckyagent_stream_content"
	feishuStreamUpdateInterval = 300 * time.Millisecond
	feishuStreamRequestTimeout = 10 * time.Second
)

var unsafeCardProgressLabel = regexp.MustCompile(`[^\pL\pN_.:-]+`)

type createCardRequest struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type createCardResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		CardID string `json:"card_id"`
	} `json:"data"`
}

type updateCardContentRequest struct {
	UUID     string `json:"uuid"`
	Content  string `json:"content"`
	Sequence int    `json:"sequence"`
}

type cardKitResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type feishuStreamingCard struct {
	Schema string                    `json:"schema"`
	Config feishuStreamingCardConfig `json:"config"`
	Body   feishuStreamingCardBody   `json:"body"`
}

type feishuStreamingCardConfig struct {
	StreamingMode   bool                       `json:"streaming_mode"`
	StreamingConfig feishuStreamingPrintConfig `json:"streaming_config"`
	UpdateMulti     bool                       `json:"update_multi"`
}

type feishuStreamingPrintConfig struct {
	PrintFrequencyMS map[string]int `json:"print_frequency_ms"`
	PrintStep        map[string]int `json:"print_step"`
	PrintStrategy    string         `json:"print_strategy"`
}

type feishuStreamingCardBody struct {
	Elements []feishuStreamingCardElement `json:"elements"`
}

type feishuStreamingCardElement struct {
	Tag       string `json:"tag"`
	ElementID string `json:"element_id"`
	Content   string `json:"content"`
}

// SendStream creates a CardKit entity and sends it as an interactive message.
// Subsequent updates replace its markdown element with the accumulated text,
// which Feishu renders as a native streaming card.
func (a *Adapter) SendStream(ctx context.Context, chatID string, replyToMsgID string) (gateway.StreamSender, error) {
	if !a.cfg.CardProgress {
		return nil, nil
	}
	if !a.IsRunning() {
		return nil, fmt.Errorf("feishu: adapter not running")
	}
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, fmt.Errorf("feishu: chat id is required")
	}

	cardID, err := a.createStreamingCard(ctx)
	if err != nil {
		return nil, err
	}
	closeOrphanedCard := func() {
		sender := &feishuStreamSender{adapter: a, cardID: cardID}
		if closeErr := sender.closeStreamingMode(); closeErr != nil {
			log.Printf("[feishu] failed to close unused streaming card: %v", closeErr)
		}
	}
	payload, err := newInteractiveCardMessageRequest(chatID, cardID)
	if err != nil {
		closeOrphanedCard()
		return nil, err
	}
	receipt, err := a.sendStreamCard(ctx, chatID, replyToMsgID, payload)
	if err != nil {
		closeOrphanedCard()
		return nil, err
	}
	return &feishuStreamSender{
		adapter:   a,
		cardID:    cardID,
		messageID: receipt.ID,
		content:   "正在思考...",
		startedAt: time.Now(),
	}, nil
}

func (a *Adapter) createStreamingCard(ctx context.Context) (string, error) {
	definition := feishuStreamingCard{
		Schema: "2.0",
		Config: feishuStreamingCardConfig{
			StreamingMode: true,
			UpdateMulti:   true,
			StreamingConfig: feishuStreamingPrintConfig{
				PrintFrequencyMS: map[string]int{"default": 120},
				PrintStep:        map[string]int{"default": 1},
				PrintStrategy:    "fast",
			},
		},
		Body: feishuStreamingCardBody{Elements: []feishuStreamingCardElement{{
			Tag:       "markdown",
			ElementID: feishuStreamElementID,
			Content:   "正在思考...",
		}}},
	}
	data, err := json.Marshal(definition)
	if err != nil {
		return "", fmt.Errorf("feishu: encode streaming card: %w", err)
	}
	var response createCardResponse
	if err := a.authorizedJSON(ctx, http.MethodPost, "/open-apis/cardkit/v1/cards", nil, createCardRequest{
		Type: "card_json",
		Data: string(data),
	}, &response); err != nil {
		return "", fmt.Errorf("feishu: create streaming card: %w", err)
	}
	if response.Code != 0 {
		return "", fmt.Errorf("feishu: create streaming card API code %d: %s", response.Code, strings.TrimSpace(response.Msg))
	}
	cardID := strings.TrimSpace(response.Data.CardID)
	if cardID == "" {
		return "", fmt.Errorf("feishu: create streaming card returned an empty card_id")
	}
	return cardID, nil
}

func newInteractiveCardMessageRequest(receiveID, cardID string) (sendMessageRequest, error) {
	content, err := json.Marshal(map[string]string{"card_id": strings.TrimSpace(cardID)})
	if err != nil {
		return sendMessageRequest{}, fmt.Errorf("feishu: encode interactive card content: %w", err)
	}
	return sendMessageRequest{
		ReceiveID: strings.TrimSpace(receiveID),
		MsgType:   "interactive",
		Content:   string(content),
	}, nil
}

func (a *Adapter) sendStreamCard(ctx context.Context, chatID, replyToMsgID string, payload sendMessageRequest) (gateway.SentMessage, error) {
	replyToMsgID = strings.TrimSpace(replyToMsgID)
	if replyToMsgID == "" {
		return a.sendStreamCardToChat(ctx, chatID, payload)
	}
	replyPayload := payload
	replyPayload.ReceiveID = ""
	var response sendMessageResponse
	path := "/open-apis/im/v1/messages/" + url.PathEscape(replyToMsgID) + "/reply"
	if err := a.authorizedJSON(ctx, http.MethodPost, path, nil, replyPayload, &response); err == nil {
		return a.recordSentMessage(chatID, response)
	}
	return a.sendStreamCardToChat(ctx, chatID, payload)
}

func (a *Adapter) sendStreamCardToChat(ctx context.Context, chatID string, payload sendMessageRequest) (gateway.SentMessage, error) {
	payload.ReceiveID = strings.TrimSpace(chatID)
	var response sendMessageResponse
	query := url.Values{"receive_id_type": []string{"chat_id"}}
	if err := a.authorizedJSON(ctx, http.MethodPost, "/open-apis/im/v1/messages", query, payload, &response); err != nil {
		return gateway.SentMessage{}, fmt.Errorf("feishu: send streaming card: %w", err)
	}
	return a.recordSentMessage(chatID, response)
}

func (a *Adapter) updateStreamingCard(ctx context.Context, cardID, content string, sequence int) error {
	path := "/open-apis/cardkit/v1/cards/" + url.PathEscape(cardID) + "/elements/" + url.PathEscape(feishuStreamElementID) + "/content"
	var response cardKitResponse
	if err := a.authorizedJSON(ctx, http.MethodPut, path, nil, updateCardContentRequest{
		UUID:     uuid.NewString(),
		Content:  content,
		Sequence: sequence,
	}, &response); err != nil {
		return fmt.Errorf("feishu: update streaming card: %w", err)
	}
	if response.Code != 0 {
		return fmt.Errorf("feishu: update streaming card API code %d: %s", response.Code, strings.TrimSpace(response.Msg))
	}
	return nil
}

type feishuStreamSender struct {
	adapter   *Adapter
	cardID    string
	messageID string

	mu           sync.Mutex
	content      string
	hasContent   bool
	sequence     int
	lastUpdate   time.Time
	finished     bool
	streamClosed bool
	startedAt    time.Time
}

func (s *feishuStreamSender) Append(content string) error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return fmt.Errorf("feishu: stream sender already finished")
	}
	if s.hasContent {
		s.content += content
	} else {
		s.content = content
		s.hasContent = true
	}
	if time.Since(s.lastUpdate) < feishuStreamUpdateInterval {
		s.mu.Unlock()
		return nil
	}
	content, sequence := s.nextUpdateLocked()
	s.mu.Unlock()
	return s.update(content, sequence)
}

func (s *feishuStreamSender) SetThinking(label string) error {
	return s.replaceAndUpdate(s.progressLabel("正在思考", label), false, false)
}

func (s *feishuStreamSender) SetToolCall(name, _ string) error {
	name = unsafeCardProgressLabel.ReplaceAllString(strings.TrimSpace(name), "")
	if len([]rune(name)) > 64 {
		name = string([]rune(name)[:64])
	}
	if name == "" {
		name = "工具"
	}
	return s.replaceAndUpdate(s.progressLabel("正在调用 "+name, ""), false, false)
}

func (s *feishuStreamSender) SetResult(content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "我这边暂时还没有整理出可发送的结果。"
	}
	return s.replaceAndUpdate(content, true, true)
}

func (s *feishuStreamSender) Finish() error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil
	}
	s.finished = true
	if strings.TrimSpace(s.content) == "" {
		s.content = "我这边暂时还没有整理出可发送的结果。"
		s.hasContent = true
	}
	content, sequence := s.nextUpdateLocked()
	s.mu.Unlock()
	updateErr := s.update(content, sequence)
	closeErr := s.closeStreamingMode()
	return errors.Join(updateErr, closeErr)
}

func (s *feishuStreamSender) MessageID() string { return s.messageID }

func (s *feishuStreamSender) replaceAndUpdate(content string, hasContent, force bool) error {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return nil
	}
	s.content = content
	s.hasContent = hasContent
	if !force && time.Since(s.lastUpdate) < feishuStreamUpdateInterval {
		s.mu.Unlock()
		return nil
	}
	content, sequence := s.nextUpdateLocked()
	s.mu.Unlock()
	return s.update(content, sequence)
}

func (s *feishuStreamSender) progressLabel(stage, detail string) string {
	detail = strings.TrimSpace(detail)
	if detail != "" {
		detail = unsafeCardProgressLabel.ReplaceAllString(detail, " ")
		if len([]rune(detail)) > 96 {
			detail = string([]rune(detail)[:96]) + "…"
		}
	}
	elapsed := time.Duration(0)
	if !s.startedAt.IsZero() {
		elapsed = time.Since(s.startedAt).Round(time.Second)
	}
	label := stage
	if detail != "" {
		label += "：" + detail
	}
	return fmt.Sprintf("%s\n已耗时 %s · 发送 /stop 可取消", label, elapsed)
}

func (s *feishuStreamSender) nextUpdateLocked() (string, int) {
	s.sequence++
	s.lastUpdate = time.Now()
	return s.content, s.sequence
}

func (s *feishuStreamSender) update(content string, sequence int) error {
	if s == nil || s.adapter == nil {
		return fmt.Errorf("feishu: stream sender is not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), feishuStreamRequestTimeout)
	defer cancel()
	if err := s.adapter.updateStreamingCard(ctx, s.cardID, content, sequence); err != nil {
		log.Printf("[feishu] streaming card update failed: %v", err)
		return errors.Join(err, s.closeStreamingMode())
	}
	return nil
}

func (s *feishuStreamSender) closeStreamingMode() error {
	if s == nil || s.adapter == nil {
		return fmt.Errorf("feishu: stream sender is not initialized")
	}
	s.mu.Lock()
	if s.streamClosed {
		s.mu.Unlock()
		return nil
	}
	s.sequence++
	sequence := s.sequence
	s.mu.Unlock()
	settings, err := json.Marshal(map[string]any{"config": map[string]bool{"streaming_mode": false}})
	if err != nil {
		return fmt.Errorf("feishu: encode streaming card settings: %w", err)
	}
	request := map[string]any{
		"settings": string(settings),
		"sequence": sequence,
		"uuid":     uuid.NewString(),
	}
	path := "/open-apis/cardkit/v1/cards/" + url.PathEscape(s.cardID) + "/settings"
	ctx, cancel := context.WithTimeout(context.Background(), feishuStreamRequestTimeout)
	defer cancel()
	var response cardKitResponse
	if err := s.adapter.authorizedJSON(ctx, http.MethodPatch, path, nil, request, &response); err != nil {
		return fmt.Errorf("feishu: close streaming card: %w", err)
	}
	if response.Code != 0 {
		return fmt.Errorf("feishu: close streaming card API code %d: %s", response.Code, strings.TrimSpace(response.Msg))
	}
	s.mu.Lock()
	s.streamClosed = true
	s.mu.Unlock()
	return nil
}

var _ gateway.StreamGateway = (*Adapter)(nil)
var _ gateway.StreamSender = (*feishuStreamSender)(nil)
