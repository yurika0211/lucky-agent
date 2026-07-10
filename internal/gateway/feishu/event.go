package feishu

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/gateway"
)

type callbackEnvelope struct {
	Schema    string         `json:"schema"`
	Header    callbackHeader `json:"header"`
	Event     messageEvent   `json:"event"`
	Type      string         `json:"type"`
	Token     string         `json:"token"`
	Challenge string         `json:"challenge"`
	Encrypt   string         `json:"encrypt"`
}

type callbackHeader struct {
	EventID    string `json:"event_id"`
	EventType  string `json:"event_type"`
	CreateTime string `json:"create_time"`
	Token      string `json:"token"`
	AppID      string `json:"app_id"`
}

type messageEvent struct {
	Sender  eventSender  `json:"sender"`
	Message eventMessage `json:"message"`
}

type eventSender struct {
	SenderID   eventUserID `json:"sender_id"`
	SenderType string      `json:"sender_type"`
}

type eventUserID struct {
	UnionID string `json:"union_id"`
	UserID  string `json:"user_id"`
	OpenID  string `json:"open_id"`
}

type eventMessage struct {
	MessageID   string         `json:"message_id"`
	RootID      string         `json:"root_id"`
	ParentID    string         `json:"parent_id"`
	CreateTime  string         `json:"create_time"`
	ChatID      string         `json:"chat_id"`
	ChatType    string         `json:"chat_type"`
	MessageType string         `json:"message_type"`
	Content     string         `json:"content"`
	Mentions    []eventMention `json:"mentions"`
}

type eventMention struct {
	Key  string      `json:"key"`
	ID   eventUserID `json:"id"`
	Name string      `json:"name"`
}

type textContent struct {
	Text string `json:"text"`
}

func (a *Adapter) convertEvent(event messageEvent) (*gateway.Message, error) {
	if strings.ToLower(strings.TrimSpace(event.Message.MessageType)) != "text" {
		return nil, nil
	}
	senderType := strings.ToLower(strings.TrimSpace(event.Sender.SenderType))
	if senderType != "" && senderType != "user" {
		return nil, nil
	}
	messageID := strings.TrimSpace(event.Message.MessageID)
	chatID := strings.TrimSpace(event.Message.ChatID)
	if messageID == "" || chatID == "" {
		return nil, fmt.Errorf("feishu: message_id and chat_id are required")
	}

	var content textContent
	if err := json.Unmarshal([]byte(event.Message.Content), &content); err != nil {
		return nil, fmt.Errorf("feishu: decode text content: %w", err)
	}

	senderIDs := event.Sender.SenderID
	senderID := firstNonEmpty(senderIDs.OpenID, senderIDs.UserID, senderIDs.UnionID)
	if senderID == "" || !a.cfg.isUserAllowed(senderIDs.OpenID, senderIDs.UserID, senderIDs.UnionID) {
		return nil, nil
	}
	if !a.cfg.isChatAllowed(chatID) {
		return nil, nil
	}

	botMentions := a.botMentions(event.Message.Mentions)
	replyToBot := a.outboundMessages.contains(strings.TrimSpace(event.Message.ParentID), a.now())
	chatType := strings.ToLower(strings.TrimSpace(event.Message.ChatType))
	msg := &gateway.Message{
		ID:        messageID,
		Text:      strings.TrimSpace(content.Text),
		Timestamp: parseFeishuTimestamp(event.Message.CreateTime, a.now()),
		Sender: gateway.User{
			ID: senderID,
		},
	}

	switch chatType {
	case "p2p":
		msg.Chat = gateway.Chat{ID: chatID, Type: gateway.ChatPrivate}
	case "group":
		msg.Chat = gateway.Chat{ID: chatID, Type: gateway.ChatGroup, Title: chatID}
		switch a.cfg.normalizedGroupTriggerMode() {
		case "none":
			return nil, nil
		case "mention":
			if len(botMentions) == 0 && !replyToBot {
				return nil, nil
			}
		}
		if len(botMentions) > 0 {
			msg.IsGroupTrigger = true
			msg.TriggerType = "mention"
		} else if replyToBot {
			msg.IsGroupTrigger = true
			msg.TriggerType = "reply"
		}
	default:
		return nil, nil
	}

	if a.cfg.RemoveAt && len(botMentions) > 0 {
		msg.Text = stripMentionMarkers(msg.Text, botMentions)
	}
	msg.IsCommand, msg.Command, msg.Args = parseCommand(msg.Text)
	if parentID := strings.TrimSpace(event.Message.ParentID); parentID != "" {
		msg.ReplyTo = &gateway.Message{ID: parentID}
	}
	return msg, nil
}

func (a *Adapter) botMentions(mentions []eventMention) []eventMention {
	if len(mentions) == 0 {
		return nil
	}
	a.mu.RLock()
	botOpenID := strings.TrimSpace(a.botOpenID)
	a.mu.RUnlock()
	if botOpenID == "" {
		return nil
	}
	matched := make([]eventMention, 0, 1)
	for _, mention := range mentions {
		if strings.TrimSpace(mention.ID.OpenID) == botOpenID {
			matched = append(matched, mention)
		}
	}
	return matched
}

func stripMentionMarkers(text string, mentions []eventMention) string {
	for _, mention := range mentions {
		if key := strings.TrimSpace(mention.Key); key != "" {
			text = strings.ReplaceAll(text, key, "")
		}
	}
	return strings.TrimSpace(text)
}

func parseCommand(text string) (bool, string, string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return false, "", ""
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return false, "", ""
	}
	command := parts[0]
	args := strings.TrimSpace(strings.TrimPrefix(text, command))
	return true, command, args
}

func parseFeishuTimestamp(raw string, fallback time.Time) time.Time {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	if value < 1_000_000_000_000 {
		return time.Unix(value, 0)
	}
	return time.UnixMilli(value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
