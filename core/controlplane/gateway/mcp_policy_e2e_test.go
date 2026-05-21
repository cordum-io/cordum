package gateway

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/mcp"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestMCPPolicyGateE2E_EmitsPreAndPostOnRealEdgeStore covers acceptance
// criterion #3: drive the booted gateway's MCP policy gate against the
// real edge.RedisStore and assert pre + post events with matching
// EventID land on the production stream. The c530c1c0 stable-EventID
// fix is what binds the pre and post; this test proves the fix survives
// the wiring into BuildMCPPolicyDeps.
//
// Substitutes a passingDispatcher for the production action-gate
// pipeline so the test focuses on the adapter chain rather than the
// gate decision logic (which depends on agent-identity store + tool
// allowlists that are out of scope here — separately covered by
// actiongates/mcp_gate_test.go). The EventEmitter + ArtifactStore are
// the real production adapters built by attachMCPPolicyDeps.
func TestMCPPolicyGateE2E_EmitsPreAndPostOnRealEdgeStore(t *testing.T) {
	t.Parallel()
	deps, ctx, tenantID, sessionID, executionID := bootMCPPolicyGateE2E(t)
	deps.Pipeline = passingDispatcher{}
	upstream := &fakeUpstreamToolCaller{result: "ok"}
	deps.Upstream = upstreamCallerAdapter{upstream: upstream}

	if _, err := mcp.InvokeToolWithPolicy(ctx, deps, mcp.ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "cordum.builtin"); err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream.calls = %d, want 1 (ALLOW path must forward)", upstream.calls)
	}

	page, err := waitForEdgeEvents(t.Context(), t, deps.EventEmitter, tenantID, sessionID, executionID, 2)
	if err != nil {
		t.Fatalf("ListEvents on edge.RedisStore: %v", err)
	}
	if len(page.Items) < 2 {
		t.Fatalf("edge.RedisStore stream has %d events; want >=2 (pre+post)", len(page.Items))
	}
	pre, post := page.Items[0], page.Items[1]
	if pre.Kind != edgecore.EventKindMCPToolPre {
		t.Fatalf("event[0].Kind = %q, want %q", pre.Kind, edgecore.EventKindMCPToolPre)
	}
	if post.Kind != edgecore.EventKindMCPToolPost {
		t.Fatalf("event[1].Kind = %q, want %q", post.Kind, edgecore.EventKindMCPToolPost)
	}
	if pre.EventID != post.EventID {
		t.Fatalf("EventID mismatch pre=%q post=%q (c530c1c0 stable-EventID fix regression)",
			pre.EventID, post.EventID)
	}
	if pre.TenantID != tenantID || post.TenantID != tenantID {
		t.Fatalf("tenant mismatch: pre=%q post=%q want %q", pre.TenantID, post.TenantID, tenantID)
	}
}

// TestMCPPolicyGateE2E_OversizedPayloadLandsInArtifactStore covers
// acceptance criterion #3 part (iii): an oversized arg payload routes
// to the production artifacts.Store and the resulting ArtifactPointer
// carries a valid 64-hex SHA256 the dashboard's evidence-export
// bundler can dereference.
func TestMCPPolicyGateE2E_OversizedPayloadLandsInArtifactStore(t *testing.T) {
	t.Parallel()
	deps, ctx, tenantID, sessionID, executionID := bootMCPPolicyGateE2E(t)
	deps.Pipeline = passingDispatcher{}
	deps.Upstream = upstreamCallerAdapter{upstream: &fakeUpstreamToolCaller{result: "ok"}}

	bigField := strings.Repeat("a", 80*1024)
	args := json.RawMessage(`{"payload":"` + bigField + `"}`)
	if _, err := mcp.InvokeToolWithPolicy(ctx, deps, mcp.ToolCallParams{
		Name:      "fs.read_file",
		Arguments: args,
	}, "cordum.builtin"); err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}

	page, err := waitForEdgeEventsByKind(t.Context(), t, deps.EventEmitter, tenantID, sessionID, executionID, edgecore.EventKindMCPToolPre)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(page) == 0 {
		t.Fatal("expected at least one pre event with artifact pointer")
	}
	pre := page[0]
	if len(pre.ArtifactPointers) == 0 {
		t.Fatal("pre event missing artifact pointer for oversized payload")
	}
	ptr := pre.ArtifactPointers[0]
	if ptr.ArtifactType != edgecore.ArtifactTypeMCPRequest {
		t.Fatalf("artifact type = %q, want %q", ptr.ArtifactType, edgecore.ArtifactTypeMCPRequest)
	}
	if len(ptr.SHA256) != 64 {
		t.Fatalf("artifact SHA256 length = %d, want 64-hex char digest (got %q)", len(ptr.SHA256), ptr.SHA256)
	}
	if _, err := hex.DecodeString(ptr.SHA256); err != nil {
		t.Fatalf("artifact SHA256 not valid hex: %v (got %q)", err, ptr.SHA256)
	}
	if ptr.URI == "" {
		t.Fatal("artifact URI empty; production artifact store must return a non-empty pointer")
	}
}

// TestMCPPolicyGateE2E_FakeDispatcherAWCPostEventCarriesConstraints covers
// the compatibility data plane only: when a fake/test dispatcher fires
// ALLOW_WITH_CONSTRAINTS, the post event landing on edge.RedisStore records
// Decision=DecisionConstrain plus the structured Constraints map. Production
// action gates are blockers-only and do not emit enforceable generic
// constraints; policy-bundle typed constraints are tested separately.
func TestMCPPolicyGateE2E_FakeDispatcherAWCPostEventCarriesConstraints(t *testing.T) {
	t.Parallel()
	deps, ctx, tenantID, sessionID, executionID := bootMCPPolicyGateE2E(t)
	constraints := map[string]any{
		"max_bytes": float64(1024),
		"redaction": "strict",
	}
	deps.Pipeline = fakeAWCDispatcher{constraints: constraints}
	deps.Upstream = upstreamCallerAdapter{upstream: &fakeUpstreamToolCaller{result: "ok"}}

	if _, err := mcp.InvokeToolWithPolicy(ctx, deps, mcp.ToolCallParams{
		Name:      "fs.read_file",
		Arguments: json.RawMessage(`{"path":"/etc/hostname"}`),
	}, "cordum.builtin"); err != nil {
		t.Fatalf("InvokeToolWithPolicy returned err: %v", err)
	}

	page, err := waitForEdgeEvents(t.Context(), t, deps.EventEmitter, tenantID, sessionID, executionID, 2)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(page.Items) < 2 {
		t.Fatalf("edge stream has %d events; want >=2 for AWC pre+post", len(page.Items))
	}
	post := page.Items[1]
	if post.Kind != edgecore.EventKindMCPToolPost {
		t.Fatalf("event[1].Kind = %q, want %q", post.Kind, edgecore.EventKindMCPToolPost)
	}
	if post.Decision != edgecore.DecisionConstrain {
		t.Fatalf("post.Decision = %q on AWC; want %q (bundled task-3d5c4f37 contract)",
			post.Decision, edgecore.DecisionConstrain)
	}
	if got, want := post.Constraints["max_bytes"], constraints["max_bytes"]; got != want {
		t.Fatalf("post.Constraints[max_bytes] = %#v, want %#v", got, want)
	}
	if got, want := post.Constraints["redaction"], constraints["redaction"]; got != want {
		t.Fatalf("post.Constraints[redaction] = %#v, want %#v", got, want)
	}
}

// bootMCPPolicyGateE2E boots a real gateway with the policy gate wired,
// seeds an EdgeSession + AgentExecution so AppendEvent has a valid
// parent, returns a fully-wired ToolCallDeps the test drives directly
// (no HTTP middleware because the gateway → mcp.CallMetadata bridge is
// a separate follow-up task — see acceptance criterion #1 + #2 only
// cover the boot wiring, NOT the request-scoped metadata bridge).
//
// Returns the deps + a context carrying mcp.CallMetadata pointing at
// the seeded session + execution + the IDs themselves so tests can
// query ListEvents. Session / execution IDs are derived from the test
// name so parallel tests under the same package don't race on the
// same edge-store keys.
func bootMCPPolicyGateE2E(t *testing.T) (mcp.ToolCallDeps, context.Context, string, string, string) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })

	configurePolicyGateBootDeps(t, s)
	setMCPConfigForBoot(t, s, true, true)

	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	runtime := s.getMCPRuntime()
	if runtime == nil || runtime.server == nil {
		t.Fatal("expected booted MCP runtime")
	}
	if !runtime.server.HasPolicyGate() {
		t.Fatal("HasPolicyGate() = false; boot wiring regressed")
	}

	// Per-test unique IDs so the t.Parallel() siblings each get their
	// own session/execution scope on the shared miniredis.
	slug := strings.ReplaceAll(strings.ToLower(t.Name()), "/", "_")
	tenantID := "tnt_" + slug
	sessionID := "sess_" + slug
	executionID := "exec_" + slug

	started := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	session := edgecore.EdgeSession{
		SessionID:         sessionID,
		TenantID:          tenantID,
		PrincipalID:       "p1",
		PrincipalType:     edgecore.PrincipalTypeHuman,
		AgentProduct:      "Claude Code",
		AgentVersion:      "2.1.0",
		Mode:              edgecore.SessionModeLocalDev,
		PolicySnapshot:    "policy-e2e",
		EnforcementLayers: edgecore.EnforcementLayers{"mcp": true},
		PolicyMode:        edgecore.PolicyModeEnforce,
		Status:            edgecore.SessionStatusRunning,
		RiskSummary:       edgecore.RiskSummary{MaxRisk: edgecore.RiskLevelLow},
		StartedAt:         started,
	}
	if err := s.edgeStore.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	execution := edgecore.AgentExecution{
		ExecutionID:    executionID,
		SessionID:      sessionID,
		TenantID:       tenantID,
		Adapter:        edgecore.AdapterMCPGateway,
		Mode:           edgecore.ExecutionModeLocalDev,
		PolicySnapshot: "policy-e2e",
		Status:         edgecore.ExecutionStatusRunning,
		StartedAt:      started.Add(time.Second),
	}
	if err := s.edgeStore.CreateExecution(ctx, execution); err != nil {
		t.Fatalf("CreateExecution: %v", err)
	}

	deps := mcp.ToolCallDeps{
		Pipeline:      runtime.server.PolicyDispatcher(),
		EventEmitter:  runtime.server.PolicyEventEmitter(),
		ArtifactStore: runtime.server.PolicyArtifactStore(),
		Redactor:      mcp.DefaultRedactor(),
		Clock:         func() time.Time { return started.Add(2 * time.Second) },
	}
	callCtx := mcp.WithCallMetadata(ctx, mcp.CallMetadata{
		Tenant:      tenantID,
		Principal:   "p1",
		AgentID:     "agent_alpha",
		SessionID:   sessionID,
		ExecutionID: executionID,
	})
	return deps, callCtx, tenantID, sessionID, executionID
}

// waitForEdgeEvents polls the underlying edge.Store backing the emitter
// adapter until at least minCount events are persisted under
// (tenant, session, execution). Returns the page so the caller can
// assert on Kind / EventID / Decision / Constraints. The emitter must
// be an edgeStoreEventEmitter built over the gateway's edgeStore.
func waitForEdgeEvents(ctx context.Context, t *testing.T, emitter mcp.EventEmitter, tenant, session, execution string, minCount int) (edgecore.EventPage, error) {
	t.Helper()
	store, ok := emitter.(edgeStoreEventEmitter)
	if !ok {
		t.Fatalf("emitter is %T; want edgeStoreEventEmitter for direct ListEvents access", emitter)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		page, err := store.store.ListEvents(ctx, edgecore.ListEventsQuery{
			TenantID:    tenant,
			SessionID:   session,
			ExecutionID: execution,
			Limit:       16,
		})
		if err != nil {
			return edgecore.EventPage{}, err
		}
		if len(page.Items) >= minCount {
			return page, nil
		}
		if time.Now().After(deadline) {
			return page, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForEdgeEventsByKind(ctx context.Context, t *testing.T, emitter mcp.EventEmitter, tenant, session, execution string, kind edgecore.EventKind) ([]edgecore.AgentActionEvent, error) {
	t.Helper()
	page, err := waitForEdgeEvents(ctx, t, emitter, tenant, session, execution, 1)
	if err != nil {
		return nil, err
	}
	out := page.Items[:0]
	for _, evt := range page.Items {
		if evt.Kind == kind {
			out = append(out, evt)
		}
	}
	return out, nil
}

// fakeAWCDispatcher returns ALLOW_WITH_CONSTRAINTS with a fixed constraint
// map. Production action gates do not emit this decision; this fake stands in
// to exercise the compatibility propagation data path through the production
// emitter + artifact store. The Pipeline interface lets tests inject the fake
// while keeping the rest of the boot-wired deps real.
type fakeAWCDispatcher struct {
	constraints map[string]any
}

func (a fakeAWCDispatcher) Dispatch(_ context.Context, _ *config.PolicyInput) (mcp.PolicyDecision, bool) {
	return mcp.PolicyDecision{
		Decision:    pb.DecisionType_DECISION_TYPE_ALLOW_WITH_CONSTRAINTS,
		GateID:      "test.awc",
		Code:        "constrained",
		Reason:      "test ceiling",
		Constraints: a.constraints,
	}, true
}

// passingDispatcher returns "no gate fired" (zero decision, fired=false)
// so EvaluateToolCall treats the call as an implicit allow. Used by
// non-AWC E2E tests that want to exercise the adapter chain (emitter +
// artifact store) without dragging in the production gate-decision
// logic (agent identity store + tool allowlists, covered separately by
// actiongates/mcp_gate_test.go).
type passingDispatcher struct{}

func (passingDispatcher) Dispatch(_ context.Context, _ *config.PolicyInput) (mcp.PolicyDecision, bool) {
	return mcp.PolicyDecision{}, false
}

// fakeUpstreamToolCaller records invocations for the E2E test, mirroring
// the bridge_policy_test.go shape but isolated to this file so it can
// run with default tags without import collisions.
type fakeUpstreamToolCaller struct {
	calls  int
	result string
	err    error
}

// upstreamCallerAdapter satisfies mcp.UpstreamToolCaller by delegating
// to a recording fakeUpstreamToolCaller. The intermediate adapter
// exists so the test can keep the fake's accessors local while
// satisfying the mcp interface contract.
type upstreamCallerAdapter struct {
	upstream *fakeUpstreamToolCaller
}

func (u upstreamCallerAdapter) Invoke(_ context.Context, _ mcp.ToolCallParams) (*mcp.ToolCallResult, error) {
	u.upstream.calls++
	if u.upstream.err != nil {
		return nil, u.upstream.err
	}
	return &mcp.ToolCallResult{
		Content: []mcp.ContentItem{{Type: "text", Text: u.upstream.result}},
	}, nil
}
