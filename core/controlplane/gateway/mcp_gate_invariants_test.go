package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// TestWireMCPApprovalGateAttachesInvariantLookupWithoutManualInjection is
// the EDGE-052 reopen #1 regression test. It builds the gate via the
// production wiring path (s.wireMCPApprovalGate), passes an invariant
// lookup as the helper's parameter, and verifies that:
//
//	(a) the helper attaches the lookup (gate.invariants != nil after
//	    construction — this was nil in production before the fix),
//	(b) an invariant DENY blocks a RequiresApproval=true tool call
//	    through the wired gate WITHOUT the test caller ever invoking
//	    gate.WithInvariantLookup directly.
//
// The pre-fix code at handlers_mcp.go:93-99 created the gate via
// NewGatewayApprovalGate, wired only gate.preapproval, and called
// SetApprovalGate — never WithInvariantLookup. That meant g.invariants
// stayed nil in production, matchMCPInvariantDeny returned no rules,
// and admin-authored secops/invariants MCP rules surfaced via
// RulesForMCPTool() and GET /api/v1/policy/global were silently ignored
// at the gate. This test pins the wiring contract so the bug can't
// regress without a test failure.
func TestWireMCPApprovalGateAttachesInvariantLookupWithoutManualInjection(t *testing.T) {
	t.Parallel()
	srv := &server{}
	store := newTestMCPStore(t)

	// Production wiring would pass a closure reading from
	// loadPolicyBundles + loadMCPInvariantsFromBundles. The test passes
	// a stub that returns a single invariant DENY for the synthetic
	// "secrets.read" tool. The point is NOT to exercise the bundle
	// parsing (covered by TestLoadMCPInvariantsFromBundles_*); it's to
	// prove the wiring helper attaches whatever lookup the caller
	// supplies — bug surface was the wiring NOT calling
	// WithInvariantLookup at all.
	invariantLookup := MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-deny-secrets-read",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"secrets.read"}},
				},
				Decision: "deny",
				Reason:   "SecOps invariant — secret reads forbidden",
			},
		}
	})

	rawGate, gate := srv.wireMCPApprovalGate(store, invariantLookup)
	if rawGate == nil {
		t.Fatal("wireMCPApprovalGate returned nil rawGate; production registry would lose the gate entirely")
	}
	if gate == nil {
		t.Fatal("wireMCPApprovalGate returned nil concrete gate; future-proof fallback fired unexpectedly with the standard NewGatewayApprovalGate path")
	}
	if gate.invariants == nil {
		t.Fatal("EDGE-052 reopen #1 regression: wireMCPApprovalGate did not attach invariant lookup; production gate stays permissive on secops/invariants MCP rules")
	}

	// Now exercise the deny path through the production-wired gate. The
	// test caller has NOT called gate.WithInvariantLookup directly —
	// only via wireMCPApprovalGate's parameter. If this assertion holds,
	// the production wiring + invariant consultation chain is intact.
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-a",
	})
	tool := mcp.Tool{Name: "secrets.read", RequiresApproval: true}
	got, err := gate.Check(ctx, tool, json.RawMessage(`{"key":"OPENAI_API_KEY"}`))
	if err == nil {
		t.Fatalf("expected ErrMCPInvariantDeny via wireMCPApprovalGate; got ApprovalRequired %+v (production wiring still failing)", got)
	}
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected ErrMCPInvariantDeny via production wiring, got %v", err)
	}
	if got != nil {
		t.Fatalf("hard deny via invariant must NOT enqueue an approval; got %+v", got)
	}
}

// TestWireMCPApprovalGateNilInvariantLookupSafe confirms the helper
// degrades gracefully when the caller passes a nil lookup — the gate is
// still constructed and installable, but invariants stays nil and the
// approval flow alone applies. Useful for future configurations that
// genuinely want no invariant layer (e.g. a dev sandbox).
func TestWireMCPApprovalGateNilInvariantLookupSafe(t *testing.T) {
	t.Parallel()
	srv := &server{}
	store := newTestMCPStore(t)
	rawGate, gate := srv.wireMCPApprovalGate(store, nil)
	if rawGate == nil || gate == nil {
		t.Fatal("wireMCPApprovalGate returned nil for nil lookup; gate must still install with approval flow alone")
	}
	if gate.invariants != nil {
		t.Fatal("nil invariant lookup must leave gate.invariants nil; helper attached something unexpectedly")
	}
}

// TestInvariantDenyBeatsMCPAllow proves the SECURITY FLOOR rail:
// when an invariant DENY rule covers a tool, the MCP gate returns a hard
// deny (ErrMCPInvariantDeny) WITHOUT enqueuing an approval. This holds
// even if the tool's RequiresApproval=true would otherwise route through
// the approval store.
func TestInvariantDenyBeatsMCPAllow(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	// SecOps invariant: deny the "secrets.read" tool.
	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-deny-secrets-read",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"secrets.read"}},
				},
				Decision: "deny",
				Reason:   "SecOps invariant — secret reads forbidden",
			},
		}
	}))

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-a",
	})
	tool := mcp.Tool{Name: "secrets.read", RequiresApproval: true}

	got, err := gate.Check(ctx, tool, json.RawMessage(`{"key":"OPENAI_API_KEY"}`))
	if err == nil {
		t.Fatalf("expected ErrMCPInvariantDeny; got ApprovalRequired %+v", got)
	}
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected ErrMCPInvariantDeny, got %v", err)
	}
	if got != nil {
		t.Fatalf("hard deny must NOT enqueue an approval; got %+v", got)
	}
}

// TestInvariantNoMatchProceedsToApproval confirms the gate does not
// short-circuit when invariants do not cover the tool — the existing
// approval flow continues and an ApprovalRequired is enqueued.
func TestInvariantNoMatchProceedsToApproval(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	// Invariant denies a different tool — should not block our call.
	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-deny-other",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{DenyTools: []string{"shell.exec"}},
				},
				Decision: "deny",
			},
		}
	}))

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-a",
	})
	tool := mcp.Tool{Name: "files.delete", RequiresApproval: true}

	got, err := gate.Check(ctx, tool, json.RawMessage(`{"path":"/tmp/foo"}`))
	if err != nil {
		t.Fatalf("expected approval flow to proceed, got err %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired (not invariant-blocked); got nil")
	}
	if got.Tool != "files.delete" {
		t.Fatalf("expected tool=files.delete, got %q", got.Tool)
	}
}

// TestInvariantAllowToolsImplicitDeny proves the allowlist-inversion
// invariant pattern: SecOps authors `mcp.allow_tools: [calculator]`
// invariant rule with decision=deny, which means "if the tool is not in
// the allow list, block". Verifies that a tool not in AllowTools triggers
// the invariant.
func TestInvariantAllowToolsImplicitDeny(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-allowlist-only",
				Match: config.PolicyMatch{
					MCP: config.MCPPolicy{AllowTools: []string{"calculator", "search"}},
				},
				Decision: "deny",
				Reason:   "only calculator and search are allowed in this tenant",
			},
		}
	}))

	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t1", AgentID: "agent-a"})

	// "files.delete" is NOT in the allow list — invariant must fire.
	_, err := gate.Check(ctx, mcp.Tool{Name: "files.delete", RequiresApproval: true},
		json.RawMessage(`{"path":"/"}`))
	if !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected ErrMCPInvariantDeny for non-allowlisted tool, got %v", err)
	}

	// "calculator" IS in the allow list — invariant must NOT fire and
	// the call proceeds to existing approval flow.
	got, err := gate.Check(ctx, mcp.Tool{Name: "calculator", RequiresApproval: true},
		json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("allowlisted tool should not be blocked, got %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired for allowlisted tool, got nil")
	}
}

// TestInvariantTenantScopedDeny verifies that an invariant scoped to a
// specific tenant only fires for that tenant — calls from other tenants
// proceed to the regular approval flow unhindered.
func TestInvariantTenantScopedDeny(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store).(*gatewayApprovalGate)

	gate.WithInvariantLookup(MCPInvariantLookupFunc(func(_ context.Context) []config.PolicyRule {
		return []config.PolicyRule{
			{
				ID: "inv-tenant-restricted",
				Match: config.PolicyMatch{
					Tenants: []string{"sensitive-tenant"},
					MCP:     config.MCPPolicy{DenyTools: []string{"shell.exec"}},
				},
				Decision: "deny",
			},
		}
	}))

	tool := mcp.Tool{Name: "shell.exec", RequiresApproval: true}

	// Tenant in scope → invariant fires.
	ctxSensitive := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "sensitive-tenant", AgentID: "a"})
	if _, err := gate.Check(ctxSensitive, tool, json.RawMessage(`{}`)); !errors.Is(err, ErrMCPInvariantDeny) {
		t.Fatalf("expected invariant deny for sensitive tenant, got %v", err)
	}

	// Different tenant → invariant skipped, approval flow proceeds.
	ctxOther := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "other-tenant", AgentID: "a"})
	got, err := gate.Check(ctxOther, tool, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("different tenant should not be blocked, got %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired for different tenant, got nil")
	}
}

// TestLoadMCPInvariantsFromBundles verifies the gateway-server-side
// helper that reads bundles via configsvc and projects the invariant
// rules — proves the wiring path the gateway uses to feed the gate's
// MCPInvariantLookup.
func TestLoadMCPInvariantsFromBundles(t *testing.T) {
	t.Parallel()
	bundles := map[string]any{
		policybundles.PolicyInvariantsBundleKey: map[string]any{
			"content": `version: "1"
rules:
  - id: inv-deny-mcp-fs-read
    match:
      mcp:
        deny_tools: ["fs.read"]
    decision: deny
    reason: SecOps invariant — fs reads forbidden
`,
		},
	}
	rules, err := loadMCPInvariantsFromBundles(bundles)
	if err != nil {
		t.Fatalf("loadMCPInvariantsFromBundles: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 invariant rule, got %d (%+v)", len(rules), rules)
	}
	if rules[0].ID != "inv-deny-mcp-fs-read" {
		t.Fatalf("expected inv-deny-mcp-fs-read, got %q", rules[0].ID)
	}
	if !slices.Contains(rules[0].Match.MCP.DenyTools, "fs.read") {
		t.Fatalf("expected fs.read in DenyTools, got %+v", rules[0].Match.MCP.DenyTools)
	}
}

// TestMCPGateMatchesKernelRuleSemantics pins that the gate and the Safety
// Kernel's rule matcher (config.MatchRule) read a deny invariant's match.mcp
// lists the same way. Same table as safetykernel
// TestKernelInvariants_MCPListsMatchGate. Plain (non-MCP) jobs never reach
// the gate, so for them only the kernel side is checked: it must not match.
func TestMCPGateMatchesKernelRuleSemantics(t *testing.T) {
	const plain = "-"
	for _, tc := range []struct {
		name         string
		lists        config.MCPPolicy
		server, tool string
		wantDeny     bool
	}{
		{"deny_tools listed tool", config.MCPPolicy{DenyTools: []string{"fs.read"}}, "fs", "fs.read", true},
		{"deny_tools other tool", config.MCPPolicy{DenyTools: []string{"fs.read"}}, "fs", "fs.write", false},
		{"deny_tools plain job", config.MCPPolicy{DenyTools: []string{"fs.read"}}, plain, plain, false},
		{"allow_tools listed tool", config.MCPPolicy{AllowTools: []string{"calculator"}}, "tools", "calculator", false},
		{"allow_tools other tool", config.MCPPolicy{AllowTools: []string{"calculator"}}, "tools", "shell.exec", true},
		{"allow_tools plain job", config.MCPPolicy{AllowTools: []string{"calculator"}}, plain, plain, false},
		{"deny_servers listed server", config.MCPPolicy{DenyServers: []string{"internal"}}, "internal", "x", true},
		{"deny_servers other server", config.MCPPolicy{DenyServers: []string{"internal"}}, "github", "x", false},
		{"allow_servers listed server", config.MCPPolicy{AllowServers: []string{"github"}}, "github", "x", false},
		{"allow_servers other server", config.MCPPolicy{AllowServers: []string{"github"}}, "jira", "x", true},
		{"allow_servers plain job", config.MCPPolicy{AllowServers: []string{"github"}}, plain, plain, false},
	} {
		rule := config.PolicyRule{ID: "inv-mcp", Decision: "deny", Match: config.PolicyMatch{MCP: tc.lists}}
		input := config.PolicyInput{Tenant: "default", Topic: "job.default"}
		if tc.tool != plain {
			input.MCP = config.MCPRequest{Server: tc.server, Tool: tc.tool}
			_, gateDeny := matchMCPInvariantDeny([]config.PolicyRule{rule},
				mcp.Tool{Name: tc.tool, ApprovalScope: tc.server}, MCPCallMetadata{Tenant: "default"})
			if gateDeny != tc.wantDeny {
				t.Errorf("%s: gate deny = %v; want %v", tc.name, gateDeny, tc.wantDeny)
			}
		}
		if kernelDeny := config.MatchRule(rule, input); kernelDeny != tc.wantDeny {
			t.Errorf("%s: kernel rule match = %v; want %v", tc.name, kernelDeny, tc.wantDeny)
		}
	}
}

func TestMCPInvariantBlockDecisionDenies(t *testing.T) {
	rule := config.PolicyRule{ID: "inv-block-fs", Decision: "block",
		Match: config.PolicyMatch{MCP: config.MCPPolicy{DenyTools: []string{"fs.read"}}}}
	if _, denied := matchMCPInvariantDeny([]config.PolicyRule{rule}, mcp.Tool{Name: "fs.read"}, MCPCallMetadata{Tenant: "default"}); !denied {
		t.Fatal("a block invariant must deny like a deny invariant")
	}
}

// TestMCPInvariantFloorCoversNonGatedTools runs tools/call through the
// production pieces (ToolRegistry plus wireMCPApprovalGate, as
// registerMCPRoutes wires them). A deny invariant blocks a tool that is not
// approval-gated before its handler runs, a tool it does not cover still
// runs, and a gated tool it does not cover still waits for approval.
func TestMCPInvariantFloorCoversNonGatedTools(t *testing.T) {
	t.Parallel()
	srv := &server{}
	lookup := MCPInvariantLookupFunc(func(context.Context) []config.PolicyRule {
		return []config.PolicyRule{{
			ID: "inv-deny-approve", Decision: "deny", Reason: "agents may not approve jobs",
			Match: config.PolicyMatch{MCP: config.MCPPolicy{DenyTools: []string{mcp.ToolApproveJob}}},
		}}
	})
	rawGate, _ := srv.wireMCPApprovalGate(newTestMCPStore(t), lookup)
	registry := mcp.NewToolRegistry()
	registry.SetApprovalGate(rawGate)
	ran := map[string]bool{}
	for _, tool := range []mcp.Tool{
		{Name: mcp.ToolApproveJob},
		{Name: mcp.ToolListJobs},
		{Name: mcp.ToolInstallPack, RequiresApproval: true, ApprovalScope: mcp.ApprovalScopeWrite},
	} {
		name := tool.Name
		if err := registry.Register(tool, func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
			ran[name] = true
			return &mcp.ToolCallResult{}, nil
		}); err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
	}
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t1", AgentID: "agent-a", Principal: "agent-a"})
	ctx = mcp.WithTenant(mcp.ContextWithIdentity(ctx, &mcp.AgentIdentity{ID: "agent-a"}), "t1")

	if _, err := registry.Call(ctx, mcp.ToolApproveJob, json.RawMessage(`{"job_id":"j1"}`)); !errors.Is(err, ErrMCPInvariantDeny) || ran[mcp.ToolApproveJob] {
		t.Fatalf("deny invariant must block the non-gated tool before it runs: err=%v ran=%v", err, ran[mcp.ToolApproveJob])
	}
	if _, err := registry.Call(ctx, mcp.ToolListJobs, json.RawMessage(`{}`)); err != nil || !ran[mcp.ToolListJobs] {
		t.Fatalf("uncovered non-gated tool must run: err=%v ran=%v", err, ran[mcp.ToolListJobs])
	}
	_, err := registry.Call(ctx, mcp.ToolInstallPack, json.RawMessage(`{"pack_id":"p1"}`))
	var required *mcp.ApprovalRequired
	if !errors.As(err, &required) || ran[mcp.ToolInstallPack] {
		t.Fatalf("uncovered gated tool must still wait for approval: err=%v ran=%v", err, ran[mcp.ToolInstallPack])
	}
}

// TestMCPInvariantDenyAnsweredLikeActionGateDeny boots the MCP runtime and
// posts tools/call to /mcp/message for a tool a deny invariant covers, on the
// built-in registry and on a fronted upstream, with mcp.policy_gate_enabled
// off and on. Every call is answered like an action-gate DENY: an isError
// result with the invariant's reason, not a JSON-RPC error, and with the
// policy gate on a blocked mcp.tool.failed event with decision=deny. The
// fronted upstream is an unreachable TEST-NET address, so a call forwarded to
// it times out instead.
func TestMCPInvariantDenyAnsweredLikeActionGateDeny(t *testing.T) {
	for _, tc := range []struct {
		name                string
		fronted, policyGate bool
	}{
		{"builtin", false, false},
		{"builtin_policy_gate", false, true},
		{"fronted", true, false},
		{"fronted_policy_gate", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(frontingTestSecretEnv, "dummy-token-never-dialed")
			s, _, _ := newTestGateway(t)
			s.auth = mcpTestAuth{apiKey: "test-key", tenant: "default"}
			s.shutdownCh = make(chan struct{})
			t.Cleanup(func() { close(s.shutdownCh) })
			configurePolicyGateBootDeps(t, s)
			setMCPConfigForBoot(t, s, true, tc.policyGate)
			ctx := context.Background()
			tool := mcp.ToolListJobs
			if tc.fronted {
				tool = "delete_board"
				s.mcpUpstreamRegistry = &fakeMCPUpstreamRegistry{
					entries: []edgecore.UpstreamServer{frontingReadyUpstream("default", "cordum.monday")},
				}
			}
			// The built-in registry's scope filter needs an identity allowed the tool.
			if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
				ID: "agent-floor", TenantID: "default", Name: "floor agent", Owner: "secops@example.com",
				RiskTier: "high", AllowedTools: []string{tool}, Status: "active",
			}); err != nil {
				t.Fatalf("create agent identity: %v", err)
			}
			if err := s.configSvc.Set(ctx, &configsvc.Document{
				Scope:   configsvc.Scope(packs.PolicyConfigScope),
				ScopeID: packs.PolicyConfigID,
				Data: map[string]any{packs.PolicyConfigKey: map[string]any{
					policybundles.PolicyInvariantsBundleKey: map[string]any{"content": `version: "1"
rules:
  - id: inv-floor
    match:
      mcp:
        deny_tools: ["` + tool + `"]
    decision: deny
    reason: not for agents
`},
				}},
			}); err != nil {
				t.Fatalf("set invariants bundle: %v", err)
			}

			mux := http.NewServeMux()
			if err := s.registerMCPRoutes(mux); err != nil {
				t.Fatalf("register mcp routes: %v", err)
			}
			rt := s.getMCPRuntime()
			if rt == nil || rt.server == nil || (rt.frontedUpstream == "cordum.monday") != tc.fronted {
				t.Fatalf("runtime %+v, want fronted=%v", rt, tc.fronted)
			}
			emitter := &recordingEdgeEmitter{}
			if tc.policyGate {
				if !rt.server.HasPolicyGate() {
					t.Fatal("policy gate not wired")
				}
				// Let the action gates allow the call, and record its events.
				rt.server.WithPolicyGate(rt.server.PolicyServerName(), mcp.ToolCallDeps{
					Pipeline:      passingDispatcher{},
					EventEmitter:  emitter,
					ArtifactStore: rt.server.PolicyArtifactStore(),
					Redactor:      mcp.DefaultRedactor(),
				})
			}

			srv := httptest.NewServer(mux)
			defer srv.Close()
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp/message", strings.NewReader(
				`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+tool+`","arguments":{}}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-key")
			req.Header.Set("X-Tenant-ID", "default")
			req.Header.Set(mcpAgentIDHeader, "agent-floor")
			resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
			if err != nil {
				t.Fatalf("tools/call got no answer (forwarded to the upstream?): %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			var out struct {
				Result *mcp.ToolCallResult `json:"result"`
				Error  json.RawMessage     `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			const want = "not for agents (invariant inv-floor)"
			if out.Error != nil || out.Result == nil || !out.Result.IsError ||
				len(out.Result.Content) != 1 || out.Result.Content[0].Text != want {
				t.Fatalf("result=%+v error=%s, want an isError result %q", out.Result, out.Error, want)
			}
			if !tc.policyGate {
				return
			}
			if last := emitter.last(); last == nil || last.Kind != edgecore.EventKindMCPToolFailed ||
				last.Status != edgecore.ActionStatusBlocked || last.Decision != edgecore.DecisionDeny || last.DecisionReason != want {
				t.Fatalf("last event = %+v, want a blocked mcp.tool.failed deny", last)
			}
		})
	}
}

type recordingEdgeEmitter struct {
	mu     sync.Mutex
	events []*edgecore.AgentActionEvent
}

func (r *recordingEdgeEmitter) Emit(_ context.Context, evt *edgecore.AgentActionEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
	return nil
}

func (r *recordingEdgeEmitter) last() *edgecore.AgentActionEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.events) == 0 {
		return nil
	}
	return r.events[len(r.events)-1]
}
