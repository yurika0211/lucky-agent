package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yurika0211/luckyagent/internal/provider"
)

func TestArtifactIntentIgnoresPlatformDeliveryGuidance(t *testing.T) {
	text := "你好\n\n[Telegram delivery rule]\nIf you want Telegram to send a file, save it to a real local file first and include MEDIA:/absolute/path/to/file.ext ."
	if hasArtifactIntent(text) {
		t.Fatal("platform delivery guidance alone must not trigger artifact finalization")
	}
}

func TestProcessDirectResponseBlocksArtifactWithoutWrite(t *testing.T) {
	a := &Agent{}
	state := newLoopRuntimeState()
	state.artifactGuard = newArtifactFinalizationGuard("保存为 md 文档")

	messages, finalized, finalResponse := a.processDirectResponse(&provider.Response{
		Content: "已保存到 /tmp/report.md",
	}, nil, state)
	if finalized {
		t.Fatalf("expected artifact guard to block finalization, got final response %q", finalResponse)
	}
	if len(messages) != 2 || messages[1].Role != "user" || !strings.Contains(messages[1].Content, "no successful file_write") {
		t.Fatalf("expected artifact recovery prompt, got %#v", messages)
	}
}

func TestProcessDirectResponseAllowsVerifiedFileWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write temp artifact: %v", err)
	}
	a := &Agent{}
	state := newLoopRuntimeState()
	state.artifactGuard = newArtifactFinalizationGuard("保存为 md 文档")
	state.artifactGuard.recordToolResult("file_write", fmt.Sprintf(`{"path":%q}`, path), fmt.Sprintf("Written 2 bytes to %s (sha256 abc123)", path))

	_, finalized, finalResponse := a.processDirectResponse(&provider.Response{
		Content: "已保存。",
	}, nil, state)
	if !finalized || finalResponse != "已保存。" {
		t.Fatalf("expected verified artifact to finalize, finalized=%t response=%q", finalized, finalResponse)
	}
}

func TestProcessDirectResponseBlocksMissingMediaPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.md")
	a := &Agent{}
	state := newLoopRuntimeState()
	state.artifactGuard = newArtifactFinalizationGuard("保存为 md 文档")
	state.artifactGuard.paths["/tmp/existing.md"] = struct{}{}

	messages, finalized, _ := a.processDirectResponse(&provider.Response{
		Content: "已保存。\nMEDIA:" + path,
	}, nil, state)
	if finalized {
		t.Fatal("expected missing MEDIA path to block finalization")
	}
	if len(messages) != 2 || !strings.Contains(messages[1].Content, "missing media file") {
		t.Fatalf("expected missing media recovery prompt, got %#v", messages)
	}
}

func TestArtifactGuardDetectsMissingWindowsMediaPath(t *testing.T) {
	missing := missingMediaPaths(`完成
MEDIA:C:\Users\Administrator\.luckyagent\workspace\interview\missing.docx`)
	if len(missing) != 1 || missing[0] != `C:\Users\Administrator\.luckyagent\workspace\interview\missing.docx` {
		t.Fatalf("expected missing Windows media path, got %#v", missing)
	}
}
