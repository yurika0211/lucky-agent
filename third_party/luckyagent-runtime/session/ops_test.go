package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateRollExportAndGC(t *testing.T) {
	dir := t.TempDir()
	s := NewSession("ops-1", dir)
	for i := 0; i < 20; i++ {
		s.AddMessage("user", fmt.Sprintf("u-%d", i))
		s.AddMessage("assistant", fmt.Sprintf("a-%d", i))
	}
	big := strings.Repeat("Z", DefaultBlobThreshold+10)
	s.AddToolMessageWithCallID("c1", "file_read", big)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	res, err := s.MigrateToSegment()
	if err != nil {
		t.Fatalf("MigrateToSegment: %v", err)
	}
	if res.ToFormat != FormatSegmentV1 || res.MessageCount < 40 {
		t.Fatalf("unexpected migrate result: %+v", res)
	}

	// Drop the only blob reference then GC should free it after we remove the tool msg via roll of older content.
	roll, err := s.Roll(2)
	if err != nil {
		t.Fatalf("Roll: %v", err)
	}
	if !roll.Rolled || roll.Retained < 3 {
		t.Fatalf("unexpected roll: %+v", roll)
	}
	if _, err := os.Stat(roll.ArchivePath); err != nil {
		t.Fatalf("archive missing: %v", err)
	}

	// Create an orphan blob.
	orphan := filepath.Join(s.blobsDir(), "deadbeef")
	if err := os.WriteFile(orphan, []byte("orphan"), 0600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	gc, err := s.GCBlobs()
	if err != nil {
		t.Fatalf("GCBlobs: %v", err)
	}
	if gc.Removed < 1 {
		t.Fatalf("expected orphan removed, got %+v", gc)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan still present")
	}

	var b strings.Builder
	if err := s.ExportMarkdown(&b, ExportOptions{}); err != nil {
		t.Fatalf("ExportMarkdown: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "### User") || !strings.Contains(out, "Session Export") {
		t.Fatalf("export missing sections: %s", out[:min(200, len(out))])
	}
	if strings.Contains(out, "### Tool") {
		t.Fatalf("export should omit tools by default")
	}
}

func TestNeedsRoll(t *testing.T) {
	s := NewSession("roll-need", t.TempDir())
	s.AddMessage("user", "hi")
	if s.NeedsRoll(0, 0) {
		t.Fatal("disabled limits must not need roll")
	}
	if !s.NeedsRoll(1, 0) {
		t.Fatal("message limit should trigger")
	}
}
