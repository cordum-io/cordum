package audit

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
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
