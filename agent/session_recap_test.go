package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

func awaitRecap(t *testing.T, sess *session.Session, want string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if got, _ := sess.Recap(); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("recap never became %q", want)
		case <-tick.C:
		}
	}
}

func TestRecapUsesEvidenceAndRejectsInvalidOutput(t *testing.T) {
	input := recapEvidence("earlier recap", []provider.Message{
		{Role: "system", Content: "private system"},
		session.CompactBoundaryMessage(session.CompactMetadata{Summary: "compact secret"}),
		{Role: "user", Content: "修复 foo.go", ReasoningContent: "private reasoning"},
		{Role: "tool", Name: "edit_file", Status: "error", Content: "permission denied"},
		{Role: "assistant", Content: "修改失败。"},
	})
	for _, want := range []string{"earlier recap", "修复 foo.go", "edit_file error", "permission denied", "修改失败"} {
		if !strings.Contains(input, want) {
			t.Fatal("missing", want, input)
		}
	}
	for _, forbidden := range []string{"private system", "compact secret", "private reasoning"} {
		if strings.Contains(input, forbidden) {
			t.Fatal("leaked", forbidden)
		}
	}
	for _, text := range []string{"", "Recap: hello", "回顾：你好", "- list", "first\nsecond", "第一句。第二句。", "One. Two.", strings.Repeat("x", 501)} {
		if validRecap(text) != "" {
			t.Fatal("accepted", text)
		}
	}
	for _, text := range []string{"你问了 foo.go 为什么失败。", "We updated config.json.", "会话刚刚开始。"} {
		if validRecap(text) != text {
			t.Fatal("rejected", text)
		}
	}
}

func TestRecapFailurePreservesPreviousAndLatestTurnWins(t *testing.T) {
	s := session.NewSession("recap", t.TempDir())
	s.AddMessage("user", "first")
	s.AddMessage("assistant", "first answer")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	_, _, version, _ := s.RecapSnapshot(64)
	if _, err := s.UpdateRecap("Previous recap.", version); err != nil {
		t.Fatal(err)
	}
	s.AddMessage("user", "second")
	s.AddMessage("assistant", "second answer")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	failed := newRecapService()
	failed.enqueue(s, &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return nil, errors.New("offline")
	}})
	failed.close()
	if text, _ := s.Recap(); text != "Previous recap." {
		t.Fatal(text)
	}
	started, release := make(chan struct{}), make(chan struct{})
	service := newRecapService()
	t.Cleanup(service.close)
	service.enqueue(s, &foregroundProvider{chat: func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
		if len(messages) != 2 || messages[0].Content != recapSystemPrompt {
			t.Error("unexpected model context")
		}
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &provider.Response{Content: "Outdated recap."}, nil
	}})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	s.AddMessage("user", "third")
	s.AddMessage("assistant", "third answer")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	service.enqueue(s, &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		return &provider.Response{Content: "You asked about the third turn."}, nil
	}})
	close(release)
	awaitRecap(t, s, "You asked about the third turn.")
}

func TestSuccessfulTurnsQueueRecapsWithoutBlocking(t *testing.T) {
	for _, mode := range []string{"sync", "native", "simulated", "foreground", "ephemeral"} {
		t.Run(mode, func(t *testing.T) {
			a, s, cfg := foregroundHarness(t)
			a.recaps = newRecapService()
			t.Cleanup(a.recaps.close)
			release := make(chan struct{})
			defer close(release)
			a.provider = &foregroundProvider{chat: func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
				if len(messages) > 0 && messages[0].Content == recapSystemPrompt {
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return &provider.Response{Content: "你问了如何开始。"}, nil
				}
				return &provider.Response{Content: "Hello"}, nil
			}}
			if mode != "foreground" {
				cfg.Foreground = false
				_ = a.cfg.Set("agent.foreground.enabled", "false")
			}
			cfg.Ephemeral = mode == "ephemeral"
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if mode == "sync" || mode == "foreground" {
				if _, err := a.ChatWithSession(ctx, s.ID, "你好"); err != nil {
					t.Fatal(err)
				}
			} else {
				streamMode := mode
				if mode == "ephemeral" {
					streamMode = "native"
				}
				_ = a.cfg.Set("stream_mode", streamMode)
				events, err := a.ChatWithSessionStreamWithLoopConfig(ctx, s.ID, "你好", cfg)
				if err != nil {
					t.Fatal(err)
				}
				done := false
				for event := range events {
					if event.Type == ChatEventError {
						t.Fatal(event.Err)
					}
					if event.Type == ChatEventDone {
						done = true
					}
				}
				if !done {
					t.Fatal("missing done")
				}
			}
			// The model is still blocked on release, yet the user turn completed.
			a.recaps.mu.Lock()
			queued := a.recaps.running[s]
			a.recaps.mu.Unlock()
			if mode == "ephemeral" && queued {
				t.Fatal("ephemeral turn queued a recap")
			}
			if mode != "ephemeral" && !queued {
				t.Fatal("turn did not queue a recap")
			}
		})
	}
}
