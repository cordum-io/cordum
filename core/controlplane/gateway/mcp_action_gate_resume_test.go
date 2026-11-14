package gateway

// TestActionGateResume (task-cd9df90a) drives the production HTTP MCP route
// with mcp.policy_gate_enabled=true against a real edge.RedisStore on the
// gateway's miniredis. An action-gate REQUIRE_HUMAN returns an Edge
// approval_ref, and the approved `_approval_ref` resume executes exactly once.
//
// REQUIRE_HUMAN comes from the real MCPGate taint hold (ruling C7); no
// production seam is involved:
//   - CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE=true is set before boot, because
//     wireActionGatePipeline reads it once;
//   - the tool agr_delete_record matches the MCPGate's default destructive glob
//     *delete*, and RequiresApproval=false keeps the registry approval gate out;
//   - the session-taint key mcp:taint:default:<session> holds a Redis hash, so
//     RedisTaintStore.GetTaint fails with WRONGTYPE, EvaluateToolCall stamps
//     TaintLookupFailed, and the MCPGate holds the destructive call
//     (sub_reason taint_lookup_unavailable_failclosed).
//
// Not parallel: t.Setenv. Subtests share one session and run in order; the
// later ones assert deltas so each still reports its own outcome.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/redis/go-redis/v9"
)

const (
	agrToolName = "agr_delete_record"
	agrAgentID  = "agent-agr-alice"
	agrTenant   = "default"
	// agrInvariantDeny is a SecOps invariant bundle that hard-denies the tool.
	agrInvariantDeny = `version: "1"
rules:
  - id: agr-inv-deny-delete
    match:
      mcp:
        deny_tools: ["agr_delete_record"]
    decision: deny
    reason: SecOps invariant - agr deletes forbidden
`
)

type agrReply struct {
	Result *mcp.ToolCallResult `json:"result"`
	Error  *struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	} `json:"error"`
}

func (r agrReply) text() string {
	if r.Result == nil || len(r.Result.Content) != 1 {
		return ""
	}
	return r.Result.Content[0].Text
}

// succeeded reports a non-error tool result whose text is want.
func (r agrReply) succeeded(want string) bool {
	return r.Error == nil && r.Result != nil && !r.Result.IsError && r.text() == want
}

func (r agrReply) String() string {
	switch {
	case r.Error != nil:
		return fmt.Sprintf("error{code=%d data=%s}", r.Error.Code, r.Error.Data)
	case r.Result != nil:
		return fmt.Sprintf("result{isError=%t text=%q}", r.Result.IsError, r.text())
	default:
		return "empty reply"
	}
}

type agrHarness struct {
	s      *server
	srv    *httptest.Server
	client *http.Client
	redis  *redis.Client
	store  edge.Store
	calls  atomic.Int64
	sawRef atomic.Int64
	rpcID  atomic.Int64
	// sessionID is the SSE session agr-alice opened; every MCP call names it.
	sessionID string
}

// agrNewServer boots the HTTP MCP routes as production does with the policy
// gate on, plus the taint-hold preconditions in the file header.
func agrNewServer(t *testing.T) *agrHarness {
	t.Helper()
	t.Setenv("CORDUM_MCP_TAINT_FAILCLOSED_DESTRUCTIVE", "true")
	t.Setenv(mcp.TaintBackendEnvVar, "redis")
	t.Setenv(mcp.DedupeBackendEnvVar, "redis")
	s, _, _ := newTestGateway(t)
	s.auth = pwAuth{keys: map[string]auth.AuthContext{
		"key-agr-alice": {Tenant: agrTenant, PrincipalID: "agr-alice", Role: "admin"},
		"key-agr-bob":   {Tenant: agrTenant, PrincipalID: "agr-bob", Role: "admin"},
	}}
	h := &agrHarness{s: s, client: &http.Client{Timeout: 30 * time.Second}, redis: pgWidePoolClient(t, s)}
	h.store = edge.NewRedisStoreFromClient(h.redis)
	s.edgeStore = h.store
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	s.wireActionGatePipeline()
	setMCPConfigForBoot(t, s, true, true)
	agrSeedIdentity(t, s)
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	mux.HandleFunc("POST /api/v1/edge/approvals/{approval_ref}/approve", s.handleApproveEdgeApproval)
	h.registerTool(t)
	h.srv = httptest.NewServer(apiKeyMiddleware(s.auth, mux))
	t.Cleanup(h.srv.Close)
	h.sessionID = openMCPSSE(t, h.srv.URL, "key-agr-alice", "")
	h.poisonTaintLookup(t)
	return h
}

func agrSeedIdentity(t *testing.T, s *server) {
	t.Helper()
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{Scope: configsvc.Scope(packs.PolicyConfigScope),
		ScopeID: packs.PolicyConfigID, Data: map[string]any{packs.PolicyConfigKey: map[string]any{}}}); err != nil {
		t.Fatalf("seed policy bundle doc: %v", err)
	}
	if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID: agrAgentID, TenantID: agrTenant, Name: agrAgentID, Owner: "ops@example.com", RiskTier: "high",
		AllowedServers: []string{mcpPolicyServerName}, AllowedTools: []string{"agr_*"},
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, agrAgentID, "agr-alice"); err != nil {
		t.Fatalf("link worker: %v", err)
	}
	if snapshot := s.mcpPolicySnapshotFunc()(ctx); snapshot == "" {
		t.Fatal("policy snapshot is empty; the approval hold needs a policy bundle snapshot")
	}
}

// registerTool registers the destructive test tool with a side-effect counter.
func (h *agrHarness) registerTool(t *testing.T) {
	t.Helper()
	runtime := h.s.getMCPRuntime()
	if runtime == nil || runtime.server == nil || !runtime.server.HasPolicyGate() || !runtime.server.HasApprovalHold() {
		t.Fatal("MCP runtime booted without the policy gate and the approval hold")
	}
	tool := mcp.Tool{Name: agrToolName, InputSchema: map[string]any{"type": "object"}}
	if err := runtime.toolRegistry.Register(tool, func(_ context.Context, args json.RawMessage) (*mcp.ToolCallResult, error) {
		h.calls.Add(1)
		if strings.Contains(string(args), "_approval_ref") {
			h.sawRef.Add(1)
		}
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: agrDeleted(args)}}}, nil
	}); err != nil {
		t.Fatalf("register %s: %v", agrToolName, err)
	}
}

// agrDeleted is the tool's result for args.
func agrDeleted(args json.RawMessage) string {
	var in struct {
		Record string `json:"record"`
	}
	_ = json.Unmarshal(args, &in)
	return "deleted " + in.Record
}

// poisonTaintLookup stores a hash at the session's taint key, so every GET of
// it fails with WRONGTYPE and the session-taint lookup errors (C7).
func (h *agrHarness) poisonTaintLookup(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	key := mcp.MCPTaintKeyPrefix + agrTenant + ":" + h.sessionID
	if err := h.redis.HSet(ctx, key, "poison", "wrongtype").Err(); err != nil {
		t.Fatalf("poison taint key: %v", err)
	}
	if _, _, err := mcp.NewRedisTaintStore(h.redis, time.Minute).GetTaint(ctx, agrTenant, h.sessionID); err == nil {
		t.Fatal("taint lookup succeeded; want the WRONGTYPE failure that drives the MCPGate hold")
	}
}

func (h *agrHarness) post(key, path, body string, mcpCall bool) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", agrTenant)
	if mcpCall {
		req.Header.Set("X-Agent-Id", agrAgentID)
		req.Header.Set("X-MCP-Session-ID", h.sessionID)
	}
	return h.client.Do(req)
}

// call issues tools/call for the tool as agr-alice on h.sessionID.
// Goroutine-safe: reports with t.Errorf.
func (h *agrHarness) call(t *testing.T, args string) agrReply {
	t.Helper()
	return h.callTool(t, agrToolName, args)
}

func (h *agrHarness) callTool(t *testing.T, name, args string) agrReply {
	t.Helper()
	id := strconv.FormatInt(h.rpcID.Add(1), 10)
	body := `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + name + `","arguments":` + args + `}}`
	var reply agrReply
	resp, err := h.post("key-agr-alice", "/mcp/message", body, true)
	if err != nil {
		t.Errorf("tools/call: %v", err)
		return reply
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("tools/call HTTP status = %d, want 200", resp.StatusCode)
		return reply
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Errorf("decode tools/call reply: %v", err)
	}
	return reply
}

func (h *agrHarness) resume(t *testing.T, args, ref string) agrReply {
	t.Helper()
	return h.resumeTool(t, agrToolName, args, ref)
}

func (h *agrHarness) resumeTool(t *testing.T, name, args, ref string) agrReply {
	t.Helper()
	return h.callTool(t, name, string(pgWithRef(t, json.RawMessage(args), ref)))
}

// requirePending asserts the action-gate hold and returns its Edge ref.
func (h *agrHarness) requirePending(t *testing.T, r agrReply) string {
	t.Helper()
	ref, ok := strings.CutPrefix(r.text(), "approval pending: ")
	if !r.succeeded(r.text()) || !ok || !strings.HasPrefix(ref, edge.ApprovalRefPrefix) {
		t.Fatalf("got %s; want an `approval pending: %s...` result", r, edge.ApprovalRefPrefix)
	}
	return ref
}

// approve approves ref as agr-bob, a principal other than the caller.
func (h *agrHarness) approve(t *testing.T, ref string) {
	t.Helper()
	resp, err := h.post("key-agr-bob", "/api/v1/edge/approvals/"+ref+"/approve", `{"reason":"ok"}`, false)
	if err != nil {
		t.Fatalf("approve %s: %v", ref, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve %s as agr-bob: status %d, want 200", ref, resp.StatusCode)
	}
}

// heldAndApproved holds a call for args and approves its ref.
func (h *agrHarness) heldAndApproved(t *testing.T, args string) string {
	t.Helper()
	return h.heldAndApprovedTool(t, agrToolName, args)
}

func (h *agrHarness) heldAndApprovedTool(t *testing.T, name, args string) string {
	t.Helper()
	ref := h.requirePending(t, h.callTool(t, name, args))
	h.approve(t, ref)
	return ref
}

func (h *agrHarness) approval(t *testing.T, ref string) *edge.EdgeApproval {
	t.Helper()
	a, found, err := h.store.GetApproval(context.Background(), agrTenant, ref)
	if err != nil || !found || a == nil {
		t.Fatalf("GetApproval(%s) = %+v, %v, %v", ref, a, found, err)
	}
	return a
}

// events lists the session's events of one kind.
func (h *agrHarness) events(t *testing.T, kind edge.EventKind) []edge.AgentActionEvent {
	t.Helper()
	page, err := h.store.ListEvents(context.Background(), edge.ListEventsQuery{
		TenantID: agrTenant, SessionID: h.sessionID, ExecutionID: "mcp-" + h.sessionID, Kind: kind, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListEvents(%s): %v", kind, err)
	}
	return page.Items
}

func TestActionGateResume(t *testing.T) {
	h := agrNewServer(t)
	const argsA = `{"record":"rec-a"}`
	var refA string
	if !t.Run("a_hold_returns_pending_ref", func(t *testing.T) { refA = h.holdBindsPreEvent(t, argsA) }) ||
		!t.Run("b_approve_as_other_principal", func(t *testing.T) { h.approve(t, refA) }) {
		return
	}
	t.Run("c_resume_executes_once", func(t *testing.T) { h.resumeExecutesOnce(t, argsA, refA) })
	t.Run("d_replay_is_refused", func(t *testing.T) {
		before := h.calls.Load()
		if r := h.resume(t, argsA, refA); r.Error == nil || r.Error.Code != -32096 {
			t.Fatalf("replay = %s; want JSON-RPC -32096", r)
		}
		if n := h.calls.Load(); n != before {
			t.Fatalf("tool executions = %d after the replay, want %d", n, before)
		}
	})
	t.Run("e_concurrent_resumes_execute_once", func(t *testing.T) { h.concurrentResumesExecuteOnce(t, `{"record":"rec-e"}`) })
	t.Run("f_invariant_floor_denies_resume", func(t *testing.T) { h.invariantDeniesResume(t, `{"record":"rec-f"}`) })
	t.Run("g_mismatched_args_refused_without_remint", func(t *testing.T) {
		ref := h.heldAndApproved(t, `{"record":"rec-g"}`)
		before, approvals := h.calls.Load(), pgApprovalCount(t, h.store, agrTenant)
		r := h.resume(t, `{"record":"rec-g2"}`, ref)
		if r.Error == nil || r.Error.Code != -32096 || !strings.Contains(string(r.Error.Data), `"args_mismatch"`) {
			t.Fatalf("resume with other args = %s; want JSON-RPC -32096 args_mismatch", r)
		}
		if n := h.calls.Load(); n != before {
			t.Errorf("tool executions = %d, want %d", n, before)
		}
		if n := pgApprovalCount(t, h.store, agrTenant); n != approvals {
			t.Errorf("Edge approvals = %d after the refused resume, want %d (no re-mint)", n, approvals)
		}
	})
}

// holdBindsPreEvent: the first call is held with an Edge ref whose approval
// binds the session's one mcp.tool.pre event, and nothing executes.
func (h *agrHarness) holdBindsPreEvent(t *testing.T, args string) string {
	t.Helper()
	ref := h.requirePending(t, h.call(t, args))
	if n := h.calls.Load(); n != 0 {
		t.Fatalf("tool executions = %d, want 0", n)
	}
	approval := h.approval(t, ref)
	pres := h.events(t, edge.EventKindMCPToolPre)
	if approval.Status != edge.ApprovalStatusPending || len(pres) != 1 || approval.EventID != pres[0].EventID {
		t.Fatalf("approval status %q event %q, pre events %+v; want a pending approval bound to the one pre event",
			approval.Status, approval.EventID, pres)
	}
	return ref
}

// resumeExecutesOnce: the approved resume returns the tool's own result (not a
// pending or replayed one), runs the tool once with the ref stripped, consumes
// the approval, and its post event carries the ref. The executed result then
// replaces the hold's cached pending one (C3).
func (h *agrHarness) resumeExecutesOnce(t *testing.T, args, ref string) {
	t.Helper()
	before, want := h.calls.Load(), agrDeleted(json.RawMessage(args))
	r := h.resume(t, args, ref)
	if !r.succeeded(want) {
		t.Fatalf("resume = %s; want the tool result %q", r, want)
	}
	if n := h.calls.Load(); n != before+1 || h.sawRef.Load() != 0 {
		t.Fatalf("tool executions = %d (saw _approval_ref %d times); want %d with stripped args", n, h.sawRef.Load(), before+1)
	}
	h.replaysExecuted(t, args, r.Result, before+1)
	if a := h.approval(t, ref); a.ConsumedAt == nil || a.ConsumedAt.IsZero() {
		t.Errorf("approval %s ConsumedAt = %v; want set", ref, a.ConsumedAt)
	}
	carried := false
	for _, e := range h.events(t, edge.EventKindMCPToolPost) {
		carried = carried || e.ApprovalRef == ref
	}
	if !carried {
		t.Errorf("no mcp.tool.post event carries ApprovalRef %q", ref)
	}
}

// replaysExecuted: an identical ref-less call replays the executed result's
// Redis dedupe record (its result_sha256), not the hold's pending one, and
// runs nothing.
func (h *agrHarness) replaysExecuted(t *testing.T, args string, executed *mcp.ToolCallResult, calls int64) {
	t.Helper()
	payload, err := json.Marshal(executed)
	if err != nil {
		t.Fatalf("marshal the executed result: %v", err)
	}
	sum := sha256.Sum256(payload)
	later := h.call(t, args)
	if !strings.Contains(later.text(), "result_sha256="+hex.EncodeToString(sum[:])) || h.calls.Load() != calls {
		t.Errorf("ref-less call after the resume = %s (executions %d); want the executed result's dedupe replay, %d executions",
			later, h.calls.Load(), calls)
	}
}

// concurrentResumesExecuteOnce: four simultaneous resumes of one approved ref
// run the tool once; the other three are refused with -32096.
func (h *agrHarness) concurrentResumesExecuteOnce(t *testing.T, args string) {
	t.Helper()
	ref := h.heldAndApproved(t, args)
	before := h.calls.Load()
	replies := make([]agrReply, 4)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			replies[i] = h.resume(t, args, ref)
		}()
	}
	wg.Wait()
	executed, refused := 0, 0
	for i, r := range replies {
		switch {
		case r.succeeded(agrDeleted(json.RawMessage(args))):
			executed++
		case r.Error != nil && r.Error.Code == -32096:
			refused++
		default:
			t.Errorf("reply %d = %s; want the tool result or JSON-RPC -32096", i, r)
		}
	}
	if executed != 1 || refused != 3 {
		t.Fatalf("concurrent resumes: %d executed, %d refused; want 1 and 3", executed, refused)
	}
	if n := h.calls.Load(); n != before+1 {
		t.Fatalf("tool executions = %d, want %d", n, before+1)
	}
}

// invariantDeniesResume: an MCP invariant DENY added after the approval stops
// the resume although its claim succeeds (C2's floor, not policy_mismatch).
func (h *agrHarness) invariantDeniesResume(t *testing.T, args string) {
	t.Helper()
	ref := h.heldAndApproved(t, args)
	restore := h.addInvariantDeny(t)
	defer restore()
	before := h.calls.Load()
	if r := h.resume(t, args, ref); r.Error == nil || !strings.Contains(string(r.Error.Data), "denied by invariant") {
		t.Fatalf("resume under an invariant DENY = %s; want an invariant-deny error", r)
	}
	if n := h.calls.Load(); n != before {
		t.Fatalf("tool executions = %d, want %d", n, before)
	}
	if a := h.approval(t, ref); a.ConsumedAt == nil {
		t.Error("approval was not consumed; the claim, not the invariant floor, refused the resume")
	}
}

// addInvariantDeny writes agrInvariantDeny into the policy config doc keeping
// its revision and updated-at, so the policy snapshot (that updated-at, at
// one-second resolution) does not move, as for an edit inside the same second.
// Otherwise the claim alone would refuse the resume as policy_mismatch. It
// returns the restore func.
func (h *agrHarness) addInvariantDeny(t *testing.T) func() {
	t.Helper()
	return h.addInvariant(t, agrInvariantDeny, mcp.Tool{Name: agrToolName})
}

func (h *agrHarness) addInvariant(t *testing.T, bundleYAML string, probe mcp.Tool) func() {
	t.Helper()
	ctx := context.Background()
	key := fmt.Sprintf("cfg:%s:%s", packs.PolicyConfigScope, packs.PolicyConfigID)
	original, err := h.redis.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatalf("read policy doc %s: %v", key, err)
	}
	var doc configsvc.Document
	if err := json.Unmarshal(original, &doc); err != nil {
		t.Fatalf("decode policy doc: %v", err)
	}
	doc.Data = map[string]any{packs.PolicyConfigKey: map[string]any{
		policybundles.PolicyInvariantsBundleKey: map[string]any{"content": bundleYAML, "enabled": true},
	}}
	payload, err := json.Marshal(&doc)
	if err != nil {
		t.Fatalf("encode policy doc: %v", err)
	}
	snapshot := h.s.mcpPolicySnapshotFunc()(ctx)
	if err := h.redis.Set(ctx, key, payload, 0).Err(); err != nil {
		t.Fatalf("write policy doc: %v", err)
	}
	restore := func() {
		if err := h.redis.Set(ctx, key, original, 0).Err(); err != nil {
			t.Errorf("restore policy doc: %v", err)
		}
	}
	bundles, _, err := h.s.loadPolicyBundles(ctx)
	rules, rerr := loadMCPInvariantsFromBundles(bundles)
	_, denied := matchMCPInvariantDeny(rules, probe, MCPCallMetadata{Tenant: agrTenant})
	if got := h.s.mcpPolicySnapshotFunc()(ctx); err != nil || rerr != nil || !denied || got != snapshot {
		restore()
		t.Fatalf("invariant DENY in effect = %t (errs %v, %v), snapshot %q -> %q; want in effect, snapshot unchanged",
			denied, err, rerr, snapshot, got)
	}
	return restore
}

// TestActionGateInvariantScopeFloor covers ungated tools through the real HTTP
// mint, resume and ALLOW paths. The unmatched rule is a positive control.
func TestActionGateInvariantScopeFloor(t *testing.T) {
	h := agrNewServer(t)
	calls := h.registerScopeTools(t)
	t.Run("a_mint", func(t *testing.T) { h.scopeDeniesMint(t, calls["agr_delete_scoped"]) })
	t.Run("b_resume", func(t *testing.T) { h.scopeDeniesResume(t, calls["agr_delete_scoped"]) })
	t.Run("c_allow_scope", func(t *testing.T) {
		defer h.denyScope(t, "agr_records")()
		r := h.callTool(t, "agr_get_scoped", `{"record":"scope-c"}`)
		agrRequireInvariantDeny(t, r)
		if n := calls["agr_get_scoped"].Load(); n != 0 {
			t.Errorf("scoped ALLOW executed %d times, want 0", n)
		}
	})
	t.Run("d_allow_server", func(t *testing.T) {
		defer h.denyScope(t, mcpPolicyServerName)()
		for _, name := range []string{"agr_get_plain", "agr_get_scoped"} {
			before := calls[name].Load()
			agrRequireInvariantDeny(t, h.callTool(t, name, `{"record":"scope-d"}`))
			if n := calls[name].Load(); n != before {
				t.Errorf("%s executed under server deny: %d -> %d", name, before, n)
			}
		}
	})
	t.Run("e_unmatched", func(t *testing.T) {
		defer h.denyScope(t, "agr_other")()
		for _, name := range []string{"agr_get_scoped", "agr_get_plain"} {
			before := calls[name].Load()
			r := h.callTool(t, name, `{"record":"scope-e"}`)
			if !r.succeeded("scope result") || calls[name].Load() != before+1 {
				t.Errorf("%s = %s, executions %d -> %d; want success and one execution", name, r, before, calls[name].Load())
			}
		}
	})
}

func (h *agrHarness) registerScopeTools(t *testing.T) map[string]*atomic.Int64 {
	t.Helper()
	calls := make(map[string]*atomic.Int64)
	for _, tool := range []mcp.Tool{
		{Name: "agr_delete_scoped", ApprovalScope: "agr_records"},
		{Name: "agr_get_scoped", ApprovalScope: "agr_records"},
		{Name: "agr_get_plain"},
	} {
		counter := &atomic.Int64{}
		calls[tool.Name] = counter
		if err := h.s.getMCPRuntime().toolRegistry.Register(tool, func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
			counter.Add(1)
			return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "scope result"}}}, nil
		}); err != nil {
			t.Fatalf("register %s: %v", tool.Name, err)
		}
	}
	return calls
}

func (h *agrHarness) denyScope(t *testing.T, scope string) func() {
	t.Helper()
	bundle := fmt.Sprintf("version: \"1\"\nrules:\n  - id: agr-scope-deny\n    match:\n      mcp:\n        deny_servers: [%q]\n    decision: deny\n", scope)
	return h.addInvariant(t, bundle, mcp.Tool{ApprovalScope: scope})
}

func agrRequireInvariantDeny(t *testing.T, reply agrReply) {
	t.Helper()
	if reply.Error == nil || !strings.Contains(string(reply.Error.Data), "denied by invariant") || reply.Result != nil {
		t.Errorf("got %s; want invariant denial with no pending result", reply)
	}
}

func (h *agrHarness) scopeDeniesMint(t *testing.T, calls *atomic.Int64) {
	t.Helper()
	defer h.denyScope(t, "agr_records")()
	before := pgApprovalCount(t, h.store, agrTenant)
	agrRequireInvariantDeny(t, h.callTool(t, "agr_delete_scoped", `{"record":"scope-a"}`))
	if n := pgApprovalCount(t, h.store, agrTenant); n != before {
		t.Errorf("approvals = %d, want %d (no mint)", n, before)
	}
	if n := calls.Load(); n != 0 {
		t.Errorf("executions = %d, want 0", n)
	}
}

func (h *agrHarness) scopeDeniesResume(t *testing.T, calls *atomic.Int64) {
	t.Helper()
	const args = `{"record":"scope-b"}`
	ref := h.heldAndApprovedTool(t, "agr_delete_scoped", args)
	defer h.denyScope(t, "agr_records")()
	agrRequireInvariantDeny(t, h.resumeTool(t, "agr_delete_scoped", args, ref))
	if n := calls.Load(); n != 0 {
		t.Errorf("executions = %d, want 0", n)
	}
	if a := h.approval(t, ref); a.ConsumedAt == nil || a.ConsumedAt.IsZero() {
		t.Error("approval not consumed; the claim, rather than the floor, refused the resume")
	}
}
