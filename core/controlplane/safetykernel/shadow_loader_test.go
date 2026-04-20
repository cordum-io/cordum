package safetykernel

import (
	"context"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/policyshadow"
)

// newShadowTestStore stands up a miniredis-backed policyshadow.Store
// with a deterministic clock so snapshots inside tests compare stably.
func newShadowTestStore(t *testing.T) (*policyshadow.Store, func()) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		srv.Close()
		t.Fatalf("configsvc: %v", err)
	}
	store := policyshadow.NewStore(svc, policyshadow.WithClock(func() time.Time {
		return time.Unix(1_700_000_000, 0).UTC()
	}))
	return store, func() {
		_ = svc.Close()
		srv.Close()
	}
}

const validShadowYAML = `version: "1"
rules:
  - id: shadow-deny-all
    match:
      topics: ["job.foo"]
    decision: deny
    reason: shadow denies all
`

const malformedShadowYAML = `version: "1"
rules:
  - id: bad
    match:
      topics: not-a-list
    decision: deny
`

// TestShadowLoader_EmptyStoreReturnsEmptySnapshot asserts that with no
// shadows configured, Snapshot returns empty maps (not nil) so callers
// can range over them without nil-checks.
func TestShadowLoader_EmptyStoreReturnsEmptySnapshot(t *testing.T) {
	t.Parallel()
	store, cleanup := newShadowTestStore(t)
	defer cleanup()

	loader := NewShadowLoader(store, 50*time.Millisecond, func() []string { return []string{"tenant-a"} })
	defer loader.Close()

	compiled, meta := loader.Snapshot()
	if compiled == nil || meta == nil {
		t.Fatalf("Snapshot returned nil map; want empty non-nil")
	}
	if len(compiled) != 0 || len(meta) != 0 {
		t.Errorf("fresh loader snapshot not empty: compiled=%v meta=%v", compiled, meta)
	}
}

// TestShadowLoader_PicksUpNewShadowWithinRefreshTick confirms the
// ticker-driven refresh — write a shadow into the store AFTER the
// loader is running and verify it shows up on the next tick.
func TestShadowLoader_PicksUpNewShadowWithinRefreshTick(t *testing.T) {
	t.Parallel()
	store, cleanup := newShadowTestStore(t)
	defer cleanup()

	loader := NewShadowLoader(store, 20*time.Millisecond, func() []string { return []string{"tenant-a"} })
	defer loader.Close()

	// Snapshot must be empty before the shadow is written.
	compiled, _ := loader.Snapshot()
	if len(compiled) != 0 {
		t.Fatalf("loader pre-snapshot not empty: %v", compiled)
	}

	_, err := store.Put(context.Background(), policyshadow.ShadowPolicy{
		TenantID:  "tenant-a",
		BundleID:  "bundle-1",
		Content:   validShadowYAML,
		CreatedBy: "op",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// Wait up to 500ms for the next tick to pick up the write.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		compiled, meta := loader.Snapshot()
		if tc, ok := compiled["tenant-a"]; ok && len(tc) == 1 {
			if _, ok := tc["bundle-1"]; !ok {
				t.Fatalf("compiled map missing bundle-1: %v", tc)
			}
			if _, ok := meta["tenant-a"]["bundle-1"]; !ok {
				t.Fatalf("meta map missing bundle-1")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("shadow never appeared in loader snapshot within 500ms")
}

// TestShadowLoader_MalformedYAMLSkippedOthersKept asserts that one
// malformed shadow does not block the rest of the tenant's shadows —
// operators regularly iterate on shadow YAML and a typo must not
// silently blank out unrelated policies.
func TestShadowLoader_MalformedYAMLSkippedOthersKept(t *testing.T) {
	t.Parallel()
	store, cleanup := newShadowTestStore(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := store.Put(ctx, policyshadow.ShadowPolicy{TenantID: "tenant-a", BundleID: "good", Content: validShadowYAML, CreatedBy: "op"}); err != nil {
		t.Fatalf("put good: %v", err)
	}
	if _, err := store.Put(ctx, policyshadow.ShadowPolicy{TenantID: "tenant-a", BundleID: "bad", Content: malformedShadowYAML, CreatedBy: "op"}); err != nil {
		t.Fatalf("put bad: %v", err)
	}

	loader := NewShadowLoader(store, time.Hour, func() []string { return []string{"tenant-a"} })
	defer loader.Close()

	// Force a deterministic refresh (don't wait for the ticker).
	if err := loader.refreshOnce(ctx); err != nil {
		t.Fatalf("refreshOnce: %v", err)
	}
	compiled, _ := loader.Snapshot()
	tenant := compiled["tenant-a"]
	if _, ok := tenant["good"]; !ok {
		t.Errorf("good shadow missing from snapshot: %v", tenant)
	}
	if _, ok := tenant["bad"]; ok {
		t.Errorf("malformed shadow should have been skipped; found in snapshot")
	}
}

// TestShadowLoader_ConcurrentSnapshotReadsRaceSafe fires N concurrent
// Snapshot readers alongside a refresh loop and checks no races (run
// with `go test -count=3` per CLAUDE.md platform note; race detector
// is unavailable on this platform so the repetition exposes flakes).
func TestShadowLoader_ConcurrentSnapshotReadsRaceSafe(t *testing.T) {
	t.Parallel()
	store, cleanup := newShadowTestStore(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		bundleID := "bundle-" + time.Now().Add(time.Duration(i)*time.Microsecond).Format("150405.000000")
		if _, err := store.Put(ctx, policyshadow.ShadowPolicy{TenantID: "tenant-a", BundleID: bundleID, Content: validShadowYAML, CreatedBy: "op"}); err != nil {
			t.Fatalf("put: %v", err)
		}
	}

	loader := NewShadowLoader(store, 5*time.Millisecond, func() []string { return []string{"tenant-a", "tenant-b"} })
	defer loader.Close()

	// Bounded iteration count keeps the test deterministic — 8 readers
	// × 500 Snapshot() calls is enough to surface any race with the
	// 5ms ticker over hundreds of refresh/read interleavings without
	// burning CPU indefinitely.
	const readersN, iterN = 8, 500
	var wg sync.WaitGroup
	for range readersN {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range iterN {
				compiled, meta := loader.Snapshot()
				if compiled == nil || meta == nil {
					t.Error("reader observed nil snapshot")
					return
				}
				// Touch a value to flush any races through the memory model.
				for _, tm := range compiled {
					for _, p := range tm {
						_ = p
					}
				}
			}
		}()
	}
	wg.Wait()
}

// TestShadowLoader_EmptyTenantListClearsSnapshot covers the tenant-
// deletion case: tenantsFn() returns a shorter list than the previous
// snapshot, so the old tenant's shadows must drop out on the next
// refresh rather than sticking around as stale state.
func TestShadowLoader_EmptyTenantListClearsSnapshot(t *testing.T) {
	t.Parallel()
	store, cleanup := newShadowTestStore(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := store.Put(ctx, policyshadow.ShadowPolicy{TenantID: "tenant-a", BundleID: "b1", Content: validShadowYAML, CreatedBy: "op"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	var currentTenants = []string{"tenant-a"}
	var tenantsMu sync.Mutex
	tenantsFn := func() []string {
		tenantsMu.Lock()
		defer tenantsMu.Unlock()
		out := make([]string, len(currentTenants))
		copy(out, currentTenants)
		return out
	}

	loader := NewShadowLoader(store, time.Hour, tenantsFn)
	defer loader.Close()
	if err := loader.refreshOnce(ctx); err != nil {
		t.Fatalf("refreshOnce (seed): %v", err)
	}
	compiled, _ := loader.Snapshot()
	if _, ok := compiled["tenant-a"]; !ok {
		t.Fatalf("seed snapshot missing tenant-a: %v", compiled)
	}
	// Tenant is removed from the authoritative list — refresh must drop it.
	tenantsMu.Lock()
	currentTenants = nil
	tenantsMu.Unlock()
	if err := loader.refreshOnce(ctx); err != nil {
		t.Fatalf("refreshOnce (clear): %v", err)
	}
	compiled2, _ := loader.Snapshot()
	if _, ok := compiled2["tenant-a"]; ok {
		t.Errorf("stale tenant still present after tenant removal: %v", compiled2)
	}
}

// TestShadowLoader_CloseIdempotent verifies Close can be called more
// than once safely — the kernel's shutdown path may call Close from
// multiple defer chains during failure handling.
func TestShadowLoader_CloseIdempotent(t *testing.T) {
	t.Parallel()
	store, cleanup := newShadowTestStore(t)
	defer cleanup()
	loader := NewShadowLoader(store, time.Minute, func() []string { return nil })
	loader.Close()
	loader.Close() // must not panic or block forever
}

// TestShadowLoader_NilStoreSafe asserts a no-op configuration (store
// unavailable) still produces a usable loader that returns empty
// snapshots and closes cleanly. This is the dev/degraded-mode path.
func TestShadowLoader_NilStoreSafe(t *testing.T) {
	t.Parallel()
	loader := NewShadowLoader(nil, time.Minute, func() []string { return []string{"tenant-a"} })
	defer loader.Close()
	compiled, meta := loader.Snapshot()
	if len(compiled) != 0 || len(meta) != 0 {
		t.Errorf("nil-store loader produced non-empty snapshot: %v %v", compiled, meta)
	}
}
