package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent/internal/autonomy"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type foregroundProvider struct {
	chat   func(context.Context, []provider.Message) (*provider.Response, error)
	stream func(context.Context, []provider.Message) (<-chan provider.StreamChunk, error)
}

func (p *foregroundProvider) Name() string    { return "foreground-test" }
func (p *foregroundProvider) Validate() error { return nil }
func (p *foregroundProvider) Chat(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
	return p.chat(ctx, messages)
}
func (p *foregroundProvider) ChatStream(ctx context.Context, messages []provider.Message) (<-chan provider.StreamChunk, error) {
	if p.stream != nil {
		return p.stream(ctx, messages)
	}
	r, err := p.chat(ctx, messages)
	if err != nil {
		return nil, err
	}
	ch := make(chan provider.StreamChunk, len(r.ToolCalls)+2)
	if r.Content != "" {
		ch <- provider.StreamChunk{Content: r.Content}
	}
	for i, c := range r.ToolCalls {
		ch <- provider.StreamChunk{ToolCallDeltas: []provider.StreamToolCallDelta{{Index: i, ID: c.ID, Name: c.Name, Arguments: c.Arguments}}}
	}
	ch <- provider.StreamChunk{Done: true, FinishReason: "stop"}
	close(ch)
	return ch, nil
}

func foregroundHarness(t *testing.T) (*Agent, *session.Session, LoopConfig) {
	t.Helper()
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"api_key": "test", "agent.foreground.max_retries": "0", "agent.foreground.max_slices": "32", "agent.max_iterations": "2"} {
		if err := mgr.Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	a, err := New(mgr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	cfg := DefaultLoopConfig()
	ApplyAgentLoopConfig(&cfg, mgr.Get().Agent)
	cfg.AutoApprove = true
	return a, a.Sessions().New(), cfg
}

func scriptedForeground(p *foregroundProvider, steps int) {
	p.chat = func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		done := 0
		for _, m := range messages {
			if m.Role == "tool" && m.Name == "perform_operation" {
				done++
			}
		}
		if done < steps {
			return &provider.Response{ToolCalls: []provider.ToolCall{{ID: fmt.Sprintf("call-%d", done), Name: "perform_operation", Arguments: fmt.Sprintf("{\"step\":%d}", done)}}}, nil
		}
		return &provider.Response{Content: "全部步骤已完成"}, nil
	}
}

func TestForegroundContinuesInRequestAndStreams(t *testing.T) {
	for _, mode := range []string{"sync", "native", "simulated"} {
		t.Run(mode, func(t *testing.T) {
			a, sess, cfg := foregroundHarness(t)
			p := &foregroundProvider{}
			scriptedForeground(p, 6)
			a.provider = p
			executed := 0
			a.Tools().Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(args map[string]any) (string, error) { executed++; return fmt.Sprint(args["step"]), nil }})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "sync" {
				result, err := a.ChatWithSession(ctx, sess.ID, "执行全部六个步骤并核对结果")
				if err != nil || result != "全部步骤已完成" {
					t.Fatalf("result=%q err=%v", result, err)
				}
			} else {
				_ = a.cfg.Set("stream_mode", mode)
				events, err := a.ChatWithSessionStreamWithLoopConfig(ctx, sess.ID, "执行全部六个步骤并核对结果", cfg)
				if err != nil {
					t.Fatal(err)
				}
				done, tools, content := 0, 0, 0
				for event := range events {
					if event.Err != nil {
						t.Fatal(event.Err)
					}
					switch event.Type {
					case ChatEventDone:
						done++
					case ChatEventToolResult:
						tools++
					case ChatEventContent:
						content++
					}
				}
				if done != 1 || tools != 6 || content == 0 {
					t.Fatalf("done=%d tools=%d content=%d", done, tools, content)
				}
			}
			if executed != 6 {
				t.Fatalf("executed=%d", executed)
			}
			q, err := a.openForeground(sess)
			if err != nil {
				t.Fatal(err)
			}
			tasks := q.ListAll()
			if len(tasks) != 1 || tasks[0].State != autonomy.TaskDone || !tasks[0].Verified || tasks[0].Continuations < 2 {
				t.Fatalf("tasks=%+v", tasks)
			}
			if len(a.autonomy.Queue().ListAll()) > 0 {
				t.Fatal("foreground task leaked into background queue")
			}
			replayed, err := a.RunLoopWithSession(ctx, sess, "继续前台任务 "+tasks[0].ID, cfg)
			if err != nil || replayed.Response != "全部步骤已完成" || executed != 6 {
				t.Fatalf("completed result replay=%+v err=%v executions=%d", replayed, err, executed)
			}
		})
	}
}

func TestForegroundCancellationResumesWithoutReplayingTool(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	p := &foregroundProvider{}
	scriptedForeground(p, 1)
	a.provider = p
	ctx, cancel := context.WithCancel(context.Background())
	executed := 0
	a.Tools().Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { executed++; cancel(); return "confirmed result", nil }})
	_, err := a.RunLoopWithSession(ctx, sess, "完成操作并核对", cfg)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation: %v", err)
	}
	// A new Agent reloads both the session and the task file, like a restart.
	restarted, err := New(a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.provider = p
	restarted.Tools().Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { executed++; return "unexpected replay", nil }})
	restored, ok := restarted.Sessions().Get(sess.ID)
	if !ok {
		t.Fatal("session not persisted")
	}
	result, err := restarted.RunLoopWithSession(context.Background(), restored, "继续前台任务", cfg)
	if err != nil || result.Response != "全部步骤已完成" || executed != 1 {
		t.Fatalf("result=%+v executions=%d err=%v", result, executed, err)
	}
}

func TestForegroundInterruptedSideEffectRequiresExplicitResolution(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	p := &foregroundProvider{}
	scriptedForeground(p, 1)
	a.provider = p
	executed := 0
	a.Tools().Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { executed++; return "", context.DeadlineExceeded }})
	_, err := a.RunLoopWithSession(context.Background(), sess, "完成操作并核对", cfg)
	if err == nil {
		t.Fatal("ambiguous operation completed")
	}
	_, err = a.RunLoopWithSession(context.Background(), sess, "继续前台任务", cfg)
	if err == nil || executed != 1 {
		t.Fatalf("ambiguous operation was replayed: %v %d", err, executed)
	}
	q, _ := a.openForeground(sess)
	task := q.ListAll()[0]
	resolve := fmt.Sprintf("处理前台操作 {\"task_id\":%q,\"operation_id\":\"step-1-1\",\"resolution\":\"completed\",\"result\":\"operator confirmed success\"}", task.ID)
	if _, err = a.RunLoopWithSession(context.Background(), sess, resolve, cfg); err != nil {
		t.Fatal(err)
	}
	result, err := a.RunLoopWithSession(context.Background(), sess, "继续前台任务", cfg)
	if err != nil || !result.Verified || executed != 1 {
		t.Fatalf("result=%+v executions=%d err=%v", result, executed, err)
	}
}

func TestForegroundBudgetAndErrorsNeverFallBackToSuccess(t *testing.T) {
	a, sess, _ := foregroundHarness(t)
	_ = a.cfg.Set("agent.foreground.max_slices", "1")
	p := &foregroundProvider{}
	scriptedForeground(p, 3)
	a.provider = p
	a.Tools().Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { return "result", nil }})
	_, err := a.ChatWithSession(context.Background(), sess.ID, "执行全部步骤")
	if err == nil || !strings.Contains(err.Error(), "未完成") {
		t.Fatalf("budget exhaustion became success: %v", err)
	}
	q, _ := a.openForeground(sess)
	if tasks := q.ListAll(); len(tasks) != 1 || tasks[0].State != autonomy.TaskBlocked {
		t.Fatalf("tasks=%+v", tasks)
	}
}

func TestForegroundRetryRetainsEvidence(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	_ = a.cfg.Set("agent.foreground.max_retries", "1")
	p := &foregroundProvider{}
	scriptedForeground(p, 1)
	script := p.chat
	failed := false
	p.chat = func(ctx context.Context, messages []provider.Message) (*provider.Response, error) {
		for _, m := range messages {
			if m.Role == "tool" && !failed {
				failed = true
				return nil, errors.New("temporary upstream failure")
			}
		}
		return script(ctx, messages)
	}
	a.provider = p
	executed := 0
	a.Tools().Register(&tool.Tool{Name: "perform_operation", Enabled: true, Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) { executed++; return "confirmed", nil }})
	result, err := a.RunLoopWithSession(context.Background(), sess, "执行操作", cfg)
	if err != nil || !result.Verified || executed != 1 {
		t.Fatalf("result=%+v executions=%d err=%v", result, executed, err)
	}
	q, _ := a.openForeground(sess)
	if q.ListAll()[0].Retries != 1 {
		t.Fatal("retry not accounted")
	}
}

func TestForegroundTotalDeadlineStopsModel(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	_ = a.cfg.Set("agent.foreground.max_total_seconds", "1")
	a.provider = &foregroundProvider{chat: func(ctx context.Context, _ []provider.Message) (*provider.Response, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	start := time.Now()
	_, err := a.RunLoopWithSession(context.Background(), sess, "等待长时间结果", cfg)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("deadline failed: %v", err)
	}
	q, _ := a.openForeground(sess)
	if q.ListAll()[0].State == autonomy.TaskDone {
		t.Fatal("deadline became completion")
	}
}

func TestForegroundControlMatchesLiteralInputAndScope(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	calls := 0
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		calls++
		return nil, errors.New("unavailable")
	}}
	scope := TurnScope{Platform: "telegram", ChatID: "group", SenderID: "alice"}
	_, err := a.RunLoopWithSessionInput(context.Background(), sess, TextUserTurnInput("完成我的任务").WithScope(scope), cfg)
	if err == nil {
		t.Fatal("expected interrupted task")
	}
	status := TextUserTurnInput("查看前台任务").WithScope(scope).WithRoutingText("[Alice]: 查看前台任务\nGateway media guidance")
	result, err := a.RunLoopWithSessionInput(context.Background(), sess, status, cfg)
	if err != nil || !strings.Contains(result.Response, "完成我的任务") || calls != 1 {
		t.Fatalf("literal command lost: %+v %v calls=%d", result, err, calls)
	}
	other := TextUserTurnInput("继续前台任务").WithScope(TurnScope{Platform: "telegram", ChatID: "group", SenderID: "bob"})
	if _, err := a.RunLoopWithSessionInput(context.Background(), sess, other, cfg); err == nil || calls != 1 {
		t.Fatalf("resumed another sender's task: %v", err)
	}
}

func TestForegroundConcurrentRequestAndCancellation(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	started := make(chan struct{})
	var calls atomic.Int32
	a.provider = &foregroundProvider{chat: func(ctx context.Context, _ []provider.Message) (*provider.Response, error) {
		calls.Add(1)
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := a.RunLoopWithSession(ctx, sess, "长时间任务", cfg); finished <- err }()
	<-started
	if _, err := a.RunLoopWithSession(context.Background(), sess, "另一项任务", cfg); err == nil {
		t.Fatal("concurrent same-session execution allowed")
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not stop caller")
	}
	if calls.Load() != 1 {
		t.Fatalf("unexpected retry after cancellation: %d", calls.Load())
	}
}

func TestForegroundDeadlineSurvivesStreamBackpressure(t *testing.T) {
	a, sess, cfg := foregroundHarness(t)
	_ = a.cfg.Set("agent.foreground.max_total_seconds", "1")
	expired := make(chan struct{})
	a.provider = &foregroundProvider{stream: func(ctx context.Context, _ []provider.Message) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk, 201)
		for i := 0; i < 200; i++ {
			ch <- provider.StreamChunk{Content: "draft "}
		}
		ch <- provider.StreamChunk{Done: true, FinishReason: "stop"}
		close(ch)
		go func() { <-ctx.Done(); close(expired) }()
		return ch, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := a.ChatWithSessionStreamWithLoopConfig(ctx, sess.ID, "输出长文本", cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately leave the event buffer unread. A slow UI cannot extend
	// execution indefinitely past the configured deadline.
	select {
	case <-expired:
	case <-time.After(3 * time.Second):
		t.Fatal("model context did not expire")
	}
	deadline := time.Now().Add(time.Second)
	for {
		if _, active := a.foregroundActive.Load(sess.ID); !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("foreground execution stuck sending progress")
		}
		time.Sleep(5 * time.Millisecond)
	}
	errorsSeen := 0
	for event := range events {
		if event.Type == ChatEventDone {
			t.Fatal("timed-out draft became a success")
		}
		if event.Type == ChatEventError {
			errorsSeen++
		}
	}
	if errorsSeen != 1 {
		t.Fatalf("error events=%d", errorsSeen)
	}
}
