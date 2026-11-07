package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"gopkg.in/yaml.v3"
)

// Subagent role files live next to the other runtime manuals.
//
//	~/.luckyagent/subagents/<name>.md                  user roles
//	<workspace>/.luckyagent/subagents/<name>.md        project roles
//
// A project file with the same frontmatter name replaces the user file.
// The workspace is the child session working directory when one is set,
// otherwise the process working directory. The body after the frontmatter
// is the role prompt; a frontmatter prompt field overrides that body.

const subagentRoleDirName = "subagents"

// subagentRole is one Markdown role file.
type subagentRole struct {
	Name            string
	Description     string
	Tools           []string
	DisallowedTools []string
	Model           string
	Prompt          string
	// Whitelist is true when the file set tools. That list wins over
	// disallowed_tools when both are present.
	Whitelist bool
	Path      string
}

type subagentRoleFrontmatter struct {
	Name            string `yaml:"name"`
	Description     string `yaml:"description"`
	Tools           any    `yaml:"tools"`
	DisallowedTools any    `yaml:"disallowed_tools"`
	Model           string `yaml:"model"`
	Prompt          string `yaml:"prompt"`
}

// subagentRoleRoots lists the user directory and then the project directory.
// Later entries override earlier ones when the frontmatter name matches.
func subagentRoleRoots(home, workspace string) []string {
	var roots []string
	if home = strings.TrimSpace(home); home != "" {
		roots = append(roots, filepath.Join(home, subagentRoleDirName))
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}
	if workspace != "" {
		roots = append(roots, filepath.Join(workspace, ".luckyagent", subagentRoleDirName))
	}
	return roots
}

// loadSubagentRole reads every role file under the user and project roots
// and returns the one whose frontmatter name matches. A missing file, a
// file with an empty name, or two files that fail to parse is an error.
func loadSubagentRole(home, workspace, name string) (subagentRole, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return subagentRole{}, fmt.Errorf("subagent role name is empty")
	}
	var found *subagentRole
	var readErr error
	for _, root := range subagentRoleRoots(home, workspace) {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return subagentRole{}, fmt.Errorf("read subagent roles in %s: %w", root, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				continue
			}
			role, err := parseSubagentRoleFile(filepath.Join(root, entry.Name()))
			if err != nil {
				readErr = err
				continue
			}
			if role.Name != name {
				continue
			}
			copied := role
			found = &copied
		}
	}
	if found == nil {
		if readErr != nil {
			return subagentRole{}, readErr
		}
		return subagentRole{}, fmt.Errorf("subagent role %q was not found", name)
	}
	return *found, nil
}

func parseSubagentRoleFile(path string) (subagentRole, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return subagentRole{}, fmt.Errorf("read subagent role %s: %w", path, err)
	}
	meta, body, err := splitRoleFrontmatter(string(data))
	if err != nil {
		return subagentRole{}, fmt.Errorf("parse subagent role %s: %w", path, err)
	}
	var front subagentRoleFrontmatter
	if err := yaml.Unmarshal([]byte(meta), &front); err != nil {
		return subagentRole{}, fmt.Errorf("parse subagent role %s: %w", path, err)
	}
	role := subagentRole{
		Name:            strings.TrimSpace(front.Name),
		Description:     strings.TrimSpace(front.Description),
		Tools:           roleToolList(front.Tools),
		DisallowedTools: roleToolList(front.DisallowedTools),
		Model:           strings.TrimSpace(front.Model),
		Prompt:          strings.TrimSpace(front.Prompt),
		Whitelist:       front.Tools != nil,
		Path:            path,
	}
	if role.Name == "" {
		return subagentRole{}, fmt.Errorf("subagent role %s is missing name", path)
	}
	if role.Prompt == "" {
		role.Prompt = strings.TrimSpace(body)
	}
	return role, nil
}

// splitRoleFrontmatter accepts a leading --- block. A file with no
// frontmatter has an empty meta so the caller reports the missing name.
func splitRoleFrontmatter(text string) (meta, body string, err error) {
	text = strings.TrimPrefix(text, "\uFEFF")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", text, nil
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", fmt.Errorf("frontmatter is not closed")
	}
	return rest[:end], strings.TrimPrefix(rest[end+4:], "\n"), nil
}

// roleToolList accepts a YAML list or a comma-separated string.
func roleToolList(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return splitRoleTools(typed)
	case []any:
		var names []string
		for _, item := range typed {
			names = append(names, splitRoleTools(fmt.Sprint(item))...)
		}
		return names
	case []string:
		var names []string
		for _, item := range typed {
			names = append(names, splitRoleTools(item)...)
		}
		return names
	default:
		return splitRoleTools(fmt.Sprint(value))
	}
}

func splitRoleTools(value string) []string {
	var names []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			names = append(names, part)
		}
	}
	return names
}

// subagentRoleWorkspace is where project role files are looked up. The
// child session directory wins; otherwise the process directory is used.
func subagentRoleWorkspace(sess *session.Session) string {
	if sess != nil {
		if cwd := strings.TrimSpace(sess.GetCwd()); cwd != "" {
			return cwd
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// applySubagentRole tightens a child loop that was already built with the
// global delegate policy. disabled_tools stays a floor: a role cannot put
// those tools back. A whitelist replaces the child schema with the listed
// tools. A disallowed list only adds names to the floor. Recursive delegate
// tools stay closed unless allowRecursive is already true.
func applySubagentRole(cfg LoopConfig, role subagentRole, allowRecursive bool, registered []string, canonical func(string) string) (LoopConfig, error) {
	if canonical == nil {
		canonical = func(name string) string { return strings.TrimSpace(name) }
	}
	floor := makeToolNameSet(cfg.DisabledTools)
	if !allowRecursive {
		for _, name := range delegateRecursiveToolNames {
			floor[name] = struct{}{}
		}
	}
	rolePrompt := strings.TrimSpace(role.Prompt)
	if rolePrompt == "" {
		rolePrompt = delegatedSubagentSystemPrompt()
	}
	cfg.ProfileSource = "subagent_role:" + role.Name
	if role.Whitelist {
		var allowed []string
		for _, name := range role.Tools {
			name = canonical(name)
			if name == "" || toolNameInSet(floor, name) {
				continue
			}
			allowed = append(allowed, name)
		}
		allowed = normalizeToolNameList(allowed)
		known := makeToolNameSet(registered)
		var resolved []string
		for _, name := range allowed {
			if toolNameInSet(known, name) {
				resolved = append(resolved, name)
			}
		}
		if len(resolved) == 0 {
			return LoopConfig{}, fmt.Errorf("subagent role %q allows no tools", role.Name)
		}
		allowed = resolved
		cfg.RestrictTools = true
		cfg.AllowedTools = allowed
		allowedSet := makeToolNameSet(allowed)
		if len(registered) > 0 {
			for _, name := range registered {
				if !toolNameInSet(allowedSet, name) {
					cfg.DisabledTools = append(cfg.DisabledTools, name)
				}
			}
		}
		cfg.DisabledTools = normalizeToolNameList(cfg.DisabledTools)
		cfg.subagentRolePrompt = rolePrompt
		return cfg, nil
	}
	for _, name := range role.DisallowedTools {
		name = canonical(name)
		if name != "" {
			cfg.DisabledTools = append(cfg.DisabledTools, name)
		}
	}
	cfg.DisabledTools = normalizeToolNameList(cfg.DisabledTools)
	cfg.subagentRolePrompt = rolePrompt
	return cfg, nil
}

// delegateRecursiveToolNames stay unavailable unless the child was opened
// with allow_recursive_delegate and the global switch permits it.
var delegateRecursiveToolNames = []string{
	"subagent",
	"autonomy",
	"delegate_parallel",
	"delegate_to_skill",
	"delegate_to_mcp",
}

// childToolSchemaNames is the model-visible tool list for one child loop.
// It uses the same allow and deny rules as the loop schema builder.
func childToolSchemaNames(registered []string, cfg LoopConfig) []string {
	opts := applyToolAllowlist(provider.CallOptions{Tools: functionSchemas(registered)}, cfg)
	var names []string
	for _, schema := range opts.Tools {
		if name := functionToolNameFromSchema(schema); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func functionSchemas(names []string) []map[string]any {
	schemas := make([]map[string]any, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		schemas = append(schemas, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": name,
			},
		})
	}
	return schemas
}

// registeredToolNames lists canonical tool names the child could be offered.
func registeredToolNames(registry *tool.Registry) []string {
	if registry == nil {
		return nil
	}
	var names []string
	for _, item := range registry.List() {
		if item == nil || strings.TrimSpace(item.Name) == "" {
			continue
		}
		names = append(names, item.Name)
	}
	return names
}
