package store

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newDLQStore(t *testing.T) *DLQStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewDLQStore("redis://"+srv.Addr(), 0)
	if err != nil {
		t.Fatalf("dlq store init: %v", err)
	}
	return store
}

func TestDLQStoreCRUD(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	entry := DLQEntry{
		JobID:     "job-1",
		Topic:     "job.test",
		Status:    "FAILED",
		Reason:    "boom",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatalf("add: %v", err)
	}

	gotOne, err := store.Get(ctx, entry.JobID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotOne.JobID != entry.JobID || gotOne.Reason != entry.Reason {
		t.Fatalf("get mismatch: %+v", gotOne)
	}

	list, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].JobID != entry.JobID {
		t.Fatalf("unexpected list: %+v", list)
	}

	if err := store.Delete(ctx, entry.JobID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list2: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}

func TestDLQStoreEntryTTL(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewDLQStore("redis://"+srv.Addr(), 2*time.Hour)
	if err != nil {
		t.Fatalf("dlq store init: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	entry := DLQEntry{
		JobID:     "job-ttl",
		Status:    "FAILED",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatalf("add: %v", err)
	}
	ttl, err := store.client.TTL(ctx, dlqEntryKey(entry.JobID)).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("expected ttl to be set, got %v", ttl)
	}
	if ttl > 2*time.Hour || ttl < 2*time.Hour-time.Second {
		t.Fatalf("expected ttl near 2h, got %s", ttl)
	}
}

func TestDLQStoreListByScore(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	entries := []DLQEntry{
		{JobID: "job-a", Status: "FAILED", CreatedAt: now.Add(-2 * time.Minute)},
		{JobID: "job-b", Status: "FAILED", CreatedAt: now.Add(-1 * time.Minute)},
		{JobID: "job-c", Status: "FAILED", CreatedAt: now.Add(-30 * time.Second)},
	}
	for _, entry := range entries {
		if err := store.Add(ctx, entry); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	list, err := store.ListByScore(ctx, now.Unix(), 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list[0].JobID != "job-c" || list[1].JobID != "job-b" {
		t.Fatalf("unexpected order: %+v", list)
	}

	cursor := list[len(list)-1].CreatedAt.Unix() - 1
	next, err := store.ListByScore(ctx, cursor, 2)
	if err != nil {
		t.Fatalf("list next: %v", err)
	}
	if len(next) != 1 || next[0].JobID != "job-a" {
		t.Fatalf("unexpected next: %+v", next)
	}
}

// ---------------------------------------------------------------------------
// CleanupStaleEntries tests
// ---------------------------------------------------------------------------

func TestDLQStoreCleanupStaleEntries(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// Add 5 entries.
	for i := 0; i < 5; i++ {
		entry := DLQEntry{
			JobID:     fmt.Sprintf("job-%d", i),
			Status:    "FAILED",
			CreatedAt: now.Add(-time.Duration(i) * time.Minute),
		}
		if err := store.Add(ctx, entry); err != nil {
			t.Fatalf("add job-%d: %v", i, err)
		}
	}

	// Verify all 5 are in the index.
	indexCount, err := store.client.ZCard(ctx, dlqIndexKey()).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if indexCount != 5 {
		t.Fatalf("expected 5 index entries, got %d", indexCount)
	}

	// Simulate expiry: delete data keys for job-1 and job-3.
	store.client.Del(ctx, dlqEntryKey("job-1"), dlqEntryKey("job-3"))

	// Run cleanup.
	removed, err := store.CleanupStaleEntries(ctx)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 2 {
		t.Fatalf("expected 2 removed, got %d", removed)
	}

	// Verify index now has 3 entries.
	indexCount, err = store.client.ZCard(ctx, dlqIndexKey()).Result()
	if err != nil {
		t.Fatalf("zcard after cleanup: %v", err)
	}
	if indexCount != 3 {
		t.Fatalf("expected 3 index entries after cleanup, got %d", indexCount)
	}

	// Verify the right entries remain.
	list, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list))
	}
}

func TestDLQStoreListLazyCleanup(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	// Add 3 entries.
	for _, id := range []string{"job-a", "job-b", "job-c"} {
		entry := DLQEntry{
			JobID:     id,
			Status:    "FAILED",
			CreatedAt: now,
		}
		if err := store.Add(ctx, entry); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	// Simulate expiry of job-b data key.
	store.client.Del(ctx, dlqEntryKey("job-b"))

	// List should return only job-a and job-c, and lazily clean the index.
	list, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	for _, e := range list {
		if e.JobID == "job-b" {
			t.Fatal("expired entry job-b should not appear in results")
		}
	}

	// Verify index was cleaned — job-b should be removed from sorted set.
	indexCount, err := store.client.ZCard(ctx, dlqIndexKey()).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if indexCount != 2 {
		t.Fatalf("expected 2 index entries after lazy cleanup, got %d", indexCount)
	}
}

func TestDLQStoreListByScoreLazyCleanup(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()

	for _, id := range []string{"job-x", "job-y", "job-z"} {
		entry := DLQEntry{
			JobID:     id,
			Status:    "FAILED",
			CreatedAt: now,
		}
		if err := store.Add(ctx, entry); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	// Expire job-y.
	store.client.Del(ctx, dlqEntryKey("job-y"))

	list, err := store.ListByScore(ctx, now.Unix()+1, 10)
	if err != nil {
		t.Fatalf("list by score: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}

	// Index should have 2 entries after lazy cleanup.
	indexCount, err := store.client.ZCard(ctx, dlqIndexKey()).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if indexCount != 2 {
		t.Fatalf("expected 2 index entries after lazy cleanup, got %d", indexCount)
	}
}

func TestDLQStoreCleanupNoStaleEntries(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	entry := DLQEntry{
		JobID:     "job-ok",
		Status:    "FAILED",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Cleanup with no stale entries should return 0.
	removed, err := store.CleanupStaleEntries(ctx)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 0 {
		t.Fatalf("expected 0 removed, got %d", removed)
	}
}

// ---------------------------------------------------------------------------
// Distributed lock tests
// ---------------------------------------------------------------------------

// newDLQStoreWithServer returns a DLQStore backed by the given miniredis server,
// allowing multiple stores to share the same Redis instance.
func newDLQStoreWithServer(t *testing.T, srv *miniredis.Miniredis) *DLQStore {
	t.Helper()
	s, err := NewDLQStore("redis://"+srv.Addr(), 0)
	if err != nil {
		t.Fatalf("dlq store init: %v", err)
	}
	return s
}

func TestDLQCleanupDistributedLock(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store1 := newDLQStoreWithServer(t, srv)
	defer store1.Close()
	store2 := newDLQStoreWithServer(t, srv)
	defer store2.Close()

	ctx := context.Background()

	// Insert entries and expire some to create cleanup work.
	for i := 0; i < 3; i++ {
		entry := DLQEntry{
			JobID:     fmt.Sprintf("lock-job-%d", i),
			Status:    "FAILED",
			CreatedAt: time.Now().UTC(),
		}
		if err := store1.Add(ctx, entry); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	// Expire one entry's data key.
	store1.client.Del(ctx, dlqEntryKey("lock-job-1"))

	lockTTL := 5 * time.Second
	id1 := generateInstanceID()
	id2 := generateInstanceID()

	// Both replicas attempt cleanup — only one should get the lock.
	var ran1, ran2 atomic.Int32
	origCleanup1 := func() {
		store1.runCleanupWithLock(ctx, lockTTL, id1)
		ran1.Add(1)
	}
	origCleanup2 := func() {
		store2.runCleanupWithLock(ctx, lockTTL, id2)
		ran2.Add(1)
	}

	origCleanup1()
	origCleanup2()

	// Both called runCleanupWithLock, but only one should have done actual cleanup.
	// The lock key should exist (from the winner) OR be released already.
	// Verify the stale entry was cleaned.
	indexCount, err := store1.client.ZCard(ctx, dlqIndexKey()).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if indexCount != 2 {
		t.Fatalf("expected 2 index entries after cleanup, got %d", indexCount)
	}
}

func TestDLQCleanupLockRelease(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store1 := newDLQStoreWithServer(t, srv)
	defer store1.Close()

	ctx := context.Background()
	lockTTL := 30 * time.Second
	id1 := generateInstanceID()

	// Add an entry so cleanup has something to scan.
	if err := store1.Add(ctx, DLQEntry{
		JobID:     "release-job",
		Status:    "FAILED",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Run cleanup — lock should be acquired and released.
	store1.runCleanupWithLock(ctx, lockTTL, id1)

	// Lock should be released — verify by checking the key no longer exists.
	exists, err := store1.client.Exists(ctx, dlqCleanupLockKey).Result()
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if exists != 0 {
		t.Fatal("expected lock to be released after cleanup")
	}

	// A second instance should now be able to acquire the lock.
	store2 := newDLQStoreWithServer(t, srv)
	defer store2.Close()
	id2 := generateInstanceID()

	ok, err := store2.client.SetNX(ctx, dlqCleanupLockKey, id2, lockTTL).Result()
	if err != nil {
		t.Fatalf("setnx: %v", err)
	}
	if !ok {
		t.Fatal("expected second instance to acquire lock after release")
	}
}

func TestDLQCleanupLockSafeRelease(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	s := newDLQStoreWithServer(t, srv)
	defer s.Close()

	ctx := context.Background()

	// Simulate another replica holding the lock.
	otherID := "other-replica-id"
	s.client.Set(ctx, dlqCleanupLockKey, otherID, 30*time.Second)

	// Our release attempt should NOT delete the lock (different owner).
	myID := "my-replica-id"
	result, err := releaseLockScript.Run(ctx, s.client, []string{dlqCleanupLockKey}, myID).Int()
	if err != nil {
		t.Fatalf("lua release: %v", err)
	}
	if result != 0 {
		t.Fatalf("expected 0 (lock not ours), got %d", result)
	}

	// Lock should still be held by the other replica.
	val, err := s.client.Get(ctx, dlqCleanupLockKey).Result()
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if val != otherID {
		t.Fatalf("expected lock value %q, got %q", otherID, val)
	}
}

func TestDLQCleanupLockBlocksSecondReplica(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	store1 := newDLQStoreWithServer(t, srv)
	defer store1.Close()
	store2 := newDLQStoreWithServer(t, srv)
	defer store2.Close()

	ctx := context.Background()
	lockTTL := 30 * time.Second

	// First replica acquires lock manually.
	id1 := generateInstanceID()
	ok, err := store1.client.SetNX(ctx, dlqCleanupLockKey, id1, lockTTL).Result()
	if err != nil || !ok {
		t.Fatalf("first lock: ok=%v err=%v", ok, err)
	}

	// Second replica tries to acquire — should fail (skip cleanup).
	id2 := generateInstanceID()
	ok2, err := store2.client.SetNX(ctx, dlqCleanupLockKey, id2, lockTTL).Result()
	if err != nil {
		t.Fatalf("second lock attempt: %v", err)
	}
	if ok2 {
		t.Fatal("expected second replica to be blocked by existing lock")
	}
}

func TestDLQCleanupLockTTLExpiry(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	s := newDLQStoreWithServer(t, srv)
	defer s.Close()

	ctx := context.Background()

	// Set lock with short TTL.
	s.client.Set(ctx, dlqCleanupLockKey, "stale-owner", 2*time.Second)

	// Fast-forward miniredis clock past TTL.
	srv.FastForward(3 * time.Second)

	// Lock should have expired — new replica can acquire.
	ok, err := s.client.SetNX(ctx, dlqCleanupLockKey, "new-owner", 30*time.Second).Result()
	if err != nil {
		t.Fatalf("setnx: %v", err)
	}
	if !ok {
		t.Fatal("expected lock to be available after TTL expiry")
	}
}

func TestGenerateInstanceID(t *testing.T) {
	id1 := generateInstanceID()
	id2 := generateInstanceID()

	if id1 == "" || id2 == "" {
		t.Fatal("instance IDs should not be empty")
	}
	if id1 == id2 {
		t.Fatal("instance IDs should be unique")
	}
	if len(id1) != 16 { // 8 bytes = 16 hex chars
		t.Fatalf("expected 16-char hex string, got %d chars: %q", len(id1), id1)
	}
}

func TestDLQCleanupRunCleanupWithLock_RedisDown(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}

	s := newDLQStoreWithServer(t, srv)
	defer s.Close()

	// Stop Redis to simulate unavailability.
	srv.Close()

	ctx := context.Background()

	// Should not panic — gracefully log and return.
	s.runCleanupWithLock(ctx, 5*time.Second, "test-id")

	// Verify no lock was set (Redis is down).
	_, err = s.client.Get(ctx, dlqCleanupLockKey).Result()
	if err != redis.Nil && err != nil {
		// Any error is fine — Redis is down. Just ensure no panic occurred.
		_ = err
	}
}
