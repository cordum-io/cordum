package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/locks"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// Retention proofs cited by docs/contract/deletion-proofs.md. Each proof writes
// a marker through the production writer with its retention shortened, requires
// tracescan to find it in every key family it names, shows it survives until
// just before the shortened TTL, moves miniredis just past it, runs the cleanup
// the inventory names, then requires no trace in the writer client's keyspace.
// Exports: these writers reach none; they write only the Redis keys named here.

func TestRetentionProofDIC11(t *testing.T) {
	const ttl = time.Minute
	t.Setenv("REDIS_DATA_TTL", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisStoreFromClient(client)
	if err := s.PutContext(ctx, MakeContextKey(marker), []byte("input "+marker)); err != nil {
		t.Fatalf("PutContext: %v", err)
	}
	if err := s.PutResult(ctx, MakeResultKey(marker), []byte("output "+marker)); err != nil {
		t.Fatalf("PutResult: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "ctx:", "res:")
	requireNoTrace(t, client, marker)
}

func TestRetentionProofDIC25(t *testing.T) {
	const ttl = 2 * time.Minute
	t.Setenv("CORDUM_IDEMPOTENCY_TTL", ttl.String())
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewRedisJobStoreFromClient(client)
	tenant := marker + "-tenant"
	if ok, _, err := s.TrySetIdempotencyKeyScoped(ctx, tenant, marker+"-key", marker+"-job"); err != nil || !ok {
		t.Fatalf("TrySetIdempotencyKeyScoped = %v, %v; want claimed", ok, err)
	}
	if err := s.SetIdempotencyKeyScoped(ctx, tenant, marker+"-key2", marker+"-job2"); err != nil {
		t.Fatalf("SetIdempotencyKeyScoped: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "job:idempotency:")
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC28: the entry key expires with the store's entry TTL;
// dlq:index keeps the id until CleanupStaleEntries removes it.
func TestRetentionProofDIC28(t *testing.T) {
	const ttl = time.Hour
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	s := NewDLQStoreFromClient(client, ttl)
	if err := s.Add(ctx, DLQEntry{
		JobID: marker + "-job", Topic: "job." + marker, Status: "failed",
		Reason: "reason " + marker, ReasonCode: marker, LastState: "FAILED", Attempts: 3,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	expireJustPast(t, mr, client, ttl, marker, "dlq:entry:", "dlq:index:")
	requireTrace(t, client, marker, "dlq:index:")
	if removed, err := s.CleanupStaleEntries(ctx); err != nil || removed != 1 {
		t.Fatalf("CleanupStaleEntries = %d, %v; want 1 stale id removed", removed, err)
	}
	requireNoTrace(t, client, marker)
}

// TestRetentionProofDIC49: no Release; the caller's lock TTL is the cleanup.
// Each owner token TryAcquireLock returns is a second marker.
func TestRetentionProofDIC49(t *testing.T) {
	const ttl = 30 * time.Second
	mr, client, marker := newProofRedis(t)
	ctx := context.Background()
	jobs := NewRedisJobStoreFromClient(client)
	keys := []string{"cordum:scheduler:job:" + marker, "cordum:wf:run:lock:" + marker, "cordum:wf:run:admission:" + marker}
	tokens := make([]string, 0, len(keys))
	for _, key := range keys {
		token, err := jobs.TryAcquireLock(ctx, key, ttl)
		if err != nil || token == "" {
			t.Fatalf("TryAcquireLock(%s) = %q, %v; want a token", key, token, err)
		}
		requireTrace(t, client, token, key)
		tokens = append(tokens, token)
	}
	resource := "tenant:" + marker + ":deploy"
	if _, ok, err := locks.NewRedisStoreFromClient(client).Acquire(ctx, resource, marker+"-owner", locks.ModeExclusive, ttl); err != nil || !ok {
		t.Fatalf("locks Acquire = %v, %v; want acquired", ok, err)
	}
	expireJustPast(t, mr, client, ttl, marker,
		"cordum:scheduler:job:", "cordum:wf:run:lock:", "cordum:wf:run:admission:", "lock:tenant:")
	requireNoTrace(t, client, marker)
	for _, token := range tokens {
		requireNoTrace(t, client, token)
	}
}

func newProofRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client, string) {
	t.Helper()
	mr := miniredis.RunT(t)
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("marker: %v", err)
	}
	return mr, testredis.NewClient(t, mr.Addr()), t.Name() + "-" + hex.EncodeToString(b)
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
	if len(hits) == 0 {
		t.Fatalf("no trace of %q", marker)
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
