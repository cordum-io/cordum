package gateway

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/model"
)

// TestClaimPreApprovedIsAtomicUnderConcurrentClaimers fires N concurrent
// ClaimPreApproved calls against a single APPROVED record. Exactly ONE
// goroutine must observe a non-nil record; every other caller must see
// nil, the "lost the consume race — enqueue a fresh approval" signal.
//
// This is the hardest invariant to guarantee in the store: two
// independent MCP clients submitting the same args in the same
// millisecond after an approval was granted must not both proceed with
// a single approval's authority. Without this test the previous
// implementation (FindPreApproved + MarkConsumed as separate Redis
// round-trips) silently double-consumed.
func TestClaimPreApprovedIsAtomicUnderConcurrentClaimers(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	ctx := context.Background()

	// Seed a single APPROVED unconsumed record.
	req := testReq()
	rec, err := store.EnqueueMCPApproval(ctx, req)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := store.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// miniredis does not guarantee the same concurrency semantics as
	// real Redis, but it honours WATCH sufficiently for the atomicity
	// check here: a record observed under WATCH and then modified by
	// another goroutine causes the second TxPipelined EXEC to return
	// redis.TxFailedErr, which our CAS loop retries — on retry the
	// loser sees ConsumedAt != 0 and returns (nil, nil).
	const workers = 16
	var (
		wg       sync.WaitGroup
		winners  atomic.Int64
		hardErrs atomic.Int64
	)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			claimed, err := store.ClaimPreApproved(ctx, req.Tenant, req.AgentID, req.ToolName, req.ArgsHash)
			if err != nil {
				hardErrs.Add(1)
				return
			}
			if claimed != nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if hardErrs.Load() != 0 {
		t.Errorf("hard errors during concurrent claim: %d (want 0)", hardErrs.Load())
	}
	if winners.Load() != 1 {
		t.Errorf("concurrent claimers produced %d winners; want exactly 1", winners.Load())
	}

	// Post-state sanity: ConsumedAt must be set.
	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ConsumedAt == 0 {
		t.Error("post-race record is not marked consumed")
	}
}

// TestResolveAndSweepCannotOverlap exercises the Resolve/Sweep race:
// a record whose ExpiresAt has already passed is concurrently resolved
// by an admin and swept by the reaper. The record must land in exactly
// one terminal state — a last-writer-wins bug would stamp EXPIRED on
// top of APPROVED and erase the admin's decision.
func TestResolveAndSweepCannotOverlap(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	ctx := context.Background()

	// Enqueue a record and manually force its ExpiresAt into the past
	// so SweepExpired treats it as eligible. We do it via a second
	// enqueue → Resolve transition path since miniredis does not
	// support arbitrary clock injection.
	rec, err := store.EnqueueMCPApproval(ctx, testReq())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Race: one goroutine resolves, the other sweeps. miniredis is
	// single-threaded under the hood but WATCH still creates the CAS
	// semantics required: whichever transaction commits first causes
	// the other's EXEC to fail and retry, observing the new status.
	var wg sync.WaitGroup
	wg.Add(2)

	var (
		resolveErr error
		sweepN     int
	)
	go func() {
		defer wg.Done()
		_, e := store.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin", "looks good")
		resolveErr = e
	}()
	go func() {
		defer wg.Done()
		// Sweep using a deadline set to the record's expiry + 1 micro so
		// it would otherwise flip to EXPIRED.
		n, _ := store.SweepExpired(ctx, time.UnixMicro(rec.ExpiresAt+1))
		sweepN = n
	}()
	wg.Wait()

	got, err := store.Get(ctx, rec.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	// Whichever goroutine won, the terminal status is exclusively one
	// of approved/expired. The decision must match the status — any
	// mismatch indicates the Resolve's write was clobbered by Sweep
	// (or vice versa).
	switch got.Status {
	case model.ApprovalStatusApproved:
		if resolveErr != nil {
			t.Errorf("record is APPROVED but resolve returned error: %v", resolveErr)
		}
		if got.Decision != model.ApprovalDecisionApprove {
			t.Errorf("status=approved but decision=%q", got.Decision)
		}
	case model.ApprovalStatusExpired:
		if sweepN == 0 && resolveErr == nil {
			t.Errorf("record is EXPIRED but neither sweep nor resolve declared ownership (sweepN=%d, resolveErr=%v)", sweepN, resolveErr)
		}
		if got.Decision != model.ApprovalDecisionExpire {
			t.Errorf("status=expired but decision=%q", got.Decision)
		}
	default:
		t.Errorf("unexpected terminal status %q", got.Status)
	}
}

// TestCanonicalArgsHashPreservesBigIntegers regression-tests the
// json.Decoder.UseNumber() fix. Two semantically different integer
// arguments that both exceed 2^53 must hash differently. The previous
// implementation decoded numbers through float64, silently rounding
// these into a collision.
func TestCanonicalArgsHashPreservesBigIntegers(t *testing.T) {
	t.Parallel()
	// 9007199254740993 = 2^53 + 1 — the smallest integer that cannot
	// be represented exactly in float64.
	a, err := canonicalArgsHash(json.RawMessage(`{"n":9007199254740993}`))
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := canonicalArgsHash(json.RawMessage(`{"n":9007199254740992}`))
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a == b {
		t.Fatalf("big-int args collided on hash: %q — UseNumber regression", a)
	}

	// And for completeness: a monstrous int64 at the high end of the
	// range still round-trips to a different hash than its neighbour.
	c, err := canonicalArgsHash(json.RawMessage(`{"n":9223372036854775807}`))
	if err != nil {
		t.Fatalf("c: %v", err)
	}
	d, err := canonicalArgsHash(json.RawMessage(`{"n":9223372036854775806}`))
	if err != nil {
		t.Fatalf("d: %v", err)
	}
	if c == d {
		t.Error("adjacent max-int64 values collided on hash")
	}
}

// TestMCPApprovalPersistsArgsPayload asserts the canonical args payload
// lands on the record. Before this fix the approver saw only a SHA-256
// hash in the dashboard modal — compliance theatre, not review.
func TestMCPApprovalPersistsArgsPayload(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	gate := NewGatewayApprovalGate(store)
	ctx := WithMCPCallMetadata(context.Background(), MCPCallMetadata{Tenant: "t", AgentID: "a", Principal: "alice"})
	params := json.RawMessage(`{"table":"users","confirm":true}`)

	got, err := gate.Check(ctx, mcp.Tool{Name: "db.drop", RequiresApproval: true}, params)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	rec, err := store.Get(ctx, got.ApprovalID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(rec.ArgsJSON) == 0 {
		t.Fatal("record persisted no args payload — approver would see only a hash")
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.ArgsJSON, &payload); err != nil {
		t.Fatalf("args JSON not valid: %v — %s", err, rec.ArgsJSON)
	}
	if payload["table"] != "users" {
		t.Errorf("args.table = %v, want 'users'", payload["table"])
	}
	// Principal must also be persisted so the audit trail captures who
	// actually made the call (the authenticated principal) separately
	// from the display-facing agent ID.
	if rec.Principal != "alice" {
		t.Errorf("Principal = %q, want 'alice'", rec.Principal)
	}
}

// TestResolveDoesNotOverwriteTriggerReason pins the split-reason fix:
// the approver's free-form comment MUST land in ResolutionReason, not
// on top of the Reason that was set at enqueue time. Auditors reading
// the final record need to know WHY the call was gated, not just what
// the resolver happened to type.
func TestResolveDoesNotOverwriteTriggerReason(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	ctx := context.Background()

	req := testReq()
	req.Reason = "tool 'db.drop' matches approval scope 'destructive'"
	rec, err := store.EnqueueMCPApproval(ctx, req)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	resolved, err := store.Resolve(ctx, rec.ID, model.ApprovalDecisionApprove, "admin-1", "lgtm — reviewed with SRE oncall")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Reason != req.Reason {
		t.Errorf("trigger reason clobbered: got %q, want %q", resolved.Reason, req.Reason)
	}
	if resolved.ResolutionReason != "lgtm — reviewed with SRE oncall" {
		t.Errorf("resolution reason not recorded: got %q", resolved.ResolutionReason)
	}
}

// TestIndexPrunesTerminalEntries exercises the index-hygiene contract
// (consumed APPROVED, REJECTED, EXPIRED records must leave the per-
// tuple index). Without pruning, repeat-offender tool calls blow the
// index up over time and ClaimPreApproved degrades to O(N) GETs.
func TestIndexPrunesTerminalEntries(t *testing.T) {
	t.Parallel()
	store := newTestMCPStore(t)
	ctx := context.Background()

	req := testReq()
	// Reject path — index entry should be gone post-resolve.
	a, _ := store.EnqueueMCPApproval(ctx, req)
	if _, err := store.Resolve(ctx, a.ID, model.ApprovalDecisionReject, "admin", "too risky"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	idxKey := mcpApprovalIndexKey(req.Tenant, req.AgentID, req.ToolName, req.ArgsHash)
	members, err := store.client.SMembers(ctx, idxKey).Result()
	if err != nil {
		t.Fatalf("smembers: %v", err)
	}
	for _, m := range members {
		if m == a.ID {
			t.Errorf("rejected record %s still present in index %s", a.ID, idxKey)
		}
	}

	// Consume path — claim should prune the winner too.
	b, _ := store.EnqueueMCPApproval(ctx, req)
	if _, err := store.Resolve(ctx, b.ID, model.ApprovalDecisionApprove, "admin", ""); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := store.ClaimPreApproved(ctx, req.Tenant, req.AgentID, req.ToolName, req.ArgsHash); err != nil {
		t.Fatalf("claim: %v", err)
	}
	members2, _ := store.client.SMembers(ctx, idxKey).Result()
	for _, m := range members2 {
		if m == b.ID {
			t.Errorf("consumed record %s still present in index %s", b.ID, idxKey)
		}
	}
}
