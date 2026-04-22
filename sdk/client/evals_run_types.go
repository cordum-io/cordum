package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// EvalRunRequest is the body for POST /api/v1/evals/datasets/{id}/run.
type EvalRunRequest struct {
	UseCurrentPolicy  bool   `json:"use_current_policy,omitempty"`
	CandidateBundleID string `json:"candidate_bundle_id,omitempty"`
	CandidateContent  string `json:"candidate_content,omitempty"`
	MaxEntries        int    `json:"max_entries,omitempty"`
}

// EvalRunSummary mirrors the gateway's runner summary.
type EvalRunSummary struct {
	Total        int      `json:"total"`
	Passed       int      `json:"passed"`
	Failed       int      `json:"failed"`
	Regressions  int      `json:"regressions"`
	Errored      int      `json:"errored"`
	ScorePercent *float64 `json:"score_percent,omitempty"`
}

// EvalEntryResult mirrors one evaluated dataset entry.
type EvalEntryResult struct {
	EntryID          string          `json:"entry_id"`
	Input            json.RawMessage `json:"input,omitempty"`
	ExpectedDecision string          `json:"expected_decision,omitempty"`
	ActualDecision   string          `json:"actual_decision,omitempty"`
	RuleID           string          `json:"rule_id,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	Status           string          `json:"status,omitempty"`
	DriftDirection   string          `json:"drift_direction,omitempty"`
	Error            string          `json:"error,omitempty"`
}

// EvalRunResponse is a union-like decode target for sync completion
// responses (200), async accepted/pending responses (202), and failed
// async status responses.
type EvalRunResponse struct {
	RunID          string            `json:"run_id"`
	Status         string            `json:"status,omitempty"`
	PollURL        string            `json:"poll_url,omitempty"`
	DatasetID      string            `json:"dataset_id,omitempty"`
	DatasetName    string            `json:"dataset_name,omitempty"`
	DatasetVersion int               `json:"dataset_version,omitempty"`
	Tenant         string            `json:"tenant,omitempty"`
	PolicySnapshot string            `json:"policy_snapshot,omitempty"`
	StartedAt      string            `json:"started_at,omitempty"`
	CompletedAt    string            `json:"completed_at,omitempty"`
	Summary        *EvalRunSummary   `json:"summary,omitempty"`
	Entries        []EvalEntryResult `json:"entries,omitempty"`
	Error          string            `json:"error,omitempty"`
}

// Pending reports whether the gateway still considers the run in flight.
func (r *EvalRunResponse) Pending() bool {
	if r == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.Status)) {
	case "pending", "running":
		return true
	default:
		return false
	}
}

// Failed reports a terminal async failure.
func (r *EvalRunResponse) Failed() bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Status), "failed")
}

// EvalRunListOptions narrows GET /api/v1/evals/datasets/{id}/runs.
type EvalRunListOptions struct {
	Cursor           string
	Limit            int
	HasRegression    bool
	HasRegressionSet bool
}

// EvalRunListResponse mirrors the gateway list payload.
type EvalRunListResponse struct {
	Items      []EvalRunResponse `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// RunEvalDataset starts an evaluation run. Small datasets return a
// completed result (200) while larger datasets return a pending record
// (202) that can be polled via GetEvalRun.
func (c *Client) RunEvalDataset(ctx context.Context, datasetID string, req *EvalRunRequest) (*EvalRunResponse, error) {
	if strings.TrimSpace(datasetID) == "" {
		return nil, fmt.Errorf("dataset id required")
	}
	var out EvalRunResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/evals/datasets/"+escapePathSegment(datasetID)+"/run", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetEvalRun polls one eval run by ID.
func (c *Client) GetEvalRun(ctx context.Context, runID string) (*EvalRunResponse, error) {
	if strings.TrimSpace(runID) == "" {
		return nil, fmt.Errorf("run id required")
	}
	var out EvalRunResponse
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/evals/runs/"+escapePathSegment(runID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListEvalRuns lists historical runs for one dataset.
func (c *Client) ListEvalRuns(ctx context.Context, datasetID string, opts EvalRunListOptions) (*EvalRunListResponse, error) {
	if strings.TrimSpace(datasetID) == "" {
		return nil, fmt.Errorf("dataset id required")
	}
	q := url.Values{}
	if cursor := strings.TrimSpace(opts.Cursor); cursor != "" {
		q.Set("cursor", cursor)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.HasRegressionSet {
		q.Set("has_regression", strconv.FormatBool(opts.HasRegression))
	}
	path := "/api/v1/evals/datasets/" + escapePathSegment(datasetID) + "/runs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out EvalRunListResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEvalRun force-deletes an eval run from history.
func (c *Client) DeleteEvalRun(ctx context.Context, runID string, force bool) error {
	if strings.TrimSpace(runID) == "" {
		return fmt.Errorf("run id required")
	}
	path := "/api/v1/evals/runs/" + escapePathSegment(runID)
	if force {
		path += "?force=true"
	}
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}
