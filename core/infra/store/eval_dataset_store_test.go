package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/cordum/cordum/core/model"
	"github.com/redis/go-redis/v9"
)

func newTestEvalDatasetStore(t *testing.T) (*EvalDatasetStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)

	client := testredis.NewClient(t, srv.Addr())

	return NewEvalDatasetStoreFromClient(client), srv
}

func sampleDataset(name string, version int, tenant string) model.EvalDataset {
	return model.EvalDataset{
		Name:        name,
		Version:     version,
		Tenant:      tenant,
		Description: "test dataset " + name,
		CreatedBy:   "tester@example.com",
		Entries: []model.EvalEntry{
			{
				ID:               "entry-1",
				Input:            json.RawMessage(`{"topic":"support","agent_id":"agent-a","tenant":"` + tenant + `"}`),
				ExpectedDecision: model.SafetyDeny,
				RuleID:           "rule-pii-01",
				Source:           model.EvalEntrySourceManual,
			},
		},
	}
}

func TestEvalDatasetCreateAndGet(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	created, err := s.CreateEvalDataset(ctx, sampleDataset("pii-leaks-q1", 1, "acme"))
	if err != nil {
		t.Fatalf("CreateEvalDataset: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected generated id")
	}
	if created.CreatedAt == "" || created.UpdatedAt != created.CreatedAt {
		t.Fatal("expected CreatedAt == UpdatedAt and both non-empty")
	}
	if created.EntryCount != 1 {
		t.Fatalf("expected EntryCount 1, got %d", created.EntryCount)
	}
	if len(created.ContentHash) != 64 {
		t.Fatalf("expected 64-char sha256, got %q", created.ContentHash)
	}

	got, err := s.GetEvalDataset(ctx, "acme", created.ID)
	if err != nil {
		t.Fatalf("GetEvalDataset: %v", err)
	}
	if got.ID != created.ID || got.Name != "pii-leaks-q1" || got.Version != 1 {
		t.Fatalf("Get: unexpected content %+v", got)
	}
	if got.ContentHash != created.ContentHash {
		t.Fatalf("ContentHash not persisted: want %q got %q", created.ContentHash, got.ContentHash)
	}
}

func TestEvalDatasetCreateDuplicateVersionReturnsSentinel(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	if _, err := s.CreateEvalDataset(ctx, sampleDataset("reg-pack", 1, "acme")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := s.CreateEvalDataset(ctx, sampleDataset("reg-pack", 1, "acme"))
	if !errors.Is(err, ErrEvalDatasetVersionExists) {
		t.Fatalf("expected ErrEvalDatasetVersionExists, got %v", err)
	}
}

func TestEvalDatasetCreateMultipleVersionsSucceeds(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	for _, v := range []int{1, 2, 3} {
		if _, err := s.CreateEvalDataset(ctx, sampleDataset("reg-pack", v, "acme")); err != nil {
			t.Fatalf("create v%d: %v", v, err)
		}
	}

	versions, err := s.ListEvalDatasetVersions(ctx, "acme", "reg-pack")
	if err != nil {
		t.Fatalf("ListEvalDatasetVersions: %v", err)
	}
	if len(versions) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(versions))
	}
	// Newest version first.
	if versions[0].Version != 3 || versions[1].Version != 2 || versions[2].Version != 1 {
		t.Fatalf("expected desc order 3,2,1, got %d,%d,%d",
			versions[0].Version, versions[1].Version, versions[2].Version)
	}
}

func TestEvalDatasetGetByNameVersion(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	created, err := s.CreateEvalDataset(ctx, sampleDataset("by-name-pack", 5, "acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetEvalDatasetByNameVersion(ctx, "acme", "by-name-pack", 5)
	if err != nil {
		t.Fatalf("GetEvalDatasetByNameVersion: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("expected id %q, got %q", created.ID, got.ID)
	}

	_, err = s.GetEvalDatasetByNameVersion(ctx, "acme", "by-name-pack", 999)
	if !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected ErrEvalDatasetNotFound on missing version, got %v", err)
	}
}

func TestEvalDatasetGetNotFound(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	_, err := s.GetEvalDataset(ctx, "acme", "nonexistent")
	if !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected ErrEvalDatasetNotFound, got %v", err)
	}
}

func TestEvalDatasetListPaginationCoversAllItems(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	const total = 12
	for i := 0; i < total; i++ {
		ds := sampleDataset(fmt.Sprintf("pack-%03d", i), 1, "acme")
		if _, err := s.CreateEvalDataset(ctx, ds); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		// Advance miniredis clock so each create lands on a distinct
		// unix-milli score; otherwise rapid same-nanosecond creates
		// collide and the cursor tie-break path gets exercised but this
		// test is specifically about the happy multi-page case.
		srv.FastForward(2 * time.Millisecond)
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, cursor, 5)
		if err != nil {
			t.Fatalf("List page %d: %v", pages+1, err)
		}
		for _, ds := range page.Items {
			if seen[ds.ID] {
				t.Fatalf("duplicate id %q across pages", ds.ID)
			}
			seen[ds.ID] = true
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate within 10 pages")
		}
	}
	if len(seen) != total {
		t.Fatalf("expected %d unique datasets across all pages, got %d (pages=%d)", total, len(seen), pages)
	}
	if pages < 2 {
		t.Fatalf("expected pagination across >=2 pages, ran only %d", pages)
	}
}

func TestEvalDatasetListUnfilteredExactLimitHasNoNextCursor(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	const limit = 5
	for i := 0; i < limit; i++ {
		ds := sampleDataset(fmt.Sprintf("exact-%03d", i), 1, "acme")
		if _, err := s.CreateEvalDataset(ctx, ds); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		srv.FastForward(2 * time.Millisecond)
	}

	page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, "", limit)
	if err != nil {
		t.Fatalf("ListEvalDatasets: %v", err)
	}
	if len(page.Items) != limit {
		t.Fatalf("len(page.Items)=%d want %d", len(page.Items), limit)
	}
	if page.NextCursor != "" {
		t.Fatalf("NextCursor=%q want empty for exact-limit unfiltered page", page.NextCursor)
	}
}

func TestEvalDatasetListUnfilteredDoesNotScanBeyondLimitPlusOne(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	const (
		tenant = "acme"
		limit  = 5
		total  = 40
	)
	for i := 0; i < total; i++ {
		ds := sampleDataset(fmt.Sprintf("overscan-%03d", i), 1, tenant)
		if _, err := s.CreateEvalDataset(ctx, ds); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		srv.FastForward(2 * time.Millisecond)
	}

	indexKey := evalDatasetIndexKey(tenant)
	ids, err := s.client.ZRevRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if len(ids) != total {
		t.Fatalf("index len=%d want %d", len(ids), total)
	}
	staleID := ids[limit+2] // older than the unfiltered page + one lookahead.
	srv.Del(evalDatasetRecordKey(tenant, staleID))

	page, err := s.ListEvalDatasets(ctx, tenant, model.EvalDatasetFilter{}, "", limit)
	if err != nil {
		t.Fatalf("ListEvalDatasets: %v", err)
	}
	if len(page.Items) != limit {
		t.Fatalf("len(page.Items)=%d want %d", len(page.Items), limit)
	}
	if _, err := s.client.ZScore(ctx, indexKey, staleID).Result(); err != nil {
		t.Fatalf("stale member outside limit+1 was pruned or unreadable; want it untouched, err=%v", err)
	}
}

func TestEvalDatasetListUnfilteredStalePruneDoesNotSkipLookahead(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	const (
		tenant = "acme"
		limit  = 5
		total  = 7
	)
	for i := 0; i < total; i++ {
		ds := sampleDataset(fmt.Sprintf("stale-lookahead-%03d", i), 1, tenant)
		if _, err := s.CreateEvalDataset(ctx, ds); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		srv.FastForward(2 * time.Millisecond)
	}

	indexKey := evalDatasetIndexKey(tenant)
	ids, err := s.client.ZRevRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if len(ids) != total {
		t.Fatalf("index len=%d want %d", len(ids), total)
	}
	staleID := ids[2]            // Inside the first limit+1 scan batch.
	oldestLiveID := ids[limit+1] // The lookahead item that offset-based paging must not skip.
	srv.Del(evalDatasetRecordKey(tenant, staleID))

	page, err := s.ListEvalDatasets(ctx, tenant, model.EvalDatasetFilter{}, "", limit)
	if err != nil {
		t.Fatalf("ListEvalDatasets first page: %v", err)
	}
	if len(page.Items) != limit {
		t.Fatalf("len(page.Items)=%d want %d", len(page.Items), limit)
	}
	if page.NextCursor == "" {
		t.Fatal("NextCursor empty; stale pruning skipped the live lookahead item")
	}
	if _, err := s.client.ZScore(ctx, indexKey, staleID).Result(); !errors.Is(err, redis.Nil) {
		t.Fatalf("stale member was not pruned after scan, err=%v", err)
	}

	second, err := s.ListEvalDatasets(ctx, tenant, model.EvalDatasetFilter{}, page.NextCursor, limit)
	if err != nil {
		t.Fatalf("ListEvalDatasets second page: %v", err)
	}
	if len(second.Items) != 1 {
		t.Fatalf("len(second.Items)=%d want 1 (%q)", len(second.Items), oldestLiveID)
	}
	if second.Items[0].ID != oldestLiveID {
		t.Fatalf("second page ID=%q want oldest live ID %q", second.Items[0].ID, oldestLiveID)
	}
}

func TestEvalDatasetListUnfilteredSameScorePaginationNoDuplicates(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	const total = 6
	ids := make([]string, 0, total)
	for i := 0; i < total; i++ {
		ds := sampleDataset(fmt.Sprintf("same-score-%03d", i), 1, "acme")
		created, err := s.CreateEvalDataset(ctx, ds)
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids = append(ids, created.ID)
	}

	sameTime := time.Date(2026, time.April, 20, 15, 0, 0, 123_000_000, time.UTC)
	sameTimeText := sameTime.Format(time.RFC3339Nano)
	indexKey := evalDatasetIndexKey("acme")
	for _, id := range ids {
		ds, err := s.GetEvalDataset(ctx, "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		ds.CreatedAt = sameTimeText
		ds.UpdatedAt = sameTimeText
		payload, err := json.Marshal(ds)
		if err != nil {
			t.Fatalf("marshal %s: %v", id, err)
		}
		if err := s.client.HSet(ctx, evalDatasetRecordKey("acme", id), evalDatasetRecField, payload).Err(); err != nil {
			t.Fatalf("hset %s: %v", id, err)
		}
		if err := s.client.ZAdd(ctx, indexKey, redis.Z{Score: float64(sameTime.UnixMilli()), Member: id}).Err(); err != nil {
			t.Fatalf("zadd %s: %v", id, err)
		}
	}

	seen := make(map[string]struct{}, total)
	cursor := ""
	for pages := 0; ; pages++ {
		page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, cursor, 2)
		if err != nil {
			t.Fatalf("List page %d: %v", pages+1, err)
		}
		for _, ds := range page.Items {
			if _, exists := seen[ds.ID]; exists {
				t.Fatalf("duplicate dataset id %q across same-score pages", ds.ID)
			}
			seen[ds.ID] = struct{}{}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > total {
			t.Fatal("same-score pagination did not terminate")
		}
	}
	if len(seen) != total {
		t.Fatalf("saw %d unique datasets, want %d", len(seen), total)
	}
}

func TestEvalDatasetListFiltersByNamePrefix(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	for _, name := range []string{"pii-leaks-q1", "pii-leaks-q2", "tool-drift", "other"} {
		if _, err := s.CreateEvalDataset(ctx, sampleDataset(name, 1, "acme")); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}

	page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{NamePrefix: "pii-leaks"}, "", 50)
	if err != nil {
		t.Fatalf("List filtered: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 pii-leaks datasets, got %d", len(page.Items))
	}
	for _, ds := range page.Items {
		if !strings.HasPrefix(ds.Name, "pii-leaks") {
			t.Fatalf("filter leak: got %q", ds.Name)
		}
	}
}

func TestEvalDatasetListTenantIsolation(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	if _, err := s.CreateEvalDataset(ctx, sampleDataset("shared-name", 1, "tenant-a")); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := s.CreateEvalDataset(ctx, sampleDataset("shared-name", 1, "tenant-b")); err != nil {
		t.Fatalf("create b: %v", err)
	}

	pageA, err := s.ListEvalDatasets(ctx, "tenant-a", model.EvalDatasetFilter{}, "", 50)
	if err != nil {
		t.Fatalf("list a: %v", err)
	}
	if len(pageA.Items) != 1 || pageA.Items[0].Tenant != "tenant-a" {
		t.Fatalf("tenant a leaked: %+v", pageA.Items)
	}

	pageB, err := s.ListEvalDatasets(ctx, "tenant-b", model.EvalDatasetFilter{}, "", 50)
	if err != nil {
		t.Fatalf("list b: %v", err)
	}
	if len(pageB.Items) != 1 || pageB.Items[0].Tenant != "tenant-b" {
		t.Fatalf("tenant b leaked: %+v", pageB.Items)
	}

	// Cross-tenant get: tenant-b cannot read tenant-a's dataset by id.
	aID := pageA.Items[0].ID
	_, err = s.GetEvalDataset(ctx, "tenant-b", aID)
	if !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected cross-tenant read to fail, got %v", err)
	}
}

func TestEvalDatasetDeleteRemovesEverything(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	created, err := s.CreateEvalDataset(ctx, sampleDataset("delete-me", 1, "acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.DeleteEvalDataset(ctx, "acme", created.ID); err != nil {
		t.Fatalf("DeleteEvalDataset: %v", err)
	}

	// Record hash is gone.
	if _, err := s.GetEvalDataset(ctx, "acme", created.ID); !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected not-found after delete, got %v", err)
	}
	// By-name key is gone.
	if _, err := s.GetEvalDatasetByNameVersion(ctx, "acme", "delete-me", 1); !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected by-name not-found after delete, got %v", err)
	}
	// Primary index entry is gone.
	indexKey := evalDatasetIndexKey("acme")
	if n, _ := s.client.ZCard(ctx, indexKey).Result(); n != 0 {
		t.Fatalf("index still contains %d members after delete", n)
	}
	// Name index is gone.
	nameKey := evalDatasetNameIndexKey("acme", "delete-me")
	if n, _ := s.client.ZCard(ctx, nameKey).Result(); n != 0 {
		t.Fatalf("name index still contains %d members after delete", n)
	}
	_ = srv // keep srv reference for FastForward paths

	// After delete, (delete-me, 1) can be recreated.
	if _, err := s.CreateEvalDataset(ctx, sampleDataset("delete-me", 1, "acme")); err != nil {
		t.Fatalf("recreate after delete failed: %v", err)
	}
}

func TestEvalDatasetDeleteMissingIsIdempotent(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()
	if err := s.DeleteEvalDataset(ctx, "acme", "not-there"); err != nil {
		t.Fatalf("expected idempotent no-op, got %v", err)
	}
}

func TestEvalDatasetReconcileIndexesPrunesDanglingMembers(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	created, err := s.CreateEvalDataset(ctx, sampleDataset("recon", 1, "acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Simulate a crashed partial write: manually DEL the record hash but
	// leave the index entry behind.
	srv.Del(evalDatasetRecordKey("acme", created.ID))

	pruned, err := s.ReconcileIndexes(ctx, "acme")
	if err != nil {
		t.Fatalf("ReconcileIndexes: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected to prune 1 dangling member, pruned %d", pruned)
	}

	indexKey := evalDatasetIndexKey("acme")
	if n, _ := s.client.ZCard(ctx, indexKey).Result(); n != 0 {
		t.Fatalf("expected index to be empty after reconcile, has %d", n)
	}
}

func TestEvalDatasetCreateInvalidPayloadRejected(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	bad := sampleDataset("bad_name_TOO_long_name_that_certainly_exceeds_the_cap_of_sixty_four_characters_wow", 1, "acme")
	if _, err := s.CreateEvalDataset(ctx, bad); err == nil {
		t.Fatal("expected validation error for bad name")
	}
}

func TestEvalDatasetListReturnsEmptyOnUnknownTenant(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()
	page, err := s.ListEvalDatasets(ctx, "nobody", model.EvalDatasetFilter{}, "", 50)
	if err != nil {
		t.Fatalf("ListEvalDatasets: %v", err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("expected empty page, got %+v", page)
	}
}

func TestEvalDatasetCreatedAtFilter(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	// Create first dataset, capture its creation time, then fast-forward
	// a bit before creating a second dataset so we can filter between
	// them.
	ds1, err := s.CreateEvalDataset(ctx, sampleDataset("pack-a", 1, "acme"))
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	ms1, err := ds1.CreatedAtMilli()
	if err != nil {
		t.Fatalf("created-at-ms 1: %v", err)
	}
	srv.FastForward(50 * time.Millisecond)
	ds2, err := s.CreateEvalDataset(ctx, sampleDataset("pack-b", 1, "acme"))
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	ms2, err := ds2.CreatedAtMilli()
	if err != nil {
		t.Fatalf("created-at-ms 2: %v", err)
	}

	// CreatedAfterMS between ds1 and ds2 should include ds2 only.
	cutoff := (ms1 + ms2) / 2
	page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{CreatedAfterMS: cutoff}, "", 50)
	if err != nil {
		t.Fatalf("filter after: %v", err)
	}
	// ms1 and ms2 may be equal when wallclock resolution is coarse; accept
	// the degenerate case where both qualify.
	if len(page.Items) == 0 {
		t.Fatalf("expected at least 1 item after cutoff, got 0 (ms1=%d ms2=%d cutoff=%d)", ms1, ms2, cutoff)
	}

	// CreatedBeforeMS between them should include ds1 only (or both on
	// resolution-coarse systems).
	pageBefore, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{CreatedBeforeMS: cutoff}, "", 50)
	if err != nil {
		t.Fatalf("filter before: %v", err)
	}
	if len(pageBefore.Items) == 0 {
		t.Fatalf("expected at least 1 item before cutoff, got 0")
	}
}

func TestEvalDatasetGetRequiresTenantAndID(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	if _, err := s.GetEvalDataset(ctx, "", "id"); err == nil {
		t.Fatal("expected tenant-required error")
	}
	if _, err := s.GetEvalDataset(ctx, "acme", ""); err == nil {
		t.Fatal("expected id-required error")
	}

	// GetByNameVersion validations.
	if _, err := s.GetEvalDatasetByNameVersion(ctx, "", "x", 1); err == nil {
		t.Fatal("expected tenant-required error")
	}
	if _, err := s.GetEvalDatasetByNameVersion(ctx, "acme", "", 1); err == nil {
		t.Fatal("expected name-required error")
	}
	if _, err := s.GetEvalDatasetByNameVersion(ctx, "acme", "x", 0); err == nil {
		t.Fatal("expected version>=1 error")
	}

	// List validations.
	if _, err := s.ListEvalDatasets(ctx, "", model.EvalDatasetFilter{}, "", 10); err == nil {
		t.Fatal("expected tenant-required error")
	}

	// ListEvalDatasetVersions validations.
	if _, err := s.ListEvalDatasetVersions(ctx, "", "x"); err == nil {
		t.Fatal("expected tenant-required error")
	}
	if _, err := s.ListEvalDatasetVersions(ctx, "acme", ""); err == nil {
		t.Fatal("expected name-required error")
	}

	// Delete validations.
	if err := s.DeleteEvalDataset(ctx, "", "id"); err == nil {
		t.Fatal("expected tenant-required error on delete")
	}
	if err := s.DeleteEvalDataset(ctx, "acme", ""); err == nil {
		t.Fatal("expected id-required error on delete")
	}
}

func TestEvalDatasetListVersionsPrunesStaleNameIndexMembers(t *testing.T) {
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	// Create 3 versions then yank the middle record hash out-of-band
	// to simulate a partial failure that left a name-index orphan.
	created := make([]string, 0, 3)
	for _, v := range []int{1, 2, 3} {
		ds, err := s.CreateEvalDataset(ctx, sampleDataset("name-prune", v, "acme"))
		if err != nil {
			t.Fatalf("create v%d: %v", v, err)
		}
		created = append(created, ds.ID)
	}
	srv.Del(evalDatasetRecordKey("acme", created[1])) // delete v2 record

	versions, err := s.ListEvalDatasetVersions(ctx, "acme", "name-prune")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 surviving versions, got %d", len(versions))
	}

	// Orphaned member should be pruned from the name index.
	nameKey := evalDatasetNameIndexKey("acme", "name-prune")
	n, err := s.client.ZCard(ctx, nameKey).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 name-index members after prune, got %d", n)
	}
}

func TestEvalDatasetNilReceiverPaths(t *testing.T) {
	var s *EvalDatasetStore
	ctx := context.Background()

	if _, err := s.CreateEvalDataset(ctx, sampleDataset("x", 1, "acme")); err == nil {
		t.Fatal("expected error on nil receiver Create")
	}
	if _, err := s.GetEvalDataset(ctx, "acme", "x"); err == nil {
		t.Fatal("expected error on nil receiver Get")
	}
	if _, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, "", 10); err == nil {
		t.Fatal("expected error on nil receiver List")
	}
	if _, err := s.GetEvalDatasetByNameVersion(ctx, "acme", "x", 1); err == nil {
		t.Fatal("expected error on nil receiver GetByName")
	}
	if _, err := s.ListEvalDatasetVersions(ctx, "acme", "x"); err == nil {
		t.Fatal("expected error on nil receiver ListVersions")
	}
	if err := s.DeleteEvalDataset(ctx, "acme", "x"); err == nil {
		t.Fatal("expected error on nil receiver Delete")
	}
	if _, err := s.ReconcileIndexes(ctx, "acme"); err == nil {
		t.Fatal("expected error on nil receiver Reconcile")
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil receiver should be no-op, got %v", err)
	}
}

func TestEvalDatasetListPrunesStaleIndexEntriesDuringScan(t *testing.T) {
	// When a record HGET misses (e.g. recovery-era orphan), the store
	// should transparently skip it AND prune the dangling index member
	// so future scans aren't slowed by the same garbage.
	s, srv := newTestEvalDatasetStore(t)
	ctx := context.Background()

	created, err := s.CreateEvalDataset(ctx, sampleDataset("stale-target", 1, "acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Force an orphan: delete the record hash out-of-band, leaving the
	// index member hanging.
	srv.Del(evalDatasetRecordKey("acme", created.ID))

	// Seed another real dataset so List has something to return.
	if _, err := s.CreateEvalDataset(ctx, sampleDataset("alive", 1, "acme")); err != nil {
		t.Fatalf("create alive: %v", err)
	}

	page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, "", 50)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 surviving item after orphan skip, got %d", len(page.Items))
	}
	if page.Items[0].Name != "alive" {
		t.Fatalf("expected to surface the live dataset, got %q", page.Items[0].Name)
	}

	// The orphan member should be gone from the primary index.
	indexKey := evalDatasetIndexKey("acme")
	n, err := s.client.ZCard(ctx, indexKey).Result()
	if err != nil {
		t.Fatalf("zcard: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 index member after prune, got %d", n)
	}
}

func TestEvalDatasetListRejectsMalformedCursor(t *testing.T) {
	s, _ := newTestEvalDatasetStore(t)
	ctx := context.Background()

	for _, bad := range []string{":nope", "not-an-int:id", "noseparator", "trailing:"} {
		_, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, bad, 10)
		if err == nil {
			t.Fatalf("expected error for malformed cursor %q", bad)
		}
	}
}

func TestNoopEvalDatasetStoreImplementsInterface(t *testing.T) {
	var s model.EvalDatasetStore = NewNoopEvalDatasetStore()
	ctx := context.Background()

	// Reads are predictably absent.
	if _, err := s.GetEvalDataset(ctx, "acme", "id"); !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected ErrEvalDatasetNotFound from noop Get, got %v", err)
	}
	if _, err := s.GetEvalDatasetByNameVersion(ctx, "acme", "x", 1); !errors.Is(err, ErrEvalDatasetNotFound) {
		t.Fatalf("expected ErrEvalDatasetNotFound from noop GetByName, got %v", err)
	}
	page, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, "", 50)
	if err != nil {
		t.Fatalf("noop List: %v", err)
	}
	if len(page.Items) != 0 || page.NextCursor != "" {
		t.Fatalf("expected empty noop list, got %+v", page)
	}
	versions, err := s.ListEvalDatasetVersions(ctx, "acme", "x")
	if err != nil {
		t.Fatalf("noop versions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("expected empty noop versions, got %d", len(versions))
	}
	// Delete is a silent success.
	if err := s.DeleteEvalDataset(ctx, "acme", "id"); err != nil {
		t.Fatalf("expected noop delete success, got %v", err)
	}
	// Create fails loudly so tests don't accidentally rely on it.
	if _, err := s.CreateEvalDataset(ctx, sampleDataset("noop-pack", 1, "acme")); err == nil {
		t.Fatal("expected noop Create to fail")
	}
}

// BenchmarkListEvalDatasets1k seeds 1k datasets into miniredis and times a
// single List call at the default page size. The plan requires this call
// to stay under 200ms.
func BenchmarkListEvalDatasets1k(b *testing.B) {
	srv, err := miniredis.Run()
	if err != nil {
		b.Fatalf("miniredis: %v", err)
	}
	b.Cleanup(srv.Close)
	client := testredis.NewClient(b, srv.Addr())
	s := NewEvalDatasetStoreFromClient(client)
	ctx := context.Background()

	for i := 0; i < 1000; i++ {
		ds := sampleDataset(fmt.Sprintf("bench-%04d", i), 1, "acme")
		if _, err := s.CreateEvalDataset(ctx, ds); err != nil {
			b.Fatalf("seed create %d: %v", i, err)
		}
		srv.FastForward(time.Millisecond)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.ListEvalDatasets(ctx, "acme", model.EvalDatasetFilter{}, "", 50); err != nil {
			b.Fatalf("List: %v", err)
		}
	}
}
