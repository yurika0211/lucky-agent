package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/session"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-tools/autonomy"
)

func TestForegroundResumeSourcesNormalizeAndFilter(t *testing.T) {
	allowed := foregroundResumeSources([]string{" Telegram ", "api", ""})
	if len(allowed) != 2 {
		t.Fatalf("allowed=%v", allowed)
	}
	if _, ok := allowed["telegram"]; !ok {
		t.Fatal("telegram source was not normalized")
	}
	if _, ok := allowed["API"]; ok {
		t.Fatal("source matching must be case-insensitive")
	}
}

func TestForegroundTaskInputAndSourcePreferMetadata(t *testing.T) {
	input := TextUserTurnInput("continue").WithScope(TurnScope{Platform: "Telegram", ChatID: "room"})
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	task := &autonomy.QueueTask{Description: string(data), Metadata: map[string]string{"foreground_source": "api"}}
	decoded, err := foregroundTaskInput(task)
	if err != nil || decoded.Scope.Platform != "telegram" {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if got := foregroundTaskSource(task, decoded); got != "api" {
		t.Fatalf("source=%q", got)
	}
	if got := foregroundTaskSource(&autonomy.QueueTask{Description: string(data)}, decoded); got != "telegram" {
		t.Fatalf("fallback source=%q", got)
	}
}

func TestForegroundQueueBindsSessionID(t *testing.T) {
	a, sess, _ := foregroundHarness(t)
	q, err := a.openForeground(sess)
	if err != nil {
		t.Fatal(err)
	}
	input := TextUserTurnInput("persist me").WithScope(TurnScope{Platform: "api"})
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := q.AddWithAcceptance(input.RoutingText, string(data), autonomy.PriorityNormal, nil, map[string]string{"foreground_source": "api"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.SessionID != sess.ID {
		t.Fatalf("session_id=%q want %q", task.SessionID, sess.ID)
	}
	if task.Metadata["foreground_source"] != "api" {
		t.Fatalf("metadata=%v", task.Metadata)
	}
}

func addForegroundTask(t *testing.T, a *Agent, sess *session.Session, source, text string) *autonomy.QueueTask {
	t.Helper()
	q, err := a.openForeground(sess)
	if err != nil {
		t.Fatal(err)
	}
	input := TextUserTurnInput(text).WithScope(TurnScope{Platform: source, ChatID: source})
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := q.AddWithAcceptance(input.RoutingText, string(data), autonomy.PriorityNormal, nil, map[string]string{"foreground_source": source}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func assertForegroundStillReady(t *testing.T, a *Agent, sess *session.Session, taskID string) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	reloaded, err := a.openForeground(sess)
	if err != nil {
		t.Fatal(err)
	}
	tasks := reloaded.ListByState(autonomy.TaskReady)
	if len(tasks) != 1 || tasks[0].ID != taskID || tasks[0].State != autonomy.TaskReady {
		t.Fatalf("task did not stay ready: %+v", tasks)
	}
}

func TestResumeForegroundEmptySourcesLeavesTaskReady(t *testing.T) {
	a, sess, _ := foregroundHarness(t)
	calls := 0
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		calls++
		return nil, errors.New("provider must not be called when resume_sources is empty")
	}}
	if sources := a.cfg.Get().Agent.Foreground.ResumeSources; len(sources) != 0 {
		t.Fatalf("default resume_sources = %v", sources)
	}
	task := addForegroundTask(t, a, sess, "api", "继续服务任务")
	if err := a.ResumeForeground(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertForegroundStillReady(t, a, sess, task.ID)
	if calls != 0 {
		t.Fatalf("provider calls=%d", calls)
	}
}

func TestResumeForegroundSkipsUndeclaredSource(t *testing.T) {
	a, sess, _ := foregroundHarness(t)
	if err := a.cfg.Set("agent.foreground.resume_sources", "api"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.provider = &foregroundProvider{chat: func(context.Context, []provider.Message) (*provider.Response, error) {
		calls++
		return nil, errors.New("provider must not be called for an undeclared source")
	}}
	task := addForegroundTask(t, a, sess, "telegram", "继续聊天任务")
	if err := a.ResumeForeground(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertForegroundStillReady(t, a, sess, task.ID)
	if calls != 0 {
		t.Fatalf("provider calls=%d", calls)
	}
}

func TestResumeForegroundOnlyRunsDeclaredSource(t *testing.T) {
	a, sess, _ := foregroundHarness(t)
	if err := a.cfg.Set("agent.foreground.resume_sources", "api"); err != nil {
		t.Fatal(err)
	}
	a.provider = &foregroundProvider{chat: func(_ context.Context, messages []provider.Message) (*provider.Response, error) {
		if len(messages) > 0 && strings.HasPrefix(messages[0].Content, "Review whether") {
			return &provider.Response{Content: verifiedAssessment}, nil
		}
		return &provider.Response{Content: "自动恢复完成"}, nil
	}}
	q, err := a.openForeground(sess)
	if err != nil {
		t.Fatal(err)
	}
	input := TextUserTurnInput("继续服务任务").WithScope(TurnScope{Platform: "api", ChatID: "service"})
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.AddWithAcceptance(input.RoutingText, string(data), autonomy.PriorityNormal, nil, map[string]string{"foreground_source": "api"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.ResumeForeground(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, active := a.foregroundActive.Load(sess.ID); !active {
			reloaded, err := a.openForeground(sess)
			if err != nil {
				t.Fatal(err)
			}
			tasks := reloaded.ListAll()
			if len(tasks) == 1 && tasks[0].State == autonomy.TaskDone {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	reloaded, _ := a.openForeground(sess)
	tasks := reloaded.ListAll()
	if len(tasks) > 0 {
		t.Fatalf("startup recovery did not finish: state=%s error=%q block=%q attempts=%d checkpoint=%d", tasks[0].State, tasks[0].Error, tasks[0].BlockReason, tasks[0].Attempts, len(tasks[0].Checkpoint))
	}
	t.Fatalf("startup recovery did not create a task")
}
