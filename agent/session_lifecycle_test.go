package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

func TestCorruptSessionStopsBeforeProviderAndFallback(t *testing.T) {
	for _, mode := range []string{"sync", "stream", "loop"} {
		t.Run(mode, func(t *testing.T) {
			a, sess, cfg := foregroundHarness(t)
			_ = a.cfg.Set("agent.foreground.enabled", "false")
			cfg.Foreground = false
			sess.AddMessage("user", "must preserve")
			if err := sess.Save(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.sessions.Dir(), sess.ID, "messages.jsonl")
			before, _ := os.ReadFile(path)
			broken := append(before, []byte("{invalid}\n")...)
			if err := os.WriteFile(path, broken, 0600); err != nil {
				t.Fatal(err)
			}
			cold, err := session.NewManager(a.sessions.Dir())
			if err != nil {
				t.Fatal(err)
			}
			a.sessions = cold
			sess, _ = cold.Get(sess.ID)
			var calls atomic.Int32
			a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
				calls.Add(1)
				return &provider.Response{Content: "must not run"}, nil
			}}
			switch mode {
			case "sync":
				_, err = a.ChatWithSession(context.Background(), sess.ID, "new request")
			case "stream":
				_, err = a.ChatWithSessionStreamWithLoopConfig(context.Background(), sess.ID, "new request", cfg)
			case "loop":
				_, err = a.RunLoopWithSession(context.Background(), sess, "new request", cfg)
			}
			if err == nil || !isSessionError(err) || calls.Load() != 0 {
				t.Fatalf("error=%v provider calls=%d", err, calls.Load())
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(broken) {
				t.Fatal("failed chat changed corrupt history")
			}
		})
	}
}

func TestSessionPersistenceFailureCannotCompleteChat(t *testing.T) {
	for _, mode := range []string{"loop", "native", "simulated"} {
		t.Run(mode, func(t *testing.T) {
			a, sess, cfg := foregroundHarness(t)
			cfg.Foreground = false
			sess.AddMessage("user", "earlier turn")
			if err := sess.Save(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(a.sessions.Dir(), sess.ID, "messages.jsonl")
			// Block append only after the model has been called, exercising the
			// final persistence boundary instead of the initial history read.
			a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
				if err := os.Remove(path); err != nil {
					return nil, err
				}
				if err := os.Mkdir(path, 0700); err != nil {
					return nil, err
				}
				return &provider.Response{Content: "answer"}, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "loop" {
				_, err := a.RunLoopWithSession(ctx, sess, "question", cfg)
				if err == nil || !isSessionError(err) {
					t.Fatalf("persistence failure=%v", err)
				}
				return
			}
			_ = a.cfg.Set("stream_mode", mode)
			events, err := a.ChatWithSessionStreamWithLoopConfig(ctx, sess.ID, "question", cfg)
			if err != nil {
				t.Fatal(err)
			}
			sawError := false
			for event := range events {
				if event.Type == ChatEventDone {
					t.Fatal("failed persistence emitted successful done")
				}
				if event.Type == ChatEventError && isSessionError(event.Err) {
					sawError = true
				}
			}
			if !sawError {
				t.Fatal("storage error was not reported")
			}
		})
	}
}

func TestCancelSessionStopsCallsWithoutFallback(t *testing.T) {
	for _, mode := range []string{"sync", "stream", "loop"} {
		t.Run(mode, func(t *testing.T) {
			a, sess, cfg := foregroundHarness(t)
			_ = a.cfg.Set("agent.foreground.enabled", "false")
			cfg.Foreground = false
			started := make(chan struct{}, 2)
			var calls atomic.Int32
			a.provider = &foregroundProvider{chat: func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
				if isIntentClassifyCall(messages) {
					return &provider.Response{Content: `{"profile":"tool_task"}`}, nil
				}
				calls.Add(1)
				started <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				var err error
				switch mode {
				case "sync":
					_, err = a.ChatWithSession(ctx, sess.ID, "run")
				case "loop":
					_, err = a.RunLoopWithSession(ctx, sess, "run", cfg)
				case "stream":
					var events <-chan ChatEvent
					events, err = a.ChatWithSessionStreamWithLoopConfig(ctx, sess.ID, "run", cfg)
					if err == nil {
						for evt := range events {
							if evt.Type == ChatEventError {
								err = evt.Err
							}
							if evt.Type == ChatEventDone {
								err = errors.New("cancelled chat completed successfully")
							}
						}
						// A cancelled stream may close before sending its error.
						if err == nil {
							err = context.Canceled
						}
					}
				}
				result <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("provider never started")
			}
			if !a.CancelSession(sess.ID) {
				t.Fatal("active call was not registered")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel error=%v", err)
				}
			case <-ctx.Done():
				t.Fatal("cancelled call did not exit")
			}
			if calls.Load() != 1 || a.CancelSession(sess.ID) {
				t.Fatalf("calls=%d or active registration leaked", calls.Load())
			}
		})
	}
}

func TestToolHistoryWriteFailureStopsFurtherModelCalls(t *testing.T) {
	for _, mode := range []string{"loop", "native", "simulated", "foreground"} {
		t.Run(mode, func(t *testing.T) {
			a, sess, cfg := foregroundHarness(t)
			cfg.Foreground = mode == "foreground"
			sess.AddMessage("user", "earlier turn")
			if err := sess.Save(); err != nil {
				t.Fatal(err)
			}
			a.Tools().Register(&tool.Tool{
				Name: "large_result", Enabled: true, Permission: tool.PermAuto,
				Handler: func(map[string]any) (string, error) { return strings.Repeat("x", 70*1024), nil },
			})
			var calls atomic.Int32
			a.provider = &foregroundProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
				if isIntentClassifyCall(messages) {
					return &provider.Response{Content: `{"profile":"tool_task"}`}, nil
				}
				if calls.Add(1) > 1 {
					return &provider.Response{Content: "must not continue"}, nil
				}
				path := filepath.Join(a.sessions.Dir(), sess.ID, "blobs")
				if err := os.RemoveAll(path); err != nil {
					return nil, err
				}
				if err := os.WriteFile(path, []byte("blocked"), 0600); err != nil {
					return nil, err
				}
				return &provider.Response{ToolCalls: []provider.ToolCall{{ID: "large-call", Name: "large_result", Arguments: "{}"}}}, nil
			}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "loop" || mode == "foreground" {
				_, err := a.RunLoopWithSession(ctx, sess, "call large_result", cfg)
				if err == nil || !isSessionError(err) {
					t.Fatalf("tool history error=%v", err)
				}
			} else {
				_ = a.cfg.Set("stream_mode", mode)
				events, err := a.ChatWithSessionStreamWithLoopConfig(ctx, sess.ID, "call large_result", cfg)
				if err != nil {
					t.Fatal(err)
				}
				sawError := false
				for evt := range events {
					if evt.Type == ChatEventDone {
						t.Fatal("failed tool history emitted done")
					}
					if evt.Type == ChatEventError && isSessionError(evt.Err) {
						sawError = true
					}
				}
				if !sawError {
					t.Fatal("tool history error was hidden")
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("continued after failed history write: calls=%d", calls.Load())
			}
		})
	}
}

func TestBoundSessionCallsCannotAdoptReusedID(t *testing.T) {
	a, old, cfg := foregroundHarness(t)
	old.AddMessage("user", "old conversation")
	if err := old.Save(); err != nil {
		t.Fatal(err)
	}
	if err := a.Sessions().Delete(old.ID); err != nil {
		t.Fatal(err)
	}
	replacement := a.Sessions().Ensure(old.ID)
	replacement.AddMessage("user", "new conversation")
	if err := replacement.Save(); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		calls.Add(1)
		return &provider.Response{Content: "must not run"}, nil
	}}
	if _, err := a.RunLoopWithSession(context.Background(), old, "late sync", cfg); !errors.Is(err, session.ErrSessionDeleted) {
		t.Fatalf("old sync handle error=%v", err)
	}
	if _, err := a.ChatWithSessionStreamForSession(context.Background(), old, TextUserTurnInput("late stream"), cfg); !errors.Is(err, session.ErrSessionDeleted) {
		t.Fatalf("old stream handle error=%v", err)
	}
	got, err := replacement.GetMessagesWithError()
	if err != nil || len(got) != 1 || got[0].Content != "new conversation" || calls.Load() != 0 {
		t.Fatalf("replacement=%v error=%v provider calls=%d", got, err, calls.Load())
	}
	if a.CancelSession(old.ID) {
		t.Fatal("rejected bound call left an active registration")
	}
}

func TestDeleteSessionCleanupFailureDoesNotReportSuccess(t *testing.T) {
	repo := initTempGitRepo(t)
	item, err := createDelegateWorktree(repo, "task-delete")
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(item.Path, "locked-dir")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(item.Path, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(item.Path, 0o755) })
	a := &Agent{}
	a.worktrees.remember("task-delete", item)
	a.rememberDelegateOwner("task-delete", "sess-a")
	if a.DeleteSession("sess-a") {
		t.Fatal("cleanup failure reported delete success")
	}
	if a.SessionCleanupError() == nil {
		t.Fatal("cleanup error was discarded")
	}
	if _, ok := a.worktrees.get("task-delete"); !ok {
		t.Fatal("failed cleanup forgot the worktree")
	}
}

func TestDeleteSessionRemovesOwnedWorktree(t *testing.T) {
	repo := initTempGitRepo(t)
	item, err := createDelegateWorktree(repo, "task-owned")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(item.Path, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &Agent{}
	a.worktrees.remember("task-owned", item)
	a.rememberDelegateOwner("task-owned", "sess-a")
	a.rememberDelegateOwner("task-other", "sess-b")
	if !a.DeleteSession("sess-a") && a.SessionCleanupError() != nil {
		t.Fatal(a.SessionCleanupError())
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Fatalf("owned worktree remains: %v", err)
	}
	if _, ok := a.worktrees.get("task-owned"); ok {
		t.Fatal("owned worktree stayed registered")
	}
}
