package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
)

const testSnapshotLockKey = "cordum:scheduler:snapshot:writer"

// TestSnapshotWriterLock_OnlyOneWriterPerTTL verifies that two concurrent
// goroutines competing for the snapshot writer lock produce no concurrent
// writes. Only the lock holder increments the write counter.
func TestSnapshotWriterLock_OnlyOneWriterPerTTL(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	const (
		lockTTL  = 10 * time.Second
		ticks    = 20
		tickRate = 5 * time.Millisecond
	)

	var (
		writes     atomic.Int32
		concurrent atomic.Int32
		maxConc    atomic.Int32
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for replica := 0; replica < 2; replica++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < ticks; i++ {
				token, err := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, lockTTL)
				if err != nil {
					t.Errorf("lock acquire error: %v", err)
					continue
				}
				if token == "" {
					// Another replica holds the lock — skip.
					time.Sleep(tickRate)
					continue
				}

				// Simulate a write — track concurrency.
				n := concurrent.Add(1)
				if n > 1 {
					// Record max concurrency for assertion.
					for {
						cur := maxConc.Load()
						if n <= cur || maxConc.CompareAndSwap(cur, n) {
							break
						}
					}
				}
				writes.Add(1)
				time.Sleep(tickRate) // simulate snapshot marshal + write
				concurrent.Add(-1)

				_ = jobStore.ReleaseLock(ctx, testSnapshotLockKey, token)
			}
		}()
	}
	wg.Wait()

	if mc := maxConc.Load(); mc > 1 {
		t.Fatalf("concurrent writes detected: max concurrency = %d", mc)
	}
	totalWrites := writes.Load()
	if totalWrites == 0 {
		t.Fatal("expected at least one write")
	}
	// With 2 replicas × 20 ticks, total writes should be <= 40 (no amplification).
	if totalWrites > int32(2*ticks) {
		t.Fatalf("too many writes: %d (max expected %d)", totalWrites, 2*ticks)
	}
	t.Logf("total writes: %d / %d max ticks", totalWrites, 2*ticks)
}

// TestSnapshotWriterLock_SkipsWhenHeld verifies that a second caller gets
// an empty token (skip) when the lock is already held.
func TestSnapshotWriterLock_SkipsWhenHeld(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	ctx := context.Background()

	// First acquire succeeds.
	token1, err := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, 10*time.Second)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if token1 == "" {
		t.Fatal("expected first lock to succeed")
	}

	// Second acquire should skip (empty token, no error).
	token2, err := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, 10*time.Second)
	if err != nil {
		t.Fatalf("second lock: %v", err)
	}
	if token2 != "" {
		t.Fatal("expected second lock to be skipped (lock held)")
	}

	// Release and verify re-acquire works.
	if err := jobStore.ReleaseLock(ctx, testSnapshotLockKey, token1); err != nil {
		t.Fatalf("release: %v", err)
	}
	token3, err := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, 10*time.Second)
	if err != nil {
		t.Fatalf("third lock: %v", err)
	}
	if token3 == "" {
		t.Fatal("expected lock re-acquire after release")
	}
	_ = jobStore.ReleaseLock(ctx, testSnapshotLockKey, token3)
}

// TestSnapshotWriterLock_TTLExpiry verifies that after TTL expires, another
// replica can take over the lock.
func TestSnapshotWriterLock_TTLExpiry(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	defer srv.Close()

	jobStore, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store: %v", err)
	}
	defer jobStore.Close()

	ctx := context.Background()

	// Acquire with short TTL.
	token, err := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, 100*time.Millisecond)
	if err != nil || token == "" {
		t.Fatalf("acquire: err=%v, token=%q", err, token)
	}

	// Second caller blocked.
	t2, _ := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, 10*time.Second)
	if t2 != "" {
		t.Fatal("expected lock to be held before TTL expiry")
	}

	// Fast-forward past TTL.
	srv.FastForward(200 * time.Millisecond)

	// Now a new replica can acquire.
	t3, err := jobStore.TryAcquireLock(ctx, testSnapshotLockKey, 10*time.Second)
	if err != nil {
		t.Fatalf("post-TTL acquire: %v", err)
	}
	if t3 == "" {
		t.Fatal("expected lock to be available after TTL expiry")
	}
	_ = jobStore.ReleaseLock(ctx, testSnapshotLockKey, t3)
}
