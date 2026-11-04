package artifact

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordToolResultFileSendJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	g := NewGuard("发给我")
	g.RecordToolResult("file_send", `{"path":"/ignored"}`, fmt.Sprintf(`{"file_name":"report.pdf","file_path":%q,"file_url":"/api/v1/artifacts?path=workspace/deliverables/ab12cd.pdf"}`, path))
	if _, ok := g.Paths[path]; !ok {
		t.Fatalf("file_send path not recorded: %#v", g.Paths)
	}
	if _, blocked := g.BlockMessage("已发送。"); blocked {
		t.Fatal("existing file_send artifact should not block")
	}

	empty := NewGuard("发给我")
	empty.RecordToolResult("file_send", `{}`, `{"file_name":"missing.pdf","file_path":"/tmp/does-not-exist-file-send.pdf"}`)
	if len(empty.Paths) != 0 {
		t.Fatalf("missing file_send path recorded: %#v", empty.Paths)
	}
}
