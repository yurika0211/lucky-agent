// Markdown note format: frontmatter, paths, wikilinks, render, and parse.
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/yurika0211/luckyagent/internal/memory/notemd"
	"github.com/yurika0211/luckyagent/internal/memory/policy"
)

type memoryNoteFrontmatter struct {
	ID            string        `yaml:"id"`
	Type          string        `yaml:"type"`
	Tier          string        `yaml:"tier"`
	Category      string        `yaml:"category"`
	Importance    float64       `yaml:"importance"`
	AccessCount   int           `yaml:"access_count"`
	CreatedAt     time.Time     `yaml:"created_at"`
	AccessedAt    time.Time     `yaml:"accessed_at"`
	Tags          []string      `yaml:"tags,omitempty"`
	SummaryOf     []string      `yaml:"summary_of,omitempty"`
	ExpiresAt     *time.Time    `yaml:"expires_at,omitempty"`
	Status        string        `yaml:"status,omitempty"`
	ValidFrom     time.Time     `yaml:"valid_from,omitempty"`
	ValidUntil    *time.Time    `yaml:"valid_until,omitempty"`
	Links         []string      `yaml:"links,omitempty"`
	Aliases       []string      `yaml:"aliases,omitempty"`
	StateKey      string        `yaml:"state_key,omitempty"`
	StateValue    string        `yaml:"state_value,omitempty"`
	Confidence    float64       `yaml:"confidence,omitempty"`
	Supersedes    []string      `yaml:"supersedes,omitempty"`
	RoutePolicies []RoutePolicy `yaml:"route_policies,omitempty"`
	BlockID       string        `yaml:"block_id,omitempty"`
}

var wikiLinkPattern = regexp.MustCompile(`!?\[\[([^\]|#]+)(?:[#|][^\]]*)?\]\]`)
var blockIDPattern = notemd.BlockIDPattern

func normalizeEntryForNote(e *Entry) {
	now := time.Now()
	if e.ID == "" {
		e.ID = fmt.Sprintf("mem_%d", now.UnixNano())
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.AccessedAt.IsZero() {
		e.AccessedAt = e.CreatedAt
	}
	if e.Status == "" {
		e.Status = "active"
	}
	if e.ValidFrom.IsZero() {
		e.ValidFrom = e.CreatedAt
	}
	if e.BlockID == "" {
		e.BlockID = blockIDForEntry(e.ID)
	}
	e.Tags = dedupSlice(e.Tags)
	e.Aliases = dedupSlice(e.Aliases)
	e.Supersedes = dedupSlice(e.Supersedes)
	if policies, err := policy.Normalize(e.RoutePolicies); err == nil {
		e.RoutePolicies = policies
	}
	e.StateKey = strings.TrimSpace(e.StateKey)
	e.StateValue = strings.TrimSpace(e.StateValue)
	if e.Confidence < 0 || e.Confidence > 1 {
		e.Confidence = clampFloat(e.Confidence, 0, 1)
	}
	e.Links = normalizeMemoryLinks(append(e.Links, extractWikiLinks(e.Content)...))
}

func blockIDForEntry(id string) string {
	id = strings.TrimSpace(id)
	id = strings.ReplaceAll(id, "_", "-")
	if id == "" {
		return "mem-block"
	}
	return id
}

func notePathForEntry(e *Entry) string {
	if strings.EqualFold(strings.TrimSpace(e.Category), "concept") {
		return conceptNotePath(e.Content)
	}
	dir := noteDirForEntry(e)
	name := humanMemoryFileBase(e) + ".md"
	return filepath.ToSlash(filepath.Join(dir, name))
}

func (s *Store) uniqueNotePathForEntry(e *Entry, used map[string]string, currentRel string) string {
	currentRel = filepath.ToSlash(strings.TrimSpace(currentRel))
	if strings.EqualFold(strings.TrimSpace(e.Status), "archived") || strings.HasPrefix(currentRel, "90_Archive/dirty/") {
		return uniqueNotePath(s.dir, "90_Archive/dirty", humanMemoryFileBase(e), used, currentRel)
	}
	if strings.EqualFold(strings.TrimSpace(e.Category), "concept") {
		return uniqueNotePath(s.dir, "70_Concepts", humanFileTitle(e.Content, "Concept"), used, currentRel)
	}
	return uniqueNotePath(s.dir, noteDirForEntry(e), humanMemoryFileBase(e), used, currentRel)
}

func uniqueNotePath(root, dir, base string, used map[string]string, currentRel string) string {
	base = humanFileTitle(base, "Memory")
	currentRel = filepath.ToSlash(strings.TrimSpace(currentRel))
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s %d", base, i+1)
		}
		rel := filepath.ToSlash(filepath.Join(dir, name+".md"))
		if rel == currentRel {
			return rel
		}
		if _, ok := used[rel]; ok {
			continue
		}
		if root != "" {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if _, err := os.Stat(path); err == nil {
				continue
			}
		}
		return rel
	}
}

func humanMemoryFileBase(e *Entry) string {
	if e == nil {
		return "Memory"
	}
	if text := firstHumanTitleLine(stripWikiSyntax(e.Content)); text != "" {
		return humanFileTitle(text, "Memory")
	}
	for _, alias := range e.Aliases {
		if strings.TrimSpace(alias) == "" {
			continue
		}
		if title := humanFileTitle(alias, ""); title != "" {
			return title
		}
	}
	for _, link := range e.Links {
		if strings.TrimSpace(link) == "" {
			continue
		}
		if title := humanFileTitle(link, ""); title != "" {
			return title
		}
	}
	if category := strings.TrimSpace(e.Category); category != "" {
		return humanFileTitle(category+" memory", "Memory")
	}
	return "Memory"
}

func firstHumanTitleLine(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimLeft(line, "#>-* \t")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "^") || strings.HasPrefix(line, "```") {
			continue
		}
		return strings.Join(strings.Fields(line), " ")
	}
	return ""
}

func humanFileTitle(text, fallback string) string {
	text = strings.TrimSpace(stripWikiSyntax(text))
	if text == "" {
		text = strings.TrimSpace(fallback)
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		if r < 32 || r == 127 || strings.ContainsRune(`<>:"/\|?*`, r) || unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	out := strings.Trim(b.String(), " .-_")
	if out == "" {
		out = strings.TrimSpace(fallback)
	}
	if out == "" {
		out = "Memory"
	}
	out = strings.Trim(truncateRunes(out, 80), " .-_")
	if out == "" {
		out = "Memory"
	}
	if windowsReservedFileTitle(out) {
		out += " note"
	}
	return out
}

func windowsReservedFileTitle(title string) bool {
	title = strings.TrimSpace(strings.TrimSuffix(title, "."))
	title = strings.ToUpper(title)
	switch title {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	for i := 1; i <= 9; i++ {
		if title == fmt.Sprintf("COM%d", i) || title == fmt.Sprintf("LPT%d", i) {
			return true
		}
	}
	return false
}

func noteDirForEntry(e *Entry) string {
	category := strings.ToLower(strings.TrimSpace(e.Category))
	switch category {
	case "identity", "preference", "profile", "user":
		return "10_Profile"
	case "project", "context", "code", "repo":
		return "20_Projects"
	case "decision", "architecture":
		return "40_Decisions"
	case "rule", "tool", "workflow":
		return "60_Rules"
	case "concept":
		return "70_Concepts"
	case "conversation", "task", "session":
		return "30_Sessions"
	case "archive":
		return "90_Archive"
	default:
		if e.Tier == TierLong {
			return "50_Facts"
		}
		return "50_Facts"
	}
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func stripWikiSyntax(s string) string {
	return wikiLinkPattern.ReplaceAllStringFunc(s, func(match string) string {
		parts := wikiLinkPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}
		return parts[1]
	})
}

func truncateRunes(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func renderMemoryNote(e *Entry) string {
	fm := memoryNoteFrontmatter{
		ID:            e.ID,
		Type:          "memory",
		Tier:          e.Tier.String(),
		Category:      e.Category,
		Importance:    e.Importance,
		AccessCount:   e.AccessCount,
		CreatedAt:     e.CreatedAt,
		AccessedAt:    e.AccessedAt,
		Tags:          e.Tags,
		SummaryOf:     e.SummaryOf,
		ExpiresAt:     e.ExpiresAt,
		Status:        e.Status,
		ValidFrom:     e.ValidFrom,
		ValidUntil:    e.ValidUntil,
		Links:         e.Links,
		Aliases:       e.Aliases,
		StateKey:      e.StateKey,
		StateValue:    e.StateValue,
		Confidence:    e.Confidence,
		Supersedes:    e.Supersedes,
		RoutePolicies: e.RoutePolicies,
		BlockID:       e.BlockID,
	}
	yml, _ := yaml.Marshal(fm)
	title := strings.TrimSpace(stripWikiSyntax(e.Content))
	if title == "" {
		title = e.ID
	}
	title = truncateRunes(strings.ReplaceAll(title, "\n", " "), 80)

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yml)
	b.WriteString("---\n\n")
	b.WriteString("# " + title + "\n\n")
	b.WriteString("## Memory\n\n")
	b.WriteString(strings.TrimSpace(e.Content))
	b.WriteString("\n\n^" + e.BlockID + "\n")
	if len(e.Links) > 0 || len(e.SummaryOf) > 0 {
		b.WriteString("\n## Links\n\n")
		for _, link := range e.Links {
			b.WriteString("- [[" + link + "]]\n")
		}
		for _, id := range e.SummaryOf {
			b.WriteString("- Summary source: `" + strings.TrimSpace(id) + "`\n")
		}
	}
	return b.String()
}

func parseMemoryNote(path, root string) (*Entry, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	fmRaw, body, ok := splitFrontmatter(string(raw))
	if !ok {
		return nil, false, nil
	}
	var fm memoryNoteFrontmatter
	if err := yaml.Unmarshal([]byte(fmRaw), &fm); err != nil {
		return nil, false, err
	}
	if fm.Type != "memory" || fm.ID == "" {
		return nil, false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	content := extractMarkdownSection(body, "Memory")
	if content == "" {
		content = strings.TrimSpace(bodyWithoutTitle(body))
	}
	content = strings.TrimSpace(blockIDPattern.ReplaceAllString(content, ""))
	policies, err := policy.Normalize(fm.RoutePolicies)
	if err != nil {
		return nil, false, fmt.Errorf("invalid route_policies: %w", err)
	}

	entry := &Entry{
		ID:            fm.ID,
		Content:       content,
		Category:      fm.Category,
		Tier:          parseTier(fm.Tier),
		Importance:    fm.Importance,
		AccessCount:   fm.AccessCount,
		CreatedAt:     fm.CreatedAt,
		AccessedAt:    fm.AccessedAt,
		Tags:          fm.Tags,
		SummaryOf:     fm.SummaryOf,
		ExpiresAt:     fm.ExpiresAt,
		Status:        fm.Status,
		ValidFrom:     fm.ValidFrom,
		ValidUntil:    fm.ValidUntil,
		Links:         normalizeMemoryLinks(append(fm.Links, extractWikiLinks(content)...)),
		Aliases:       dedupSlice(fm.Aliases),
		StateKey:      fm.StateKey,
		StateValue:    fm.StateValue,
		Confidence:    fm.Confidence,
		Supersedes:    dedupSlice(fm.Supersedes),
		RoutePolicies: policies,
		BlockID:       fm.BlockID,
		Path:          filepath.ToSlash(rel),
	}
	normalizeEntryForNote(entry)
	return entry, true, nil
}

func splitFrontmatter(md string) (string, string, bool) {
	return notemd.SplitFrontmatter(md)
}

func bodyWithoutTitle(body string) string {
	return notemd.BodyWithoutTitle(body)
}

func extractMarkdownSection(body, heading string) string {
	return notemd.Section(body, heading)
}

func parseTier(raw string) Tier {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "short", "0":
		return TierShort
	case "medium", "mid", "1", "":
		return TierMedium
	case "long", "2":
		return TierLong
	default:
		return TierMedium
	}
}

func extractWikiLinks(text string) []string {
	matches := wikiLinkPattern.FindAllStringSubmatch(text, -1)
	links := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		link := strings.TrimSpace(m[1])
		if link != "" {
			links = append(links, link)
		}
	}
	return normalizeLinks(links)
}

func normalizeLinks(links []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(links))
	for _, link := range links {
		link = strings.TrimSpace(link)
		if link == "" {
			continue
		}
		key := strings.ToLower(link)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, link)
	}
	return out
}

func normalizeMemoryLinks(links []string) []string {
	normalized := normalizeLinks(links)
	out := make([]string, 0, len(normalized))
	for _, link := range normalized {
		link = canonicalMemoryLinkTarget(link)
		if link == "" {
			continue
		}
		out = append(out, link)
	}
	return normalizeLinks(out)
}

func canonicalMemoryLinkTarget(link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	if rule, ok := conceptRuleForName(link); ok {
		return rule.Concept
	}
	return link
}
