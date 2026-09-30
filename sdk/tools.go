package sdk

import (
	"fmt"
	"strings"

	"github.com/yurika0211/luckyagent/internal/tool"
)

// ToolParam describes one JSON argument for a host-registered tool.
type ToolParam struct {
	Type        string
	Description string
	Required    bool
	Default     any
}

// ToolSpec registers a host-owned tool into the embedded runtime.
//
// Handler runs in-process. Keep it fast and side-effect explicit; long work
// should honor process shutdown via Agent.Close.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  map[string]ToolParam
	Handler     func(args map[string]any) (string, error)

	// AutoApprove marks the tool safe to run without an approval gate.
	// Default false maps to "needs approval" unless Config.AutoApprove is on.
	AutoApprove bool

	// ParallelSafe allows the runtime to run this tool concurrently with others.
	ParallelSafe bool

	// HiddenFromModel keeps the tool callable by host code but out of the model menu.
	HiddenFromModel bool
}

// ToolInfo is a read-only snapshot of a registered tool.
type ToolInfo struct {
	Name        string
	Description string
	Enabled     bool
	Category    string
	Source      string
}

// RegisterTool adds or replaces a host tool in the embedded agent.
func (a *Agent) RegisterTool(spec ToolSpec) error {
	if err := a.require(); err != nil {
		return err
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return fmt.Errorf("sdk: tool name is empty")
	}
	if spec.Handler == nil {
		return fmt.Errorf("sdk: tool %q handler is nil", name)
	}

	params := make(map[string]tool.Param, len(spec.Parameters))
	for key, p := range spec.Parameters {
		params[key] = tool.Param{
			Type:        p.Type,
			Description: p.Description,
			Required:    p.Required,
			Default:     p.Default,
		}
	}

	perm := tool.PermApprove
	if spec.AutoApprove {
		perm = tool.PermAuto
	}

	a.inner.Tools().Register(&tool.Tool{
		Name:            name,
		Description:     strings.TrimSpace(spec.Description),
		Parameters:      params,
		Handler:         spec.Handler,
		Permission:      perm,
		Category:        tool.CatBuiltin,
		Source:          "embed-sdk",
		Enabled:         true,
		ParallelSafe:    spec.ParallelSafe,
		HiddenFromModel: spec.HiddenFromModel,
	})
	return nil
}

// UnregisterTool removes a previously registered tool by name.
func (a *Agent) UnregisterTool(name string) error {
	if err := a.require(); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("sdk: tool name is empty")
	}
	reg := a.inner.Tools()
	if _, ok := reg.Get(name); !ok {
		return fmt.Errorf("sdk: tool %q not found", name)
	}
	reg.Unregister(name)
	return nil
}

// EnableTool enables a registered tool.
func (a *Agent) EnableTool(name string) error {
	if err := a.require(); err != nil {
		return err
	}
	return a.inner.Tools().Enable(strings.TrimSpace(name))
}

// DisableTool disables a registered tool without removing it.
func (a *Agent) DisableTool(name string) error {
	if err := a.require(); err != nil {
		return err
	}
	return a.inner.Tools().Disable(strings.TrimSpace(name))
}

// ListTools returns a snapshot of tools visible to the runtime.
func (a *Agent) ListTools() []ToolInfo {
	if err := a.require(); err != nil {
		return nil
	}
	items := a.inner.Tools().List()
	out := make([]ToolInfo, 0, len(items))
	for _, t := range items {
		if t == nil {
			continue
		}
		out = append(out, ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			Enabled:     t.Enabled,
			Category:    string(t.Category),
			Source:      t.Source,
		})
	}
	return out
}

func (a *Agent) applyDisabledTools(names []string) error {
	if len(names) == 0 {
		return nil
	}
	reg := a.inner.Tools()
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		// Unknown names are ignored so hosts can ship a stable deny-list
		// across runtime versions that add/remove builtins.
		_ = reg.Disable(name)
	}
	return nil
}
