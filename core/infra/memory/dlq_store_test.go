package memory

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newDLQStore(t *testing.T) *DLQStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewDLQStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("dlq store init: %v", err)
	}
	return store
}

func TestDLQStoreCRUD(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	entry := DLQEntry{
		JobID:     "job-1",
		Topic:     "job.test",
		Status:    "FAILED",
		Reason:    "boom",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatalf("add: %v", err)
	}

	gotOne, err := store.Get(ctx, entry.JobID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotOne.JobID != entry.JobID || gotOne.Reason != entry.Reason {
		t.Fatalf("get mismatch: %+v", gotOne)
	}

	list, err := store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].JobID != entry.JobID {
		t.Fatalf("unexpected list: %+v", list)
	}

	if err := store.Delete(ctx, entry.JobID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = store.List(ctx, 10)
	if err != nil {
		t.Fatalf("list2: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %+v", list)
	}
}
