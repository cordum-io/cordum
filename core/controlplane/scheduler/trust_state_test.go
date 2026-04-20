package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestTrustResolver_NoSessionWhenStoreEmpty(t *testing.T) {
	t.Parallel()
	_, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	res := NewTrustResolver(rdb)
	state, err := res.ResolveTrust(context.Background(), "agent-unknown")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if state.SessionValid {
		t.Fatalf("expected SessionValid=false, got %+v", state)
	}
	if state.Reason != TrustReasonNoSession {
		t.Fatalf("reason=%q want %q", state.Reason, TrustReasonNoSession)
	}
	if state.IsAlive() {
		t.Fatal("IsAlive should be false")
	}
}

func TestTrustResolver_ValidSession(t *testing.T) {
	t.Parallel()
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	_, claims, err := issuer.Issue(ctx, "agent-1", "tenant-acme", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	res := NewTrustResolver(rdb)
	state, err := res.ResolveTrust(ctx, "agent-1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !state.SessionValid {
		t.Fatalf("expected valid session, got %+v", state)
	}
	if state.Reason != TrustReasonValid {
		t.Fatalf("reason=%q want %q", state.Reason, TrustReasonValid)
	}
	if state.JTI != claims.JTI {
		t.Fatalf("jti mismatch: %q vs %q", state.JTI, claims.JTI)
	}
	if state.Tenant != "tenant-acme" {
		t.Fatalf("tenant=%q", state.Tenant)
	}
	if !state.IsAlive() {
		t.Fatal("IsAlive should be true")
	}
}

func TestTrustResolver_ExpiredSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 5 * time.Minute,
		Now:      clk.Now,
	})
	defer cleanup()

	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "agent-2", "tenant-acme", "v1"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	clk.Advance(10 * time.Minute)

	res := NewTrustResolver(rdb).WithClock(clk.Now)
	state, _ := res.ResolveTrust(ctx, "agent-2")
	// miniredis honours TTL — by now the per-agent record should be gone.
	// In that case Reason=NoSession; otherwise Reason=Expired. Either is
	// a "not alive" outcome.
	if state.SessionValid {
		t.Fatalf("expected expired/absent session, got %+v", state)
	}
	if state.Reason != TrustReasonExpired && state.Reason != TrustReasonNoSession {
		t.Fatalf("reason=%q want expired or no_session", state.Reason)
	}
}

func TestTrustResolver_RevokedSession(t *testing.T) {
	t.Parallel()
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	_, claims, err := issuer.Issue(ctx, "agent-3", "tenant-acme", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	res := NewTrustResolver(rdb)
	state, err := res.ResolveTrust(ctx, "agent-3")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if state.SessionValid || state.RevokedAt == nil {
		t.Fatalf("expected revoked, got %+v", state)
	}
	if state.Reason != TrustReasonRevoked {
		t.Fatalf("reason=%q want %q", state.Reason, TrustReasonRevoked)
	}
	if state.IsAlive() {
		t.Fatal("IsAlive should be false on revocation")
	}
}

func TestTrustResolver_NilStoreReportsUnready(t *testing.T) {
	t.Parallel()
	res := NewTrustResolver(nil)
	state, err := res.ResolveTrust(context.Background(), "agent-x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if state.SessionValid {
		t.Fatal("nil store must not report valid")
	}
	if state.Reason != TrustReasonStoreUnready {
		t.Fatalf("reason=%q", state.Reason)
	}
}

func TestTrustResolver_BatchPreservesPerAgentOutcome(t *testing.T) {
	t.Parallel()
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	if _, _, err := issuer.Issue(ctx, "alive", "t", "v1"); err != nil {
		t.Fatalf("issue alive: %v", err)
	}
	_, claims, err := issuer.Issue(ctx, "revoked", "t", "v1")
	if err != nil {
		t.Fatalf("issue revoked: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	res := NewTrustResolver(rdb)
	out, err := res.ResolveTrustBatch(ctx, []string{"alive", "revoked", "absent"})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if !out["alive"].SessionValid {
		t.Fatal("alive should be valid")
	}
	if out["revoked"].SessionValid || out["revoked"].RevokedAt == nil {
		t.Fatalf("revoked: %+v", out["revoked"])
	}
	if out["absent"].SessionValid || out["absent"].Reason != TrustReasonNoSession {
		t.Fatalf("absent: %+v", out["absent"])
	}
}

func TestTrustResolver_RejectsEmptyAgentID(t *testing.T) {
	t.Parallel()
	_, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	res := NewTrustResolver(rdb)
	_, err := res.ResolveTrust(context.Background(), "  ")
	if err == nil {
		t.Fatal("expected error for empty agent id")
	}
	if !errors.Is(err, err) {
		t.Fatalf("unexpected error: %v", err)
	}
}
