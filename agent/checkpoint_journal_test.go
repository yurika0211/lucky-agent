package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
	"github.com/yurika0211/luckyagent-runtime/tool"
)

// checkpointSaveStats is one measured save: bytes handed to the store and
// time spent producing them. The store holds its queue lock for the write
// of those bytes, so the byte count is the lock-hold cost of the checkpoint.
type checkpointSaveStats struct {
	Bytes    int
	EncodeNS int64
}

// measureCheckpointSaves replays one long task under a saver. Each step
// appends a tool result the way executeDurablePending does, then saves.
func measureCheckpointSaves(steps int, body string, save func(*durableCheckpoint) ([]byte, error)) (checkpointTrajectoryMeasurement, error) {
	cp := durableCheckpoint{Version: 1, Messages: []provider.Message{{Role: "user", Content: "long task"}}, LastResultDigests: map[string]string{}}
	var measured checkpointTrajectoryMeasurement
	for i := 0; i < steps; i++ {
		id := fmt.Sprintf("operation-%d", i)
		cp.Messages = append(cp.Messages, provider.Message{Role: "tool", ToolCallID: id, Content: body})
		cp.Batch = i + 1
		cp.Tokens += 10
		cp.LastResultDigests[id] = durableResultDigest(body)
		start := time.Now()
		data, err := save(&cp)
		if err != nil {
			return measured, err
		}
		measured.Saves = append(measured.Saves, checkpointSaveStats{Bytes: len(data), EncodeNS: time.Since(start).Nanoseconds()})
		measured.TotalBytes += len(data)
		if len(data) > measured.MaxBytes {
			measured.MaxBytes = len(data)
		}
	}
	measured.Steps = steps
	return measured, nil
}

// checkpointTrajectoryMeasurement is the before/after record the issue asks
// for: size, save count, total bytes written, and per-save encode time.
type checkpointTrajectoryMeasurement struct {
	Steps      int
	Saves      []checkpointSaveStats
	TotalBytes int
	MaxBytes   int
}

func (m checkpointTrajectoryMeasurement) encodeP95() time.Duration {
	if len(m.Saves) == 0 {
		return 0
	}
	times := make([]int64, len(m.Saves))
	for i, save := range m.Saves {
		times[i] = save.EncodeNS
	}
	for i := 1; i < len(times); i++ {
		for j := i; j > 0 && times[j] < times[j-1]; j-- {
			times[j], times[j-1] = times[j-1], times[j]
		}
	}
	return time.Duration(times[(len(times)-1)*95/100])
}

func TestDurableLegacyCheckpointResumesUnchanged(t *testing.T) {
	legacy := pendingCheckpoint()
	a, sess, _, _, cfg := durableHarness(t, legacy)
	task, err := cfg.Execution.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(task.Checkpoint, []byte(`"format"`)) {
		t.Fatal("harness stored a version 2 anchor for a version 1 fixture")
	}
	calls := 0
	a.tools.Register(&tool.Tool{Name: "perform_operation", Permission: tool.PermAuto, Handler: func(map[string]any) (string, error) {
		calls++
		return "effect", nil
	}})
	result, err := a.RunLoopWithSession(context.Background(), sess, "perform requested operation", cfg)
	if err != nil || !result.Verified || calls != 1 {
		t.Fatalf("version 1 checkpoint did not resume: calls=%d result=%+v err=%v", calls, result, err)
	}
	saved, err := cfg.Execution.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := loadDurableCheckpoint(saved.Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Batch < 1 || len(restored.Messages) < len(legacy.Messages) {
		t.Fatalf("resumed checkpoint lost the legacy transcript: %+v", restored)
	}
}

func TestCheckpointJournalPreservesLegacyV1(t *testing.T) {
	original := durableCheckpoint{
		Version: 1, Batch: 2, Tokens: 40, Candidate: "done",
		Messages:          []provider.Message{{Role: "user", Content: "legacy task"}, {Role: "assistant", Content: "done"}},
		LastResultDigests: map[string]string{"sig": "abc"},
		Shell:             &session.ShellContext{Cwd: "/work"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDurableCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	if !sameCheckpoint(&original, &loaded) {
		t.Fatalf("version 1 checkpoint changed on load: %+v", loaded)
	}
}

func TestCheckpointJournalReplaysAppendAndTruncation(t *testing.T) {
	writer := testJournalWriter(t)
	cp := durableCheckpoint{Version: 1, Messages: []provider.Message{{Role: "user", Content: "task"}}}
	var persisted []byte
	for i := 0; i < 5; i++ {
		cp.Messages = append(cp.Messages, provider.Message{Role: "tool", ToolCallID: fmt.Sprintf("c%d", i), Content: strings.Repeat("x", 50)})
		cp.Batch = i + 1
		cp.Pending = []durablePendingCall{{ID: fmt.Sprintf("step-%d", i), Call: provider.ToolCall{ID: "call", Name: "read", Arguments: "{}"}}}
		data, err := writer.encode(&cp)
		if err != nil {
			t.Fatal(err)
		}
		persisted = data
		cp.Pending = nil
		data, err = writer.encode(&cp)
		if err != nil {
			t.Fatal(err)
		}
		persisted = data
	}
	restored, err := loadDurableCheckpoint(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if !sameCheckpoint(&cp, &restored) {
		t.Fatalf("replay drifted: got batch %d messages %d", restored.Batch, len(restored.Messages))
	}

	// Cut the sidecar inside its last line. The anchor and earlier lines load.
	log, err := os.ReadFile(writer.log)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the last message event in half and keep every earlier line.
	marker := []byte(`"kind":"messages"`)
	at := bytes.LastIndex(log, marker)
	if at < 0 {
		t.Fatal("log has no message event")
	}
	line := bytes.LastIndex(log[:at], []byte("\n")) + 1
	partial := append(append([]byte{}, log[:line]...), log[line:line+len(marker)]...)
	if err := os.WriteFile(writer.log, partial, 0o600); err != nil {
		t.Fatal(err)
	}
	truncated, err := loadDurableCheckpoint(persisted)
	if err != nil {
		t.Fatalf("truncated journal did not load: %v", err)
	}
	if truncated.Version != 1 || len(truncated.Messages) == 0 || len(truncated.Messages) >= len(cp.Messages) {
		t.Fatalf("truncated journal restored an impossible transcript: %d messages, full %d", len(truncated.Messages), len(cp.Messages))
	}
}

func TestCheckpointJournalResetSurvivesCompaction(t *testing.T) {
	writer := testJournalWriter(t)
	cp := durableCheckpoint{Version: 1, Messages: []provider.Message{{Role: "user", Content: "task"}}}
	for i := 0; i < 3; i++ {
		cp.Messages = append(cp.Messages, provider.Message{Role: "assistant", Content: fmt.Sprintf("step %d", i)})
		if _, err := writer.encode(&cp); err != nil {
			t.Fatal(err)
		}
	}
	// Compaction rewrites the tail the way appendLatestComputerObservation does.
	cp.Messages = []provider.Message{{Role: "user", Content: "task"}, {Role: "system", Content: "summary"}}
	cp.Summary, cp.SummarizedThrough = "summary", 1
	data, err := writer.encode(&cp)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := loadDurableCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	if !sameCheckpoint(&cp, &restored) {
		t.Fatalf("compaction reset did not replay: %+v", restored)
	}
}

func TestCheckpointWriteAmplificationDrops(t *testing.T) {
	const steps = 100
	body := strings.Repeat("output-", 146)
	before, err := measureCheckpointSaves(steps, body, func(cp *durableCheckpoint) ([]byte, error) {
		return json.Marshal(cp)
	})
	if err != nil {
		t.Fatal(err)
	}
	writer := testJournalWriter(t)
	var meter journalWriteMeter
	after, err := measureCheckpointSaves(steps, body, func(cp *durableCheckpoint) ([]byte, error) {
		anchor, err := writer.encode(cp)
		if err != nil {
			return nil, err
		}
		// Bytes written this step are the new anchor, when it changed, plus
		// the sidecar lines appended after the previous save.
		return meter.added(anchor, &writer), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The journal must round-trip the same checkpoint the old encoder wrote.
	legacy, _ := json.Marshal(lastMeasuredCheckpoint(steps, body))
	replayed, err := loadDurableCheckpoint(lastJournalBytes(steps, body))
	if err != nil {
		t.Fatal(err)
	}
	want := lastMeasuredCheckpoint(steps, body)
	if !sameCheckpoint(&want, &replayed) {
		t.Fatal("journal replay does not match the full-rewrite checkpoint")
	}
	if len(legacy) == 0 {
		t.Fatal("legacy encoding empty")
	}
	t.Logf("checkpoint write amplification, %d steps, %d-byte tool result", steps, len(body))
	t.Logf("full rewrite: saves=%d total=%d bytes max=%d p95=%s", before.Steps, before.TotalBytes, before.MaxBytes, before.encodeP95())
	t.Logf("journal:      saves=%d total=%d bytes max=%d p95=%s", after.Steps, after.TotalBytes, after.MaxBytes, after.encodeP95())
	if after.TotalBytes*2 > before.TotalBytes {
		t.Fatalf("journal did not cut total bytes written: before=%d after=%d", before.TotalBytes, after.TotalBytes)
	}
}

func lastMeasuredCheckpoint(steps int, body string) durableCheckpoint {
	cp := durableCheckpoint{Version: 1, Messages: []provider.Message{{Role: "user", Content: "long task"}}, LastResultDigests: map[string]string{}}
	for i := 0; i < steps; i++ {
		id := fmt.Sprintf("operation-%d", i)
		cp.Messages = append(cp.Messages, provider.Message{Role: "tool", ToolCallID: id, Content: body})
		cp.Batch = i + 1
		cp.Tokens += 10
		cp.LastResultDigests[id] = durableResultDigest(body)
	}
	return cp
}

func lastJournalBytes(steps int, body string) []byte {
	var writer checkpointWriter
	measured, err := measureCheckpointSaves(steps, body, func(cp *durableCheckpoint) ([]byte, error) {
		return writer.encode(cp)
	})
	if err != nil || len(measured.Saves) == 0 {
		return nil
	}
	// Re-encode the final state; measureCheckpointSaves does not keep the bytes.
	cp := lastMeasuredCheckpoint(steps, body)
	var again checkpointWriter
	var last []byte
	for i := 1; i <= steps; i++ {
		partial := cp
		partial.Messages = append([]provider.Message(nil), cp.Messages[:i+1]...)
		partial.Batch = i
		partial.Tokens = i * 10
		partial.LastResultDigests = map[string]string{}
		for j := 0; j < i; j++ {
			id := fmt.Sprintf("operation-%d", j)
			partial.LastResultDigests[id] = cp.LastResultDigests[id]
		}
		last, err = again.encode(&partial)
		if err != nil {
			return nil
		}
	}
	return last
}

func testJournalWriter(t *testing.T) checkpointWriter {
	t.Helper()
	return checkpointWriter{dir: t.TempDir()}
}

// journalWriteMeter sums bytes a save added: a changed anchor, plus the
// growth of the sidecar since the previous call.
type journalWriteMeter struct {
	accounted int
	anchor    []byte
	log       string
}

func (m *journalWriteMeter) added(anchor []byte, w *checkpointWriter) []byte {
	if m.log != w.log {
		m.accounted = 0
		m.log = w.log
	}
	info, err := os.Stat(w.log)
	size := 0
	if err == nil {
		size = int(info.Size())
	}
	written := size - m.accounted
	m.accounted = size
	if !bytes.Equal(anchor, m.anchor) {
		written += len(anchor)
		m.anchor = append([]byte(nil), anchor...)
	}
	if written < 0 {
		written = 0
	}
	return bytes.Repeat([]byte{1}, written)
}

func TestCheckpointJournalSnapshotBoundsTheTail(t *testing.T) {
	writer := testJournalWriter(t)
	cp := durableCheckpoint{Version: 1, Messages: []provider.Message{{Role: "user", Content: "task"}}}
	var previousLog string
	for i := 0; i < checkpointSnapshotEvery+2; i++ {
		cp.Messages = append(cp.Messages, provider.Message{Role: "tool", Content: "row"})
		cp.Batch = i + 1
		if _, err := writer.encode(&cp); err != nil {
			t.Fatal(err)
		}
		if i == checkpointSnapshotEvery+1 {
			// The periodic snapshot starts a new empty sidecar.
			if writer.log == previousLog {
				t.Fatal("snapshot reused the event log")
			}
			info, err := os.Stat(writer.log)
			if err != nil {
				t.Fatal(err)
			}
			if info.Size() != 0 {
				t.Fatalf("snapshot left %d event bytes in the new log", info.Size())
			}
		}
		previousLog = writer.log
	}
	restored, err := loadDurableCheckpoint(writer.anchor)
	if err != nil {
		t.Fatal(err)
	}
	if !sameCheckpoint(&cp, &restored) {
		t.Fatal("state after a periodic snapshot did not replay")
	}
}

func TestRecoverTruncatedJournalDropsOnlyTheTail(t *testing.T) {
	writer := testJournalWriter(t)
	cp := durableCheckpoint{Version: 1, Messages: []provider.Message{{Role: "user", Content: "task"}}}
	data, err := writer.encode(&cp)
	if err != nil {
		t.Fatal(err)
	}
	cp.Messages = append(cp.Messages, provider.Message{Role: "assistant", Content: "first"})
	data, err = writer.encode(&cp)
	if err != nil {
		t.Fatal(err)
	}
	complete := append([]byte(nil), data...)
	cp.Messages = append(cp.Messages, provider.Message{Role: "assistant", Content: strings.Repeat("tail", 40)})
	if _, err = writer.encode(&cp); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(writer.log)
	if err != nil {
		t.Fatal(err)
	}
	cutAt := bytes.LastIndex(log, []byte(`"first"`))
	if cutAt < 0 {
		t.Fatal("fixture lost the first event")
	}
	if err := os.WriteFile(writer.log, log[:cutAt+len(`"first"`)+2], 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadDurableCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	full, err := loadDurableCheckpoint(complete)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != len(full.Messages) {
		t.Fatalf("truncated tail changed the committed prefix: loaded %d committed %d", len(loaded.Messages), len(full.Messages))
	}
}
