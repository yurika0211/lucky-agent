package telegram

import (
	"encoding/json"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// telegramTopicUpdate preserves forum-topic metadata omitted by tgbotapi v5.5.1.
// The embedded Update remains the compatibility boundary for the rest of the adapter.
type telegramTopicUpdate struct {
	tgbotapi.Update
	MessageThreadID string
}

func decodeTelegramTopicUpdates(payload json.RawMessage) ([]telegramTopicUpdate, error) {
	var rawUpdates []json.RawMessage
	if err := json.Unmarshal(payload, &rawUpdates); err != nil {
		return nil, fmt.Errorf("unmarshal Telegram updates: %w", err)
	}

	updates := make([]telegramTopicUpdate, 0, len(rawUpdates))
	for _, rawUpdate := range rawUpdates {
		var update tgbotapi.Update
		if err := json.Unmarshal(rawUpdate, &update); err != nil {
			return nil, fmt.Errorf("unmarshal Telegram update: %w", err)
		}

		var metadata struct {
			Message *struct {
				MessageThreadID *int `json:"message_thread_id"`
			} `json:"message"`
		}
		if err := json.Unmarshal(rawUpdate, &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal Telegram topic metadata: %w", err)
		}

		threadID := ""
		if metadata.Message != nil && metadata.Message.MessageThreadID != nil {
			threadID = strconv.Itoa(*metadata.Message.MessageThreadID)
		}
		updates = append(updates, telegramTopicUpdate{
			Update:          update,
			MessageThreadID: threadID,
		})
	}
	return updates, nil
}
