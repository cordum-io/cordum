package audit

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newTestLegalHoldStore(t *testing.T) (*LegalHoldStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewLegalHoldStore("redis://" + srv.Addr())
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
