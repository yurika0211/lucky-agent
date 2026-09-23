package gateway

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireTelegramGatewayLeaseRejectsFreshRuntime(t *testing.T) {
	homeDir := t.TempDir()
	if err := WriteSharedTelegramState(homeDir, SharedTelegramState{PID: 42, Registered: true, Connected: true}); err != nil {
		t.Fatalf("write state: %v", err)
	}

	if _, err := AcquireTelegramGatewayLease(homeDir); err == nil {
		t.Fatal("expected active runtime to prevent a second Telegram gateway")
	}
}

func TestAcquireTelegramGatewayLeaseRecoversStaleLease(t *testing.T) {
	homeDir := t.TempDir()
	leasePath := telegramGatewayLeasePath(homeDir)
	if err := os.MkdirAll(filepath.Dir(leasePath), 0o700); err != nil {
		t.Fatalf("make runtime dir: %v", err)
	}
	if err := os.WriteFile(leasePath, []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("write stale lease: %v", err)
	}
	staleAt := time.Now().Add(-telegramGatewayLeaseMaxAge - time.Second)
	if err := os.Chtimes(leasePath, staleAt, staleAt); err != nil {
		t.Fatalf("age stale lease: %v", err)
	}

	release, err := AcquireTelegramGatewayLease(homeDir)
	if err != nil {
		t.Fatalf("acquire lease after stale process: %v", err)
	}
	if _, err := os.Stat(leasePath); err != nil {
		t.Fatalf("lease does not exist after acquire: %v", err)
	}
	release()
	release()
	if _, err := os.Stat(leasePath); !os.IsNotExist(err) {
		t.Fatalf("lease still exists after release: %v", err)
	}
}

func TestTimeoutEventRoundTrip(t *testing.T) {
	homeDir := t.TempDir()
	want := TimeoutEvent{Layer: "Telegram Gateway Chat", ConfigPath: "msg_gateway.telegram.chat_timeout_seconds", ConfiguredSeconds: 600, Error: "context deadline exceeded"}
	if err := WriteTimeoutEvent(homeDir, want); err != nil {
		t.Fatalf("WriteTimeoutEvent: %v", err)
	}
	got, err := ReadTimeoutEvent(homeDir)
	if err != nil {
		t.Fatalf("ReadTimeoutEvent: %v", err)
	}
	if got.Layer != want.Layer || got.ConfigPath != want.ConfigPath || got.ConfiguredSeconds != want.ConfiguredSeconds || got.Error != want.Error || got.UpdatedAt.IsZero() {
		t.Fatalf("unexpected timeout event: %#v", got)
	}
	events, err := ReadTimeoutEventsSince(homeDir, time.Now().Add(-time.Minute))
	if err != nil || len(events) != 1 {
		t.Fatalf("ReadTimeoutEventsSince() = %#v, %v", events, err)
	}
}

func TestRecentChatTargetRoundTrip(t *testing.T) {
	homeDir := t.TempDir()
	target := RecentChatTarget{Platform: " feishu ", ChatID: " oc_chat ", ReplyToMsgID: " om_message "}
	if err := WriteRecentChatTarget(homeDir, target); err != nil {
		t.Fatalf("WriteRecentChatTarget: %v", err)
	}
	got, err := ReadRecentChatTarget(homeDir)
	if err != nil {
		t.Fatalf("ReadRecentChatTarget: %v", err)
	}
	if got.Platform != "feishu" || got.ChatID != "oc_chat" || got.ReplyToMsgID != "om_message" || got.UpdatedAt.IsZero() {
		t.Fatalf("unexpected recent target: %#v", got)
	}
	if time.Since(got.UpdatedAt) > time.Minute {
		t.Fatalf("recent target timestamp is unexpectedly old: %v", got.UpdatedAt)
	}
}
