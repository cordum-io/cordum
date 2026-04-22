package scheduler

// Dispatch-gate test matrix. Every case drives the real
// SessionTokenIssuer / TrustResolver / MemoryRegistry — no mocks.
//
// The table matches the DoD verbatim:
//
//   session: {valid, invalid, revoked, expired}
//   heartbeat: {fresh, stale, absent}
//
// For each cell we assert whether a worker ends up in EligibleWorkers
// and whether the per-worker IsWorkerEligible agrees.

import (
	"context"
	"fmt"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

type sessionCondition int

const (
	sessionValid sessionCondition = iota
	sessionNone                   // never issued
	sessionRevoked
	sessionExpired
)

type heartbeatCondition int

const (
	heartbeatFresh heartbeatCondition = iota
	heartbeatStale
	heartbeatAbsent
)

// prepareWorker applies the desired session + heartbeat condition to
// the scheduler state and returns the worker ID under test.
func prepareWorker(t *testing.T, ctx context.Context, issuer *SessionTokenIssuer, clk *fakeClock, reg *MemoryRegistry, id string, sess sessionCondition, hb heartbeatCondition) {
	t.Helper()

	switch sess {
	case sessionValid:
		if _, _, err := issuer.Issue(ctx, id, "tenant-matrix", "v1"); err != nil {
			t.Fatalf("issue %s: %v", id, err)
		}
	case sessionNone:
		// no-op
	case sessionRevoked:
		_, claims, err := issuer.Issue(ctx, id, "tenant-matrix", "v1")
		if err != nil {
			t.Fatalf("issue %s: %v", id, err)
		}
		if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
			t.Fatalf("revoke %s: %v", id, err)
		}
	case sessionExpired:
		if _, _, err := issuer.Issue(ctx, id, "tenant-matrix", "v1"); err != nil {
			t.Fatalf("issue %s: %v", id, err)
		}
		// Fast-forward past the token lifetime so the resolver sees
		// the exp check fail. Also note: miniredis honours TTL so the
		// per-agent record may be swept — either Reason=Expired or
		// Reason=NoSession is a "not alive" outcome.
		clk.Advance(2 * issuer.Lifetime())
	default:
		t.Fatalf("unknown session condition: %d", sess)
	}

	switch hb {
	case heartbeatFresh, heartbeatStale:
		reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: id, Pool: "matrix"})
	case heartbeatAbsent:
		// no-op; registry has no entry for the worker
	default:
		t.Fatalf("unknown heartbeat condition: %d", hb)
	}
}

// staleAllHeartbeats pushes every entry in the registry past the TTL
// horizon without deleting it. We can't just time.Sleep past the TTL
// because the registry's expireLoop would GC the workers. Instead we
// grab the lock directly and shift their lastSeen timestamps back.
// This mirrors real-world stale heartbeats (clock skew, lost packet)
// while keeping the records around so SnapshotAll can still see them.
func staleAllHeartbeats(reg *MemoryRegistry) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	cutoff := time.Now().Add(-2 * reg.ttl)
	for _, entry := range reg.workers {
		entry.lastSeen = cutoff
	}
}

func TestDispatchGate_SessionAuthorityMatrix(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)

	type cell struct {
		name      string
		session   sessionCondition
		heartbeat heartbeatCondition
		eligible  bool
	}
	cells := []cell{
		// Fresh session ⇒ dispatch, regardless of heartbeat state.
		{"valid_fresh_hb", sessionValid, heartbeatFresh, true},
		{"valid_stale_hb", sessionValid, heartbeatStale, true},
		// Absent-heartbeat + valid session: the worker never reported
		// a pool to route to, so it cannot be in the registry snapshot.
		{"valid_absent_hb", sessionValid, heartbeatAbsent, false},
		// Any non-valid session ⇒ no dispatch, regardless of heartbeat.
		{"no_session_fresh_hb", sessionNone, heartbeatFresh, false},
		{"no_session_stale_hb", sessionNone, heartbeatStale, false},
		{"no_session_absent_hb", sessionNone, heartbeatAbsent, false},
		{"revoked_fresh_hb", sessionRevoked, heartbeatFresh, false},
		{"revoked_stale_hb", sessionRevoked, heartbeatStale, false},
		{"revoked_absent_hb", sessionRevoked, heartbeatAbsent, false},
		{"expired_fresh_hb", sessionExpired, heartbeatFresh, false},
		{"expired_stale_hb", sessionExpired, heartbeatStale, false},
		{"expired_absent_hb", sessionExpired, heartbeatAbsent, false},
	}

	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			localClk := &fakeClock{now: now}
			localIssuer, _, localRdb, localCleanup := newTestIssuer(t, SessionTokenIssuerOptions{
				Lifetime: 1 * time.Hour,
				Now:      localClk.Now,
			})
			defer localCleanup()
			localResolver := NewTrustResolver(localRdb).WithClock(localClk.Now)
			reg := NewMemoryRegistry()
			defer reg.Close()

			id := "worker-" + c.name
			prepareWorker(t, context.Background(), localIssuer, localClk, reg, id, c.session, c.heartbeat)
			if c.heartbeat == heartbeatStale {
				staleAllHeartbeats(reg)
			}

			gate := NewDispatchGate(localResolver, HeartbeatModeTelemetry)
			eligible, _ := gate.EligibleWorkers(context.Background(), reg)
			_, got := eligible[id]
			if got != c.eligible {
				t.Fatalf("EligibleWorkers[%s] got=%v want=%v (session=%d heartbeat=%d)", id, got, c.eligible, c.session, c.heartbeat)
			}
			// IsWorkerEligible must match — absent-heartbeat is a pure
			// registry absence, not a gate decision, so we only assert
			// IsWorkerEligible when the registry knows about the worker.
			if c.heartbeat != heartbeatAbsent {
				alive, _ := gate.IsWorkerEligible(context.Background(), id, reg.IsAlive)
				if alive != c.eligible {
					t.Fatalf("IsWorkerEligible[%s] got=%v want=%v", id, alive, c.eligible)
				}
			}
		})
	}
}

func TestDispatchGate_AuthorityModePassThrough(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	_, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{Now: clk.Now})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "alpha", Pool: "x"})

	gate := NewDispatchGate(resolver, HeartbeatModeAuthority)
	if gate.EnforcesSession() {
		t.Fatal("authority mode must not enforce session")
	}
	out, dis := gate.EligibleWorkers(context.Background(), reg)
	if _, ok := out["alpha"]; !ok {
		t.Fatalf("authority mode must pass-through; got %+v", out)
	}
	if len(dis) != 0 {
		t.Fatalf("authority mode must not emit disagreements, got %d", len(dis))
	}
	alive, d := gate.IsWorkerEligible(context.Background(), "alpha", reg.IsAlive)
	if !alive {
		t.Fatal("authority mode must consult legacy IsAlive, which returns true for fresh hb")
	}
	if d != nil {
		t.Fatalf("authority mode must not emit disagreement, got %+v", d)
	}
}

func TestDispatchGate_WarnEmitsDisagreements(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 1 * time.Hour,
		Now:      clk.Now,
	})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	ctx := context.Background()

	// Case A: valid session, stale heartbeat (session allows, heartbeat
	// would have blocked).
	if _, _, err := issuer.Issue(ctx, "worker-A", "tenant-warn", "v1"); err != nil {
		t.Fatalf("issue A: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-A", Pool: "x"})

	// Case B: revoked session, fresh heartbeat (session blocks, heartbeat
	// would have allowed).
	_, claimsB, err := issuer.Issue(ctx, "worker-B", "tenant-warn", "v1")
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}
	if err := issuer.Revoke(ctx, claimsB.Tenant, claimsB.JTI, claimsB.ExpiresAt); err != nil {
		t.Fatalf("revoke B: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-B", Pool: "x"})

	// Push worker-A's heartbeat into stale territory.
	staleAllHeartbeats(reg)
	// Restore worker-B to fresh by overwriting its heartbeat timestamp.
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "worker-B", Pool: "x"})

	gate := NewDispatchGate(resolver, HeartbeatModeWarn)
	out, dis := gate.EligibleWorkers(ctx, reg)
	if _, ok := out["worker-A"]; !ok {
		t.Fatalf("worker-A (valid session, stale hb) must be eligible in warn mode; got %+v", out)
	}
	if _, ok := out["worker-B"]; ok {
		t.Fatalf("worker-B (revoked session) must not be eligible; got %+v", out)
	}

	foundAllowsBlocks := false
	foundBlocksAllows := false
	for _, d := range dis {
		switch d.WorkerID {
		case "worker-A":
			if d.Direction != "session_allows_heartbeat_blocks" {
				t.Fatalf("worker-A direction=%q want session_allows_heartbeat_blocks", d.Direction)
			}
			foundAllowsBlocks = true
		case "worker-B":
			if d.Direction != "session_blocks_heartbeat_allows" {
				t.Fatalf("worker-B direction=%q want session_blocks_heartbeat_allows", d.Direction)
			}
			foundBlocksAllows = true
		}
	}
	if !foundAllowsBlocks || !foundBlocksAllows {
		t.Fatalf("missing disagreement events: allows_blocks=%v blocks_allows=%v (got %d total)", foundAllowsBlocks, foundBlocksAllows, len(dis))
	}
}

func TestDispatchGate_NilResolverFallsBack(t *testing.T) {
	t.Parallel()
	reg := NewMemoryRegistry()
	defer reg.Close()
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "alpha", Pool: "x"})

	gate := NewDispatchGate(nil, HeartbeatModeTelemetry)
	if gate.EnforcesSession() {
		t.Fatal("nil resolver must not enforce session")
	}
	out, dis := gate.EligibleWorkers(context.Background(), reg)
	if _, ok := out["alpha"]; !ok {
		t.Fatalf("nil resolver must degrade to pass-through; got %+v", out)
	}
	if len(dis) != 0 {
		t.Fatalf("nil resolver must not emit disagreements, got %d", len(dis))
	}
}

func TestMemoryRegistry_SnapshotAllIgnoresTTL(t *testing.T) {
	t.Parallel()
	reg := NewMemoryRegistry()
	defer reg.Close()
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "alpha", Pool: "p"})
	staleAllHeartbeats(reg)

	if got := reg.Snapshot(); len(got) != 0 {
		t.Fatalf("Snapshot must drop stale workers; got %+v", got)
	}
	all := reg.SnapshotAll()
	if _, ok := all["alpha"]; !ok {
		t.Fatalf("SnapshotAll must surface stale workers; got %+v", all)
	}
}

func TestDispatchGate_RevokeAfterEligibleCallFlipsOutcome(t *testing.T) {
	// A dispatch attempt may loop twice (post-pick re-check). Between
	// those two calls the operator could revoke a worker. The second
	// decision must see the revocation, not the stale pre-revoke
	// state — that's the whole point of reading trust state on each
	// call rather than caching.
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 1 * time.Hour,
		Now:      clk.Now,
	})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	ctx := context.Background()
	_, claims, err := issuer.Issue(ctx, "flip", "tenant-flip", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "flip", Pool: "p"})

	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	out1, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out1["flip"]; !ok {
		t.Fatalf("first call must see worker as eligible")
	}

	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	out2, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out2["flip"]; ok {
		t.Fatalf("second call must see revoked worker as ineligible; got %+v", out2)
	}

	alive, _ := gate.IsWorkerEligible(ctx, "flip", reg.IsAlive)
	if alive {
		t.Fatal("IsWorkerEligible must reflect the revocation immediately")
	}
}

func TestDispatchGate_MultipleTenantsInSnapshot(t *testing.T) {
	// The resolver is tenant-aware: each worker's active record
	// carries its own tenant, and revocations scope per-tenant:jti.
	// A mixed pool must therefore filter per-worker, not per-tenant.
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{Now: clk.Now})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	ctx := context.Background()

	// tenant-A worker: valid.
	if _, _, err := issuer.Issue(ctx, "a-worker", "tenant-A", "v1"); err != nil {
		t.Fatalf("issue A: %v", err)
	}
	// tenant-B worker: revoked.
	_, claimsB, err := issuer.Issue(ctx, "b-worker", "tenant-B", "v1")
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}
	if err := issuer.Revoke(ctx, claimsB.Tenant, claimsB.JTI, claimsB.ExpiresAt); err != nil {
		t.Fatalf("revoke B: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "a-worker", Pool: "p"})
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "b-worker", Pool: "p"})

	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	out, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out["a-worker"]; !ok {
		t.Fatalf("a-worker must be eligible")
	}
	if _, ok := out["b-worker"]; ok {
		t.Fatalf("b-worker must not be eligible (revoked under its own tenant)")
	}
}

func TestDispatchGate_ConcurrentEligibleWorkersAreConsistent(t *testing.T) {
	// Hammering EligibleWorkers from 32 goroutines must never return
	// a partially filtered result. The gate must treat each call as
	// an independent read.
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{Now: clk.Now})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	reg := NewMemoryRegistry()
	defer reg.Close()
	ctx := context.Background()
	for _, id := range []string{"w1", "w2", "w3", "w4"} {
		if _, _, err := issuer.Issue(ctx, id, "tenant-cc", "v1"); err != nil {
			t.Fatalf("issue %s: %v", id, err)
		}
		reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: id, Pool: "cc"})
	}
	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)

	const goroutines = 32
	const iterations = 10
	done := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				out, _ := gate.EligibleWorkers(ctx, reg)
				if len(out) != 4 {
					done <- fmtErrf("concurrent read saw %d workers, want 4", len(out))
					return
				}
			}
			done <- nil
		}()
	}
	for i := 0; i < goroutines; i++ {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestDispatchGate_IsWorkerEligibleWithNilLegacy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{Now: clk.Now})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "w1", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}

	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	alive, d := gate.IsWorkerEligible(ctx, "w1", nil)
	if !alive {
		t.Fatal("valid session must be eligible even with nil legacyIsAlive")
	}
	if d != nil {
		t.Fatalf("telemetry mode must not emit disagreement; got %+v", d)
	}

	// Authority mode + nil legacyIsAlive defaults to true (no gate data).
	gateAuth := NewDispatchGate(resolver, HeartbeatModeAuthority)
	alive, d = gateAuth.IsWorkerEligible(ctx, "anything", nil)
	if !alive {
		t.Fatal("authority-mode nil legacy must default to true")
	}
	if d != nil {
		t.Fatalf("authority mode must not emit disagreement; got %+v", d)
	}
}

func TestDispatchGate_ExpiredSessionDroppedImmediately(t *testing.T) {
	// Session just past exp — the resolver's time-bound check (not
	// Redis TTL sweep) must mark it ineligible even if miniredis still
	// holds the active-record key.
	t.Parallel()
	now := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 10 * time.Minute,
		Now:      clk.Now,
	})
	defer cleanup()
	resolver := NewTrustResolver(rdb).WithClock(clk.Now)

	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "w1", "tenant-exp", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	clk.Advance(20 * time.Minute)

	reg := NewMemoryRegistry()
	defer reg.Close()
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w1", Pool: "p"})

	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	out, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out["w1"]; ok {
		t.Fatal("expired session must be dropped even before Redis sweep")
	}
}

// fmtErrf keeps the concurrency goroutine's error path off testing.T.
func fmtErrf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
