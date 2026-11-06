package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yurika0211/luckyagent-tools/memory"
)

// DefaultMaxMemoryNoteBytes is the on-disk cap for one vault note.
// Remember refuses before the inner store runs, because persist rewrites every note.
const DefaultMaxMemoryNoteBytes int64 = 4 << 20

const memoryNoteRewriteOverhead = 4 << 10

var generatedMemoryHeading = regexp.MustCompile(`(?m)^## (?:Memory|Links)\s*$`)

// cappedMemoryStore refuses writes that would persist a note past maxBytes
// or that repeat generated Memory/Links headings. It does not read note bodies.
type cappedMemoryStore struct {
	memory.RuntimeStore
	maxBytes int64
}

func capMemoryStore(store memory.RuntimeStore, maxBytes int64) memory.RuntimeStore {
	if store == nil {
		return nil
	}
	if existing, ok := store.(*cappedMemoryStore); ok {
		if maxBytes <= 0 || existing.maxBytes == maxBytes {
			return existing
		}
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxMemoryNoteBytes
	}
	return &cappedMemoryStore{RuntimeStore: store, maxBytes: maxBytes}
}

func (s *cappedMemoryStore) Remember(ctx context.Context, request memory.RememberRequest) (memory.RememberResult, error) {
	if s == nil || s.RuntimeStore == nil {
		return memory.RememberResult{}, &memory.WriteRejectedError{Message: "memory store not initialized"}
	}
	if err := ctx.Err(); err != nil {
		return memory.RememberResult{}, err
	}
	if err := s.refuseRemember(request); err != nil {
		return memory.RememberResult{}, err
	}
	return s.RuntimeStore.Remember(ctx, request)
}

func (s *cappedMemoryStore) refuseRemember(request memory.RememberRequest) error {
	content := strings.TrimSpace(request.Content)
	if int64(len(content)) > s.maxBytes {
		return &memory.WriteRejectedError{Message: "memory note content exceeds the file byte cap"}
	}
	if len(generatedMemoryHeading.FindAllString(content, -1)) >= 3 {
		return &memory.WriteRejectedError{Message: "memory write refused: content repeats generated ## Memory or ## Links blocks"}
	}
	root := strings.TrimSpace(s.Source().Location)
	if root == "" {
		return nil
	}
	if path, size, ok := oversizeVaultNote(root, s.maxBytes); ok {
		return &memory.WriteRejectedError{Message: "memory write refused: vault note " + path + " already exceeds the file byte cap"}
	} else if size < 0 && path != "" {
		return &memory.WriteRejectedError{Message: "memory write refused: cannot stat vault notes"}
	}
	projected, rel, existed := s.projectedBytes(root, request, content)
	if projected > s.maxBytes {
		return &memory.WriteRejectedError{Message: "memory write refused: destination note would exceed the file byte cap"}
	}
	_ = existed
	_ = rel
	return nil
}

func (s *cappedMemoryStore) projectedBytes(root string, request memory.RememberRequest, content string) (int64, string, bool) {
	estimate := int64(len(content)) + int64(len(strings.Join(request.Options.Links, "\n")))
	rel := destinationNotePath(s.RuntimeStore, request, content)
	if rel == "" {
		return estimate, rel, false
	}
	info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	if statErr != nil {
		return estimate, rel, false
	}
	estimate += memoryNoteRewriteOverhead
	return info.Size() + estimate, rel, true
}

func destinationNotePath(store memory.RuntimeStore, request memory.RememberRequest, content string) string {
	if store == nil {
		return ""
	}
	key := strings.TrimSpace(request.Options.StateKey)
	for _, entry := range store.Recent(64) {
		if key != "" && entry.StateKey == key {
			return entry.Path
		}
		if strings.TrimSpace(entry.Content) == content && strings.EqualFold(entry.Category, request.Category) {
			return entry.Path
		}
	}
	title := memoryNoteTitle(content, request.Category)
	dir := memoryNoteDir(request.Category)
	if title == "" || dir == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(dir, title+".md"))
}

func memoryNoteDir(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "concept":
		return "70_Concepts"
	case "identity", "preference", "profile", "user":
		return "10_Profile"
	case "project", "context", "code", "repo":
		return "20_Projects"
	case "decision", "architecture":
		return "40_Decisions"
	case "rule", "tool", "workflow":
		return "60_Rules"
	case "conversation", "task", "session":
		return "30_Sessions"
	case "archive":
		return "90_Archive"
	default:
		return "50_Facts"
	}
}

func memoryNoteTitle(content, category string) string {
	line := ""
	for _, part := range strings.Split(content, "\n") {
		part = strings.TrimSpace(strings.TrimLeft(part, "#>-* \t"))
		if part == "" || strings.HasPrefix(part, "^") || strings.HasPrefix(part, "```") {
			continue
		}
		line = strings.Join(strings.Fields(part), " ")
		break
	}
	if line == "" {
		line = strings.TrimSpace(category) + " memory"
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range line {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
		if b.Len() >= 80 {
			break
		}
	}
	out := strings.Trim(b.String(), " .-_")
	if out == "" {
		return "Memory"
	}
	return out
}

func oversizeVaultNote(root string, maxBytes int64) (string, int64, bool) {
	var found string
	var foundSize int64
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxBytes && (found == "" || info.Size() > foundSize) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			found = filepath.ToSlash(rel)
			foundSize = info.Size()
		}
		return nil
	})
	if err != nil {
		return found, -1, false
	}
	if found == "" {
		return "", 0, false
	}
	return found, foundSize, true
}
