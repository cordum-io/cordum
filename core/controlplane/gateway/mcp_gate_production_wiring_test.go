package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/mcp"
)

// Production-wiring coverage for task-f44be149. Gateways always build an
// Edge store, but with mcp.policy_gate_enabled=false (the default) nothing
// consumes `_approval_ref` and no parent action event exists, so the gate
// must not mint Edge approvals. The legacy MCPApprovalStore is then the sole
// approval authority and -32099 advertises retry_with_same_args.

const (
	pwToolName = "cordum_pw_gated_counter"
	pwArgs     = `{"target":"prod","count":3}`
)

type pwAuth struct {
	testAuthProvider
	keys map[string]auth.AuthContext
}

func (a pwAuth) AuthenticateHTTP(r *http.Request) (*auth.AuthContext, error) {
	key := strings.TrimSpace(r.Header.Get("X-API-Key"))
	ac, ok := a.keys[key]
	if !ok {
		return nil, errors.New("unauthorized")
	}
	ac.APIKey = key
	ac.AuthSource = auth.AuthSourceAPIKey
	return &ac, nil
}

type pwReply struct {
	Result *struct {
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	} `json:"error"`
}

type pwHarness struct {
	t     *testing.T
	srv   *httptest.Server
	calls atomic.Int64
}

// newPWHarness boots the HTTP MCP routes the way production does: Edge store
// set, policy gate flag unset. withPolicyDoc seeds the policy-bundle doc the
// old mint needed for its snapshot.
func newPWHarness(t *testing.T, withPolicyDoc bool) *pwHarness {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = pwAuth{keys: map[string]auth.AuthContext{
		"key-pw-alice": {Tenant: "default", PrincipalID: "pw-alice", Role: "admin"},
		"key-pw-bob":   {Tenant: "default", PrincipalID: "pw-bob", Role: "admin"},
	}}
	s.edgeStore = edge.NewRedisStoreFromClient(s.redisClient())
	s.shutdownCh = make(chan struct{})
	t.Cleanup(func() { close(s.shutdownCh) })
	ctx := context.Background()
	docs := []*configsvc.Document{{Scope: configsvc.ScopeSystem, ScopeID: "default",
		Data: map[string]any{"mcp": map[string]any{"enabled": true, "transport": "http"}}}}
	if withPolicyDoc {
		docs = append(docs, &configsvc.Document{Scope: configsvc.Scope(packs.PolicyConfigScope),
			ScopeID: packs.PolicyConfigID, Data: map[string]any{packs.PolicyConfigKey: map[string]any{}}})
	}
	for _, doc := range docs {
		if err := s.configSvc.Set(ctx, doc); err != nil {
			t.Fatalf("set config %s/%s: %v", doc.Scope, doc.ScopeID, err)
		}
	}
	if _, err := s.agentIdentityStore.Create(ctx, store.AgentIdentity{
		ID: "agent-pw-alice", TenantID: "default", Name: "agent-pw-alice", Owner: "ops@example.com",
		RiskTier: "high", AllowedTools: []string{"cordum_*"},
	}); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if err := s.agentIdentityStore.LinkWorker(ctx, "agent-pw-alice", "pw-alice"); err != nil {
		t.Fatalf("link worker: %v", err)
	}
	mux := http.NewServeMux()
	if err := s.registerMCPRoutes(mux); err != nil {
		t.Fatalf("register mcp routes: %v", err)
	}
	mux.HandleFunc("POST /api/v1/mcp/approvals/{id}/approve", s.handleMCPApprovalApprove)
	h := &pwHarness{t: t}
	raw, ok := gatewayMCPState.Load(s)
	if !ok {
		t.Fatal("mcp runtime state not registered")
	}
	tool := mcp.Tool{Name: pwToolName, RequiresApproval: true, InputSchema: map[string]any{"type": "object"}}
	if err := raw.(*mcpRuntimeState).toolRegistry.Register(tool, func(context.Context, json.RawMessage) (*mcp.ToolCallResult, error) {
		h.calls.Add(1)
		return &mcp.ToolCallResult{Content: []mcp.ContentItem{{Type: "text", Text: `{"ok":true}`}}}, nil
	}); err != nil {
		t.Fatalf("register counting tool: %v", err)
	}
	h.srv = httptest.NewServer(apiKeyMiddleware(s.auth, mux))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *pwHarness) post(key, path, body string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Tenant-ID", "default")
	return http.DefaultClient.Do(req)
}

// call issues tools/call for the counting tool as alice. Goroutine-safe.
func (h *pwHarness) call() pwReply {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + pwToolName + `","arguments":` + pwArgs + `}}`
	var reply pwReply
	resp, err := h.post("key-pw-alice", "/mcp/message", body)
	if err != nil {
		h.t.Errorf("tools/call: %v", err)
		return reply
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		h.t.Errorf("tools/call HTTP status = %d, want 200", resp.StatusCode)
		return reply
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		h.t.Errorf("decode tools/call reply: %v", err)
	}
	return reply
}

func (h *pwHarness) approve(key, approvalID string) int {
	h.t.Helper()
	resp, err := h.post(key, "/api/v1/mcp/approvals/"+approvalID+"/approve", `{"reason":"ok"}`)
	if err != nil {
		h.t.Fatalf("approve: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// requireLegacyApproval asserts a -32099 on the legacy path and returns its id.
func (h *pwHarness) requireLegacyApproval(r pwReply) string {
	h.t.Helper()
	if r.Error == nil || r.Error.Code != -32099 {
		h.t.Fatalf("want JSON-RPC -32099 approval_required, got error=%+v result=%+v", r.Error, r.Result)
	}
	id, _ := r.Error.Data["approval_id"].(string)
	if id == "" {
		h.t.Fatalf("-32099 without approval_id: %+v", r.Error.Data)
	}
	// Without the policy gate the Edge approval is still minted beside the
	// legacy record, but nothing consumes `_approval_ref`: the reply asks
	// for a same-args retry, which claims the pair (claimApprovedTwin).
	if ref, _ := r.Error.Data["approval_ref"].(string); ref == "" {
		h.t.Errorf("-32099 without approval_ref: %+v", r.Error.Data)
	}
	if hint, _ := r.Error.Data["retry_hint"].(string); hint != "retry_with_same_args" {
		h.t.Errorf("retry_hint = %q, want retry_with_same_args", hint)
	}
	return id
}

func (h *pwHarness) requireExecuted(r pwReply) {
	h.t.Helper()
	if r.Error != nil || r.Result == nil || r.Result.IsError {
		h.t.Fatalf("want a successful tool result, got error=%+v result=%+v", r.Error, r.Result)
	}
}

func TestMCPHTTP_ProductionWiringLegacyApprovalExecutesOnce(t *testing.T) {
	t.Parallel()
	h := newPWHarness(t, true)

	before := time.Now()
	first := h.call()
	after := time.Now()
	id := h.requireLegacyApproval(first) // (i)
	expiresAt, err := time.Parse(time.RFC3339Nano, first.Error.Data["expires_at"].(string))
	if err != nil {
		t.Fatalf("expires_at not RFC3339: %v", err)
	}
	lexpAssertWithinTTL(t, expiresAt, before, after)
	if n := h.calls.Load(); n != 0 {
		t.Fatalf("tool ran %d times before approval, want 0", n)
	}

	if code := h.approve("key-pw-alice", id); code != http.StatusForbidden { // (ii)
		t.Fatalf("self-approval status = %d, want 403", code)
	}
	if h.requireLegacyApproval(h.call()) == "" || h.calls.Load() != 0 {
		t.Fatalf("self-approval authorized execution: calls=%d", h.calls.Load())
	}

	if code := h.approve("key-pw-bob", id); code != http.StatusOK { // (iii)
		t.Fatalf("operator approval status = %d, want 200", code)
	}
	h.requireExecuted(h.call())
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("calls after approved retry = %d, want 1", n)
	}

	again := h.requireLegacyApproval(h.call()) // (iv) consume-once
	if n := h.calls.Load(); n != 1 {
		t.Fatalf("calls after second retry = %d, want 1", n)
	}

	if code := h.approve("key-pw-bob", again); code != http.StatusOK { // (v)
		t.Fatalf("fresh approval status = %d, want 200", code)
	}
	replies := make([]pwReply, 4)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Add(1)
		go func(i int) { defer wg.Done(); replies[i] = h.call() }(i)
	}
	wg.Wait()
	executed, gated := 0, 0
	for _, r := range replies {
		switch {
		case r.Error == nil && r.Result != nil && !r.Result.IsError:
			executed++
		case r.Error != nil && r.Error.Code == -32099:
			gated++
		default:
			t.Errorf("unexpected concurrent reply: error=%+v result=%+v", r.Error, r.Result)
		}
	}
	if executed != 1 || gated != 3 || h.calls.Load() != 2 {
		t.Fatalf("concurrent retries: executed=%d gated=%d calls=%d, want 1/3/2", executed, gated, h.calls.Load())
	}
}

// The old mint also failed "policy_snapshot is required" without a policy doc.
func TestMCPHTTP_ProductionWiringWithoutPolicyDocReturns32099(t *testing.T) {
	t.Parallel()
	h := newPWHarness(t, false)
	h.requireLegacyApproval(h.call())
	if n := h.calls.Load(); n != 0 {
		t.Fatalf("tool ran %d times before approval, want 0", n)
	}
}

func pwNewMCPServer() *mcp.MCPServer {
	return mcp.NewServer(mcp.NewHTTPTransport(mcp.DefaultMaxMessageBytes, mcp.DefaultHTTPResponseTimeout),
		mcp.NewToolRegistry(), mcp.NewResourceRegistry(),
		mcp.ServerConfig{Name: "cordum", ProtocolVersion: mcp.DefaultProtocolVersion})
}

// pwGateCtx carries both the gateway call metadata and an Edge session, so
// a wired mint is attempted.
func pwGateCtx() context.Context {
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant: "default", AgentID: "agent-pw", Principal: "pw-alice"})
	return mcp.WithCallMetadata(ctx, mcp.CallMetadata{Tenant: "default", Principal: "pw-alice",
		AgentID: "agent-pw", SessionID: "pw-sess", ExecutionID: "pw-exec"})
}

func pwSnapshot(context.Context) string { return "policy-pw-1" }

func pwCheck(t *testing.T, gate *gatewayApprovalGate, ctx context.Context) (*mcp.ApprovalRequired, error) {
	t.Helper()
	return gate.Check(ctx, mcp.Tool{Name: pwToolName, RequiresApproval: true}, json.RawMessage(pwArgs))
}

func TestAttachMCPPolicyDeps_WiresEdgeMintWithHold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, serverName, want string }{
		{"builtin", "", mcpPolicyServerName},
		{"upstream", "cordum.monday", "cordum.monday"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			configurePolicyGateBootDeps(t, s)
			gate := NewGatewayApprovalGate(newTestMCPStore(t)).(*gatewayApprovalGate)
			got, reason := s.attachMCPPolicyDepsNamed(pwNewMCPServer(), gate, tc.serverName)
			if reason != "" || !got.HasPolicyGate() || !got.HasApprovalHold() {
				t.Fatalf("attach degraded: reason=%q gate=%v hold=%v", reason, got.HasPolicyGate(), got.HasApprovalHold())
			}
			if gate.edgeStore != s.edgeStore || gate.policySnapshot == nil {
				t.Fatalf("Edge mint not wired with the hold: edgeStore=%v snapshot=%v", gate.edgeStore, gate.policySnapshot != nil)
			}
			if gate.serverName != tc.want || got.PolicyServerName() != tc.want {
				t.Fatalf("mint server=%q hold server=%q, want both %q", gate.serverName, got.PolicyServerName(), tc.want)
			}
			if _, reason := s.attachMCPPolicyDepsNamed(pwNewMCPServer(), nil, tc.serverName); reason != "" {
				t.Fatalf("nil gate attach reason = %q", reason)
			}
		})
	}
	t.Run("degraded", func(t *testing.T) {
		s, _, _ := newTestGateway(t)
		s.edgeStore = edge.NewRedisStoreFromClient(s.redisClient()) // actionGatePipeline stays nil
		gate := NewGatewayApprovalGate(newTestMCPStore(t)).(*gatewayApprovalGate)
		if _, reason := s.attachMCPPolicyDepsNamed(pwNewMCPServer(), gate, ""); reason == "" {
			t.Fatal("attach without actionGatePipeline did not degrade")
		}
		if gate.edgeStore != nil || gate.serverName != "" {
			t.Fatalf("degraded attach wired the Edge mint: edgeStore=%v serverName=%q", gate.edgeStore, gate.serverName)
		}
	})
}

// DoD-2. core/mcp TestJSONRPC_ApprovalStoreUnavailableWiresTo32096
// (server_approval_hold_e2e_test.go) pins ErrApprovalStoreUnavailable -> -32096.
func TestGate_EdgeMintOutageFailsClosed(t *testing.T) {
	t.Parallel()
	edgeStore := &captureEdgeStore{enqueueErr: errors.New("dial tcp 10.0.0.7:6379: connect: connection refused")}
	gate := NewGatewayApprovalGate(newTestMCPStore(t)).(*gatewayApprovalGate)
	gate.WithEdgeApprovalMint(edgeStore, pwSnapshot, mcpPolicyServerName)
	got, err := pwCheck(t, gate, pwGateCtx())
	if got != nil {
		t.Fatalf("Edge outage returned ApprovalRequired %+v; want fail closed", got)
	}
	if !errors.Is(err, mcp.ErrApprovalStoreUnavailable) {
		t.Fatalf("err = %v; want mcp.ErrApprovalStoreUnavailable", err)
	}
	if edgeStore.lastReq.SessionID != "pw-sess" {
		t.Fatalf("Edge mint was not attempted: %+v", edgeStore.lastReq)
	}
}

func TestGate_RetryHintReflectsMintedPath(t *testing.T) {
	t.Parallel()
	// The hint follows the `_approval_ref` consume path: a minted ref is
	// advertised only while the MCP server's approval hold can claim it.
	t.Run("edge_minted_with_hold", func(t *testing.T) {
		gate := NewGatewayApprovalGate(newTestMCPStore(t)).(*gatewayApprovalGate)
		gate.WithEdgeApprovalMint(&captureEdgeStore{approvalID: "edge-ref"}, pwSnapshot, mcpPolicyServerName)
		gate.approvalHold = true
		got, err := pwCheck(t, gate, pwGateCtx())
		if err != nil || got == nil {
			t.Fatalf("Check = %+v, %v", got, err)
		}
		if got.RetryHint != "retry_with_approval_ref" || got.ApprovalRef != "edge-ref" || got.ApprovalID == "edge-ref" {
			t.Fatalf("hint=%q ref=%q id=%q; want retry_with_approval_ref/edge-ref/legacy id", got.RetryHint, got.ApprovalRef, got.ApprovalID)
		}
	})
	legacy := map[string]func(*gatewayApprovalGate) context.Context{
		"no_mint_wired": func(*gatewayApprovalGate) context.Context { return pwGateCtx() },
		"no_edge_session": func(g *gatewayApprovalGate) context.Context {
			g.WithEdgeApprovalMint(&captureEdgeStore{}, pwSnapshot, mcpPolicyServerName)
			return WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "default", AgentID: "agent-pw"})
		},
	}
	for name, setup := range legacy {
		t.Run(name, func(t *testing.T) {
			gate := NewGatewayApprovalGate(newTestMCPStore(t)).(*gatewayApprovalGate)
			got, err := pwCheck(t, gate, setup(gate))
			if err != nil || got == nil {
				t.Fatalf("Check = %+v, %v", got, err)
			}
			if got.RetryHint != "retry_with_same_args" || got.ApprovalRef != got.ApprovalID {
				t.Fatalf("hint=%q ref=%q id=%q; want retry_with_same_args with ref == id", got.RetryHint, got.ApprovalRef, got.ApprovalID)
			}
		})
	}
}
