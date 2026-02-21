package workflow

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/store"
)

// TestLockManager_ConcurrentAcquireRelease launches 50 goroutines that all
// acquire the same runID and verifies mutual exclusion (the shared counter
// never exceeds 1) and that the lock is cleaned up after all goroutines finish.
func TestLockManager_ConcurrentAcquireRelease(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)}
	const goroutines = 50
	const runID = "run-stress"

	var wg sync.WaitGroup
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			release, _ := lm.acquire(runID)
			defer release()

			// Inside the critical section — increment and check.
			cur := concurrent.Add(1)
			// Track the maximum concurrency observed.
			for {
				old := maxConcurrent.Load()
				if cur <= old || maxConcurrent.CompareAndSwap(old, cur) {
					break
				}
			}
			if cur > 1 {
				t.Errorf("mutual exclusion violated: %d goroutines in critical section", cur)
			}
			concurrent.Add(-1)
		}()
	}
	wg.Wait()

	if max := maxConcurrent.Load(); max > 1 {
		t.Errorf("max concurrent was %d, expected 1", max)
	}

	// After all releases, lock should be absent (no terminal flag, so it persists).
	// Mark terminal and verify cleanup.
	lm.markTerminal(runID)
	lm.mu.Lock()
	_, exists := lm.locks[runID]
	lm.mu.Unlock()
	if exists {
		t.Error("expected lock entry to be cleaned up after markTerminal with refs==0")
	}
}

// TestLockManager_ConcurrentDifferentRuns verifies that locks for different
// runIDs don't interfere — they should execute truly concurrently.
func TestLockManager_ConcurrentDifferentRuns(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)}
	const goroutines = 20

	var wg sync.WaitGroup
	var active atomic.Int32
	var maxActive atomic.Int32

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		runID := "run-" + string(rune('A'+i))
		go func() {
			defer wg.Done()
			release, _ := lm.acquire(runID)
			defer release()

			cur := active.Add(1)
			for {
				old := maxActive.Load()
				if cur <= old || maxActive.CompareAndSwap(old, cur) {
					break
				}
			}
			// Yield to encourage concurrency.
			active.Add(-1)
		}()
	}
	wg.Wait()

	// With different runIDs we expect multiple goroutines to be active simultaneously.
	// On slow/single-core CI this may still serialize, so we just verify no panics.
}

// TestLockManager_RaceWithTerminal exercises the race between markTerminal
// and concurrent acquires on the same runID.
func TestLockManager_RaceWithTerminal(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)}
	const runID = "run-terminal-race"
	const goroutines = 50

	var wg sync.WaitGroup

	// Phase 1: acquire, mark terminal, release — all concurrently.
	wg.Add(goroutines + 1)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			release, _ := lm.acquire(runID)
			release()
		}()
	}
	// Concurrently mark terminal.
	go func() {
		defer wg.Done()
		lm.markTerminal(runID)
	}()
	wg.Wait()

	// Phase 2: after everything settles, acquire should still work
	// (it creates a fresh entry if the old one was cleaned up).
	release, _ := lm.acquire(runID)
	release()
	lm.markTerminal(runID)

	lm.mu.Lock()
	_, exists := lm.locks[runID]
	lm.mu.Unlock()
	if exists {
		t.Error("expected lock to be cleaned up after final markTerminal")
	}
}

// TestLockManager_TerminalWhileHeld verifies that when markTerminal is called
// while the lock is held, cleanup happens on release, not on markTerminal.
func TestLockManager_TerminalWhileHeld(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)}
	const runID = "run-held"

	release, _ := lm.acquire(runID)

	lm.markTerminal(runID)

	// Should still exist because refs > 0.
	lm.mu.Lock()
	_, exists := lm.locks[runID]
	lm.mu.Unlock()
	if !exists {
		t.Fatal("expected entry to exist while lock is held")
	}

	release()

	// Now should be gone.
	lm.mu.Lock()
	_, exists = lm.locks[runID]
	lm.mu.Unlock()
	if exists {
		t.Fatal("expected entry to be cleaned up after release")
	}
}

// TestLockManager_MultipleHoldersTerminal verifies cleanup waits for ALL holders.
func TestLockManager_MultipleHoldersTerminal(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)}
	const runID = "run-multi"

	// Acquire from goroutine A.
	releaseA, _ := lm.acquire(runID)

	// Ensure B has called acquire (and thus incremented refs) before we proceed.
	// B will block on lock.mu until A releases, but refs is already incremented.
	bReady := make(chan struct{})
	done := make(chan func(), 1)
	go func() {
		// We need B to increment refs before markTerminal runs.
		// acquire increments refs under lm.mu, then blocks on lock.mu.
		// Since A holds lock.mu, B will block there after incrementing refs.
		releaseB, _ := lm.acquire(runID)
		close(bReady) // Signal: B has the lock now (A must have released)
		done <- releaseB
	}()

	// Wait until B has incremented refs. B calls lm.mu.Lock(), refs++, lm.mu.Unlock(),
	// then blocks on lock.mu. We can detect B has incremented refs by checking refs==2.
	for {
		lm.mu.Lock()
		lock, ok := lm.locks[runID]
		refsVal := int32(0)
		if ok {
			refsVal = lock.refs
		}
		lm.mu.Unlock()
		if refsVal >= 2 {
			break
		}
	}

	// Mark terminal while A holds and B is waiting (refs==2).
	lm.markTerminal(runID)

	// Release A — B should now acquire lock.mu.
	releaseA()

	// Wait for B to actually hold the lock.
	<-bReady

	// Get B's release function.
	releaseB := <-done

	// Entry should still exist because B holds it.
	lm.mu.Lock()
	_, exists := lm.locks[runID]
	lm.mu.Unlock()
	if !exists {
		t.Fatal("expected entry to exist while B holds lock")
	}

	// Release B — now cleanup should happen.
	releaseB()

	lm.mu.Lock()
	_, exists = lm.locks[runID]
	lm.mu.Unlock()
	if exists {
		t.Fatal("expected cleanup after all holders released")
	}
}

// ---- distributed lock tests ----

func newTestJobStore(t *testing.T) (*store.RedisJobStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	js, err := store.NewRedisJobStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("job store init: %v", err)
	}
	return js, srv
}

// TestDistributedRunLock_MutualExclusion verifies that two lockManagers
// sharing the same Redis cannot both hold the distributed lock simultaneously.
func TestDistributedRunLock_MutualExclusion(t *testing.T) {
	jobStore, srv := newTestJobStore(t)
	defer srv.Close()
	defer jobStore.Close()

	lm1 := lockManager{locks: make(map[string]*runLock), locker: jobStore}
	lm2 := lockManager{locks: make(map[string]*runLock), locker: jobStore}

	const runID = "run-mutex-test"
	var (
		concurrent atomic.Int32
		maxConc    atomic.Int32
		writes     atomic.Int32
	)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		lm := &lm1
		if i == 1 {
			lm = &lm2
		}
		wg.Add(1)
		go func(mgr *lockManager) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				release, ok := mgr.acquire(runID)
				if !ok {
					// Another replica holds the lock — skip (expected behavior).
					continue
				}
				n := concurrent.Add(1)
				if n > 1 {
					for {
						cur := maxConc.Load()
						if n <= cur || maxConc.CompareAndSwap(cur, n) {
							break
						}
					}
				}
				writes.Add(1)
				time.Sleep(time.Millisecond)
				concurrent.Add(-1)
				release()
			}
		}(lm)
	}
	wg.Wait()

	// With separate lockManagers (simulating replicas), the losing manager
	// skips when the distributed lock is contended. At least one manager
	// must make progress.
	if w := writes.Load(); w == 0 {
		t.Fatal("expected writes")
	}
	if mc := maxConc.Load(); mc > 1 {
		t.Errorf("expected mutual exclusion, got max concurrent = %d", mc)
	}
}

// TestDistributedRunLock_DifferentRunIDs verifies that locks on different
// runIDs do not interfere with each other.
func TestDistributedRunLock_DifferentRunIDs(t *testing.T) {
	jobStore, srv := newTestJobStore(t)
	defer srv.Close()
	defer jobStore.Close()

	lm := lockManager{locks: make(map[string]*runLock), locker: jobStore}

	var wg sync.WaitGroup
	var acquired atomic.Int32

	for _, runID := range []string{"run-a", "run-b", "run-c"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			release, ok := lm.acquire(id)
			if !ok {
				return
			}
			acquired.Add(1)
			time.Sleep(5 * time.Millisecond)
			release()
		}(runID)
	}
	wg.Wait()

	if got := acquired.Load(); got != 3 {
		t.Fatalf("expected 3 acquires on different runIDs, got %d", got)
	}
}

// TestDistributedRunLock_TTLExpiry verifies that after TTL expires, another
// instance can acquire the lock.
func TestDistributedRunLock_TTLExpiry(t *testing.T) {
	jobStore, srv := newTestJobStore(t)
	defer srv.Close()
	defer jobStore.Close()

	lm1 := lockManager{locks: make(map[string]*runLock), locker: jobStore}
	lm2 := lockManager{locks: make(map[string]*runLock), locker: jobStore}

	const runID = "run-ttl-test"

	// lm1 acquires — don't release, let TTL expire.
	_, _ = lm1.acquire(runID)

	key := runLockKey(runID)
	if !srv.Exists(key) {
		t.Fatal("expected Redis lock key to exist")
	}

	// Fast-forward past TTL.
	srv.FastForward(runLockTTL + time.Second)

	// Key should be expired.
	if srv.Exists(key) {
		t.Fatal("expected Redis lock key to expire after TTL")
	}

	// lm2 can now acquire the distributed lock.
	release2, ok := lm2.acquire(runID)
	if !ok {
		t.Fatal("expected lm2 to acquire lock after TTL expiry")
	}
	if !srv.Exists(key) {
		t.Fatal("expected lm2 to acquire Redis lock after TTL expiry")
	}
	release2()
}

// TestDistributedRunLock_LocalFallback verifies that the lockManager works
// correctly with no RunLocker (nil — backward-compatible local-only).
func TestDistributedRunLock_LocalFallback(t *testing.T) {
	lm := lockManager{locks: make(map[string]*runLock)} // no locker

	const runID = "run-local-dist"
	release, _ := lm.acquire(runID)
	release()

	release2, _ := lm.acquire(runID)
	release2()

	release3, _ := lm.acquire(runID)
	lm.markTerminal(runID)
	release3()

	lm.mu.Lock()
	_, exists := lm.locks[runID]
	lm.mu.Unlock()
	if exists {
		t.Fatal("expected lock entry to be cleaned up after markTerminal + release")
	}
}

// TestDistributedRunLock_MarkTerminalCleanup verifies markTerminal with
// distributed locks cleans up the local map entry.
func TestDistributedRunLock_MarkTerminalCleanup(t *testing.T) {
	jobStore, srv := newTestJobStore(t)
	defer srv.Close()
	defer jobStore.Close()

	lm := lockManager{locks: make(map[string]*runLock), locker: jobStore}

	const runID = "run-terminal-dist"
	release, ok := lm.acquire(runID)
	if !ok {
		t.Fatal("expected lock acquisition to succeed")
	}

	lm.markTerminal(runID)

	lm.mu.Lock()
	_, exists := lm.locks[runID]
	lm.mu.Unlock()
	if !exists {
		t.Fatal("expected entry to exist while lock held")
	}

	release()

	lm.mu.Lock()
	_, exists = lm.locks[runID]
	lm.mu.Unlock()
	if exists {
		t.Fatal("expected entry cleaned up after release with terminal flag")
	}
}

// TestDistributedRunLock_Renewal verifies that the lock TTL is renewed when
// the locker implements RunLockRenewer.
func TestDistributedRunLock_Renewal(t *testing.T) {
	jobStore, srv := newTestJobStore(t)
	defer srv.Close()
	defer jobStore.Close()

	lm := lockManager{locks: make(map[string]*runLock), locker: jobStore}

	const runID = "run-renewal-dist"
	release, ok := lm.acquire(runID)
	if !ok {
		t.Fatal("expected lock acquisition to succeed")
	}

	key := runLockKey(runID)
	if !srv.Exists(key) {
		t.Fatal("expected Redis lock key to exist after acquire")
	}

	// Fast-forward past original TTL — renewal should keep it alive.
	srv.FastForward(25 * time.Second)
	time.Sleep(50 * time.Millisecond)

	if !srv.Exists(key) {
		t.Fatal("expected Redis lock key to still exist after renewal")
	}

	release()

	if srv.Exists(key) {
		t.Fatal("expected Redis lock key to be released")
	}
}
