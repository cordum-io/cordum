package model

// StateEventContext captures rich context for a job state transition.
// Each field is optional — callers populate only what's relevant for
// the specific transition (e.g. safety eval populates Rule/EvalMs,
// dispatch populates WorkerID/Pool, completion populates DurationMs).
type StateEventContext struct {
	Rule         string            `json:"rule,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	EvalMs       int64             `json:"eval_ms,omitempty"`
	EvalPath     []string          `json:"eval_path,omitempty"`
	WorkerID     string            `json:"worker_id,omitempty"`
	Pool         string            `json:"pool,omitempty"`
	Strategy     string            `json:"strategy,omitempty"`
	ApprovedBy   string            `json:"approved_by,omitempty"`
	ApprovalRole string            `json:"approval_role,omitempty"`
	ApprovalNote string            `json:"approval_note,omitempty"`
	WaitMs       int64             `json:"wait_ms,omitempty"`
	ErrorCode    string            `json:"error_code,omitempty"`
	ErrorMsg     string            `json:"error_msg,omitempty"`
	ResultPtr    string            `json:"result_ptr,omitempty"`
	DurationMs   int64             `json:"duration_ms,omitempty"`
	Findings     int               `json:"findings,omitempty"`
	Scanner      string            `json:"scanner,omitempty"`
	Topic        string            `json:"topic,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	RiskTags     []string          `json:"risk_tags,omitempty"`
	ActorID      string            `json:"actor_id,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// JobEvent represents a single state transition event stored in the
// job:events:{id} Redis LIST. Supports both the new JSON format and
// backward-compatible parsing of old "timestamp|state" entries.
type JobEvent struct {
	Timestamp int64              `json:"ts"`
	State     string             `json:"state"`
	Context   *StateEventContext `json:"ctx,omitempty"`
}
