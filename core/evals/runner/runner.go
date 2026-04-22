package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/cordum/cordum/core/controlplane/policyreplay"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// inputSnapshot captures the subset of the originating job-request
// shape that the dataset store preserves in EvalEntry.Input. Every
// field is optional; the runner projects what's present into a minimal
// *pb.JobRequest and trusts the evaluator with the rest.
type inputSnapshot struct {
	Tenant       string            `json:"tenant,omitempty"`
	Topic        string            `json:"topic,omitempty"`
	AgentID      string            `json:"agent_id,omitempty"`
	PrincipalID  string            `json:"principal_id,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	RiskTags     []string          `json:"risk_tags,omitempty"`
	Requires     []string          `json:"requires,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	PackID       string            `json:"pack_id,omitempty"`
	Capability   string            `json:"capability,omitempty"`
}

// Run replays a dataset against the supplied evaluator. Entries are
// evaluated SEQUENTIALLY — policy evaluation is stateless and fast,
// and sequential order keeps the EntryResult slice deterministic for
// history storage and diffing.
func Run(ctx context.Context, rc RunContext, dataset model.EvalDataset, evaluator PolicyEvaluator, req RunRequest) (RunResult, error) {
	if evaluator == nil {
		return RunResult{}, fmt.Errorf("runner: policy evaluator is required")
	}
	if err := ctx.Err(); err != nil {
		return RunResult{}, err
	}

	clock := rc.normalizeClock()
	started := clock()

	limit := len(dataset.Entries)
	if req.MaxEntries > 0 && req.MaxEntries < limit {
		limit = req.MaxEntries
	}
	if limit > HardMaxEntries {
		limit = HardMaxEntries
	}

	entries := make([]EntryResult, 0, limit)
	var passed, failed, regressions, errored int

	for i := 0; i < limit; i++ {
		if err := ctx.Err(); err != nil {
			return RunResult{}, err
		}
		e := dataset.Entries[i]
		expected := strings.ToUpper(strings.TrimSpace(ExpectedDecisionString(e)))

		jobReq, buildErr := buildJobRequest(e)
		if buildErr != nil {
			entries = append(entries, EntryResult{
				EntryID:          e.ID,
				Input:            e.Input,
				ExpectedDecision: expected,
				Status:           StatusError,
				DriftDirection:   DriftUnchanged,
				Error:            buildErr.Error(),
			})
			errored++
			continue
		}

		decision, ruleID, reason, evalErr := evaluator.Evaluate(jobReq)
		if evalErr != nil {
			entries = append(entries, EntryResult{
				EntryID:          e.ID,
				Input:            e.Input,
				ExpectedDecision: expected,
				Status:           StatusError,
				DriftDirection:   DriftUnchanged,
				Error:            evalErr.Error(),
			})
			errored++
			continue
		}

		actual := strings.ToUpper(strings.TrimSpace(decision))
		status, drift := classify(expected, actual)
		switch status {
		case StatusPass:
			passed++
		case StatusRegression:
			regressions++
		case StatusFail:
			failed++
		}

		entries = append(entries, EntryResult{
			EntryID:          e.ID,
			Input:            e.Input,
			ExpectedDecision: expected,
			ActualDecision:   actual,
			RuleID:           ruleID,
			Reason:           reason,
			Status:           status,
			DriftDirection:   drift,
		})
	}

	total := len(entries)
	summary := RunSummary{
		Total:       total,
		Passed:      passed,
		Failed:      failed,
		Regressions: regressions,
		Errored:     errored,
	}
	if total > 0 {
		raw := float64(passed) / float64(total) * 100
		rounded := math.Round(raw*100) / 100
		summary.ScorePercent = &rounded
	}

	completed := clock()
	return RunResult{
		RunID:          rc.RunID,
		DatasetID:      dataset.ID,
		DatasetName:    rc.DatasetName,
		DatasetVersion: rc.DatasetVersion,
		Tenant:         rc.Tenant,
		PolicySnapshot: rc.PolicySnapshot,
		StartedAt:      started,
		CompletedAt:    completed,
		Summary:        summary,
		Entries:        entries,
	}, nil
}

// classify returns the entry outcome + drift direction. Per the epic
// rail: a regression is strictly ExpectedDecision ∈ {DENY,
// REQUIRE_APPROVAL, THROTTLE, ALLOW_WITH_CONSTRAINTS} AND
// ActualDecision == ALLOW.
func classify(expected, actual string) (EntryStatus, DriftDirection) {
	exp := strings.ToUpper(strings.TrimSpace(expected))
	act := strings.ToUpper(strings.TrimSpace(actual))

	drift := DriftDirection(policyreplay.CompareDecisions(exp, act))

	if exp == act {
		return StatusPass, drift
	}
	if act == "ALLOW" {
		switch exp {
		case "DENY", "REQUIRE_APPROVAL", "THROTTLE", "ALLOW_WITH_CONSTRAINTS":
			return StatusRegression, drift
		}
	}
	return StatusFail, drift
}

// buildJobRequest projects an EvalEntry's JSON snapshot onto a minimal
// *pb.JobRequest.
func buildJobRequest(e model.EvalEntry) (*pb.JobRequest, error) {
	if len(e.Input) == 0 {
		return nil, fmt.Errorf("entry %s: input is empty", e.ID)
	}
	var snap inputSnapshot
	if err := json.Unmarshal(e.Input, &snap); err != nil {
		return nil, fmt.Errorf("entry %s: parse input snapshot: %w", e.ID, err)
	}

	meta := &pb.JobMetadata{
		TenantId:   strings.TrimSpace(snap.Tenant),
		Capability: strings.TrimSpace(snap.Capability),
		PackId:     strings.TrimSpace(snap.PackID),
		RiskTags:   snap.RiskTags,
		Requires:   snap.Requires,
	}

	req := &pb.JobRequest{
		JobId:       e.ID,
		Topic:       strings.TrimSpace(snap.Topic),
		TenantId:    strings.TrimSpace(snap.Tenant),
		PrincipalId: strings.TrimSpace(snap.PrincipalID),
		Labels:      snap.Labels,
		Meta:        meta,
	}
	return req, nil
}
