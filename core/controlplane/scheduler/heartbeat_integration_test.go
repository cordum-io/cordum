//go:build integration
// +build integration

package scheduler

// Integration scenario for the heartbeat-demotion rollout. Drives a
// real Engine + DispatchGate + TrustResolver + MemoryRegistry +
// SessionTokenIssuer + miniredis — zero mocks. Exercises the three
// authoritative modes end-to-end:
//
//   authority  — legacy. Stale heartbeat blocks dispatch, session
//                authority is not consulted.
//   telemetry  — demotion target. Session authority gates dispatch;
//                heartbeat staleness is irrelevant.
//   revoke    — session revoked at runtime under telemetry mode. The
//                next dispatch attempt must refuse, even if the
//                heartbeat is fresh.
//
// Run with: make test-integration (go test -tags=integration ./...).

import (
	"context"
	"testing"
	"time"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// staleHeartbeatSince rewinds every worker's lastSeen past the TTL
// horizon so the session gate is the only thing that can admit them.
func staleHeartbeatSince(reg *MemoryRegistry) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	cutoff := time.Now().Add(-2 * reg.ttl)
	for _, entry := range reg.workers {
		entry.lastSeen = cutoff
	}
}

func TestHeartbeatDemotion_AuthorityModeBlocksStaleHeartbeat(t *testing.T) {
	// Under authority mode the scheduler's dispatch path refuses a
	// worker with a stale heartbeat, regardless of the session
	// token state. This is the legacy behaviour that the demotion
	// replaces — we codify it so a future regression (e.g. someone
	// flips the default mode) is caught immediately.
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
	if _, _, err := issuer.Issue(ctx, "w-auth", "tenant-auth", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-auth", Pool: "p"})
	staleHeartbeatSince(reg)

	gate := NewDispatchGate(resolver, HeartbeatModeAuthority)
	out, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out["w-auth"]; ok {
		t.Fatalf("authority mode must drop stale-heartbeat worker; got %+v", out)
	}
}

func TestHeartbeatDemotion_TelemetryModeDispatchesStaleHeartbeat(t *testing.T) {
	// Under telemetry mode the scheduler's dispatch path admits a
	// worker with a stale heartbeat as long as its session token
	// is valid. This is the load-bearing demotion invariant.
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
	if _, _, err := issuer.Issue(ctx, "w-tel", "tenant-tel", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-tel", Pool: "p"})
	staleHeartbeatSince(reg)

	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	out, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out["w-tel"]; !ok {
		t.Fatalf("telemetry mode must admit stale-heartbeat worker with valid session; got %+v", out)
	}
	alive, _ := gate.IsWorkerEligible(ctx, "w-tel", reg.IsAlive)
	if !alive {
		t.Fatal("IsWorkerEligible must agree with EligibleWorkers")
	}
}

func TestHeartbeatDemotion_RevokedSessionBlocksFreshHeartbeat(t *testing.T) {
	// The counterpart invariant: under telemetry mode, a revoked
	// session blocks dispatch even if the worker is still sending
	// fresh heartbeats. Demonstrates that the demotion does not
	// weaken the admission control — it strengthens it.
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
	_, claims, err := issuer.Issue(ctx, "w-rev", "tenant-rev", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-rev", Pool: "p"})
	// Heartbeat is fresh here — lastSeen = time.Now().

	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	gate := NewDispatchGate(resolver, HeartbeatModeTelemetry)
	out, _ := gate.EligibleWorkers(ctx, reg)
	if _, ok := out["w-rev"]; ok {
		t.Fatalf("telemetry mode must drop revoked-session worker even with fresh heartbeat; got %+v", out)
	}
}

func TestHeartbeatDemotion_WarnModeEmitsDisagreementsOnBothSides(t *testing.T) {
	// Warn mode fires a disagreement event for (a) valid session +
	// stale heartbeat and (b) revoked session + fresh heartbeat.
	// Running both through a single gate call captures both
	// directions — SIEM correlation rules rely on seeing the full
	// bidirectional distribution.
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

	// A: valid + stale ⇒ session_allows_heartbeat_blocks.
	if _, _, err := issuer.Issue(ctx, "w-allow", "tenant-w", "v1"); err != nil {
		t.Fatalf("issue A: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-allow", Pool: "p"})

	// B: revoked + fresh ⇒ session_blocks_heartbeat_allows.
	_, claimsB, err := issuer.Issue(ctx, "w-block", "tenant-w", "v1")
	if err != nil {
		t.Fatalf("issue B: %v", err)
	}
	if err := issuer.Revoke(ctx, claimsB.Tenant, claimsB.JTI, claimsB.ExpiresAt); err != nil {
		t.Fatalf("revoke B: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-block", Pool: "p"})

	staleHeartbeatSince(reg)
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-block", Pool: "p"}) // re-fresh B

	gate := NewDispatchGate(resolver, HeartbeatModeWarn)
	_, disagreements := gate.EligibleWorkers(ctx, reg)

	foundAllowsBlocks, foundBlocksAllows := false, false
	for _, d := range disagreements {
		switch d.Direction {
		case "session_allows_heartbeat_blocks":
			if d.WorkerID == "w-allow" {
				foundAllowsBlocks = true
			}
		case "session_blocks_heartbeat_allows":
			if d.WorkerID == "w-block" {
				foundBlocksAllows = true
			}
		}
	}
	if !foundAllowsBlocks || !foundBlocksAllows {
		t.Fatalf("missing disagreement directions: allows_blocks=%v blocks_allows=%v (got %d total)", foundAllowsBlocks, foundBlocksAllows, len(disagreements))
	}
}

func TestHeartbeatDemotion_ModeFlipFlipsOutcome(t *testing.T) {
	// Same worker, same registry state, two gates: the only thing
	// that changes is CORDUM_HEARTBEAT_MODE. Authority rejects,
	// telemetry admits. Exercises the rollback ergonomic described
	// in docs/architecture/heartbeat-demotion.md §8.
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
	if _, _, err := issuer.Issue(ctx, "w-flip", "tenant-flip", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	reg.UpdateHeartbeat(&pb.Heartbeat{WorkerId: "w-flip", Pool: "p"})
	staleHeartbeatSince(reg)

	authority := NewDispatchGate(resolver, HeartbeatModeAuthority)
	telemetry := NewDispatchGate(resolver, HeartbeatModeTelemetry)

	outAuth, _ := authority.EligibleWorkers(ctx, reg)
	outTel, _ := telemetry.EligibleWorkers(ctx, reg)

	if _, ok := outAuth["w-flip"]; ok {
		t.Fatalf("authority mode must block; got %+v", outAuth)
	}
	if _, ok := outTel["w-flip"]; !ok {
		t.Fatalf("telemetry mode must admit; got %+v", outTel)
	}
}
