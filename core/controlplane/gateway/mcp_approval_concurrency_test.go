package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/store"
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
// alone: MULTI/EXEC must never block. cmd names another read to park after,
// such as the index SMEMBERS an enqueue starts with; empty means GET.
type consumeReadBarrier struct {
	cmd      string
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
		args, want := cmd.Args(), b.cmd
		if want == "" {
			want = "get"
		}
		if cmd.Name() != want || len(args) != 2 || args[1] != b.key {
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

// TestEnqueueMCPApprovalConcurrentDuplicatesShareOnePending: concurrent
// duplicates of one call, such as a redelivered CAP job racing the original,
// all get one PENDING record. The barrier lets every enqueuer read the index
// before any can write, so without the WATCH each would add its own record.
func TestEnqueueMCPApprovalConcurrentDuplicatesShareOnePending(t *testing.T) {
	t.Parallel()
	seed := newTestMCPStore(t)
	ctx := context.Background()
	req := testReq()
	idxKey := mcpApprovalIndexKey(req.Tenant, req.AgentID, req.ToolName, req.ArgsHash)

	const workers = 8
	base, ok := seed.client.(*redis.Client)
	if !ok {
		t.Fatalf("test store client is %T; want *redis.Client", seed.client)
	}
	opts := testRedisOptions(base.Options().Addr)
	opts.PoolSize = workers
	wide := redis.NewClient(opts)
	t.Cleanup(func() { _ = wide.Close() })
	barrier := &consumeReadBarrier{cmd: "smembers", key: idxKey, parties: workers, release: make(chan struct{})}
	wide.AddHook(barrier)
	enqueuers := NewMCPApprovalStore(wide)

	ids := make([]string, workers)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			dup := *req
			rec, err := enqueuers.EnqueueMCPApproval(ctx, &dup)
			if err != nil {
				t.Errorf("enqueue %d: %v", i, err)
				return
			}
			ids[i] = rec.ID
		}(i)
	}
	wg.Wait()
	if n := barrier.timedOut.Load(); n != 0 {
		t.Errorf("barrier timed out %d times; want every enqueuer to read the index before any writes", n)
	}
	members, err := seed.client.SMembers(ctx, idxKey).Result()
	if err != nil || len(members) != 1 {
		t.Fatalf("index members = %v (err %v); want exactly one PENDING record", members, err)
	}
	for i, id := range ids {
		if id != members[0] {
			t.Errorf("enqueuer %d got %q; want the shared record %q", i, id, members[0])
		}
	}
}

// TestEnqueueMCPApprovalReusesOnlyTheCallersLivePendingRecord: a pending record
// goes back only to a non-Edge repeat from the same requester before the
// record's deadline; every other request writes a record of its own.
func TestEnqueueMCPApprovalReusesOnlyTheCallersLivePendingRecord(t *testing.T) {
	t.Parallel()
	edgePaired := func(r *MCPApprovalRequest) { r.ID, r.EdgeApprovalRef = "paired-record", "edge-ref" }
	for _, tc := range []struct {
		name         string
		first, again func(*MCPApprovalRequest)
		reuse        bool
	}{
		{"same requester", nil, nil, true},
		{"another requester", nil, func(r *MCPApprovalRequest) { r.Requester = "someone-else" }, false},
		{"Edge-paired repeat", nil, edgePaired, false},
		{"repeat of an Edge-paired record", edgePaired, nil, false},
		{"past the deadline", func(r *MCPApprovalRequest) { r.TTL = time.Millisecond }, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := newTestMCPStore(t), context.Background()
			first, again := testReq(), testReq()
			if tc.first != nil {
				tc.first(first)
			}
			if tc.again != nil {
				tc.again(again)
			}
			a, err := s.EnqueueMCPApproval(ctx, first)
			if err != nil {
				t.Fatalf("first enqueue: %v", err)
			}
			time.Sleep(2 * time.Millisecond) // past a 1ms deadline
			b, err := s.EnqueueMCPApproval(ctx, again)
			if err != nil {
				t.Fatalf("second enqueue: %v", err)
			}
			if (a.ID == b.ID) != tc.reuse {
				t.Errorf("second request got %s after %s; want reuse=%v", b.ID, a.ID, tc.reuse)
			}
		})
	}
}

// cwKeyArgs carries an idempotency_key, as a CAP worker's job does.
const cwKeyArgs = `{"name":"mcp-demo","steps":{"s1":{"type":"approval"}},"idempotency_key":"cap-job-42"}`

// TestMCPCreateWorkflow_DuplicateDeliveryWritesOneWorkflow: a redelivered job
// repeats the call. While the approval is pending the repeat gets it back,
// and once each delivery is approved both write the same workflow.
func TestMCPCreateWorkflow_DuplicateDeliveryWritesOneWorkflow(t *testing.T) {
	h := newCreateWorkflowMCPHarness(t)
	first := h.requireApproval(h.call(cwAlice, cwKeyArgs))
	again := h.call(cwAlice, cwKeyArgs)
	if id := h.requireApproval(again); id != first || again.Error.Data["approval_ref"] != first ||
		again.Error.Data["retry_hint"] != mcp.RetryHintSameArgs {
		t.Fatalf("duplicate got %+v; want pending approval %s with retry_with_same_args", again.Error.Data, first)
	}
	if status := h.approve(cwBob, first); status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", status)
	}
	created := h.toolPayload(h.call(cwAlice, cwKeyArgs), false)
	// The duplicate's own retry: that approval is spent, so it re-gates.
	second := h.requireApproval(h.call(cwAlice, cwKeyArgs))
	if status := h.approve(cwBob, second); status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", status)
	}
	repeat := h.toolPayload(h.call(cwAlice, cwKeyArgs), false)
	if repeat["workflow_id"] != created["workflow_id"] {
		t.Fatalf("repeat wrote workflow %v; want %v", repeat["workflow_id"], created["workflow_id"])
	}
	if n := h.workflowCount(""); n != 1 {
		t.Fatalf("workflows = %d, want 1", n)
	}
}

// TestMCPCreateWorkflow_PreapprovedDuplicateDeliveryWritesOneWorkflow: an agent
// preapproved for the tool runs every delivery, and deliveries with one key
// write one workflow; another key writes another.
func TestMCPCreateWorkflow_PreapprovedDuplicateDeliveryWritesOneWorkflow(t *testing.T) {
	h := newCreateWorkflowMCPHarness(t)
	if _, err := h.s.agentIdentityStore.Update(context.Background(), "default", "agent-alice",
		store.AgentIdentity{PreapprovedMutatingTools: []string{mcp.ToolCreateWorkflow}}); err != nil {
		t.Fatalf("preapprove: %v", err)
	}
	a := h.toolPayload(h.call(cwAlice, cwKeyArgs), false)
	b := h.toolPayload(h.call(cwAlice, cwKeyArgs), false)
	if n := h.workflowCount(""); a["workflow_id"] != b["workflow_id"] || n != 1 {
		t.Fatalf("deliveries wrote %v and %v (%d workflows); want one workflow", a["workflow_id"], b["workflow_id"], n)
	}
	c := h.toolPayload(h.call(cwAlice, strings.Replace(cwKeyArgs, "cap-job-42", "cap-job-43", 1)), false)
	if n := h.workflowCount(""); c["workflow_id"] == a["workflow_id"] || n != 2 {
		t.Fatalf("another key wrote %v (%d workflows); want a second workflow", c["workflow_id"], n)
	}
}
