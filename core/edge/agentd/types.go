package agentd

import (
	"context"
	"errors"
	"net/http"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
)

const (
	// MaxGatewayMetadataValueBytes bounds each local metadata string before it
	// is sent to the Gateway. Large/raw agent payloads belong in artifacts, not
	// EdgeSession metadata.
	MaxGatewayMetadataValueBytes = 2048

	defaultAgentdHookPath = "/v1/edge/hooks/claude"
	defaultAgentdBindURL  = "http://127.0.0.1:8765" + defaultAgentdHookPath
	defaultHookTimeout    = 5 * time.Second
	defaultHeartbeatTTL   = 30 * time.Second
	defaultGatewayTimeout = 10 * time.Second
	maxAgentdDuration     = 5 * time.Minute
)

var (
	ErrGatewayTimeout = errors.New("agentd gateway timeout")
	ErrFailClosed     = errors.New("agentd fail closed")
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type GatewayClientConfig struct {
	BaseURL    string
	APIKey     string
	TenantID   string
	Timeout    time.Duration
	HTTPClient httpDoer
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type CreateSessionRequest struct {
	TenantID          string                     `json:"tenant_id"`
	PrincipalID       string                     `json:"principal_id"`
	PrincipalType     edgecore.PrincipalType     `json:"principal_type"`
	AgentProduct      string                     `json:"agent_product"`
	AgentVersion      string                     `json:"agent_version"`
	Mode              edgecore.SessionMode       `json:"mode"`
	Repo              string                     `json:"repo"`
	GitRemote         string                     `json:"git_remote"`
	GitBranch         string                     `json:"git_branch"`
	GitSHA            string                     `json:"git_sha"`
	CWD               string                     `json:"cwd"`
	HostID            string                     `json:"host_id"`
	DeviceID          string                     `json:"device_id"`
	TraceID           string                     `json:"trace_id,omitempty"`
	WorkflowRunID     string                     `json:"workflow_run_id,omitempty"`
	JobID             string                     `json:"job_id,omitempty"`
	PolicySnapshot    string                     `json:"policy_snapshot"`
	EnforcementLayers edgecore.EnforcementLayers `json:"enforcement_layers"`
	PolicyMode        edgecore.PolicyMode        `json:"policy_mode"`
	Labels            edgecore.Labels            `json:"labels"`
}

type CreateSessionResponse struct {
	SessionID      string                  `json:"session_id"`
	ExecutionID    string                  `json:"execution_id"`
	TraceID        string                  `json:"trace_id"`
	PolicySnapshot string                  `json:"policy_snapshot"`
	DashboardURL   string                  `json:"dashboard_url"`
	Session        edgecore.EdgeSession    `json:"session"`
	Execution      edgecore.AgentExecution `json:"execution"`
}

type HeartbeatResponse struct {
	SessionID      string `json:"session_id"`
	HeartbeatAlive bool   `json:"heartbeat_alive"`
}

type EndExecutionRequest struct {
	Status  edgecore.ExecutionStatus `json:"status"`
	EndedAt *time.Time               `json:"ended_at,omitempty"`
}

type EndSessionRequest struct {
	Status  edgecore.SessionStatus `json:"status"`
	EndedAt *time.Time             `json:"ended_at,omitempty"`
}

type LocalSessionMetadata struct {
	TenantID      string
	PrincipalID   string
	PrincipalType edgecore.PrincipalType
	AgentProduct  string
	AgentVersion  string
	Mode          edgecore.SessionMode
	Repo          string
	GitRemote     string
	GitBranch     string
	GitSHA        string
	CWD           string
	HostID        string
	DeviceID      string
	Labels        edgecore.Labels
}

type SessionState struct {
	SessionID         string                 `json:"session_id"`
	ExecutionID       string                 `json:"execution_id"`
	TraceID           string                 `json:"trace_id"`
	TenantID          string                 `json:"tenant_id"`
	PrincipalID       string                 `json:"principal_id"`
	PolicySnapshot    string                 `json:"policy_snapshot"`
	DashboardURL      string                 `json:"dashboard_url"`
	PolicyMode        edgecore.PolicyMode    `json:"policy_mode"`
	Status            edgecore.SessionStatus `json:"status"`
	SocketPath        string                 `json:"socket_path,omitempty"`
	StartedAt         time.Time              `json:"started_at"`
	EndedAt           *time.Time             `json:"ended_at,omitempty"`
	DegradedReason    string                 `json:"degraded_reason,omitempty"`
	FailClosed        bool                   `json:"fail_closed,omitempty"`
	PendingGatewayEnd bool                   `json:"pending_gateway_end,omitempty"`
	Metadata          map[string]string      `json:"metadata,omitempty"`

	TransientSecrets map[string]string `json:"-"`
}

type ShutdownOptions struct {
	ExecutionStatus edgecore.ExecutionStatus
	SessionStatus   edgecore.SessionStatus
	Reason          string
}

type GatewayLifecycleClient interface {
	CreateSession(context.Context, CreateSessionRequest) (CreateSessionResponse, error)
	EndExecution(context.Context, string, EndExecutionRequest) error
	EndSession(context.Context, string, EndSessionRequest) error
}

type HeartbeatClient interface {
	Heartbeat(context.Context, string) (HeartbeatResponse, error)
}

type SessionDegradedWriter interface {
	MarkSessionDegraded(context.Context, SessionState, string) (edgecore.AgentActionEvent, error)
}
