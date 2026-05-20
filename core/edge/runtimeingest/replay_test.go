package runtimeingest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newReplayWindowTestClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})
	return client, mr
}

func mustReplayWindowKey(t *testing.T, window *ReplayWindow, tenantID, collectorID string) string {
	t.Helper()
	key, _, err := window.keyAndValue(tenantID, collectorID, "nonce-key-probe")
	if err != nil {
		t.Fatalf("keyAndValue: %v", err)
	}
	return key
}

func TestReplayWindow_FirstNonceAccepted(t *testing.T) {
	ctx := context.Background()
	client, mr := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)

	accepted, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001")
	if err != nil {
		t.Fatalf("Reserve first nonce: %v", err)
	}
	if !accepted {
		t.Fatal("Reserve first nonce accepted=false; want true")
	}

	key := mustReplayWindowKey(t, window, "tenant-a", "collector-x")
	if !mr.Exists(key) {
		t.Fatalf("expected replay key %q to exist", key)
	}
	ttl := mr.TTL(key)
	if ttl <= 0 || ttl > ReplayWindowTTL {
		t.Fatalf("TTL(%s) = %v; want >0 and <= %v", key, ttl, ReplayWindowTTL)
	}
}

func TestReplayWindow_DuplicateNonceRejected(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)

	first, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001")
	if err != nil || !first {
		t.Fatalf("first Reserve = (%v, %v); want (true, nil)", first, err)
	}
	second, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001")
	if err != nil {
		t.Fatalf("duplicate Reserve: %v", err)
	}
	if second {
		t.Fatal("duplicate Reserve accepted=true; want false replay result")
	}
}

func TestReplayWindow_ReleaseAllowsRetry(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)
	nonce := "nonce-release-000001"

	first, err := window.Reserve(ctx, "tenant-a", "collector-x", nonce)
	if err != nil || !first {
		t.Fatalf("first Reserve = (%v, %v); want (true, nil)", first, err)
	}
	if err := window.Release(ctx, "tenant-a", "collector-x", nonce); err != nil {
		t.Fatalf("Release: %v", err)
	}
	retry, err := window.Reserve(ctx, "tenant-a", "collector-x", nonce)
	if err != nil || !retry {
		t.Fatalf("retry Reserve after Release = (%v, %v); want (true, nil)", retry, err)
	}
}

func TestReplayWindow_DoesNotPersistRawNonce(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)
	nonce := "nonce-raw-canary-0001"

	accepted, err := window.Reserve(ctx, "tenant-a", "collector-x", nonce)
	if err != nil || !accepted {
		t.Fatalf("Reserve = (%v, %v); want (true, nil)", accepted, err)
	}
	key := mustReplayWindowKey(t, window, "tenant-a", "collector-x")
	members, err := client.SMembers(ctx, key).Result()
	if err != nil {
		t.Fatalf("SMembers replay key: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("members len = %d; want 1", len(members))
	}
	if members[0] == nonce {
		t.Fatal("replay window persisted raw nonce; want hashed value only")
	}
	if want := replayNonceDigest(nonce); members[0] != want {
		t.Fatalf("stored member = %q; want nonce digest %q", members[0], want)
	}
}

func TestReplayWindow_CrossCollectorIsolated(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)

	if ok, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001"); err != nil || !ok {
		t.Fatalf("collector-x Reserve = (%v, %v); want (true, nil)", ok, err)
	}
	if ok, err := window.Reserve(ctx, "tenant-a", "collector-y", "nonce-000000000001"); err != nil || !ok {
		t.Fatalf("collector-y Reserve = (%v, %v); want (true, nil)", ok, err)
	}
}

func TestReplayWindow_CrossTenantIsolated(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)

	if ok, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001"); err != nil || !ok {
		t.Fatalf("tenant-a Reserve = (%v, %v); want (true, nil)", ok, err)
	}
	if ok, err := window.Reserve(ctx, "tenant-b", "collector-x", "nonce-000000000001"); err != nil || !ok {
		t.Fatalf("tenant-b Reserve = (%v, %v); want (true, nil)", ok, err)
	}
}

func TestReplayWindow_TenantKeyDelimCollisionFree(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)
	nonce := "nonce-delim-collision"

	first, err := window.Reserve(ctx, "a:b", "c", nonce)
	if err != nil || !first {
		t.Fatalf("first Reserve = (%v, %v); want (true, nil)", first, err)
	}
	second, err := window.Reserve(ctx, "a", "b:c", nonce)
	if err != nil || !second {
		t.Fatalf("second Reserve with distinct tenant/collector tuple = (%v, %v); want (true, nil)", second, err)
	}
}

func TestReplayWindow_CapExhaustionRefuses(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, time.Hour, 2)

	for _, nonce := range []string{"nonce-000000000001", "nonce-000000000002"} {
		if ok, err := window.Reserve(ctx, "tenant-a", "collector-x", nonce); err != nil || !ok {
			t.Fatalf("Reserve(%s) = (%v, %v); want (true, nil)", nonce, ok, err)
		}
	}
	ok, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000003")
	if !errors.Is(err, ErrReplayWindowFull) {
		t.Fatalf("Reserve over cap error = %v; want ErrReplayWindowFull", err)
	}
	if ok {
		t.Fatal("Reserve over cap accepted=true; want false")
	}
}

func TestReplayWindow_CrossInstanceShared(t *testing.T) {
	ctx := context.Background()
	client, _ := newReplayWindowTestClient(t)
	firstWindow := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)
	secondWindow := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)

	first, err := firstWindow.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001")
	if err != nil || !first {
		t.Fatalf("first instance Reserve = (%v, %v); want (true, nil)", first, err)
	}
	second, err := secondWindow.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001")
	if err != nil {
		t.Fatalf("second instance Reserve: %v", err)
	}
	if second {
		t.Fatal("second instance accepted duplicate nonce; want replay=false")
	}
}

func TestReplayWindow_TTLExpiryAcceptsAgain(t *testing.T) {
	ctx := context.Background()
	client, mr := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, time.Hour, MaxReplayWindowCardinality)

	if ok, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001"); err != nil || !ok {
		t.Fatalf("first Reserve = (%v, %v); want (true, nil)", ok, err)
	}
	mr.FastForward(2 * time.Hour)
	if ok, err := window.Reserve(ctx, "tenant-a", "collector-x", "nonce-000000000001"); err != nil || !ok {
		t.Fatalf("post-expiry Reserve = (%v, %v); want (true, nil)", ok, err)
	}
}

// TestReplayWindow_ReserveAtomicUnderConcurrency races N goroutines (N >
// maxCard) at the same tenant/collector key with distinct nonces and asserts
// the exact accepted/full split. The pre-fix Reserve sequence (SCARD →
// SISMEMBER → SADD → EXPIRE on the round-tripped go-redis client) is a TOCTOU:
// every goroutine observes count < maxCard via SCARD before any one of them
// runs SADD, so the cap is approximate rather than enforced. A single-Lua-EVAL
// fix makes SCARD/SISMEMBER/SADD/EXPIRE one atomic step: exactly maxCard new
// nonces are accepted, the rest fail with ErrReplayWindowFull, and the key has
// a TTL.
func TestReplayWindow_ReserveAtomicUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	client, mr := newReplayWindowTestClient(t)
	const maxCard = int64(50)
	const goroutines = 100
	window := NewReplayWindow(client, ReplayWindowTTL, maxCard)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	ready := make(chan struct{})
	type reserveOutcome struct {
		accepted bool
		err      error
	}
	outcomes := make(chan reserveOutcome, goroutines)
	for i := range goroutines {
		nonce := fmt.Sprintf("nonce-race-%06d", i)
		go func(n string) {
			defer wg.Done()
			<-ready
			accepted, err := window.Reserve(ctx, "tenant-a", "collector-x", n)
			outcomes <- reserveOutcome{accepted: accepted, err: err}
		}(nonce)
	}
	// Release all goroutines simultaneously to widen the race window.
	close(ready)
	wg.Wait()
	close(outcomes)

	var accepted, full, replayed int
	var unexpected []error
	for outcome := range outcomes {
		switch {
		case outcome.accepted && outcome.err == nil:
			accepted++
		case !outcome.accepted && errors.Is(outcome.err, ErrReplayWindowFull):
			full++
		case !outcome.accepted && outcome.err == nil:
			replayed++
		default:
			unexpected = append(unexpected, outcome.err)
		}
	}
	if len(unexpected) > 0 {
		t.Fatalf("Reserve returned %d unexpected errors, first=%v", len(unexpected), unexpected[0])
	}
	if accepted != int(maxCard) {
		t.Fatalf("accepted reserves = %d; want %d", accepted, maxCard)
	}
	if full != goroutines-int(maxCard) {
		t.Fatalf("ErrReplayWindowFull reserves = %d; want %d", full, goroutines-int(maxCard))
	}
	if replayed != 0 {
		t.Fatalf("replayed reserves = %d; want 0 for unique concurrent nonces", replayed)
	}

	key := mustReplayWindowKey(t, window, "tenant-a", "collector-x")
	size, err := client.SCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("post-race SCard: %v", err)
	}
	if size != maxCard {
		t.Fatalf("post-race SCard = %d; want exactly maxCard=%d", size, maxCard)
	}
	ttl := mr.TTL(key)
	if ttl <= 0 {
		t.Fatalf("post-race TTL(%s) = %v; want >0", key, ttl)
	}
	if ttl > ReplayWindowTTL {
		t.Fatalf("post-race TTL(%s) = %v; want <= %v", key, ttl, ReplayWindowTTL)
	}
}

// TestReplayWindow_ReserveAlwaysAppliesTTL is the no-orphan regression guard
// for DoD-2. With the pre-fix code, an Expire failure between SADD and EXPIRE
// (network blip, command timeout, miniredis close mid-flight) leaves a new
// member in a key with no TTL — the key never expires and the set grows
// unbounded across reboots. The Lua EVAL fix makes SADD and EXPIRE one
// atomic operation: either both happen or neither does. We assert the
// invariant by Reserving many distinct nonces and verifying the key always
// has a positive TTL.
func TestReplayWindow_ReserveAlwaysAppliesTTL(t *testing.T) {
	ctx := context.Background()
	client, mr := newReplayWindowTestClient(t)
	window := NewReplayWindow(client, ReplayWindowTTL, MaxReplayWindowCardinality)
	key := mustReplayWindowKey(t, window, "tenant-a", "collector-x")

	for i := range 25 {
		nonce := fmt.Sprintf("nonce-ttl-%06d", i)
		ok, err := window.Reserve(ctx, "tenant-a", "collector-x", nonce)
		if err != nil || !ok {
			t.Fatalf("Reserve(%s) = (%v, %v); want (true, nil)", nonce, ok, err)
		}
		ttl := mr.TTL(key)
		if ttl <= 0 {
			t.Fatalf("after Reserve(%s): TTL(%s) = %v; want >0 (no orphan SADD without EXPIRE)", nonce, key, ttl)
		}
		if ttl > ReplayWindowTTL {
			t.Fatalf("after Reserve(%s): TTL(%s) = %v; want <= %v", nonce, key, ttl, ReplayWindowTTL)
		}
	}
}

// TestReplayWindow_ReserveAtomicNoOrphanOnExpireFailure is the DoD-2
// regression test for the "SADD-succeeded-but-EXPIRE-failed" orphan-member
// scenario the PR #276 audit flagged. We inject a fault on the Redis
// EXPIRE command:
//
//   - Pre-Lua-fix code: SADD succeeds (network call 1), then EXPIRE fails
//     (network call 2) → 1 orphan member with no TTL. The set grows forever.
//   - Post-Lua-fix code: SADD+EXPIRE happen inside the same EVAL call.
//     A network-level "expire" command is never sent, so the hook never
//     fires and the script runs atomically (members > 0 ↔ TTL > 0).
//
// Invariant under test: if the key has members, it has a TTL.
func TestReplayWindow_ReserveAtomicNoOrphanOnExpireFailure(t *testing.T) {
	ctx := context.Background()
	plainClient, mr := newReplayWindowTestClient(t)
	hookedClient := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() { _ = hookedClient.Close() })
	hookedClient.AddHook(&failExpireHook{err: errors.New("injected expire failure")})

	window := NewReplayWindow(hookedClient, ReplayWindowTTL, MaxReplayWindowCardinality)
	_, _ = window.Reserve(ctx, "tenant-a", "collector-x", "nonce-orphan-test")
	// Reserve may return an error (pre-fix) or succeed (post-fix); either
	// outcome is acceptable. The invariant is the post-state, not the
	// return value.

	key := mustReplayWindowKey(t, window, "tenant-a", "collector-x")
	members, err := plainClient.SCard(ctx, key).Result()
	if err != nil {
		t.Fatalf("post-failure SCard via plain client: %v", err)
	}
	ttl := mr.TTL(key)
	if members > 0 && ttl <= 0 {
		t.Fatalf("orphan detected: SCard=%d members but TTL=%v (no expiry); SADD+EXPIRE atomicity violated", members, ttl)
	}
}

// failExpireHook returns an injected error on the Redis EXPIRE command;
// all other commands pass through. Used to simulate the network failure
// mode that exposed the pre-Lua-fix orphan-member bug.
type failExpireHook struct {
	err error
}

func (h *failExpireHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *failExpireHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *failExpireHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "expire" {
			cmd.SetErr(h.err)
			return h.err
		}
		return next(ctx, cmd)
	}
}
