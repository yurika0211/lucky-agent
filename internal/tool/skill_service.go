package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SkillToolService wraps skill tool registration and skill_read access.
type SkillToolService struct {
	skills []*SkillInfo
}

// NewSkillToolService creates a skill tool service.
func NewSkillToolService(skills []*SkillInfo) *SkillToolService {
	return &SkillToolService{skills: skills}
}

// RegisterSkillTools registers skill-derived tools and skill_read onto the registry.
func (s *SkillToolService) RegisterSkillTools(r *Registry) {
	if s == nil || r == nil {
		return
	}
	RegisterSkillTools(r, s.skills, nil)
	s.RegisterReadTool(r)
}

// RegisterReadTool registers the skill_read helper without registering skill tools.
// This lets SkillRegistry own the executable skill-tool lifecycle while the
// service still exposes skill documentation lookup.
func (s *SkillToolService) RegisterReadTool(r *Registry) {
	if s == nil || r == nil {
		return
	}
	r.Register(&Tool{
		Name:        "skill_read",
		Description: "Read a skill's SKILL.md before using that workflow. Use this when a task clearly matches a named skill and you need its trigger rules, steps, or operating guidance.",
		Category:    CatSkill,
		Permission:  PermAuto,
		Enabled:     true,
		Parameters: map[string]Param{
			"name": {
				Type:        "string",
				Description: "Skill name to inspect before execution. Leave empty to list currently available skills.",
				Required:    false,
			},
			"format": {
				Type:        "string",
				Description: "Response format: text or json. Defaults to text for backward compatibility.",
				Required:    false,
				Default:     "text",
			},
		},
		Handler: s.HandleRead,
	})
}

// HandleRead reads a skill's SKILL.md or lists available skills.
func (s *SkillToolService) HandleRead(args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	format, _ := args["format"].(string)
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "text"
	}

	if name == "" {
		if format == "json" {
			items := make([]map[string]any, 0, len(s.skills))
			for _, skill := range s.skills {
				items = append(items, skillReadMetadata(skill, ""))
			}
			return prettyStructuredValue(map[string]any{
				"skills": items,
				"count":  len(items),
			})
		}

		var b strings.Builder
		b.WriteString("Available skills:\n")
		for _, skill := range s.skills {
			b.WriteString(fmt.Sprintf("- %s: %s\n", skill.Name, skill.Description))
		}
		return b.String(), nil
	}

	for _, skill := range s.skills {
		if skillMatchesName(skill, name) {
			skillFile := filepath.Join(skill.Dir, "SKILL.md")
			data, err := os.ReadFile(skillFile)
			if err != nil {
				return "", fmt.Errorf("read SKILL.md for %s: %w", name, err)
			}
			if format == "json" {
				return prettyStructuredValue(skillReadMetadata(skill, string(data)))
			}
			return string(data), nil
		}
	}

	var candidates []string
	lowerName := strings.ToLower(name)
	for _, skill := range s.skills {
		if strings.Contains(strings.ToLower(skill.Name), lowerName) {
			candidates = append(candidates, skill.Name)
		}
	}
	if format == "json" {
		return prettyStructuredValue(map[string]any{
			"found":      false,
			"name":       name,
			"candidates": candidates,
			"message": func() string {
				if len(candidates) > 0 {
					return fmt.Sprintf("Skill '%s' not found. Did you mean: %s?", name, strings.Join(candidates, ", "))
				}
				return fmt.Sprintf("Skill '%s' not found. Use skill_read without name to list all skills.", name)
			}(),
		})
	}
	if len(candidates) > 0 {
		return fmt.Sprintf("Skill '%s' not found. Did you mean: %s?", name, strings.Join(candidates, ", ")), nil
	}
	return fmt.Sprintf("Skill '%s' not found. Use skill_read without name to list all skills.", name), nil
}

func skillReadMetadata(skill *SkillInfo, content string) map[string]any {
	if skill == nil {
		return map[string]any{}
	}
	toolItems := make([]map[string]any, 0, len(skill.Tools))
	for _, toolDef := range skill.Tools {
		toolItems = append(toolItems, map[string]any{
			"name":            toolDef.Name,
			"description":     toolDef.Description,
			"expose_to_model": toolDef.ExposeToModel,
			"command":         toolDef.Command,
		})
	}

	summary := strings.TrimSpace(skill.Summary)
	if summary == "" {
		summary = strings.TrimSpace(skill.Description)
	}

	payload := map[string]any{
		"found":         true,
		"name":          skill.Name,
		"aliases":       append([]string(nil), skill.Aliases...),
		"description":   skill.Description,
		"summary":       summary,
		"dir":           skill.Dir,
		"skill_md_path": filepath.Join(skill.Dir, "SKILL.md"),
		"available":     skill.Available,
		"tools":         toolItems,
	}
	if content != "" {
		payload["content"] = content
	}
	return payload
}

func skillMatchesName(s *SkillInfo, name string) bool {
	if strings.EqualFold(s.Name, name) {
		return true
	}
	target := normalizeSkillLookup(name)
	if target == "" {
		return false
	}
	if normalizeSkillLookup(s.Name) == target {
		return true
	}
	for _, alias := range s.Aliases {
		if strings.EqualFold(alias, name) || normalizeSkillLookup(alias) == target {
			return true
		}
	}
	return false
}

func normalizeSkillLookup(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.Join(strings.Fields(name), "-")
	name = strings.Trim(name, "-")
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool {
		return r == '-'
	}), "-")
	return name
}
