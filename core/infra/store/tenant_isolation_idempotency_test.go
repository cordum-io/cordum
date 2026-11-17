package store

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

const isolationIdemKey = "shared-idem-key"

func newIsolationJobStore(t *testing.T) (*RedisJobStore, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	s, err := NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("new job store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, srv
}

// seedIsolationJob writes the tenant-owned job that the other tenant must
// never receive or change.
func seedIsolationJob(t *testing.T, s *RedisJobStore, jobID, tenant string) {
	t.Helper()
	ctx := context.Background()
	if err := s.SetTenant(ctx, jobID, tenant); err != nil {
		t.Fatalf("seed tenant for %s: %v", jobID, err)
	}
	if err := s.SetResultPtr(ctx, jobID, "redis://res:"+jobID); err != nil {
		t.Fatalf("seed result for %s: %v", jobID, err)
	}
}

func assertIsolationJobUnchanged(t *testing.T, s *RedisJobStore, jobID, tenant string) {
	t.Helper()
	ctx := context.Background()
	if got, err := s.GetTenant(ctx, jobID); err != nil || got != tenant {
		t.Fatalf("job %s tenant = %q, %v; want %q", jobID, got, err, tenant)
	}
	if got, err := s.GetResultPtr(ctx, jobID); err != nil || got != "redis://res:"+jobID {
		t.Fatalf("job %s result = %q, %v; want unchanged", jobID, got, err)
	}
}

func assertIsolationMapping(t *testing.T, srv *miniredis.Miniredis, key, want string) {
	t.Helper()
	if got, err := srv.Get(key); err != nil || got != want {
		t.Fatalf("%s = %q, %v; want %q", key, got, err, want)
	}
}

func TestTenantIsolationJobIdempotencyScopedKey(t *testing.T) {
	s, srv := newIsolationJobStore(t)
	ctx := context.Background()
	const keyA = "job:idempotency:tenant-a:" + isolationIdemKey
	seedIsolationJob(t, s, "job-a", "tenant-a")
	if ok, got, err := s.TrySetIdempotencyKeyScoped(ctx, "tenant-a", isolationIdemKey, "job-a"); err != nil || !ok || got != "job-a" {
		t.Fatalf("tenant-a reserve = %v, %q, %v; want true, job-a", ok, got, err)
	}

	ok, got, err := s.TrySetIdempotencyKeyScoped(ctx, "tenant-b", isolationIdemKey, "job-b")
	if err != nil || !ok || got != "job-b" {
		t.Fatalf("tenant-b reserve with tenant-a's key = %v, %q, %v; want its own new job-b", ok, got, err)
	}
	if got, err := s.GetJobByIdempotencyKeyScoped(ctx, "tenant-b", isolationIdemKey); err != nil || got != "job-b" {
		t.Fatalf("tenant-b lookup = %q, %v; want job-b", got, err)
	}

	// A's mapping was live the whole time, so B's new job is not an expiry artifact.
	assertIsolationMapping(t, srv, keyA, "job-a")
	if ttl := srv.TTL(keyA); ttl <= 0 {
		t.Fatalf("tenant-a mapping TTL = %v; want a live key", ttl)
	}
	if got, err := s.GetJobByIdempotencyKeyScoped(ctx, "tenant-a", isolationIdemKey); err != nil || got != "job-a" {
		t.Fatalf("tenant-a lookup = %q, %v; want job-a", got, err)
	}
	assertIsolationJobUnchanged(t, s, "job-a", "tenant-a")

	// Dedupe still holds inside tenant B.
	if ok, got, err := s.TrySetIdempotencyKeyScoped(ctx, "tenant-b", isolationIdemKey, "job-b-retry"); err != nil || ok || got != "job-b" {
		t.Fatalf("tenant-b retry = %v, %q, %v; want false, job-b", ok, got, err)
	}
}

func TestTenantIsolationJobIdempotencyLegacyFallback(t *testing.T) {
	s, srv := newIsolationJobStore(t)
	ctx := context.Background()
	const legacyKey = "job:idempotency:" + isolationIdemKey
	seedIsolationJob(t, s, "job-a-legacy", "tenant-a")
	if err := srv.Set(legacyKey, "job-a-legacy"); err != nil {
		t.Fatalf("seed legacy key: %v", err)
	}

	got, err := s.GetJobByIdempotencyKeyScoped(ctx, "tenant-b", isolationIdemKey)
	if !errors.Is(err, redis.Nil) || got != "" {
		t.Fatalf("tenant-b lookup of tenant-a's legacy key = %q, %v; want redis.Nil", got, err)
	}
	if srv.Exists("job:idempotency:tenant-b:" + isolationIdemKey) {
		t.Fatal("tenant-b lookup adopted tenant-a's legacy key into tenant-b's scope")
	}
	ok, got, err := s.TrySetIdempotencyKeyScoped(ctx, "tenant-b", isolationIdemKey, "job-b")
	if err != nil || !ok || got != "job-b" {
		t.Fatalf("tenant-b reserve with tenant-a's legacy key = %v, %q, %v; want its own new job-b", ok, got, err)
	}
	assertIsolationMapping(t, srv, legacyKey, "job-a-legacy")
	assertIsolationJobUnchanged(t, s, "job-a-legacy", "tenant-a")

	// The fallback is live for its owner, so B's miss comes from the tenant check.
	if got, err := s.GetJobByIdempotencyKeyScoped(ctx, "tenant-a", isolationIdemKey); err != nil || got != "job-a-legacy" {
		t.Fatalf("tenant-a lookup = %q, %v; want its legacy job", got, err)
	}
	if ok, got, err := s.TrySetIdempotencyKeyScoped(ctx, "tenant-a", isolationIdemKey, "job-a-new"); err != nil || ok || got != "job-a-legacy" {
		t.Fatalf("tenant-a reserve = %v, %q, %v; want false, job-a-legacy", ok, got, err)
	}
}
