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

	// defaultLocalServerWriteTimeout caps the agentd local HTTP server's
	// connection-write phase so a slow-reading or hanging Claude-hook client
	// cannot pin a handler goroutine indefinitely. EDGE-059 closed an
	// unbounded-write DoS vector at app.go:300-303 (only ReadHeaderTimeout
	// was set; WriteTimeout==0 == infinite). 2s gives ~8x margin over
	// defaultHookResponseWriteBudget (250ms) for loopback CPU-pressure
	// spikes while staying 2.5x under defaultHookTimeout (5s) so the agentd
	// write completes inside the hook's outer budget. Decision payloads are
	// small (bounded reason ~500B; total response < 4KB) — 2s is generous.
	defaultLocalServerWriteTimeout = 2 * time.Second

	// defaultLocalServerIdleTimeout caps lurking-but-idle Keep-Alive
	// connections so a malicious client cannot reserve hundreds of TCP
	// slots without sending traffic. agentd's hook is request/response —
	// no streaming or long-poll — so 30s is comfortably above any
	// legitimate inter-request gap on a developer laptop.
	defaultLocalServerIdleTimeout = 30 * time.Second
)

var (
	ErrGatewayTimeout            = errors.New("agentd gateway timeout")
	ErrEvaluateResponseMalformed = errors.New("agentd evaluate response malformed")
	ErrFailClosed                = errors.New("agentd fail closed")
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }

type GatewayClientConfig struct {
	BaseURL  string
	APIKey   string
	TenantID string
	// PrincipalID is the principal identifier this agentd instance binds
	// outbound Gateway requests to. The Gateway's basic auth provider reads
	// X-Principal-Id when API-key auth alone leaves the principal blank
	// (core/controlplane/gateway/auth/basic.go HeaderValue), and EDGE-008.7
	// hardening on resolveEdgeAuthPrincipal refuses to read principal_id
	// from the JSON body. Empty means agentd will not send the header,
	// matching pre-EDGE-039 behavior.
	PrincipalID string
	Timeout     time.Duration
	HTTPClient  httpDoer
	// TLSCAFile, when non-empty, points to a PEM-encoded CA bundle that
	// will be used to validate the Gateway's TLS certificate. Required on
	// Windows when the Gateway uses a locally-issued CA: Go's HTTP client
	// uses the Windows certificate store and ignores SSL_CERT_FILE.
	// On Linux/macOS SSL_CERT_FILE works as a fallback but TLSCAFile is
	// the explicit, cross-platform way to wire it in. Has no effect when
	// HTTPClient is supplied (caller controls TLS in that case).
	TLSCAFile string
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
	SessionID                string                  `json:"session_id"`
	ExecutionID              string                  `json:"execution_id"`
	TraceID                  string                  `json:"trace_id"`
	PolicySnapshot           string                  `json:"policy_snapshot"`
	WorkflowOverrideSnapshot string                  `json:"workflow_override_snapshot,omitempty"`
	JobOverrideSnapshot      string                  `json:"job_override_snapshot,omitempty"`
	DashboardURL             string                  `json:"dashboard_url"`
	Session                  edgecore.EdgeSession    `json:"session"`
	Execution                edgecore.AgentExecution `json:"execution"`
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
	SessionID                string                 `json:"session_id"`
	ExecutionID              string                 `json:"execution_id"`
	TraceID                  string                 `json:"trace_id"`
	TenantID                 string                 `json:"tenant_id"`
	PrincipalID              string                 `json:"principal_id"`
	PolicySnapshot           string                 `json:"policy_snapshot"`
	WorkflowOverrideSnapshot string                 `json:"workflow_override_snapshot,omitempty"`
	JobOverrideSnapshot      string                 `json:"job_override_snapshot,omitempty"`
	DashboardURL             string                 `json:"dashboard_url"`
	PolicyMode               edgecore.PolicyMode    `json:"policy_mode"`
	Status                   edgecore.SessionStatus `json:"status"`
	SocketPath               string                 `json:"socket_path,omitempty"`
	StartedAt                time.Time              `json:"started_at"`
	EndedAt                  *time.Time             `json:"ended_at,omitempty"`
	DegradedReason           string                 `json:"degraded_reason,omitempty"`
	FailClosed               bool                   `json:"fail_closed,omitempty"`
	PendingGatewayEnd        bool                   `json:"pending_gateway_end,omitempty"`
	Metadata                 map[string]string      `json:"metadata,omitempty"`

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
