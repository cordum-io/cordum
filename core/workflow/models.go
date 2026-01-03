package workflow

import "time"

// StepType identifies the kind of step in a workflow.
type StepType string

const (
	StepTypeLLM         StepType = "llm"
	StepTypeWorker      StepType = "worker"
	StepTypeHTTP        StepType = "http"
	StepTypeContainer   StepType = "container"
	StepTypeScript      StepType = "script"
	StepTypeApproval    StepType = "approval"
	StepTypeInput       StepType = "input"
	StepTypeCondition   StepType = "condition"
	StepTypeSwitch      StepType = "switch"
	StepTypeParallel    StepType = "parallel"
	StepTypeLoop        StepType = "loop"
	StepTypeDelay       StepType = "delay"
	StepTypeNotify      StepType = "notify"
	StepTypeTransform   StepType = "transform"
	StepTypeStorage     StepType = "storage"
	StepTypeSubWorkflow StepType = "subworkflow"
)

// RunStatus captures the lifecycle of a workflow run.
type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusWaiting   RunStatus = "waiting"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
	RunStatusTimedOut  RunStatus = "timed_out"
)

// StepStatus captures the lifecycle of a step run.
type StepStatus string

const (
	StepStatusPending   StepStatus = "pending"
	StepStatusRunning   StepStatus = "running"
	StepStatusWaiting   StepStatus = "waiting"
	StepStatusSucceeded StepStatus = "succeeded"
	StepStatusFailed    StepStatus = "failed"
	StepStatusCancelled StepStatus = "cancelled"
	StepStatusTimedOut  StepStatus = "timed_out"
)

// RetryConfig configures retry behavior for a step.
type RetryConfig struct {
	MaxRetries        int     `json:"max_retries,omitempty"`
	InitialBackoffSec int     `json:"initial_backoff_sec,omitempty"`
	MaxBackoffSec     int     `json:"max_backoff_sec,omitempty"`
	Multiplier        float64 `json:"multiplier,omitempty"`
}

// StepMeta captures job metadata overrides for a workflow step.
type StepMeta struct {
	ActorId        string            `json:"actor_id,omitempty"`
	ActorType      string            `json:"actor_type,omitempty"` // "human" | "service"
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	PackId         string            `json:"pack_id,omitempty"`
	Capability     string            `json:"capability,omitempty"`
	RiskTags       []string          `json:"risk_tags,omitempty"`
	Requires       []string          `json:"requires,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// Workflow is the persisted definition.
type Workflow struct {
	ID          string           `json:"id" db:"id"`
	OrgID       string           `json:"org_id" db:"org_id"`
	TeamID      string           `json:"team_id" db:"team_id"`
	Name        string           `json:"name" db:"name"`
	Description string           `json:"description" db:"description"`
	Version     string           `json:"version" db:"version"`
	TimeoutSec  int64            `json:"timeout_sec" db:"timeout_sec"`
	Steps       map[string]*Step `json:"steps" db:"steps"`                  // JSON
	Config      map[string]any   `json:"config,omitempty" db:"config"`      // JSON
	InputSchema map[string]any   `json:"input_schema,omitempty" db:"input"` // JSON
	Parameters  []map[string]any `json:"parameters,omitempty" db:"params"`  // JSON
	CreatedBy   string           `json:"created_by" db:"created_by"`
	CreatedAt   time.Time        `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at" db:"updated_at"`
}

// Step is a node in the workflow graph.
type Step struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           StepType          `json:"type"`
	WorkerID       string            `json:"worker_id,omitempty"` // for worker/container types
	Topic          string            `json:"topic,omitempty"`     // for built-in job topics
	DependsOn      []string          `json:"depends_on,omitempty"`
	Condition      string            `json:"condition,omitempty"` // expression
	ForEach        string            `json:"for_each,omitempty"`  // expression
	MaxParallel    int               `json:"max_parallel,omitempty"`
	Input          map[string]any    `json:"input,omitempty"` // can contain expressions
	InputSchema    map[string]any    `json:"input_schema,omitempty"`
	InputSchemaID  string            `json:"input_schema_id,omitempty"`
	OutputPath     string            `json:"output_path,omitempty"` // context path
	OutputSchema   map[string]any    `json:"output_schema,omitempty"`
	OutputSchemaID string            `json:"output_schema_id,omitempty"`
	Meta           *StepMeta         `json:"meta,omitempty"`
	OnError        string            `json:"on_error,omitempty"` // step ID to jump to
	Retry          *RetryConfig      `json:"retry,omitempty"`
	TimeoutSec     int64             `json:"timeout_sec,omitempty"`
	DelaySec       int64             `json:"delay_sec,omitempty"`
	DelayUntil     string            `json:"delay_until,omitempty"`
	RouteLabels    map[string]string `json:"route_labels,omitempty"` // routing hints to workers/pools
}

// WorkflowRun represents one execution.
type WorkflowRun struct {
	ID             string              `json:"id" db:"id"`
	WorkflowID     string              `json:"workflow_id" db:"workflow_id"`
	OrgID          string              `json:"org_id" db:"org_id"`
	TeamID         string              `json:"team_id" db:"team_id"`
	Input          map[string]any      `json:"input" db:"input"`     // JSON
	Context        map[string]any      `json:"context" db:"context"` // JSON
	Status         RunStatus           `json:"status" db:"status"`
	StartedAt      *time.Time          `json:"started_at" db:"started_at"`
	CompletedAt    *time.Time          `json:"completed_at" db:"completed_at"`
	Output         map[string]any      `json:"output" db:"output"` // JSON
	Error          map[string]any      `json:"error" db:"error"`   // JSON (code/message)
	Steps          map[string]*StepRun `json:"steps" db:"steps"`   // JSON
	TotalCost      float64             `json:"total_cost" db:"total_cost"`
	TriggeredBy    string              `json:"triggered_by" db:"triggered_by"`
	CreatedAt      time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at" db:"updated_at"`
	Labels         map[string]string   `json:"labels,omitempty" db:"labels"`
	Metadata       map[string]string   `json:"metadata,omitempty" db:"metadata"`
	IdempotencyKey string              `json:"idempotency_key,omitempty" db:"idempotency_key"`
	RerunOf        string              `json:"rerun_of,omitempty" db:"rerun_of"`
	RerunStep      string              `json:"rerun_step,omitempty" db:"rerun_step"`
	DryRun         bool                `json:"dry_run,omitempty" db:"dry_run"`
}

// StepRun tracks state for a step within a run.
type StepRun struct {
	StepID        string              `json:"step_id"`
	Status        StepStatus          `json:"status"`
	StartedAt     *time.Time          `json:"started_at,omitempty"`
	CompletedAt   *time.Time          `json:"completed_at,omitempty"`
	NextAttemptAt *time.Time          `json:"next_attempt_at,omitempty"`
	Attempts      int                 `json:"attempts,omitempty"`
	Input         map[string]any      `json:"input,omitempty"`
	Output        any                 `json:"output,omitempty"`
	Error         map[string]any      `json:"error,omitempty"`
	JobID         string              `json:"job_id,omitempty"` // dispatched job ID
	Item          any                 `json:"item,omitempty"`   // for for_each child entries
	Children      map[string]*StepRun `json:"children,omitempty"`
}

// TimelineEvent captures append-only run events for audit/replay.
type TimelineEvent struct {
	Time       time.Time      `json:"time"`
	Type       string         `json:"type"`
	RunID      string         `json:"run_id,omitempty"`
	WorkflowID string         `json:"workflow_id,omitempty"`
	StepID     string         `json:"step_id,omitempty"`
	JobID      string         `json:"job_id,omitempty"`
	Status     string         `json:"status,omitempty"`
	ResultPtr  string         `json:"result_ptr,omitempty"`
	Message    string         `json:"message,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}
