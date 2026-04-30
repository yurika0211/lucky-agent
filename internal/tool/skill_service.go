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
	r.Register(&Tool{
		Name:        "skill_read",
		Description: "读取指定 skill 的 SKILL.md 内容，了解该 skill 的完整使用方法和步骤。当用户请求涉及某个 skill 的能力时，先调用此工具读取 SKILL.md，再按指引操作。",
		Category:    CatSkill,
		Permission:  PermAuto,
		Enabled:     true,
		Parameters: map[string]Param{
			"name": {
				Type:        "string",
				Description: "Skill 名称（如 web-search, summarize, rewrite 等）",
				Required:    false,
			},
		},
		Handler: s.HandleRead,
	})
}

// HandleRead reads a skill's SKILL.md or lists available skills.
func (s *SkillToolService) HandleRead(args map[string]any) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
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
	if len(candidates) > 0 {
		return fmt.Sprintf("Skill '%s' not found. Did you mean: %s?", name, strings.Join(candidates, ", ")), nil
	}
	return fmt.Sprintf("Skill '%s' not found. Use skill_read without name to list all skills.", name), nil
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
