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
	if isMarkdownStorePath(s.path) {
		return s.writeMarkdown(state)
	}
	return s.writeJSON(state)
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

	var (
		state PersistedState
		err2  error
	)
	if isMarkdownStorePath(s.path) {
		state, err2 = parseMarkdownState(string(data))
		if err2 != nil {
			return 0, fmt.Errorf("parse mission store: %w", err2)
		}
	} else {
		if err2 = json.Unmarshal(data, &state); err2 != nil {
			return 0, fmt.Errorf("parse cron store: %w", err2)
		}
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

func isMarkdownStorePath(path string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(path)), ".md")
}

func (s *Store) writeJSON(state PersistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cron store: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write cron store: %w", err)
	}
	return nil
}

func (s *Store) writeMarkdown(state PersistedState) error {
	var b strings.Builder
	b.WriteString("# LuckyHarness Mission Store\n\n")
	b.WriteString(fmt.Sprintf("engine_running: %t\n\n", state.EngineRunning))

	for _, job := range state.Jobs {
		b.WriteString(fmt.Sprintf("## job:%s\n", job.ID))
		b.WriteString(fmt.Sprintf("schedule: %s\n", job.ScheduleText))
		b.WriteString(fmt.Sprintf("mode: %s\n", job.Mode))
		b.WriteString(fmt.Sprintf("paused: %t\n", job.Paused))
		b.WriteString("command: |\n")
		for _, line := range strings.Split(job.Command, "\n") {
			b.WriteString("  " + line + "\n")
		}
		metaJSON, _ := json.Marshal(job.Metadata)
		b.WriteString(fmt.Sprintf("metadata: %s\n\n", string(metaJSON)))
	}

	if err := os.WriteFile(s.path, []byte(b.String()), 0600); err != nil {
		return fmt.Errorf("write mission store: %w", err)
	}
	return nil
}

func parseMarkdownState(content string) (PersistedState, error) {
	state := PersistedState{
		Version: 1,
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		i++
		if line == "" || strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.HasPrefix(line, "engine_running:") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "engine_running:"))
			state.EngineRunning = strings.EqualFold(v, "true")
			continue
		}
		if strings.HasPrefix(line, "## job:") {
			id := strings.TrimSpace(strings.TrimPrefix(line, "## job:"))
			if id == "" {
				continue
			}
			pj := PersistedJob{
				ID:       id,
				Mode:     "shell",
				Metadata: map[string]string{},
			}
			for i < len(lines) {
				raw := lines[i]
				trimmed := strings.TrimSpace(raw)
				if strings.HasPrefix(trimmed, "## job:") {
					break
				}
				i++
				if trimmed == "" {
					continue
				}
				switch {
				case strings.HasPrefix(trimmed, "schedule:"):
					pj.ScheduleText = strings.TrimSpace(strings.TrimPrefix(trimmed, "schedule:"))
				case strings.HasPrefix(trimmed, "mode:"):
					pj.Mode = strings.TrimSpace(strings.TrimPrefix(trimmed, "mode:"))
				case strings.HasPrefix(trimmed, "paused:"):
					pj.Paused = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "paused:")), "true")
				case strings.HasPrefix(trimmed, "metadata:"):
					metaRaw := strings.TrimSpace(strings.TrimPrefix(trimmed, "metadata:"))
					if metaRaw != "" && metaRaw != "null" {
						_ = json.Unmarshal([]byte(metaRaw), &pj.Metadata)
					}
				case trimmed == "command: |":
					var cmdLines []string
					for i < len(lines) {
						if strings.HasPrefix(lines[i], "  ") {
							cmdLines = append(cmdLines, strings.TrimPrefix(lines[i], "  "))
							i++
							continue
						}
						break
					}
					pj.Command = strings.Join(cmdLines, "\n")
				}
			}
			if strings.TrimSpace(pj.ScheduleText) == "" || strings.TrimSpace(pj.Command) == "" {
				continue
			}
			if pj.Metadata == nil {
				pj.Metadata = map[string]string{}
			}
			state.Jobs = append(state.Jobs, pj)
		}
	}
	return state, nil
}
