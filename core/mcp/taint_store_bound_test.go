package mcp

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestInProcessTaintStore_CapEvictsOldest asserts the in-process taint store is
// bounded by a max-entry cap (an attacker cycling synthetic session ids must not
// grow it without limit), while the most-recent taint is retained.
func TestInProcessTaintStore_CapEvictsOldest(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	store := newInProcessTaintStore(time.Hour, 3, clock)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		now = now.Add(time.Second) // distinct expiry per entry => deterministic oldest
		if err := store.Taint(ctx, "tnt", "sess-"+strconv.Itoa(i), SessionTaint{Tool: "get_board"}); err != nil {
			t.Fatalf("Taint: %v", err)
		}
	}
	if n := store.entryCount(); n > 3 {
		t.Fatalf("cap not enforced: entryCount=%d, want <= 3", n)
	}
	// The most-recent session must still be tainted (taint persists within bound).
	if _, ok, _ := store.GetTaint(ctx, "tnt", "sess-4"); !ok {
		t.Fatalf("most-recent taint must be retained within the cap")
	}
}

// TestInProcessTaintStore_TTLExpires asserts a per-entry TTL: a taint is
// retrievable before expiry and reads clean (fail-safe default) after.
func TestInProcessTaintStore_TTLExpires(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	store := newInProcessTaintStore(time.Minute, 10_000, clock)
	ctx := context.Background()

	if err := store.Taint(ctx, "tnt", "sess", SessionTaint{Tool: "get_board"}); err != nil {
		t.Fatalf("Taint: %v", err)
	}
	if _, ok, _ := store.GetTaint(ctx, "tnt", "sess"); !ok {
		t.Fatalf("taint must be retrievable before TTL expiry")
	}
	now = now.Add(2 * time.Minute) // past the 1-minute TTL
	if _, ok, _ := store.GetTaint(ctx, "tnt", "sess"); ok {
		t.Fatalf("taint must expire after its TTL (fail-safe clean default)")
	}
}

// TestInProcessTaintStore_ExpiredSweptBeforeCap asserts cap-triggered eviction
// drops EXPIRED entries first, and a fresh taint within cap+TTL is retained.
func TestInProcessTaintStore_ExpiredSweptBeforeCap(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	clock := func() time.Time { return now }
	store := newInProcessTaintStore(time.Minute, 3, clock)
	ctx := context.Background()

	_ = store.Taint(ctx, "tnt", "old-1", SessionTaint{Tool: "t"})
	_ = store.Taint(ctx, "tnt", "old-2", SessionTaint{Tool: "t"})
	now = now.Add(2 * time.Minute) // expire old-1, old-2
	for i := 0; i < 3; i++ {
		_ = store.Taint(ctx, "tnt", "new-"+strconv.Itoa(i), SessionTaint{Tool: "t"})
	}
	if n := store.entryCount(); n > 3 {
		t.Fatalf("entryCount=%d, want <= 3", n)
	}
	if _, ok, _ := store.GetTaint(ctx, "tnt", "old-1"); ok {
		t.Fatalf("expired taint must not survive eviction")
	}
	if _, ok, _ := store.GetTaint(ctx, "tnt", "new-2"); !ok {
		t.Fatalf("a fresh taint within cap+TTL must be retained")
	}
}

// TestInProcessTaintStore_ConcurrentTaintAndGet exercises concurrent Taint/Get
// under churn that exceeds the cap; it must stay bounded and not panic/race
// (run with -count for flake detection; -race in CI).
func TestInProcessTaintStore_ConcurrentTaintAndGet(t *testing.T) {
	store := newInProcessTaintStore(time.Hour, 1_000, time.Now)
	ctx := context.Background()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				sess := "s-" + strconv.Itoa(g) + "-" + strconv.Itoa(i)
				_ = store.Taint(ctx, "tnt", sess, SessionTaint{Tool: "t"})
				_, _, _ = store.GetTaint(ctx, "tnt", sess)
			}
		}(g)
	}
	wg.Wait()
	if n := store.entryCount(); n > 1_000 {
		t.Fatalf("concurrent churn must stay within the cap, entryCount=%d", n)
	}
}
