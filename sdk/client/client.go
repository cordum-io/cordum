package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal HTTP client for the API gateway.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

// New returns a client with a default HTTP timeout.
func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Step is a generic workflow step payload.
type Step map[string]any

// CreateWorkflowRequest mirrors the gateway create payload.
type CreateWorkflowRequest struct {
	ID          string           `json:"id,omitempty"`
	OrgID       string           `json:"org_id,omitempty"`
	TeamID      string           `json:"team_id,omitempty"`
	Name        string           `json:"name,omitempty"`
	Description string           `json:"description,omitempty"`
	Version     string           `json:"version,omitempty"`
	TimeoutSec  int64            `json:"timeout_sec,omitempty"`
	CreatedBy   string           `json:"created_by,omitempty"`
	InputSchema map[string]any   `json:"input_schema,omitempty"`
	Parameters  []map[string]any `json:"parameters,omitempty"`
	Steps       map[string]Step  `json:"steps"`
	Config      map[string]any   `json:"config,omitempty"`
}

// WorkflowRun captures minimal run fields from the gateway response.
type WorkflowRun struct {
	ID         string             `json:"id"`
	WorkflowID string             `json:"workflow_id"`
	Status     string             `json:"status"`
	Steps      map[string]StepRun `json:"steps,omitempty"`
	UpdatedAt  string             `json:"updated_at,omitempty"`
	Metadata   map[string]string  `json:"metadata,omitempty"`
	Labels     map[string]string  `json:"labels,omitempty"`
	Context    map[string]any     `json:"context,omitempty"`
	Output     map[string]any     `json:"output,omitempty"`
	Error      map[string]any     `json:"error,omitempty"`
}

// TimelineEvent captures a run timeline entry.
type TimelineEvent struct {
	Time       string         `json:"time"`
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

// ArtifactMetadata mirrors artifact store metadata.
type ArtifactMetadata struct {
	ContentType string            `json:"content_type,omitempty"`
	SizeBytes   int64             `json:"size_bytes,omitempty"`
	Retention   string            `json:"retention,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// Artifact captures stored artifact data.
type Artifact struct {
	Pointer  string           `json:"artifact_ptr"`
	Content  []byte           `json:"-"`
	Metadata ArtifactMetadata `json:"metadata"`
}

// StepRun captures minimal step status details.
type StepRun struct {
	StepID string `json:"step_id"`
	Status string `json:"status"`
	JobID  string `json:"job_id,omitempty"`
}

// RunOptions configures workflow run creation.
type RunOptions struct {
	DryRun         bool
	IdempotencyKey string
}

// JobSubmitRequest mirrors the gateway submit job payload.
type JobSubmitRequest struct {
	Prompt         string            `json:"prompt"`
	Topic          string            `json:"topic"`
	Context        any               `json:"context,omitempty"`
	OrgID          string            `json:"org_id,omitempty"`
	TenantID       string            `json:"tenant_id,omitempty"`
	PrincipalID    string            `json:"principal_id,omitempty"`
	ActorID        string            `json:"actor_id,omitempty"`
	ActorType      string            `json:"actor_type,omitempty"`
	IdempotencyKey string            `json:"idempotency_key,omitempty"`
	PackID         string            `json:"pack_id,omitempty"`
	Capability     string            `json:"capability,omitempty"`
	RiskTags       []string          `json:"risk_tags,omitempty"`
	Requires       []string          `json:"requires,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// JobSubmitResponse captures the job submit response.
type JobSubmitResponse struct {
	JobID   string `json:"job_id"`
	TraceID string `json:"trace_id,omitempty"`
}

func (c *Client) endpoint(path string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	return base + path
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	return c.doJSONWithHeaders(ctx, method, path, body, out, nil)
}

func (c *Client) doJSONWithHeaders(ctx context.Context, method, path string, body any, out any, headers map[string]string) error {
	var payload io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}
		payload = buf
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), payload)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
	for k, v := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, msg)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

// CreateWorkflow creates or upserts a workflow and returns its ID.
func (c *Client) CreateWorkflow(ctx context.Context, req *CreateWorkflowRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("request is nil")
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/workflows", req, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}

// StartRun starts a workflow run and returns the run ID.
func (c *Client) StartRun(ctx context.Context, workflowID string, input map[string]any) (string, error) {
	return c.StartRunWithOptions(ctx, workflowID, input, RunOptions{})
}

// StartRunWithDryRun starts a workflow run with optional dry-run mode.
func (c *Client) StartRunWithDryRun(ctx context.Context, workflowID string, input map[string]any, dryRun bool) (string, error) {
	return c.StartRunWithOptions(ctx, workflowID, input, RunOptions{DryRun: dryRun})
}

// StartRunWithOptions starts a workflow run with additional options (dry-run/idempotency).
func (c *Client) StartRunWithOptions(ctx context.Context, workflowID string, input map[string]any, opts RunOptions) (string, error) {
	if workflowID == "" {
		return "", fmt.Errorf("workflow id required")
	}
	path := "/api/v1/workflows/" + workflowID + "/runs"
	if opts.DryRun {
		path += "?dry_run=true"
	}
	var resp struct {
		RunID string `json:"run_id"`
	}
	headers := map[string]string{}
	if opts.IdempotencyKey != "" {
		headers["Idempotency-Key"] = opts.IdempotencyKey
	}
	if err := c.doJSONWithHeaders(ctx, http.MethodPost, path, input, &resp, headers); err != nil {
		return "", err
	}
	return resp.RunID, nil
}

// ApproveStep approves or rejects a waiting approval step.
func (c *Client) ApproveStep(ctx context.Context, workflowID, runID, stepID string, approved bool) error {
	if workflowID == "" || runID == "" || stepID == "" {
		return fmt.Errorf("workflow id, run id, and step id are required")
	}
	body := map[string]bool{"approved": approved}
	path := "/api/v1/workflows/" + workflowID + "/runs/" + runID + "/steps/" + stepID + "/approve"
	return c.doJSON(ctx, http.MethodPost, path, body, nil)
}

// GetRun fetches a workflow run by ID.
func (c *Client) GetRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id required")
	}
	var run WorkflowRun
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workflow-runs/"+runID, nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// DeleteRun deletes a workflow run by ID.
func (c *Client) DeleteRun(ctx context.Context, runID string) error {
	if runID == "" {
		return fmt.Errorf("run id required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workflow-runs/"+runID, nil, nil)
}

// DeleteWorkflow deletes a workflow by ID.
func (c *Client) DeleteWorkflow(ctx context.Context, workflowID string) error {
	if workflowID == "" {
		return fmt.Errorf("workflow id required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workflows/"+workflowID, nil, nil)
}

// GetRunTimeline fetches the run timeline.
func (c *Client) GetRunTimeline(ctx context.Context, runID string) ([]TimelineEvent, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id required")
	}
	var out []TimelineEvent
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workflow-runs/"+runID+"/timeline", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApproveJob approves or rejects a job awaiting approval.
func (c *Client) ApproveJob(ctx context.Context, jobID string, approved bool) error {
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	path := "/api/v1/approvals/" + jobID
	if approved {
		path += "/approve"
	} else {
		path += "/reject"
	}
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// RetryDLQ requeues a job from the DLQ.
func (c *Client) RetryDLQ(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/dlq/"+jobID+"/retry", nil, nil)
}

// SubmitJob submits a new job and returns IDs.
func (c *Client) SubmitJob(ctx context.Context, req *JobSubmitRequest) (*JobSubmitResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	var resp JobSubmitResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/jobs", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetJob fetches a job record by ID.
func (c *Client) GetJob(ctx context.Context, jobID string) (map[string]any, error) {
	if jobID == "" {
		return nil, fmt.Errorf("job id required")
	}
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/jobs/"+jobID, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetStatus fetches the gateway status snapshot.
func (c *Client) GetStatus(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/status", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// PutArtifact uploads content to the artifact store.
func (c *Client) PutArtifact(ctx context.Context, content []byte, meta ArtifactMetadata, maxBytes int64) (string, error) {
	if len(content) == 0 {
		return "", fmt.Errorf("content required")
	}
	body := map[string]any{
		"content_base64": base64.StdEncoding.EncodeToString(content),
		"content_type":   meta.ContentType,
		"retention":      meta.Retention,
		"labels":         meta.Labels,
	}
	path := "/api/v1/artifacts"
	if maxBytes > 0 {
		path += "?max_bytes=" + fmt.Sprint(maxBytes)
	}
	var resp struct {
		Pointer string `json:"artifact_ptr"`
	}
	if err := c.doJSON(ctx, http.MethodPost, path, body, &resp); err != nil {
		return "", err
	}
	return resp.Pointer, nil
}

// GetArtifact fetches an artifact by pointer.
func (c *Client) GetArtifact(ctx context.Context, ptr string) (*Artifact, error) {
	if ptr == "" {
		return nil, fmt.Errorf("artifact pointer required")
	}
	var resp struct {
		Pointer  string           `json:"artifact_ptr"`
		Content  string           `json:"content_base64"`
		Metadata ArtifactMetadata `json:"metadata"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/artifacts/"+ptr, nil, &resp); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		return nil, err
	}
	return &Artifact{
		Pointer:  resp.Pointer,
		Content:  data,
		Metadata: resp.Metadata,
	}, nil
}
