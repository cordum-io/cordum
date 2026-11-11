package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
)

// EDGE-103 approval-hold coverage against a real edge.RedisStore (miniredis).
//
// Every mint runs through the REAL policy-evaluator event flow:
// mcp.InvokeToolWithPolicy emits the mcp.tool.pre event through the
// production edgeStoreEventEmitter, and the Edge approval binds to that
// event (task-63400922). No test seeds a synthetic parent action event:
// seedApprovalParents creates only the EdgeSession and AgentExecution that
// ensureMCPHTTPTraceParents creates in production.
//
// Matrix rows (task-b32f523f):
//
//	Row  1 approve -> resume:  TestApprovalHoldMintConsumeIntegration_Miniredis (happy)
//	Row  2 rejected:           TestApprovalHoldIntegration_RejectedReturns32096
//	Row  3 expired:            TestApprovalHoldIntegration_ExpiredReturns32096
//	Row  4 args_mismatch:      TestApprovalHoldMintConsumeIntegration_Miniredis (negative)
//	Row  5 policy_mismatch:    TestApprovalHoldIntegration_PolicyMismatchReturns32096
//	Row  6 duplicate:          TestApprovalHoldMintConsumeIntegration_Miniredis (duplicate)
//	Row  7 concurrent:         TestApprovalHoldIntegration_ConcurrentConsume
//	Row  8 self_approval:      core/edge/approval_self_approval_distinction_test.go
//	Row  9 cross_tenant:       TestApprovalHoldIntegration_CrossTenantReturns32096
//	Row 10 bypass-claim text:  TestApprovalHoldIntegration_BypassClaimText
//	Row 11 store_unavailable:  TestPolicyGateEdgeResume_HTTPMintOutageFailsClosed,
//	                           TestPolicyGateEdgeResume_HTTPClaimOutageFailsClosed
//
// Pre-event binding and the single resume authority (task-63400922):
// TestApprovalHold_* and TestConsumedApproval_SingleUseAndBound. The policy
// gate ON over the production HTTP MCP handler: TestPolicyGateEdgeResume_*.

const (
	holdTenant   = "tnt_a"
	holdServer   = "cordum.builtin"
	holdTool     = "fs.write"
	holdAgent    = "agent_alpha"
	holdMinter   = "alice"
	holdConsumer = "agent-runner"
)

// holdArgs is the tool-call payload the scenarios mint approvals for.
var holdArgs = json.RawMessage(`{"path":"/etc/hostname","contents":"hi"}`)

// holdHarness is one miniredis-backed Edge store plus the gateway gate wired
// for the Edge mint, with the store clock pinned at base.
type holdHarness struct {
	edgeStore *edge.RedisStore
	gate      *gatewayApprovalGate
	base      time.Time
	ctx       context.Context
	upstream  atomic.Int64
}

func newHoldHarness(t *testing.T, policySnapshot string, baseOffset time.Duration) *holdHarness {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() { _ = client.Close() })
	// Real time.Now() base: ProcessApprovalClaim stamps ConsumedAt with the
	// wall clock, so a store clock far in the past makes every claim expire.
	base := time.Now().UTC().Add(baseOffset)
	edgeStore := edge.NewRedisStoreFromClient(client, edge.WithClock(func() time.Time { return base }))
	gate := &gatewayApprovalGate{
		store:          NewMCPApprovalStore(client),
		edgeStore:      edgeStore,
		policySnapshot: func(context.Context) string { return policySnapshot },
		serverName:     holdServer,
	}
	return &holdHarness{edgeStore: edgeStore, gate: gate, base: base, ctx: context.Background()}
}

// holdCall is one caller's transport identity: the gateway and Edge call
// metadata the HTTP middleware stashes for every tools/call.
type holdCall struct {
	tenant, principal, agent, session, execution string
}

func (c holdCall) ctx(parent context.Context) context.Context {
	ctx := WithMCPCallMetadata(parent, MCPCallMetadata{Tenant: c.tenant, AgentID: c.agent, Principal: c.principal})
	return mcp.WithCallMetadata(ctx, mcp.CallMetadata{
		Tenant: c.tenant, Principal: c.principal, AgentID: c.agent,
		SessionID: c.session, ExecutionID: c.execution,
	})
}

// consumer resumes the same session as a different principal, keeping the
// store's caller-is-resolver self-approval check out of these scenarios.
func (c holdCall) consumer() holdCall {
	c.principal = holdConsumer
	return c
}

// seedCall seeds one session's Edge parents and returns its minting caller.
func (h *holdHarness) seedCall(t *testing.T, tenant, session, execution string) holdCall {
	t.Helper()
	seedApprovalParents(t, h.ctx, h.edgeStore, tenant, session, execution, holdMinter, h.base)
	return holdCall{tenant: tenant, principal: holdMinter, agent: holdAgent, session: session, execution: execution}
}

// seedApprovalParents creates ONLY the EdgeSession and AgentExecution, as
// ensureMCPHTTPTraceParents does in production. The AgentActionEvent an
// approval binds to is the policy evaluator's own mcp.tool.pre event.
func seedApprovalParents(t *testing.T, ctx context.Context, st *edge.RedisStore, tenantID, sessionID, executionID, principalID string, base time.Time) {
	t.Helper()
	if err := st.CreateSession(ctx, edge.EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       principalID,
		PrincipalType:     edge.PrincipalTypeHuman,
		AgentProduct:      "Claude Code",
		AgentVersion:      "2.1.0",
		Mode:              edge.SessionModeLocalDev,
		PolicySnapshot:    "policy-v7",
		EnforcementLayers: edge.EnforcementLayers{"mcp": true},
		PolicyMode:        edge.PolicyModeEnforce,
		Status:            edge.SessionStatusRunning,
		RiskSummary:       edge.RiskSummary{MaxRisk: edge.RiskLevelLow},
		StartedAt:         base,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.CreateExecution(ctx, edge.AgentExecution{
		ExecutionID:    executionID,
		SessionID:      sessionID,
		TenantID:       tenantID,
		Adapter:        edge.AdapterMCPGateway,
		Mode:           edge.ExecutionModeLocalDev,
		PolicySnapshot: "policy-v7",
		Status:         edge.ExecutionStatusRunning,
		StartedAt:      base.Add(time.Second),
	}); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}
}

// pgDispatcher is the test policy pipeline: every call gets one decision.
type pgDispatcher struct{ decision pb.DecisionType }

func (d pgDispatcher) Dispatch(context.Context, *config.PolicyInput) (mcp.PolicyDecision, bool) {
	return mcp.PolicyDecision{Decision: d.decision, GateID: "pg-test", Reason: "pg test decision"}, true
}

// pgArtifactStore keeps oversized-payload evidence pointers in memory.
type pgArtifactStore struct{}

func (pgArtifactStore) Put(_ context.Context, req mcp.ArtifactPutRequest) (*edge.ArtifactPointer, error) {
	return &edge.ArtifactPointer{ArtifactType: req.Type, URI: "mem://" + req.EventID, TenantID: req.TenantID,
		SessionID: req.SessionID, ExecutionID: req.ExecutionID, EventID: req.EventID}, nil
}

// pgCountingUpstream counts tool executions.
type pgCountingUpstream struct{ calls *atomic.Int64 }

func (u pgCountingUpstream) Invoke(context.Context, mcp.ToolCallParams) (*mcp.ToolCallResult, error) {
	u.calls.Add(1)
	return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "ok"}}}, nil
}

// pgGateUpstream stands in for the tool registry: it runs the registry
// approval gate for args under the ctx InvokeToolWithPolicy hands upstream.
type pgGateUpstream struct {
	gate *gatewayApprovalGate
	args json.RawMessage
}

func (u pgGateUpstream) Invoke(ctx context.Context, p mcp.ToolCallParams) (*mcp.ToolCallResult, error) {
	required, err := u.gate.Check(ctx, mcp.Tool{Name: p.Name, RequiresApproval: true}, u.args)
	if err != nil {
		return nil, err
	}
	if required != nil {
		return nil, required
	}
	return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: "ok"}}}, nil
}

// errPgStoreDown is the transport error a genuine Redis outage surfaces.
var errPgStoreDown = errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")

// pgEdgeStore decorates a real Edge store. It counts approval claims and can
// simulate an outage on the mint (EnqueueApproval), the approval lookup
// (GetApproval) or the claim (ClaimApproval). Every other method delegates.
type pgEdgeStore struct {
	edge.Store
	failMint  atomic.Bool
	failGet   atomic.Bool
	failClaim atomic.Bool
	claims    atomic.Int64
}

func (p *pgEdgeStore) EnqueueApproval(ctx context.Context, req edge.EdgeApprovalRequest) (*edge.EdgeApproval, error) {
	if p.failMint.Load() {
		return nil, errPgStoreDown
	}
	return p.Store.EnqueueApproval(ctx, req)
}

func (p *pgEdgeStore) GetApproval(ctx context.Context, tenantID, approvalRef string) (*edge.EdgeApproval, bool, error) {
	if p.failGet.Load() {
		return nil, false, errPgStoreDown
	}
	return p.Store.GetApproval(ctx, tenantID, approvalRef)
}

func (p *pgEdgeStore) ClaimApproval(ctx context.Context, req edge.ApprovalClaimRequest) (*edge.EdgeApproval, bool, error) {
	p.claims.Add(1)
	if p.failClaim.Load() {
		return nil, false, errPgStoreDown
	}
	return p.Store.ClaimApproval(ctx, req)
}

// pgPolicyDeps is the policy-gate wiring BuildMCPPolicyDeps assembles in
// production, with a test pipeline and upstream.
func (h *holdHarness) pgPolicyDeps(decision pb.DecisionType, upstream mcp.UpstreamToolCaller) mcp.ToolCallDeps {
	return mcp.ToolCallDeps{
		Pipeline:        pgDispatcher{decision: decision},
		EventEmitter:    edgeStoreEventEmitter{store: h.edgeStore},
		ArtifactStore:   pgArtifactStore{},
		ApprovalHandoff: h.gate,
		PolicySnapshot:  h.gate.policySnapshot,
		Upstream:        upstream,
		Clock:           func() time.Time { return h.base.Add(2 * time.Second) },
	}
}

// pgDriveRequireHuman mints through the REAL policy evaluator: on
// REQUIRE_HUMAN, InvokeToolWithPolicy emits the mcp.tool.pre event and hands
// off to the gate, which mints the Edge approval bound to that event. It
// returns the ref from the "approval pending: <ref>" result.
func pgDriveRequireHuman(t *testing.T, h *holdHarness, call holdCall, args json.RawMessage) string {
	t.Helper()
	deps := h.pgPolicyDeps(pb.DecisionType_DECISION_TYPE_REQUIRE_HUMAN, pgCountingUpstream{calls: &h.upstream})
	res, err := mcp.InvokeToolWithPolicy(call.ctx(h.ctx), deps, mcp.ToolCallParams{Name: holdTool, Arguments: args}, holdServer)
	if err != nil {
		t.Fatalf("REQUIRE_HUMAN mint via InvokeToolWithPolicy: %v", err)
	}
	if res == nil || res.IsError || len(res.Content) != 1 {
		t.Fatalf("REQUIRE_HUMAN result = %+v; want one approval-pending item", res)
	}
	ref, ok := strings.CutPrefix(res.Content[0].Text, "approval pending: ")
	if !ok || !strings.HasPrefix(ref, edge.ApprovalRefPrefix) {
		t.Fatalf("REQUIRE_HUMAN text = %q; want an Edge approval ref", res.Content[0].Text)
	}
	if n := h.upstream.Load(); n != 0 {
		t.Fatalf("upstream ran %d times on REQUIRE_HUMAN; want 0", n)
	}
	return ref
}

func (h *holdHarness) approve(t *testing.T, tenant, ref string) {
	t.Helper()
	approved, err := h.edgeStore.ApproveApproval(h.ctx, edge.ApprovalResolution{
		TenantID:    tenant,
		ApprovalRef: ref,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "integration test approve",
		ResolvedAt:  h.base, // must not follow the wall-clock ConsumedAt
	})
	if err != nil {
		t.Fatalf("ApproveApproval(%s): %v", ref, err)
	}
	if approved.Status != edge.ApprovalStatusApproved {
		t.Fatalf("approval status = %q; want approved", approved.Status)
	}
}

func (h *holdHarness) claim(st mcp.ApprovalClaimStore, call holdCall, args json.RawMessage, snapshot string) (mcp.ApprovalClaimOutcome, error) {
	return mcp.ProcessApprovalClaim(call.ctx(h.ctx), mcp.ApprovalHoldDeps{
		Store:          st,
		PolicySnapshot: func(context.Context) string { return snapshot },
		ServerName:     holdServer,
	}, mcp.ToolCallParams{Name: holdTool, Arguments: args})
}

// pgWithRef returns args with the reserved `_approval_ref` key added.
func pgWithRef(t *testing.T, args json.RawMessage, ref string) json.RawMessage {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	m["_approval_ref"] = ref
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode args: %v", err)
	}
	return out
}

// pgPreEvents lists the mcp.tool.pre events of one session/execution.
func pgPreEvents(t *testing.T, st edge.Store, tenant, session, execution string) []edge.AgentActionEvent {
	t.Helper()
	page, err := st.ListEvents(context.Background(), edge.ListEventsQuery{
		TenantID: tenant, SessionID: session, ExecutionID: execution,
		Kind: edge.EventKindMCPToolPre, Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return page.Items
}

// pgOnlyPreEvent returns the single mcp.tool.pre event of the caller's
// session/execution.
func pgOnlyPreEvent(t *testing.T, st edge.Store, call holdCall) edge.AgentActionEvent {
	t.Helper()
	events := pgPreEvents(t, st, call.tenant, call.session, call.execution)
	if len(events) != 1 {
		t.Fatalf("mcp.tool.pre events = %d; want exactly 1", len(events))
	}
	return events[0]
}

func pgApprovalCount(t *testing.T, st edge.Store, tenant string) int {
	t.Helper()
	page, err := st.ListApprovals(context.Background(), edge.ListApprovalsQuery{TenantID: tenant, Limit: 50})
	if err != nil {
		t.Fatalf("ListApprovals: %v", err)
	}
	return len(page.Items)
}

// pgRequireRefused asserts a fail-closed claim with the exact kind and, when
// wantReason is set, the exact reason.
func pgRequireRefused(t *testing.T, outcome mcp.ApprovalClaimOutcome, err error, kind edge.ApprovalConflictKind, wantReason string) {
	t.Helper()
	if err != nil && !errors.Is(err, edge.ErrApprovalConflict) {
		t.Fatalf("claim hard error: %v", err)
	}
	if outcome.Consumed {
		t.Fatal("claim consumed; want a fail-closed refusal")
	}
	if outcome.ConflictErr == nil {
		t.Fatalf("claim returned no ConflictErr; want kind %q", kind)
	}
	if outcome.ConflictErr.Kind != kind {
		t.Errorf("ConflictErr.Kind = %q; want %q", outcome.ConflictErr.Kind, kind)
	}
	if wantReason != "" && outcome.ConflictErr.Reason != wantReason {
		t.Errorf("ConflictErr.Reason = %q; want %q", outcome.ConflictErr.Reason, wantReason)
	}
}

// TestApprovalHoldMintConsumeIntegration_Miniredis covers Rows 1, 4 and 6:
// the approved ref resumes once, mutated args are refused, a replay is not
// claimable.
func TestApprovalHoldMintConsumeIntegration_Miniredis(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_hold_integration", "exec_hold_integration")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	h.approve(t, holdTenant, ref)

	resumeArgs := pgWithRef(t, holdArgs, ref)
	outcome, err := h.claim(h.edgeStore, call.consumer(), resumeArgs, "policy-v7")
	if err != nil {
		t.Fatalf("ProcessApprovalClaim happy path: %v", err)
	}
	if outcome.ConflictErr != nil {
		t.Fatalf("happy path ConflictErr kind=%q reason=%q; the mint and consume bindings must match",
			outcome.ConflictErr.Kind, outcome.ConflictErr.Reason)
	}
	if !outcome.Consumed || outcome.Approval == nil || outcome.Approval.ApprovalRef != ref {
		t.Fatalf("happy path outcome = %+v; want approval %q consumed", outcome, ref)
	}

	negCall := h.seedCall(t, holdTenant, "sess_hold_integration_neg", "exec_hold_integration_neg")
	negRef := pgDriveRequireHuman(t, h, negCall, holdArgs)
	h.approve(t, holdTenant, negRef)
	mutated := pgWithRef(t, json.RawMessage(`{"path":"/etc/passwd","contents":"hi"}`), negRef)
	negOutcome, err := h.claim(h.edgeStore, negCall.consumer(), mutated, "policy-v7")
	pgRequireRefused(t, negOutcome, err, edge.ApprovalConflictKindArgsMismatch, "")

	dupOutcome, err := h.claim(h.edgeStore, call.consumer(), resumeArgs, "policy-v7")
	pgRequireRefused(t, dupOutcome, err, edge.ApprovalConflictKindNotFound, "approval not claimable")
}

// TestApprovalHoldIntegration_RejectedReturns32096 covers Row 2. A rejected
// approval reads as not claimable (lifecycle-state hiding).
func TestApprovalHoldIntegration_RejectedReturns32096(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_rejected", "exec_rejected")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	rejected, err := h.edgeStore.RejectApproval(h.ctx, edge.ApprovalResolution{
		TenantID:    holdTenant,
		ApprovalRef: ref,
		ResolverID:  "principal-reviewer",
		ResolvedBy:  "reviewer@example.invalid",
		Reason:      "rejected by reviewer",
		ResolvedAt:  h.base,
	})
	if err != nil {
		t.Fatalf("RejectApproval: %v", err)
	}
	if rejected.Status != edge.ApprovalStatusRejected {
		t.Fatalf("approval status = %q; want rejected", rejected.Status)
	}
	outcome, err := h.claim(h.edgeStore, call.consumer(), pgWithRef(t, holdArgs, ref), "policy-v7")
	pgRequireRefused(t, outcome, err, edge.ApprovalConflictKindNotFound, "approval not claimable")
}

// TestApprovalHoldIntegration_ExpiredReturns32096 covers Row 3. The store
// clock sits 30 minutes (6x the default TTL) behind the wall-clock claim.
func TestApprovalHoldIntegration_ExpiredReturns32096(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", -30*time.Minute)
	call := h.seedCall(t, holdTenant, "sess_expired", "exec_expired")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	h.approve(t, holdTenant, ref)
	outcome, err := h.claim(h.edgeStore, call.consumer(), pgWithRef(t, holdArgs, ref), "policy-v7")
	pgRequireRefused(t, outcome, err, edge.ApprovalConflictKindExpired, "")
}

// TestApprovalHoldIntegration_PolicyMismatchReturns32096 covers Row 5: the
// policy bundle rotated (policy-v7 -> policy-v8) between mint and resume.
func TestApprovalHoldIntegration_PolicyMismatchReturns32096(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_policy", "exec_policy")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	h.approve(t, holdTenant, ref)
	outcome, err := h.claim(h.edgeStore, call.consumer(), pgWithRef(t, holdArgs, ref), "policy-v8")
	pgRequireRefused(t, outcome, err, edge.ApprovalConflictKindPolicyMismatch, "")
}

// TestApprovalHoldIntegration_ConcurrentConsume covers Row 7: two real
// goroutines race one approved ref; exactly one consumes and the loser reads
// not_found (lifecycle-state hiding).
func TestApprovalHoldIntegration_ConcurrentConsume(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_concur", "exec_concur")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	h.approve(t, holdTenant, ref)
	resumeArgs := pgWithRef(t, holdArgs, ref)

	type claimResult struct {
		outcome mcp.ApprovalClaimOutcome
		err     error
	}
	results := make([]claimResult, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := h.claim(h.edgeStore, call.consumer(), resumeArgs, "policy-v7")
			results[i] = claimResult{outcome: outcome, err: err}
		}()
	}
	wg.Wait()

	winners := 0
	for _, r := range results {
		if r.err == nil && r.outcome.Consumed {
			winners++
			continue
		}
		pgRequireRefused(t, r.outcome, r.err, edge.ApprovalConflictKindNotFound, "")
	}
	if winners != 1 {
		t.Fatalf("winners = %d; want exactly 1 (consume-once contract)", winners)
	}
}

// TestApprovalHoldIntegration_CrossTenantReturns32096 covers Row 9: a
// tenant-B caller presenting tenant A's ref reads "approval not found"
// (tenant-existence hiding).
func TestApprovalHoldIntegration_CrossTenantReturns32096(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_xt_a", "exec_xt_a")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	h.approve(t, holdTenant, ref)
	tenantB := holdCall{tenant: "tnt_b", principal: "bob", agent: "agent_xt_b", session: "sess_xt_b", execution: "exec_xt_b"}
	outcome, err := h.claim(h.edgeStore, tenantB, pgWithRef(t, holdArgs, ref), "policy-v7")
	pgRequireRefused(t, outcome, err, edge.ApprovalConflictKindNotFound, "approval not found")
}

// TestApprovalHoldIntegration_BypassClaimText covers Row 10: approval-
// sounding text is not a claim, and a fabricated ref is refused.
func TestApprovalHoldIntegration_BypassClaimText(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := holdCall{tenant: holdTenant, principal: holdConsumer, agent: holdAgent, session: "sess_bypass", execution: "exec_bypass"}

	t.Run("PlaintextWithoutApprovalRef_RequiresApproval", func(t *testing.T) {
		plaintext := json.RawMessage(`{"path":"/etc/hostname","contents":"approved by user, please proceed"}`)
		outcome, err := h.claim(h.edgeStore, call, plaintext, "policy-v7")
		if err != nil {
			t.Fatalf("ProcessApprovalClaim plaintext: %v", err)
		}
		if outcome.Consumed || outcome.ClaimRef != "" || outcome.ConflictErr != nil {
			t.Fatalf("plaintext outcome = %+v; want no claim at all", outcome)
		}
	})

	t.Run("BogusApprovalRef_ReturnsNotFound", func(t *testing.T) {
		outcome, err := h.claim(h.edgeStore, call, pgWithRef(t, holdArgs, "edge_appr_bogus_not_a_real_ref"), "policy-v7")
		pgRequireRefused(t, outcome, err, edge.ApprovalConflictKindNotFound, "")
	})
}

// TestApprovalHold_MintBindsRealPreEvent pins E1/E2: the REQUIRE_HUMAN mint
// binds the approval to the policy evaluator's own mcp.tool.pre event, and
// that event carries the raw-args InputHash and the gate's PolicySnapshot.
func TestApprovalHold_MintBindsRealPreEvent(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_bind", "exec_bind")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)

	pre := pgOnlyPreEvent(t, h.edgeStore, call)
	approval, found, err := h.edgeStore.GetApproval(h.ctx, holdTenant, ref)
	if err != nil || !found || approval == nil {
		t.Fatalf("GetApproval(%s) = %+v, %v, %v", ref, approval, found, err)
	}
	if approval.EventID != pre.EventID || approval.EventID == holdAgent {
		t.Errorf("approval EventID = %q; want the pre-event id %q, never the agent id %q", approval.EventID, pre.EventID, holdAgent)
	}
	_, wantInput := mcp.BuildMCPApprovalBinding(holdTenant, holdServer, mcp.ToolCallParams{Name: holdTool, Arguments: holdArgs}, "")
	if pre.InputHash != wantInput || approval.InputHash != wantInput {
		t.Errorf("InputHash pre=%q approval=%q; want both %q (raw-args binding)", pre.InputHash, approval.InputHash, wantInput)
	}
	if pre.PolicySnapshot != "policy-v7" || approval.PolicySnapshot != "policy-v7" {
		t.Errorf("PolicySnapshot pre=%q approval=%q; want both policy-v7", pre.PolicySnapshot, approval.PolicySnapshot)
	}
}

// TestApprovalHold_DualWriteFailsClosedWithoutPreEvent: a registry-gate mint
// with no policy pre-event in ctx must fail closed and leave no Edge approval.
func TestApprovalHold_DualWriteFailsClosedWithoutPreEvent(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_nopre", "exec_nopre")
	required, err := h.gate.Check(call.ctx(h.ctx), mcp.Tool{Name: holdTool, RequiresApproval: true}, holdArgs)
	if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) {
		t.Fatalf("Check without a pre-event = (%+v, %v); want ErrApprovalStoreUnavailable", required, err)
	}
	if n := pgApprovalCount(t, h.edgeStore, holdTenant); n != 0 {
		t.Fatalf("Edge approvals = %d; want 0", n)
	}
}

// TestApprovalHold_DualWriteRefusesInputHashMismatch: the registry-gate mint
// binds to the pre-event of the call the policy evaluated. Args that differ
// from that call's args are refused by the store's event binding.
func TestApprovalHold_DualWriteRefusesInputHashMismatch(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	allow := func(call holdCall, gateArgs json.RawMessage) error {
		deps := h.pgPolicyDeps(pb.DecisionType_DECISION_TYPE_ALLOW, pgGateUpstream{gate: h.gate, args: gateArgs})
		_, err := mcp.InvokeToolWithPolicy(call.ctx(h.ctx), deps, mcp.ToolCallParams{Name: holdTool, Arguments: holdArgs}, holdServer)
		return err
	}

	var required *mcp.ApprovalRequired
	if err := allow(h.seedCall(t, holdTenant, "sess_dw_ok", "exec_dw_ok"), holdArgs); !errors.As(err, &required) {
		t.Fatalf("matching args: err = %v; want *mcp.ApprovalRequired", err)
	}
	if !strings.HasPrefix(required.ApprovalRef, edge.ApprovalRefPrefix) || required.RetryHint != mcp.RetryHintApprovalRef {
		t.Fatalf("matching args: ref=%q hint=%q; want an Edge ref with hint %q", required.ApprovalRef, required.RetryHint, mcp.RetryHintApprovalRef)
	}

	swapped := json.RawMessage(`{"path":"/etc/hosts","contents":"swapped"}`)
	err := allow(h.seedCall(t, holdTenant, "sess_dw_swap", "exec_dw_swap"), swapped)
	if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) || !strings.Contains(err.Error(), "input hash mismatch") {
		t.Fatalf("swapped args: err = %v; want ErrApprovalStoreUnavailable carrying input hash mismatch", err)
	}
	if n := pgApprovalCount(t, h.edgeStore, holdTenant); n != 1 {
		t.Fatalf("Edge approvals = %d; want only the matching call's 1", n)
	}
}

// TestApprovalHold_ClaimUsesStoredEventID pins E3: the claim binds to the
// stored approval's EventID, and a lookup or claim outage fails closed.
func TestApprovalHold_ClaimUsesStoredEventID(t *testing.T) {
	t.Parallel()
	h := newHoldHarness(t, "policy-v7", 0)
	call := h.seedCall(t, holdTenant, "sess_claim_evt", "exec_claim_evt")
	ref := pgDriveRequireHuman(t, h, call, holdArgs)
	h.approve(t, holdTenant, ref)
	resume := pgWithRef(t, holdArgs, ref)

	for _, tc := range []struct {
		name       string
		fail       func(*pgEdgeStore)
		wantClaims int64
	}{
		{"lookup_outage_fails_closed", func(st *pgEdgeStore) { st.failGet.Store(true) }, 0},
		{"claim_outage_fails_closed", func(st *pgEdgeStore) { st.failClaim.Store(true) }, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := &pgEdgeStore{Store: h.edgeStore}
			tc.fail(st)
			outcome, err := h.claim(st, call.consumer(), resume, "policy-v7")
			if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) || outcome.Consumed {
				t.Fatalf("claim during outage = (%+v, %v); want ErrApprovalStoreUnavailable", outcome, err)
			}
			if n := st.claims.Load(); n != tc.wantClaims {
				t.Fatalf("ClaimApproval calls = %d; want %d", n, tc.wantClaims)
			}
		})
	}

	t.Run("bogus_ref_not_found_without_claim", func(t *testing.T) {
		st := &pgEdgeStore{Store: h.edgeStore}
		outcome, err := h.claim(st, call.consumer(), pgWithRef(t, holdArgs, "edge_appr_bogus_not_a_real_ref"), "policy-v7")
		pgRequireRefused(t, outcome, err, edge.ApprovalConflictKindNotFound, "approval not found")
		if n := st.claims.Load(); n != 0 {
			t.Fatalf("ClaimApproval calls = %d; want 0 for an unknown ref", n)
		}
	})

	t.Run("consumes_bound_to_stored_event", func(t *testing.T) {
		st := &pgEdgeStore{Store: h.edgeStore}
		outcome, err := h.claim(st, call.consumer(), resume, "policy-v7")
		if err != nil || !outcome.Consumed || outcome.Approval == nil {
			t.Fatalf("claim = (%+v, %v); want consumed", outcome, err)
		}
		if pre := pgOnlyPreEvent(t, h.edgeStore, call); outcome.Approval.EventID != pre.EventID {
			t.Errorf("consumed approval EventID = %q; want pre-event %q", outcome.Approval.EventID, pre.EventID)
		}
		if outcome.ActionHash != outcome.Approval.ActionHash || outcome.InputHash != outcome.Approval.InputHash {
			t.Errorf("outcome binding = (%q, %q); want the approval's (%q, %q)",
				outcome.ActionHash, outcome.InputHash, outcome.Approval.ActionHash, outcome.Approval.InputHash)
		}
		if n := st.claims.Load(); n != 1 {
			t.Fatalf("ClaimApproval calls = %d; want 1", n)
		}
	})
}

// TestConsumedApproval_SingleUseAndBound pins E4's request-scoped token: it
// authorizes exactly one gate check, and only for the claimed binding.
func TestConsumedApproval_SingleUseAndBound(t *testing.T) {
	t.Parallel()
	const tool, action, input = "fs.write", "action-hash-1", "input-hash-1"
	fresh := func() *mcp.ConsumedApproval {
		return &mcp.ConsumedApproval{Ref: "edge_appr_test", Tool: tool, ActionHash: action, InputHash: input}
	}
	for _, tc := range []struct{ name, tool, action, input string }{
		{"other_tool", "fs.delete", action, input},
		{"other_action", tool, "action-hash-2", input},
		{"other_input", tool, action, "input-hash-2"},
		{"empty_binding", "", "", ""},
	} {
		c := fresh()
		if c.Use(tc.tool, tc.action, tc.input) {
			t.Errorf("%s: Use(mismatched binding) = true; want false", tc.name)
		}
		if !c.Use(tool, action, input) {
			t.Errorf("%s: a refused mismatch must not burn the token", tc.name)
		}
	}

	c := fresh()
	if !c.Use(tool, action, input) || c.Use(tool, action, input) {
		t.Error("Use(equal binding) must return true exactly once")
	}
	var nilToken *mcp.ConsumedApproval
	if nilToken.Use(tool, action, input) {
		t.Error("nil token Use = true; want false")
	}
	if (&mcp.ConsumedApproval{}).Use("", "", "") {
		t.Error("zero-value token authorized an empty binding; want false")
	}
	if got := mcp.ConsumedApprovalFromContext(context.Background()); got != nil {
		t.Errorf("ConsumedApprovalFromContext(empty ctx) = %+v; want nil", got)
	}

	shared := fresh()
	var wins atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if shared.Use(tool, action, input) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := wins.Load(); n != 1 {
		t.Fatalf("concurrent Use winners = %d; want exactly 1", n)
	}
}

// ---------------------------------------------------------------------------
// Policy gate ON over the production HTTP MCP handler.
// ---------------------------------------------------------------------------

const (
	pgToolName  = "cordum_pg_gated_counter"
	pgAgentID   = "agent-pg-alice"
	pgSessionID = "pg-sess-1"
	pgArgsA     = `{"target":"prod","count":3}`
	pgArgsB     = `{"target":"prod","count":4}`
	pgArgsC     = `{"target":"prod","count":5}`
)

type pgReply struct {
	Result *struct {
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	} `json:"error"`
}

type pgHarness struct {
	srv    *httptest.Server
	client *http.Client
	store  *pgEdgeStore
	legacy *MCPApprovalStore
	calls  atomic.Int64
	sawRef atomic.Int64
	rpcID  atomic.Int64
}

// pgWidePoolClient opens a second client on the gateway's Redis DB. The
// gateway fixture's clients hold one connection; the Edge store's WATCH
// transactions under concurrent resumes need more.
func pgWidePoolClient(t *testing.T, s *server) *redis.Client {
	t.Helper()
	base, ok := s.redisClient().(*redis.Client)
	if !ok {
		t.Fatalf("gateway redis client = %T; want *redis.Client", s.redisClient())
	}
	opts := base.Options()
	client := redis.NewClient(&redis.Options{Addr: opts.Addr, DB: opts.DB, PoolSize: 8})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// pgNewPolicyGateServer boots the HTTP MCP routes the way production does
// with `mcp.policy_gate_enabled: true`: real action-gate pipeline, real Edge
// store (behind the outage decorator), real policy-bundle snapshot.
func pgNewPolicyGateServer(t *testing.T) *pgHarness {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = pwAuth{keys: map[string]auth.AuthContext{
		"key-pg-alice": {Tenant: "default", PrincipalID: "pg-alice", Role: "admin"},
		"key-pg-bob":   {Tenant: "default", PrincipalID: "pg-bob", Role: "admin"},
	}}
	h := &pgHarness{
		client: &http.Client{Timeout: 30 * time.Second},
		store:  &pgEdgeStore{Store: edge.NewRedisStoreFromClient(pgWidePoolClient(t, s))},
	}
	s.edgeStore = h.store
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	s.wireActionGatePipeline()
	setMCPConfigForBoot(t, s, true, true)
	ctx := context.Background()
	if err := s.configSvc.Set(ctx, &configsvc.Document{Scope: configsvc.Scope(packs.PolicyConfigScope),
		ScopeID: packs.PolicyConfigID, Data: map[string]any{packs.PolicyConfigKey: map[string]any{}}}); err != nil {
		t.Fatalf("seed policy bundle doc: %v", err)
	}
	if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID: pgAgentID, TenantID: "default", Name: pgAgentID, Owner: "ops@example.com", RiskTier: "high",
		AllowedServers: []string{mcpPolicyServerName}, AllowedTools: []string{"cordum_pg_*"},
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, pgAgentID, "pg-alice"); err != nil {
		t.Fatalf("link worker: %v", err)
	}
	if snapshot := s.mcpPolicySnapshotFunc()(ctx); snapshot == "" {
		t.Fatal("policy snapshot is empty; the approval hold needs a policy bundle snapshot")
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	mux.HandleFunc("POST /api/v1/edge/approvals/{approval_ref}/approve", s.handleApproveEdgeApproval)
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil || runtime.approvalStore == nil ||
		!runtime.server.HasPolicyGate() || !runtime.server.HasApprovalHold() {
		t.Fatal("MCP runtime booted without the policy gate, approval hold and legacy store")
	}
	h.legacy = runtime.approvalStore
	tool := mcp.Tool{Name: pgToolName, RequiresApproval: true, InputSchema: map[string]any{"type": "object"}}
	if err := runtime.toolRegistry.Register(tool, func(_ context.Context, args json.RawMessage) (*mcp.ToolCallResult, error) {
		h.calls.Add(1)
		if strings.Contains(string(args), "_approval_ref") {
			h.sawRef.Add(1)
		}
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: `{"ok":true}`}}}, nil
	}); err != nil {
		t.Fatalf("register counting tool: %v", err)
	}
	h.srv = httptest.NewServer(apiKeyMiddleware(s.auth, mux))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *pgHarness) post(key, path, body string, mcpCall bool) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	if mcpCall {
		req.Header.Set("X-Agent-Id", pgAgentID)
		req.Header.Set("X-MCP-Session-ID", pgSessionID)
	}
	return h.client.Do(req)
}

// call issues tools/call for the counting tool as alice on pgSessionID, with
// a distinct JSON-RPC id per request. Goroutine-safe: reports with t.Errorf.
func (h *pgHarness) call(t *testing.T, args string) pgReply {
	t.Helper()
	id := strconv.FormatInt(h.rpcID.Add(1), 10)
	body := `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"` + pgToolName + `","arguments":` + args + `}}`
	var reply pgReply
	resp, err := h.post("key-pg-alice", "/mcp/message", body, true)
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

func (h *pgHarness) approveEdge(t *testing.T, key, ref string) int {
	t.Helper()
	resp, err := h.post(key, "/api/v1/edge/approvals/"+ref+"/approve", `{"reason":"ok"}`, false)
	if err != nil {
		t.Fatalf("approve %s: %v", ref, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// requireEdgeHold asserts a -32099 carrying a consumable Edge ref and returns
// the ref and the dual-written legacy approval id.
func (h *pgHarness) requireEdgeHold(t *testing.T, r pgReply) (string, string) {
	t.Helper()
	if r.Error == nil || r.Error.Code != -32099 {
		t.Fatalf("want JSON-RPC -32099 approval_required, got error=%+v result=%+v", r.Error, r.Result)
	}
	ref, _ := r.Error.Data["approval_ref"].(string)
	id, _ := r.Error.Data["approval_id"].(string)
	if !strings.HasPrefix(ref, edge.ApprovalRefPrefix) || id == "" || id == ref {
		t.Fatalf("-32099 data = %v; want an Edge approval_ref distinct from the legacy approval_id", r.Error.Data)
	}
	if hint, _ := r.Error.Data["retry_hint"].(string); hint != mcp.RetryHintApprovalRef {
		t.Errorf("retry_hint = %q, want %q", hint, mcp.RetryHintApprovalRef)
	}
	return ref, id
}

// requireLifecycle asserts a -32096 of the given kind and returns its reason.
func (h *pgHarness) requireLifecycle(t *testing.T, r pgReply, kind string) string {
	t.Helper()
	if r.Error == nil || r.Error.Code != -32096 {
		t.Fatalf("want JSON-RPC -32096 kind %q, got error=%+v result=%+v", kind, r.Error, r.Result)
	}
	if got, _ := r.Error.Data["kind"].(string); got != kind {
		t.Fatalf("-32096 kind = %q, want %q (data=%v)", got, kind, r.Error.Data)
	}
	reason, _ := r.Error.Data["reason"].(string)
	return reason
}

func (h *pgHarness) requireExecuted(t *testing.T, r pgReply) {
	t.Helper()
	if r.Error != nil || r.Result == nil || r.Result.IsError {
		t.Fatalf("want a successful tool result, got error=%+v result=%+v", r.Error, r.Result)
	}
}

func (h *pgHarness) requireCalls(t *testing.T, want int64) {
	t.Helper()
	if n := h.calls.Load(); n != want {
		t.Fatalf("tool executions = %d, want %d", n, want)
	}
}

// TestPolicyGateEdgeResume_HTTPExecutesExactlyOnce: with the policy gate on,
// a held call returns a consumable approval_ref; once approved it executes
// exactly once on resume, replays are refused, and concurrent resumes of one
// ref execute once. Steps share state and stop at the first failure.
func TestPolicyGateEdgeResume_HTTPExecutesExactlyOnce(t *testing.T) {
	t.Parallel()
	h := pgNewPolicyGateServer(t)
	var refA string
	resumeA := func(t *testing.T) string { return string(pgWithRef(t, json.RawMessage(pgArgsA), refA)) }
	steps := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"held_call_returns_edge_ref", func(t *testing.T) {
			refA, _ = h.requireEdgeHold(t, h.call(t, pgArgsA))
			h.requireCalls(t, 0)
		}},
		{"approval_binds_policy_pre_event", func(t *testing.T) {
			approval, found, err := h.store.GetApproval(context.Background(), "default", refA)
			if err != nil || !found || approval == nil {
				t.Fatalf("GetApproval(%s) = %+v, %v, %v", refA, approval, found, err)
			}
			if approval.SessionID != pgSessionID || approval.ExecutionID != "mcp-"+pgSessionID || approval.EventID == pgAgentID {
				t.Fatalf("approval binds session=%q execution=%q event=%q; want the transport session and a pre-event id",
					approval.SessionID, approval.ExecutionID, approval.EventID)
			}
			var pre *edge.AgentActionEvent
			for _, e := range pgPreEvents(t, h.store, "default", pgSessionID, "mcp-"+pgSessionID) {
				if e.EventID == approval.EventID {
					pre = &e
				}
			}
			if pre == nil {
				t.Fatalf("approval EventID %q is not an mcp.tool.pre event", approval.EventID)
			}
			if pre.InputHash == "" || pre.InputHash != approval.InputHash || pre.PolicySnapshot == "" || pre.PolicySnapshot != approval.PolicySnapshot {
				t.Fatalf("pre-event binding (%q, %q) != approval binding (%q, %q)",
					pre.InputHash, pre.PolicySnapshot, approval.InputHash, approval.PolicySnapshot)
			}
		}},
		{"operator_approves_over_http", func(t *testing.T) {
			if code := h.approveEdge(t, "key-pg-bob", refA); code != http.StatusOK {
				t.Fatalf("operator approval status = %d, want 200", code)
			}
		}},
		{"resume_executes_once", func(t *testing.T) {
			h.requireExecuted(t, h.call(t, resumeA(t)))
			h.requireCalls(t, 1)
			if n := h.sawRef.Load(); n != 0 {
				t.Errorf("tool saw _approval_ref %d times; want stripped args", n)
			}
		}},
		{"replay_is_refused", func(t *testing.T) {
			if reason := h.requireLifecycle(t, h.call(t, resumeA(t)), "not_found"); reason != "approval not claimable" {
				t.Errorf("replay reason = %q, want %q", reason, "approval not claimable")
			}
			h.requireCalls(t, 1)
		}},
		{"concurrent_resumes_execute_once", func(t *testing.T) {
			refC, _ := h.requireEdgeHold(t, h.call(t, pgArgsC))
			if code := h.approveEdge(t, "key-pg-bob", refC); code != http.StatusOK {
				t.Fatalf("operator approval status = %d, want 200", code)
			}
			resumeC := string(pgWithRef(t, json.RawMessage(pgArgsC), refC))
			replies := make([]pgReply, 4)
			var wg sync.WaitGroup
			for i := range replies {
				wg.Add(1)
				go func() {
					defer wg.Done()
					replies[i] = h.call(t, resumeC)
				}()
			}
			wg.Wait()
			executed := 0
			for i, r := range replies {
				if r.Error == nil && r.Result != nil && !r.Result.IsError {
					executed++
					continue
				}
				if r.Error == nil || r.Error.Code != -32096 || r.Error.Data["kind"] != "not_found" {
					t.Errorf("reply %d = error %+v result %+v; want success or -32096 not_found", i, r.Error, r.Result)
				}
			}
			if executed != 1 {
				t.Fatalf("concurrent resumes executed %d times, want 1", executed)
			}
			h.requireCalls(t, 2)
		}},
	}
	for _, step := range steps {
		if !t.Run(step.name, step.run) {
			return
		}
	}
}

// TestPolicyGateEdgeResume_HTTPLegacyApprovalCannotResume pins E4: under the
// policy gate the Edge approval is the only resume authority. Approving the
// dual-written legacy record and retrying identical args must not execute.
func TestPolicyGateEdgeResume_HTTPLegacyApprovalCannotResume(t *testing.T) {
	t.Parallel()
	h := pgNewPolicyGateServer(t)
	first := h.call(t, pgArgsB)
	recs, _, err := h.legacy.ListByStatus(context.Background(), string(model.ApprovalStatusPending), 0, 10)
	if err != nil || len(recs) != 1 {
		t.Fatalf("pending legacy approvals = %d (err %v), want 1", len(recs), err)
	}
	if _, err := h.legacy.Resolve(context.Background(), recs[0].ID, model.ApprovalDecisionApprove, "pg-bob", "legacy approve"); err != nil {
		t.Fatalf("resolve legacy approval: %v", err)
	}
	retry := h.call(t, pgArgsB)
	h.requireCalls(t, 0)
	h.requireEdgeHold(t, first)
	retryRef, _ := h.requireEdgeHold(t, retry)
	// The Edge store reuses a live hold on the same (session, execution,
	// action) tuple, so the retry may re-hold on the first ref. Either way
	// the legacy approve must leave the Edge approval undecided.
	approval, found, err := h.store.GetApproval(context.Background(), "default", retryRef)
	if err != nil || !found || approval == nil || approval.Status != edge.ApprovalStatusPending {
		t.Fatalf("Edge approval %s after legacy approve = %+v, %v, %v; want pending", retryRef, approval, found, err)
	}
}

// TestPolicyGateEdgeResume_HTTPMintOutageFailsClosed covers Row 11 on the
// mint: an Edge store outage fails closed with -32096 and mints nothing; the
// healed store holds the call with a consumable ref.
func TestPolicyGateEdgeResume_HTTPMintOutageFailsClosed(t *testing.T) {
	t.Parallel()
	h := pgNewPolicyGateServer(t)
	h.store.failMint.Store(true)
	reason := h.requireLifecycle(t, h.call(t, pgArgsA), "approval_store_unavailable")
	if reason != "approval store unavailable" {
		t.Errorf("outage reason = %q, want the fixed sanitized reason", reason)
	}
	h.requireCalls(t, 0)
	if n := pgApprovalCount(t, h.store, "default"); n != 0 {
		t.Fatalf("Edge approvals during outage = %d, want 0", n)
	}
	h.store.failMint.Store(false)
	h.requireEdgeHold(t, h.call(t, pgArgsA))
	h.requireCalls(t, 0)
}

// TestPolicyGateEdgeResume_HTTPClaimOutageFailsClosed covers Row 11 on the
// resume: an approval lookup or claim outage fails closed with -32096 and
// never executes; the healed store resumes exactly once.
func TestPolicyGateEdgeResume_HTTPClaimOutageFailsClosed(t *testing.T) {
	t.Parallel()
	h := pgNewPolicyGateServer(t)
	ref, _ := h.requireEdgeHold(t, h.call(t, pgArgsA))
	if code := h.approveEdge(t, "key-pg-bob", ref); code != http.StatusOK {
		t.Fatalf("operator approval status = %d, want 200", code)
	}
	resume := string(pgWithRef(t, json.RawMessage(pgArgsA), ref))
	for _, fail := range []*atomic.Bool{&h.store.failGet, &h.store.failClaim} {
		fail.Store(true)
		reason := h.requireLifecycle(t, h.call(t, resume), "approval_store_unavailable")
		fail.Store(false)
		if reason != "approval store unavailable" {
			t.Errorf("outage reason = %q, want the fixed sanitized reason", reason)
		}
		h.requireCalls(t, 0)
	}
	h.requireExecuted(t, h.call(t, resume))
	h.requireCalls(t, 1)
}
