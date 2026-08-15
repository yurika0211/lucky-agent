package telegram

import "testing"

func TestDecodeTelegramTopicUpdatesPreservesMessageThreadID(t *testing.T) {
	updates, err := decodeTelegramTopicUpdates([]byte(`[
		{"update_id":101,"message":{"message_id":7,"message_thread_id":42,"date":1,"chat":{"id":-100123,"type":"supergroup"},"from":{"id":5,"is_bot":false,"first_name":"Ada"},"text":"first"}},
		{"update_id":102,"message":{"message_id":8,"date":2,"chat":{"id":-100123,"type":"supergroup"},"from":{"id":5,"is_bot":false,"first_name":"Ada"},"text":"general"}}
	]`))
	if err != nil {
		t.Fatalf("decode topic updates: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("got %d updates, want 2", len(updates))
	}
	if got := updates[0].MessageThreadID; got != "42" {
		t.Fatalf("first update thread ID = %q, want 42", got)
	}
	if updates[0].Message == nil || updates[0].Message.Text != "first" {
		t.Fatalf("first update lost message payload: %#v", updates[0].Message)
	}
	if got := updates[1].MessageThreadID; got != "" {
		t.Fatalf("general update thread ID = %q, want empty", got)
	}
}

func TestDecodeTelegramTopicUpdatesRejectsInvalidPayload(t *testing.T) {
	if _, err := decodeTelegramTopicUpdates([]byte(`{"update_id":101}`)); err == nil {
		t.Fatal("expected invalid update-list payload to fail")
	}
}
