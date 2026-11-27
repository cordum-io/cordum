package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/redis/go-redis/v9"
)

// Retention proofs cited by docs/contract/deletion-proofs.md. Each proof writes
// a marker through the production writer with its retention shortened, requires
// tracescan to find it in every key family it names, shows it survives until
// just before the shortened TTL, moves miniredis just past it, then requires no
// trace in the writer client's keyspace.
// Exports: these writers reach none; they write only the Redis keys named here.

// TestRetentionProofDIC39 covers sessions and their per-user index, both
// login-failure counters, and SAML and OIDC request state. The SAML and OIDC
// TTLs are arguments, as the adapters pass their CORDUM_*_STATE_TTL values.
func TestRetentionProofDIC39(t *testing.T) {
	const ttl = time.Minute
	t.Setenv("CORDUM_AUTH_SESSION_TTL", ttl.String())
	t.Setenv("LOGIN_LOCKOUT_PERIOD", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisUserStoreFromClient(client)
	user := &User{ID: marker + "-uid", Username: marker + "-user", Tenant: marker + "-tenant", Role: "viewer"}
	if err := s.StoreSession(ctx, marker+"-token", user, authSessionTTL()); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}
	s.RecordFailedLogin(ctx, user.Username, "203.0.113.7")
	if err := s.PutSAMLState(ctx, marker+"-saml", marker+"-request", "/after/"+marker, ttl); err != nil {
		t.Fatalf("PutSAMLState: %v", err)
	}
	if err := (&redisOIDCStateStore{client: client}).Put(ctx, marker+"-oidc", marker+"-nonce", "/after/"+marker, ttl); err != nil {
		t.Fatalf("oidc Put: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker,
		"session:"+marker, "session:user:"+marker, "login:failed:"+marker,
		"login:failed:global:"+marker, "saml:state:"+marker, "oidc:state:"+marker)
	requireNoTrace(t, client, marker)
}

// newProofRedis returns a dedicated miniredis, a client on it and a lowercase
// marker: the login counters lowercase the username they key on.
func newProofRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, newTestRedisClient(t, mr.Addr()), strings.ToLower(t.Name()) + "-" + hex.EncodeToString(b)
}

// expireJustPast requires the marker in each family, then again one second
// before ttl, then moves miniredis one second past ttl.
func expireJustPast(t *testing.T, mr *miniredis.Miniredis, client redis.UniversalClient, ttl time.Duration, marker string, families ...string) {
	t.Helper()
	requireTrace(t, client, marker, families...)
	mr.FastForward(ttl - time.Second)
	requireTrace(t, client, marker, families...)
	mr.FastForward(2 * time.Second)
}

func requireTrace(t *testing.T, client redis.UniversalClient, marker string, families ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	for _, family := range families {
		if !slices.ContainsFunc(hits, func(hit string) bool { return strings.HasPrefix(hit, family) }) {
			t.Fatalf("no %s* trace of %q; hits=%v", family, marker, hits)
		}
	}
}

func requireNoTrace(t *testing.T, client redis.UniversalClient, marker string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("trace of %q remains after cleanup: %v", marker, hits)
	}
}
