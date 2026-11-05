package memoryaestus_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yurika0211/luckyagent-runtime/memoryaestus"
	"github.com/yurika0211/luckyagent-tools/memory"
)

func TestRuntimeSummaryPortPreservesIdentityAndCancellation(t *testing.T) {
	home := t.TempDir()
	runtime, err := memoryaestus.Open(memoryaestus.Options{HomeDir: home, Tidal: memoryaestus.TidalOptions{Enabled: true, StorePath: "telemetry/custom.db"}})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Store.Close()
	if _, err := os.Stat(filepath.Join(home, "telemetry", "custom.db")); err != nil {
		t.Fatalf("configured telemetry database missing: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := memory.SummaryRequest{SessionID: "session-a", UserID: "user-a", Title: "Orion", Content: "A compact conversation summary."}
	if err := runtime.Summaries.SaveSummary(ctx, r); err != context.Canceled {
		t.Fatalf("cancel=%v", err)
	}
	if runtime.Summaries.Count() != 0 {
		t.Fatal("canceled summary persisted")
	}
	if err := runtime.Summaries.SaveSummary(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	summary, ok := runtime.Summaries.Get(r.SessionID)
	if !ok || summary.UserID != r.UserID || summary.RawSummary != r.Content || len(summary.Topics) == 0 || summary.Topics[0] != r.Title {
		t.Fatalf("summary=%+v", summary)
	}
	if _, enabled, err := runtime.Store.TidalStats(); err != nil || !enabled {
		t.Fatalf("diagnostics enabled=%v err=%v", enabled, err)
	}
}
