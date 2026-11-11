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
	"github.com/redis/go-redis/v9"
)

// consumeReadBarrier is a test-only go-redis hook that makes N claimers
// overlap inside consumeRecord's WATCH window. It runs the record-key GET
// first, then parks the first parties-1 arrivals until the last one arrives, so
// every claimer has read the APPROVED, unconsumed record (and, unmutated,
// holds its WATCH) before any of them can reach MULTI/EXEC. Later arrivals are
// CAS-retry re-reads and pass straight through. The pipeline hook is left
// alone: MULTI/EXEC must never block.
type consumeReadBarrier struct {
	key      string
	parties  int64
	arrived  atomic.Int64
	timedOut atomic.Int64
	release  chan struct{}
}

func (b *consumeReadBarrier) DialHook(next redis.DialHook) redis.DialHook { return next }

func (b *consumeReadBarrier) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (b *consumeReadBarrier) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd) // run the GET first, then wait
		args := cmd.Args()
		if cmd.Name() != "get" || len(args) != 2 || args[1] != b.key {
			return err
		}
		switch n := b.arrived.Add(1); {
		case n == b.parties:
			close(b.release)
		case n < b.parties:
			select {
			case <-b.release:
			case <-time.After(10 * time.Second):
				b.timedOut.Add(1)
			}
		}
		return err
	}
}

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

	// The claimers run on their own PoolSize=workers client to the SAME
	// miniredis. The default test client has PoolSize 1, and a go-redis Tx
	// holds its connection from WATCH through EXEC, so on it every claim was
	// serialized and removing WATCH from consumeRecord went undetected. With a
	// wide pool plus consumeReadBarrier, every claimer reads the record before
	// any can write, so the overlap is deterministic. Unmutated, the 15 losers
	// fail EXEC with TxFailedErr, retry (mcpCASMaxAttempts = 5) and see
	// ConsumedAt != 0, returning (nil, nil). Seeding and the final Get stay on
	// the PoolSize-1 store so they never consume barrier slots.
	const workers = 16
	base, ok := store.client.(*redis.Client)
	if !ok {
		t.Fatalf("test store client is %T; want *redis.Client", store.client)
	}
	opts := testRedisOptions(base.Options().Addr)
	opts.PoolSize = workers
	wide := redis.NewClient(opts)
	t.Cleanup(func() { _ = wide.Close() })
	barrier := &consumeReadBarrier{key: mcpApprovalKey(rec.ID), parties: workers, release: make(chan struct{})}
	wide.AddHook(barrier)
	claimers := NewMCPApprovalStore(wide)

	var (
		wg       sync.WaitGroup
		winners  atomic.Int64
		hardErrs atomic.Int64
	)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			claimed, err := claimers.ClaimPreApproved(ctx, req.Tenant, req.AgentID, req.ToolName, req.ArgsHash)
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

	if barrier.timedOut.Load() != 0 || barrier.arrived.Load() < workers {
		t.Errorf("barrier: arrived=%d timedOut=%d; want all %d claimers to read before any writes",
			barrier.arrived.Load(), barrier.timedOut.Load(), workers)
	}
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
