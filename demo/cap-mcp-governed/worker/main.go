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
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	codeApprovalNeeded = -32099
	shutdownBudget     = 15 * time.Second
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

var (
	errUnavailable = jobError{codeMCPUnavailable, "the gateway is unreachable or returned a server error"}
	errRedirect    = jobError{codeMCPProtocolError, "the gateway answered with a redirect, which is refused"}
	errMalformed   = jobError{codeMCPProtocolError, "the gateway response breaks the JSON-RPC or MCP contract"}
	errRefused     = jobError{codeMCPCallFailed, "the gateway refused the request"}
	errShutdown    = jobError{codeWorkerShutdown, "the worker is shutting down"}
	errTimeout     = jobError{codeApprovalTimeout, "no approval decision arrived before the job deadline"}
	errBadApproval = jobError{codeApprovalInvalid, "the approval record has an unexpected status"}

	// safeID bounds job and approval ids; both end up in a URI or URL path.
	safeID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// isSafeID also refuses all-dot ids such as "..", which a URL path cleans.
func isSafeID(id string) bool { return safeID.MatchString(id) && strings.Trim(id, ".") != "" }

// workflowSpec is the workflow the job asks the gateway to create. Steps
// stay raw so their values reach the gateway verbatim.
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

type createWorkflowArgs struct {
	Name           string                     `json:"name"`
	Description    string                     `json:"description,omitempty"`
	Steps          map[string]json.RawMessage `json:"steps"`
	IdempotencyKey string                     `json:"idempotency_key"`
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

// handle runs one job: validate, open the session, read, status, create,
// and on -32099 wait for a human decision and retry the same bytes once.
func (w *worker) handle(rc runtime.Context, in jobInput) (jobOutput, error) {
	jobID := rc.Job.GetJobId()
	args, err := buildArgs(jobID, in)
	if err != nil {
		return jobOutput{}, err
	}
	ctx, cancel := context.WithTimeout(w.base, w.cfg.JobTimeout)
	defer cancel()
	s := &session{cfg: w.cfg, base: w.base, ctx: ctx, id: rand.Text()}
	if err := s.open(jobID); err != nil {
		return jobOutput{}, err
	}
	workflowID, approvalID, err := s.create(args, false)
	if err != nil || approvalID == "" {
		return jobOutput{WorkflowID: workflowID}, err
	}
	s.gated = true
	rc.Logger.Info("approval required", "approval_id", approvalID)
	if err := s.awaitApproval(approvalID); err != nil {
		return jobOutput{}, err
	}
	if workflowID, _, err = s.create(args, true); err != nil {
		return jobOutput{}, err
	}
	return jobOutput{WorkflowID: workflowID, ApprovalID: approvalID}, nil
}

func invalid(msg string) error { return jobError{codeInvalidInput, msg} }

// buildArgs validates the job before any I/O and marshals the create
// arguments once; both create calls send exactly these bytes.
func buildArgs(jobID string, in jobInput) ([]byte, error) {
	if !isSafeID(jobID) {
		return nil, invalid("the job id is not a safe identifier")
	}
	spec := in.Workflow
	if in.Context != nil && in.Context.Workflow != nil {
		if spec != nil {
			return nil, invalid("set exactly one of workflow or context.workflow")
		}
		spec = in.Context.Workflow
	}
	if spec == nil {
		return nil, invalid("set exactly one of workflow or context.workflow")
	}
	name := strings.TrimSpace(spec.Name)
	if name == "" || len(name) > 128 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, invalid("the workflow name must be 1-128 bytes without control characters")
	}
	if len(spec.Steps) == 0 {
		return nil, invalid("the workflow steps must be a non-empty object")
	}
	args, err := json.Marshal(createWorkflowArgs{Name: name, Description: spec.Description,
		Steps: spec.Steps, IdempotencyKey: "cap-mcp-governed:" + jobID})
	if err != nil || len(args) > maxArgsBytes {
		return nil, invalid("the workflow arguments must be valid JSON of at most 64 KiB")
	}
	return args, nil
}

// session is one job's MCP session. JSON-RPC ids count up from 1. gated is
// set once the gateway asked for approval; from then on the job deadline
// reports as mcp_approval_timeout.
type session struct {
	cfg    config
	base   context.Context // worker lifetime
	ctx    context.Context // job deadline, derived from base
	id     string
	nextID int
	gated  bool
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcFault struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// interrupted maps a transport failure or a done context to a code. A
// shutdown is checked first so it never reports as a timeout.
func (s *session) interrupted() error {
	switch {
	case s.base.Err() != nil:
		return errShutdown
	case s.gated && s.ctx.Err() != nil:
		return errTimeout
	}
	return errUnavailable
}

// statusErr maps a non-200 gateway status to a code.
func statusErr(status int) error {
	switch {
	case status >= 500:
		return errUnavailable
	case status >= 300 && status < 400:
		return errRedirect
	}
	return errRefused
}

// do sends one request bounded by callTimeout and returns the status and,
// for a 200, the body. The client never follows redirects, so the API key
// only goes to the configured gateway.
func (s *session) do(method, path string, body []byte) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(s.ctx, callTimeout)
	defer cancel()
	target := strings.TrimSuffix(s.cfg.GatewayURL.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return 0, nil, errUnavailable
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Key", s.cfg.APIKey)
	req.Header.Set("X-Tenant-ID", s.cfg.TenantID)
	req.Header.Set("X-MCP-Session-ID", s.id)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return 0, nil, s.interrupted()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, s.interrupted()
	}
	if len(data) > maxResponseBytes {
		return 0, nil, errMalformed
	}
	return resp.StatusCode, data, nil
}

func (s *session) post(msg rpcRequest) (int, []byte, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return 0, nil, errMalformed
	}
	return s.do(http.MethodPost, "/mcp/message", body)
}

// call sends one JSON-RPC request. With a nil error exactly one of the
// result and the JSON-RPC error is set.
func (s *session) call(method string, params any) (json.RawMessage, *rpcFault, error) {
	s.nextID++
	status, data, err := s.post(rpcRequest{JSONRPC: "2.0", ID: s.nextID, Method: method, Params: params})
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, nil, statusErr(status)
	}
	var resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *rpcFault       `json:"error"`
	}
	if json.Unmarshal(data, &resp) != nil || resp.JSONRPC != "2.0" || string(resp.ID) != strconv.Itoa(s.nextID) {
		return nil, nil, errMalformed
	}
	if string(resp.Result) == "null" {
		resp.Result = nil
	}
	if (resp.Result == nil) == (resp.Error == nil) {
		return nil, nil, errMalformed
	}
	return resp.Result, resp.Error, nil
}

func (s *session) notify(method string) error {
	status, _, err := s.post(rpcRequest{JSONRPC: "2.0", Method: method})
	if err != nil {
		return err
	}
	if status < 200 || status > 299 {
		return statusErr(status)
	}
	return nil
}

// open runs initialize, notifications/initialized, resources/read on the
// job and the read-only cordum_status call. Any failure stops the job
// before the mutation.
func (s *session) open(jobID string) error {
	res, rerr, err := s.call("initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion, "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "cap-mcp-governed-worker", "version": "1"},
	})
	if err != nil {
		return err
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if rerr != nil {
		return jobError{codeMCPCallFailed, "initialize failed"}
	}
	if json.Unmarshal(res, &init) != nil || init.ProtocolVersion != mcpProtocolVersion {
		return errMalformed
	}
	if err := s.notify("notifications/initialized"); err != nil {
		return err
	}
	if res, rerr, err = s.call("resources/read", map[string]string{"uri": "cordum://jobs/" + jobID}); err != nil {
		return err
	}
	var read struct {
		Contents []json.RawMessage `json:"contents"`
	}
	if rerr != nil || json.Unmarshal(res, &read) != nil || len(read.Contents) == 0 {
		return jobError{codeMCPCallFailed, "resources/read of the job failed"}
	}
	if res, rerr, err = s.call("tools/call", toolCall{Name: "cordum_status", Arguments: json.RawMessage(`{}`)}); err != nil {
		return err
	}
	var status toolResult
	if rerr != nil || json.Unmarshal(res, &status) != nil || status.IsError {
		return jobError{codeMCPCallFailed, "cordum_status failed"}
	}
	return nil
}

// create calls cordum_create_workflow once with args. It returns the
// workflow id, or the approval id when the first call answers -32099. On
// the retry any -32099 is a failure: there is never a third call.
func (s *session) create(args []byte, retry bool) (workflowID, approvalID string, err error) {
	failed := jobError{codeMCPCallFailed, "cordum_create_workflow failed"}
	res, rerr, err := s.call("tools/call", toolCall{Name: "cordum_create_workflow", Arguments: args})
	if err != nil {
		return "", "", err
	}
	if rerr != nil {
		if rerr.Code != codeApprovalNeeded || retry {
			return "", "", failed
		}
		var data struct {
			ApprovalID string `json:"approval_id"`
		}
		if json.Unmarshal(rerr.Data, &data) != nil || !isSafeID(data.ApprovalID) {
			return "", "", errMalformed
		}
		return "", data.ApprovalID, nil
	}
	var tr toolResult
	var out struct {
		WorkflowID string `json:"workflow_id"`
	}
	if json.Unmarshal(res, &tr) != nil || tr.IsError || len(tr.Content) == 0 || tr.Content[0].Type != "text" ||
		json.Unmarshal([]byte(tr.Content[0].Text), &out) != nil || out.WorkflowID == "" {
		return "", "", failed
	}
	return out.WorkflowID, "", nil
}

// awaitApproval polls the approval record, first immediately, until a
// human resolves it. The worker only reads the record.
func (s *session) awaitApproval(approvalID string) error {
	path := "/api/v1/mcp/approvals/" + url.PathEscape(approvalID)
	for {
		status, data, err := s.do(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if status == http.StatusNotFound {
			return jobError{codeApprovalNotFound, "the approval record does not exist"}
		}
		if status != http.StatusOK {
			return statusErr(status)
		}
		var rec struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(data, &rec) != nil {
			return errBadApproval
		}
		switch rec.Status {
		case "approved":
			return nil
		case "rejected":
			return jobError{codeApprovalRejected, "an operator rejected the approval"}
		case "expired":
			return jobError{codeApprovalExpired, "the approval expired before a decision"}
		case "pending":
		default:
			return errBadApproval
		}
		select {
		case <-s.ctx.Done():
			return s.interrupted()
		case <-time.After(s.cfg.PollInterval):
		}
	}
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

// newHTTPClient builds the gateway client: TLS 1.2 or later, the system
// roots plus an optional PEM bundle, no redirects and no client-wide
// timeout (each request carries its own deadline).
func newHTTPClient(caFile string) (*http.Client, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if caFile != "" {
		pemData, err := os.ReadFile(caFile)
		if err != nil {
			return nil, errors.New("cannot read the CA file")
		}
		if !pool.AppendCertsFromPEM(pemData) {
			return nil, errors.New("the CA file holds no PEM certificate")
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyFromEnvironment
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

var tenantID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// loadConfig reads the worker settings and fails closed. Errors name the
// variable and never echo its value.
func loadConfig(getenv func(string) string) (config, error) {
	gw, err := parseGatewayURL(getenv("CORDUM_GATEWAY_URL"))
	if err != nil {
		return config{}, err
	}
	key := getenv("CORDUM_API_KEY")
	if key == "" || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return config{}, errors.New("CORDUM_API_KEY is required and must not contain whitespace or control characters")
	}
	tenant := getenv("CORDUM_TENANT_ID")
	if !tenantID.MatchString(tenant) {
		return config{}, errors.New("CORDUM_TENANT_ID is required and must match [A-Za-z0-9._-]{1,128}")
	}
	client, err := newHTTPClient(getenv("CORDUM_TLS_CA"))
	if err != nil {
		return config{}, fmt.Errorf("CORDUM_TLS_CA: %w", err)
	}
	timeout, err := envDuration(getenv, "CAP_MCP_JOB_TIMEOUT", 10*time.Minute, time.Second, time.Hour)
	if err != nil {
		return config{}, err
	}
	interval, err := envDuration(getenv, "CAP_MCP_POLL_INTERVAL", 2*time.Second, 100*time.Millisecond, time.Minute)
	if err != nil {
		return config{}, err
	}
	if interval >= timeout {
		return config{}, errors.New("CAP_MCP_POLL_INTERVAL must be shorter than CAP_MCP_JOB_TIMEOUT")
	}
	return config{GatewayURL: gw, APIKey: key, TenantID: tenant, Client: client,
		JobTimeout: timeout, PollInterval: interval}, nil
}

// parseGatewayURL accepts https, or http to a loopback host only, with no
// userinfo, query, fragment or path. The API key travels in a header, so
// plain http is limited to this machine.
func parseGatewayURL(raw string) (*url.URL, error) {
	const name = "CORDUM_GATEWAY_URL"
	u, err := url.Parse(raw)
	if raw == "" || err != nil || u.Hostname() == "" {
		return nil, errors.New(name + " is required and must be an absolute URL with a host")
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#") || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return nil, errors.New(name + " must not carry userinfo, a query, a fragment or a path")
	}
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return u, nil
		}
		return nil, errors.New(name + " may use http only for localhost, ::1 or 127.0.0.0/8")
	}
	return nil, errors.New(name + " must use https")
}

func envDuration(getenv func(string) string, name string, def, low, high time.Duration) (time.Duration, error) {
	raw := getenv(name)
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < low || d > high {
		return 0, fmt.Errorf("%s must be a duration from %s to %s", name, low, high)
	}
	return d, nil
}

// buildHeartbeat advertises one job at a time: a second concurrent job
// would only queue another approval.
func buildHeartbeat() ([]byte, error) {
	return proto.Marshal(&agentv1.BusPacket{
		TraceId: workerID, SenderId: workerID, CreatedAt: timestamppb.Now(),
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		Payload: &agentv1.BusPacket_Heartbeat{Heartbeat: &agentv1.Heartbeat{
			WorkerId: workerID, Pool: workerPool, Type: "cpu", MaxParallelJobs: 1,
			Capabilities: []string{capability},
			Labels:       map[string]string{"name": "CAP MCP governed demo", "env": "demo"},
		}},
	})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx)
	stop()
	if err != nil {
		log.Fatalf("[cap-mcp-governed] %v", err)
	}
}

// run serves jobs until ctx is done. A job waiting for approval then ends
// with worker_shutdown; draining NATS lets that result publish before the
// connection and the blob store close.
func run(ctx context.Context) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	natsURL := envOr("NATS_URL", "nats://127.0.0.1:4222")
	store, err := runtime.NewRedisBlobStoreWithPing(envOr("REDIS_URL", "redis://:cordum-dev@127.0.0.1:6379/0"))
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	nc, err := connectNATS(natsURL)
	if err != nil {
		_ = store.Close()
		return fmt.Errorf("nats connect: %w", err)
	}
	agent := newAgent(&worker{cfg: cfg, base: ctx}, natsURL, nc, store)
	if err := agent.Start(); err != nil {
		nc.Close()
		_ = store.Close()
		return fmt.Errorf("agent start: %w", err)
	}
	go runtime.HeartbeatLoop(ctx, nc, buildHeartbeat)
	log.Printf("[cap-mcp-governed] ready (topic=%s, subject=%s, gateway=%s)", topic, directSubject, cfg.GatewayURL.Host)

	<-ctx.Done()
	log.Println("[cap-mcp-governed] shutdown requested")
	closed := make(chan struct{})
	nc.SetClosedHandler(func(*nats.Conn) { close(closed) })
	if err := nc.Drain(); err == nil {
		select {
		case <-closed:
		case <-time.After(shutdownBudget):
		}
	}
	return agent.Close()
}

func connectNATS(natsURL string) (*nats.Conn, error) {
	opts := []nats.Option{nats.Name(workerID), nats.Timeout(5 * time.Second)}
	tlsCfg, err := runtime.NATSTLSConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("nats tls config: %w", err)
	}
	if tlsCfg != nil {
		opts = append(opts, nats.Secure(tlsCfg))
	}
	if token := os.Getenv("NATS_TOKEN"); token != "" {
		opts = append(opts, nats.Token(token))
	}
	return nats.Connect(natsURL, opts...)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
