package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
)

// TestGateRefusesWithoutCallMetadata confirms the middleware contract:
// missing tenant/agent in ctx is a hard error, not a silent pass.
func TestGateRefusesWithoutCallMetadata(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	// RequiresApproval=true — gate short-circuits harmlessly for
	// non-gated tools (task-2d989055), so the missing-metadata error
	// only fires when approval is actually required.
	_, err := gate.Check(context.Background(), mcp.Tool{Name: "x", RequiresApproval: true}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrMissingMCPCallMeta) {
		t.Errorf("want ErrMissingMCPCallMeta, got %v", err)
	}
}

// TestGateEnqueuesOnFirstCall confirms a fresh tool call with no
// pre-approval produces an ApprovalRequired with a non-empty approval
// ID and the tool name populated.
func TestGateEnqueuesOnFirstCall(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t1", AgentID: "a1"})
	got, err := gate.Check(ctx, mcp.Tool{Name: "files.delete", RequiresApproval: true}, json.RawMessage(`{"path":"/"}`))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if got == nil {
		t.Fatal("want ApprovalRequired, got nil")
	}
	if got.ApprovalID == "" {
		t.Error("approval id empty")
	}
	if got.Tool != "files.delete" {
		t.Errorf("tool = %q", got.Tool)
	}
}

// TestGateConsumeOnceApproveThenCallTwice is the canonical step-5 test:
// approve once out-of-band, call the gate twice — the first call clears,
// the second call re-enqueues.
func TestGateConsumeOnceApproveThenCallTwice(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t1", AgentID: "a1"})

	tool := mcp.Tool{Name: "db.drop", RequiresApproval: true}
	args := json.RawMessage(`{"table":"users"}`)

	// First call: gate enqueues.
	first, err := gate.Check(ctx, tool, args)
	if err != nil {
		t.Fatalf("first check: %v", err)
	}
	if first == nil {
		t.Fatal("expected ApprovalRequired on first call")
	}

	// Out-of-band approve.
	if _, err := store.Resolve(ctx, first.ApprovalID, model.ApprovalDecisionApprove, "admin", ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Second gate check (with identical args): pre-approval hit, gate
	// returns (nil, nil) and marks consumed.
	second, err := gate.Check(ctx, tool, args)
	if err != nil {
		t.Fatalf("second check: %v", err)
	}
	if second != nil {
		t.Fatalf("second call should succeed; got ApprovalRequired %+v", second)
	}

	// Third gate check (identical args again): consume-once means a
	// fresh approval is enqueued. The new ID must differ from the first.
	third, err := gate.Check(ctx, tool, args)
	if err != nil {
		t.Fatalf("third check: %v", err)
	}
	if third == nil {
		t.Fatal("third call should re-enqueue; got nil (unapproved second consume?)")
	}
	if third.ApprovalID == first.ApprovalID {
		t.Errorf("third approval id matches first — consume-once not enforced")
	}
}

// TestGateDifferentArgsReEnqueue verifies that changing args after an
// approval produces a brand-new approval. Two calls with the same tool
// but different args must NOT share an approval.
func TestGateDifferentArgsReEnqueue(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t1", AgentID: "a1"})
	tool := mcp.Tool{Name: "db.drop", RequiresApproval: true}

	a1, err := gate.Check(ctx, tool, json.RawMessage(`{"table":"users"}`))
	if err != nil || a1 == nil {
		t.Fatalf("first: %v / %v", err, a1)
	}
	_, _ = store.Resolve(ctx, a1.ApprovalID, model.ApprovalDecisionApprove, "admin", "")

	// Same tool, DIFFERENT args: pre-approval lookup must miss, gate must
	// enqueue a fresh approval.
	a2, err := gate.Check(ctx, tool, json.RawMessage(`{"table":"orders"}`))
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a2 == nil {
		t.Fatal("different args should re-gate; got no ApprovalRequired")
	}
	if a2.ApprovalID == a1.ApprovalID {
		t.Error("different args produced same approval id — args_hash collision")
	}
}

// TestCanonicalArgsHashKeyOrderingIndependent ensures two calls whose
// args differ only in JSON key order hash identically. Without this an
// approver would pointlessly re-approve the same call.
func TestCanonicalArgsHashKeyOrderingIndependent(t *testing.T) {
	t.Parallel()
	a, err := canonicalArgsHash(json.RawMessage(`{"a":1,"b":"x","c":[1,2]}`))
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := canonicalArgsHash(json.RawMessage(`{"c":[1,2],"b":"x","a":1}`))
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a != b {
		t.Errorf("key-reordered JSON hashed differently: %q vs %q", a, b)
	}

	// Whitespace-insensitive too — the normaliser decodes then re-encodes.
	c, err := canonicalArgsHash(json.RawMessage(`  {"a":1, "b":"x",  "c":[1, 2]} `))
	if err != nil {
		t.Fatalf("c: %v", err)
	}
	if c != a {
		t.Errorf("whitespace-padded JSON hashed differently: %q vs %q", c, a)
	}
}

// TestCanonicalArgsHashEmptyAndNil treats empty/null payloads as the
// same canonical form. Prevents a drift where a tool call with no args
// re-gates against its own previous run.
func TestCanonicalArgsHashEmptyAndNil(t *testing.T) {
	t.Parallel()
	empty, err := canonicalArgsHash(nil)
	if err != nil {
		t.Fatalf("nil: %v", err)
	}
	brace, err := canonicalArgsHash(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("{}: %v", err)
	}
	if empty != brace {
		t.Errorf("nil and empty object hashed differently: %q vs %q", empty, brace)
	}
}

// TestGateStoresPrincipalAsRequester regression-tests the self-approval
// security fix. When middleware populates MCPCallMetadata.Principal,
// the enqueued record's Requester field must be the principal (not the
// agent_id). This lets the handler's self-approval guard compare the
// approver's composite identity against a principal it actually shares
// a namespace with.
func TestGateStoresPrincipalAsRequester(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:    "t1",
		AgentID:   "agent-alpha",
		Principal: "alice@corp",
	})
	got, err := gate.Check(ctx, mcp.Tool{Name: "db.drop", RequiresApproval: true}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired")
	}
	rec, err := store.Get(ctx, got.ApprovalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if rec.Requester != "alice@corp" {
		t.Errorf("Requester = %q; want the authenticated principal alice@corp so the self-approval guard works", rec.Requester)
	}
	if rec.AgentID != "agent-alpha" {
		t.Errorf("AgentID = %q; want agent-alpha (display value preserved)", rec.AgentID)
	}
}

// TestGateFallsBackToAgentIDWhenPrincipalMissing documents the
// backward-compat path for non-HTTP transports (stdio/dev) where
// middleware does not set Principal.
func TestGateFallsBackToAgentIDWhenPrincipalMissing(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:  "t1",
		AgentID: "agent-alpha",
	})
	got, _ := gate.Check(ctx, mcp.Tool{Name: "db.drop", RequiresApproval: true}, json.RawMessage(`{}`))
	rec, _ := store.Get(ctx, got.ApprovalID)
	if rec.Requester != "agent-alpha" {
		t.Errorf("Requester = %q; want agent-alpha fallback", rec.Requester)
	}
}

// TestGate_ApprovalRequiredCarriesResumeMetadata is the EDGE-103 reopen #1
// regression: the gate's ApprovalRequired payload MUST carry the metadata
// a client needs to resume — approval_ref, args_hash, expires_at, and
// a machine-readable retry_hint. Without these, the JSON-RPC -32099
// envelope would not document how the caller is supposed to retry, and
// the new `_approval_ref` consume path could not be exercised by any
// off-the-shelf MCP client.
//
// approval_id stays populated for backward-compatibility with existing
// SIEM correlation; approval_ref is the EDGE-103 handle (legacy ID when
// the gate's downstream is MCPApprovalStore, Edge ref when the gate is
// wired to an EdgeApprovalMinter).
func TestGate_ApprovalRequiredCarriesResumeMetadata(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{
		Tenant:    "tnt_a",
		AgentID:   "agent_alpha",
		Principal: "alice@corp",
	})
	got, err := gate.Check(ctx,
		mcp.Tool{Name: "fs.write", RequiresApproval: true},
		json.RawMessage(`{"path":"/etc/hostname"}`))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if got == nil {
		t.Fatal("expected ApprovalRequired")
	}
	if got.ApprovalID == "" {
		t.Error("ApprovalID empty (legacy correlation handle)")
	}
	if got.ApprovalRef == "" {
		t.Error("ApprovalRef empty; EDGE-103 resume protocol cannot bind to ApprovalRef=\"\"")
	}
	if got.ArgsHash == "" {
		t.Error("ArgsHash empty; DoD #2 (args hash match on resume) cannot be checked without it")
	}
	if got.RetryHint == "" {
		t.Error("RetryHint empty; clients have no machine-readable signal to retry with _approval_ref")
	}
	if got.ExpiresAt.IsZero() {
		t.Error("ExpiresAt zero; DoD #3 (bounded timeout) requires the client to see the hold window")
	}
}
