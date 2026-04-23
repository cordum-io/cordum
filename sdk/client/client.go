package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal HTTP client for the API gateway.
type Client struct {
	BaseURL string
	// #nosec G117 -- API keys are provided at runtime, not hardcoded.
	APIKey     string
	TenantID   string
	HTTPClient *http.Client
}

// String returns a debug-safe representation that redacts the API key.
func (c *Client) String() string {
	redacted := "(none)"
	if c.APIKey != "" {
		redacted = c.APIKey[:min(4, len(c.APIKey))] + "****"
	}
	return fmt.Sprintf("Client{BaseURL:%s, APIKey:%s, TenantID:%s}", c.BaseURL, redacted, c.TenantID)
}

// TLSOptions controls TLS behaviour for HTTP clients that connect to the
// Cordum gateway. Zero value means "use system defaults".
type TLSOptions struct {
	// CACertPath is the path to a PEM-encoded CA certificate bundle
	// used to verify the server certificate. Empty uses the system pool.
	CACertPath string
	// InsecureSkipVerify disables server certificate verification.
	// Use only for development or testing.
	InsecureSkipVerify bool
}

// New returns a client that uses the system default TLS settings.
func New(baseURL, apiKey string) *Client {
	return NewWithTLS(baseURL, apiKey, TLSOptions{})
}

// NewWithTLS returns a client with explicit TLS configuration.
// TLS configuration errors (invalid CA path, bad PEM) are logged to stderr
// and the client falls back to system defaults. Use [NewWithTLSErr] for
// strict error handling in production.
func NewWithTLS(baseURL, apiKey string, opts TLSOptions) *Client {
	c, err := NewWithTLSErr(baseURL, apiKey, opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cordum sdk: %v (falling back to system TLS defaults)\n", err)
		return newClient(baseURL, apiKey, nil)
	}
	return c
}

// NewWithTLSErr returns a client with explicit TLS configuration and strict
// error handling. Returns an error if the TLS configuration is invalid (e.g.
// CA cert file missing or unparseable).
func NewWithTLSErr(baseURL, apiKey string, opts TLSOptions) (*Client, error) {
	tr, err := BuildTLSTransportErr(opts)
	if err != nil {
		return nil, fmt.Errorf("tls config: %w", err)
	}
	return newClient(baseURL, apiKey, tr), nil
}

func newClient(baseURL, apiKey string, transport *http.Transport) *Client {
	tenantID := strings.TrimSpace(os.Getenv("CORDUM_TENANT_ID"))
	if tenantID == "" {
		tenantID = "default"
	}
	httpClient := &http.Client{Timeout: 15 * time.Second}
	if transport != nil {
		httpClient.Transport = transport
	}
	return &Client{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		TenantID:   tenantID,
		HTTPClient: httpClient,
	}
}

// BuildTLSTransportErr returns an [http.Transport] configured from the given
// options, or (nil, nil) when no TLS customization is needed. It returns an
// error if the CA certificate cannot be read or parsed, preventing a silent
// fall-back to system CAs.
func BuildTLSTransportErr(opts TLSOptions) (*http.Transport, error) {
	if opts.CACertPath == "" && !opts.InsecureSkipVerify {
		return nil, nil
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12} // #nosec G402 -- operator-controlled TLS settings.
	if opts.InsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true // #nosec G402 -- operator opt-in for dev/testing.
	}
	if opts.CACertPath != "" {
		caCert, err := os.ReadFile(opts.CACertPath) // #nosec G304 -- path from operator config.
		if err != nil {
			return nil, fmt.Errorf("read ca cert %s: %w", opts.CACertPath, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("parse ca cert %s: no valid PEM certificates found", opts.CACertPath)
		}
		tlsConfig.RootCAs = pool
	}
	return &http.Transport{TLSClientConfig: tlsConfig}, nil
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
	Input      map[string]any     `json:"input,omitempty"`
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

// TopicRegistration mirrors the canonical topic registry record returned by the gateway.
type TopicRegistration struct {
	Name              string   `json:"name"`
	Pool              string   `json:"pool"`
	InputSchemaID     string   `json:"input_schema_id,omitempty"`
	OutputSchemaID    string   `json:"output_schema_id,omitempty"`
	PackID            string   `json:"pack_id,omitempty"`
	Requires          []string `json:"requires,omitempty"`
	RiskTags          []string `json:"risk_tags,omitempty"`
	Status            string   `json:"status"`
	ActiveWorkerCount int      `json:"active_worker_count,omitempty"`
}

// TopicCreateInput is the request payload for creating or updating a topic registration.
type TopicCreateInput struct {
	Name           string   `json:"name"`
	Pool           string   `json:"pool,omitempty"`
	InputSchemaID  string   `json:"input_schema_id,omitempty"`
	OutputSchemaID string   `json:"output_schema_id,omitempty"`
	PackID         string   `json:"pack_id,omitempty"`
	Requires       []string `json:"requires,omitempty"`
	RiskTags       []string `json:"risk_tags,omitempty"`
	Status         string   `json:"status,omitempty"`
}

// WorkerCredentialSummary mirrors a worker credential record returned by the gateway.
type WorkerCredentialSummary struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools,omitempty"`
	AllowedTopics []string `json:"allowed_topics,omitempty"`
	PackID        string   `json:"pack_id,omitempty"`
	CreatedBy     string   `json:"created_by"`
	CreatedAt     string   `json:"created_at"`
	RevokedAt     string   `json:"revoked_at,omitempty"`
}

// WorkerCredentialInput is the request payload for issuing or rotating a worker credential.
type WorkerCredentialInput struct {
	WorkerID      string   `json:"worker_id"`
	AllowedPools  []string `json:"allowed_pools,omitempty"`
	AllowedTopics []string `json:"allowed_topics,omitempty"`
}

// WorkerCredentialIssued returns the stored credential record plus the plaintext token.
type WorkerCredentialIssued struct {
	WorkerCredentialSummary
	Token string `json:"token"`
}

func (c *Client) endpoint(path string) string {
	base := strings.TrimRight(c.BaseURL, "/")
	return base + path
}

func escapePathSegment(value string) string {
	return url.PathEscape(value)
}

// redactSecrets replaces any occurrence of the client's API key in the given
// string with a redacted placeholder. This prevents credential leakage if a
// server error response echoes back request headers.
func (c *Client) redactSecrets(s string) string {
	if len(c.APIKey) >= 8 && strings.Contains(s, c.APIKey) {
		return strings.ReplaceAll(s, c.APIKey, "[REDACTED]")
	}
	return s
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
		slog.Debug("sdk request body", "method", method, "path", path, "body_bytes", buf.Len(), "body", buf.String())
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
	if tenant := strings.TrimSpace(c.TenantID); tenant != "" && req.Header.Get("X-Tenant-ID") == "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	for k, v := range headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req) // #nosec -- client targets operator-provided gateway URL.
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Limit error body read to 1 MiB to prevent OOM from oversized responses.
		const maxErrBody = 1 << 20
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrBody))
		msg := strings.TrimSpace(string(data))
		if msg == "" {
			msg = resp.Status
		}
		// Redact API key from error body in case the server echoes it back.
		msg = c.redactSecrets(msg)
		if readErr != nil {
			return fmt.Errorf("unexpected status %d: %s (body read error: %w)", resp.StatusCode, msg, readErr)
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

// ExtractEvalDatasetFromIncidents scans decision-log incidents and optionally
// persists the deduplicated result as an immutable eval dataset.
func (c *Client) ExtractEvalDatasetFromIncidents(ctx context.Context, req *ExtractIncidentsRequest) (*ExtractIncidentsResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	if strings.TrimSpace(req.Name) == "" {
		return nil, fmt.Errorf("dataset name required")
	}

	path := "/api/v1/evals/datasets/from-incidents"
	if req.DryRun {
		path += "?dry_run=true"
	}

	var resp ExtractIncidentsResponse
	if err := c.doJSON(ctx, http.MethodPost, path, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
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
	// Guard against nil map: a nil map[string]any assigned to the body
	// parameter (type any) becomes a non-nil interface that encodes to
	// JSON null instead of {}. Normalize to an empty map.
	if input == nil {
		input = map[string]any{}
	}
	path := "/api/v1/workflows/" + escapePathSegment(workflowID) + "/runs"
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

// GetRun fetches a workflow run by ID.
func (c *Client) GetRun(ctx context.Context, runID string) (*WorkflowRun, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id required")
	}
	var run WorkflowRun
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workflow-runs/"+escapePathSegment(runID), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// DeleteRun deletes a workflow run by ID.
func (c *Client) DeleteRun(ctx context.Context, runID string) error {
	if runID == "" {
		return fmt.Errorf("run id required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workflow-runs/"+escapePathSegment(runID), nil, nil)
}

// DeleteWorkflow deletes a workflow by ID.
func (c *Client) DeleteWorkflow(ctx context.Context, workflowID string) error {
	if workflowID == "" {
		return fmt.Errorf("workflow id required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workflows/"+escapePathSegment(workflowID), nil, nil)
}

// GetRunTimeline fetches the run timeline.
func (c *Client) GetRunTimeline(ctx context.Context, runID string) ([]TimelineEvent, error) {
	if runID == "" {
		return nil, fmt.Errorf("run id required")
	}
	var out []TimelineEvent
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workflow-runs/"+escapePathSegment(runID)+"/timeline", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ApproveJob approves or rejects a job awaiting approval.
func (c *Client) ApproveJob(ctx context.Context, jobID string, approved bool) error {
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	path := "/api/v1/approvals/" + escapePathSegment(jobID)
	if approved {
		path += "/approve"
	} else {
		path += "/reject"
	}
	return c.doJSON(ctx, http.MethodPost, path, nil, nil)
}

// RepairApproval inspects or applies a safe repair plan for a stuck approval.
func (c *Client) RepairApproval(ctx context.Context, jobID string, apply bool, note string) (map[string]any, error) {
	if jobID == "" {
		return nil, fmt.Errorf("job id required")
	}
	body := map[string]any{
		"apply": apply,
	}
	if trimmed := strings.TrimSpace(note); trimmed != "" {
		body["note"] = trimmed
	}
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/approvals/"+escapePathSegment(jobID)+"/repair", body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RetryDLQ requeues a job from the DLQ.
func (c *Client) RetryDLQ(ctx context.Context, jobID string) error {
	if jobID == "" {
		return fmt.Errorf("job id required")
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/dlq/"+escapePathSegment(jobID)+"/retry", nil, nil)
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
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/jobs/"+escapePathSegment(jobID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MCPApproval mirrors the gateway's MCPApprovalRecord JSON shape so
// cordumctl and automation tools can decode the list/get responses
// without reaching into internal packages.
type MCPApproval struct {
	ID         string `json:"id"`
	Tenant     string `json:"tenant"`
	AgentID    string `json:"agent_id"`
	ToolName   string `json:"tool_name"`
	ArgsHash   string `json:"args_hash"`
	Requester  string `json:"requester,omitempty"`
	Reason     string `json:"reason,omitempty"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	ResolvedAt int64  `json:"resolved_at,omitempty"`
	ResolvedBy string `json:"resolved_by,omitempty"`
	Decision   string `json:"decision,omitempty"`
	ConsumedAt int64  `json:"consumed_at,omitempty"`
}

// MCPToolInfo describes a tool as returned by GET /api/v1/mcp/tools.
// Only the fields the CLI renders are decoded; the full descriptor
// shape is richer.
type MCPToolInfo struct {
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	RiskTier            string   `json:"riskTier,omitempty"`
	Tags                []string `json:"tags,omitempty"`
	DataClassifications []string `json:"dataClassifications,omitempty"`
	RequiresApproval    bool     `json:"requiresApproval,omitempty"`
}

// MCPToolList is the payload wrapper returned by GET /api/v1/mcp/tools.
type MCPToolList struct {
	Tools    []MCPToolInfo `json:"tools"`
	AgentID  string        `json:"agent_id"`
	Filtered bool          `json:"filtered"`
	Note     string        `json:"note,omitempty"`
}

// ListMCPTools returns the MCP tool catalogue. When agentID is empty
// the gateway returns the unfiltered catalogue (admin-only). When set,
// the gateway returns the subset that agent identity can currently
// see — callers use this to verify scope configuration before rolling
// the identity into production.
func (c *Client) ListMCPTools(ctx context.Context, agentID string) (*MCPToolList, error) {
	path := "/api/v1/mcp/tools"
	if id := strings.TrimSpace(agentID); id != "" {
		path += "?agent_id=" + url.QueryEscape(id)
	}
	var out MCPToolList
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListMCPApprovals returns the pending MCP approvals for the configured
// tenant (narrowed by optional status). Empty status means "all".
func (c *Client) ListMCPApprovals(ctx context.Context, status string) ([]MCPApproval, error) {
	path := "/api/v1/approvals/mcp"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var resp struct {
		Items []MCPApproval `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// GetMCPApproval fetches a single record by ID.
func (c *Client) GetMCPApproval(ctx context.Context, id string) (*MCPApproval, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("approval id required")
	}
	var out MCPApproval
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/approvals/mcp/"+escapePathSegment(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ApproveMCP resolves a pending MCP approval as approved.
func (c *Client) ApproveMCP(ctx context.Context, id, reason string) (*MCPApproval, error) {
	return c.resolveMCP(ctx, id, "approve", reason)
}

// RejectMCP resolves a pending MCP approval as rejected.
func (c *Client) RejectMCP(ctx context.Context, id, reason string) (*MCPApproval, error) {
	return c.resolveMCP(ctx, id, "reject", reason)
}

func (c *Client) resolveMCP(ctx context.Context, id, verb, reason string) (*MCPApproval, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("approval id required")
	}
	body := map[string]any{"reason": strings.TrimSpace(reason)}
	var out MCPApproval
	path := "/api/v1/approvals/mcp/" + escapePathSegment(id) + "/" + verb
	if err := c.doJSON(ctx, http.MethodPost, path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetStatus fetches the gateway status snapshot.
func (c *Client) GetStatus(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/status", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListTopics fetches canonical topic registrations from the gateway.
func (c *Client) ListTopics(ctx context.Context) ([]TopicRegistration, error) {
	var resp struct {
		Items []TopicRegistration `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/topics", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// CreateTopic creates or updates a canonical topic registration.
func (c *Client) CreateTopic(ctx context.Context, input TopicCreateInput) error {
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("topic name required")
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/topics", input, nil)
}

// DeleteTopic removes a topic registration by name.
func (c *Client) DeleteTopic(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("topic name required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/topics/"+escapePathSegment(name), nil, nil)
}

// ListWorkerCredentials fetches issued worker credentials from the gateway.
func (c *Client) ListWorkerCredentials(ctx context.Context) ([]WorkerCredentialSummary, error) {
	var resp struct {
		Items []WorkerCredentialSummary `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/workers/credentials", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

// CreateWorkerCredential creates or rotates a worker credential and returns the plaintext token.
func (c *Client) CreateWorkerCredential(ctx context.Context, input WorkerCredentialInput) (*WorkerCredentialIssued, error) {
	if strings.TrimSpace(input.WorkerID) == "" {
		return nil, fmt.Errorf("worker id required")
	}
	var issued WorkerCredentialIssued
	if err := c.doJSON(ctx, http.MethodPost, "/api/v1/workers/credentials", input, &issued); err != nil {
		return nil, err
	}
	return &issued, nil
}

// RevokeWorkerCredential revokes a worker credential by worker ID.
func (c *Client) RevokeWorkerCredential(ctx context.Context, workerID string) error {
	if strings.TrimSpace(workerID) == "" {
		return fmt.Errorf("worker id required")
	}
	return c.doJSON(ctx, http.MethodDelete, "/api/v1/workers/credentials/"+escapePathSegment(workerID), nil, nil)
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
	if err := c.doJSON(ctx, http.MethodGet, "/api/v1/artifacts/"+escapePathSegment(ptr), nil, &resp); err != nil {
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

// ---------------------------------------------------------------------------
// Pool management
// ---------------------------------------------------------------------------

type PoolItem struct {
	Name        string  `json:"name"`
	Workers     int     `json:"workers"`
	ActiveJobs  int32   `json:"active_jobs"`
	Capacity    int32   `json:"capacity"`
	Utilization float64 `json:"utilization"`
}

type PoolDetail struct {
	PoolItem
	Status      string   `json:"status"`
	Description string   `json:"description"`
	Requires    []string `json:"requires"`
	Topics      []string `json:"topics"`
	WorkerList  []any    `json:"worker_list"`
	CapturedAt  string   `json:"captured_at"`
}

type PoolCreateRequest struct {
	Requires    []string `json:"requires,omitempty"`
	Description string   `json:"description,omitempty"`
}

type PoolUpdateRequest struct {
	Requires    *[]string `json:"requires,omitempty"`
	Description *string   `json:"description,omitempty"`
	Status      *string   `json:"status,omitempty"`
}

type PoolDrainRequest struct {
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

func (c *Client) ListPools(ctx context.Context) ([]PoolItem, error) {
	var resp struct {
		Items []PoolItem `json:"items"`
	}
	if err := c.doJSON(ctx, "GET", "/api/v1/pools", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetPool(ctx context.Context, name string) (*PoolDetail, error) {
	var pool PoolDetail
	if err := c.doJSON(ctx, "GET", "/api/v1/pools/"+escapePathSegment(name), nil, &pool); err != nil {
		return nil, err
	}
	return &pool, nil
}

func (c *Client) CreatePool(ctx context.Context, name string, req *PoolCreateRequest) error {
	return c.doJSON(ctx, "PUT", "/api/v1/pools/"+escapePathSegment(name), req, nil)
}

func (c *Client) UpdatePool(ctx context.Context, name string, req *PoolUpdateRequest) error {
	return c.doJSON(ctx, "PATCH", "/api/v1/pools/"+escapePathSegment(name), req, nil)
}

func (c *Client) DeletePool(ctx context.Context, name string, force bool) error {
	path := "/api/v1/pools/" + escapePathSegment(name)
	if force {
		path += "?force=true"
	}
	return c.doJSON(ctx, "DELETE", path, nil, nil)
}

func (c *Client) DrainPool(ctx context.Context, name string, req *PoolDrainRequest) error {
	return c.doJSON(ctx, "POST", "/api/v1/pools/"+escapePathSegment(name)+"/drain", req, nil)
}

func (c *Client) AddTopicToPool(ctx context.Context, pool, topic string) error {
	return c.doJSON(ctx, "PUT", "/api/v1/pools/"+escapePathSegment(pool)+"/topics/"+escapePathSegment(topic), nil, nil)
}

func (c *Client) RemoveTopicFromPool(ctx context.Context, pool, topic string) error {
	return c.doJSON(ctx, "DELETE", "/api/v1/pools/"+escapePathSegment(pool)+"/topics/"+escapePathSegment(topic), nil, nil)
}

// AuditVerifyGap mirrors the gateway's audit.VerifyGap response field.
// Kept as a plain struct so the SDK does not depend on core/audit.
type AuditVerifyGap struct {
	AtSeq int64  `json:"at_seq"`
	Type  string `json:"type"`
}

// AuditVerifyResult mirrors the gateway's audit.VerifyResult. The SDK
// re-declares the shape rather than importing core/audit to keep the
// client module dependency-free for third-party consumers.
type AuditVerifyResult struct {
	Status               string           `json:"status"`
	TotalEvents          int              `json:"total_events"`
	VerifiedEvents       int              `json:"verified_events"`
	Gaps                 []AuditVerifyGap `json:"gaps"`
	RetentionBoundarySeq int64            `json:"retention_boundary_seq"`
	RetentionWindowHours float64          `json:"retention_window_hours,omitempty"`
	FirstSeq             int64            `json:"first_seq,omitempty"`
	LastSeq              int64            `json:"last_seq,omitempty"`
}

// AuditVerifyOptions narrows a verify call. All fields are optional.
type AuditVerifyOptions struct {
	SinceMs int64
	UntilMs int64
	Limit   int64
}

// VerifyAuditChain calls GET /api/v1/audit/verify for the given tenant.
// tenant=="" uses the client's default tenant (sent via X-Tenant-ID).
// The gateway walks the tenant's audit chain and returns an integrity
// report — see AuditVerifyResult for the shape.
func (c *Client) VerifyAuditChain(ctx context.Context, tenant string, opts AuditVerifyOptions) (*AuditVerifyResult, error) {
	q := url.Values{}
	if t := strings.TrimSpace(tenant); t != "" {
		q.Set("tenant", t)
	}
	if opts.SinceMs > 0 {
		q.Set("since", strconv.FormatInt(opts.SinceMs, 10))
	}
	if opts.UntilMs > 0 {
		q.Set("until", strconv.FormatInt(opts.UntilMs, 10))
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.FormatInt(opts.Limit, 10))
	}
	path := "/api/v1/audit/verify"
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}
	var out AuditVerifyResult
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
