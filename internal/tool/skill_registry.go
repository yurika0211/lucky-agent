package tool

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SkillVersion is the current skill plugin system version.
const SkillVersion = "v0.5.0"

// SkillState represents the lifecycle state of a skill plugin.
type SkillState int

const (
	SkillDiscovered SkillState = iota // Skill directory found, not yet loaded
	SkillLoaded                       // SKILL.md parsed, metadata available
	SkillRegistered                   // Tools registered in Registry
	SkillEnabled                      // Skill is active and tools are callable
	SkillDisabled                     // Skill is loaded but tools are disabled
	SkillError                        // Skill encountered an error
	SkillUnloaded                     // Skill has been unloaded
)

func (s SkillState) String() string {
	switch s {
	case SkillDiscovered:
		return "discovered"
	case SkillLoaded:
		return "loaded"
	case SkillRegistered:
		return "registered"
	case SkillEnabled:
		return "enabled"
	case SkillDisabled:
		return "disabled"
	case SkillError:
		return "error"
	case SkillUnloaded:
		return "unloaded"
	default:
		return "unknown"
	}
}

// SkillMetadata holds rich metadata about a skill plugin.
type SkillMetadata struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Author       string            `json:"author"`
	Description  string            `json:"description"`
	Dir          string            `json:"dir"`
	Tools        []string          `json:"tools"`        // tool names provided by this skill
	Dependencies []string          `json:"dependencies"` // names of skills this one depends on
	State        SkillState        `json:"state"`
	LoadedAt     time.Time         `json:"loaded_at"`
	Error        string            `json:"error,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// SkillRegistry manages skill plugin lifecycle: discover → load → register → enable/disable → unload.
type SkillRegistry struct {
	mu       sync.RWMutex
	skills   map[string]*SkillMetadata                         // name -> metadata
	infos    map[string]*SkillInfo                             // name -> parsed skill definition
	registry *Registry                                         // tool registry for registering/unregistering tools
	loader   *SkillLoader                                      // skill loader for discovering skills
	executor *SkillExecutor                                    // skill execution adapter
	watchers map[string]func(name string, from, to SkillState) // state change watchers
}

// NewSkillRegistry creates a new SkillRegistry.
func NewSkillRegistry(registry *Registry, loader *SkillLoader) *SkillRegistry {
	return &SkillRegistry{
		skills:   make(map[string]*SkillMetadata),
		infos:    make(map[string]*SkillInfo),
		registry: registry,
		loader:   loader,
		executor: NewSkillExecutor(),
		watchers: make(map[string]func(name string, from, to SkillState)),
	}
}

// SetExecutor replaces the skill executor used when registering skill tools.
func (sr *SkillRegistry) SetExecutor(executor *SkillExecutor) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if executor == nil {
		executor = NewSkillExecutor()
	}
	sr.executor = executor
}

func skillMetadataFromInfo(info *SkillInfo, state SkillState) *SkillMetadata {
	meta := &SkillMetadata{
		Name:        info.Name,
		Description: info.Description,
		Dir:         info.Dir,
		State:       state,
		Labels:      make(map[string]string),
	}
	for _, t := range info.Tools {
		meta.Tools = append(meta.Tools, t.Name)
	}
	return meta
}

// Discover scans the skill directory and records all discovered skills.
func (sr *SkillRegistry) Discover() ([]*SkillMetadata, error) {
	if sr.loader == nil {
		return nil, fmt.Errorf("skill loader not configured")
	}

	skills, err := sr.loader.LoadAll()
	if err != nil {
		return nil, fmt.Errorf("discover skills: %w", err)
	}

	sr.mu.Lock()
	defer sr.mu.Unlock()

	var discovered []*SkillMetadata
	seen := make(map[string]struct{}, len(skills))
	for _, info := range skills {
		if info == nil || strings.TrimSpace(info.Name) == "" {
			continue
		}
		if _, ok := seen[info.Name]; ok {
			return nil, fmt.Errorf("duplicate skill id: %s", info.Name)
		}
		seen[info.Name] = struct{}{}

		meta := skillMetadataFromInfo(info, SkillDiscovered)
		sr.skills[info.Name] = meta
		sr.infos[info.Name] = info
		discovered = append(discovered, meta)
	}

	return discovered, nil
}

// Load parses a specific skill's SKILL.md and transitions it to SkillLoaded.
func (sr *SkillRegistry) Load(name string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not discovered: %s", name)
	}

	if meta.State == SkillLoaded || meta.State == SkillRegistered ||
		meta.State == SkillEnabled || meta.State == SkillDisabled {
		return nil // already loaded
	}

	if sr.loader == nil {
		return fmt.Errorf("skill loader not configured")
	}

	skillFile := meta.Dir
	if skillFile == "" {
		return fmt.Errorf("skill directory not set for: %s", name)
	}

	// SkillLoader.Load expects a path to SKILL.md, not a directory
	skillMDPath := filepath.Join(skillFile, "SKILL.md")
	info, err := sr.loader.Load(skillMDPath)
	if err != nil {
		meta.State = SkillError
		meta.Error = err.Error()
		return fmt.Errorf("load skill %s: %w", name, err)
	}

	from := meta.State
	if strings.TrimSpace(info.Name) != "" && info.Name != name {
		delete(sr.skills, name)
		delete(sr.infos, name)
		name = info.Name
		meta.Name = info.Name
	}
	meta.Description = info.Description
	meta.Dir = info.Dir
	meta.Tools = nil
	for _, t := range info.Tools {
		meta.Tools = append(meta.Tools, t.Name)
	}
	meta.State = SkillLoaded
	meta.LoadedAt = time.Now()
	meta.Error = ""
	sr.skills[name] = meta
	sr.infos[name] = info

	sr.notifyWatchers(name, from, SkillLoaded)
	return nil
}

// LoadAll loads all discovered skills.
func (sr *SkillRegistry) LoadAll() error {
	sr.mu.RLock()
	names := make([]string, 0, len(sr.skills))
	for name, meta := range sr.skills {
		if meta.State == SkillDiscovered {
			names = append(names, name)
		}
	}
	sr.mu.RUnlock()

	for _, name := range names {
		if err := sr.Load(name); err != nil {
			// Continue loading others even if one fails
			continue
		}
	}
	return nil
}

// Validate checks whether a loaded skill can be registered safely.
func (sr *SkillRegistry) Validate(name string) error {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return sr.validateLocked(name)
}

func (sr *SkillRegistry) validateLocked(name string) error {
	if sr.registry == nil {
		return fmt.Errorf("tool registry not configured")
	}
	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}
	info, ok := sr.infos[name]
	if !ok || info == nil {
		return fmt.Errorf("skill %s has no loaded definition", name)
	}
	if strings.TrimSpace(meta.Name) == "" || strings.TrimSpace(info.Name) == "" {
		return fmt.Errorf("skill id is required")
	}
	if strings.TrimSpace(meta.Dir) == "" || strings.TrimSpace(info.Dir) == "" {
		return fmt.Errorf("skill %s directory is required", name)
	}
	for _, dep := range meta.Dependencies {
		if strings.TrimSpace(dep) == "" {
			continue
		}
		if _, ok := sr.skills[dep]; !ok {
			return fmt.Errorf("skill %s depends on unknown skill %s", name, dep)
		}
	}

	seenTools := make(map[string]struct{}, len(info.Tools))
	for _, toolDef := range info.Tools {
		toolName := strings.TrimSpace(toolDef.Name)
		if toolName == "" {
			return fmt.Errorf("skill %s has a tool with empty name", name)
		}
		if _, ok := seenTools[toolName]; ok {
			return fmt.Errorf("skill %s has duplicate tool %s", name, toolName)
		}
		seenTools[toolName] = struct{}{}
	}

	return nil
}

// ValidateAll checks all loaded skills before registration.
func (sr *SkillRegistry) ValidateAll() error {
	sr.mu.RLock()
	names := make([]string, 0, len(sr.skills))
	for name, meta := range sr.skills {
		switch meta.State {
		case SkillLoaded, SkillRegistered, SkillEnabled, SkillDisabled:
			names = append(names, name)
		}
	}
	sr.mu.RUnlock()

	for _, name := range names {
		if err := sr.Validate(name); err != nil {
			return err
		}
	}
	return nil
}

// Register registers a skill's tools into the tool Registry and transitions to SkillRegistered.
func (sr *SkillRegistry) Register(name string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	if meta.State != SkillLoaded && meta.State != SkillRegistered {
		return fmt.Errorf("skill %s must be loaded before registering (current state: %s)", name, meta.State)
	}

	if meta.State == SkillRegistered {
		return nil // already registered
	}

	if err := sr.validateLocked(name); err != nil {
		meta.State = SkillError
		meta.Error = err.Error()
		return err
	}
	info := sr.infos[name]
	executor := sr.executor
	if executor == nil {
		executor = NewSkillExecutor()
		sr.executor = executor
	}

	// Register tools into the tool registry
	for _, toolDef := range info.Tools {
		fullName := fmt.Sprintf("skill_%s_%s", name, toolDef.Name)
		if _, exists := sr.registry.Get(fullName); !exists {
			handler, err := executor.HandlerFor(info, toolDef)
			if err != nil {
				meta.State = SkillError
				meta.Error = err.Error()
				return fmt.Errorf("build skill tool %s: %w", fullName, err)
			}
			tool := newSkillToolFromDef(info, toolDef, handler)
			sr.registry.Register(tool)
			// Registry.Register forces Enabled=true, so we disable it again
			sr.registry.Disable(fullName)
		}
	}

	from := meta.State
	meta.State = SkillRegistered
	meta.Error = ""
	sr.notifyWatchers(name, from, SkillRegistered)
	return nil
}

// RegisterAll registers all loaded skills.
func (sr *SkillRegistry) RegisterAll() error {
	order, err := sr.ResolveLoadOrder()
	if err != nil {
		return err
	}

	sr.mu.RLock()
	names := make([]string, 0, len(sr.skills))
	for _, name := range order {
		if meta := sr.skills[name]; meta != nil && meta.State == SkillLoaded {
			names = append(names, name)
		}
	}
	sr.mu.RUnlock()

	for _, name := range names {
		if err := sr.Register(name); err != nil {
			continue
		}
	}
	return nil
}

// EnableAll enables every registered or disabled skill.
func (sr *SkillRegistry) EnableAll() error {
	sr.mu.RLock()
	names := make([]string, 0, len(sr.skills))
	for name, meta := range sr.skills {
		if meta.State == SkillRegistered || meta.State == SkillDisabled {
			names = append(names, name)
		}
	}
	sr.mu.RUnlock()
	sort.Strings(names)

	for _, name := range names {
		if err := sr.Enable(name); err != nil {
			continue
		}
	}
	return nil
}

// Enable enables a skill's tools and transitions to SkillEnabled.
func (sr *SkillRegistry) Enable(name string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	if meta.State != SkillRegistered && meta.State != SkillDisabled {
		return fmt.Errorf("skill %s must be registered before enabling (current state: %s)", name, meta.State)
	}

	for _, toolName := range meta.Tools {
		fullName := fmt.Sprintf("skill_%s_%s", name, toolName)
		sr.registry.Enable(fullName)
	}

	from := meta.State
	meta.State = SkillEnabled
	meta.Error = ""
	sr.notifyWatchers(name, from, SkillEnabled)
	return nil
}

// Disable disables a skill's tools and transitions to SkillDisabled.
func (sr *SkillRegistry) Disable(name string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	if meta.State != SkillEnabled {
		return fmt.Errorf("skill %s is not enabled (current state: %s)", name, meta.State)
	}

	for _, toolName := range meta.Tools {
		fullName := fmt.Sprintf("skill_%s_%s", name, toolName)
		sr.registry.Disable(fullName)
	}

	from := meta.State
	meta.State = SkillDisabled
	sr.notifyWatchers(name, from, SkillDisabled)
	return nil
}

// Unload removes a skill's tools from the registry and transitions to SkillUnloaded.
func (sr *SkillRegistry) Unload(name string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	if meta.State == SkillUnloaded {
		return nil
	}

	// Unregister all tools
	for _, toolName := range meta.Tools {
		fullName := fmt.Sprintf("skill_%s_%s", name, toolName)
		sr.registry.Unregister(fullName)
	}

	from := meta.State
	meta.State = SkillUnloaded
	meta.Error = ""
	sr.notifyWatchers(name, from, SkillUnloaded)
	return nil
}

// Get retrieves metadata for a skill.
func (sr *SkillRegistry) Get(name string) (*SkillMetadata, bool) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	meta, ok := sr.skills[name]
	if !ok {
		return nil, false
	}
	// Return a copy
	copy := *meta
	return &copy, true
}

// List returns all skill metadata sorted by name.
func (sr *SkillRegistry) List() []*SkillMetadata {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	var result []*SkillMetadata
	for _, meta := range sr.skills {
		copy := *meta
		result = append(result, &copy)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// SkillInfos returns loaded skill definitions sorted by name.
func (sr *SkillRegistry) SkillInfos() []*SkillInfo {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	result := make([]*SkillInfo, 0, len(sr.infos))
	for _, info := range sr.infos {
		if info == nil {
			continue
		}
		copyInfo := *info
		copyInfo.Aliases = append([]string(nil), info.Aliases...)
		copyInfo.Tools = append([]SkillToolDef(nil), info.Tools...)
		result = append(result, &copyInfo)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ListByState returns skills filtered by state.
func (sr *SkillRegistry) ListByState(state SkillState) []*SkillMetadata {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	var result []*SkillMetadata
	for _, meta := range sr.skills {
		if meta.State == state {
			copy := *meta
			result = append(result, &copy)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ResolveLoadOrder returns skill names in dependency-safe load order.
// Skills with no dependencies come first; skills that depend on others come after.
// Returns an error if a circular dependency is detected.
func (sr *SkillRegistry) ResolveLoadOrder() ([]string, error) {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	// Build adjacency list
	graph := make(map[string][]string) // skill -> skills it depends on
	inDegree := make(map[string]int)

	for name := range sr.skills {
		if _, ok := inDegree[name]; !ok {
			inDegree[name] = 0
		}
		graph[name] = nil
	}

	for name, meta := range sr.skills {
		for _, dep := range meta.Dependencies {
			if _, exists := sr.skills[dep]; !exists {
				return nil, fmt.Errorf("skill %s depends on unknown skill %s", name, dep)
			}
			graph[dep] = append(graph[dep], name)
			inDegree[name]++
		}
	}

	// Kahn's algorithm for topological sort
	var queue []string
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	sort.Strings(queue) // deterministic order

	var order []string
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		order = append(order, name)

		deps := graph[name]
		sort.Strings(deps)
		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
		sort.Strings(queue)
	}

	if len(order) != len(sr.skills) {
		return nil, fmt.Errorf("circular dependency detected among skills")
	}

	return order, nil
}

// HealthCheck verifies that all tools for enabled skills are callable.
// Returns a map of skill name -> list of unhealthy tool names.
func (sr *SkillRegistry) HealthCheck() map[string][]string {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	unhealthy := make(map[string][]string)

	for _, meta := range sr.skills {
		if meta.State != SkillEnabled {
			continue
		}

		for _, toolName := range meta.Tools {
			fullName := fmt.Sprintf("skill_%s_%s", meta.Name, toolName)
			t, ok := sr.registry.Get(fullName)
			if !ok || !t.Enabled {
				unhealthy[meta.Name] = append(unhealthy[meta.Name], toolName)
			}
		}
	}

	return unhealthy
}

// SetMetadata sets additional metadata fields on a skill.
func (sr *SkillRegistry) SetMetadata(name string, version, author string, dependencies []string, labels map[string]string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	meta, ok := sr.skills[name]
	if !ok {
		return fmt.Errorf("skill not found: %s", name)
	}

	meta.Version = version
	meta.Author = author
	meta.Dependencies = dependencies
	if labels != nil {
		if meta.Labels == nil {
			meta.Labels = make(map[string]string)
		}
		for k, v := range labels {
			meta.Labels[k] = v
		}
	}

	return nil
}

// WatchState registers a callback for skill state changes.
func (sr *SkillRegistry) WatchState(id string, callback func(name string, from, to SkillState)) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	sr.watchers[id] = callback
}

// UnwatchState removes a state change watcher.
func (sr *SkillRegistry) UnwatchState(id string) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	delete(sr.watchers, id)
}

// notifyWatchers calls all registered state change watchers.
func (sr *SkillRegistry) notifyWatchers(name string, from, to SkillState) {
	for _, cb := range sr.watchers {
		cb(name, from, to)
	}
}

// Count returns the number of skills.
func (sr *SkillRegistry) Count() int {
	sr.mu.RLock()
	defer sr.mu.RUnlock()
	return len(sr.skills)
}

// CountByState returns the number of skills in a given state.
func (sr *SkillRegistry) CountByState(state SkillState) int {
	sr.mu.RLock()
	defer sr.mu.RUnlock()

	count := 0
	for _, meta := range sr.skills {
		if meta.State == state {
			count++
		}
	}
	return count
}

// Reload reloads a skill: unload → discover → load → register → re-enable if was enabled.
func (sr *SkillRegistry) Reload(name string) error {
	sr.mu.RLock()
	wasEnabled := false
	if meta, ok := sr.skills[name]; ok && meta.State == SkillEnabled {
		wasEnabled = true
	}
	sr.mu.RUnlock()

	if err := sr.Unload(name); err != nil {
		return fmt.Errorf("reload: unload %s: %w", name, err)
	}

	if _, err := sr.Discover(); err != nil {
		return fmt.Errorf("reload: discover: %w", err)
	}

	if err := sr.Load(name); err != nil {
		return fmt.Errorf("reload: load %s: %w", name, err)
	}

	if err := sr.Register(name); err != nil {
		return fmt.Errorf("reload: register %s: %w", name, err)
	}

	if wasEnabled {
		if err := sr.Enable(name); err != nil {
			return fmt.Errorf("reload: enable %s: %w", name, err)
		}
	}

	return nil
}
