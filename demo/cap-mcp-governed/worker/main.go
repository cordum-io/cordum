// Package main is the cap-mcp-governed demo worker.
//
// It is a CAP v2.17.0 worker that, for every job, opens an MCP session
// against a Cordum gateway, reads its own job resource
// (cordum://jobs/<job_id>), calls the read-only cordum_status tool and then
// requests the governed cordum_create_workflow mutation. When the gateway
// answers JSON-RPC -32099 (approval required) the worker polls the approval
// record until a human operator resolves it, then retries the byte-identical
// arguments exactly once. The worker never approves or rejects anything.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/cordum/cordum/sdk/runtime"
)

const (
	workerID      = "cap-mcp-governed"
	workerPool    = "cap-mcp-governed"
	topic         = "job.cap-mcp-governed.create-workflow"
	directSubject = "worker." + workerID + ".jobs"
	capability    = "cap-mcp-governed.create-workflow"

	mcpProtocolVersion = "2024-11-05"
	callTimeout        = 10 * time.Second
	maxResponseBytes   = 1 << 20
	maxArgsBytes       = 64 << 10
)

// Error codes. A failed job's error_message is always "<code>: <fixed
// sentence>"; CAP v2.17.0 JobResult has no structured code field.
const (
	codeInvalidInput     = "invalid_input"
	codeMCPUnavailable   = "mcp_unavailable"
	codeMCPProtocolError = "mcp_protocol_error"
	codeMCPCallFailed    = "mcp_call_failed"
	codeApprovalRejected = "mcp_approval_rejected"
	codeApprovalExpired  = "mcp_approval_expired"
	codeApprovalNotFound = "mcp_approval_not_found"
	codeApprovalTimeout  = "mcp_approval_timeout"
	codeApprovalInvalid  = "mcp_approval_invalid"
	codeWorkerShutdown   = "worker_shutdown"
)

// jobError is the only error type handle returns. msg is a fixed sentence;
// it never carries server text, response bodies or credentials.
type jobError struct{ code, msg string }

func (e jobError) Error() string { return e.code + ": " + e.msg }

// workflowSpec is the workflow the job asks the gateway to create.
type workflowSpec struct {
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	Steps       map[string]json.RawMessage `json:"steps"`
}

// jobInput accepts exactly one of the direct form {"workflow":{...}} or the
// gateway REST envelope {"context":{"workflow":{...}}, ...}.
type jobInput struct {
	Workflow *workflowSpec `json:"workflow"`
	Context  *struct {
		Workflow *workflowSpec `json:"workflow"`
	} `json:"context"`
}

type jobOutput struct {
	WorkflowID string `json:"workflow_id"`
	ApprovalID string `json:"approval_id,omitempty"`
}

type config struct {
	GatewayURL   *url.URL
	APIKey       string
	TenantID     string
	Client       *http.Client
	JobTimeout   time.Duration
	PollInterval time.Duration
}

type worker struct {
	cfg  config
	base context.Context
}

func (w *worker) handle(_ runtime.Context, _ jobInput) (jobOutput, error) {
	return jobOutput{}, jobError{codeMCPCallFailed, "not implemented"}
}

// newAgent builds the unsigned (compat-profile) CAP agent and registers the
// handler on the pool topic and on the worker's direct subject, which is
// where Cordum's scheduler dispatches. Retries are 0: a runtime retry would
// enqueue a new approval. HandshakeMode stays empty so CORDUM_SDK_HANDSHAKE
// applies.
func newAgent(w *worker, natsURL string, nc runtime.NATSConn, store runtime.BlobStore) *runtime.Agent {
	agent := runtime.NewLegacyUnsignedAgent(natsURL, "", workerID, nc, store)
	runtime.Register(agent, topic, w.handle, runtime.WithRetries(0))
	runtime.Register(agent, directSubject, w.handle, runtime.WithRetries(0))
	return agent
}

func loadConfig(_ func(string) string) (config, error) {
	return config{}, errors.New("not implemented")
}

func newHTTPClient(_ string) (*http.Client, error) {
	return nil, errors.New("not implemented")
}

func buildHeartbeat() ([]byte, error) {
	return nil, errors.New("not implemented")
}

func main() {}
