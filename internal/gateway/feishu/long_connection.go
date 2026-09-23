package feishu

import (
	"context"
	"log"
	"strings"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type longConnectionClient interface {
	Start(context.Context) error
	CloseAndWait(context.Context) error
}

type longConnectionFactory func(Config, func(context.Context, longConnectionEvent) error) longConnectionClient

type longConnectionEvent struct {
	ID      string
	AppID   string
	Message messageEvent
}

func newSDKLongConnection(cfg Config, receive func(context.Context, longConnectionEvent) error) longConnectionClient {
	dispatcher := larkevent.NewEventDispatcher("", "")
	dispatcher.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		incoming, ok := newLongConnectionEvent(event)
		if !ok {
			return nil
		}
		return receive(ctx, incoming)
	})

	options := []larkws.ClientOption{
		larkws.WithEventHandler(dispatcher),
		larkws.WithDomain(cfg.normalizedAPIBaseURL()),
	}
	if cfg.HTTPClient != nil {
		options = append(options, larkws.WithHttpClient(cfg.HTTPClient))
	}
	return larkws.NewClient(strings.TrimSpace(cfg.AppID), strings.TrimSpace(cfg.AppSecret), options...)
}

func newLongConnectionEvent(event *larkim.P2MessageReceiveV1) (longConnectionEvent, bool) {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Message == nil {
		return longConnectionEvent{}, false
	}

	senderID := event.Event.Sender.SenderId
	if senderID == nil {
		return longConnectionEvent{}, false
	}
	message := event.Event.Message
	incoming := longConnectionEvent{
		Message: messageEvent{
			Sender: eventSender{
				SenderID: eventUserID{
					UnionID: stringValue(senderID.UnionId),
					UserID:  stringValue(senderID.UserId),
					OpenID:  stringValue(senderID.OpenId),
				},
				SenderType: stringValue(event.Event.Sender.SenderType),
			},
			Message: eventMessage{
				MessageID:   stringValue(message.MessageId),
				RootID:      stringValue(message.RootId),
				ParentID:    stringValue(message.ParentId),
				CreateTime:  stringValue(message.CreateTime),
				ChatID:      stringValue(message.ChatId),
				ChatType:    stringValue(message.ChatType),
				MessageType: stringValue(message.MessageType),
				Content:     stringValue(message.Content),
				Mentions:    mapLongConnectionMentions(message.Mentions),
			},
		},
	}
	if event.EventV2Base != nil && event.EventV2Base.Header != nil {
		incoming.ID = strings.TrimSpace(event.EventV2Base.Header.EventID)
		incoming.AppID = strings.TrimSpace(event.EventV2Base.Header.AppID)
	}
	return incoming, true
}

func mapLongConnectionMentions(mentions []*larkim.MentionEvent) []eventMention {
	result := make([]eventMention, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil || mention.Id == nil {
			continue
		}
		result = append(result, eventMention{
			Key:  stringValue(mention.Key),
			Name: stringValue(mention.Name),
			ID: eventUserID{
				UnionID: stringValue(mention.Id.UnionId),
				UserID:  stringValue(mention.Id.UserId),
				OpenID:  stringValue(mention.Id.OpenId),
			},
		})
	}
	return result
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (a *Adapter) handleLongConnectionEvent(_ context.Context, event longConnectionEvent) error {
	if event.AppID != "" && !secureTokenEqual(event.AppID, a.cfg.AppID) {
		return nil
	}
	msg, err := a.convertEvent(event.Message)
	if err != nil || msg == nil {
		return err
	}
	if eventID := strings.TrimSpace(event.ID); eventID != "" && a.receivedEvents.seenOrAdd(eventID, a.now()) {
		return nil
	}

	a.mu.RLock()
	handlerCtx := a.runCtx
	running := a.running
	a.mu.RUnlock()
	if !running {
		return nil
	}
	if handlerCtx == nil {
		handlerCtx = context.Background()
	}
	go func() {
		if err := a.dispatchMessage(handlerCtx, msg, event.Message.Message.MessageType); err != nil {
			log.Printf("[feishu] long connection dispatch failed chat_id=%q message_id=%q: %v", msg.Chat.ID, msg.ID, err)
		}
	}()
	return nil
}
