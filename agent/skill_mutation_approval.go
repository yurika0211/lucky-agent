package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
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
		candidateRaw := strings.TrimSpace(raw)
		if !filepath.IsAbs(candidateRaw) {
			if cwd, ok := args["_cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
				candidateRaw = filepath.Join(cwd, candidateRaw)
			}
		}
		candidate, err := filepath.Abs(filepath.Clean(candidateRaw))
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
			reason := skillMutationReview(skill.Name, candidate, name, args)
			return key, reason
		}
	}
	return "", ""
}

// skillMutationReview is the text shown before a user approves an edit to an
// installed skill. It names the file, the observed fault when one was passed,
// and the smallest diff the tool arguments already contain.
func skillMutationReview(skillName, path, toolName string, args map[string]any) string {
	fault := firstSkillArg(args, "error", "fault", "observed_error")
	cause := firstSkillArg(args, "cause", "reason")
	match := firstSkillArg(args, "match", "old", "old_string")
	replace := firstSkillArg(args, "replace", "new", "new_string", "content")
	if fault == "" {
		fault = "未附带已观察到的故障原文"
	}
	if cause == "" {
		cause = "原因尚未证实，按调用方给出的假设处理"
	}
	diff := "未给出最小差异"
	if match != "" || replace != "" {
		diff = fmt.Sprintf("-%s\n+%s", redactSkillReview(match), redactSkillReview(replace))
	}
	return fmt.Sprintf("已安装 skill %q 的修改需要批准。\n位置：%s（%s）\n故障：%s\n原因：%s\n最小差异：\n%s\n本次批准只覆盖这一处。外部失败不会因此改写技能文件。", skillName, path, toolName, redactSkillReview(fault), redactSkillReview(cause), diff)
}

func firstSkillArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := args[key].(string)
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

var skillReviewSecret = regexp.MustCompile(`(?i)(sk-[a-z0-9][a-z0-9_-]{6,}|bearer\s+[a-z0-9._~+/-]+=*|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

func redactSkillReview(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	text = skillReviewSecret.ReplaceAllString(text, "[redacted]")
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
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
