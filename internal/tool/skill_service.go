package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultSkillReadLimit    = 50
	defaultSkillReadMaxChars = 12000
	maxSkillReadMaxChars     = 50000
)

// SkillToolService wraps skill tool registration and skill_read access.
type SkillToolService struct {
	skills []*SkillInfo
	mu     sync.Mutex
	cache  map[string]cachedSkillDoc
}

// NewSkillToolService creates a skill tool service.
func NewSkillToolService(skills []*SkillInfo) *SkillToolService {
	return &SkillToolService{skills: skills, cache: make(map[string]cachedSkillDoc)}
}

type cachedSkillDoc struct {
	modTime time.Time
	size    int64
	content string
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
			"action": {
				Type:        "string",
				Description: "Action: list, read, metadata, or sections. Defaults to list without name and read with name.",
				Required:    false,
			},
			"format": {
				Type:        "string",
				Description: "Response format: json or text.",
				Required:    false,
				Default:     "json",
			},
			"detail": {
				Type:        "string",
				Description: "Detail level: summary, metadata, or full. full is required to request unbounded content.",
				Required:    false,
				Default:     "summary",
			},
			"section": {
				Type:        "string",
				Description: "Optional Markdown heading to return from SKILL.md.",
				Required:    false,
			},
			"max_chars": {
				Type:        "number",
				Description: "Maximum content characters to return. Defaults to 12000.",
				Required:    false,
				Default:     defaultSkillReadMaxChars,
			},
			"offset": {
				Type:        "number",
				Description: "Character offset for continuing a truncated read or paginating skill lists.",
				Required:    false,
				Default:     0,
			},
			"limit": {
				Type:        "number",
				Description: "Maximum skills to list. Defaults to 50.",
				Required:    false,
				Default:     defaultSkillReadLimit,
			},
			"include_tools": {
				Type:        "boolean",
				Description: "Whether to include skill tool metadata.",
				Required:    false,
				Default:     true,
			},
			"include_commands": {
				Type:        "boolean",
				Description: "Whether to include local tool commands. Defaults to false.",
				Required:    false,
				Default:     false,
			},
		},
		Handler: s.HandleRead,
	})
}

// HandleRead reads a skill's SKILL.md or lists available skills.
func (s *SkillToolService) HandleRead(args map[string]any) (string, error) {
	if s == nil {
		return "", fmt.Errorf("skill service not initialized")
	}
	opts, err := parseSkillReadOptions(args)
	if err != nil {
		return "", err
	}

	if opts.Action == "list" {
		return s.handleSkillList(opts)
	}

	for _, skill := range s.skills {
		if skillMatchesName(skill, opts.Name) {
			return s.handleSkillMatch(skill, opts)
		}
	}

	candidates := skillReadCandidates(s.skills, opts.Name, 5)
	if opts.Format == "json" {
		return prettyStructuredValue(map[string]any{
			"found":      false,
			"name":       opts.Name,
			"candidates": candidates,
			"message":    skillNotFoundMessage(opts.Name, candidates),
		})
	}
	return skillNotFoundMessage(opts.Name, candidates), nil
}

type skillReadOptions struct {
	Action          string
	Name            string
	Format          string
	Detail          string
	Section         string
	MaxChars        int
	MaxCharsSet     bool
	Offset          int
	Limit           int
	IncludeTools    bool
	IncludeCommands bool
}

func parseSkillReadOptions(args map[string]any) (skillReadOptions, error) {
	name := stringArg(args["name"])
	format, err := parseSkillReadFormat(stringArg(args["format"]))
	if err != nil {
		return skillReadOptions{}, err
	}
	action := strings.ToLower(strings.TrimSpace(stringArg(args["action"])))
	if action == "" {
		if name == "" {
			action = "list"
		} else {
			action = "read"
		}
	}
	switch action {
	case "list", "read", "metadata", "sections":
	default:
		return skillReadOptions{}, fmt.Errorf("invalid skill_read action %q (use list, read, metadata, or sections)", action)
	}
	if action != "list" && name == "" {
		return skillReadOptions{}, fmt.Errorf("name is required for action=%s", action)
	}
	detail := strings.ToLower(strings.TrimSpace(stringArg(args["detail"])))
	if detail == "" {
		detail = "summary"
	}
	switch detail {
	case "summary", "metadata", "full":
	default:
		return skillReadOptions{}, fmt.Errorf("detail must be summary, metadata, or full")
	}
	maxChars := defaultSkillReadMaxChars
	maxCharsSet := false
	if raw, ok := numberArg(args["max_chars"]); ok {
		maxChars = int(raw)
		maxCharsSet = true
	}
	if maxChars <= 0 || maxChars > maxSkillReadMaxChars {
		return skillReadOptions{}, fmt.Errorf("max_chars must be between 1 and %d", maxSkillReadMaxChars)
	}
	offset := 0
	if raw, ok := numberArg(args["offset"]); ok {
		offset = int(raw)
	}
	if offset < 0 {
		return skillReadOptions{}, fmt.Errorf("offset must be >= 0")
	}
	limit := defaultSkillReadLimit
	if raw, ok := numberArg(args["limit"]); ok {
		limit = int(raw)
	}
	if limit <= 0 || limit > defaultSkillReadLimit {
		return skillReadOptions{}, fmt.Errorf("limit must be between 1 and %d", defaultSkillReadLimit)
	}
	includeTools := true
	if _, ok := args["include_tools"]; ok {
		includeTools = boolArg(args["include_tools"])
	}
	return skillReadOptions{
		Action:          action,
		Name:            name,
		Format:          format,
		Detail:          detail,
		Section:         stringArg(args["section"]),
		MaxChars:        maxChars,
		MaxCharsSet:     maxCharsSet,
		Offset:          offset,
		Limit:           limit,
		IncludeTools:    includeTools,
		IncludeCommands: boolArg(args["include_commands"]),
	}, nil
}

func parseSkillReadFormat(raw string) (string, error) {
	format := strings.ToLower(strings.TrimSpace(raw))
	if format == "" {
		return "json", nil
	}
	switch format {
	case "json", "text":
		return format, nil
	default:
		return "", fmt.Errorf("invalid skill_read format %q (use json or text)", raw)
	}
}

func (s *SkillToolService) handleSkillList(opts skillReadOptions) (string, error) {
	start := opts.Offset
	if start > len(s.skills) {
		start = len(s.skills)
	}
	end := start + opts.Limit
	if end > len(s.skills) {
		end = len(s.skills)
	}
	selected := s.skills[start:end]
	if opts.Format == "json" {
		items := make([]map[string]any, 0, len(selected))
		for _, skill := range selected {
			items = append(items, skillReadMetadata(skill, skillReadMetadataOptions{
				IncludeTools:    opts.IncludeTools && opts.Detail != "summary",
				IncludeCommands: opts.IncludeCommands,
			}))
		}
		payload := map[string]any{
			"skills": items,
			"count":  len(items),
			"total":  len(s.skills),
			"offset": start,
		}
		if end < len(s.skills) {
			payload["next_offset"] = end
		}
		return prettyStructuredValue(payload)
	}

	var b strings.Builder
	b.WriteString("Available skills:\n")
	for _, skill := range selected {
		b.WriteString(fmt.Sprintf("- %s: %s\n", skill.Name, skillReadSummary(skill)))
	}
	if end < len(s.skills) {
		b.WriteString(fmt.Sprintf("... truncated; use offset=%d to continue\n", end))
	}
	return b.String(), nil
}

func (s *SkillToolService) handleSkillMatch(skill *SkillInfo, opts skillReadOptions) (string, error) {
	skillFile, err := resolveSkillMarkdownPath(skill)
	if err != nil {
		return "", err
	}
	content, err := s.readSkillDoc(skillFile)
	if err != nil {
		return "", fmt.Errorf("read SKILL.md for %s: %w", opts.Name, err)
	}
	sections := parseSkillDocSections(content)
	if opts.Action == "sections" {
		if opts.Format == "json" {
			return prettyStructuredValue(map[string]any{
				"found":         true,
				"name":          skill.Name,
				"skill_md_path": skillFile,
				"sections":      sections,
			})
		}
		return formatSkillSectionsText(sections), nil
	}

	meta := skillReadMetadata(skill, skillReadMetadataOptions{
		SkillMDPath:      skillFile,
		IncludeTools:     opts.IncludeTools,
		IncludeCommands:  opts.IncludeCommands,
		IncludeDirectory: true,
	})
	meta["sections"] = sections
	if opts.Action == "metadata" || opts.Detail == "metadata" {
		if opts.Format == "json" {
			return prettyStructuredValue(meta)
		}
		return formatSkillMetadataText(skill, skillFile), nil
	}

	readText := content
	if opts.Section != "" {
		var ok bool
		readText, ok = extractSkillDocSection(content, sections, opts.Section)
		if !ok {
			return "", fmt.Errorf("section %q not found in skill %q", opts.Section, skill.Name)
		}
	}
	contentPayload := sliceSkillReadContent(readText, opts.Offset, skillReadMaxChars(opts, readText))
	if opts.Format == "json" {
		meta["content"] = contentPayload
		return prettyStructuredValue(meta)
	}
	if contentPayload.Truncated {
		return fmt.Sprintf("%s\n... truncated; use offset=%d to continue", contentPayload.Text, contentPayload.NextOffset), nil
	}
	return contentPayload.Text, nil
}

func (s *SkillToolService) readSkillDoc(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = make(map[string]cachedSkillDoc)
	}
	if cached, ok := s.cache[path]; ok && cached.size == info.Size() && cached.modTime.Equal(info.ModTime()) {
		return cached.content, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := string(data)
	s.cache[path] = cachedSkillDoc{modTime: info.ModTime(), size: info.Size(), content: content}
	return content, nil
}

type skillReadMetadataOptions struct {
	SkillMDPath      string
	IncludeTools     bool
	IncludeCommands  bool
	IncludeDirectory bool
}

func skillReadMetadata(skill *SkillInfo, opts skillReadMetadataOptions) map[string]any {
	if skill == nil {
		return map[string]any{}
	}
	payload := map[string]any{
		"found":       true,
		"name":        skill.Name,
		"aliases":     append([]string(nil), skill.Aliases...),
		"description": skill.Description,
		"summary":     skillReadSummary(skill),
		"available":   skill.Available,
	}
	if opts.IncludeDirectory {
		payload["dir"] = skill.Dir
	}
	if opts.SkillMDPath != "" {
		payload["skill_md_path"] = opts.SkillMDPath
	} else if skill.Dir != "" {
		payload["skill_md_path"] = filepath.Join(skill.Dir, "SKILL.md")
	}
	if opts.IncludeTools {
		toolItems := make([]map[string]any, 0, len(skill.Tools))
		for _, toolDef := range skill.Tools {
			item := map[string]any{
				"name":            toolDef.Name,
				"description":     toolDef.Description,
				"expose_to_model": toolDef.ExposeToModel,
			}
			if opts.IncludeCommands {
				item["command"] = toolDef.Command
			}
			toolItems = append(toolItems, item)
		}
		payload["tools"] = toolItems
	}
	return payload
}

func skillReadSummary(skill *SkillInfo) string {
	if skill == nil {
		return ""
	}
	summary := strings.TrimSpace(skill.Summary)
	if summary == "" {
		summary = strings.TrimSpace(skill.Description)
	}
	return summary
}

type SkillReadContent struct {
	Text       string `json:"text"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset,omitempty"`
	Truncated  bool   `json:"truncated"`
	TotalChars int    `json:"total_chars"`
}

type SkillDocSection struct {
	Heading string `json:"heading"`
	Level   int    `json:"level"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

type SkillCandidate struct {
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

func skillReadMaxChars(opts skillReadOptions, content string) int {
	if opts.Detail == "full" && !opts.MaxCharsSet {
		return len([]rune(content))
	}
	return opts.MaxChars
}

func sliceSkillReadContent(content string, offset, maxChars int) SkillReadContent {
	runes := []rune(content)
	total := len(runes)
	if offset > total {
		offset = total
	}
	end := offset + maxChars
	if end > total {
		end = total
	}
	out := SkillReadContent{
		Text:       string(runes[offset:end]),
		Offset:     offset,
		TotalChars: total,
		Truncated:  end < total,
	}
	if out.Truncated {
		out.NextOffset = end
	}
	return out
}

func parseSkillDocSections(content string) []SkillDocSection {
	type heading struct {
		section SkillDocSection
	}
	lines := strings.SplitAfter(content, "\n")
	offset := 0
	headings := make([]heading, 0)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for level < len(trimmed) && trimmed[level] == '#' {
				level++
			}
			if level > 0 && level <= 6 && len(trimmed) > level && trimmed[level] == ' ' {
				headings = append(headings, heading{section: SkillDocSection{
					Heading: strings.TrimSpace(trimmed[level:]),
					Level:   level,
					Start:   offset,
					End:     len(content),
				}})
			}
		}
		offset += len([]byte(line))
	}
	sections := make([]SkillDocSection, 0, len(headings))
	for i, h := range headings {
		section := h.section
		for j := i + 1; j < len(headings); j++ {
			if headings[j].section.Level <= section.Level {
				section.End = headings[j].section.Start
				break
			}
		}
		sections = append(sections, section)
	}
	return sections
}

func extractSkillDocSection(content string, sections []SkillDocSection, heading string) (string, bool) {
	target := normalizeSkillLookup(heading)
	if target == "" {
		return "", false
	}
	for _, section := range sections {
		if normalizeSkillLookup(section.Heading) == target {
			if section.Start < 0 || section.End > len(content) || section.Start > section.End {
				return "", false
			}
			return strings.TrimSpace(content[section.Start:section.End]), true
		}
	}
	return "", false
}

func formatSkillSectionsText(sections []SkillDocSection) string {
	if len(sections) == 0 {
		return "No sections found."
	}
	var b strings.Builder
	b.WriteString("Skill sections:\n")
	for _, section := range sections {
		b.WriteString(fmt.Sprintf("- %s%s\n", strings.Repeat("#", section.Level), " "+section.Heading))
	}
	return b.String()
}

func formatSkillMetadataText(skill *SkillInfo, skillFile string) string {
	return fmt.Sprintf("Skill: %s\nSummary: %s\nSKILL.md: %s", skill.Name, skillReadSummary(skill), skillFile)
}

func resolveSkillMarkdownPath(skill *SkillInfo) (string, error) {
	if skill == nil {
		return "", fmt.Errorf("skill is nil")
	}
	if strings.TrimSpace(skill.Dir) == "" {
		return "", fmt.Errorf("skill %q has no directory", skill.Name)
	}
	root, err := filepath.Abs(filepath.Clean(skill.Dir))
	if err != nil {
		return "", fmt.Errorf("resolve skill directory for %q: %w", skill.Name, err)
	}
	skillFile := filepath.Join(root, "SKILL.md")
	info, err := os.Lstat(skillFile)
	if err != nil {
		return "", err
	}
	resolved := skillFile
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err = filepath.EvalSymlinks(skillFile)
		if err != nil {
			return "", fmt.Errorf("resolve SKILL.md symlink for %q: %w", skill.Name, err)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return "", fmt.Errorf("skill %q SKILL.md is outside skill directory", skill.Name)
		}
		info, err = os.Stat(resolved)
		if err != nil {
			return "", err
		}
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("skill %q SKILL.md is not a regular file", skill.Name)
	}
	return resolved, nil
}

func skillReadCandidates(skills []*SkillInfo, name string, limit int) []SkillCandidate {
	target := normalizeSkillLookup(name)
	lower := strings.ToLower(strings.TrimSpace(name))
	candidates := make([]SkillCandidate, 0, len(skills))
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		score, reason := skillCandidateScore(skill, target, lower)
		if score <= 0 {
			continue
		}
		candidates = append(candidates, SkillCandidate{Name: skill.Name, Score: score, Reason: reason})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Name < candidates[j].Name
		}
		return candidates[i].Score > candidates[j].Score
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func skillCandidateScore(skill *SkillInfo, target, lower string) (int, string) {
	if target == "" && lower == "" {
		return 0, ""
	}
	if normalizeSkillLookup(skill.Name) == target {
		return 95, "normalized name match"
	}
	if strings.Contains(strings.ToLower(skill.Name), lower) || strings.Contains(normalizeSkillLookup(skill.Name), target) {
		return 70, "name contains query"
	}
	for _, alias := range skill.Aliases {
		if normalizeSkillLookup(alias) == target {
			return 90, "alias match"
		}
		if strings.Contains(strings.ToLower(alias), lower) || strings.Contains(normalizeSkillLookup(alias), target) {
			return 65, "alias contains query"
		}
	}
	text := strings.ToLower(skill.Description + " " + skill.Summary)
	if lower != "" && strings.Contains(text, lower) {
		return 50, "description contains query"
	}
	queryTokens := strings.Fields(strings.ReplaceAll(target, "-", " "))
	if len(queryTokens) == 0 {
		return 0, ""
	}
	hits := 0
	for _, token := range queryTokens {
		if token != "" && strings.Contains(text, token) {
			hits++
		}
	}
	if hits > 0 {
		return 30 + hits*5, "description token overlap"
	}
	return 0, ""
}

func skillNotFoundMessage(name string, candidates []SkillCandidate) string {
	if len(candidates) > 0 {
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}
		return fmt.Sprintf("Skill '%s' not found. Did you mean: %s?", name, strings.Join(names, ", "))
	}
	return fmt.Sprintf("Skill '%s' not found. Use skill_read without name to list all skills.", name)
}

func skillMatchesName(s *SkillInfo, name string) bool {
	if s == nil {
		return false
	}
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
