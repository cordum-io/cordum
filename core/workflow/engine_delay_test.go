package workflow

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestStoreWithServer(t *testing.T) (*RedisStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisWorkflowStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	return store, srv
}

// TestDelayTimerPersistedToRedis verifies that AddDelayTimer writes to the ZSET
// with the correct score and member format.
func TestDelayTimerPersistedToRedis(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	fireAt := time.Now().Add(30 * time.Second)

	if err := store.AddDelayTimer(ctx, "wf-1", "run-1", fireAt); err != nil {
		t.Fatalf("AddDelayTimer: %v", err)
	}

	// Verify ZSET entry exists with correct member.
	members, err := store.client.ZRangeWithScores(ctx, delayTimerKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("ZRangeWithScores: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if members[0].Member != "wf-1:run-1" {
		t.Fatalf("expected member wf-1:run-1, got %v", members[0].Member)
	}
	if int64(members[0].Score) != fireAt.Unix() {
		t.Fatalf("expected score %d, got %d", fireAt.Unix(), int64(members[0].Score))
	}
}

// TestDelayTimerRemovedOnRemove verifies RemoveDelayTimer deletes the entry.
func TestDelayTimerRemovedOnRemove(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	fireAt := time.Now().Add(30 * time.Second)

	if err := store.AddDelayTimer(ctx, "wf-1", "run-1", fireAt); err != nil {
		t.Fatalf("AddDelayTimer: %v", err)
	}
	if err := store.RemoveDelayTimer(ctx, "wf-1", "run-1"); err != nil {
		t.Fatalf("RemoveDelayTimer: %v", err)
	}

	count, err := store.client.ZCard(ctx, delayTimerKey).Result()
	if err != nil {
		t.Fatalf("ZCard: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 entries after remove, got %d", count)
	}
}

// TestPopFiredDelays_Atomic verifies that PopFiredDelays atomically returns
// and removes all past-due entries.
func TestPopFiredDelays_Atomic(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Add 3 past-due and 1 future timer.
	for i, d := range []time.Duration{-60 * time.Second, -30 * time.Second, -1 * time.Second, 30 * time.Second} {
		wfID := "wf-1"
		runID := "run-" + string(rune('a'+i))
		if err := store.AddDelayTimer(ctx, wfID, runID, now.Add(d)); err != nil {
			t.Fatalf("AddDelayTimer %d: %v", i, err)
		}
	}

	fired, err := store.PopFiredDelays(ctx, now)
	if err != nil {
		t.Fatalf("PopFiredDelays: %v", err)
	}
	if len(fired) != 3 {
		t.Fatalf("expected 3 fired, got %d: %v", len(fired), fired)
	}

	// Verify only the future entry remains.
	remaining, err := store.client.ZCard(ctx, delayTimerKey).Result()
	if err != nil {
		t.Fatalf("ZCard: %v", err)
	}
	if remaining != 1 {
		t.Fatalf("expected 1 remaining, got %d", remaining)
	}

	// Second call should return nothing.
	fired2, err := store.PopFiredDelays(ctx, now)
	if err != nil {
		t.Fatalf("PopFiredDelays 2: %v", err)
	}
	if len(fired2) != 0 {
		t.Fatalf("expected 0 on second pop, got %d", len(fired2))
	}
}

// TestListFutureDelays returns only entries with fire time > now.
func TestListFutureDelays(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	if err := store.AddDelayTimer(ctx, "wf-1", "run-past", now.Add(-10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDelayTimer(ctx, "wf-1", "run-future1", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDelayTimer(ctx, "wf-1", "run-future2", now.Add(60*time.Second)); err != nil {
		t.Fatal(err)
	}

	future, err := store.ListFutureDelays(ctx, now)
	if err != nil {
		t.Fatalf("ListFutureDelays: %v", err)
	}
	if len(future) != 2 {
		t.Fatalf("expected 2 future, got %d", len(future))
	}
}

// TestCleanStaleDelays removes entries older than the cutoff.
func TestCleanStaleDelays(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Add entries at various ages.
	if err := store.AddDelayTimer(ctx, "wf-1", "run-old", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDelayTimer(ctx, "wf-1", "run-recent", now.Add(-30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.AddDelayTimer(ctx, "wf-1", "run-future", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}

	cutoff := now.Add(-1 * time.Hour)
	removed, err := store.CleanStaleDelays(ctx, cutoff)
	if err != nil {
		t.Fatalf("CleanStaleDelays: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 removed, got %d", removed)
	}

	remaining, err := store.client.ZCard(ctx, delayTimerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if remaining != 2 {
		t.Fatalf("expected 2 remaining, got %d", remaining)
	}
}

// TestRecoverDelayTimers_PastDue verifies that past-due timers are fired immediately.
func TestRecoverDelayTimers_PastDue(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Manually add past-due entries to ZSET (simulating crash scenario).
	store.client.ZAdd(ctx, delayTimerKey,
		redis.Z{Score: float64(now.Add(-60 * time.Second).Unix()), Member: "wf-1:run-a"},
		redis.Z{Score: float64(now.Add(-30 * time.Second).Unix()), Member: "wf-1:run-b"},
	)

	// Create engine with no bus (StartRun will be a no-op since no bus/workflow).
	engine := NewEngine(store, nil)
	engine.recoverDelayTimers(ctx)

	// Verify ZSET is empty (past-due entries were popped).
	count, err := store.client.ZCard(ctx, delayTimerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected 0 after recovery, got %d", count)
	}
}

// TestRecoverDelayTimers_Future verifies that future timers are re-scheduled.
func TestRecoverDelayTimers_Future(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Add a future timer.
	fireAt := now.Add(60 * time.Second)
	store.client.ZAdd(ctx, delayTimerKey,
		redis.Z{Score: float64(fireAt.Unix()), Member: "wf-1:run-future"},
	)

	engine := NewEngine(store, nil)
	engine.recoverDelayTimers(ctx)
	defer engine.Stop()

	// Verify timer was re-scheduled (pendingTimers should have 1 entry).
	if n := engine.PendingTimers(); n != 1 {
		t.Fatalf("expected 1 pending timer, got %d", n)
	}

	// The ZSET entry should still exist (scheduleAfter re-adds it via ZADD).
	count, err := store.client.ZCard(ctx, delayTimerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 ZSET entry (re-added), got %d", count)
	}
}

// TestRecoverDelayTimers_NilStore verifies no panic when store is nil.
func TestRecoverDelayTimers_NilStore(t *testing.T) {
	engine := NewEngine(nil, nil)
	// Should not panic.
	engine.recoverDelayTimers(context.Background())
}

// TestSplitDelayMember verifies parsing of "workflowID:runID" format.
func TestSplitDelayMember(t *testing.T) {
	tests := []struct {
		input string
		wfID  string
		runID string
	}{
		{"wf-1:run-1", "wf-1", "run-1"},
		{"abc:xyz", "abc", "xyz"},
		{"", "", ""},
		{"nocolon", "", ""},
		{":trailing", "", ""},
		{"leading:", "", ""},
	}
	for _, tc := range tests {
		wf, r := splitDelayMember(tc.input)
		if wf != tc.wfID || r != tc.runID {
			t.Errorf("splitDelayMember(%q) = (%q, %q), want (%q, %q)", tc.input, wf, r, tc.wfID, tc.runID)
		}
	}
}

// TestGetDelayTimerFuture verifies that GetDelayTimer returns timer info for a future fire time.
func TestGetDelayTimerFuture(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	fireAt := time.Now().Add(30 * time.Second)

	if err := store.AddDelayTimer(ctx, "wf-1", "run-1", fireAt); err != nil {
		t.Fatal(err)
	}

	info, err := store.GetDelayTimer(ctx, "wf-1", "run-1")
	if err != nil {
		t.Fatalf("GetDelayTimer: %v", err)
	}
	if info == nil {
		t.Fatal("expected timer info, got nil")
	}
	if info.WorkflowID != "wf-1" || info.RunID != "run-1" {
		t.Fatalf("unexpected IDs: %s, %s", info.WorkflowID, info.RunID)
	}
	if info.RemainingMs <= 0 {
		t.Fatalf("expected positive remaining_ms, got %d", info.RemainingMs)
	}
	if info.FiresAt.Unix() != fireAt.Unix() {
		t.Fatalf("expected fires_at %d, got %d", fireAt.Unix(), info.FiresAt.Unix())
	}
}

// TestGetDelayTimerNotFound verifies nil return when no timer exists.
func TestGetDelayTimerNotFound(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	info, err := store.GetDelayTimer(context.Background(), "wf-none", "run-none")
	if err != nil {
		t.Fatalf("GetDelayTimer: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil for missing timer, got %+v", info)
	}
}

// TestGetDelayTimerStale verifies nil return when timer has already fired.
func TestGetDelayTimerStale(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	pastTime := time.Now().Add(-10 * time.Second)

	if err := store.AddDelayTimer(ctx, "wf-1", "run-stale", pastTime); err != nil {
		t.Fatal(err)
	}

	info, err := store.GetDelayTimer(ctx, "wf-1", "run-stale")
	if err != nil {
		t.Fatalf("GetDelayTimer: %v", err)
	}
	if info != nil {
		t.Fatalf("expected nil for stale timer, got %+v", info)
	}
}

// TestDelayTimerIdempotent verifies ZADD is idempotent (same member updates score).
func TestDelayTimerIdempotent(t *testing.T) {
	store, srv := newTestStoreWithServer(t)
	defer srv.Close()
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	if err := store.AddDelayTimer(ctx, "wf-1", "run-1", now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	// Re-add with different fire time.
	newFireAt := now.Add(60 * time.Second)
	if err := store.AddDelayTimer(ctx, "wf-1", "run-1", newFireAt); err != nil {
		t.Fatal(err)
	}

	// Should still have only 1 entry.
	count, err := store.client.ZCard(ctx, delayTimerKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 entry (idempotent), got %d", count)
	}

	// Score should be updated.
	members, _ := store.client.ZRangeWithScores(ctx, delayTimerKey, 0, -1).Result()
	if int64(members[0].Score) != newFireAt.Unix() {
		t.Fatalf("expected updated score %d, got %d", newFireAt.Unix(), int64(members[0].Score))
	}
}
