package scheduler

import (
	"context"
	"errors"
	"testing"
)

func TestSessionTokenRevokeByWorker_RevokesBoundWorkerSession(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	token, _, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	if err := issuer.RevokeByWorker(ctx, binding.Tenant, binding.WorkerID); err != nil {
		t.Fatalf("revoke bound worker: %v", err)
	}
	_, err = issuer.VerifyBound(ctx, token, true)
	if !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("VerifyBound error = %v, want ErrSessionTokenRevoked", err)
	}
}

func TestSessionTokenRevokeByWorker_DoesNotCrossTenantBoundary(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	token, _, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	if err := issuer.RevokeByWorker(ctx, "tenant-b", binding.WorkerID); err != nil {
		t.Fatalf("cross-tenant no-op: %v", err)
	}
	if _, err := issuer.VerifyBound(ctx, token, true); err != nil {
		t.Fatalf("tenant-b revoke affected tenant-a session: %v", err)
	}
}

func TestSessionTokenRevokeByWorker_CorruptBoundRecordFailsClosed(t *testing.T) {
	t.Parallel()
	issuer, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	if _, _, err := issuer.IssueBound(ctx, binding); err != nil {
		t.Fatalf("issue bound: %v", err)
	}
	if err := mr.Set(boundWorkerKey(binding.Tenant, binding.WorkerID), "not-json"); err != nil {
		t.Fatalf("write corrupt bound record: %v", err)
	}

	if err := issuer.RevokeByWorker(ctx, binding.Tenant, binding.WorkerID); err == nil {
		t.Fatal("corrupt tenant-scoped record must fail closed")
	}
}

func TestSessionTokenRevokeByWorker_RequiresTenantAndWorker(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	for _, input := range []struct{ tenant, worker string }{
		{"", "worker-a"}, {"tenant-a", ""}, {"  ", "worker-a"},
	} {
		err := issuer.RevokeByWorker(context.Background(), input.tenant, input.worker)
		if !errors.Is(err, ErrSessionTokenMissingClaims) {
			t.Fatalf("RevokeByWorker(%q, %q) error = %v", input.tenant, input.worker, err)
		}
	}
}

func TestSessionTokenRevokeByWorker_RevokesBoundAndLegacySessions(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	legacy, _, err := issuer.Issue(ctx, binding.WorkerID, binding.Tenant, binding.SDKVersion)
	if err != nil {
		t.Fatalf("issue legacy: %v", err)
	}
	bound, _, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	if err := issuer.RevokeByWorker(ctx, binding.Tenant, binding.WorkerID); err != nil {
		t.Fatalf("revoke sessions: %v", err)
	}
	if _, err := issuer.VerifyBound(ctx, bound, true); !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("bound Verify error = %v, want revoked", err)
	}
	if _, err := issuer.Verify(ctx, legacy, true); !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("legacy Verify error = %v, want revoked", err)
	}
}

func TestSessionTokenRevokeByAgent_PreservesLegacyIdentifierSemantics(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	legacy, _, err := issuer.Issue(ctx, binding.AgentID, binding.Tenant, binding.SDKVersion)
	if err != nil {
		t.Fatalf("issue legacy: %v", err)
	}
	bound, _, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	if err := issuer.RevokeByAgent(ctx, binding.Tenant, binding.AgentID); err != nil {
		t.Fatalf("revoke legacy agent session: %v", err)
	}
	if _, err := issuer.Verify(ctx, legacy, true); !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("legacy Verify error = %v, want revoked", err)
	}
	if _, err := issuer.VerifyBound(ctx, bound, true); err != nil {
		t.Fatalf("agent-id revoke crossed into distinct worker subject: %v", err)
	}
}
