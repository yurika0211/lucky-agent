package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yurika0211/luckyagent-runtime/provider"
	"github.com/yurika0211/luckyagent-runtime/session"
)

// checkpointFormatVersion is the on-disk layout written by new saves.
// A version 1 checkpoint is still a bare durableCheckpoint and stays readable.
const checkpointFormatVersion = 2

// checkpointSnapshotEvery bounds how many events follow the last full
// snapshot. Crossing the bound rewrites the snapshot once and clears the
// log. Every other save appends a single event.
const checkpointSnapshotEvery = 32

// checkpointJournal is the version 2 snapshot stored in the task record.
// Events after the snapshot live in a sidecar file named by Log, one JSON
// object per line. The task record therefore stays the size of the last
// snapshot instead of growing with every save.
type checkpointJournal struct {
	Format   int               `json:"format"`
	Snapshot durableCheckpoint `json:"snapshot"`
	Log      string            `json:"log,omitempty"`
	// Since counts saves applied after Snapshot. It decides when the next
	// save rewrites the snapshot. It is recovered from the sidecar, so the
	// task record stays byte-stable between snapshots.
	Since int `json:"-"`
	// Events is the in-memory tail. It is not written into the task record.
	Events []checkpointEvent `json:"-"`
	// Dropped counts events discarded while reading a truncated tail.
	Dropped int `json:"-"`
}

// checkpointEvent is one persisted transition. Message and pending events
// carry only the records appended since the previous save.
type checkpointEvent struct {
	Kind string `json:"kind"`
	// Since is the save count after the snapshot, written on every event so
	// recovery can place the next snapshot without rewriting the anchor.
	Since int `json:"since,omitempty"`
	// Messages appended after the shared prefix. Kind "messages".
	Added []provider.Message `json:"added,omitempty"`
	// Pending calls appended. Kind "pending".
	Pending []durablePendingCall `json:"pending,omitempty"`
	// How many leading pending calls were consumed. Kind "pending_drop".
	Drop int `json:"drop,omitempty"`
	// Messages and pending calls replaced outright. Kind "reset". Used when
	// the transcript is rewritten rather than extended, which compaction and
	// computer-frame replacement both do.
	Messages []provider.Message   `json:"messages,omitempty"`
	Calls    []durablePendingCall `json:"calls,omitempty"`
	// Scalar and small-map state. Kind "state". Messages and Pending are
	// cleared because those fields move only through their own events.
	// Only fields that differ from the baseline are set.
	State   *durableCheckpoint `json:"state,omitempty"`
	Changed []string           `json:"changed,omitempty"`
}

const (
	checkpointEventMessages    = "messages"
	checkpointEventPending     = "pending"
	checkpointEventPendingDrop = "pending_drop"
	checkpointEventReset       = "reset"
	checkpointEventState       = "state"
)

// checkpointWriter encodes one durableCheckpoint as a snapshot plus a short
// event tail. Events are appended to a sidecar file; the bytes returned to
// the task store are the snapshot anchor and change only on a snapshot.
type checkpointWriter struct {
	dir      string
	log      string
	snapshot durableCheckpoint
	events   []checkpointEvent
	steps    int
	// baseline is the checkpoint the last successful encode represented.
	baseline durableCheckpoint
	ready    bool
	// anchor is the last snapshot record returned to the task store.
	anchor []byte
}

// loadDurableCheckpoint reads a version 1 checkpoint or a version 2 journal.
// A journal whose final sidecar line was cut off mid-write still yields the
// state reconstructed from the snapshot and the complete lines before the cut.
func loadDurableCheckpoint(data []byte) (durableCheckpoint, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return durableCheckpoint{Version: 1}, nil
	}
	if !bytes.Contains(data[:min(len(data), 96)], []byte(`"format"`)) {
		return loadVersion1Checkpoint(data)
	}
	journal, err := decodeCheckpointJournal(data)
	if err != nil {
		// A store that only ever wrote version 1 never has a format field
		// past the first object key, so a decode failure here is a broken
		// version 2 record, not a legacy checkpoint.
		return durableCheckpoint{}, err
	}
	if journal.Format != checkpointFormatVersion {
		return durableCheckpoint{}, errUnsupportedCheckpoint
	}
	return replayCheckpointJournal(journal)
}

// errUnsupportedCheckpoint is returned for a journal this process cannot
// replay. Callers map it onto the existing blocked-checkpoint error.
var errUnsupportedCheckpoint = errCheckpointRejected("unsupported checkpoint format")

type errCheckpointRejected string

func (e errCheckpointRejected) Error() string { return string(e) }

func loadVersion1Checkpoint(data []byte) (durableCheckpoint, error) {
	var cp durableCheckpoint
	if err := json.Unmarshal(data, &cp); err != nil || cp.Version != 1 || len(cp.Messages) == 0 {
		return durableCheckpoint{}, errCheckpointRejected("unreadable or unsupported task checkpoint")
	}
	return cp, nil
}

// decodeCheckpointJournal reads the snapshot anchor and, when it names a
// sidecar, the events appended after that snapshot. A partial last line is
// dropped. Events carried inline (tests and older encoders) are kept too.
func decodeCheckpointJournal(data []byte) (checkpointJournal, error) {
	var journal checkpointJournal
	if err := json.Unmarshal(data, &journal); err != nil || journal.Format != checkpointFormatVersion {
		return checkpointJournal{}, errCheckpointRejected("unreadable or unsupported task checkpoint")
	}
	if journal.Log == "" {
		return journal, nil
	}
	events, dropped, err := readCheckpointLog(journal.Log)
	if err != nil {
		return checkpointJournal{}, err
	}
	journal.Events = append(journal.Events, events...)
	journal.Dropped = dropped
	return journal, nil
}

// readCheckpointLog reads one JSON event per line. The last line may be a
// partial write; it is counted as dropped and the earlier lines still apply.
func readCheckpointLog(path string) ([]checkpointEvent, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	var events []checkpointEvent
	dropped := 0
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte("\n"))
		data = rest
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event checkpointEvent
		if err := json.Unmarshal(line, &event); err != nil || event.Kind == "" {
			dropped++
			// A partial line is always the tail of the file.
			break
		}
		events = append(events, event)
	}
	return events, dropped, nil
}

// firstJSONValue returns the first complete JSON value in data. ok is false
// when the value runs off the end of data.
func firstJSONValue(data []byte) (value, rest []byte, ok bool) {
	data = bytes.TrimLeft(data, " \t\r\n")
	if len(data) == 0 {
		return nil, nil, false
	}
	switch data[0] {
	case '{', '[':
		depth, inString, escaped := 0, false, false
		for i, b := range data {
			if inString {
				if escaped {
					escaped = false
					continue
				}
				if b == '\\' {
					escaped = true
					continue
				}
				if b == '"' {
					inString = false
				}
				continue
			}
			switch b {
			case '"':
				inString = true
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return data[:i+1], data[i+1:], true
				}
			}
		}
		return nil, nil, false
	default:
		return nil, nil, false
	}
}

func replayCheckpointJournal(journal checkpointJournal) (durableCheckpoint, error) {
	cp := journal.Snapshot
	if cp.Version != 1 || len(cp.Messages) == 0 {
		return durableCheckpoint{}, errCheckpointRejected("unreadable or unsupported task checkpoint")
	}
	for _, event := range journal.Events {
		if err := applyCheckpointEvent(&cp, event); err != nil {
			return durableCheckpoint{}, err
		}
	}
	return cp, nil
}

func applyCheckpointEvent(cp *durableCheckpoint, event checkpointEvent) error {
	switch event.Kind {
	case checkpointEventMessages:
		cp.Messages = append(cp.Messages, event.Added...)
	case checkpointEventPending:
		cp.Pending = append(cp.Pending, event.Pending...)
	case checkpointEventPendingDrop:
		if event.Drop < 0 || event.Drop > len(cp.Pending) {
			return errCheckpointRejected("checkpoint event drops more pending calls than recorded")
		}
		cp.Pending = append([]durablePendingCall(nil), cp.Pending[event.Drop:]...)
	case checkpointEventReset:
		cp.Messages = append([]provider.Message(nil), event.Messages...)
		cp.Pending = append([]durablePendingCall(nil), event.Calls...)
	case checkpointEventState:
		if event.State == nil {
			return errCheckpointRejected("checkpoint state event is empty")
		}
		applyCheckpointState(cp, event.State, event.Changed)
	default:
		return errCheckpointRejected("unknown checkpoint event")
	}
	return nil
}

// applyCheckpointState copies the fields named in changed. An empty list is
// the older form that replaced every scalar field. Messages and Pending stay
// with their own events.
func applyCheckpointState(cp *durableCheckpoint, state *durableCheckpoint, changed []string) {
	if len(changed) == 0 {
		messages, pending := cp.Messages, cp.Pending
		*cp = *state
		cp.Messages = messages
		cp.Pending = pending
		cp.Version = 1
		return
	}
	for _, field := range changed {
		copyCheckpointField(cp, state, field)
	}
}

func copyCheckpointField(dst, src *durableCheckpoint, field string) {
	switch field {
	case "observe_only_batches":
		dst.ObserveOnlyBatches = src.ObserveOnlyBatches
	case "memory_synthesis":
		dst.MemorySynthesis = src.MemorySynthesis
	case "batch":
		dst.Batch = src.Batch
	case "tokens":
		dst.Tokens = src.Tokens
	case "candidate":
		dst.Candidate = src.Candidate
	case "reasoning":
		dst.Reasoning = src.Reasoning
	case "verification":
		dst.Verification = cloneVerification(src.Verification)
	case "last_results":
		dst.LastResults = cloneStringMap(src.LastResults)
	case "last_result_digests":
		dst.LastResultDigests = cloneStringMap(src.LastResultDigests)
	case "repeat_signatures":
		dst.RepeatSignatures = append([]string(nil), src.RepeatSignatures...)
	case "no_progress":
		dst.NoProgress = src.NoProgress
	case "model_stalls":
		dst.ModelStalls = src.ModelStalls
	case "continuation":
		dst.Continuation = src.Continuation
	case "summary":
		dst.Summary = src.Summary
	case "summarized_through":
		dst.SummarizedThrough = src.SummarizedThrough
	case "shell":
		dst.Shell = cloneShell(src.Shell)
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneVerification(in *durableVerification) *durableVerification {
	if in == nil {
		return nil
	}
	out := *in
	out.Evidence = append([]string(nil), in.Evidence...)
	out.Criteria = append([]criterionAssessment(nil), in.Criteria...)
	for i := range out.Criteria {
		out.Criteria[i].OperationIDs = append([]string(nil), in.Criteria[i].OperationIDs...)
	}
	out.Failures = append([]failureAssessment(nil), in.Failures...)
	for i := range out.Failures {
		out.Failures[i].OperationIDs = append([]string(nil), in.Failures[i].OperationIDs...)
	}
	return &out
}

func cloneShell(shell *session.ShellContext) *session.ShellContext {
	if shell == nil {
		return nil
	}
	out := *shell
	if shell.Env != nil {
		out.Env = make(map[string]string, len(shell.Env))
		for k, v := range shell.Env {
			out.Env[k] = v
		}
	}
	return &out
}

// adopt continues a journal that was just loaded, so the next save appends
// to its sidecar. A version 1 checkpoint, a truncated log, or a journal that
// does not replay to cp starts a fresh snapshot on the next encode.
func (w *checkpointWriter) adopt(data []byte, cp *durableCheckpoint) {
	raw := bytes.TrimSpace(data)
	journal, err := decodeCheckpointJournal(raw)
	if err != nil || journal.Dropped > 0 || journal.Format != checkpointFormatVersion {
		return
	}
	replayed, err := replayCheckpointJournal(journal)
	if err != nil || !sameCheckpoint(&replayed, cp) {
		return
	}
	w.snapshot = journal.Snapshot
	w.events = journal.Events
	w.log = journal.Log
	w.steps = journalSince(journal.Events)
	w.baseline = cloneCheckpoint(cp)
	w.anchor = append([]byte(nil), raw...)
	w.ready = true
}

func sameCheckpoint(a, b *durableCheckpoint) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

// encode records cp and returns the bytes to persist. The first call, and
// every call that crosses checkpointSnapshotEvery, writes a full snapshot.
// Calls in between append the delta of this checkpoint onto the log.
func (w *checkpointWriter) encode(cp *durableCheckpoint) ([]byte, error) {
	if !w.ready || w.steps >= checkpointSnapshotEvery || !sameCheckpointPrefix(&w.baseline, cp) {
		if err := w.writeSnapshot(cp); err != nil {
			return nil, err
		}
		return append([]byte(nil), w.anchor...), nil
	}
	events := diffCheckpoint(&w.baseline, cp)
	w.steps++
	for i := range events {
		events[i].Since = w.steps
	}
	if err := w.appendEvents(events); err != nil {
		w.steps--
		return nil, err
	}
	w.events = append(w.events, events...)
	w.baseline = cloneCheckpoint(cp)
	return append([]byte(nil), w.anchor...), nil
}

func journalSince(events []checkpointEvent) int {
	since := 0
	for _, event := range events {
		if event.Since > since {
			since = event.Since
		}
	}
	return since
}

// writeSnapshot replaces the sidecar with an empty log and returns a new
// anchor. The previous log is removed so recovery cannot replay stale events
// on top of the new snapshot.
func (w *checkpointWriter) writeSnapshot(cp *durableCheckpoint) error {
	if w.dir == "" {
		w.dir = filepath.Join(os.TempDir(), "luckyagent-checkpoint-logs")
	}
	if err := os.MkdirAll(w.dir, 0o700); err != nil {
		return err
	}
	// A new generation gets a new file so a crash between the anchor write and
	// the log truncation cannot pair a new snapshot with the previous events.
	// The name is unique per snapshot because every task shares this directory.
	name, err := w.createLog()
	if err != nil {
		return err
	}
	anchor, err := json.Marshal(checkpointJournal{Format: checkpointFormatVersion, Snapshot: cloneCheckpoint(cp), Log: name})
	if err != nil {
		return err
	}
	if w.log != "" && w.log != name {
		_ = os.Remove(w.log)
	}
	w.log = name
	w.anchor = anchor
	w.snapshot = cloneCheckpoint(cp)
	w.events = nil
	w.steps = 0
	w.baseline = cloneCheckpoint(cp)
	w.ready = true
	return nil
}

func (w *checkpointWriter) appendEvents(events []checkpointEvent) error {
	if len(events) == 0 {
		return nil
	}
	if w.log == "" {
		return fmt.Errorf("checkpoint journal has no log")
	}
	f, err := os.OpenFile(w.log, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			return err
		}
		line = append(line, '\n')
		if _, err := f.Write(line); err != nil {
			return err
		}
	}
	return f.Sync()
}

// createLog makes an empty sidecar with a name no other snapshot uses.
func (w *checkpointWriter) createLog() (string, error) {
	for i := 0; i < 100; i++ {
		name := filepath.Join(w.dir, fmt.Sprintf("checkpoint-%d.log", time.Now().UnixNano()))
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", fmt.Errorf("checkpoint log name collision")
}

// checkpointJournalDir keeps sidecars under the runtime home, beside the
// queue files recovery already reloads. The anchor stores the log path.
// LoopConfig.CheckpointLogDir overrides it for a task whose queue lives elsewhere.
func (a *Agent) checkpointJournalDir(cfg LoopConfig) string {
	if dir := strings.TrimSpace(cfg.CheckpointLogDir); dir != "" {
		return dir
	}
	if a != nil && a.cfg != nil {
		if home := strings.TrimSpace(a.cfg.HomeDir()); home != "" {
			return filepath.Join(home, "runtime", "checkpoint-logs")
		}
	}
	if home := strings.TrimSpace(os.Getenv("LUCKYAGENT_HOME")); home != "" {
		return filepath.Join(home, "runtime", "checkpoint-logs")
	}
	return filepath.Join(os.TempDir(), "luckyagent-checkpoint-logs")
}

// sameCheckpointPrefix reports whether next still starts with prev. A
// rewritten transcript is stored as a reset event instead, so this only
// rejects a pending list that shrank from somewhere other than its front.
func sameCheckpointPrefix(prev, next *durableCheckpoint) bool {
	if pendingDroppedFromMiddle(prev.Pending, next.Pending) {
		return false
	}
	return true
}

// pendingDroppedFromMiddle is true when next is not a suffix of prev and not
// an extension of prev. The loop only consumes pending calls from the front.
func pendingDroppedFromMiddle(prev, next []durablePendingCall) bool {
	if len(next) >= len(prev) {
		for i := range prev {
			if !samePending(prev[i], next[i]) {
				return true
			}
		}
		return false
	}
	drop := len(prev) - len(next)
	for i := range next {
		if !samePending(prev[drop+i], next[i]) {
			return true
		}
	}
	return false
}

func samePending(a, b durablePendingCall) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

func sameMessage(a, b provider.Message) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ab, bb)
}

func diffCheckpoint(prev, next *durableCheckpoint) []checkpointEvent {
	var events []checkpointEvent
	shared := sharedMessagePrefix(prev.Messages, next.Messages)
	switch {
	case shared == len(prev.Messages) && len(next.Messages) > len(prev.Messages):
		added := append([]provider.Message(nil), next.Messages[shared:]...)
		events = append(events, checkpointEvent{Kind: checkpointEventMessages, Added: added})
	case shared != len(prev.Messages) || shared != len(next.Messages):
		events = append(events, checkpointEvent{Kind: checkpointEventReset, Messages: append([]provider.Message(nil), next.Messages...), Calls: append([]durablePendingCall(nil), next.Pending...)})
	}
	reset := len(events) > 0 && events[0].Kind == checkpointEventReset
	if !reset {
		if len(next.Pending) > len(prev.Pending) {
			added := append([]durablePendingCall(nil), next.Pending[len(prev.Pending):]...)
			events = append(events, checkpointEvent{Kind: checkpointEventPending, Pending: added})
		} else if len(next.Pending) < len(prev.Pending) {
			events = append(events, checkpointEvent{Kind: checkpointEventPendingDrop, Drop: len(prev.Pending) - len(next.Pending)})
		}
	}
	if changed := changedCheckpointFields(prev, next); len(changed) > 0 {
		state := sparseCheckpoint(next, changed)
		events = append(events, checkpointEvent{Kind: checkpointEventState, State: &state, Changed: changed})
	}
	return events
}

// changedCheckpointFields lists scalar fields whose JSON form differs.
// Digests and repeat signatures are included; messages and pending calls
// have their own events.
func changedCheckpointFields(prev, next *durableCheckpoint) []string {
	var changed []string
	for _, field := range checkpointStateFields {
		if checkpointFieldJSON(prev, field) != checkpointFieldJSON(next, field) {
			changed = append(changed, field)
		}
	}
	return changed
}

var checkpointStateFields = []string{
	"observe_only_batches", "memory_synthesis", "batch", "tokens", "candidate",
	"reasoning", "verification", "last_results", "last_result_digests",
	"repeat_signatures", "no_progress", "model_stalls", "continuation",
	"summary", "summarized_through", "shell",
}

func checkpointFieldJSON(cp *durableCheckpoint, field string) string {
	sparse := sparseCheckpoint(cp, []string{field})
	data, err := json.Marshal(sparse)
	if err != nil {
		return field
	}
	return string(data)
}

func sparseCheckpoint(cp *durableCheckpoint, fields []string) durableCheckpoint {
	var out durableCheckpoint
	for _, field := range fields {
		copyCheckpointField(&out, cp, field)
	}
	return out
}

func sharedMessagePrefix(prev, next []provider.Message) int {
	n := len(prev)
	if len(next) < n {
		n = len(next)
	}
	for i := 0; i < n; i++ {
		if !sameMessage(prev[i], next[i]) {
			return i
		}
	}
	return n
}

func cloneCheckpoint(cp *durableCheckpoint) durableCheckpoint {
	out := *cp
	out.Messages = append([]provider.Message(nil), cp.Messages...)
	out.Pending = append([]durablePendingCall(nil), cp.Pending...)
	out.LastResults = cloneStringMap(cp.LastResults)
	out.LastResultDigests = cloneStringMap(cp.LastResultDigests)
	out.RepeatSignatures = append([]string(nil), cp.RepeatSignatures...)
	out.Verification = cloneVerification(cp.Verification)
	out.Shell = cloneShell(cp.Shell)
	return out
}
