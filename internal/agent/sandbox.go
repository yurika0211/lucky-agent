package agent

import (
	"fmt"
	"os"
	"strings"

	"github.com/yurika0211/luckyagent/internal/sandbox"
	"github.com/yurika0211/luckyagent/internal/session"
)

// SandboxMode returns the persisted global execution mode.
func (a *Agent) SandboxMode() sandbox.Mode {
	if a == nil || a.sandbox == nil {
		return sandbox.ModeDev
	}
	return a.sandbox.Current()
}

func snapshotMode(snapshot *sandbox.Snapshot) sandbox.Mode {
	if snapshot == nil || !snapshot.Mode.Valid() {
		return sandbox.ModeDev
	}
	return snapshot.Mode
}

// SetSandboxMode changes the global mode for new turns and new background
// tasks. Existing tasks keep the snapshot captured when they started.
func (a *Agent) SetSandboxMode(raw string) error {
	if a == nil || a.sandbox == nil {
		return fmt.Errorf("sandbox manager is unavailable")
	}
	mode := sandbox.Mode(strings.ToLower(strings.TrimSpace(raw)))
	if !mode.Valid() {
		return fmt.Errorf("usage: /set dev|iso|status")
	}
	return a.sandbox.Set(mode)
}

func (a *Agent) sandboxSnapshot(sess *session.Session) (*sandbox.Snapshot, error) {
	if a == nil || a.sandbox == nil {
		return sandbox.Prepare(sandbox.ModeDev, "")
	}
	root := ""
	if sess != nil {
		root = strings.TrimSpace(sess.GetCwd())
	}
	if root == "" {
		root, _ = os.Getwd()
	}
	return sandbox.Prepare(a.sandbox.Current(), root)
}

func (a *Agent) applySandboxToolPolicy(cfg *LoopConfig, snapshot *sandbox.Snapshot) {
	if cfg == nil || snapshot == nil || !snapshot.Isolated() {
		return
	}
	// These tools run outside the shell process and therefore must be disabled
	// explicitly in iso mode. Terminal itself is wrapped by bubblewrap.
	disabled := []string{
		"computer_observe", "computer_act",
		"web_search", "web_fetch", "http_request", "opencli",
		"grok.start_session", "grok.resume_session", "grok.start_turn",
		"grok.subscribe_events", "grok.respond_approval", "grok.get_turn_summary",
		"cron", "cron_add", "cron_remove", "cron_pause", "cron_resume",
		"autonomy", "autonomy_queue_add", "autonomy_queue_update", "autonomy_worker_spawn",
		"delegate_task", "delegate_cancel",
	}
	if a.tools != nil {
		for _, item := range a.tools.ListEnabled() {
			if item == nil {
				continue
			}
			name := item.Name
			if strings.HasPrefix(name, "skill_") && strings.HasSuffix(name, "_run") {
				disabled = append(disabled, name)
			}
		}
	}
	cfg.DisabledTools = normalizeToolNameList(append(cfg.DisabledTools, disabled...))
}
