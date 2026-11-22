package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// installedSkillMutation returns a stable approval key and a redacted prompt
// whenever a file tool targets a skill that is already installed. Reading and
// executing a skill never reaches this gate; only a write-like operation does.
func (a *Agent) installedSkillMutation(name string, args map[string]any) (string, string) {
	if a == nil || args == nil {
		return "", ""
	}
	name = strings.ToLower(strings.TrimSpace(a.canonicalToolName(name)))
	switch name {
	case "file_write", "file_patch", "file_move", "file_delete", "file_mkdir":
	default:
		return "", ""
	}
	keys := []string{"path", "src", "dst"}
	for _, key := range keys {
		raw, ok := args[key].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		candidate, err := filepath.Abs(filepath.Clean(strings.TrimSpace(raw)))
		if err != nil {
			continue
		}
		for _, skill := range a.snapshotSkills() {
			if skill == nil || strings.TrimSpace(skill.Dir) == "" {
				continue
			}
			root, err := filepath.Abs(filepath.Clean(skill.Dir))
			if err != nil || !pathWithin(root, candidate) {
				continue
			}
			payload, _ := json.Marshal(map[string]any{"tool": name, "args": args, "skill": skill.Name})
			digest := sha256.Sum256(payload)
			key := fmt.Sprintf("%x", digest[:])
			reason := fmt.Sprintf("已安装 skill %q 的文件将被修改（%s）。请先说明已观察到的故障、原因证据、最小修复内容和影响范围；本次批准只覆盖展示的文件与改动。", skill.Name, candidate)
			return key, reason
		}
	}
	return "", ""
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == "." || rel == ".." {
		return rel == "."
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (a *Agent) skillMutationApprovalSeen(key string) bool {
	if a == nil || key == "" {
		return false
	}
	a.skillMutationApprovalMu.Lock()
	defer a.skillMutationApprovalMu.Unlock()
	if a.skillMutationApprovals == nil {
		return false
	}
	approvedAt, ok := a.skillMutationApprovals[key]
	return ok && time.Since(approvedAt) < 15*time.Minute
}

func (a *Agent) rememberSkillMutationApproval(key string) {
	if a == nil || key == "" {
		return
	}
	a.skillMutationApprovalMu.Lock()
	defer a.skillMutationApprovalMu.Unlock()
	if a.skillMutationApprovals == nil {
		a.skillMutationApprovals = make(map[string]time.Time)
	}
	a.skillMutationApprovals[key] = time.Now()
}
