package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func newTestLegalHoldStore(t *testing.T) (*LegalHoldStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewLegalHoldStore(testredis.URL(srv.Addr()))
	if err != nil {
		srv.Close()
		t.Fatalf("new legal hold store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		srv.Close()
	})
	return store, srv
}

func TestCreateHold_StoredInRedis(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	hold, err := store.CreateHold(ctx, "tenant-a", "litigation pending", "admin@corp.com")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if hold.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if hold.TenantID != "tenant-a" {
		t.Fatalf("expected tenant-a, got %s", hold.TenantID)
	}
	if hold.Reason != "litigation pending" {
		t.Fatalf("expected reason, got %s", hold.Reason)
	}
	if !hold.IsActive() {
		t.Fatal("new hold should be active")
	}

	// Verify retrievable
	got, err := store.GetHold(ctx, hold.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TenantID != "tenant-a" {
		t.Fatalf("got wrong tenant: %s", got.TenantID)
	}
}

func TestIsUnderHold_TrueForHeldTenant(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	_, err := store.CreateHold(ctx, "tenant-held", "compliance audit", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	held, err := store.IsUnderHold(ctx, "tenant-held")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !held {
		t.Fatal("tenant should be under hold")
	}

	// Different tenant not under hold
	notHeld, err := store.IsUnderHold(ctx, "tenant-other")
	if err != nil {
		t.Fatalf("check other: %v", err)
	}
	if notHeld {
		t.Fatal("other tenant should NOT be under hold")
	}
}

func TestReleaseHold_IsUnderHoldReturnsFalse(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	hold, err := store.CreateHold(ctx, "tenant-release", "temporary hold", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Release the hold
	if err := store.ReleaseHold(ctx, hold.ID, "ops-lead"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Should no longer be under hold
	held, err := store.IsUnderHold(ctx, "tenant-release")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if held {
		t.Fatal("released tenant should NOT be under hold")
	}

	// Verify hold record still exists (not deleted) with release info
	released, err := store.GetHold(ctx, hold.ID)
	if err != nil {
		t.Fatalf("get after release: %v", err)
	}
	if released.IsActive() {
		t.Fatal("hold should be marked released")
	}
	if released.ReleasedBy != "ops-lead" {
		t.Fatalf("expected released_by ops-lead, got %s", released.ReleasedBy)
	}
}

func TestDuplicateHold_Returns409(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	_, err := store.CreateHold(ctx, "tenant-dup", "first hold", "admin")
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err = store.CreateHold(ctx, "tenant-dup", "second hold", "admin")
	if !errors.Is(err, ErrHoldAlreadyExists) {
		t.Fatalf("expected ErrHoldAlreadyExists, got %v", err)
	}
}

func TestReleaseHold_DoesNotDeleteData(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	hold, err := store.CreateHold(ctx, "tenant-data", "retention required", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.ReleaseHold(ctx, hold.ID, "admin"); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Hold record still exists
	got, err := store.GetHold(ctx, hold.ID)
	if err != nil {
		t.Fatalf("get after release: %v", err)
	}
	if got == nil {
		t.Fatal("hold record should still exist after release")
	}
	if got.TenantID != "tenant-data" {
		t.Fatalf("tenant preserved: %s", got.TenantID)
	}
}

func TestListHolds_ByTenant(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	_, _ = store.CreateHold(ctx, "tenant-list-a", "hold a", "admin")
	_, _ = store.CreateHold(ctx, "tenant-list-b", "hold b", "admin")

	holdsA, err := store.ListHolds(ctx, "tenant-list-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(holdsA) != 1 {
		t.Fatalf("expected 1 hold for tenant-a, got %d", len(holdsA))
	}

	holdsB, err := store.ListHolds(ctx, "tenant-list-b")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(holdsB) != 1 {
		t.Fatalf("expected 1 hold for tenant-b, got %d", len(holdsB))
	}
}

func TestReleaseHold_AlreadyReleased(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	hold, err := store.CreateHold(ctx, "tenant-double-release", "hold", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.ReleaseHold(ctx, hold.ID, "admin"); err != nil {
		t.Fatalf("first release: %v", err)
	}

	err = store.ReleaseHold(ctx, hold.ID, "admin")
	if !errors.Is(err, ErrHoldAlreadyReleased) {
		t.Fatalf("expected ErrHoldAlreadyReleased, got %v", err)
	}
}

func TestGetHold_NotFound(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	_, err := store.GetHold(context.Background(), "nonexistent-id")
	if !errors.Is(err, ErrHoldNotFound) {
		t.Fatalf("expected ErrHoldNotFound, got %v", err)
	}
}

func TestCreateHold_ValidationErrors(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()

	_, err := store.CreateHold(ctx, "", "reason", "admin")
	if err == nil {
		t.Fatal("expected error for empty tenant")
	}

	_, err = store.CreateHold(ctx, "tenant", "", "admin")
	if err == nil {
		t.Fatal("expected error for empty reason")
	}
}

// TestIsUnderHold_SurfacesUnreadableHold pins the fail-safe: a hold record
// that cannot be read is an error, never "not held" — retention would
// otherwise delete held data.
func TestIsUnderHold_SurfacesUnreadableHold(t *testing.T) {
	store, srv := newTestLegalHoldStore(t)
	ctx := context.Background()
	if _, err := srv.SAdd(legalHoldSetPrefix+"tenant-x", "h1"); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	if err := srv.Set(legalHoldKeyPrefix+"h1", "{not json"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := store.IsUnderHold(ctx, "tenant-x"); err == nil {
		t.Fatal("expected an error for an unreadable hold record")
	}

	// A dangling index entry (record gone) is still just "not held".
	srv.Del(legalHoldKeyPrefix + "h1")
	held, err := store.IsUnderHold(ctx, "tenant-x")
	if err != nil || held {
		t.Fatalf("dangling index entry: held=%v err=%v, want false/nil", held, err)
	}
}

func TestIsUnderHold_EmptyTenant(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	held, err := store.IsUnderHold(context.Background(), "")
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if held {
		t.Fatal("empty tenant should not be under hold")
	}
}

// TestLegalHold_ChainLinkageUnaffected verifies that placing a tenant
// under legal hold does not perturb the audit hash chain: events for the
// held tenant get monotonic seqs and correct prev_hash linkage exactly
// as they would without the hold. Legal hold is a retention-policy
// flag, not a pipeline filter — it must never shift seq numbering or
// the chain's verifiability collapses.
//
// Regression guard: if a future change accidentally routes held-tenant
// events around Chainer.Append (e.g. to short-circuit to a long-term
// retention store), this test detects the seq gap.
func TestLegalHold_ChainLinkageUnaffected(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	t.Cleanup(srv.Close)

	client := testredis.NewClient(t, srv.Addr())

	holdStore := NewLegalHoldStoreFromClient(client)
	chainer := NewChainer(client, "lh:chain:")
	ctx := context.Background()

	// Place tenant under legal hold BEFORE appending any events so the
	// hold is active throughout. If Append consulted the hold store
	// and took a different path, the chain would shift — that would
	// show up as a broken prev_hash.
	if _, err := holdStore.CreateHold(ctx, "lh-tenant", "regulatory inquiry 2026-Q2", "admin@cordum.io"); err != nil {
		t.Fatalf("create hold: %v", err)
	}
	held, err := holdStore.IsUnderHold(ctx, "lh-tenant")
	if err != nil {
		t.Fatalf("is-under-hold: %v", err)
	}
	if !held {
		t.Fatal("tenant should report as under hold")
	}

	const n = 6
	hashes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ev := &SIEMEvent{
			EventType: EventSafetyDecision,
			Severity:  SeverityInfo,
			TenantID:  "lh-tenant",
			Action:    "on-hold",
		}
		if err := chainer.Append(ctx, ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
		if ev.Seq != int64(i+1) {
			t.Errorf("Seq[%d] = %d, want %d (legal hold must not shift seqs)", i, ev.Seq, i+1)
		}
		if i == 0 {
			if ev.PrevHash != "" {
				t.Errorf("genesis PrevHash = %q, want empty", ev.PrevHash)
			}
		} else if ev.PrevHash != hashes[i-1] {
			t.Errorf("PrevHash[%d] = %q, want %q (linkage broken under legal hold)",
				i, ev.PrevHash, hashes[i-1])
		}
		hashes = append(hashes, ev.EventHash)
	}

	// Verify every event_hash recomputes — if the held-tenant path
	// ever mutates payloads (e.g. to tag them with a hold_id), the
	// hash would stop matching and this assertion catches it.
	stream, err := client.XRange(ctx, chainer.StreamKey("lh-tenant"), "-", "+").Result()
	if err != nil {
		t.Fatalf("xrange: %v", err)
	}
	if len(stream) != n {
		t.Fatalf("stream len = %d, want %d", len(stream), n)
	}
	for i, entry := range stream {
		payload := entry.Values[chainStreamFieldEvent].(string)
		var got SIEMEvent
		if err := json.Unmarshal([]byte(payload), &got); err != nil {
			t.Fatalf("decode[%d]: %v", i, err)
		}
		ok, err := VerifyEventHash(&got)
		if err != nil {
			t.Fatalf("verify[%d]: %v", i, err)
		}
		if !ok {
			t.Errorf("Seq=%d hash did not recompute under legal hold", got.Seq)
		}
	}
}

func TestCreateHold_FencesChainStream(t *testing.T) {
	store, srv := newTestLegalHoldStore(t)
	ctx := context.Background()
	fence := store.fenceKey("tenant-f")
	if fence != "{audit:chain:tenant-f}:legal_hold_fence" {
		t.Fatalf("fence key = %q, want it in the chain stream's slot", fence)
	}
	if got := legalHoldFenceKey("{audit}:chain:tenant-f"); got != "{audit}:chain:tenant-f:legal_hold_fence" {
		t.Fatalf("fence key for a tagged stream = %q", got)
	}

	hold, err := store.CreateHold(ctx, "tenant-f", "litigation", "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, _ := srv.Get(fence); got != hold.ID {
		t.Fatalf("fence = %q, want the hold id %q", got, hold.ID)
	}
	if err := store.ReleaseHold(ctx, hold.ID, "test"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if srv.Exists(fence) {
		t.Fatal("fence survived the release")
	}
}

func TestIsUnderHold_StaleFence(t *testing.T) {
	store, srv := newTestLegalHoldStore(t)
	ctx := context.Background()
	fence := store.fenceKey("tenant-s")

	// A fence naming no record, or a released hold, is stale and cleared.
	hold, err := store.CreateHold(ctx, "tenant-s", "litigation", "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.ReleaseHold(ctx, hold.ID, "test"); err != nil {
		t.Fatalf("release: %v", err)
	}
	for _, id := range []string{"ghost", hold.ID} {
		if err := srv.Set(fence, id); err != nil {
			t.Fatalf("set fence: %v", err)
		}
		if held, err := store.IsUnderHold(ctx, "tenant-s"); err != nil || held {
			t.Fatalf("fence %q: held=%v err=%v, want false/nil", id, held, err)
		}
		if srv.Exists(fence) {
			t.Fatalf("stale fence %q not cleared", id)
		}
	}

	// A fence naming an active hold holds, even one the index lost.
	hold, err = store.CreateHold(ctx, "tenant-s", "litigation", "test")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := srv.SRem(legalHoldSetPrefix+"tenant-s", hold.ID); err != nil {
		t.Fatalf("srem: %v", err)
	}
	if held, err := store.IsUnderHold(ctx, "tenant-s"); err != nil || !held {
		t.Fatalf("held=%v err=%v, want true", held, err)
	}
	// An unreadable record behind the fence is surfaced, not skipped.
	if err := srv.Set(legalHoldKeyPrefix+hold.ID, "{not json"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := store.IsUnderHold(ctx, "tenant-s"); err == nil {
		t.Fatal("expected an error for an unreadable fenced hold")
	}
}

func TestCreateHold_WaitsForDeletionInFlight(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()
	deleting, release, deleted := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
	go func() {
		ran, err := store.DeleteUnlessHeld(ctx, "tenant-w", "sess-1", func(context.Context) error {
			close(deleting)
			<-release
			return nil
		})
		if err != nil {
			t.Errorf("delete: %v", err)
		}
		deleted <- ran
	}()
	<-deleting

	created := make(chan error, 1)
	go func() {
		_, err := store.CreateHold(ctx, "tenant-w", "litigation", "test")
		created <- err
	}()
	select {
	case err := <-created:
		t.Fatalf("CreateHold returned (%v) while a deletion was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if !<-deleted {
		t.Fatal("the deletion in flight before the hold did not run")
	}
	if err := <-created; err != nil {
		t.Fatalf("CreateHold: %v", err)
	}
	// From now on nothing of the tenant's is deleted.
	ran := false
	if deleted, err := store.DeleteUnlessHeld(ctx, "tenant-w", "sess-2", func(context.Context) error { ran = true; return nil }); err != nil || deleted || ran {
		t.Fatalf("delete under hold: deleted=%v ran=%v err=%v, want nothing", deleted, ran, err)
	}
}

func TestCreateHold_UnconfirmedWhenDeletionOutlastsContext(t *testing.T) {
	store, _ := newTestLegalHoldStore(t)
	ctx := context.Background()
	// A deletion registered by a sweeper that has not finished.
	if err := store.registerInflight(ctx, "tenant-u", "attempt sess-1"); err != nil {
		t.Fatalf("register: %v", err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	hold, err := store.CreateHold(short, "tenant-u", "litigation", "test")
	if !errors.Is(err, ErrHoldUnconfirmed) || hold == nil {
		t.Fatalf("CreateHold = %+v, %v; want the hold with ErrHoldUnconfirmed", hold, err)
	}
	// The hold is in place regardless.
	if held, err := store.IsUnderHold(ctx, "tenant-u"); err != nil || !held {
		t.Fatalf("held=%v err=%v, want true", held, err)
	}
	if _, err := store.CreateHold(ctx, "tenant-u", "again", "test"); !errors.Is(err, ErrHoldAlreadyExists) {
		t.Fatalf("second create: %v, want ErrHoldAlreadyExists", err)
	}

	// A registration older than legalHoldInflightStale is a crashed
	// sweeper's, not a deletion in flight.
	now, err := store.client.Time(ctx).Result()
	if err != nil {
		t.Fatalf("time: %v", err)
	}
	old := redis.Z{Score: float64(now.Add(-legalHoldInflightStale - time.Minute).UnixMilli()), Member: "attempt sess-old"}
	if err := store.client.ZAdd(ctx, store.inflightKey("tenant-v"), old).Err(); err != nil {
		t.Fatalf("zadd: %v", err)
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := store.CreateHold(bounded, "tenant-v", "litigation", "test"); err != nil {
		t.Fatalf("CreateHold behind a stale registration: %v", err)
	}
}

func TestDeleteUnlessHeld_KeepsHeldAndUnreadable(t *testing.T) {
	store, srv := newTestLegalHoldStore(t)
	ctx := context.Background()
	if _, err := store.CreateHold(ctx, "tenant-h", "litigation", "test"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := srv.SAdd(legalHoldSetPrefix+"tenant-b", "h1"); err != nil {
		t.Fatalf("sadd: %v", err)
	}
	if err := srv.Set(legalHoldKeyPrefix+"h1", "{not json"); err != nil {
		t.Fatalf("set: %v", err)
	}
	ran := false
	del := func(context.Context) error { ran = true; return nil }

	if deleted, err := store.DeleteUnlessHeld(ctx, "tenant-h", "k", del); err != nil || deleted || ran {
		t.Fatalf("held tenant: deleted=%v ran=%v err=%v", deleted, ran, err)
	}
	if deleted, err := store.DeleteUnlessHeld(ctx, "tenant-b", "k", del); !errors.As(err, new(HoldCheckError)) || deleted || ran {
		t.Fatalf("unreadable hold: deleted=%v ran=%v err=%v, want HoldCheckError", deleted, ran, err)
	}
	// del's own failure is the caller's, and ends the registration too.
	boom := errors.New("boom")
	if _, err := store.DeleteUnlessHeld(ctx, "tenant-free", "k", func(context.Context) error { return boom }); !errors.Is(err, boom) || errors.As(err, new(HoldCheckError)) {
		t.Fatalf("failed delete: %v, want boom", err)
	}
	for _, tenant := range []string{"tenant-h", "tenant-b", "tenant-free"} {
		if n, _ := store.client.ZCard(ctx, store.inflightKey(tenant)).Result(); n != 0 {
			t.Fatalf("%s: %d deletions still registered", tenant, n)
		}
	}
}
