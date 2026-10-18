package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	taskstore "github.com/yurika0211/luckyagent-orchestrator/task"
	"github.com/yurika0211/luckyagent/internal/gateway"
	"github.com/yurika0211/luckyagent/internal/tool"
)

type delegateTraceMessenger interface {
	SendHTMLWithReceipt(context.Context, string, string) (gateway.SentMessage, error)
	EditHTML(context.Context, string, string, string, *tgbotapi.InlineKeyboardMarkup) error
	AnswerCallback(string, string) error
}

type telegramTaskStoreProvider interface {
	TaskStore() taskstore.Store
}

type telegramDelegateProvider interface {
	Delegate() *tool.DelegateManager
}

type delegateProgressTracker struct {
	mu        sync.Mutex
	adapter   delegateTraceMessenger
	store     taskstore.Store
	chatID    string
	messageID string
	taskIDs   map[string]struct{}
	cancel    context.CancelFunc
	onDone    func()
}

func (h *Handler) observeDelegateToolResult(msg *gateway.Message, result string) {
	if h == nil || msg == nil || h.state == nil {
		return
	}
	storeProvider, ok := h.state.(telegramTaskStoreProvider)
	if !ok || storeProvider.TaskStore() == nil {
		return
	}
	var payload struct {
		TaskID string `json:"task_id"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(result)), &payload) != nil || strings.TrimSpace(payload.TaskID) == "" {
		return
	}
	h.mu.Lock()
	if h.delegateChats == nil {
		h.delegateChats = make(map[string]string)
	}
	if existing := h.delegateChats[msg.Chat.ID]; existing != "" {
		if tracker := h.delegateTrackers[msg.Chat.ID]; tracker != nil {
			tracker.addTask(payload.TaskID)
			h.delegateTaskChats[payload.TaskID] = msg.Chat.ID
		}
		h.mu.Unlock()
		return
	}
	h.delegateChats[msg.Chat.ID] = payload.TaskID
	h.mu.Unlock()
	tracker := h.startDelegateProgress(msg, payload.TaskID, storeProvider.TaskStore())
	h.mu.Lock()
	if tracker != nil {
		h.delegateTrackers[msg.Chat.ID] = tracker
		h.delegateTaskChats[payload.TaskID] = msg.Chat.ID
	} else {
		delete(h.delegateChats, msg.Chat.ID)
	}
	h.mu.Unlock()
}

func (h *Handler) startDelegateProgress(msg *gateway.Message, taskID string, store taskstore.Store) *delegateProgressTracker {
	adapter, ok := h.adapter.(delegateTraceMessenger)
	if !ok || adapter == nil || store == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	tracker := &delegateProgressTracker{
		adapter: adapter,
		store:   store,
		chatID:  msg.Chat.ID,
		taskIDs: map[string]struct{}{taskID: {}},
		cancel:  cancel,
	}
	tracker.onDone = func() {
		h.mu.Lock()
		if h.delegateTrackers[msg.Chat.ID] == tracker {
			delete(h.delegateTrackers, msg.Chat.ID)
			delete(h.delegateChats, msg.Chat.ID)
		}
		h.mu.Unlock()
	}
	initial := tracker.render()
	receipt, err := adapter.SendHTMLWithReceipt(ctx, tracker.chatID, initial)
	if err != nil || strings.TrimSpace(receipt.ID) == "" {
		cancel()
		return nil
	}
	tracker.messageID = receipt.ID
	go tracker.run(ctx)
	return tracker
}

func (t *delegateProgressTracker) addTask(taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	t.mu.Lock()
	t.taskIDs[taskID] = struct{}{}
	t.mu.Unlock()
}

func (t *delegateProgressTracker) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer t.cancel()
	defer func() {
		if t.onDone != nil {
			t.onDone()
		}
	}()
	last := ""
	for {
		content, markup, terminal := t.snapshot()
		if content != last {
			_ = t.adapter.EditHTML(ctx, t.chatID, t.messageID, content, markup)
			last = content
		}
		if terminal {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (t *delegateProgressTracker) render() string {
	return "<b>🧭 Agent Trace</b>\n<pre><code>Task Overview · waiting for task status...</code></pre>"
}

func (t *delegateProgressTracker) snapshot() (string, *tgbotapi.InlineKeyboardMarkup, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	rows := make([]string, 0, len(t.taskIDs))
	buttons := make([][]tgbotapi.InlineKeyboardButton, 0, len(t.taskIDs))
	taskIDs := make([]string, 0, len(t.taskIDs))
	for taskID := range t.taskIDs {
		taskIDs = append(taskIDs, taskID)
	}
	sort.Strings(taskIDs)
	total, completed, failed, running := 0, 0, 0, 0
	terminal := true
	for _, taskID := range taskIDs {
		record, ok, err := t.store.Get(taskID)
		if err != nil || !ok {
			terminal = false
			total++
			if total <= 6 {
				rows = append(rows, fmt.Sprintf("[%d] ⏳ %s · pending", total, taskID))
			}
			continue
		}
		total++
		icon := "⏳"
		switch record.Status {
		case taskstore.StatusRunning, taskstore.StatusPending:
			running++
			terminal = false
			if record.Status == taskstore.StatusRunning {
				icon = "🔄"
			}
		case taskstore.StatusCompleted:
			completed++
			icon = "✅"
		case taskstore.StatusFailed, taskstore.StatusBlocked:
			failed++
			icon = "🔴"
		case taskstore.StatusCancelled:
			failed++
			icon = "🛑"
		}
		description := strings.Join(strings.Fields(record.Description), " ")
		if len(description) > 55 {
			description = description[:52] + "..."
		}
		line := fmt.Sprintf("[%d] %s %s (%s)", total, icon, description, record.ID)
		elapsed := record.Outcome.Cost.Elapsed
		if elapsed <= 0 && !record.StartedAt.IsZero() {
			end := record.CompletedAt
			if end.IsZero() {
				end = time.Now()
			}
			elapsed = end.Sub(record.StartedAt)
		}
		if elapsed > 0 {
			line += "\n    Duration: " + formatDelegateDuration(elapsed)
		}
		if record.Outcome.Cost.ToolCalls > 0 {
			line += fmt.Sprintf("\n    Tools: %dx", record.Outcome.Cost.ToolCalls)
		}
		if lastTool := strings.TrimSpace(record.Metadata["last_tool"]); lastTool != "" && record.Status == taskstore.StatusRunning {
			line += "\n    Last Action: " + lastTool
		}
		if record.Status == taskstore.StatusCompleted {
			line += "\n    Result: completed"
		}
		if record.Outcome.UserFeedback != "" {
			line += "\n    Error: " + strings.ReplaceAll(record.Outcome.UserFeedback, "\n", " ")
		}
		visible := total <= 6
		if visible {
			rows = append(rows, line)
		}
		if visible && (record.Status == taskstore.StatusFailed || record.Status == taskstore.StatusBlocked) {
			data := "agent_retry:" + record.ID
			buttons = append(buttons, []tgbotapi.InlineKeyboardButton{{Text: "🔄 Retry " + record.ID, CallbackData: &data}})
		}
		if visible && record.Status == taskstore.StatusCompleted {
			data := "agent_view:" + record.ID
			buttons = append(buttons, []tgbotapi.InlineKeyboardButton{{Text: "📄 View " + record.ID, CallbackData: &data}})
		}
	}
	if total > len(rows) {
		rows = append(rows, fmt.Sprintf("… %d more tasks hidden; use task_status for details", total-len(rows)))
	}
	body := fmt.Sprintf("Task Overview · total: %d · running: %d · completed: %d · failed: %d\n\n%s\nDone · %d agent steps · %d succeeded, %d failed", total, running, completed, failed, strings.Join(rows, "\n"), total, completed, failed)
	return "<b>🧭 Agent Trace (Multi-Agent)</b>\n<pre><code>" + html.EscapeString(body) + "</code></pre>", &tgbotapi.InlineKeyboardMarkup{InlineKeyboard: buttons}, terminal
}

func formatDelegateDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (h *Handler) handleDelegateCallback(ctx context.Context, query *tgbotapi.CallbackQuery) {
	if query == nil || query.Message == nil || h == nil || h.adapter == nil {
		return
	}
	data := strings.TrimSpace(query.Data)
	parts := strings.SplitN(data, ":", 2)
	if len(parts) != 2 || (parts[0] != "agent_retry" && parts[0] != "agent_view") {
		return
	}
	taskID := strings.TrimSpace(parts[1])
	if taskID == "" {
		return
	}
	if adapter, ok := h.adapter.(delegateTraceMessenger); ok {
		_ = adapter.AnswerCallback(query.ID, "处理中…")
	}
	storeProvider, ok := h.state.(telegramTaskStoreProvider)
	if !ok || storeProvider.TaskStore() == nil {
		return
	}
	store := storeProvider.TaskStore()
	record, found, err := store.Get(taskID)
	if err != nil || !found {
		return
	}
	chatID := fmt.Sprint(query.Message.Chat.ID)
	h.mu.RLock()
	boundChat := h.delegateTaskChats[taskID]
	h.mu.RUnlock()
	if boundChat != "" && boundChat != chatID {
		return
	}
	switch parts[0] {
	case "agent_view":
		result, _, _ := store.Result(taskID)
		if strings.TrimSpace(result) == "" {
			result = record.Outcome.UserFeedback
		}
		if result == "" {
			result = "暂无输出。"
		}
		_ = h.adapter.Send(ctx, chatID, "📄 "+taskID+"\n"+result)
	case "agent_retry":
		delegateProvider, ok := h.state.(telegramDelegateProvider)
		if !ok || delegateProvider.Delegate() == nil {
			return
		}
		result, err := delegateProvider.Delegate().RetryTask(taskID)
		if err != nil {
			_ = h.adapter.Send(ctx, chatID, "❌ Retry 失败："+err.Error())
			return
		}
		_ = h.adapter.Send(ctx, chatID, "🔄 已重新提交："+result)
	}
}
