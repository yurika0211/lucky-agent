package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yurika0211/luckyharness/internal/autonomy"
)

func (a *Agent) startAutonomyResultReporter() {
	if a == nil || a.autonomy == nil || a.autonomy.Pool() == nil {
		return
	}

	a.autonomyResultsMu.Lock()
	if a.autonomyResultsCancel != nil {
		a.autonomyResultsMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.autonomyResultsCancel = cancel
	results := a.autonomy.Pool().Results()
	a.autonomyResultsMu.Unlock()

	go a.consumeAutonomyResults(ctx, results)
}

func (a *Agent) stopAutonomyResultReporter() {
	if a == nil {
		return
	}
	a.autonomyResultsMu.Lock()
	cancel := a.autonomyResultsCancel
	a.autonomyResultsCancel = nil
	a.autonomyResultsMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *Agent) consumeAutonomyResults(ctx context.Context, results <-chan *autonomy.WorkerResult) {
	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-results:
			if !ok {
				return
			}
			if result == nil {
				continue
			}
			a.notifyAutonomyWorkerResult(ctx, result)
		}
	}
}

func (a *Agent) notifyAutonomyWorkerResult(ctx context.Context, result *autonomy.WorkerResult) {
	message := a.formatAutonomyWorkerNotification(result)
	if strings.TrimSpace(message) == "" {
		return
	}
	_ = a.notifyHeartbeatResponse(ctx, message)
}

func (a *Agent) formatAutonomyWorkerNotification(result *autonomy.WorkerResult) string {
	if result == nil {
		return ""
	}

	taskID := strings.TrimSpace(result.TaskID)
	title := taskID
	assignedTo := ""
	if a != nil && a.autonomy != nil && a.autonomy.Queue() != nil && taskID != "" {
		if task, ok := a.autonomy.Queue().Get(taskID); ok {
			if strings.TrimSpace(task.Title) != "" {
				title = strings.TrimSpace(task.Title)
			}
			assignedTo = strings.TrimSpace(task.AssignedTo)
		}
	}
	if title == "" {
		title = "unknown task"
	}

	var b strings.Builder
	if result.Error != nil {
		b.WriteString("后台 worker 任务失败")
	} else {
		b.WriteString("后台 worker 任务完成")
	}
	b.WriteString("\n\n任务: ")
	b.WriteString(title)
	if taskID != "" && taskID != title {
		b.WriteString(" (")
		b.WriteString(taskID)
		b.WriteString(")")
	}
	if assignedTo != "" {
		b.WriteString("\nWorker: ")
		b.WriteString(assignedTo)
	}
	if result.Duration > 0 {
		b.WriteString("\n耗时: ")
		b.WriteString(result.Duration.Round(time.Second).String())
	}
	if result.TokensUsed > 0 {
		b.WriteString("\nTokens: ")
		b.WriteString(fmt.Sprintf("%d", result.TokensUsed))
	}

	detail := strings.TrimSpace(result.Output)
	if result.Error != nil {
		detail = strings.TrimSpace(result.Error.Error())
	}
	if detail == "" {
		detail = "worker 没有返回文本结果。"
	}

	b.WriteString("\n\n结果:\n")
	b.WriteString(truncateAutonomyMessage(detail, 1800))
	return b.String()
}

func truncateAutonomyMessage(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "\n...（结果过长，已截断；可用 autonomy report 查看队列记录）"
}
