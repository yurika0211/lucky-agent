package agent

import (
	"hash/fnv"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yurika0211/luckyagent-runtime/logger"
)

// Compact A/B compares two compactions of the same tool result.
//
// Control is the existing line compressor. Treatment rewrites a recognised
// shape first, then applies the same line compressor and the same rune budget.
// The paired difference is the effect: both arms see the same input, so a
// shorter treatment is not an artifact of which tool happened to run.
//
// Assignment is deterministic for a tool-call id, so a replay stays in the
// same arm. The context only receives the assigned arm. The other arm is
// computed for the ledger and discarded. LUCKYAGENT_COMPACT_AB=control or
// =treatment forces the arm; the default is the hash split. =off disables the
// shadow comparison and keeps the historical line compressor.

const (
	compactArmControl   = "control"
	compactArmTreatment = "treatment"
	compactABOff        = "off"
)

// compactABMinSamples is the point at which a ratio is reported. Fewer pairs
// are still recorded; the ratio is withheld so a handful of diffs cannot look
// like a result.
const compactABMinSamples = 30

// compactShapeABEnabled is the process default. Tests flip it directly.
var compactShapeABEnabled atomic.Bool

func init() {
	// The paired ledger is on so a deployment can measure the rewrite. Context
	// still receives the control arm until LUCKYAGENT_COMPACT_AB=treatment, or
	// until the measured saving and the identifier check both hold.
	compactShapeABEnabled.Store(true)
}

// compactABLedger stores paired outcomes. It is process-local and reset by tests.
type compactABLedger struct {
	mu    sync.Mutex
	pairs []compactABPair
}

type compactABPair struct {
	Tool            string
	Filter          string
	Skip            string
	Arm             string
	InputBytes      int
	ControlBytes    int
	TreatmentBytes  int
	ControlRunes    int
	TreatmentRunes  int
	ServedBytes     int
	IdentifiersKept bool
	ShapeIdempotent bool
}

var compactAB = &compactABLedger{}

// compactABSnapshot is the readable aggregate. SavedRatio is treatment savings
// against the control arm, (control-treatment)/control. Negative means the
// shape rewrite made the budgeted result larger.
type compactABSnapshot struct {
	Samples          int            `json:"samples"`
	ControlServed    int            `json:"control_served"`
	TreatmentServed  int            `json:"treatment_served"`
	InputBytes       int            `json:"input_bytes"`
	ControlBytes     int            `json:"control_bytes"`
	TreatmentBytes   int            `json:"treatment_bytes"`
	BytesSaved       int            `json:"bytes_saved"`
	SavedRatio       float64        `json:"saved_ratio,omitempty"`
	RatioReady       bool           `json:"ratio_ready"`
	IdentifierMisses int            `json:"identifier_misses"`
	NonIdempotent    int            `json:"non_idempotent"`
	ByFilter         map[string]int `json:"by_filter,omitempty"`
	BySkip           map[string]int `json:"by_skip,omitempty"`
	ByArm            map[string]int `json:"by_arm,omitempty"`
}

func (l *compactABLedger) add(pair compactABPair) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.pairs = append(l.pairs, pair)
	n := len(l.pairs)
	l.mu.Unlock()
	if n%compactABMinSamples == 0 {
		logger.Info("compact shape ab", "samples", n, "snapshot", l.snapshot())
	}
}

func (l *compactABLedger) snapshot() compactABSnapshot {
	if l == nil {
		return compactABSnapshot{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := compactABSnapshot{
		Samples:  len(l.pairs),
		ByFilter: map[string]int{},
		BySkip:   map[string]int{},
		ByArm:    map[string]int{},
	}
	for _, pair := range l.pairs {
		out.InputBytes += pair.InputBytes
		out.ControlBytes += pair.ControlBytes
		out.TreatmentBytes += pair.TreatmentBytes
		out.ByArm[pair.Arm]++
		if pair.Arm == compactArmControl {
			out.ControlServed++
		} else {
			out.TreatmentServed++
		}
		if pair.Filter != "" {
			out.ByFilter[pair.Filter]++
		}
		if pair.Skip != "" {
			out.BySkip[pair.Skip]++
		}
		if !pair.IdentifiersKept {
			out.IdentifierMisses++
		}
		if pair.Filter != "" && !pair.ShapeIdempotent {
			out.NonIdempotent++
		}
	}
	out.BytesSaved = out.ControlBytes - out.TreatmentBytes
	if out.Samples >= compactABMinSamples && out.ControlBytes > 0 {
		out.SavedRatio = float64(out.BytesSaved) / float64(out.ControlBytes)
		out.RatioReady = true
	}
	return out
}

func (l *compactABLedger) reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.pairs = nil
	l.mu.Unlock()
}

// CompactShapeABSnapshot returns the paired comparison collected so far.
func CompactShapeABSnapshot() compactABSnapshot {
	return compactAB.snapshot()
}

// compactToolResultAB compacts one tool result and records both arms.
// toolError skips the shape rewrite; the line compressor still applies, because
// an error stack that exceeds the rune budget has always been truncated.
func compactToolResultAB(toolName, result, assignmentKey string, limit int, toolError bool) string {
	control := compressToolResultLines(result, limit)
	if !compactShapeABEnabled.Load() {
		return control
	}
	shape := rewriteToolResultShape(result, toolError)
	shaped := result
	if shape.Filter != "" {
		shaped = shape.Text
	}
	treatment := compressToolResultLines(shaped, limit)
	arm := compactArmControl
	served := control
	servedBytes := len(control)
	if compactABMode() == compactArmTreatment {
		arm = compactArmTreatment
		served = treatment
		servedBytes = len(treatment)
	} else if assignmentKey != "" && compactABMode() == "split" && compactAssignment(assignmentKey) == compactArmTreatment {
		arm = compactArmTreatment
		served = treatment
		servedBytes = len(treatment)
	}
	compactAB.add(compactABPair{
		Tool:            toolName,
		Filter:          shape.Filter,
		Skip:            shape.Skip,
		Arm:             arm,
		InputBytes:      len(result),
		ControlBytes:    len(control),
		TreatmentBytes:  len(treatment),
		ControlRunes:    len([]rune(control)),
		TreatmentRunes:  len([]rune(treatment)),
		ServedBytes:     servedBytes,
		IdentifiersKept: shapeKeepsIdentifiers(result, treatment),
		ShapeIdempotent: shape.Idempotent || shape.Filter == "",
	})
	return served
}

// compactAssignment puts the same key in the same arm across replays.
// An empty key is control: tests and callers without a tool-call id keep the
// historical result unless they opt into the split.
func compactAssignment(key string) string {
	switch compactABMode() {
	case compactArmControl, compactABOff:
		return compactArmControl
	case compactArmTreatment:
		return compactArmTreatment
	}
	if key == "" {
		return compactArmControl
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	if h.Sum32()%2 == 0 {
		return compactArmControl
	}
	return compactArmTreatment
}

func compactABMode() string {
	// Tests set the override. Production reads the environment on each call so
	// a running process can be flipped without resetting the ledger.
	if mode := compactABModeOverride.Load(); mode != nil {
		if text, ok := mode.(string); ok && text != "" {
			return text
		}
	}
	return os.Getenv("LUCKYAGENT_COMPACT_AB")
}

var compactABModeOverride atomic.Value

func shapeKeepsIdentifiers(source, compacted string) bool {
	for _, line := range nonEmptyLines(source) {
		if !toolLineHasIdentifier(line) {
			continue
		}
		// A shape rewrite may regroup a path onto its own header. The check is
		// that the identifying token is still present, not that the whole line
		// survived verbatim. Lines longer than the budget cannot be required
		// whole; the line compressor is allowed to cut those.
		token := identifyingToken(line)
		if token == "" {
			continue
		}
		if !containsToken(compacted, token) && len([]rune(line)) <= 8000 {
			return false
		}
	}
	return true
}

func identifyingToken(line string) string {
	if match := pathPattern.FindString(line); match != "" {
		return trimToken(match)
	}
	if match := urlPattern.FindString(line); match != "" {
		return match
	}
	if match := commitPattern.FindString(strings.ToLower(line)); match != "" {
		return match
	}
	return ""
}

func trimToken(token string) string {
	return strings.Trim(token, " \t\"'(=:[,")
}

func containsToken(text, token string) bool {
	return len(token) > 0 && (strings.Contains(text, token) || strings.Contains(strings.ToLower(text), strings.ToLower(token)))
}
