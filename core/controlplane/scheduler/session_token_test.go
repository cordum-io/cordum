package scheduler

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/policysign"
	"github.com/redis/go-redis/v9"
)

// newTestIssuer wires up an Ed25519 key pair, a populated TrustStore,
// and an in-memory Redis (miniredis) backed go-redis client. The
// returned cleanup func must be deferred by the caller.
func newTestIssuer(t *testing.T, opts SessionTokenIssuerOptions) (*SessionTokenIssuer, *miniredis.Miniredis, redis.UniversalClient, func()) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	trust := policysign.NewTrustStore()
	if err := trust.Add("primary", pub); err != nil {
		t.Fatalf("trust add: %v", err)
	}
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	issuer, err := NewSessionTokenIssuer(priv, "primary", trust, rdb, opts)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	cleanup := func() {
		_ = rdb.Close()
		mr.Close()
	}
	return issuer, mr, rdb, cleanup
}

func TestSessionTokenIssue_RoundTrip(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	token, claims, err := issuer.Issue(ctx, "agent-001", "tenant-acme", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if token == "" {
		t.Fatal("issue returned empty token")
	}
	if claims.Subject != "agent-001" || claims.Tenant != "tenant-acme" || claims.SDKVersion != "v2.9.0" {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.JTI == "" {
		t.Fatal("jti must be populated")
	}
	if !claims.ExpiresAt.After(claims.IssuedAt) {
		t.Fatalf("exp must be after iat: %+v", claims)
	}

	got, err := issuer.Verify(ctx, token, true)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.JTI != claims.JTI || got.Subject != claims.Subject {
		t.Fatalf("verify returned different claims: got=%+v want=%+v", got, claims)
	}
}

func TestSessionTokenIssue_RequiresAllFields(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	cases := []struct {
		name                      string
		agent, tenant, sdkVersion string
	}{
		{"empty agent", "", "tenant", "v1"},
		{"empty tenant", "agent", "", "v1"},
		{"empty sdk", "agent", "tenant", ""},
		{"whitespace agent", "   ", "tenant", "v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := issuer.Issue(context.Background(), c.agent, c.tenant, c.sdkVersion)
			if !errors.Is(err, ErrSessionTokenMissingClaims) {
				t.Fatalf("expected ErrSessionTokenMissingClaims, got %v", err)
			}
		})
	}
}

func TestSessionTokenVerify_RejectsBadSignature(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "agent-001", "tenant-acme", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 segments, got %d", len(parts))
	}
	// Flip a byte in the signature.
	rawSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	rawSig[0] ^= 0xFF
	parts[2] = base64.RawURLEncoding.EncodeToString(rawSig)
	tampered := strings.Join(parts, ".")

	_, err = issuer.Verify(ctx, tampered, false)
	if !errors.Is(err, ErrSessionTokenSignature) {
		t.Fatalf("expected ErrSessionTokenSignature, got %v", err)
	}
}

func TestSessionTokenVerify_RejectsTamperedClaims(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	token, claims, err := issuer.Issue(ctx, "agent-001", "tenant-acme", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	parts := strings.Split(token, ".")
	// Re-encode claims with a different tenant (privilege escalation
	// attempt). Signature was over the original claims segment so this
	// must fail signature verification.
	tampered := claims
	tampered.Tenant = "tenant-evil"
	raw, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(raw)
	bad := strings.Join(parts, ".")

	_, err = issuer.Verify(ctx, bad, false)
	if !errors.Is(err, ErrSessionTokenSignature) {
		t.Fatalf("expected signature error, got %v", err)
	}
}

func TestSessionTokenVerify_RejectsExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 5 * time.Minute,
		Skew:     30 * time.Second,
		Now:      clk.Now,
	})
	defer cleanup()

	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "agent", "tenant", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Move past exp + skew.
	clk.Advance(6 * time.Minute)
	_, err = issuer.Verify(ctx, token, false)
	if !errors.Is(err, ErrSessionTokenExpired) {
		t.Fatalf("expected ErrSessionTokenExpired, got %v", err)
	}
}

func TestSessionTokenVerify_RejectsNotYetValid(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: time.Hour,
		Skew:     30 * time.Second,
		Now:      clk.Now,
	})
	defer cleanup()

	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "agent", "tenant", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Roll clock backwards more than skew.
	clk.now = now.Add(-2 * time.Minute)
	_, err = issuer.Verify(ctx, token, false)
	if !errors.Is(err, ErrSessionTokenNotYetValid) {
		t.Fatalf("expected ErrSessionTokenNotYetValid, got %v", err)
	}
}

func TestSessionTokenVerify_RejectsUnknownKey(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	// Build a token using a different key + kid the trust store does
	// not know about.
	otherPub, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	trust := policysign.NewTrustStore()
	_ = trust.Add("hostile", otherPub)
	rogue, err := NewSessionTokenIssuer(otherPriv, "hostile", trust, nil, SessionTokenIssuerOptions{})
	if err != nil {
		t.Fatalf("rogue issuer: %v", err)
	}
	token, err := rogue.signClaims(SessionTokenClaims{
		Subject:    "agent",
		Tenant:     "tenant",
		SDKVersion: "v1",
		JTI:        "abcd1234abcd1234",
		IssuedAt:   time.Now().UTC(),
		ExpiresAt:  time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("rogue sign: %v", err)
	}

	_, err = issuer.Verify(context.Background(), token, false)
	if !errors.Is(err, ErrSessionTokenUnknownKey) {
		t.Fatalf("expected ErrSessionTokenUnknownKey, got %v", err)
	}
}

func TestSessionTokenVerify_RejectsMalformed(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	cases := []string{
		"",
		"not-a-token",
		"only.two",
		"a.b.c.d",
		"!!!.!!!.!!!", // invalid base64
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := issuer.Verify(ctx, raw, false)
			if err == nil {
				t.Fatalf("expected error for %q", raw)
			}
			if !errors.Is(err, ErrSessionTokenMalformed) {
				t.Fatalf("expected malformed err, got %v", err)
			}
		})
	}
}

func TestSessionTokenRevoke_BlocksVerify(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	token, claims, err := issuer.Issue(ctx, "agent-x", "tenant-y", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	// Pre-revoke verify is fine.
	if _, err := issuer.Verify(ctx, token, true); err != nil {
		t.Fatalf("pre-revoke verify: %v", err)
	}

	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, err = issuer.Verify(ctx, token, true)
	if !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("expected ErrSessionTokenRevoked, got %v", err)
	}

	// Verify without active-check still passes — the signature is
	// untampered. This proves the revocation lives in Redis, not the
	// crypto layer.
	if _, err := issuer.Verify(ctx, token, false); err != nil {
		t.Fatalf("signature-only verify after revoke: %v", err)
	}
}

func TestSessionTokenRenew_RotatesAndRevokesOld(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: time.Hour,
	})
	defer cleanup()

	ctx := context.Background()
	old, oldClaims, err := issuer.Issue(ctx, "agent-r", "tenant-r", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	fresh, freshClaims, err := issuer.Renew(ctx, old)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if fresh == old {
		t.Fatal("renew returned identical token")
	}
	if freshClaims.JTI == oldClaims.JTI {
		t.Fatal("renewed token must have new jti")
	}
	if freshClaims.Subject != oldClaims.Subject || freshClaims.Tenant != oldClaims.Tenant {
		t.Fatalf("renew altered identity: %+v -> %+v", oldClaims, freshClaims)
	}

	// Old token must now be refused under active check (single active
	// token per worker invariant).
	_, err = issuer.Verify(ctx, old, true)
	if !errors.Is(err, ErrSessionTokenSuperseded) && !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("expected superseded or revoked for old token, got %v", err)
	}
	// Fresh token verifies cleanly.
	if _, err := issuer.Verify(ctx, fresh, true); err != nil {
		t.Fatalf("fresh verify: %v", err)
	}
}

func TestSessionTokenRenew_RefusesExpiredBeyondSkew(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	clk := &fakeClock{now: now}
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{
		Lifetime: 5 * time.Minute,
		Skew:     30 * time.Second,
		Now:      clk.Now,
	})
	defer cleanup()

	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "agent", "tenant", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Far past expiry.
	clk.Advance(time.Hour)
	_, _, err = issuer.Renew(ctx, token)
	if !errors.Is(err, ErrSessionTokenExpired) {
		t.Fatalf("expected ErrSessionTokenExpired, got %v", err)
	}
}

func TestSessionTokenRenew_RefusesRevokedToken(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	token, claims, err := issuer.Issue(ctx, "agent", "tenant", "v1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := issuer.Revoke(ctx, claims.Tenant, claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, _, err = issuer.Renew(ctx, token)
	if !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("expected ErrSessionTokenRevoked, got %v", err)
	}
}

func TestSessionTokenRevokeByAgent(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	token, _, err := issuer.Issue(ctx, "agent-z", "tenant-z", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	if err := issuer.RevokeByAgent(ctx, "tenant-z", "agent-z"); err != nil {
		t.Fatalf("revoke-by-agent: %v", err)
	}
	if _, err := issuer.Verify(ctx, token, true); !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("expected ErrSessionTokenRevoked, got %v", err)
	}

	// No-active-token path is a no-op success.
	if err := issuer.RevokeByAgent(ctx, "tenant-z", "no-such-agent"); err != nil {
		t.Fatalf("revoke-by-agent no-op: %v", err)
	}
}

func TestSessionTokenIssue_PersistsActiveRecord(t *testing.T) {
	t.Parallel()
	issuer, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	_, claims, err := issuer.Issue(ctx, "agent-p", "tenant-p", "v2.9.0")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	raw, err := mr.Get(workerKey("agent-p"))
	if err != nil {
		t.Fatalf("active record missing: %v", err)
	}
	var rec activeRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("active record parse: %v", err)
	}
	if rec.JTI != claims.JTI || rec.ExpUnix != claims.ExpiresAt.Unix() || rec.Tenant != claims.Tenant {
		t.Fatalf("active record mismatch: %+v vs %+v", rec, claims)
	}
	// TTL must be set so a long-dead worker's record self-clears.
	ttl := mr.TTL(workerKey("agent-p"))
	if ttl <= 0 {
		t.Fatalf("expected positive ttl, got %v", ttl)
	}
}

func TestSessionTokenIssue_RotationRevokesPriorJTI(t *testing.T) {
	t.Parallel()
	issuer, _, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()

	ctx := context.Background()
	first, _, err := issuer.Issue(ctx, "agent-rot", "tenant-rot", "v2.9.0")
	if err != nil {
		t.Fatalf("issue1: %v", err)
	}
	_, _, err = issuer.Issue(ctx, "agent-rot", "tenant-rot", "v2.9.0")
	if err != nil {
		t.Fatalf("issue2: %v", err)
	}
	_, err = issuer.Verify(ctx, first, true)
	if !errors.Is(err, ErrSessionTokenSuperseded) && !errors.Is(err, ErrSessionTokenRevoked) {
		t.Fatalf("expected first token rejected after rotation, got %v", err)
	}
}

func TestNewSessionTokenIssuer_RejectsMisconfig(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	trust := policysign.NewTrustStore()
	_ = trust.Add("primary", pub)

	cases := []struct {
		name string
		key  ed25519.PrivateKey
		kid  string
		ts   *policysign.TrustStore
		want error
	}{
		{"bad key length", ed25519.PrivateKey{0x01}, "primary", trust, policysign.ErrInvalidPrivateKey},
		{"empty kid", priv, "  ", trust, policysign.ErrEmptyKeyID},
		{"nil trust", priv, "primary", nil, errors.New("trust store")},
		{"kid not in trust", priv, "missing", trust, errors.New("not present")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewSessionTokenIssuer(c.key, c.kid, c.ts, nil, SessionTokenIssuerOptions{})
			if err == nil {
				t.Fatal("expected error")
			}
			if errors.Is(c.want, policysign.ErrInvalidPrivateKey) || errors.Is(c.want, policysign.ErrEmptyKeyID) {
				if !errors.Is(err, c.want) {
					t.Fatalf("expected %v, got %v", c.want, err)
				}
			} else {
				if !strings.Contains(err.Error(), c.want.Error()) {
					t.Fatalf("expected error containing %q, got %v", c.want.Error(), err)
				}
			}
		})
	}
}

type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time          { return f.now }
func (f *fakeClock) Advance(d time.Duration) { f.now = f.now.Add(d) }
