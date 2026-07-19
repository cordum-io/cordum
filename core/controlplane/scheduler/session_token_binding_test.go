package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
)

func boundTestSession(tenant string) SessionBinding {
	return SessionBinding{
		WorkerID:   "worker-001",
		AgentID:    "agent-001",
		Tenant:     tenant,
		Audience:   "cordum-scheduler",
		ProofKeyID: "proof-key-001",
		SDKVersion: "v2.9.0",
	}
}

func readActiveRecordForTest(t *testing.T, mr *miniredis.Miniredis, key string) activeRecord {
	t.Helper()
	raw, err := mr.Get(key)
	if err != nil {
		t.Fatalf("read active record: %v", err)
	}
	var rec activeRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("parse active record: %v", err)
	}
	return rec
}

func writeActiveRecordForTest(t *testing.T, mr *miniredis.Miniredis, key string, rec activeRecord) {
	t.Helper()
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal active record: %v", err)
	}
	if err := mr.Set(key, string(raw)); err != nil {
		t.Fatalf("write active record: %v", err)
	}
}

func TestSessionTokenIssueBound_RoundTripsWorkerAuthority(t *testing.T) {
	t.Parallel()
	issuer, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	binding := boundTestSession("tenant-acme")
	token, claims, err := issuer.IssueBound(context.Background(), binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}
	if claims.Subject != binding.WorkerID || claims.AgentID != binding.AgentID {
		t.Fatalf("worker authority mismatch: claims=%+v binding=%+v", claims, binding)
	}
	if claims.Tenant != binding.Tenant || claims.Audience != binding.Audience {
		t.Fatalf("tenant/audience mismatch: claims=%+v binding=%+v", claims, binding)
	}
	if claims.ProofKeyID != binding.ProofKeyID || claims.SDKVersion != binding.SDKVersion {
		t.Fatalf("proof/sdk mismatch: claims=%+v binding=%+v", claims, binding)
	}

	verified, err := issuer.VerifyBound(context.Background(), token, true)
	if err != nil {
		t.Fatalf("verify bound: %v", err)
	}
	if verified != claims {
		t.Fatalf("round trip changed claims: got=%+v want=%+v", verified, claims)
	}
	if !mr.Exists(boundWorkerKey(binding.Tenant, binding.WorkerID)) {
		t.Fatalf("tenant-scoped active record missing: %s", boundWorkerKey(binding.Tenant, binding.WorkerID))
	}
	if mr.Exists(workerKey(binding.WorkerID)) {
		t.Fatal("bound session must not use the legacy tenantless active key")
	}
}

func TestSessionTokenIssueBound_FailsClosedWithoutActiveStore(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	issuer.redis = nil

	token, claims, err := issuer.IssueBound(context.Background(), boundTestSession("tenant-a"))
	if !errors.Is(err, ErrSessionTokenStoreUnready) {
		t.Fatalf("IssueBound error = %v, want ErrSessionTokenStoreUnready", err)
	}
	if token != "" || claims != (SessionTokenClaims{}) {
		t.Fatalf("failed IssueBound leaked authority: token=%q claims=%+v", token, claims)
	}
}

func TestSessionTokenIssueBound_RequiresEveryAuthorityField(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	mutations := []struct {
		name   string
		mutate func(*SessionBinding)
	}{
		{"worker", func(b *SessionBinding) { b.WorkerID = "" }},
		{"agent", func(b *SessionBinding) { b.AgentID = "" }},
		{"tenant", func(b *SessionBinding) { b.Tenant = "" }},
		{"audience", func(b *SessionBinding) { b.Audience = "" }},
		{"proof key", func(b *SessionBinding) { b.ProofKeyID = "" }},
		{"sdk version", func(b *SessionBinding) { b.SDKVersion = "" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			binding := boundTestSession("tenant-a")
			tc.mutate(&binding)
			_, _, err := issuer.IssueBound(context.Background(), binding)
			if !errors.Is(err, ErrSessionTokenMissingClaims) {
				t.Fatalf("IssueBound error = %v, want ErrSessionTokenMissingClaims", err)
			}
		})
	}
}

func TestSessionTokenIssueBound_SameWorkerIDAcrossTenantsDoesNotCollide(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	first := boundTestSession("tenant-a")
	firstToken, _, err := issuer.IssueBound(ctx, first)
	if err != nil {
		t.Fatalf("issue tenant-a: %v", err)
	}
	second := boundTestSession("tenant-b")
	second.ProofKeyID = "proof-key-002"
	secondToken, _, err := issuer.IssueBound(ctx, second)
	if err != nil {
		t.Fatalf("issue tenant-b: %v", err)
	}
	if _, err := issuer.VerifyBound(ctx, firstToken, true); err != nil {
		t.Fatalf("tenant-a token was displaced by tenant-b: %v", err)
	}
	if _, err := issuer.VerifyBound(ctx, secondToken, true); err != nil {
		t.Fatalf("tenant-b token invalid: %v", err)
	}
}

func TestSessionTokenVerifyBound_RejectsLegacyUnboundToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	token, _, err := issuer.Issue(context.Background(), "legacy-agent", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue legacy: %v", err)
	}
	_, err = issuer.VerifyBound(context.Background(), token, true)
	if !errors.Is(err, ErrSessionTokenMissingClaims) {
		t.Fatalf("VerifyBound error = %v, want ErrSessionTokenMissingClaims", err)
	}
}

func TestSessionTokenVerifyBound_RejectsStoredAuthorityMismatch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(*activeRecord)
	}{
		{"tenant", func(rec *activeRecord) { rec.Tenant = "tenant-attacker" }},
		{"worker", func(rec *activeRecord) { rec.WorkerID = "worker-attacker" }},
		{"agent", func(rec *activeRecord) { rec.AgentID = "agent-attacker" }},
		{"audience", func(rec *activeRecord) { rec.Audience = "cordum-api-gateway" }},
		{"proof key", func(rec *activeRecord) { rec.ProofKeyID = "proof-key-attacker" }},
		{"sdk", func(rec *activeRecord) { rec.SDKVersion = "v-attacker" }},
		{"expiry", func(rec *activeRecord) { rec.ExpUnix++ }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			issuer, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
			defer cleanup()
			ctx := context.Background()
			binding := boundTestSession("tenant-a")
			token, _, err := issuer.IssueBound(ctx, binding)
			if err != nil {
				t.Fatalf("issue bound: %v", err)
			}
			key := boundWorkerKey(binding.Tenant, binding.WorkerID)
			rec := readActiveRecordForTest(t, mr, key)
			tc.mutate(&rec)
			writeActiveRecordForTest(t, mr, key, rec)

			_, err = issuer.VerifyBound(ctx, token, true)
			if !errors.Is(err, ErrSessionTokenBindingMismatch) {
				t.Fatalf("VerifyBound error = %v, want ErrSessionTokenBindingMismatch", err)
			}
		})
	}
}

func TestSessionTokenRenew_RequiresCurrentActiveToken(t *testing.T) {
	t.Parallel()
	issuer, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()

	oldToken, oldClaims, err := issuer.Issue(ctx, "agent-renew", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue old: %v", err)
	}
	if _, _, err := issuer.Issue(ctx, "agent-renew", "tenant-a", "v1"); err != nil {
		t.Fatalf("issue replacement: %v", err)
	}
	// Remove the defense-in-depth revocation marker. Renewal must still reject
	// because the presented JTI is no longer the current active record.
	mr.Del(revokedKey(oldClaims.Tenant, oldClaims.JTI))
	_, _, err = issuer.Renew(ctx, oldToken)
	if !errors.Is(err, ErrSessionTokenSuperseded) {
		t.Fatalf("Renew error = %v, want ErrSessionTokenSuperseded", err)
	}
}

func TestSessionTokenRenew_FailsClosedWithoutActiveStore(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	token, _, err := issuer.Issue(context.Background(), "agent-renew", "tenant-a", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	issuer.redis = nil

	_, _, err = issuer.Renew(context.Background(), token)
	if !errors.Is(err, ErrSessionTokenStoreUnready) {
		t.Fatalf("Renew error = %v, want ErrSessionTokenStoreUnready", err)
	}
}

func TestSessionTokenRenewBound_RequiresAuthoritativeBinding(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	token, _, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	attacker := binding
	attacker.ProofKeyID = "proof-key-attacker"
	_, _, err = issuer.RenewBound(ctx, token, attacker)
	if !errors.Is(err, ErrSessionTokenBindingMismatch) {
		t.Fatalf("RenewBound error = %v, want ErrSessionTokenBindingMismatch", err)
	}
}

func TestSessionTokenRenewBound_PreservesAuthority(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	binding := boundTestSession("tenant-a")
	oldToken, oldClaims, err := issuer.IssueBound(ctx, binding)
	if err != nil {
		t.Fatalf("issue bound: %v", err)
	}

	newToken, newClaims, err := issuer.RenewBound(ctx, oldToken, binding)
	if err != nil {
		t.Fatalf("renew bound: %v", err)
	}
	if newClaims.JTI == oldClaims.JTI || newToken == oldToken {
		t.Fatal("renew bound must rotate token and JTI")
	}
	if newClaims.Binding() != binding {
		t.Fatalf("renew changed authority: got=%+v want=%+v", newClaims.Binding(), binding)
	}
	if _, err := issuer.VerifyBound(ctx, newToken, true); err != nil {
		t.Fatalf("verify renewed bound token: %v", err)
	}
	if _, err := issuer.VerifyBound(ctx, oldToken, true); err == nil {
		t.Fatal("old bound token remained active after renew")
	}
}
