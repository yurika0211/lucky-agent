package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/logger"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-tools/autonomy"
)

type foregroundResumeJob struct {
	session *session.Session
	queue   *autonomy.TaskQueue
	tasks   []*autonomy.QueueTask
}

type foregroundQueueSnapshot struct {
	Version int                  `json:"version"`
	Tasks   []autonomy.QueueTask `json:"tasks"`
}

// ForegroundTasks returns a read-only snapshot for host progress endpoints.
// It reads queue files directly so observing progress never changes an
// in-progress task back to ready as EnablePersistence would during recovery.
func (a *Agent) ForegroundTasks() []*autonomy.QueueTask {
	if a == nil || a.cfg == nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(a.cfg.HomeDir(), "runtime", "foreground"))
	if err != nil {
		return nil
	}
	var tasks []*autonomy.QueueTask
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.cfg.HomeDir(), "runtime", "foreground", entry.Name()))
		if err != nil {
			continue
		}
		var snapshot foregroundQueueSnapshot
		if err := json.Unmarshal(data, &snapshot); err != nil || snapshot.Version != 1 {
			continue
		}
		for i := range snapshot.Tasks {
			task := snapshot.Tasks[i]
			if task.Metadata == nil {
				task.Metadata = map[string]string{}
			}
			tasks = append(tasks, &task)
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
	})
	return tasks
}

// ResumeForeground starts only the unfinished foreground tasks whose source is
// explicitly enabled by agent.foreground.resume_sources. Empty configuration
// is intentionally a no-op, preserving the interactive recovery gate.
func (a *Agent) ResumeForeground(ctx context.Context) error {
	if a == nil || a.cfg == nil || a.sessions == nil {
		return nil
	}
	allowed := foregroundResumeSources(a.cfg.Get().Agent.Foreground.ResumeSources)
	if len(allowed) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root := filepath.Join(a.cfg.HomeDir(), "runtime", "foreground")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read foreground recovery directory: %w", err)
	}

	jobs := make(map[string]*foregroundResumeJob)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		q := autonomy.NewTaskQueue(8)
		q.SetRunPolicy(a.foregroundPolicy())
		if _, err := q.EnablePersistence(path); err != nil {
			return fmt.Errorf("load foreground queue %s: %w", entry.Name(), err)
		}
		for _, task := range q.ListByState(autonomy.TaskReady) {
			if strings.TrimSpace(task.SessionID) == "" {
				// Queues written before session binding cannot be mapped safely
				// after a restart. A user turn can still adopt them explicitly.
				continue
			}
			input, err := foregroundTaskInput(task)
			if err != nil {
				logger.Warn("skip foreground task with invalid input", "task_id", task.ID, "error", err)
				continue
			}
			if _, ok := allowed[foregroundTaskSource(task, input)]; !ok {
				continue
			}
			sess := a.sessions.Ensure(task.SessionID)
			job := jobs[sess.ID]
			if job == nil {
				job = &foregroundResumeJob{session: sess, queue: q}
				jobs[sess.ID] = job
			}
			job.tasks = append(job.tasks, task)
		}
	}

	for _, job := range jobs {
		sort.SliceStable(job.tasks, func(i, j int) bool {
			return job.tasks[i].CreatedAt.Before(job.tasks[j].CreatedAt)
		})
		go a.resumeForegroundJob(ctx, job, allowed)
	}
	return nil
}

func foregroundResumeSources(values []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			allowed[value] = struct{}{}
		}
	}
	return allowed
}

func foregroundTaskInput(task *autonomy.QueueTask) (UserTurnInput, error) {
	var input UserTurnInput
	if err := json.Unmarshal([]byte(task.Description), &input); err != nil {
		return UserTurnInput{}, fmt.Errorf("decode task description: %w", err)
	}
	return input.Normalize(), nil
}

func foregroundTaskSource(task *autonomy.QueueTask, input UserTurnInput) string {
	if task != nil && task.Metadata != nil {
		if source := strings.TrimSpace(task.Metadata["foreground_source"]); source != "" {
			return strings.ToLower(source)
		}
	}
	return strings.ToLower(foregroundSource(input))
}

func (a *Agent) resumeForegroundJob(ctx context.Context, job *foregroundResumeJob, allowed map[string]struct{}) {
	if job == nil || job.session == nil || job.queue == nil {
		return
	}
	if _, loaded := a.foregroundActive.LoadOrStore(job.session.ID, struct{}{}); loaded {
		return
	}
	defer a.foregroundActive.Delete(job.session.ID)

	for _, task := range job.tasks {
		if ctx.Err() != nil {
			return
		}
		current, ok := job.queue.Get(task.ID)
		if !ok || current.State != autonomy.TaskReady {
			continue
		}
		input, err := foregroundTaskInput(current)
		if err != nil {
			logger.Warn("skip foreground task with invalid input", "task_id", current.ID, "error", err)
			continue
		}
		if _, ok := allowed[foregroundTaskSource(current, input)]; !ok {
			continue
		}
		cfg := DefaultLoopConfig()
		if a.cfg != nil {
			currentConfig := a.cfg.Get()
			ApplyAgentLoopConfig(&cfg, currentConfig.Agent)
		}
		cfg.Foreground = true
		cfg.Source = foregroundTaskSource(current, input)
		cfg.SessionID = job.session.ID
		snapshot, err := a.sandboxSnapshot(job.session)
		if err != nil {
			_ = job.queue.Block(current.ID, "启动恢复无法准备执行边界: "+err.Error())
			logger.Warn("foreground startup recovery blocked by sandbox", "task_id", current.ID, "error", err)
			continue
		}
		cfg.Sandbox = snapshot
		a.applySandboxToolPolicy(&cfg, snapshot)
		result, err := a.runForegroundTask(ctx, job.session, input, cfg, a.providerSnapshotForTurn(input.RoutingText), job.queue, current)
		snapshot.Close()
		if err == nil && result != nil {
			a.finishForegroundMemory(job.session, input, result)
		}
		if err != nil {
			logger.Warn("foreground startup recovery stopped", "task_id", current.ID, "error", err)
		}
	}
}
