package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	agentv1 "github.com/cordum-io/cap/v2/cordum/agent/v1"
	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	capruntime "github.com/cordum-io/cap/v2/sdk/go/runtime"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/sdk/runtime"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	testAPIKey   = "test-api-key-SECRET-0123456789"
	testTenant   = "tenant-a"
	serverSecret = "SERVER-SECRET-TEXT"
	testApproval = "0123456789abcdef0123456789abcdef"
	resultWait   = 10 * time.Second

	keyInit   = mcp.MethodInitialize
	keyRead   = mcp.MethodResourcesRead
	keyStatus = mcp.MethodToolsCall + ":" + mcp.ToolStatus
	keyCreate = mcp.MethodToolsCall + ":" + mcp.ToolCreateWorkflow
)

var allErrorCodes = []string{
	codeInvalidInput, codeMCPUnavailable, codeMCPProtocolError, codeMCPCallFailed,
	codeApprovalRejected, codeApprovalExpired, codeApprovalNotFound,
	codeApprovalTimeout, codeApprovalInvalid, codeWorkerShutdown,
}

// ---- embedded NATS + CAP job harness ----

func startNATS(t *testing.T) string {
	t.Helper()
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("start nats server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns.ClientURL()
}

// runJob delivers one job to a fresh agent over a real embedded nats-server
// and returns its JobResult and the res:<jobID> blob (nil when FAILED).
func runJob(t *testing.T, w *worker, subject, jobID string, input []byte) (*agentv1.JobResult, []byte) {
	t.Helper()
	natsURL := startNATS(t)
	nc, err := nats.Connect(natsURL, nats.Timeout(2*time.Second))
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)
	store := runtime.NewInMemoryBlobStore()
	agent := newAgent(w, natsURL, nc, store)
	agent.HandshakeMode = capruntime.HandshakeModeOff
	results := make(chan *agentv1.JobResult, 1)
	if _, err := nc.Subscribe(capsdk.SubjectResult, func(m *nats.Msg) {
		var pkt agentv1.BusPacket
		if proto.Unmarshal(m.Data, &pkt) != nil {
			return
		}
		if res := pkt.GetJobResult(); res.GetJobId() == jobID {
			select {
			case results <- res:
			default:
			}
		}
	}); err != nil {
		t.Fatalf("subscribe results: %v", err)
	}
	if err := agent.Start(); err != nil {
		t.Fatalf("agent start: %v", err)
	}
	t.Cleanup(func() { _ = agent.Close() })
	if err := store.Set(context.Background(), "ctx:"+jobID, input); err != nil {
		t.Fatalf("write ctx: %v", err)
	}
	publishJob(t, nc, subject, jobID)
	select {
	case res := <-results:
		blob, _ := store.Get(context.Background(), "res:"+jobID)
		return res, blob
	case <-time.After(resultWait):
		t.Fatalf("no JobResult for %s within %s", jobID, resultWait)
		return nil, nil
	}
}

func publishJob(t *testing.T, nc *nats.Conn, subject, jobID string) {
	t.Helper()
	pkt := &agentv1.BusPacket{
		TraceId:         "trace-" + jobID,
		SenderId:        "test-scheduler",
		ProtocolVersion: capsdk.DefaultProtocolVersion,
		CreatedAt:       timestamppb.Now(),
		Payload: &agentv1.BusPacket_JobRequest{JobRequest: &agentv1.JobRequest{
			JobId: jobID, Topic: topic, ContextPtr: "redis://ctx:" + jobID,
		}},
	}
	if err := capsdk.ValidateBusPacket(pkt); err != nil {
		t.Fatalf("job packet invalid: %v", err)
	}
	data, err := proto.Marshal(pkt)
	if err != nil {
		t.Fatalf("marshal job: %v", err)
	}
	if err := nc.Publish(subject, data); err != nil {
		t.Fatalf("publish job: %v", err)
	}
}

// ---- fake Cordum gateway built from the real core/mcp wire types ----

type fakeReq struct {
	kind   string // rpc | notify | poll | approve | reject | other
	method string
	tool   string
	uri    string
	id     string
	args   json.RawMessage
	header http.Header
}

func (r fakeReq) label() string {
	switch r.kind {
	case "rpc", "notify":
		if r.tool != "" {
			return r.kind + ":" + r.method + ":" + r.tool
		}
		return r.kind + ":" + r.method
	}
	return r.kind
}

type reply func(w http.ResponseWriter, id json.RawMessage)

type pollReply func(w http.ResponseWriter, id string)

type fakeCordum struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []fakeReq
	scripts map[string][]reply
	polls   []pollReply
	onPoll  func(n int)
}

func newFake(t *testing.T) *fakeCordum {
	t.Helper()
	f := &fakeCordum{scripts: map[string][]reply{
		keyInit:   {result(initOK(mcp.DefaultProtocolVersion))},
		keyRead:   {result(resourceOK())},
		keyStatus: {result(toolText(`{"status":"ok"}`, false))},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", f.serveRPC)
	mux.HandleFunc("GET /api/v1/mcp/approvals/{id}", f.servePoll)
	mux.HandleFunc("POST /api/v1/mcp/approvals/{id}/approve", f.refuse("approve"))
	mux.HandleFunc("POST /api/v1/mcp/approvals/{id}/reject", f.refuse("reject"))
	mux.HandleFunc("/", f.refuse("other"))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	// DoD 3, after every scenario: the worker never resolves an approval.
	t.Cleanup(func() {
		if n := f.count("approve") + f.count("reject") + f.count("other"); n != 0 {
			t.Errorf("approve+reject+other requests = %d, want 0", n)
		}
	})
	return f
}

func (f *fakeCordum) serveRPC(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		http.Error(w, serverSecret, http.StatusBadRequest)
		return
	}
	var p struct {
		Name      string          `json:"name"`
		URI       string          `json:"uri"`
		Arguments json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(msg.Params, &p)
	req := fakeReq{kind: "rpc", method: msg.Method, tool: p.Name, uri: p.URI, id: string(msg.ID), args: p.Arguments, header: r.Header.Clone()}
	if len(msg.ID) == 0 {
		req.kind = "notify"
	}
	next := f.record(req)
	if req.kind == "notify" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	next(w, msg.ID)
}

// record appends req and pops its scripted reply; the last reply repeats.
func (f *fakeCordum) record(req fakeReq) reply {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	key := req.method
	if req.tool != "" {
		key += ":" + req.tool
	}
	queue := f.scripts[key]
	if len(queue) == 0 {
		return rpcError(-32601, nil)
	}
	if len(queue) > 1 {
		f.scripts[key] = queue[1:]
	}
	return queue[0]
}

func (f *fakeCordum) servePoll(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	f.reqs = append(f.reqs, fakeReq{kind: "poll", uri: id, header: r.Header.Clone()})
	n := f.countLocked("poll")
	var next pollReply
	if len(f.polls) > 0 {
		next = f.polls[0]
		if len(f.polls) > 1 {
			f.polls = f.polls[1:]
		}
	}
	hook := f.onPoll
	f.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	if next == nil {
		http.Error(w, serverSecret, http.StatusInternalServerError)
		return
	}
	next(w, id)
}

func (f *fakeCordum) refuse(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs = append(f.reqs, fakeReq{kind: kind, uri: r.URL.Path, header: r.Header.Clone()})
		f.mu.Unlock()
		http.Error(w, serverSecret, http.StatusForbidden)
	}
}

func (f *fakeCordum) script(key string, replies ...reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[key] = replies
}

func (f *fakeCordum) pollScript(replies ...pollReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls = replies
}

func (f *fakeCordum) setOnPoll(hook func(n int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onPoll = hook
}

func (f *fakeCordum) requests() []fakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.reqs)
}

func (f *fakeCordum) count(label string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.countLocked(label)
}

func (f *fakeCordum) countLocked(label string) int {
	n := 0
	for _, r := range f.reqs {
		if r.label() == label {
			n++
		}
	}
	return n
}

func (f *fakeCordum) creates() []json.RawMessage {
	var out []json.RawMessage
	for _, r := range f.requests() {
		if r.label() == "rpc:"+keyCreate {
			out = append(out, r.args)
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func result(v any) reply {
	return func(w http.ResponseWriter, id json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: mcp.JSONRPCVersion, ID: id, Result: v})
	}
}

func rpcError(code int, data any) reply {
	return func(w http.ResponseWriter, id json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: mcp.JSONRPCVersion, ID: id,
			Error: &mcp.JSONRPCError{Code: code, Message: serverSecret, Data: data}})
	}
}

func httpStatus(code int) reply {
	return func(w http.ResponseWriter, _ json.RawMessage) { http.Error(w, serverSecret, code) }
}

func initOK(version string) mcp.InitializeResult {
	return mcp.InitializeResult{ProtocolVersion: version, ServerInfo: mcp.Implementation{Name: "cordum", Version: "test"}}
}

func resourceOK() mcp.ResourceReadResult {
	return mcp.ResourceReadResult{Contents: []mcp.ResourceContents{{
		URI: "cordum://jobs/job-1", MIMEType: "application/json", Text: `{"id":"job-1","state":"RUNNING"}`,
	}}}
}

func toolText(text string, isError bool) mcp.ToolCallResult {
	return mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: text}}, IsError: isError}
}

func created(workflowID string) reply {
	return result(toolText(`{"workflow_id":"`+workflowID+`","version":"","status":"created"}`, false))
}

func approvalRequired(approvalID string) reply {
	return rpcError(-32099, &mcp.ApprovalRequired{
		ApprovalID: approvalID, ApprovalRef: approvalID, ArgsHash: "hash",
		RetryHint: mcp.RetryHintSameArgs, Reason: serverSecret, Tool: mcp.ToolCreateWorkflow,
	})
}

func pollStatus(status model.ApprovalStatus) pollReply {
	return func(w http.ResponseWriter, id string) {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "tool_name": mcp.ToolCreateWorkflow, "status": status})
	}
}

func pollHTTP(code int) pollReply {
	return func(w http.ResponseWriter, _ string) {
		writeJSON(w, code, map[string]any{"error": serverSecret, "code": "approval_not_found", "status": code})
	}
}

// ---- fixtures ----

func testConfig(t *testing.T, gatewayURL string) config {
	t.Helper()
	u, err := url.Parse(gatewayURL)
	if err != nil {
		t.Fatalf("parse gateway url: %v", err)
	}
	client, err := newHTTPClient("")
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}
	return config{GatewayURL: u, APIKey: testAPIKey, TenantID: testTenant, Client: client,
		JobTimeout: 2 * time.Second, PollInterval: 10 * time.Millisecond}
}

func newTestWorker(t *testing.T, cfg config) *worker {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &worker{cfg: cfg, base: ctx}
}

func testSpec() map[string]any {
	return map[string]any{
		"name":        "governed-demo",
		"description": "created by the cap-mcp-governed demo",
		"steps":       map[string]any{"notify": map[string]any{"type": "notify", "input": map[string]any{"message": "hello <world> & co"}}},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func directInput(t *testing.T) []byte {
	return mustJSON(t, map[string]any{"workflow": testSpec()})
}

func wantFailed(t *testing.T, res *agentv1.JobResult, code string) {
	t.Helper()
	if res.GetStatus() != agentv1.JobStatus_JOB_STATUS_FAILED {
		t.Fatalf("status = %s (%q), want FAILED", res.GetStatus(), res.GetErrorMessage())
	}
	msg := res.GetErrorMessage()
	if !strings.HasPrefix(msg, code+": ") {
		t.Fatalf("error_message = %q, want prefix %q", msg, code+": ")
	}
	if strings.Contains(msg, testAPIKey) || strings.Contains(msg, serverSecret) {
		t.Fatalf("error_message leaks a secret or server text: %q", msg)
	}
}

func wantSucceeded(t *testing.T, res *agentv1.JobResult, blob []byte, wantBlob string) {
	t.Helper()
	if res.GetStatus() != agentv1.JobStatus_JOB_STATUS_SUCCEEDED {
		t.Fatalf("status = %s (%q), want SUCCEEDED", res.GetStatus(), res.GetErrorMessage())
	}
	if string(blob) != wantBlob {
		t.Fatalf("result blob = %s, want %s", blob, wantBlob)
	}
}

func wantCreates(t *testing.T, f *fakeCordum, want int) []json.RawMessage {
	t.Helper()
	got := f.creates()
	if len(got) != want {
		t.Fatalf("create calls = %d, want %d", len(got), want)
	}
	return got
}

// ---- DoD 1 ----

func TestApprovedCreateRetriesOnceWithIdenticalArgs(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, approvalRequired(testApproval), created("wf-123"))
	f.pollScript(pollStatus(model.ApprovalStatusPending), pollStatus(model.ApprovalStatusPending), pollStatus(model.ApprovalStatusApproved))
	w := newTestWorker(t, testConfig(t, f.srv.URL))

	res, blob := runJob(t, w, topic, "job-approved-1", directInput(t))

	wantSucceeded(t, res, blob, `{"workflow_id":"wf-123","approval_id":"`+testApproval+`"}`)
	args := wantCreates(t, f, 2)
	if !bytes.Equal(args[0], args[1]) {
		t.Fatalf("retry args differ:\n first=%s\nsecond=%s", args[0], args[1])
	}
	var decoded map[string]any
	if err := json.Unmarshal(args[0], &decoded); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if decoded["idempotency_key"] != "cap-mcp-governed:job-approved-1" {
		t.Fatalf("idempotency_key = %v", decoded["idempotency_key"])
	}
	if _, ok := decoded["_approval_ref"]; ok {
		t.Fatal("args carry _approval_ref")
	}
	spec := testSpec()
	if decoded["name"] != spec["name"] || decoded["description"] != spec["description"] {
		t.Fatalf("args name/description = %v/%v", decoded["name"], decoded["description"])
	}
	if !bytes.Equal(mustJSON(t, decoded["steps"]), mustJSON(t, spec["steps"])) {
		t.Fatalf("args steps = %v", decoded["steps"])
	}
	if got := f.count("poll"); got != 3 {
		t.Fatalf("polls = %d, want 3", got)
	}
}

// ---- DoD 2 ----

func TestApprovalOutcomesFailWithoutRetry(t *testing.T) {
	cases := []struct {
		name    string
		polls   []pollReply
		timeout time.Duration
		code    string
	}{
		{"rejected", []pollReply{pollStatus(model.ApprovalStatusPending), pollStatus(model.ApprovalStatusRejected)}, 0, codeApprovalRejected},
		{"expired", []pollReply{pollStatus(model.ApprovalStatusExpired)}, 0, codeApprovalExpired},
		{"not found", []pollReply{pollHTTP(http.StatusNotFound)}, 0, codeApprovalNotFound},
		{"deadline", []pollReply{pollStatus(model.ApprovalStatusPending)}, 300 * time.Millisecond, codeApprovalTimeout},
		{"invalidated", []pollReply{pollStatus(model.ApprovalStatusInvalidated)}, 0, codeApprovalInvalid},
		{"repaired", []pollReply{pollStatus(model.ApprovalStatusRepaired)}, 0, codeApprovalInvalid},
		{"unknown status", []pollReply{pollStatus("bogus")}, 0, codeApprovalInvalid},
		{"poll 503", []pollReply{pollHTTP(http.StatusServiceUnavailable)}, 0, codeMCPUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.script(keyCreate, approvalRequired(testApproval))
			f.pollScript(tc.polls...)
			cfg := testConfig(t, f.srv.URL)
			if tc.timeout > 0 {
				cfg.JobTimeout = tc.timeout
			}
			res, _ := runJob(t, newTestWorker(t, cfg), topic, "job-outcome-1", directInput(t))
			wantFailed(t, res, tc.code)
			wantCreates(t, f, 1)
			for _, r := range f.requests() {
				if r.kind == "poll" && r.uri != testApproval {
					t.Fatalf("polled approval %q, want %q", r.uri, testApproval)
				}
			}
		})
	}
}

// ---- DoD 4 ----

func TestReadAndStatusPrecedeMutation(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, approvalRequired(testApproval), created("wf-123"))
	f.pollScript(pollStatus(model.ApprovalStatusApproved))
	res, blob := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-order-1", directInput(t))
	wantSucceeded(t, res, blob, `{"workflow_id":"wf-123","approval_id":"`+testApproval+`"}`)

	var labels, ids []string
	for _, r := range f.requests() {
		labels = append(labels, r.label())
		if r.kind == "rpc" {
			ids = append(ids, r.id)
		}
		if r.method == mcp.MethodResourcesRead && r.uri != "cordum://jobs/job-order-1" {
			t.Fatalf("resources/read uri = %q", r.uri)
		}
	}
	want := []string{
		"rpc:" + keyInit, "notify:notifications/initialized", "rpc:" + keyRead,
		"rpc:" + keyStatus, "rpc:" + keyCreate, "poll", "rpc:" + keyCreate,
	}
	if !slices.Equal(labels, want) {
		t.Fatalf("request order:\n got %v\nwant %v", labels, want)
	}
	if !slices.Equal(ids, []string{"1", "2", "3", "4", "5"}) {
		t.Fatalf("json-rpc ids = %v, want 1..5", ids)
	}
}

func TestReadOrStatusFailureBlocksMutation(t *testing.T) {
	cases := []struct {
		name string
		key  string
		rep  reply
	}{
		{"read rpc error", keyRead, rpcError(-32602, serverSecret)},
		{"read empty contents", keyRead, result(mcp.ResourceReadResult{Contents: []mcp.ResourceContents{}})},
		{"status rpc error", keyStatus, rpcError(-32603, serverSecret)},
		{"status isError", keyStatus, result(toolText(serverSecret, true))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.script(tc.key, tc.rep)
			f.script(keyCreate, created("wf-123"))
			res, _ := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-blocked-1", directInput(t))
			wantFailed(t, res, codeMCPCallFailed)
			wantCreates(t, f, 0)
		})
	}
}

// ---- transport, headers and protocol ----

func TestHeadersAndSession(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, approvalRequired(testApproval), created("wf-123"))
	f.pollScript(pollStatus(model.ApprovalStatusApproved))
	cfg := testConfig(t, f.srv.URL)

	// The worker sends nothing after publishing a result, so the request
	// count after job 1 splits the two jobs' traffic.
	var split int
	for i, jobID := range []string{"job-headers-1", "job-headers-2"} {
		res, _ := runJob(t, newTestWorker(t, cfg), topic, jobID, directInput(t))
		if res.GetStatus() != agentv1.JobStatus_JOB_STATUS_SUCCEEDED {
			t.Fatalf("%s: %s %q", jobID, res.GetStatus(), res.GetErrorMessage())
		}
		if i == 0 {
			split = len(f.requests())
		}
	}
	all := f.requests()
	first, second := all[:split], all[split:]
	if f.count("poll") != 1 || first[len(first)-2].kind != "poll" {
		t.Fatal("job 1 must include the approval poll, so poll headers are checked")
	}

	s1 := sessionOf(t, first)
	s2 := sessionOf(t, second)
	if s1 == s2 {
		t.Fatalf("two jobs share session id %q", s1)
	}
	if len(s1) < 16 {
		t.Fatalf("session id %q is too short to be random", s1)
	}
}

// sessionOf asserts the per-request header contract and returns the single
// X-MCP-Session-ID the requests share.
func sessionOf(t *testing.T, reqs []fakeReq) string {
	t.Helper()
	if len(reqs) == 0 {
		t.Fatal("no requests recorded")
	}
	session := reqs[0].header.Get("X-MCP-Session-ID")
	for _, r := range reqs {
		h := r.header
		if h.Get("X-API-Key") != testAPIKey || h.Get("X-Tenant-ID") != testTenant {
			t.Fatalf("%s: missing X-API-Key/X-Tenant-ID", r.label())
		}
		if h.Get("X-Agent-Id") != "" {
			t.Fatalf("%s: sent X-Agent-Id", r.label())
		}
		if got := h.Get("X-MCP-Session-ID"); got == "" || got != session {
			t.Fatalf("%s: session %q, want one session %q per job", r.label(), got, session)
		}
	}
	return session
}

func TestOutage(t *testing.T) {
	t.Run("server closed", func(t *testing.T) {
		f := newFake(t)
		cfg := testConfig(t, f.srv.URL)
		f.srv.Close()
		res, _ := runJob(t, newTestWorker(t, cfg), topic, "job-outage-1", directInput(t))
		wantFailed(t, res, codeMCPUnavailable)
		wantCreates(t, f, 0)
	})
	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"initialize 500", http.StatusInternalServerError, codeMCPUnavailable},
		{"initialize 503", http.StatusServiceUnavailable, codeMCPUnavailable},
		{"initialize 403", http.StatusForbidden, codeMCPCallFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.script(keyInit, httpStatus(tc.status))
			res, _ := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-outage-2", directInput(t))
			wantFailed(t, res, tc.code)
			wantCreates(t, f, 0)
		})
	}
}

func TestProtocolErrors(t *testing.T) {
	other := &fakeCordum{scripts: map[string][]reply{}}
	otherSrv := httptest.NewServer(http.HandlerFunc(other.refuse("other")))
	t.Cleanup(otherSrv.Close)
	redirect := func(code int) reply {
		return func(w http.ResponseWriter, _ json.RawMessage) {
			w.Header().Set("Location", otherSrv.URL+"/mcp/message")
			w.WriteHeader(code)
		}
	}
	wrongID := func(w http.ResponseWriter, _ json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: mcp.JSONRPCVersion, ID: json.RawMessage(`999`), Result: initOK(mcp.DefaultProtocolVersion)})
	}
	oversize := func(w http.ResponseWriter, id json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: mcp.JSONRPCVersion, ID: id,
			Result: map[string]string{"pad": strings.Repeat("x", maxResponseBytes)}})
	}
	bothResultAndError := func(w http.ResponseWriter, id json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: mcp.JSONRPCVersion, ID: id,
			Result: initOK(mcp.DefaultProtocolVersion), Error: &mcp.JSONRPCError{Code: -32603, Message: serverSecret}})
	}
	neither := func(w http.ResponseWriter, id json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: mcp.JSONRPCVersion, ID: id})
	}
	badVersion := func(w http.ResponseWriter, id json.RawMessage) {
		writeJSON(w, http.StatusOK, mcp.JSONRPCMessage{JSONRPC: "1.0", ID: id, Result: initOK(mcp.DefaultProtocolVersion)})
	}
	cases := []struct {
		name string
		key  string
		rep  reply
	}{
		{"approval id missing", keyCreate, approvalRequired("")},
		{"approval id traversal", keyCreate, approvalRequired("../x")},
		{"approval id dot-dot", keyCreate, approvalRequired("..")},
		{"approval id 129 chars", keyCreate, approvalRequired(strings.Repeat("a", 129))},
		{"approval data not an object", keyCreate, rpcError(-32099, serverSecret)},
		{"response id mismatch", keyInit, wrongID},
		{"protocolVersion mismatch", keyInit, result(initOK("2025-03-26"))},
		{"body over 1 MiB", keyInit, oversize},
		{"result and error", keyInit, bothResultAndError},
		{"neither result nor error", keyInit, neither},
		{"jsonrpc not 2.0", keyInit, badVersion},
		{"302 redirect", keyInit, redirect(http.StatusFound)},
		{"307 redirect", keyInit, redirect(http.StatusTemporaryRedirect)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.script(tc.key, tc.rep)
			f.pollScript(pollStatus(model.ApprovalStatusApproved))
			res, _ := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-proto-1", directInput(t))
			wantFailed(t, res, codeMCPProtocolError)
			if n := f.count("poll"); n != 0 {
				t.Fatalf("polls = %d, want 0", n)
			}
			if n := len(f.creates()); n > 1 {
				t.Fatalf("create calls = %d, want <= 1", n)
			}
		})
	}
	if n := other.count("other"); n != 0 {
		t.Fatalf("redirect target received %d requests; the API key would have leaked", n)
	}
}

func TestRetryStillGatedFails(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, approvalRequired(testApproval), approvalRequired("fedcba9876543210fedcba9876543210"))
	f.pollScript(pollStatus(model.ApprovalStatusApproved))
	res, _ := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-regated-1", directInput(t))
	wantFailed(t, res, codeMCPCallFailed)
	wantCreates(t, f, 2)
	if n := f.count("poll"); n != 1 {
		t.Fatalf("polls = %d, want 1", n)
	}
}

func TestPreapprovedCreateSucceedsWithoutPolling(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, created("wf-123"))
	res, blob := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-pre-1", directInput(t))
	wantSucceeded(t, res, blob, `{"workflow_id":"wf-123"}`)
	wantCreates(t, f, 1)
	if n := f.count("poll"); n != 0 {
		t.Fatalf("polls = %d, want 0", n)
	}
}

func TestCreateResultWithoutWorkflowIDFails(t *testing.T) {
	cases := map[string]reply{
		"isError":           result(toolText(serverSecret, true)),
		"no workflow_id":    result(toolText(`{"status":"created"}`, false)),
		"non-string id":     result(toolText(`{"workflow_id":42}`, false)),
		"text not json":     result(toolText(serverSecret, false)),
		"empty content":     result(mcp.ToolCallResult{Content: []mcp.ContentItem{}}),
		"other rpc error":   rpcError(-32098, serverSecret),
		"result null":       result(nil),
		"content not text":  result(mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "image", Data: "AAAA"}}}),
		"misconfigured gw":  rpcError(-32097, serverSecret),
		"store unavailable": rpcError(-32096, serverSecret),
	}
	for name, rep := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t)
			f.script(keyCreate, rep)
			res, _ := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-bad-create-1", directInput(t))
			if name == "result null" {
				wantFailed(t, res, codeMCPProtocolError)
			} else {
				wantFailed(t, res, codeMCPCallFailed)
			}
			wantCreates(t, f, 1)
		})
	}
}

// ---- input ----

func TestInvalidInputMakesNoCalls(t *testing.T) {
	spec := testSpec()
	withName := func(name string) []byte {
		s := maps.Clone(spec)
		s["name"] = name
		return mustJSON(t, map[string]any{"workflow": s})
	}
	big := maps.Clone(spec)
	big["steps"] = map[string]any{"s": map[string]any{"blob": strings.Repeat("y", maxArgsBytes)}}
	cases := []struct {
		name  string
		jobID string
		input []byte
	}{
		{"missing name", "", []byte(`{"workflow":{"steps":{"s":{}}}}`)},
		{"blank name", "", withName("   ")},
		{"control char in name", "", withName("bad\u0007name")},
		{"name over 128 bytes", "", withName(strings.Repeat("n", 129))},
		{"empty steps", "", []byte(`{"workflow":{"name":"x","steps":{}}}`)},
		{"null steps", "", []byte(`{"workflow":{"name":"x","steps":null}}`)},
		{"args over 64 KiB", "", mustJSON(t, map[string]any{"workflow": big})},
		{"both sources", "", mustJSON(t, map[string]any{"workflow": spec, "context": map[string]any{"workflow": spec}})},
		{"envelope without context.workflow", "", mustJSON(t, map[string]any{"prompt": "p", "topic": topic, "context": map[string]any{}})},
		{"neither source", "", []byte(`{}`)},
		{"null sources", "", []byte(`{"workflow":null,"context":{"workflow":null}}`)},
		{"job id with slash", "bad/job", directInput(t)},
		{"job id dot-dot", "..", directInput(t)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.script(keyCreate, created("wf-123"))
			jobID := tc.jobID
			if jobID == "" {
				jobID = "job-invalid-1"
			}
			res, _ := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, jobID, tc.input)
			wantFailed(t, res, codeInvalidInput)
			if n := len(f.requests()); n != 0 {
				t.Fatalf("requests = %d, want 0", n)
			}
		})
	}
}

func TestRESTEnvelopeInput(t *testing.T) {
	envelope := mustJSON(t, map[string]any{
		"prompt": "", "adapter_id": "", "priority": "", "topic": topic,
		"created_at": "2026-11-11T00:00:00Z", "tenant_id": "default",
		"context": map[string]any{"workflow": testSpec()},
	})
	direct := newFake(t)
	direct.script(keyCreate, created("wf-123"))
	if res, _ := runJob(t, newTestWorker(t, testConfig(t, direct.srv.URL)), topic, "job-rest-1", directInput(t)); res.GetStatus() != agentv1.JobStatus_JOB_STATUS_SUCCEEDED {
		t.Fatalf("direct form: %s %q", res.GetStatus(), res.GetErrorMessage())
	}

	f := newFake(t)
	f.script(keyCreate, approvalRequired(testApproval), created("wf-123"))
	f.pollScript(pollStatus(model.ApprovalStatusApproved))
	res, blob := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), topic, "job-rest-1", envelope)

	wantSucceeded(t, res, blob, `{"workflow_id":"wf-123","approval_id":"`+testApproval+`"}`)
	args := wantCreates(t, f, 2)
	if !bytes.Equal(args[0], args[1]) {
		t.Fatalf("retry args differ: %s vs %s", args[0], args[1])
	}
	if !bytes.Equal(args[0], direct.creates()[0]) {
		t.Fatalf("envelope args %s differ from direct-form args %s", args[0], direct.creates()[0])
	}
}

func TestDirectSubjectDispatch(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, created("wf-123"))
	res, blob := runJob(t, newTestWorker(t, testConfig(t, f.srv.URL)), directSubject, "job-direct-1", directInput(t))
	wantSucceeded(t, res, blob, `{"workflow_id":"wf-123"}`)
	wantCreates(t, f, 1)
}

func TestShutdownDuringApprovalWait(t *testing.T) {
	f := newFake(t)
	f.script(keyCreate, approvalRequired(testApproval))
	f.pollScript(pollStatus(model.ApprovalStatusPending))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f.setOnPoll(func(n int) {
		if n == 1 {
			cancel()
		}
	})
	w := &worker{cfg: testConfig(t, f.srv.URL), base: ctx}
	res, _ := runJob(t, w, topic, "job-shutdown-1", directInput(t))
	wantFailed(t, res, codeWorkerShutdown)
	wantCreates(t, f, 1)
}

// ---- config, docs, heartbeat ----

func writeTestCA(t *testing.T, dir string) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	der := srv.Certificate().Raw
	srv.Close()
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	return path
}

func TestLoadConfigFailsClosed(t *testing.T) {
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "not.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	goodCA := writeTestCA(t, dir)
	base := map[string]string{"CORDUM_GATEWAY_URL": "https://gw.example", "CORDUM_API_KEY": testAPIKey, "CORDUM_TENANT_ID": testTenant}
	cases := []struct {
		name    string
		set     map[string]string
		wantVar string // "" = accepted
	}{
		{"https accepted", nil, ""},
		{"https with slash accepted", map[string]string{"CORDUM_GATEWAY_URL": "https://gw.example:8443/"}, ""},
		{"http 127.0.0.1 accepted", map[string]string{"CORDUM_GATEWAY_URL": "http://127.0.0.1:8081"}, ""},
		{"http 127.0.0.2 accepted", map[string]string{"CORDUM_GATEWAY_URL": "http://127.0.0.2:8081"}, ""},
		{"http localhost accepted", map[string]string{"CORDUM_GATEWAY_URL": "http://localhost:8081"}, ""},
		{"http ::1 accepted", map[string]string{"CORDUM_GATEWAY_URL": "http://[::1]:8081"}, ""},
		{"ca file accepted", map[string]string{"CORDUM_TLS_CA": goodCA}, ""},
		{"durations accepted", map[string]string{"CAP_MCP_JOB_TIMEOUT": "30s", "CAP_MCP_POLL_INTERVAL": "500ms"}, ""},
		{"missing url", map[string]string{"CORDUM_GATEWAY_URL": ""}, "CORDUM_GATEWAY_URL"},
		{"missing key", map[string]string{"CORDUM_API_KEY": ""}, "CORDUM_API_KEY"},
		{"missing tenant", map[string]string{"CORDUM_TENANT_ID": ""}, "CORDUM_TENANT_ID"},
		{"key with space", map[string]string{"CORDUM_API_KEY": "abc " + testAPIKey}, "CORDUM_API_KEY"},
		{"key with newline", map[string]string{"CORDUM_API_KEY": testAPIKey + "\n"}, "CORDUM_API_KEY"},
		{"bad tenant", map[string]string{"CORDUM_TENANT_ID": "a/b"}, "CORDUM_TENANT_ID"},
		{"http remote host", map[string]string{"CORDUM_GATEWAY_URL": "http://gw.example:8081"}, "CORDUM_GATEWAY_URL"},
		{"http private ip", map[string]string{"CORDUM_GATEWAY_URL": "http://10.0.0.1:8081"}, "CORDUM_GATEWAY_URL"},
		{"http localhost lookalike", map[string]string{"CORDUM_GATEWAY_URL": "http://localhost.gw.example"}, "CORDUM_GATEWAY_URL"},
		{"ftp scheme", map[string]string{"CORDUM_GATEWAY_URL": "ftp://gw.example"}, "CORDUM_GATEWAY_URL"},
		{"no host", map[string]string{"CORDUM_GATEWAY_URL": "https://"}, "CORDUM_GATEWAY_URL"},
		{"userinfo", map[string]string{"CORDUM_GATEWAY_URL": "https://u:pw-SECRET@gw.example"}, "CORDUM_GATEWAY_URL"},
		{"query", map[string]string{"CORDUM_GATEWAY_URL": "https://gw.example?x=1"}, "CORDUM_GATEWAY_URL"},
		{"fragment", map[string]string{"CORDUM_GATEWAY_URL": "https://gw.example#f"}, "CORDUM_GATEWAY_URL"},
		{"path", map[string]string{"CORDUM_GATEWAY_URL": "https://gw.example/api"}, "CORDUM_GATEWAY_URL"},
		{"unreadable ca", map[string]string{"CORDUM_TLS_CA": filepath.Join(dir, "missing.pem")}, "CORDUM_TLS_CA"},
		{"non-pem ca", map[string]string{"CORDUM_TLS_CA": notPEM}, "CORDUM_TLS_CA"},
		{"bad timeout", map[string]string{"CAP_MCP_JOB_TIMEOUT": "soon"}, "CAP_MCP_JOB_TIMEOUT"},
		{"timeout too short", map[string]string{"CAP_MCP_JOB_TIMEOUT": "500ms"}, "CAP_MCP_JOB_TIMEOUT"},
		{"timeout too long", map[string]string{"CAP_MCP_JOB_TIMEOUT": "2h"}, "CAP_MCP_JOB_TIMEOUT"},
		{"bad interval", map[string]string{"CAP_MCP_POLL_INTERVAL": "-1s"}, "CAP_MCP_POLL_INTERVAL"},
		{"interval too short", map[string]string{"CAP_MCP_POLL_INTERVAL": "50ms"}, "CAP_MCP_POLL_INTERVAL"},
		{"interval too long", map[string]string{"CAP_MCP_POLL_INTERVAL": "2m"}, "CAP_MCP_POLL_INTERVAL"},
		{"interval not below timeout", map[string]string{"CAP_MCP_JOB_TIMEOUT": "1s", "CAP_MCP_POLL_INTERVAL": "1s"}, "CAP_MCP_POLL_INTERVAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := maps.Clone(base)
			maps.Copy(env, tc.set)
			cfg, err := loadConfig(func(k string) string { return env[k] })
			if tc.wantVar == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				checkAcceptedConfig(t, cfg, env)
				return
			}
			if err == nil {
				t.Fatal("accepted, want rejection")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantVar) {
				t.Fatalf("error %q does not name %s", msg, tc.wantVar)
			}
			if strings.Contains(msg, testAPIKey) || strings.Contains(msg, "pw-SECRET") {
				t.Fatalf("error leaks a secret: %q", msg)
			}
		})
	}
}

func checkAcceptedConfig(t *testing.T, cfg config, env map[string]string) {
	t.Helper()
	if cfg.Client == nil || cfg.GatewayURL == nil || cfg.APIKey != env["CORDUM_API_KEY"] || cfg.TenantID != env["CORDUM_TENANT_ID"] {
		t.Fatalf("incomplete config: %+v", cfg)
	}
	wantTimeout, wantInterval := 10*time.Minute, 2*time.Second
	if env["CAP_MCP_JOB_TIMEOUT"] != "" {
		wantTimeout, wantInterval = 30*time.Second, 500*time.Millisecond
	}
	if cfg.JobTimeout != wantTimeout || cfg.PollInterval != wantInterval {
		t.Fatalf("timeout/interval = %s/%s, want %s/%s", cfg.JobTimeout, cfg.PollInterval, wantTimeout, wantInterval)
	}
}

func TestErrorCodesDocumented(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	for _, code := range allErrorCodes {
		if !bytes.Contains(readme, []byte("`"+code+"`")) {
			t.Errorf("README does not document error code %s", code)
		}
	}
}

func TestHeartbeatPassesCAPValidation(t *testing.T) {
	data, err := buildHeartbeat()
	if err != nil {
		t.Fatalf("buildHeartbeat: %v", err)
	}
	var pkt agentv1.BusPacket
	if err := proto.Unmarshal(data, &pkt); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := capsdk.ValidateBusPacket(&pkt); err != nil {
		t.Fatalf("ValidateBusPacket: %v", err)
	}
	hb := pkt.GetHeartbeat()
	if pkt.GetTraceId() != workerID || pkt.GetSenderId() != workerID || hb.GetWorkerId() != workerID {
		t.Fatalf("ids: trace=%q sender=%q worker=%q", pkt.GetTraceId(), pkt.GetSenderId(), hb.GetWorkerId())
	}
	if hb.GetMaxParallelJobs() != 1 || hb.GetPool() != workerPool || !slices.Equal(hb.GetCapabilities(), []string{capability}) {
		t.Fatalf("heartbeat = %+v", hb)
	}
}
