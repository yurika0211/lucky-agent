package sdk

import (
	"fmt"
	"strings"
)

// SkillInfo is a read-only snapshot of a loaded skill pack.
type SkillInfo struct {
	Name        string
	Aliases     []string
	Description string
	Summary     string
	Dir         string
	Available   bool
	ToolNames   []string
}

// ListSkills returns currently loaded skills.
func (a *Agent) ListSkills() []SkillInfo {
	if err := a.require(); err != nil {
		return nil
	}
	items := a.inner.Skills()
	out := make([]SkillInfo, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		aliases := item.Aliases
		if aliases != nil {
			aliases = append([]string(nil), aliases...)
		}
		tools := make([]string, 0, len(item.Tools))
		for _, t := range item.Tools {
			if name := strings.TrimSpace(t.Name); name != "" {
				tools = append(tools, name)
			}
		}
		out = append(out, SkillInfo{
			Name:        item.Name,
			Aliases:     aliases,
			Description: item.Description,
			Summary:     item.Summary,
			Dir:         item.Dir,
			Available:   item.Available,
			ToolNames:   tools,
		})
	}
	return out
}

// LoadSkills loads skill packs from dir and returns how many were registered.
func (a *Agent) LoadSkills(dir string) (int, error) {
	if err := a.require(); err != nil {
		return 0, err
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return 0, fmt.Errorf("sdk: skills dir is empty")
	}
	n, err := a.inner.LoadSkills(dir)
	if err != nil {
		return 0, fmt.Errorf("sdk: load skills: %w", err)
	}
	return n, nil
}

// ReloadSkills reloads skills from the runtime skills directory.
func (a *Agent) ReloadSkills() (int, error) {
	if err := a.require(); err != nil {
		return 0, err
	}
	n, err := a.inner.ReloadSkills()
	if err != nil {
		return 0, fmt.Errorf("sdk: reload skills: %w", err)
	}
	return n, nil
}

// SkillsDir returns the configured skills directory for this agent, if any.
func (a *Agent) SkillsDir() string {
	if err := a.require(); err != nil {
		return ""
	}
	return a.inner.SkillsDir()
}
