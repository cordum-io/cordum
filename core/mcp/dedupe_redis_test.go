package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// newMiniRedisDedupeBackend returns a fresh miniredis + go-redis client
// for the dedupe store tests. The cleanup closes the client and shuts
// down miniredis so -count=N runs see isolated state per iteration.
func newMiniRedisDedupeBackend(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })
	return client, mr
}

// TestRedisDedupeStore_CrossProcessCollapses is the headline cross-
// process dedupe contract. Two separate RedisDedupeStore instances
// pointing at the same Redis backend AND using the same semantic key
// MUST produce exactly one winner: the second caller observes the
// existing entry via SET NX failure and decodes the cached record.
//
// In production this is the "two gateway instances behind a load
// balancer, each receives one half of a retried MCP tool call" case
// that the in-process sync.Map cannot collapse.
func TestRedisDedupeStore_CrossProcessCollapses(t *testing.T) {
	t.Parallel()
	client, _ := newMiniRedisDedupeBackend(t)

	// Two independent store instances, single backend.
	storeA := NewRedisDedupeStore(client)
	storeB := NewRedisDedupeStore(client)

	winnerVal := &redisDedupeRecord{State: redisDedupeStatePending}
	actualA, loadedA := storeA.LoadOrStore("k.cross", winnerVal)
	if loadedA {
		t.Fatalf("first cross-process call reports loaded=true on empty backend; want false (must win the slot)")
	}
	if _, ok := actualA.(*redisDedupeRecord); !ok || actualA.(*redisDedupeRecord).State != redisDedupeStatePending {
		t.Fatalf("first call returned %+v; want pending wire record", actualA)
	}

	// Winner completes the work and publishes the completed record.
	completed := &redisDedupeRecord{
		State: redisDedupeStateCompleted,
		Result: &redisDedupeResultMetadata{
			IsError:      false,
			ContentCount: 1,
			ResultSHA256: strings.Repeat("a", sha256.Size*2),
		},
	}
	storeA.Store("k.cross", completed)

	// Second-instance caller hits SET NX, falls through to GET, decodes
	// the completed wire record. loaded MUST be true so dedupeBegin
	// short-circuits with the cached payload instead of running upstream.
	loserVal := &redisDedupeRecord{State: redisDedupeStatePending}
	actualB, loadedB := storeB.LoadOrStore("k.cross", loserVal)
	if !loadedB {
		t.Fatalf("second cross-process call reports loaded=false; want true (must observe winner's record)")
	}
	gotB, ok := actualB.(*redisDedupeRecord)
	if !ok {
		t.Fatalf("second call returned non-record type %T", actualB)
	}
	if gotB.State != redisDedupeStateCompleted {
		t.Fatalf("second call observed state=%q; want %q", gotB.State, redisDedupeStateCompleted)
	}
	if gotB.Result == nil || gotB.Result.ContentCount != 1 || gotB.Result.ResultSHA256 != strings.Repeat("a", sha256.Size*2) {
		t.Fatalf("second call metadata mismatch: got %#v", gotB.Result)
	}
}

func TestRedisDedupeStore_CompletedRecordDoesNotPersistRawResultFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client, _ := newMiniRedisDedupeBackend(t)
	storeA := NewRedisDedupeStore(client)
	storeB := NewRedisDedupeStore(client)
	canary := "redis-wire-canary-20260519"

	storeA.Store("k.no-raw-result", &redisDedupeRecord{
		State: redisDedupeStateCompleted,
		Result: &redisDedupeResultMetadata{
			IsError:              true,
			ContentCount:         2,
			HasStructuredContent: true,
			ResultSHA256:         strings.Repeat("b", sha256.Size*2),
		},
	})

	payload := singleRedisDedupePayload(t, ctx, client)
	for i, forbidden := range []string{canary, "raw-data-" + canary, "\"content\":", "\"text\":", "\"data\":", "structuredContent", "payload"} {
		if strings.Contains(payload, forbidden) {
			t.Errorf("Redis completed dedupe record contains raw result field/fragment at index %d", i)
		}
	}
	actual, loaded := storeB.LoadOrStore("k.no-raw-result", &redisDedupeRecord{State: redisDedupeStatePending})
	if !loaded {
		t.Fatalf("second Redis store LoadOrStore loaded=false; want completed metadata record")
	}
	rec, ok := actual.(*redisDedupeRecord)
	if !ok || rec.State != redisDedupeStateCompleted || rec.Result == nil {
		t.Fatalf("second Redis store actual = %#v; want completed redisDedupeRecord", actual)
	}
	if !rec.Result.IsError || rec.Result.ContentCount != 2 || !rec.Result.HasStructuredContent {
		t.Fatalf("second Redis store metadata = %#v; want completed safe metadata", rec.Result)
	}
}

// TestRedisDedupeStore_KeyPrefix asserts every Redis key the store
// writes is namespaced under `mcp:dedupe:` so an operator scanning
// Redis with `KEYS mcp:dedupe:*` enumerates the full dedupe set and
// other subsystems can't accidentally collide on a raw user-supplied
// key. Empty / unprefixed keys are an integrity bug.
func TestRedisDedupeStore_KeyPrefix(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	store.LoadOrStore("k.prefix", &redisDedupeRecord{State: redisDedupeStatePending})

	if !mr.Exists(MCPDedupeKeyPrefix + "k.prefix") {
		t.Fatalf("expected Redis key %q to exist; got miss", MCPDedupeKeyPrefix+"k.prefix")
	}
	if MCPDedupeKeyPrefix != "mcp:dedupe:" {
		t.Fatalf("MCPDedupeKeyPrefix = %q; want %q (contract: subsystem-scoped namespace)", MCPDedupeKeyPrefix, "mcp:dedupe:")
	}
}

// TestRedisDedupeStore_TTLExpires asserts every write applies the
// MCPDedupeTTL (10 min) so a stuck pending record cannot livelock
// waiters forever — the TTL is the deadline-breaker mentioned in step 6
// for the Redis polling path. miniredis FastForward fast-forwards the
// internal clock past the TTL so the key gets garbage-collected.
func TestRedisDedupeStore_TTLExpires(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	store.LoadOrStore("k.ttl", &redisDedupeRecord{State: redisDedupeStatePending})

	if !mr.Exists(MCPDedupeKeyPrefix + "k.ttl") {
		t.Fatalf("pre-fastforward: expected key to exist")
	}
	// Verify the TTL was applied (not -1 = no expiry).
	gotTTL := mr.TTL(MCPDedupeKeyPrefix + "k.ttl")
	if gotTTL <= 0 {
		t.Fatalf("TTL on Redis key = %v; want >0 (MCPDedupeTTL must be applied on every SET)", gotTTL)
	}
	if gotTTL > MCPDedupeTTL {
		t.Fatalf("TTL on Redis key = %v; want <= MCPDedupeTTL=%v", gotTTL, MCPDedupeTTL)
	}

	mr.FastForward(MCPDedupeTTL + time.Second)
	if mr.Exists(MCPDedupeKeyPrefix + "k.ttl") {
		t.Fatalf("post-fastforward: key still exists after MCPDedupeTTL+1s; want garbage-collected")
	}
}

// TestRedisDedupeStore_StorePreservesTTL asserts Store (the publish-
// completed-record path) ALSO applies MCPDedupeTTL — not just the
// initial LoadOrStore. Without the TTL on Store, a completed record
// would either inherit the original TTL (correct only if Redis is
// configured to retain TTL on SET, which depends on Redis version) or
// live forever (depending on go-redis defaults), risking permanent
// stale-cache entries.
func TestRedisDedupeStore_StorePreservesTTL(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	store.LoadOrStore("k.store-ttl", &redisDedupeRecord{State: redisDedupeStatePending})
	store.Store("k.store-ttl", &redisDedupeRecord{
		State: redisDedupeStateCompleted,
		Result: &redisDedupeResultMetadata{
			ContentCount: 1,
			ResultSHA256: strings.Repeat("c", sha256.Size*2),
		},
	})

	gotTTL := mr.TTL(MCPDedupeKeyPrefix + "k.store-ttl")
	if gotTTL <= 0 {
		t.Fatalf("TTL on Redis key after Store = %v; want >0 (Store must reapply MCPDedupeTTL)", gotTTL)
	}
	if gotTTL > MCPDedupeTTL {
		t.Fatalf("TTL on Redis key after Store = %v; want <= MCPDedupeTTL=%v", gotTTL, MCPDedupeTTL)
	}
}

// TestRedisDedupeStore_Delete asserts Delete removes the key so the
// next retry fires a fresh upstream call — mirrors the in-process
// dedupeFinish error-path contract.
func TestRedisDedupeStore_Delete(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	store.LoadOrStore("k.del", &redisDedupeRecord{State: redisDedupeStatePending})
	if !mr.Exists(MCPDedupeKeyPrefix + "k.del") {
		t.Fatalf("pre-delete: key missing")
	}
	store.Delete("k.del")
	if mr.Exists(MCPDedupeKeyPrefix + "k.del") {
		t.Fatalf("post-delete: key still exists; want removed")
	}
}

// TestRedisDedupeStore_FailSoftOnClosedClient is the fail-soft taskRail
// #3 contract: a Redis outage MUST NOT block MCP gate traffic. When
// Redis commands error, the store routes through its internal in-
// process fallback so callers see a normal LoadOrStore/Store/Delete
// pattern rather than a hung gate.
//
// We exercise the contract by closing the go-redis client before any
// dedupe operation; the underlying connection pool will then return an
// error on every command. The store MUST NOT panic, MUST NOT block,
// and MUST surface the in-process result.
func TestRedisDedupeStore_FailSoftOnClosedClient(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	// Close the client AND the miniredis so every Redis command errors.
	_ = client.Close()
	mr.Close()

	done := make(chan struct{})
	go func() {
		// LoadOrStore on a closed client must not block forever.
		actual, loaded := store.LoadOrStore("k.failsoft", &redisDedupeRecord{State: redisDedupeStatePending})
		if loaded {
			t.Errorf("fail-soft LoadOrStore on closed client reports loaded=true on empty fallback; want false")
		}
		if actual == nil {
			t.Errorf("fail-soft LoadOrStore returned nil actual; want the supplied value")
		}
		// Store + Delete must also not panic.
		store.Store("k.failsoft", &redisDedupeRecord{State: redisDedupeStateCompleted})
		store.Delete("k.failsoft")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("fail-soft Redis operations blocked >3s on closed client; gate would be blocked in prod")
	}
}

func TestRedisDedupeStore_FailSoftFallbackSharedAcrossCalls(t *testing.T) {
	t.Parallel()
	client, mr := newMiniRedisDedupeBackend(t)
	store := NewRedisDedupeStore(client)
	// Close both endpoints so every Redis command takes the fail-soft path.
	_ = client.Close()
	mr.Close()

	pending := &redisDedupeRecord{State: redisDedupeStatePending}
	actual, loaded := store.LoadOrStore("k.failsoft-shared", pending)
	if loaded {
		t.Fatalf("first fail-soft LoadOrStore loaded=true; want false from empty fallback")
	}
	if actual != pending {
		t.Fatalf("first fail-soft LoadOrStore actual=%#v; want supplied pending record %#v", actual, pending)
	}

	loser := &redisDedupeRecord{State: redisDedupeStatePending}
	actual, loaded = store.LoadOrStore("k.failsoft-shared", loser)
	if !loaded {
		t.Fatalf("second fail-soft LoadOrStore loaded=false; want shared fallback to collapse duplicate call")
	}
	if actual != pending {
		t.Fatalf("second fail-soft LoadOrStore actual=%#v; want original fallback record %#v", actual, pending)
	}

	completed := &redisDedupeRecord{
		State: redisDedupeStateCompleted,
		Result: &redisDedupeResultMetadata{
			ContentCount: 1,
			ResultSHA256: strings.Repeat("d", sha256.Size*2),
		},
	}
	store.Store("k.failsoft-shared", completed)
	actual, loaded = store.LoadOrStore("k.failsoft-shared", loser)
	if !loaded {
		t.Fatalf("post-Store fail-soft LoadOrStore loaded=false; want completed fallback record")
	}
	if actual != completed {
		t.Fatalf("post-Store fail-soft LoadOrStore actual=%#v; want completed record %#v", actual, completed)
	}
}

// TestRedisDedupe_LoadOrStoreNoFalseWinnerOnNXMissThenNil is the regression
// test for the PR #276 audit finding (RACE CRITICAL). The pre-fix sequence
// is:
//
//  1. SetNX returns stored=false (an existing pending/completed entry).
//  2. Get returns redis.Nil because another gateway process called Delete
//     between our SetNX-miss and our Get (a benign race: the prior winner
//     finished + cleaned up its slot).
//  3. The Get error causes the store to fall back to the in-process map,
//     which is per-instance and empty here, so it reports the caller as the
//     local winner (loaded=false).
//
// Two gateway instances both hitting this race both win locally and both
// fire the upstream MCP tool call → duplicate side effects (double policy
// evaluation, double audit, etc).
//
// The fix retries SETNX→GET internally to resolve the benign race, and if
// retries exhaust it returns a pending wire record with loaded=true so the
// caller polls via waitForRedisDedupe instead of firing upstream. This
// test injects redis.Nil on every Get to force the race-exhausted path and
// asserts the caller-side outcome is "retry/wait" (loaded=true with a
// pending record), NOT "local-win" (loaded=false).
func TestRedisDedupe_LoadOrStoreNoFalseWinnerOnNXMissThenNil(t *testing.T) {
	t.Parallel()
	plainClient, mr := newMiniRedisDedupeBackend(t)
	// Pre-populate a record so SetNX returns stored=false on first attempt.
	storeA := NewRedisDedupeStore(plainClient)
	if _, loaded := storeA.LoadOrStore("k.nx-then-nil", &redisDedupeRecord{State: redisDedupeStatePending}); loaded {
		t.Fatalf("preload: first LoadOrStore loaded=true on empty backend; want false")
	}

	hookedClient := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() { _ = hookedClient.Close() })
	getNilHook := &injectGetNilHook{}
	hookedClient.AddHook(getNilHook)
	storeB := NewRedisDedupeStore(hookedClient)

	// SetNX from storeB will hit the pre-populated key and return false,
	// then Get is intercepted by the hook and returns redis.Nil. The fix
	// must NOT promote storeB to local-winner; it must signal retry/wait.
	loserSupplied := &redisDedupeRecord{State: redisDedupeStatePending}
	actual, loaded := storeB.LoadOrStore("k.nx-then-nil", loserSupplied)

	if !loaded {
		t.Fatalf("LoadOrStore under NX-miss-then-Nil reported loaded=false (false-winner — DUPLICATE upstream call risk); want loaded=true so caller enters waitForRedisDedupe polling path")
	}
	rec, ok := actual.(*redisDedupeRecord)
	if !ok {
		t.Fatalf("LoadOrStore returned %T; want *redisDedupeRecord so caller's type switch routes to polling path", actual)
	}
	if rec.State != redisDedupeStatePending {
		t.Fatalf("LoadOrStore returned state=%q; want %q so caller enters waitForRedisDedupe polling instead of short-circuiting on a fake completed record", rec.State, redisDedupeStatePending)
	}
	if atomic.LoadInt32(&getNilHook.gets) == 0 {
		t.Fatal("test never injected redis.Nil on Get; LoadOrStore is not exercising the NX-miss-then-Get path")
	}
}

// TestRedisDedupe_LoadOrStoreResolvesTransientNXRace asserts the retry path
// succeeds when the race is benign: SetNX→Get fails once, then on retry the
// key is gone so SetNX wins and the caller becomes the legitimate winner.
// This proves the fix doesn't degrade the happy path of "prior winner
// finished+cleared, we should claim the slot".
func TestRedisDedupe_LoadOrStoreResolvesTransientNXRace(t *testing.T) {
	t.Parallel()
	_, mr := newMiniRedisDedupeBackend(t)
	// Pre-populate so first SetNX returns false; the hook will Delete and
	// inject Nil on the first Get to simulate the cross-process race.
	preloaded, err := json.Marshal(&redisDedupeRecord{State: redisDedupeStatePending})
	if err != nil {
		t.Fatalf("marshal pre-load record: %v", err)
	}
	if err := mr.Set(MCPDedupeKeyPrefix+"k.transient-race", string(preloaded)); err != nil {
		t.Fatalf("seed mr.Set: %v", err)
	}
	mr.SetTTL(MCPDedupeKeyPrefix+"k.transient-race", MCPDedupeTTL)

	hookedClient := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 4})
	t.Cleanup(func() { _ = hookedClient.Close() })

	var oneShot atomic.Bool
	hookedClient.AddHook(&interceptHook{
		onProcess: func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error {
			if cmd.Name() == "get" && !oneShot.Swap(true) {
				mr.Del(MCPDedupeKeyPrefix + "k.transient-race")
				cmd.SetErr(redis.Nil)
				return redis.Nil
			}
			return next(ctx, cmd)
		},
	})

	store := NewRedisDedupeStore(hookedClient)
	supplied := &redisDedupeRecord{State: redisDedupeStatePending}
	actual, loaded := store.LoadOrStore("k.transient-race", supplied)
	if loaded {
		t.Fatal("transient race: LoadOrStore loaded=true; want false (retry SETNX should win after race resolved)")
	}
	if actual != supplied {
		t.Fatalf("transient race: LoadOrStore actual=%#v; want supplied value (winner path)", actual)
	}
	// The winner must persist their record in Redis via SETNX retry, not
	// merely in the per-instance fallback. Cross-process dedupe semantics
	// require the next gateway instance to observe the winner via SETNX
	// returning stored=false on the same key.
	if !mr.Exists(MCPDedupeKeyPrefix + "k.transient-race") {
		t.Fatal("transient race: Redis key missing after win; the retry path landed on local fallback rather than re-attempting SETNX (cross-process collapse broken)")
	}
}

// injectGetNilHook returns redis.Nil for every Get command. Used to force
// the LoadOrStore NX-miss-then-Get-Nil race path.
type injectGetNilHook struct {
	gets int32
}

func (h *injectGetNilHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *injectGetNilHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *injectGetNilHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "get" {
			atomic.AddInt32(&h.gets, 1)
			cmd.SetErr(redis.Nil)
			return redis.Nil
		}
		return next(ctx, cmd)
	}
}

// interceptHook is a programmable hook for per-command behavior tweaks
// (e.g. delete-then-Nil on first Get, normal afterward).
type interceptHook struct {
	onProcess func(ctx context.Context, cmd redis.Cmder, next redis.ProcessHook) error
}

func (h *interceptHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *interceptHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *interceptHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		return h.onProcess(ctx, cmd, next)
	}
}
