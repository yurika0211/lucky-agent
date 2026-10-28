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
	// A running engine can pick up an immediately-due job before AddJobWithMeta
	// returns. Do not execute it until flags and the initial save are complete.
	ready := make(chan struct{})
	var setupErr error
	defer close(ready)
	preparedTask := func() error {
		<-ready
		if setupErr != nil {
			return setupErr
		}
		return task()
	}
	if err := a.cronEngine.AddJobWithMeta(id, name, description, schedule, preparedTask, meta); err != nil {
		setupErr = err
		return err
	}
	if metadataBool(meta, "delete_after_run") {
		// RestoreJobState replaces counters, history, and NextRun. Only the
		// flag should change; save and restart keep it from job metadata.
		if err := a.cronEngine.SetDeleteAfterRun(id, true); err != nil {
			setupErr = err
			_ = a.cronEngine.RemoveJob(id)
			return err
		}
	}
	if setupErr = a.saveCronJobs(); setupErr != nil {
		_ = a.cronEngine.RemoveJob(id)
		return setupErr
	}
	if !a.cronEngine.IsRunning() {
		a.cronEngine.Start()
	}
	return nil
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
