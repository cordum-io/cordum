package extraction

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const (
	DefaultMaxEntries = 1000
	MaxExtractionDays = 90
)

var (
	defaultVerdicts        = []model.SafetyDecision{model.SafetyDeny, model.SafetyRequireApproval}
	maxExtractionWindow    = time.Duration(MaxExtractionDays) * 24 * time.Hour
	defaultExtractionRange = 24 * time.Hour
	datasetNamePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9_\-]{2,63}$`)
)

// ExtractionRequest describes a scan of the Policy Decision Log that turns
// matched incidents into an immutable eval dataset.
type ExtractionRequest struct {
	Tenant             string
	Since              time.Time
	Until              time.Time
	TopicPattern       string
	RuleID             string
	Verdicts           []model.SafetyDecision
	AgentID            string
	MaxEntries         int
	DatasetName        string
	DatasetDescription string
	DryRun             bool
}

// ExtractionResult summarizes an incident extraction run.
type ExtractionResult struct {
	DatasetID        string   `json:"dataset_id,omitempty"`
	Name             string   `json:"name"`
	Version          int      `json:"version,omitempty"`
	EntryCount       int      `json:"entry_count"`
	DedupedCount     int      `json:"deduped_count,omitempty"`
	ScannedDecisions int      `json:"scanned_decisions"`
	Warnings         []string `json:"warnings,omitempty"`
}

// JobRequestStore is the subset of the job store the extraction pipeline
// needs. The concrete Redis job store already satisfies this interface.
type JobRequestStore interface {
	GetJobRequest(ctx context.Context, jobID string) (*pb.JobRequest, error)
}

// ExtractionDeps packages the external dependencies required by the extractor.
type ExtractionDeps struct {
	DecisionLog  model.DecisionLogStore
	JobStore     JobRequestStore
	EvalDatasets model.EvalDatasetStore
	Now          func() time.Time
}

// Normalize applies defaults and validates the request shape.
func (r ExtractionRequest) Normalize(now time.Time) (ExtractionRequest, error) {
	normalized := r
	normalized.Tenant = strings.TrimSpace(normalized.Tenant)
	normalized.TopicPattern = strings.TrimSpace(normalized.TopicPattern)
	normalized.RuleID = strings.TrimSpace(normalized.RuleID)
	normalized.AgentID = strings.TrimSpace(normalized.AgentID)
	normalized.DatasetName = strings.ToLower(strings.TrimSpace(normalized.DatasetName))
	normalized.DatasetDescription = strings.TrimSpace(normalized.DatasetDescription)

	if normalized.Tenant == "" {
		return ExtractionRequest{}, fmt.Errorf("tenant is required")
	}
	if normalized.DatasetName == "" {
		return ExtractionRequest{}, fmt.Errorf("dataset name is required")
	}
	if !datasetNamePattern.MatchString(normalized.DatasetName) {
		return ExtractionRequest{}, fmt.Errorf("dataset name must match %s", datasetNamePattern.String())
	}

	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	normalized.Since = normalized.Since.UTC()
	normalized.Until = normalized.Until.UTC()
	switch {
	case normalized.Since.IsZero() && normalized.Until.IsZero():
		normalized.Until = now
		normalized.Since = now.Add(-defaultExtractionRange)
	case normalized.Since.IsZero():
		normalized.Until = normalized.Until.UTC()
		normalized.Since = normalized.Until.Add(-defaultExtractionRange)
	case normalized.Until.IsZero():
		normalized.Until = now
	}
	if normalized.Until.Before(normalized.Since) {
		return ExtractionRequest{}, fmt.Errorf("until must be >= since")
	}
	if normalized.Until.Sub(normalized.Since) > maxExtractionWindow {
		return ExtractionRequest{}, fmt.Errorf("time window must be <= %d days", MaxExtractionDays)
	}

	if normalized.MaxEntries <= 0 {
		normalized.MaxEntries = DefaultMaxEntries
	}
	if normalized.MaxEntries > model.MaxEvalDatasetEntries {
		return ExtractionRequest{}, fmt.Errorf("max_entries must be <= %d", model.MaxEvalDatasetEntries)
	}

	if len(normalized.Verdicts) == 0 {
		normalized.Verdicts = append([]model.SafetyDecision(nil), defaultVerdicts...)
	} else {
		seen := make(map[model.SafetyDecision]struct{}, len(normalized.Verdicts))
		deduped := make([]model.SafetyDecision, 0, len(normalized.Verdicts))
		for _, verdict := range normalized.Verdicts {
			if err := validateVerdict(verdict); err != nil {
				return ExtractionRequest{}, err
			}
			if _, ok := seen[verdict]; ok {
				continue
			}
			seen[verdict] = struct{}{}
			deduped = append(deduped, verdict)
		}
		normalized.Verdicts = deduped
	}
	if len(normalized.Verdicts) == 0 {
		return ExtractionRequest{}, fmt.Errorf("at least one verdict is required")
	}

	return normalized, nil
}

func validateVerdict(verdict model.SafetyDecision) error {
	switch verdict {
	case model.SafetyAllow,
		model.SafetyDeny,
		model.SafetyRequireApproval,
		model.SafetyThrottle,
		model.SafetyAllowWithConstraints:
		return nil
	default:
		return fmt.Errorf("invalid verdict %q", verdict)
	}
}
