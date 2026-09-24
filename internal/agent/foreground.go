package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent/internal/autonomy"
	"github.com/yurika0211/luckyagent/internal/config"
	"github.com/yurika0211/luckyagent/internal/provider"
	"github.com/yurika0211/luckyagent/internal/session"
)

func (a *Agent) useForeground(sess *session.Session, cfg LoopConfig) bool {
	if !cfg.Foreground || cfg.Ephemeral || cfg.Execution != nil || sess == nil || a.cfg == nil {
		return false
	}
	switch cfg.Source {
	case "autonomy", "delegate", "heartbeat", "cron":
		return false
	}
	return true
}

func sendForegroundEvent(ctx context.Context, events chan<- ChatEvent, event ChatEvent) {
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

func emitForeground(cfg LoopConfig, event ChatEvent) {
	if cfg.emit != nil {
		cfg.emit(cfg.eventContext, event)
	}
}

func (a *Agent) foregroundPolicy() autonomy.RunPolicy {
	p := autonomy.DefaultRunPolicy()
	p.MaxTotalTime, p.RetryInitial, p.RetryMax = time.Hour, time.Second, 30*time.Second
	c := config.DefaultForegroundConfig()
	if a.cfg != nil {
		c = a.cfg.Get().Agent.Foreground
	}
	if c.MaxSlices > 0 {
		p.MaxSlices = c.MaxSlices
	}
	if c.MaxTotalSeconds > 0 {
		p.MaxTotalTime = time.Duration(c.MaxTotalSeconds) * time.Second
	}
	if c.MaxRetries != nil {
		p.MaxRetries = *c.MaxRetries
	}
	return p
}

func (a *Agent) openForeground(sess *session.Session) (*autonomy.TaskQueue, error) {
	q := autonomy.NewTaskQueue(8)
	q.SetRunPolicy(a.foregroundPolicy())
	name := fmt.Sprintf("%x.json", sha256.Sum256([]byte(sess.ID)))
	_, err := q.EnablePersistence(filepath.Join(a.cfg.HomeDir(), "runtime", "foreground", name))
	if err == nil {
		err = q.CompactCompleted(10)
	}
	if err == nil {
		for _, task := range q.ListByState(autonomy.TaskReady) {
			for _, op := range task.Operations {
				if op.State != "started" {
					continue
				}
				err = q.Block(task.ID, "中断操作 "+op.ID+" 没有确认结果，需先核对")
				break
			}
			if err != nil {
				break
			}
		}
	}
	return q, err
}

// runForeground owns the entire execution lifetime. The store is deliberately
// separate from autonomy: no dispatcher ever polls these records.
func (a *Agent) runForeground(ctx context.Context, sess *session.Session, input UserTurnInput, cfg LoopConfig, snapshot providerSnapshot) (*LoopResult, error) {
	if _, loaded := a.foregroundActive.LoadOrStore(sess.ID, struct{}{}); loaded {
		return nil, fmt.Errorf("该会话已有前台任务正在运行，请先等待或取消")
	}
	defer a.foregroundActive.Delete(sess.ID)
	q, err := a.openForeground(sess)
	if err != nil {
		return nil, fmt.Errorf("前台恢复记录不可用: %w", err)
	}

	var task *autonomy.QueueTask
	text := strings.TrimSpace(input.OriginalText)
	if len(input.Attachments) > 0 {
		text = ""
	}
	switch {
	case text == "查看前台任务":
		tasks := q.ListAll()
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].CreatedAt.After(tasks[j].CreatedAt) })
		var lines []string
		for _, t := range tasks {
			if !foregroundScopeMatches(t, input.Scope) {
				continue
			}
			lines = append(lines, foregroundTaskStatus(t))
			if len(lines) == 10 {
				break
			}
		}
		if len(lines) == 0 {
			lines = append(lines, "该会话没有前台任务记录。")
		}
		return &LoopResult{Response: strings.Join(lines, "\n"), State: StateDone, foregroundControl: true}, nil
	case text == "继续前台任务" || strings.HasPrefix(text, "继续前台任务 "):
		id := strings.TrimSpace(strings.TrimPrefix(text, "继续前台任务"))
		for _, t := range q.ListAll() {
			if !foregroundScopeMatches(t, input.Scope) {
				continue
			}
			if id != "" && t.ID != id {
				continue
			}
			if t.State == autonomy.TaskDone {
				if id != "" {
					return &LoopResult{Response: t.Result, State: StateDone, Verified: t.Verified, Verification: t.Verification, foregroundControl: true}, nil
				}
				continue
			}
			if task == nil || t.CreatedAt.After(task.CreatedAt) {
				task = t
			}
		}
		if task == nil {
			return nil, fmt.Errorf("没有可恢复的前台任务；发送“查看前台任务”检查记录")
		}
		if task.State != autonomy.TaskBlocked {
			if err := q.Block(task.ID, "用户请求恢复"); err != nil {
				return nil, err
			}
		}
		if err := q.Unblock(task.ID); err != nil {
			return nil, fmt.Errorf("%s\n恢复前需要核对中断操作: %w", foregroundTaskStatus(task), err)
		}
		if err := json.Unmarshal([]byte(task.Description), &input); err != nil {
			return nil, fmt.Errorf("无法恢复原始请求: %w", err)
		}
		input = input.Normalize()
		snapshot = a.providerSnapshotForTurn(input.RoutingText)
	case strings.HasPrefix(text, "处理前台操作 "):
		var request struct {
			TaskID      string `json:"task_id"`
			OperationID string `json:"operation_id"`
			Resolution  string `json:"resolution"`
			Result      string `json:"result"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(text, "处理前台操作 ")), &request); err != nil {
			return nil, fmt.Errorf("处理前台操作需要 JSON: %w", err)
		}
		t, ok := q.Get(request.TaskID)
		if !ok || !foregroundScopeMatches(t, input.Scope) {
			return nil, fmt.Errorf("该会话范围内没有此任务")
		}
		if err := q.ResolveOperation(request.TaskID, request.OperationID, request.Resolution, request.Result); err != nil {
			return nil, err
		}
		return &LoopResult{Response: "操作处理决定已保存；发送“继续前台任务 " + request.TaskID + "”恢复执行。", State: StateDone, foregroundControl: true}, nil
	default:
		data, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		task, _, err = q.AddWithAcceptance(input.RoutingText, string(data), autonomy.PriorityNormal, nil, nil, nil)
		if err != nil {
			return nil, err
		}
	}

	sanitizeLoopConfig(&cfg)
	a.applyIntentToolGating(&cfg, input.RoutingText)
	a.applyVisionToolPolicy(&cfg, snapshot)
	policy := a.foregroundPolicy()
	// Recompute the deadline from the persisted run budget; time between
	// attempts counts, so network retries cannot reset the total timeout.
	task, _ = q.Get(task.ID)
	started := task.FirstStartedAt
	if started.IsZero() {
		started = time.Now()
	}
	runCtx, cancel := context.WithDeadline(ctx, started.Add(policy.MaxTotalTime))
	defer cancel()
	cfg.eventContext = runCtx
	var last *LoopResult
	for {
		if err := runCtx.Err(); err != nil {
			return last, foregroundInterrupted(task, err)
		}
		task, _ = q.Get(task.ID)
		if task.State == autonomy.TaskBlocked {
			return last, foregroundInterrupted(task, errors.New(task.BlockReason))
		}
		if delay := time.Until(task.NextRunAt); delay > 0 {
			emitForeground(cfg, ChatEvent{Type: ChatEventThinking, Content: fmt.Sprintf("请求暂时失败，%.0f 秒后从检查点重试。", delay.Seconds())})
			timer := time.NewTimer(delay)
			select {
			case <-runCtx.Done():
				timer.Stop()
				return last, foregroundInterrupted(task, runCtx.Err())
			case <-timer.C:
			}
		}
		claimed, err := q.ClaimTask(task.ID, "foreground")
		if err != nil {
			return last, err
		}
		if claimed == nil {
			task, _ = q.Get(task.ID)
			return last, foregroundInterrupted(task, errors.New("执行预算已用完"))
		}
		cfg.Execution = autonomy.NewExecution(q, claimed)
		if err := cfg.Execution.BindSession(sess.ID); err != nil {
			return last, err
		}
		emitForeground(cfg, ChatEvent{Type: ChatEventThinking, TaskID: task.ID, Content: fmt.Sprintf("前台任务 %s，执行第 %d 段。", task.ID, claimed.Attempts-claimed.BudgetStartAttempt)})
		last, err = a.runDurableLoop(runCtx, sess, input, cfg, snapshot)
		outcome := &autonomy.WorkerResult{Error: err}
		if last != nil {
			outcome.Output, outcome.Verified, outcome.Verification = last.Response, last.Verified, last.Verification
		}
		if persistErr := cfg.Execution.Finish(outcome, runCtx.Err() != nil); persistErr != nil {
			return last, fmt.Errorf("前台任务结果保存失败: %w", persistErr)
		}
		task, _ = q.Get(task.ID)
		if err == nil {
			last.foregroundInput = &input
			return last, nil
		}
		if runCtx.Err() != nil {
			return last, foregroundInterrupted(task, runCtx.Err())
		}
	}
}

func foregroundScopeMatches(t *autonomy.QueueTask, scope TurnScope) bool {
	var original UserTurnInput
	if json.Unmarshal([]byte(t.Description), &original) != nil {
		return false
	}
	x, y := original.Scope.Normalize(), scope.Normalize()
	return x.Platform == y.Platform && x.ChatID == y.ChatID && x.SenderID == y.SenderID
}

func foregroundTaskStatus(t *autonomy.QueueTask) string {
	line := fmt.Sprintf("%s [%s] %s", t.ID, t.State, t.Title)
	if t.BlockReason != "" {
		line += "；" + t.BlockReason
	}
	for _, op := range t.Operations {
		if op.State == "started" {
			line += fmt.Sprintf("；待核对操作 %s (%s)，参数 %s", op.ID, op.Name, op.Arguments)
		}
	}
	return line
}

func foregroundInterrupted(t *autonomy.QueueTask, err error) error {
	return fmt.Errorf("前台任务未完成，已保留恢复记录。%s\n可发送“继续前台任务 %s”恢复；结果不明的工具操作需要先核对。原因: %w", foregroundTaskStatus(t), t.ID, err)
}

func (a *Agent) finishForegroundMemory(sess *session.Session, input UserTurnInput, result *LoopResult) {
	if result.foregroundControl {
		return
	}
	if result.foregroundInput != nil {
		input = *result.foregroundInput
	}
	maintenance := a.recordMemoryTurn(sess)
	a.saveConversationMemoryFromSession(sess, input, result.Response)
	a.applyMemoryMaintenance(maintenance)
	if a.ragManager != nil && autoIndexFinalAnswersEnabled() {
		a.indexConversationTurn(input.RoutingText, result.Response)
	}
	if a.metrics != nil {
		a.metrics.RecordChatRequest()
	}
}

// foregroundModelCall preserves native streaming, while treating text as a
// draft until the persisted completion check succeeds.
func (a *Agent) foregroundModelCall(ctx context.Context, messages []provider.Message, opts provider.CallOptions, cfg LoopConfig, snapshot providerSnapshot) (*provider.Response, error) {
	cfg.eventContext = ctx
	if cfg.emit == nil || a.getStreamMode() != StreamModeNative {
		return a.chatLoopIteration(ctx, messages, opts, false, snapshot)
	}
	ch, err := a.streamLoopIteration(ctx, messages, opts, false, snapshot)
	if err != nil {
		return nil, err
	}
	var content, reasoning strings.Builder
	var acc []streamToolCallAcc
	resp := &provider.Response{}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-ch:
			if !ok {
				return nil, fmt.Errorf("provider stream closed without terminal event")
			}
			if chunk.Err != nil {
				return nil, chunk.Err
			}
			content.WriteString(chunk.Content)
			reasoning.WriteString(chunk.ReasoningContent)
			if chunk.Content != "" && !shouldHoldPotentialTextToolCallStream(content.String()) {
				emitForeground(cfg, ChatEvent{Type: ChatEventContent, Content: chunk.Content})
			}
			if chunk.ReasoningContent != "" {
				emitForeground(cfg, ChatEvent{Type: ChatEventReasoningContent, Content: chunk.ReasoningContent})
			}
			for _, delta := range chunk.ToolCallDeltas {
				if delta.Index < 0 || delta.Index > 1024 {
					return nil, fmt.Errorf("invalid tool call index")
				}
				for len(acc) <= delta.Index {
					acc = append(acc, streamToolCallAcc{})
				}
				c := &acc[delta.Index]
				if delta.ID != "" {
					c.id = delta.ID
				}
				if delta.Name != "" {
					c.name = delta.Name
				}
				c.arguments += delta.Arguments
			}
			if chunk.FinishReason != "" {
				resp.FinishReason = chunk.FinishReason
			}
			if chunk.Usage != nil {
				resp.Usage = chunk.Usage
				resp.TokensUsed = chunk.Usage.TotalTokens
			}
			if !chunk.Done {
				continue
			}
			resp.Content, resp.ReasoningContent = content.String(), reasoning.String()
			for _, c := range acc {
				if c.name == "" {
					continue
				}
				if c.id == "" {
					c.id = provider.GenerateCallID()
				}
				resp.ToolCalls = append(resp.ToolCalls, provider.ToolCall{ID: c.id, Name: c.name, Arguments: c.arguments})
			}
			return resp, nil
		}
	}
}
