// Package runner replays a curated eval dataset through a supplied
// policy evaluator and classifies each entry as pass / fail /
// regression / error. The package is deliberately decoupled from the
// concrete policy-evaluation pipeline via the PolicyEvaluator interface
// so the runner can be unit-tested against a fake policy and wired
// into the production `policybundles.EvaluatePolicyCheck` pipeline at
// the handler boundary in a sibling step.
package runner

import (
	"encoding/json"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// EntryStatus is the outcome classification for a single entry run.
// The four values are mutually exclusive.
type EntryStatus string

const (
	StatusPass       EntryStatus = "pass"
	StatusFail       EntryStatus = "fail"
	StatusRegression EntryStatus = "regression"
	StatusError      EntryStatus = "error"
)

// DriftDirection mirrors the familiar policy-replay vocabulary.
type DriftDirection string

const (
	DriftEscalated DriftDirection = "escalated"
	DriftRelaxed   DriftDirection = "relaxed"
	DriftUnchanged DriftDirection = "unchanged"
)

// RunRequest controls a single evaluation pass.
type RunRequest struct {
	Tenant            string
	DatasetID         string
	UseCurrentPolicy  bool
	CandidateBundleID string
	CandidateContent  string
	MaxEntries        int
}

// HardMaxEntries caps any single run at 10k entries — matches the
// model-level dataset cap so callers can't bypass limits by tweaking
// MaxEntries.
const HardMaxEntries = 10_000

// RunSummary rolls up per-entry outcomes. ScorePercent is nil when
// Total == 0 so the wire encodes that as JSON null rather than NaN.
type RunSummary struct {
	Total        int      `json:"total"`
	Passed       int      `json:"passed"`
	Failed       int      `json:"failed"`
	Regressions  int      `json:"regressions"`
	Errored      int      `json:"errored"`
	ScorePercent *float64 `json:"score_percent"`
}

// EntryResult carries one entry's per-entry outcome.
type EntryResult struct {
	EntryID          string          `json:"entry_id"`
	Input            json.RawMessage `json:"input"`
	ExpectedDecision string          `json:"expected_decision"`
	ActualDecision   string          `json:"actual_decision"`
	RuleID           string          `json:"rule_id,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	Status           EntryStatus     `json:"status"`
	DriftDirection   DriftDirection  `json:"drift_direction"`
	Error            string          `json:"error,omitempty"`
}

// RunResult is the durable shape of a completed run.
type RunResult struct {
	RunID          string        `json:"run_id"`
	DatasetID      string        `json:"dataset_id"`
	DatasetName    string        `json:"dataset_name"`
	DatasetVersion int           `json:"dataset_version"`
	Tenant         string        `json:"tenant"`
	PolicySnapshot string        `json:"policy_snapshot"`
	StartedAt      time.Time     `json:"started_at"`
	CompletedAt    time.Time     `json:"completed_at"`
	Summary        RunSummary    `json:"summary"`
	Entries        []EntryResult `json:"entries"`
}

// PolicyEvaluator is the narrow abstraction the runner depends on so
// it can be unit-tested against a fake policy while being wired to
// `policybundles.EvaluatePolicyCheck` in production.
type PolicyEvaluator interface {
	Evaluate(req *pb.JobRequest) (decision, ruleID, reason string, err error)
}

// RunContext carries the caller-owned metadata that describes which
// dataset + policy the run is targeting.
type RunContext struct {
	RunID          string
	Tenant         string
	DatasetName    string
	DatasetVersion int
	PolicySnapshot string
	Clock          func() time.Time
}

func (rc RunContext) normalizeClock() func() time.Time {
	if rc.Clock != nil {
		return rc.Clock
	}
	return func() time.Time { return time.Now().UTC() }
}

// ExpectedDecisionString returns the canonical SCREAMING_SNAKE form of
// the entry's expected decision.
func ExpectedDecisionString(e model.EvalEntry) string {
	return string(e.ExpectedDecision)
}
