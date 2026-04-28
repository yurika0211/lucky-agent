package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type PersistedState struct {
	Version       int            `json:"version"`
	EngineRunning bool           `json:"engine_running"`
	Jobs          []PersistedJob `json:"jobs"`
}

type PersistedJob struct {
	ID           string            `json:"id"`
	ScheduleText string            `json:"schedule_text"`
	Command      string            `json:"command"`
	Mode         string            `json:"mode"`
	Paused       bool              `json:"paused"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type Store struct {
	path string
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) Save(engine *Engine) error {
	if s == nil || engine == nil {
		return nil
	}

	jobs := engine.ListJobs()
	state := PersistedState{
		Version:       1,
		EngineRunning: engine.IsRunning(),
		Jobs:          make([]PersistedJob, 0, len(jobs)),
	}

	for _, job := range jobs {
		scheduleText := strings.TrimSpace(job.Metadata["schedule_text"])
		command := strings.TrimSpace(job.Metadata["command"])
		mode := strings.TrimSpace(job.Metadata["mode"])
		if scheduleText == "" || command == "" {
			continue
		}
		if mode == "" {
			mode = "shell"
		}
		state.Jobs = append(state.Jobs, PersistedJob{
			ID:           job.ID,
			ScheduleText: scheduleText,
			Command:      command,
			Mode:         mode,
			Paused:       job.Status == StatusPaused,
			Metadata:     copyMetadata(job.Metadata),
		})
	}

	sort.Slice(state.Jobs, func(i, j int) bool {
		return state.Jobs[i].ID < state.Jobs[j].ID
	})

	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create cron store dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cron store: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write cron store: %w", err)
	}
	return nil
}

func (s *Store) Load(engine *Engine, taskBuilder func(job PersistedJob) (func() error, map[string]string, error)) (int, error) {
	if s == nil || engine == nil {
		return 0, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read cron store: %w", err)
	}

	var state PersistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return 0, fmt.Errorf("parse cron store: %w", err)
	}

	restored := 0
	for _, pj := range state.Jobs {
		schedule, err := ParsePersistedSchedule(pj.ScheduleText)
		if err != nil {
			return restored, fmt.Errorf("restore job %s: %w", pj.ID, err)
		}
		task, metadata, err := taskBuilder(pj)
		if err != nil {
			return restored, fmt.Errorf("restore job %s: %w", pj.ID, err)
		}
		if metadata == nil {
			metadata = make(map[string]string)
		}
		for k, v := range pj.Metadata {
			metadata[k] = v
		}
		if strings.TrimSpace(metadata["mode"]) == "" {
			metadata["mode"] = pj.Mode
		}
		metadata["command"] = pj.Command
		metadata["schedule_text"] = pj.ScheduleText

		if err := engine.AddJobWithMeta(pj.ID, "Cron: "+pj.ID, pj.Command, schedule, task, metadata); err != nil {
			return restored, fmt.Errorf("restore job %s: %w", pj.ID, err)
		}
		if pj.Paused {
			if err := engine.PauseJob(pj.ID); err != nil {
				return restored, fmt.Errorf("pause restored job %s: %w", pj.ID, err)
			}
		}
		restored++
	}

	if state.EngineRunning && restored > 0 {
		engine.Start()
	}
	return restored, nil
}

func copyMetadata(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

func ParsePersistedSchedule(input string) (Schedule, error) {
	trimmed := strings.TrimSpace(input)
	schedule, err := ParseNaturalLanguage(trimmed)
	if err == nil {
		return schedule, nil
	}
	return ParseCronExpr(trimmed)
}
