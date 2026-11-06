package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/bootstrap"
	"github.com/yurika0211/luckyagent-runtime/config"
	"github.com/yurika0211/luckyagent-runtime/tool"
	"github.com/yurika0211/luckyagent-tools/memory"
)

func TestRememberRejectsRepeatedMemoryLinksBlocks(t *testing.T) {
	a := memoryCapAgent(t, DefaultMaxMemoryNoteBytes)
	root := a.memory.Source().Location
	before := vaultMarkdownBytes(t, root)
	content := strings.Join([]string{
		"Stable fact about the vault.",
		"## Memory",
		"first generated body",
		"## Links",
		"[[Other]]",
		"## Memory",
		"second generated body",
	}, "\n")
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: content, Category: "fact", Tier: memory.TierMedium, Importance: 0.5,
	})
	var rejected *memory.WriteRejectedError
	if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "Memory") {
		t.Fatalf("err=%v", err)
	}
	if after := vaultMarkdownBytes(t, root); after != before {
		t.Fatalf("vault grew %d -> %d", before, after)
	}
}

func TestRememberRejectsWhenDestinationWouldExceedCap(t *testing.T) {
	const capBytes int64 = 4096
	a := memoryCapAgent(t, capBytes)
	root := a.memory.Source().Location
	result, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: "Project Orion keeps the nightly build on runner 7.",
		Category: "concept", Tier: memory.TierMedium, Importance: 0.5,
	})
	if err != nil {
		t.Fatalf("first write failed: result=%+v err=%v", result, err)
	}
	rel := result.Path
	if rel == "" {
		for _, entry := range a.memory.Recent(8) {
			if entry.Path != "" {
				rel = entry.Path
				break
			}
		}
	}
	if rel == "" {
		rel = findVaultNote(t, root, "70_Concepts")
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	pad := int(capBytes - info.Size() - 64)
	if pad < 0 {
		pad = 0
	}
	if err := os.WriteFile(path, append(mustReadSmall(t, path), []byte(strings.Repeat("x", pad))...), 0o600); err != nil {
		t.Fatal(err)
	}
	seeded, err := os.Stat(path)
	if err != nil || seeded.Size() > capBytes || seeded.Size() < capBytes-128 {
		t.Fatalf("seed path=%s size=%d err=%v rel=%s", path, seeded.Size(), err, rel)
	}
	_, err = a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: "Project Orion keeps the nightly build on runner 7.\n" + strings.Repeat("append ", 80),
		Category: "concept", Tier: memory.TierMedium, Importance: 0.5,
	})
	var rejected *memory.WriteRejectedError
	if !errors.As(err, &rejected) {
		info, _ := os.Stat(path)
		var recent []string
		for _, entry := range a.memory.Recent(8) {
			recent = append(recent, entry.Path+":"+entry.Content[:min(40, len(entry.Content))])
		}
		t.Fatalf("err=%v file=%s size=%d recent=%v", err, path, info.Size(), recent)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() > capBytes {
		t.Fatalf("destination grew to %d", after.Size())
	}
}

func TestRememberRejectsWhenVaultAlreadyHasOversizeNote(t *testing.T) {
	const capBytes int64 = 4096
	a := memoryCapAgent(t, capBytes)
	root := a.memory.Source().Location
	dir := filepath.Join(root, "70_Concepts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bloated := filepath.Join(dir, "Already Huge.md")
	if err := os.WriteFile(bloated, []byte(strings.Repeat("y", 8<<10)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: "An unrelated short fact about the tea preference.",
		Category: "fact", Tier: memory.TierMedium, Importance: 0.5,
	})
	var rejected *memory.WriteRejectedError
	if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "already exceeds") {
		t.Fatalf("err=%v", err)
	}
	info, err := os.Stat(bloated)
	if err != nil || info.Size() != 8<<10 {
		t.Fatalf("bloated note changed: %+v err=%v", info, err)
	}
}

func TestRememberAllowsSmallNoteUnderCap(t *testing.T) {
	a := memoryCapAgent(t, DefaultMaxMemoryNoteBytes)
	_, err := a.WriteMemory(context.Background(), memory.RememberRequest{
		Content: "The user prefers short status notes.",
		Category: "preference", Tier: memory.TierMedium, Importance: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRememberToolUsesCappedStore(t *testing.T) {
	a := memoryCapAgent(t, DefaultMaxMemoryNoteBytes)
	if a.toolServices == nil || a.toolServices.Memory == nil {
		t.Fatal("remember tool was not bound")
	}
	_, err := a.toolServices.Memory.HandleRemember(map[string]any{
		"content":  "fact\n## Memory\n## Links\n## Memory\nstill under the rune cap",
		"category": "fact",
	})
	if err == nil || !strings.Contains(err.Error(), "Memory") {
		t.Fatalf("tool err=%v", err)
	}
}

func memoryCapAgent(t *testing.T, cap int64) *Agent {
	t.Helper()
	mgr, err := config.NewManagerWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := mgr.Get()
	cfg.APIKey, cfg.LlmProvider.APIKey = "test-key", "test-key"
	if err := mgr.Replace(cfg); err != nil {
		t.Fatal(err)
	}
	components, err := bootstrap.Open(mgr, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = components.Close() })
	a, err := NewWithDependencies(Options{}, components)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if cap > 0 && cap != DefaultMaxMemoryNoteBytes {
		a.memory = capMemoryStore(unwrapMemoryStore(a.memory), cap)
		if a.toolServices != nil {
			if backend, ok := a.memory.(memory.ToolBackend); ok {
				a.toolServices.Memory = tool.NewMemoryToolService(backend)
			}
		}
	}
	return a
}

func unwrapMemoryStore(store memory.RuntimeStore) memory.RuntimeStore {
	if wrapped, ok := store.(*cappedMemoryStore); ok {
		return wrapped.RuntimeStore
	}
	return store
}

func vaultMarkdownBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func findVaultNote(t *testing.T, root, dir string) string {
	t.Helper()
	var found string
	_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") || found != "" {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			found = filepath.ToSlash(rel)
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no note under %s", dir)
	}
	return found
}

func mustReadSmall(t *testing.T, path string) []byte {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 64<<10 {
		t.Fatalf("refusing to read %d bytes", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
