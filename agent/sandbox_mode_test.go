package agent

import (
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
