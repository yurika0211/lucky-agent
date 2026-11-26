package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/session"
)

func TestCronAndroidDeliveryUsesDurableResultWithoutSessionPush(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewManager(dir)
	sess := sessions.New()
	var pushed bool
	a := &Agent{
		sessions: sessions,
		sessionEvents: func(string, string) {
			pushed = true
		},
	}

	err := a.sendCronNotification(map[string]string{
		"session_id": sess.ID,
		"platform":   "android",
	}, cronNotificationPayload{
		JobID:     "job-android",
		Mode:      "agent",
		Command:   "检查服务",
		Outcome:   "succeeded",
		RawResult: "服务正常",
	})
	if err != nil {
		t.Fatal(err)
	}
	if pushed {
		t.Fatal("android cron result must not push a live session event")
	}
	reloaded, ok := sessions.Get(sess.ID)
	if !ok {
		t.Fatal("session missing")
	}
	messages := reloaded.GetMessages()
	if len(messages) != 0 {
		t.Fatalf("android cron result was appended to session: %#v", messages)
	}
	if got := a.cronResultPayloads["job-android"].RawResult; got != "服务正常" {
		t.Fatalf("durable payload = %q, want 服务正常", got)
	}
}

func TestCronAppDeliveryStillPushesSession(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewManager(dir)
	sess := sessions.New()
	var gotSession, got string
	a := &Agent{
		sessions: sessions,
		sessionEvents: func(sessionID, content string) {
			gotSession = sessionID
			got = content
		},
	}

	err := a.sendCronNotification(map[string]string{
		"session_id": sess.ID,
		"platform":   "app",
	}, cronNotificationPayload{
		JobID:     "job-app",
		Mode:      "agent",
		Command:   "检查服务",
		Outcome:   "succeeded",
		RawResult: "服务正常",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotSession != sess.ID || !strings.Contains(got, "服务正常") {
		t.Fatalf("push = session %q content %q", gotSession, got)
	}
}

func TestDeliverCronSessionDoesNotRequireGateway(t *testing.T) {
	dir := t.TempDir()
	sessions := session.NewManager(dir)
	sess := sessions.New()
	a := &Agent{sessions: sessions}
	err := a.sendCronNotification(map[string]string{"session_id": sess.ID}, cronNotificationPayload{
		JobID:     "job-session",
		Mode:      "shell",
		Command:   "date",
		Outcome:   "succeeded",
		RawResult: "ok",
	})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, _ := sessions.Get(sess.ID)
	if len(reloaded.GetMessages()) == 0 {
		t.Fatal("expected session message without a gateway")
	}
}
