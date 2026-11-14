package agent

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yurika0211/luckyagent-runtime/logger"
)

const (
	skillUsageActionRoute = "route"
	skillUsageActionRead  = "read"
	skillUsageRouteWindow = 2 * time.Second
)

type skillUsageEvent struct {
	TS         time.Time     `json:"ts"`
	Skill      string        `json:"skill"`
	Action     string        `json:"action"`
	Result     string        `json:"result"`
	DurationMS int64         `json:"duration_ms"`
	Duration   time.Duration `json:"-"`
}

// SkillUsageSnapshot is one skill+action pair after restart-safe aggregation.
type SkillUsageSnapshot struct {
	Skill    string
	Action   string
	OK       int64
	Failed   int64
	Duration time.Duration
}

type skillUsageStore struct {
	mu             sync.Mutex
	path           string
	events         []skillUsageEvent
	lastRouteSkill string
	lastRouteAt    time.Time
}

func newSkillUsageStore(path string) *skillUsageStore {
	s := &skillUsageStore{path: strings.TrimSpace(path)}
	if s.path != "" {
		s.loadLocked()
	}
	return s
}

func (a *Agent) skillUsagePath() string {
	if a == nil || a.cfg == nil {
		return ""
	}
	home := strings.TrimSpace(a.cfg.HomeDir())
	if home == "" {
		return ""
	}
	return filepath.Join(home, "runtime", "skill_usage.jsonl")
}

func (a *Agent) ensureSkillUsageStore() *skillUsageStore {
	if a == nil {
		return nil
	}
	if a.skillUsage != nil {
		return a.skillUsage
	}
	a.skillUsage = newSkillUsageStore(a.skillUsagePath())
	return a.skillUsage
}

func (a *Agent) recordMatchedSkillRoute(match *skillRouteMatch, userInput string) {
	if a == nil || match == nil || match.skill == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(match.skill.Name), "obsidian") && isLuckyAgentMemoryBackendQuestion(userInput) {
		return
	}
	a.recordSkillRoute(match.skill.Name)
}

func (a *Agent) recordSkillRoute(skillName string) {
	if a == nil {
		return
	}
	a.ensureSkillUsageStore().record(strings.TrimSpace(skillName), skillUsageActionRoute, "ok", 0)
}

func (a *Agent) recordSkillToolUsage(name, arguments, output string, d time.Duration, err error) {
	if a == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "skill_read" {
		skill := skillNameFromReadArguments(arguments)
		if skill == "" {
			return
		}
		result := "ok"
		if err != nil || toolResultFailed(output) {
			result = "error"
		}
		a.ensureSkillUsageStore().record(skill, skillUsageActionRead, result, d)
		return
	}
	skill, action := splitSkillExecName(name)
	if skill == "" || action == "" {
		return
	}
	result := "ok"
	if err != nil || toolResultFailed(output) {
		result = "error"
	}
	a.ensureSkillUsageStore().record(skill, action, result, d)
}

func (s *skillUsageStore) record(skill, action, result string, d time.Duration) {
	if s == nil {
		return
	}
	skill = strings.TrimSpace(skill)
	action = strings.TrimSpace(action)
	if skill == "" || action == "" {
		return
	}
	if result == "" {
		result = "ok"
	}
	if d < 0 {
		d = 0
	}
	event := skillUsageEvent{
		TS:         time.Now().UTC(),
		Skill:      skill,
		Action:     action,
		Result:     result,
		DurationMS: d.Milliseconds(),
		Duration:   d,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if action == skillUsageActionRoute && s.lastRouteSkill == skill && !s.lastRouteAt.IsZero() && event.TS.Sub(s.lastRouteAt) < skillUsageRouteWindow {
		return
	}
	s.events = append(s.events, event)
	if action == skillUsageActionRoute {
		s.lastRouteSkill = skill
		s.lastRouteAt = event.TS
	}
	if err := s.appendLocked(event); err != nil {
		logger.Debug("skill usage persist skipped", "error", err)
	}
}

func (s *skillUsageStore) appendLocked(event skillUsageEvent) error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(skillUsageEvent{
		TS:         event.TS,
		Skill:      event.Skill,
		Action:     event.Action,
		Result:     event.Result,
		DurationMS: event.DurationMS,
	})
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

func (s *skillUsageStore) loadLocked() {
	if s.path == "" {
		return
	}
	f, err := os.Open(s.path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event skillUsageEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		event.Skill = strings.TrimSpace(event.Skill)
		event.Action = strings.TrimSpace(event.Action)
		if event.Skill == "" || event.Action == "" {
			continue
		}
		if event.DurationMS > 0 {
			event.Duration = time.Duration(event.DurationMS) * time.Millisecond
		}
		s.events = append(s.events, event)
	}
}

func (s *skillUsageStore) snapshot() []SkillUsageSnapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	events := append([]skillUsageEvent(nil), s.events...)
	s.mu.Unlock()
	type key struct{ skill, action string }
	grouped := map[key]*SkillUsageSnapshot{}
	for _, event := range events {
		k := key{skill: event.Skill, action: event.Action}
		row := grouped[k]
		if row == nil {
			row = &SkillUsageSnapshot{Skill: event.Skill, Action: event.Action}
			grouped[k] = row
		}
		if event.Result == "error" {
			row.Failed++
		} else {
			row.OK++
		}
		row.Duration += event.Duration
	}
	out := make([]SkillUsageSnapshot, 0, len(grouped))
	for _, row := range grouped {
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Skill != out[j].Skill {
			return out[i].Skill < out[j].Skill
		}
		return out[i].Action < out[j].Action
	})
	return out
}

func (a *Agent) SkillUsageSnapshot() []SkillUsageSnapshot {
	if a == nil || a.skillUsage == nil {
		return nil
	}
	return a.skillUsage.snapshot()
}

func skillNameFromReadArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return ""
	}
	name, _ := args["name"].(string)
	return strings.TrimSpace(name)
}

func splitSkillExecName(name string) (skill, action string) {
	name = strings.TrimSpace(name)
	if !strings.HasPrefix(name, "skill_") {
		return "", ""
	}
	rest := strings.TrimPrefix(name, "skill_")
	idx := strings.LastIndex(rest, "_")
	if idx <= 0 || idx == len(rest)-1 {
		return "", ""
	}
	skill = rest[:idx]
	action = rest[idx+1:]
	if skill == "" || action == "" || skill == "read" {
		return "", ""
	}
	return skill, action
}
