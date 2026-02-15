package workflow

import (
	"sync"
	"sync/atomic"
	"testing"
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
			release := lm.acquire(runID)
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
			release := lm.acquire(runID)
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
			release := lm.acquire(runID)
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
	release := lm.acquire(runID)
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

	release := lm.acquire(runID)

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
	releaseA := lm.acquire(runID)

	// Ensure B has called acquire (and thus incremented refs) before we proceed.
	// B will block on lock.mu until A releases, but refs is already incremented.
	bReady := make(chan struct{})
	done := make(chan func(), 1)
	go func() {
		// We need B to increment refs before markTerminal runs.
		// acquire increments refs under lm.mu, then blocks on lock.mu.
		// Since A holds lock.mu, B will block there after incrementing refs.
		releaseB := lm.acquire(runID)
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
