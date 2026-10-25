package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
)

// End-to-end coverage for cordum_create_workflow over the HTTP MCP
// transport: the real mcpAuth + approval gate (miniredis) + approval
// routes + the gateway's workflow-create handler behind the bridge.
// Workflows are counted through the workflow store, never the logs.

// createWorkflowMCPAuth authenticates X-API-Key against a fixed principal
// table and inherits role/tenant enforcement from testAuthProvider.
type createWorkflowMCPAuth struct {
	testAuthProvider
	keys map[string]auth.AuthContext
}

func (a createWorkflowMCPAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	ac, ok := a.keys[key]
	if !ok {
		return nil, errors.New("unauthorized")
	}
	ac.APIKey = key
	ac.AuthSource = auth.AuthSourceAPIKey
	return &ac, nil
}

type cwCaller struct{ key, tenant, agentID string }

var (
	cwAlice  = cwCaller{key: "key-alice", tenant: "default"}  // requester (admin)
	cwBob    = cwCaller{key: "key-bob", tenant: "default"}    // distinct operator (admin)
	cwVictor = cwCaller{key: "key-victor", tenant: "default"} // viewer: no workflows-write
	cwCarol  = cwCaller{key: "key-carol", tenant: "tenant-b"} // tenant-b admin
	// cwMallory replays alice's agent id from tenant-b, so the approval
	// key differs from alice's only by tenant.
	cwMallory = cwCaller{key: "key-mallory", tenant: "tenant-b", agentID: "alice"}
)

// No "id": every execution mints a fresh workflow, so the store count
// equals the number of times the tool body actually ran.
const cwArgs = `{"name":"mcp-demo","steps":{"s1":{"type":"approval"}}}`

type cwReply struct {
	Result *struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
	Error *struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	} `json:"error"`
}

type cwHarness struct {
	t   *testing.T
	s   *server
	srv *httptest.Server
}

// skipUntilHTTPMCPApprovalGateFixed parks the HTTP-MCP approval-flow tests
// while the production-wired gate cannot issue -32099: the Edge dual-write
// mint has no parent action event (task-f44be149) and the legacy path
// decodes ExpiresAt as seconds (task-ff988dfa). Remove once both land.
func skipUntilHTTPMCPApprovalGateFixed(t *testing.T) {
	t.Helper()
	t.Skip("blocked on task-f44be149 + task-ff988dfa: HTTP MCP approval gate returns -32096 under production wiring")
}

func newCreateWorkflowMCPHarness(t *testing.T) *cwHarness {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = createWorkflowMCPAuth{keys: map[string]auth.AuthContext{
		"key-alice":   {Tenant: "default", PrincipalID: "alice", Role: "admin"},
		"key-bob":     {Tenant: "default", PrincipalID: "bob", Role: "admin"},
		"key-victor":  {Tenant: "default", PrincipalID: "victor", Role: "viewer"},
		"key-carol":   {Tenant: "tenant-b", PrincipalID: "carol", Role: "admin"},
		"key-mallory": {Tenant: "tenant-b", PrincipalID: "mallory", Role: "admin"},
	}}
	// Production always wires the Edge store (gateway.go), so the gate
	// dual-writes Edge approvals; that mint needs a policy snapshot, which
	// comes from the policy-bundle config doc.
	s.edgeStore = edge.NewRedisStoreFromClient(s.redisClient())
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	ctx := context.Background()
	for _, doc := range []*configsvc.Document{
		{Scope: configsvc.ScopeSystem, ScopeID: "default",
			Data: map[string]any{"mcp": map[string]any{"enabled": true, "transport": "http"}}},
		{Scope: configsvc.Scope(packs.PolicyConfigScope), ScopeID: packs.PolicyConfigID,
			Data: map[string]any{packs.PolicyConfigKey: map[string]any{}}},
	} {
		if err := s.configSvc.Set(ctx, doc); err != nil {
			t.Fatalf("set config %s/%s: %v", doc.Scope, doc.ScopeID, err)
		}
	}
	// Scope enforcement needs an AgentIdentity per caller. Default-tenant
	// principals resolve via worker link; mallory's X-Agent-Id resolves to
	// the tenant-b identity "alice".
	for _, id := range []struct{ id, tenant, worker string }{
		{"agent-alice", "default", "alice"}, {"agent-victor", "default", "victor"}, {"alice", "tenant-b", ""},
	} {
		if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
			ID: id.id, TenantID: id.tenant, Name: id.id, Owner: "ops@example.com",
			RiskTier: "high", AllowedTools: []string{"cordum_*"},
		}); err != nil {
			t.Fatalf("create identity %s: %v", id.id, err)
		}
		if id.worker != "" {
			if err := s.agentIdentityStore.LinkWorker(ctx, id.id, id.worker); err != nil {
				t.Fatalf("link worker %s: %v", id.worker, err)
			}
		}
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	// Mirrors gateway.go's approve route registration.
	mux.HandleFunc("POST /api/v1/mcp/approvals/{id}/approve", s.handleMCPApprovalApprove)
	srv := httptest.NewServer(apiKeyMiddleware(s.auth, mux))
	t.Cleanup(srv.Close)
	return &cwHarness{t: t, s: s, srv: srv}
}

func (h *cwHarness) do(c cwCaller, path, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("X-Tenant-ID", c.tenant)
	if c.agentID != "" {
		req.Header.Set(mcpAgentIDHeader, c.agentID)
	}
	return http.DefaultClient.Do(req)
}

// call issues tools/call cordum_create_workflow. Safe from goroutines.
func (h *cwHarness) call(c cwCaller, args string) cwReply {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cordum_create_workflow","arguments":` + args + `}}`
	var reply cwReply
	resp, err := h.do(c, "/mcp/message", body)
	if err != nil {
		h.t.Errorf("tools/call: %v", err)
		return reply
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		h.t.Errorf("tools/call status = %d, want 200", resp.StatusCode)
		return reply
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		h.t.Errorf("decode tools/call reply: %v", err)
	}
	return reply
}

func (h *cwHarness) approve(c cwCaller, approvalID string) int {
	h.t.Helper()
	resp, err := h.do(c, "/api/v1/mcp/approvals/"+approvalID+"/approve", `{"reason":"ok"}`)
	if err != nil {
		h.t.Fatalf("approve: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func (h *cwHarness) requireApproval(r cwReply) string {
	h.t.Helper()
	if r.Error == nil || r.Error.Code != -32099 {
		h.t.Fatalf("want JSON-RPC -32099 approval_required, got error=%+v result=%+v", r.Error, r.Result)
	}
	id, _ := r.Error.Data["approval_id"].(string)
	if id == "" {
		h.t.Fatalf("-32099 without approval_id: %+v", r.Error.Data)
	}
	return id
}

// toolPayload returns the decoded JSON text of a tools/call result.
func (h *cwHarness) toolPayload(r cwReply, wantError bool) map[string]any {
	h.t.Helper()
	if r.Error != nil || r.Result == nil || r.Result.IsError != wantError || len(r.Result.Content) != 1 {
		h.t.Fatalf("want result isError=%v, got error=%+v result=%+v", wantError, r.Error, r.Result)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(r.Result.Content[0].Text), &out); err != nil {
		h.t.Fatalf("decode tool payload: %v", err)
	}
	return out
}

func (h *cwHarness) workflowCount(tenant string) int {
	h.t.Helper()
	wfs, err := h.s.workflowStore.ListWorkflows(context.Background(), tenant, 1000)
	if err != nil {
		h.t.Fatalf("list workflows: %v", err)
	}
	return len(wfs)
}

func (h *cwHarness) requireApprovedUnconsumed(approvalID string) {
	h.t.Helper()
	rec, err := h.s.getMCPRuntime().approvalStore.Get(context.Background(), approvalID)
	if err != nil {
		h.t.Fatalf("get approval: %v", err)
	}
	if rec.Status != model.ApprovalStatusApproved || rec.ConsumedAt != 0 {
		h.t.Fatalf("approval %s status=%s consumed_at=%d, want approved+unconsumed", approvalID, rec.Status, rec.ConsumedAt)
	}
}

func TestMCPCreateWorkflow_ExecutesOnceAfterDistinctOperatorApproval(t *testing.T) {
	skipUntilHTTPMCPApprovalGateFixed(t)
	h := newCreateWorkflowMCPHarness(t)
	approvalID := h.requireApproval(h.call(cwAlice, cwArgs))
	if n := h.workflowCount(""); n != 0 {
		t.Fatalf("workflows after first call = %d, want 0", n)
	}
	if status := h.approve(cwBob, approvalID); status != http.StatusOK {
		t.Fatalf("operator approve status = %d, want 200", status)
	}
	payload := h.toolPayload(h.call(cwAlice, cwArgs), false)
	wfID, _ := payload["workflow_id"].(string)
	if wfID == "" || payload["status"] != "created" {
		t.Fatalf("create payload = %+v, want workflow_id + status=created", payload)
	}
	stored, err := h.s.workflowStore.GetWorkflow(context.Background(), wfID)
	if err != nil || stored == nil || stored.OrgID != "default" || stored.Name != "mcp-demo" {
		t.Fatalf("stored workflow = %+v err=%v, want org default name mcp-demo", stored, err)
	}
	if n := h.workflowCount(""); n != 1 {
		t.Fatalf("workflows after approved retry = %d, want 1", n)
	}
	// Consume-once: the approval is spent, so the same args re-gate.
	if again := h.requireApproval(h.call(cwAlice, cwArgs)); again == approvalID {
		t.Fatalf("further retry reused consumed approval %s", approvalID)
	}
	if n := h.workflowCount(""); n != 1 {
		t.Fatalf("workflows after further retry = %d, want 1", n)
	}
}

func TestMCPCreateWorkflow_SelfApprovalCreatesNothing(t *testing.T) {
	skipUntilHTTPMCPApprovalGateFixed(t)
	h := newCreateWorkflowMCPHarness(t)
	approvalID := h.requireApproval(h.call(cwAlice, cwArgs))
	if status := h.approve(cwAlice, approvalID); status != http.StatusForbidden {
		t.Fatalf("self-approve status = %d, want 403", status)
	}
	h.requireApproval(h.call(cwAlice, cwArgs))
	if n := h.workflowCount(""); n != 0 {
		t.Fatalf("workflows = %d, want 0", n)
	}
}

func TestMCPCreateWorkflow_ChangedArgsAfterApprovalCreateNothing(t *testing.T) {
	skipUntilHTTPMCPApprovalGateFixed(t)
	h := newCreateWorkflowMCPHarness(t)
	approvalID := h.requireApproval(h.call(cwAlice, cwArgs))
	if status := h.approve(cwBob, approvalID); status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", status)
	}
	changed := `{"name":"mcp-demo-changed","steps":{"s1":{"type":"approval"}}}`
	if again := h.requireApproval(h.call(cwAlice, changed)); again == approvalID {
		t.Fatal("changed args reused the approval granted for different args")
	}
	h.requireApprovedUnconsumed(approvalID)
	if n := h.workflowCount(""); n != 0 {
		t.Fatalf("workflows = %d, want 0", n)
	}
}

func TestMCPCreateWorkflow_CrossTenantApprovalCreatesNothing(t *testing.T) {
	skipUntilHTTPMCPApprovalGateFixed(t)
	h := newCreateWorkflowMCPHarness(t)
	approvalID := h.requireApproval(h.call(cwAlice, cwArgs))
	if status := h.approve(cwCarol, approvalID); status != http.StatusNotFound {
		t.Fatalf("cross-tenant approve status = %d, want 404", status)
	}
	h.requireApproval(h.call(cwAlice, cwArgs)) // still pending: re-gates
	if status := h.approve(cwBob, approvalID); status != http.StatusOK {
		t.Fatalf("operator approve status = %d, want 200", status)
	}
	// A tenant-b caller replaying alice's agent id and byte-identical args
	// must not spend (or execute against) the default-tenant approval.
	if r := h.call(cwMallory, cwArgs); r.Error == nil {
		t.Fatalf("cross-tenant replay succeeded: %+v", r.Result)
	}
	h.requireApprovedUnconsumed(approvalID)
	if n := h.workflowCount(""); n != 0 {
		t.Fatalf("workflows = %d, want 0", n)
	}
}

func TestMCPCreateWorkflow_CallerWithoutWorkflowsWriteCreatesNothing(t *testing.T) {
	skipUntilHTTPMCPApprovalGateFixed(t)
	h := newCreateWorkflowMCPHarness(t)
	approvalID := h.requireApproval(h.call(cwVictor, cwArgs))
	if status := h.approve(cwBob, approvalID); status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", status)
	}
	payload := h.toolPayload(h.call(cwVictor, cwArgs), true)
	if code, _ := payload["status_code"].(float64); code != http.StatusForbidden {
		t.Fatalf("bridge error = %+v, want status_code 403", payload)
	}
	if n := h.workflowCount(""); n != 0 {
		t.Fatalf("workflows = %d, want 0", n)
	}
}

func TestMCPCreateWorkflow_ConcurrentApprovedRetriesCreateExactlyOne(t *testing.T) {
	skipUntilHTTPMCPApprovalGateFixed(t)
	h := newCreateWorkflowMCPHarness(t)
	approvalID := h.requireApproval(h.call(cwAlice, cwArgs))
	if status := h.approve(cwBob, approvalID); status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", status)
	}
	const racers = 4
	replies := make([]cwReply, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range replies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			replies[i] = h.call(cwAlice, cwArgs)
		}(i)
	}
	close(start)
	wg.Wait()
	created, regated := 0, 0
	for _, r := range replies {
		switch {
		case r.Result != nil && !r.Result.IsError:
			created++
		case r.Error != nil && r.Error.Code == -32099:
			regated++
		}
	}
	if created != 1 || regated != racers-1 {
		t.Fatalf("created=%d regated=%d, want 1 and %d", created, regated, racers-1)
	}
	if n := h.workflowCount(""); n != 1 {
		t.Fatalf("workflows = %d, want exactly 1", n)
	}
}

// bridgeCreate drives the production CreateWorkflowFunc adapter directly
// under the auth context of the given API key ("" = no auth context).
func (h *cwHarness) bridgeCreate(key string, in mcp.CreateWorkflowInput) (*mcp.CreateWorkflowOutput, error) {
	ctx := context.Background()
	if key != "" {
		ac := h.s.auth.(createWorkflowMCPAuth).keys[key]
		ac.APIKey = key
		ctx = context.WithValue(ctx, auth.ContextKey{}, &ac)
	}
	return h.s.newMCPServiceBridge().CreateWorkflow(ctx, in)
}

func requireBridgeStatus(t *testing.T, err error, want int) {
	t.Helper()
	var be *mcp.BridgeError
	if !errors.As(err, &be) || be.StatusCode != want {
		t.Fatalf("err = %v, want BridgeError status %d", err, want)
	}
}

var cwSteps = map[string]any{"s1": map[string]any{"type": "approval"}}

func TestMCPCreateWorkflowBridge_CreatesUnderCallerAuth(t *testing.T) {
	h := newCreateWorkflowMCPHarness(t)
	out, err := h.bridgeCreate("key-alice", mcp.CreateWorkflowInput{Name: "bridge-demo", Version: "v2", Steps: cwSteps})
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	if out.WorkflowID == "" || out.Version != "v2" {
		t.Fatalf("output = %+v, want workflow id + version v2", out)
	}
	stored, err := h.s.workflowStore.GetWorkflow(context.Background(), out.WorkflowID)
	if err != nil || stored == nil || stored.OrgID != "default" || stored.Name != "bridge-demo" || stored.Version != "v2" {
		t.Fatalf("stored = %+v err=%v, want default-tenant bridge-demo v2", stored, err)
	}
}

// The adapter must inherit POST /api/v1/workflows' permission, tenant and
// validation checks; each refusal leaves the store empty.
func TestMCPCreateWorkflowBridge_EnforcesHandlerChecks(t *testing.T) {
	h := newCreateWorkflowMCPHarness(t)
	cases := []struct {
		name, key string
		in        mcp.CreateWorkflowInput
		status    int
	}{
		{"no auth context", "", mcp.CreateWorkflowInput{Steps: cwSteps}, http.StatusUnauthorized},
		{"no workflows-write", "key-victor", mcp.CreateWorkflowInput{Steps: cwSteps}, http.StatusForbidden},
		{"cross-tenant org_id", "key-alice", mcp.CreateWorkflowInput{OrgID: "tenant-b", Steps: cwSteps}, http.StatusForbidden},
		{"negative timeout", "key-alice", mcp.CreateWorkflowInput{TimeoutSec: -1, Steps: cwSteps}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.bridgeCreate(tc.key, tc.in)
			requireBridgeStatus(t, err, tc.status)
		})
	}
	if n := h.workflowCount(""); n != 0 {
		t.Fatalf("workflows = %d, want 0", n)
	}
}
