package model

import (
	"context"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// OutputDecision indicates the result of an output policy check.
type OutputDecision string

const (
	OutputAllow      OutputDecision = "ALLOW"
	OutputDeny       OutputDecision = "DENY"
	OutputQuarantine OutputDecision = "QUARANTINE"
	OutputRedact     OutputDecision = "REDACT"
)

// OutputEvaluateRequest captures output content and original job context for policy checks.
type OutputEvaluateRequest struct {
	JobID           string
	Topic           string
	Tenant          string
	Labels          map[string]string
	ResultPtr       string
	ArtifactPtrs    []string
	ErrorMessage    string
	ErrorCode       string
	WorkerID        string
	ExecutionMs     int64
	OutputSizeBytes int64
	ContentHash     string
	WorkflowID      string
	StepID          string
	OutputContent   []byte
	Capabilities    []string
	RiskTags        []string
	PrincipalID     string
	PackID          string
	ContentType     string
	OriginalLabels  map[string]string
}

type OutputFinding struct {
	Type           string  `json:"type"`
	Severity       string  `json:"severity"`
	Detail         string  `json:"detail"`
	Scanner        string  `json:"scanner,omitempty"`
	Confidence     float64 `json:"confidence,omitempty"`
	MatchedPattern string  `json:"matched_pattern,omitempty"`
	Offset         int64   `json:"offset,omitempty"`
	Length         int64   `json:"length,omitempty"`
}

// OutputSafetyRecord captures the output policy evaluation result.
type OutputSafetyRecord struct {
	Decision        OutputDecision  `json:"decision"`
	Reason          string          `json:"reason,omitempty"`
	RuleID          string          `json:"rule_id,omitempty"`
	PolicySnapshot  string          `json:"policy_snapshot,omitempty"`
	Findings        []OutputFinding `json:"findings,omitempty"`
	RedactedPtr     string          `json:"redacted_ptr,omitempty"`
	OriginalPtr     string          `json:"original_ptr,omitempty"`
	CheckedAt       int64           `json:"checked_at,omitempty"`
	CheckDurationMs int64           `json:"check_duration_ms,omitempty"`
	Phase           string          `json:"phase,omitempty"` // "sync" or "async"
}

// OutputSafetyChecker evaluates job outputs against policy rules.
type OutputSafetyChecker interface {
	// EvaluateOutput runs output policy checks using dereferenced content and original request context.
	EvaluateOutput(ctx context.Context, req *OutputEvaluateRequest) (OutputSafetyRecord, error)
	// CheckOutputMeta runs fast sync checks on metadata only (~1ms).
	CheckOutputMeta(res *pb.JobResult, req *pb.JobRequest) (OutputSafetyRecord, error)
	// CheckOutputContent runs deep async checks on actual content.
	CheckOutputContent(ctx context.Context, res *pb.JobResult, req *pb.JobRequest) (OutputSafetyRecord, error)
}
