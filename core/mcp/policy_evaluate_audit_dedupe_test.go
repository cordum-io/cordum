package mcp

import (
	"encoding/json"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// TestSemanticDedupeKeyForCall_DistinctNonPathArgs locks the CRITICAL #1 fix:
// two tool calls whose args carry NO path-like field (cordum_audit_query,
// cordum_query_policy, generic SQL `query=...`, etc.) but differ on the
// other arg values MUST produce DIFFERENT dedupe keys. Pre-fix,
// targetPath-only hashing collapsed every distinct arg set to a single
// 10-minute slot — call #2's arbitrary args silently served call #1's
// cached result without re-evaluating policy, calling upstream, or
// emitting a pre-event audit row.
func TestSemanticDedupeKeyForCall_DistinctNonPathArgs(t *testing.T) {
	t.Parallel()
	ctx := newAuthedToolCallCtx()
	server := "cordum"
	a := ToolCallParams{
		Name:      "cordum_query_policy",
		Arguments: json.RawMessage(`{"bundle":"safety-v1"}`),
	}
	b := ToolCallParams{
		Name:      "cordum_query_policy",
		Arguments: json.RawMessage(`{"bundle":"safety-v2"}`),
	}
	keyA := semanticDedupeKeyForCall(ctx, a, server)
	keyB := semanticDedupeKeyForCall(ctx, b, server)
	if keyA == "" || keyB == "" {
		t.Fatalf("dedupe keys must be non-empty for authed ctx: a=%q b=%q", keyA, keyB)
	}
	if keyA == keyB {
		t.Fatalf("dedupe collision on distinct non-path args: both calls hash to %q (attacker controls call #2 args and gets call #1 cached result)", keyA)
	}
}

// TestSemanticDedupeKeyForCall_SameArgsSameKey locks the inverse — two
// calls with byte-identical args MUST still collapse to one key. The fix
// for distinct-args must NOT regress the legitimate retry-idempotent path.
func TestSemanticDedupeKeyForCall_SameArgsSameKey(t *testing.T) {
	t.Parallel()
	ctx := newAuthedToolCallCtx()
	server := "cordum"
	a := ToolCallParams{
		Name:      "cordum_audit_query",
		Arguments: json.RawMessage(`{"q":"select * from events"}`),
	}
	b := ToolCallParams{
		Name:      "cordum_audit_query",
		Arguments: json.RawMessage(`{"q":"select * from events"}`),
	}
	if semanticDedupeKeyForCall(ctx, a, server) != semanticDedupeKeyForCall(ctx, b, server) {
		t.Fatal("identical-arg retries must dedupe to the same key")
	}
}

// TestSemanticDedupeKeyForCall_CanonicalArgsCollapseWhitespace asserts
// that JSON-equivalent args with different whitespace/key-order still
// produce the same key. The fix hashes the canonical form, not raw bytes.
func TestSemanticDedupeKeyForCall_CanonicalArgsCollapseWhitespace(t *testing.T) {
	t.Parallel()
	ctx := newAuthedToolCallCtx()
	server := "cordum"
	a := ToolCallParams{
		Name:      "cordum_query_policy",
		Arguments: json.RawMessage(`{"bundle":"v1","tenant":"t1"}`),
	}
	b := ToolCallParams{
		Name:      "cordum_query_policy",
		Arguments: json.RawMessage(`{ "tenant" : "t1" , "bundle" : "v1" }`),
	}
	if semanticDedupeKeyForCall(ctx, a, server) != semanticDedupeKeyForCall(ctx, b, server) {
		t.Fatal("JSON-equivalent args (reordered keys + whitespace) must produce the same key")
	}
}

// TestPolicyEvaluate_DenyInvalidatesDedupeKey locks the HIGH #2 fix: when
// EvaluateToolCall returns DENY, dedupeFinish MUST remove the key from
// the store so a subsequent call re-evaluates (instead of returning a
// stale-DENY for the full TTL after the policy bundle is updated to ALLOW).
//
// The test seeds a DENY decision, invokes once, then re-probes the dedupe
// slot via LoadOrStore: if a sentinel sentinelValue is reported as freshly
// stored (loaded=false), the prior DENY slot is gone — the delete-on-deny
// fix is wired. If loaded=true, the stale DENY is still cached.
func TestPolicyEvaluate_DenyInvalidatesDedupeKey(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "blocked"},
		fired:    true,
	}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.DedupeState = NewInProcessDedupeStore()
	deps.Upstream = &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "should not run"}}},
	}

	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{
		Name:      "cordum_query_policy",
		Arguments: json.RawMessage(`{"bundle":"v1"}`),
	}

	if _, err := InvokeToolWithPolicy(ctx, deps, params, "cordum"); err != nil {
		t.Fatalf("InvokeToolWithPolicy: %v", err)
	}

	key := semanticDedupeKeyForCall(ctx, params, "cordum")
	if key == "" {
		t.Fatal("dedupe key empty — test cannot assert delete-on-deny")
	}
	type sentinel struct{ probe bool }
	probeVal := &sentinel{probe: true}
	actual, loaded := deps.DedupeState.LoadOrStore(key, probeVal)
	if loaded {
		t.Fatalf("dedupe key NOT deleted on DENY decision — policy-bundle update will return stale DENY for the full TTL (cached value=%T)", actual)
	}
}

// TestPolicyEvaluate_DenyRecomputesAfterPolicyUpdate is the end-to-end
// counterpart: the first call DENIES, the policy state flips to ALLOW,
// the second call MUST re-evaluate (not hit the cache) and produce a
// fresh upstream invocation.
func TestPolicyEvaluate_DenyRecomputesAfterPolicyUpdate(t *testing.T) {
	t.Parallel()
	pipeline := &fakePolicyDispatcher{
		decision: PolicyDecision{Decision: pb.DecisionType_DECISION_TYPE_DENY, Reason: "blocked"},
		fired:    true,
	}
	upstream := &fakeUpstreamToolCaller{
		result: &ToolCallResult{Content: []ContentItem{{Type: "text", Text: "ok"}}},
	}
	emitter := &fakeEventEmitter{}
	deps := newToolCallDepsFixture(pipeline, emitter, &fakeArtifactStore{})
	deps.DedupeState = NewInProcessDedupeStore()
	deps.Upstream = upstream

	ctx := newAuthedToolCallCtx()
	params := ToolCallParams{
		Name:      "cordum_query_policy",
		Arguments: json.RawMessage(`{"bundle":"v1"}`),
	}

	// First call: DENY.
	if _, err := InvokeToolWithPolicy(ctx, deps, params, "cordum"); err != nil {
		t.Fatalf("first InvokeToolWithPolicy: %v", err)
	}
	if upstream.calls != 0 {
		t.Fatalf("upstream invoked on DENY: count=%d (want 0)", upstream.calls)
	}

	// Operator updates the policy bundle to ALLOW the same tuple.
	pipeline.decision = PolicyDecision{Decision: pb.DecisionType_DECISION_TYPE_ALLOW}

	// Second call: must re-evaluate, not return cached DENY.
	if _, err := InvokeToolWithPolicy(ctx, deps, params, "cordum"); err != nil {
		t.Fatalf("second InvokeToolWithPolicy: %v", err)
	}
	if upstream.calls != 1 {
		t.Fatalf("upstream NOT invoked after policy flip to ALLOW: count=%d (want 1) — stale DENY served from dedupe cache", upstream.calls)
	}
}
