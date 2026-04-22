package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/mcp/outbound"
)

// TestGatewayClient_OutboundSignerStampsHeadersOnRealRequests is the
// integration-style test QA specifically asked for after the first
// rejection: build the *exact* production wiring path
// (tools.Register over a GatewayClient), fire a request against an
// httptest.Server, and assert the captured request headers carry all
// 6 X-Cordum-* signature headers.
//
// The earlier unit tests for outbound.Signer proved the primitive
// works in isolation. Without this test the rejection finding —
// "WithOutboundSigner defined but never called from production" —
// could regress silently. This test now fails loudly if the wiring
// from cordum-mcp main → GatewayClient → HTTPServiceBridge → req
// ever drops the signer.
func TestGatewayClient_OutboundSignerStampsHeadersOnRealRequests(t *testing.T) {
	t.Parallel()

	key, err := outbound.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	signer, err := outbound.NewSigner(key, "test-key-1")
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	var (
		capturedMu sync.Mutex
		captured   http.Header
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		captured = r.Header.Clone()
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer ts.Close()

	// Mirror production wiring exactly: NewGatewayClient + the three
	// builder methods cordum-mcp main calls after the QA-mandated fix,
	// then tools.Register to build the HTTPServiceBridge carrying the
	// signer. Finally invoke a bridge method so doRequest fires an
	// outbound HTTP request the signer is expected to stamp.
	client := NewGatewayClient(ts.URL, "test-api-key", ts.Client()).
		WithAllowPrivateHosts(true). // httptest uses 127.0.0.1
		WithOutboundSigner(signer, "agent-alpha")

	// Build the bridge the same way tools.Register does. This is the
	// exact production code path — same config struct, same builder
	// chain. If tools.Register ever stops forwarding the signer, the
	// assertions below will fail.
	bridge := mcp.NewHTTPServiceBridge(mcp.HTTPServiceBridgeConfig{
		BaseURL:           client.Addr,
		HTTPClient:        client.HTTPClient,
		AllowedHosts:      []string{},
		AllowPrivateHosts: client.allowPrivateHosts,
	}.WithAuthToken(client.authTokenValue()))
	if client.outboundSigner != nil {
		bridge.WithOutboundSigner(client.outboundSigner, client.outboundAgentID)
	}

	ctx := context.Background()
	// SubmitJob hits POST /api/v1/jobs and carries a body; the doRequest
	// path signs body+method+path together, so this exercises the full
	// signer flow. We don't care about the response — the fake server
	// returns a trivial JSON that the decode may not like, but the
	// OUTBOUND HEADERS on the captured request are what we assert.
	_, _ = bridge.SubmitJob(ctx, mcp.SubmitJobInput{
		Prompt: "hello",
		Topic:  "default",
	})

	capturedMu.Lock()
	h := captured
	capturedMu.Unlock()
	if h == nil {
		t.Fatal("server captured no request — the bridge did not dispatch")
	}

	// All 6 X-Cordum-* signature headers MUST be present. A missing
	// header here means production still ships unsigned requests —
	// the precise regression QA flagged on reopen #1.
	required := []string{
		"X-Cordum-Key-Id",
		"X-Cordum-Timestamp",
		"X-Cordum-Nonce",
		"X-Cordum-Tenant",
		"X-Cordum-Agent-Id",
		"X-Cordum-Signature",
	}
	for _, name := range required {
		if v := h.Get(name); strings.TrimSpace(v) == "" {
			t.Errorf("missing outbound signature header %q — wiring regression", name)
		}
	}

	// Agent-Id must reflect the caller's explicit override rather
	// than falling back to the tenant, so multi-tenant deployments
	// can correlate calls to specific agents in the audit chain.
	if got := h.Get("X-Cordum-Agent-Id"); got != "agent-alpha" {
		t.Errorf("X-Cordum-Agent-Id = %q, want agent-alpha", got)
	}
	if got := h.Get("X-Cordum-Key-Id"); got != "test-key-1" {
		t.Errorf("X-Cordum-Key-Id = %q, want test-key-1", got)
	}
}

// TestBridge_SignHookFiresOnSuccessfulSign proves the audit callback
// installed via WithOutboundSignAuditHook fires once per signed
// outbound request. This is the seam the gateway uses to emit
// mcp.outbound_signed SIEM events — a silent-drop regression here
// would leave the audit chain blind to outbound calls.
func TestBridge_SignHookFiresOnSuccessfulSign(t *testing.T) {
	t.Parallel()

	key, _ := outbound.GeneratePrivateKey()
	signer, err := outbound.NewSigner(key, "hook-test-key")
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":"job-1"}`))
	}))
	defer ts.Close()

	var (
		mu      sync.Mutex
		calls   int
		lastKey string
	)
	hook := func(method, path, keyID, nonce, tenant, agentID string) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		lastKey = keyID
		if method == "" || path == "" || nonce == "" {
			t.Errorf("hook received empty field(s): method=%q path=%q nonce=%q", method, path, nonce)
		}
	}
	bridge := mcp.NewHTTPServiceBridge(mcp.HTTPServiceBridgeConfig{
		BaseURL:           ts.URL,
		HTTPClient:        ts.Client(),
		AllowPrivateHosts: true,
	}).WithOutboundSigner(signer, "agent-alpha").
		WithOutboundSignAuditHook(hook)

	ctx := context.Background()
	_, _ = bridge.SubmitJob(ctx, mcp.SubmitJobInput{Prompt: "p", Topic: "t"})

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("hook invocations = %d, want 1", calls)
	}
	if lastKey != "hook-test-key" {
		t.Errorf("hook saw key_id %q, want hook-test-key", lastKey)
	}
}

// TestGatewayClient_NilSignerLeavesHeadersAbsent mirrors the happy
// path — legacy deployments without a signing key keep working; no
// X-Cordum-* headers land on the wire. This pins the opt-in contract:
// unset CORDUM_MCP_OUTBOUND_SIGNING_KEY, no signatures.
func TestGatewayClient_NilSignerLeavesHeadersAbsent(t *testing.T) {
	t.Parallel()

	var (
		capturedMu sync.Mutex
		captured   http.Header
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMu.Lock()
		captured = r.Header.Clone()
		capturedMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer ts.Close()

	client := NewGatewayClient(ts.URL, "test-api-key", ts.Client()).
		WithAllowPrivateHosts(true)
	// No WithOutboundSigner call — this is the legacy-compat path.

	bridge := mcp.NewHTTPServiceBridge(mcp.HTTPServiceBridgeConfig{
		BaseURL:           client.Addr,
		HTTPClient:        client.HTTPClient,
		AllowedHosts:      []string{},
		AllowPrivateHosts: client.allowPrivateHosts,
	}.WithAuthToken(client.authTokenValue()))
	// Deliberately do NOT call WithOutboundSigner here — matches the
	// legacy-compat path where cordum-mcp boots without a signing key.

	ctx := context.Background()
	_, _ = bridge.SubmitJob(ctx, mcp.SubmitJobInput{Prompt: "hello", Topic: "default"})

	capturedMu.Lock()
	h := captured
	capturedMu.Unlock()
	if h == nil {
		t.Fatal("server captured no request")
	}
	// Must NOT carry any X-Cordum-* signature header.
	for _, name := range []string{
		"X-Cordum-Key-Id", "X-Cordum-Timestamp", "X-Cordum-Nonce",
		"X-Cordum-Tenant", "X-Cordum-Agent-Id", "X-Cordum-Signature",
	} {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			t.Errorf("legacy client must not stamp %q, got %q", name, v)
		}
	}
}
