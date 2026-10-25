package agent

import (
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent-tools/cron"
)

// AddCronJob creates a scheduled job through the same task builder and
// persistence path used by the cron tool. The caller supplies the parsed
// schedule so HTTP and the tool share one parser.
func (a *Agent) AddCronJob(id, name, description, mode, command string, metadata map[string]string, schedule cron.Schedule) error {
	if a == nil || a.cronEngine == nil {
		return fmt.Errorf("cron engine is not initialized")
	}
	if schedule == nil {
		return fmt.Errorf("schedule is required")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch cronTaskMode(mode) {
	case cronTaskModeAgent, cronTaskModeShell:
	default:
		return fmt.Errorf("invalid cron mode %q (use shell or agent)", mode)
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return fmt.Errorf("command is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	meta := map[string]string{
		"mode":    mode,
		"command": command,
	}
	for key, value := range metadata {
		meta[key] = value
	}
	if strings.TrimSpace(name) == "" {
		name = "Cron: " + id
	}
	if strings.TrimSpace(description) == "" {
		description = command
	}
	task := a.buildCronTask(id, cronTaskMode(mode), command, meta)
	if err := a.cronEngine.AddJobWithMeta(id, name, description, schedule, task, meta); err != nil {
		return err
	}
	if metadataBool(meta, "delete_after_run") {
		// RestoreJobState replaces the whole runtime snapshot. Carry the job
		// AddJobWithMeta just created so only DeleteAfterRun changes.
		if err := a.enableCronDeleteAfterRun(id); err != nil {
			_ = a.cronEngine.RemoveJob(id)
			return err
		}
	}
	if !a.cronEngine.IsRunning() {
		a.cronEngine.Start()
	}
	return a.saveCronJobs()
}

// PauseCronJob pauses a job and persists the change.
func (a *Agent) PauseCronJob(id string) error {
	return a.mutateCronJob(id, a.cronEngine.PauseJob)
}

// ResumeCronJob resumes a job and persists the change.
func (a *Agent) ResumeCronJob(id string) error {
	return a.mutateCronJob(id, a.cronEngine.ResumeJob)
}

// RemoveCronJob removes a job and persists the change.
func (a *Agent) RemoveCronJob(id string) error {
	return a.mutateCronJob(id, a.cronEngine.RemoveJob)
}

func (a *Agent) enableCronDeleteAfterRun(id string) error {
	job, ok := a.cronEngine.GetJob(id)
	if !ok || job == nil {
		return fmt.Errorf("cron job %s not found", id)
	}
	state := cron.RestoredJobState{
		Status:         job.Status.String(),
		Paused:         job.Status == cron.StatusPaused,
		RunCount:       job.RunCount,
		ErrorCount:     job.ErrorCount,
		LastError:      job.LastError,
		RunHistory:     append([]cron.RunRecord(nil), job.RunHistory...),
		DeleteAfterRun: true,
	}
	if !job.LastRun.IsZero() {
		lastRun := job.LastRun
		state.LastRun = &lastRun
	}
	if !job.NextRun.IsZero() {
		nextRun := job.NextRun
		state.NextRun = &nextRun
	}
	if !job.CreatedAt.IsZero() {
		createdAt := job.CreatedAt
		state.CreatedAt = &createdAt
	}
	if !job.UpdatedAt.IsZero() {
		updatedAt := job.UpdatedAt
		state.UpdatedAt = &updatedAt
	}
	return a.cronEngine.RestoreJobState(id, state)
}

func (a *Agent) mutateCronJob(id string, mutate func(string) error) error {
	if a == nil || a.cronEngine == nil {
		return fmt.Errorf("cron engine is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if err := mutate(id); err != nil {
		return err
	}
	return a.saveCronJobs()
}

func metadataBool(metadata map[string]string, key string) bool {
	value := strings.ToLower(strings.TrimSpace(metadata[key]))
	return value == "true" || value == "1" || value == "yes"
}
