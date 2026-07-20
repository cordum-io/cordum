package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/workercredentials"
)

type boundCredentialResolverStub struct {
	record *workercredentials.Credential
	err    error
}

func (s *boundCredentialResolverStub) GetByWorkerID(context.Context, string) (*workercredentials.Credential, error) {
	return s.record, s.err
}

func TestBoundTrustResolverUsesAuthoritativeTenantAndNeverLegacy(t *testing.T) {
	t.Parallel()
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	if _, _, err := issuer.Issue(ctx, "worker-a", "tenant-a", "legacy"); err != nil {
		t.Fatal(err)
	}
	credentials := &boundCredentialResolverStub{record: &workercredentials.Credential{
		WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a", ProofKeyID: "proof-a",
	}}
	resolver, err := NewBoundTrustResolver(rdb, credentials)
	if err != nil {
		t.Fatal(err)
	}
	state, err := resolver.ResolveTrust(ctx, "worker-a")
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionValid || state.Reason != TrustReasonNoSession {
		t.Fatalf("legacy record granted bound authority: %+v", state)
	}
	binding := SessionBinding{
		WorkerID: "worker-a", AgentID: "agent-a", Tenant: "tenant-a",
		Audience: WorkerHandshakeAudience, ProofKeyID: "proof-a", SDKVersion: "v1",
	}
	if _, _, err := issuer.IssueBound(ctx, binding); err != nil {
		t.Fatal(err)
	}
	state, err = resolver.ResolveTrust(ctx, "worker-a")
	if err != nil || !state.IsAlive() || state.Tenant != "tenant-a" {
		t.Fatalf("bound session not authoritative: state=%+v err=%v", state, err)
	}
	credentials.record.TenantID = "tenant-b"
	state, err = resolver.ResolveTrust(ctx, "worker-a")
	if err != nil || state.SessionValid || state.Reason != TrustReasonNoSession {
		t.Fatalf("resolver guessed another tenant: state=%+v err=%v", state, err)
	}
}

func TestBoundTrustResolverRejectsInvalidCredentialAndStoredBinding(t *testing.T) {
	t.Parallel()
	issuer, mr, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	binding := SessionBinding{
		WorkerID: "worker-a", AgentID: "agent-a", Tenant: "tenant-a",
		Audience: WorkerHandshakeAudience, ProofKeyID: "proof-a", SDKVersion: "v1",
	}
	if _, _, err := issuer.IssueBound(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	credentials := &boundCredentialResolverStub{record: &workercredentials.Credential{
		WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a", ProofKeyID: "proof-a",
		RevokedAt: time.Now().UTC().Format(time.RFC3339),
	}}
	resolver, err := NewBoundTrustResolver(rdb, credentials)
	if err != nil {
		t.Fatal(err)
	}
	state, err := resolver.ResolveTrust(context.Background(), "worker-a")
	if err != nil || state.SessionValid || state.Reason != TrustReasonCredentialInvalid {
		t.Fatalf("revoked credential trusted: state=%+v err=%v", state, err)
	}
	credentials.record.RevokedAt = ""
	credentials.record.AgentID = "agent-attacker"
	state, err = resolver.ResolveTrust(context.Background(), "worker-a")
	if !errors.Is(err, ErrSessionTokenBindingMismatch) || state.SessionValid {
		t.Fatalf("wrong agent accepted: state=%+v err=%v", state, err)
	}
	credentials.record.AgentID = "agent-a"
	credentials.record.ProofKeyID = "proof-attacker"
	state, err = resolver.ResolveTrust(context.Background(), "worker-a")
	if !errors.Is(err, ErrSessionTokenBindingMismatch) || state.SessionValid {
		t.Fatalf("wrong proof key accepted: state=%+v err=%v", state, err)
	}
	credentials.record.ProofKeyID = "proof-a"
	record := readActiveRecordForTest(t, mr, boundWorkerKey("tenant-a", "worker-a"))
	record.Audience = "wrong-audience"
	writeActiveRecordForTest(t, mr, boundWorkerKey("tenant-a", "worker-a"), record)
	state, err = resolver.ResolveTrust(context.Background(), "worker-a")
	if !errors.Is(err, ErrSessionTokenBindingMismatch) || state.SessionValid {
		t.Fatalf("wrong audience accepted: state=%+v err=%v", state, err)
	}
}

func TestNewBoundTrustResolverRequiresAuthorities(t *testing.T) {
	t.Parallel()
	_, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	if _, err := NewBoundTrustResolver(nil, &boundCredentialResolverStub{}); err == nil {
		t.Fatal("nil Redis authority accepted")
	}
	if _, err := NewBoundTrustResolver(rdb, nil); err == nil {
		t.Fatal("nil credential authority accepted")
	}
}

func TestBoundTrustResolverRejectsExpiredAndRevokedSessions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	issuer, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Now: clock.Now, Lifetime: time.Hour,
	})
	defer cleanup()
	credentials := &boundCredentialResolverStub{record: &workercredentials.Credential{
		WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a", ProofKeyID: "proof-a",
	}}
	resolver, err := NewBoundTrustResolver(rdb, credentials)
	if err != nil {
		t.Fatal(err)
	}
	resolver.WithClock(clock.Now)
	binding := SessionBinding{
		WorkerID: "worker-a", AgentID: "agent-a", Tenant: "tenant-a",
		Audience: WorkerHandshakeAudience, ProofKeyID: "proof-a", SDKVersion: "v1",
	}
	_, claims, err := issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := issuer.Revoke(context.Background(), claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	state, err := resolver.ResolveTrust(context.Background(), "worker-a")
	if err != nil || state.Reason != TrustReasonRevoked || state.SessionValid {
		t.Fatalf("revoked state=%+v err=%v", state, err)
	}
	clock.Advance(2 * time.Hour)
	state, err = resolver.ResolveTrust(context.Background(), "worker-a")
	if err != nil || state.Reason != TrustReasonExpired || state.SessionValid {
		t.Fatalf("expired state=%+v err=%v", state, err)
	}
}

func TestBoundTrustResolverRejectsCredentialResolutionFailures(t *testing.T) {
	t.Parallel()
	_, _, rdb, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	credentials := &boundCredentialResolverStub{err: errors.New("credential store down")}
	resolver, err := NewBoundTrustResolver(rdb, credentials)
	if err != nil {
		t.Fatal(err)
	}
	state, err := resolver.ResolveTrust(context.Background(), "worker-a")
	if err == nil || state.Reason != TrustReasonStoreUnready {
		t.Fatalf("credential error state=%+v err=%v", state, err)
	}
	for name, record := range map[string]*workercredentials.Credential{
		"missing":       nil,
		"wrong_worker":  {WorkerID: "worker-b", TenantID: "tenant-a", AgentID: "agent-a", ProofKeyID: "proof-a"},
		"empty_tenant":  {WorkerID: "worker-a", AgentID: "agent-a", ProofKeyID: "proof-a"},
		"empty_agent":   {WorkerID: "worker-a", TenantID: "tenant-a", ProofKeyID: "proof-a"},
		"empty_key":     {WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a"},
		"padded_tenant": {WorkerID: "worker-a", TenantID: " tenant-a", AgentID: "agent-a", ProofKeyID: "proof-a"},
		"padded_agent":  {WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a ", ProofKeyID: "proof-a"},
		"padded_key":    {WorkerID: "worker-a", TenantID: "tenant-a", AgentID: "agent-a", ProofKeyID: " proof-a"},
	} {
		t.Run(name, func(t *testing.T) {
			credentials.err, credentials.record = nil, record
			state, err := resolver.ResolveTrust(context.Background(), "worker-a")
			if err != nil || state.Reason != TrustReasonCredentialInvalid || state.SessionValid {
				t.Fatalf("invalid credential state=%+v err=%v", state, err)
			}
		})
	}
}
