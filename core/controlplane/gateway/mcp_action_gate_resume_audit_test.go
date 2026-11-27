package gateway

// TestActionGateResumeAudit (task-74f4c01e) drives the production HTTP MCP
// route with the policy gate on, a real edge.RedisStore and the gateway's
// invocation auditor wired to a recording sink. The identity's scope
// preapproval covers a destructive tool that is ALSO RequiresApproval:
//   - the MCPGate taint-unavailable hold (the agr setup) holds it for an Edge
//     approval, which preapproval never waives;
//   - its approved `_approval_ref` resume marks the invocation consumed, then
//     passes the registry gate's preapproval bypass.
//
// The mcp.tool_invocation event of that resume must keep
// approval_status=consumed and approval_id=<the Edge ref>, matching the Edge
// mcp.tool.post ApprovalRef. A registry-only preapproved tool keeps
// approval_status=preapproved with no approval_id. Every subtest logs one
// OUTCOME line before its label assertions, so the outcome tables at BASE, at
// HEAD and under mutation M1 can be compared: the fix is label-only.
//
// Not parallel: t.Setenv. Subtests share one session and run in order.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

const (
	// rpaDeleteTool is dual-gated: destructive (MCPGate glob *delete*) and
	// RequiresApproval (registry gate).
	rpaDeleteTool = "rpa_delete_widget"
	// rpaInstallTool is RequiresApproval only: the registry-gate control.
	rpaInstallTool = "rpa_install_widget"
)

var rpaPendingRE = regexp.MustCompile(`^approval pending: (edge_appr_[^[:space:]]+)$`)

// rpaAuditSink records every SIEM event the gateway exports.
type rpaAuditSink struct {
	mu     sync.Mutex
	events []audit.SIEMEvent
}

func (s *rpaAuditSink) Send(e audit.SIEMEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	s.mu.Unlock()
}

func (s *rpaAuditSink) Close() error { return nil }

// rpaInvocations returns the mcp.tool_invocation events recorded for tool.
func (s *rpaAuditSink) rpaInvocations(tool string) []audit.SIEMEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []audit.SIEMEvent
	for _, e := range s.events {
		if e.EventType == audit.EventMCPToolInvocation && e.Extra["tool_name"] == tool {
			out = append(out, e)
		}
	}
	return out
}

type rpaHarness struct {
	*agrHarness
	sink     *rpaAuditSink
	deletes  atomic.Int64 // D
	installs atomic.Int64 // I
}

// rpaNewServer is agrNewServer with the audit sink set as s.auditExporter
// BEFORE registerMCPRoutes (handlers_mcp.go wires the invocation auditor only
// then), the two rpa tools instead of the agr tool, and the identity scoped
// and preapproved for exactly those two tools.
func rpaNewServer(t *testing.T) *rpaHarness {
	t.Helper()
	t.Setenv("CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE", "true")
	t.Setenv(mcp.TaintBackendEnvVar, "redis")
	t.Setenv(mcp.DedupeBackendEnvVar, "redis")
	s, _, _ := newTestGateway(t)
	s.auth = pwAuth{keys: map[string]auth.AuthContext{
		"key-agr-alice": {Tenant: agrTenant, PrincipalID: "agr-alice", Role: "admin"},
		"key-agr-bob":   {Tenant: agrTenant, PrincipalID: "agr-bob", Role: "admin"},
	}}
	h := &rpaHarness{sink: &rpaAuditSink{}, agrHarness: &agrHarness{
		s: s, client: &http.Client{Timeout: 30 * time.Second}, redis: pgWidePoolClient(t, s),
	}}
	h.store = edge.NewRedisStoreFromClient(h.redis)
	s.edgeStore = h.store
	s.auditExporter = h.sink
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	s.wireActionGatePipeline()
	setMCPConfigForBoot(t, s, true, true)
	agrSeedIdentity(t, s)
	h.rpaScope(t)
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	mux.HandleFunc("POST /api/v1/edge/approvals/{approval_ref}/approve", s.handleApproveEdgeApproval)
	h.rpaRegister(t, rpaDeleteTool, &h.deletes)
	h.rpaRegister(t, rpaInstallTool, &h.installs)
	h.srv = httptest.NewServer(apiKeyMiddleware(s.auth, mux))
	t.Cleanup(h.srv.Close)
	h.sessionID = openMCPSSE(t, h.srv.URL, "key-agr-alice", "")
	h.poisonTaintLookup(t)
	return h
}

// rpaScope sets AllowedTools and PreapprovedMutatingTools of agrAgentID to
// exactly the two rpa tools.
func (h *rpaHarness) rpaScope(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	tools := []string{rpaDeleteTool, rpaInstallTool}
	if _, err := h.s.agentIdentityStore.Update(ctx, agrTenant, agrAgentID, store.AgentIdentity{
		AllowedTools: tools, PreapprovedMutatingTools: tools,
	}); err != nil {
		t.Fatalf("scope %s: %v", agrAgentID, err)
	}
	lookup := newAgentIdentityPreapprovalLookup(h.s.agentIdentityStore)
	for _, tool := range tools {
		if !lookup.IsPreapproved(ctx, agrTenant, agrAgentID, tool) {
			t.Fatalf("%s is not preapproved for %s after the update", agrAgentID, tool)
		}
	}
}

// rpaRegister registers a RequiresApproval tool whose result is "<name> ran".
func (h *rpaHarness) rpaRegister(t *testing.T, name string, calls *atomic.Int64) {
	t.Helper()
	runtime := h.s.getMCPRuntime()
	if runtime == nil || runtime.server == nil || !runtime.server.HasPolicyGate() || !runtime.server.HasApprovalHold() {
		t.Fatal("MCP runtime booted without the policy gate and the approval hold")
	}
	tool := mcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}, RequiresApproval: true}
	if err := runtime.toolRegistry.Register(tool, func(_ context.Context, _ json.RawMessage) (*mcp.ToolCallResult, error) {
		calls.Add(1)
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: name + " ran"}}}, nil
	}); err != nil {
		t.Fatalf("register %s: %v", name, err)
	}
}

// rpaOutcome logs the authorization outcome of a call: JSON-RPC result or
// code, plus both execution counters.
func (h *rpaHarness) rpaOutcome(t *testing.T, name string, r agrReply) {
	t.Helper()
	rpc := "ok"
	if r.Error != nil {
		rpc = strconv.Itoa(r.Error.Code)
	}
	t.Logf("OUTCOME %s rpc=%s D=%d I=%d", name, rpc, h.deletes.Load(), h.installs.Load())
}

// rpaPostEventIDs returns the IDs of the session's mcp.tool.post events.
func (h *rpaHarness) rpaPostEventIDs(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, e := range h.events(t, edge.EventKindMCPToolPost) {
		out[e.EventID] = e.ApprovalRef
	}
	return out
}

func TestActionGateResumeAudit(t *testing.T) {
	h := rpaNewServer(t)
	const deleteArgs = `{"widget":"w-del"}`
	var ref string

	t.Run("a_registry_only_preapproved_control", func(t *testing.T) {
		r := h.paCall(t, rpaInstallTool, `{"widget":"w-1"}`)
		h.rpaOutcome(t, "a", r)
		if !r.succeeded(rpaInstallTool+" ran") || h.installs.Load() != 1 {
			t.Errorf("install = %s (I=%d); want its result and I=1", r, h.installs.Load())
		}
		evs := h.sink.rpaInvocations(rpaInstallTool)
		if len(evs) != 1 {
			t.Fatalf("invocation events for %s = %d, want 1", rpaInstallTool, len(evs))
		}
		if got := evs[0].Extra["approval_status"]; got != "preapproved" {
			t.Errorf("approval_status = %q, want preapproved", got)
		}
		if id, ok := evs[0].Extra["approval_id"]; ok {
			t.Errorf("approval_id = %q present, want no approval_id key", id)
		}
	})
	if !t.Run("b_hold_returns_edge_ref", func(t *testing.T) {
		r := h.paCall(t, rpaDeleteTool, deleteArgs)
		h.rpaOutcome(t, "b", r)
		m := rpaPendingRE.FindStringSubmatch(r.text())
		if !r.succeeded(r.text()) || m == nil || h.deletes.Load() != 0 {
			t.Fatalf("held call = %s (D=%d); want `approval pending: edge_appr_...` and D=0", r, h.deletes.Load())
		}
		ref = m[1]
	}) {
		t.FailNow()
	}
	if !t.Run("c_approve_as_other_principal", func(t *testing.T) {
		h.approve(t, ref)
		t.Logf("OUTCOME c rpc=approved D=%d I=%d", h.deletes.Load(), h.installs.Load())
	}) {
		t.FailNow()
	}
	t.Run("d_resume_records_consumed_edge_ref", func(t *testing.T) {
		eventsBefore, postsBefore := len(h.sink.rpaInvocations(rpaDeleteTool)), h.rpaPostEventIDs(t)
		r := h.paCall(t, rpaDeleteTool, string(pgWithRef(t, json.RawMessage(deleteArgs), ref)))
		h.rpaOutcome(t, "d", r)
		if !r.succeeded(rpaDeleteTool+" ran") || h.deletes.Load() != 1 {
			t.Errorf("resume = %s (D=%d); want the tool's own result and D=1", r, h.deletes.Load())
		}
		evs := h.sink.rpaInvocations(rpaDeleteTool)[eventsBefore:]
		if len(evs) != 1 {
			t.Fatalf("new invocation events for %s = %d, want 1", rpaDeleteTool, len(evs))
		}
		want := map[string]string{"approval_status": "consumed", "approval_id": ref, "result_type": "ok"}
		for k, v := range want {
			if got := evs[0].Extra[k]; got != v {
				t.Errorf("invocation Extra[%s] = %q, want %q", k, got, v)
			}
		}
		if evs[0].Decision != "allow" {
			t.Errorf("invocation decision = %q, want allow", evs[0].Decision)
		}
		var newPosts []string
		for id, approvalRef := range h.rpaPostEventIDs(t) {
			if _, seen := postsBefore[id]; !seen {
				newPosts = append(newPosts, approvalRef)
			}
		}
		if len(newPosts) != 1 || newPosts[0] != ref {
			t.Errorf("new mcp.tool.post ApprovalRefs = %q, want exactly [%q]", newPosts, ref)
		}
	})
	t.Run("e_replay_refused", func(t *testing.T) {
		r := h.paCall(t, rpaDeleteTool, string(pgWithRef(t, json.RawMessage(deleteArgs), ref)))
		h.rpaOutcome(t, "e", r)
		var data struct {
			Kind string `json:"kind"`
		}
		if r.Error == nil || r.Error.Code != -32096 || json.Unmarshal(r.Error.Data, &data) != nil || data.Kind != "not_found" {
			t.Errorf("replay = %s; want JSON-RPC -32096 kind not_found", r)
		}
		if n := h.deletes.Load(); n != 1 {
			t.Errorf("D = %d after the replay, want 1", n)
		}
	})
}
