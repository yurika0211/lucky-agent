package agent

import (
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/sandbox"
)

func TestRestrictedSandboxModesUseTheSessionWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, mode := range []sandbox.Mode{sandbox.ModeWorkspaceWrite} {
		snapshot, err := sandbox.Prepare(mode, root)
		if err != nil {
			t.Fatal(err)
		}
		if !snapshot.Restricted() || snapshot.Root != root || snapshot.OriginalRoot != root {
			t.Fatalf("mode %q did not preserve the workspace: %+v", mode, snapshot)
		}
		snapshot.Close()
	}
}

func TestSandboxModePromptNamesHostShellForDev(t *testing.T) {
	dev := sandboxModePrompt(sandbox.ModeDev)
	if !strings.Contains(dev, "host sh -c") || !strings.Contains(dev, "No OS jail") || !strings.Contains(dev, "not a sandbox") {
		t.Fatalf("dev prompt does not name the host shell: %s", dev)
	}
	restricted := sandboxModePrompt(sandbox.ModeWorkspaceWrite)
	if !strings.Contains(restricted, "OS jail") || strings.Contains(restricted, "host sh -c") {
		t.Fatalf("workspace-write prompt lost the boundary: %s", restricted)
	}
	prompt := (&Agent{}).buildSystemPromptWithOptions(nil, systemPromptOptions{OmitProjectContext: true})
	if !strings.Contains(prompt, "Execution boundary: dev") {
		t.Fatal("standing prompt omitted the default execution boundary")
	}
}
