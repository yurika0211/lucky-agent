package turn

import "time"

// DurableOperation is the neutral write-ahead record used by a resumable
// turn. It mirrors the persistence fields without importing autonomy.
type DurableOperation struct {
	ID         string
	Name       string
	Arguments  string
	State      string
	Output     string
	Detail     []byte
	Failed     bool
	ResolvedBy string
}

// DurableTask is the checkpoint view needed by the turn runner.
type DurableTask struct {
	SessionID          string
	Checkpoint         []byte
	Operations         []DurableOperation
	AcceptanceCriteria []string
	Verified           bool
	Verification       string
	ID                 string
	Title              string
	Description        string
	State              string
	UpdatedAt          time.Time
}

// DurableResult is the neutral result passed back to the queue owner when a
// durable slice finishes.
type DurableResult struct {
	Output       string
	Error        error
	// Duration is the active execution time for the durable slice. Queue
	// owners use it to enforce cumulative foreground budgets without charging
	// time spent waiting between slices.
	Duration     time.Duration
	TokensUsed   int
	Verified     bool
	Verification string
}

// DurableExecution is the persistence boundary for resumable turns. The
// queue implementation lives outside the turn package and adapts to this
// contract at the runtime boundary.
type DurableExecution interface {
	Snapshot() (*DurableTask, error)
	BindSession(string) error
	SaveCheckpoint([]byte) error
	BeginOperation(DurableOperation) (DurableOperation, error)
	FinishOperation(id, output string, failed bool, detail []byte) error
	Finish(*DurableResult, bool) error
}
