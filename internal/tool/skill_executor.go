package tool

import "fmt"

// SkillExecutor is the single execution adapter for skill-defined tools.
// Loader discovers skill metadata; registry owns lifecycle; executor turns a
// loaded skill tool definition into a callable Tool handler.
type SkillExecutor struct {
}

// NewSkillExecutor creates the default skill executor.
func NewSkillExecutor() *SkillExecutor {
	return &SkillExecutor{}
}

// HandlerFor returns the callable handler for one skill tool definition.
func (e *SkillExecutor) HandlerFor(skill *SkillInfo, toolDef SkillToolDef) (func(args map[string]any) (string, error), error) {
	if skill == nil {
		return nil, fmt.Errorf("skill is nil")
	}
	if toolDef.Handler != nil {
		return toolDef.Handler, nil
	}
	return defaultSkillHandler(toolDef, skill.Dir, skill.Name), nil
}

// Execute runs one skill tool through the unified skill execution path.
func (e *SkillExecutor) Execute(skill *SkillInfo, toolDef SkillToolDef, args map[string]any) (string, error) {
	handler, err := e.HandlerFor(skill, toolDef)
	if err != nil {
		return "", err
	}
	if args == nil {
		args = map[string]any{}
	}
	return handler(args)
}

func newSkillToolFromDef(skill *SkillInfo, toolDef SkillToolDef, handler func(args map[string]any) (string, error)) *Tool {
	return &Tool{
		Name:            fmt.Sprintf("skill_%s_%s", skill.Name, toolDef.Name),
		Description:     toolDef.Description,
		Parameters:      toolDef.Parameters,
		Handler:         handler,
		Category:        CatSkill,
		Source:          skill.Name,
		Permission:      PermApprove,
		Enabled:         true,
		HiddenFromModel: !toolDef.ExposeToModel && toolDef.Name != "run",
	}
}
