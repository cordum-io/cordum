package audit

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
)

// The hold record and the tenant index hash to different slots, so a
// cluster client refuses to write them in one MULTI.
func TestLegalHoldStore_WorksOnClusterClient(t *testing.T) {
	mr := miniredis.RunT(t)
	store := NewLegalHoldStoreFromClient(testredis.NewMiniClusterClient(t, mr.Addr()))
	ctx := context.Background()

	hold, err := store.CreateHold(ctx, "acme", "litigation", "admin")
	if err != nil {
		t.Fatalf("create hold on a cluster client: %v", err)
	}
	if held, err := store.IsUnderHold(ctx, "acme"); err != nil || !held {
		t.Fatalf("IsUnderHold = %v, %v; want true", held, err)
	}
	if holds, err := store.ListHolds(ctx, "acme"); err != nil || len(holds) != 1 || holds[0].ID != hold.ID {
		t.Fatalf("ListHolds = %+v, %v", holds, err)
	}
	if err := store.ReleaseHold(ctx, hold.ID, "admin"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if held, err := store.IsUnderHold(ctx, "acme"); err != nil || held {
		t.Fatalf("IsUnderHold after release = %v, %v; want false", held, err)
	}
}

// CreateHold writes the index entry before the record. Failing in between
// leaves an entry every reader skips: no hold, in every listing.
func TestLegalHoldStore_IndexEntryWithoutRecordIsNoHold(t *testing.T) {
	mr := miniredis.RunT(t)
	client := testredis.NewClient(t, mr.Addr())
	store := NewLegalHoldStoreFromClient(client)
	ctx := context.Background()
	if err := client.SAdd(ctx, legalHoldSetPrefix+"acme", "h-lost").Err(); err != nil {
		t.Fatalf("sadd: %v", err)
	}

	if held, err := store.IsUnderHold(ctx, "acme"); err != nil || held {
		t.Fatalf("IsUnderHold = %v, %v; want false", held, err)
	}
	for _, tenant := range []string{"acme", ""} {
		if holds, err := store.ListHolds(ctx, tenant); err != nil || len(holds) != 0 {
			t.Fatalf("ListHolds(%q) = %+v, %v; want none", tenant, holds, err)
		}
	}
	if _, err := store.CreateHold(ctx, "acme", "retry", "admin"); err != nil {
		t.Fatalf("create after a lost record: %v", err)
	}
	if held, err := store.IsUnderHold(ctx, "acme"); err != nil || !held {
		t.Fatalf("IsUnderHold after retry = %v, %v; want true", held, err)
	}
}
