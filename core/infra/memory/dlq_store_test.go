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
	store, err := NewDLQStore("redis://"+srv.Addr(), 0)
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

func TestDLQStoreEntryTTL(t *testing.T) {
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewDLQStore("redis://"+srv.Addr(), 2*time.Hour)
	if err != nil {
		t.Fatalf("dlq store init: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	entry := DLQEntry{
		JobID:     "job-ttl",
		Status:    "FAILED",
		CreatedAt: time.Now().UTC(),
	}
	if err := store.Add(ctx, entry); err != nil {
		t.Fatalf("add: %v", err)
	}
	ttl, err := store.client.TTL(ctx, dlqEntryKey(entry.JobID)).Result()
	if err != nil {
		t.Fatalf("ttl: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("expected ttl to be set, got %v", ttl)
	}
	if ttl > 2*time.Hour || ttl < 2*time.Hour-time.Second {
		t.Fatalf("expected ttl near 2h, got %s", ttl)
	}
}

func TestDLQStoreListByScore(t *testing.T) {
	store := newDLQStore(t)
	defer store.Close()

	ctx := context.Background()
	now := time.Now().UTC()
	entries := []DLQEntry{
		{JobID: "job-a", Status: "FAILED", CreatedAt: now.Add(-2 * time.Minute)},
		{JobID: "job-b", Status: "FAILED", CreatedAt: now.Add(-1 * time.Minute)},
		{JobID: "job-c", Status: "FAILED", CreatedAt: now.Add(-30 * time.Second)},
	}
	for _, entry := range entries {
		if err := store.Add(ctx, entry); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	list, err := store.ListByScore(ctx, now.Unix(), 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(list))
	}
	if list[0].JobID != "job-c" || list[1].JobID != "job-b" {
		t.Fatalf("unexpected order: %+v", list)
	}

	cursor := list[len(list)-1].CreatedAt.Unix() - 1
	next, err := store.ListByScore(ctx, cursor, 2)
	if err != nil {
		t.Fatalf("list next: %v", err)
	}
	if len(next) != 1 || next[0].JobID != "job-a" {
		t.Fatalf("unexpected next: %+v", next)
	}
}
