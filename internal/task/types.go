package task

import "time"

type Source string

const (
	SourceTool     Source = "tool"
	SourceHTTP     Source = "http"
	SourceAutonomy Source = "autonomy"
	SourceGateway  Source = "gateway"
)

type Mode string

const (
	ModeSingle        Mode = "single"
	ModeAuto          Mode = "auto"
	ModeParallel      Mode = "parallel"
	ModePipeline      Mode = "pipeline"
	ModeDebate        Mode = "debate"
	ModeAutonomyQueue Mode = "autonomy_queue"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusBlocked   Status = "blocked"
	StatusCancelled Status = "cancelled"
)

type EventType string

const (
	EventCreated         EventType = "task.created"
	EventPlanned         EventType = "task.planned"
	EventStarted         EventType = "task.started"
	EventChildCreated    EventType = "task.child_created"
	EventProgress        EventType = "task.progress"
	EventToolUsed        EventType = "task.tool_used"
	EventCompleted       EventType = "task.completed"
	EventFailed          EventType = "task.failed"
	EventCancelled       EventType = "task.cancelled"
	EventOutcomeRecorded EventType = "task.outcome_recorded"
)

type RuntimeRef struct {
	Type      string            `json:"type,omitempty"`
	AgentID   string            `json:"agent_id,omitempty"`
	ProfileID string            `json:"profile_id,omitempty"`
	Workspace string            `json:"workspace,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

type Budget struct {
	MaxChildren     int           `json:"max_children,omitempty"`
	MaxConcurrent   int           `json:"max_concurrent,omitempty"`
	MaxDebateRounds int           `json:"max_debate_rounds,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	MaxToolCalls    int           `json:"max_tool_calls,omitempty"`
	Timeout         time.Duration `json:"timeout,omitempty"`
	AllowRecursive  bool          `json:"allow_recursive,omitempty"`
	RequireVerifier bool          `json:"require_verifier,omitempty"`
}

type CostSnapshot struct {
	TokenEstimate int           `json:"token_estimate,omitempty"`
	ToolCalls     int           `json:"tool_calls,omitempty"`
	Elapsed       time.Duration `json:"elapsed,omitempty"`
	ChildCount    int           `json:"child_count,omitempty"`
	RetryCount    int           `json:"retry_count,omitempty"`
}

type Outcome struct {
	Status          Status       `json:"status,omitempty"`
	Verified        bool         `json:"verified,omitempty"`
	Verifier        string       `json:"verifier,omitempty"`
	UserFeedback    string       `json:"user_feedback,omitempty"`
	Score           float64      `json:"score,omitempty"`
	Cost            CostSnapshot `json:"cost,omitempty"`
	RecommendedNext string       `json:"recommended_next,omitempty"`
}

type Record struct {
	ID          string            `json:"id"`
	ParentID    string            `json:"parent_id,omitempty"`
	Source      Source            `json:"source"`
	Mode        Mode              `json:"mode"`
	Status      Status            `json:"status"`
	Description string            `json:"description"`
	Input       string            `json:"input,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	CompletedAt time.Time         `json:"completed_at,omitempty"`
	Runtime     RuntimeRef        `json:"runtime,omitempty"`
	Budget      Budget            `json:"budget,omitempty"`
	Outcome     Outcome           `json:"outcome,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type Event struct {
	Type     EventType         `json:"type"`
	TaskID   string            `json:"task_id"`
	ParentID string            `json:"parent_id,omitempty"`
	Time     time.Time         `json:"time"`
	Message  string            `json:"message,omitempty"`
	Status   Status            `json:"status,omitempty"`
	Mode     Mode              `json:"mode,omitempty"`
	Progress float64           `json:"progress,omitempty"`
	ChildID  string            `json:"child_id,omitempty"`
	Error    string            `json:"error,omitempty"`
	Evidence []string          `json:"evidence,omitempty"`
	Files    []string          `json:"files,omitempty"`
	Tests    []string          `json:"tests,omitempty"`
	Cost     CostSnapshot      `json:"cost,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type Store interface {
	Create(record Record) (Record, error)
	Update(record Record) error
	Get(id string) (Record, bool, error)
	List(filter ListFilter) ([]Record, error)
	AppendEvent(event Event) error
	Events(taskID string) ([]Event, error)
	SaveResult(taskID, markdown string) error
	SavePlannerTrace(taskID string, trace any) error
}

type ListFilter struct {
	Source   Source
	Status   Status
	ParentID string
	Limit    int
}
