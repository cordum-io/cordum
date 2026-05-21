package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/evals/runner"
	"github.com/cordum/cordum/core/internal/testredis"
)

func newTestEvalRunStore(t *testing.T) (*EvalRunStore, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(srv.Close)
	client := testredis.NewClient(t, srv.Addr())
	return NewEvalRunStoreFromClient(client), srv
}

func sampleRun(id, tenant, datasetID string, startedAt time.Time, score float64, regressions int) runner.RunResult {
	s := score
	return runner.RunResult{
		RunID:          id,
		Tenant:         tenant,
		DatasetID:      datasetID,
		DatasetName:    "unit-dataset",
		DatasetVersion: 1,
		PolicySnapshot: "snap-1",
		StartedAt:      startedAt,
		CompletedAt:    startedAt.Add(2 * time.Second),
		Summary: runner.RunSummary{
			Total:        10,
			Passed:       10 - regressions,
			Failed:       0,
			Regressions:  regressions,
			Errored:      0,
			ScorePercent: &s,
		},
		Entries: []runner.EntryResult{},
	}
}

func TestEvalRunCreateAndGet(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	r := sampleRun("run-1", "acme", "ds-1", time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC), 100.0, 0)
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	got, err := s.GetRun(ctx, "acme", "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.RunID != "run-1" || got.Tenant != "acme" || got.DatasetID != "ds-1" {
		t.Fatalf("unexpected: %+v", got)
	}
	if got.Summary.ScorePercent == nil || *got.Summary.ScorePercent != 100.0 {
		t.Fatalf("score not preserved: %+v", got.Summary)
	}
}

func TestEvalRunCreateDuplicateReturnsSentinel(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	r := sampleRun("run-dup", "acme", "ds-1", time.Now().UTC(), 100.0, 0)
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := s.CreateRun(ctx, r)
	if !errors.Is(err, ErrEvalRunAlreadyExists) {
		t.Fatalf("expected ErrEvalRunAlreadyExists, got %v", err)
	}
}

func TestEvalRunGetNotFound(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()
	if _, err := s.GetRun(ctx, "acme", "missing"); !errors.Is(err, ErrEvalRunNotFound) {
		t.Fatalf("expected ErrEvalRunNotFound, got %v", err)
	}
}

func TestEvalRunListByTime(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	base := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		r := sampleRun(fmt.Sprintf("run-%d", i), "acme", "ds-1", base.Add(time.Duration(i)*time.Second), 100.0, 0)
		if err := s.CreateRun(ctx, r); err != nil {
			t.Fatalf("CreateRun %d: %v", i, err)
		}
	}

	page, err := s.ListRuns(ctx, "acme", RunFilter{}, "", 3)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(page.Items))
	}
	// Newest-first: run-4, run-3, run-2
	if page.Items[0].RunID != "run-4" || page.Items[2].RunID != "run-2" {
		t.Fatalf("unexpected order: %s ... %s", page.Items[0].RunID, page.Items[2].RunID)
	}
	if page.NextCursor == "" {
		t.Fatal("expected next cursor with 5 total items and limit=3")
	}

	page2, err := s.ListRuns(ctx, "acme", RunFilter{}, page.NextCursor, 3)
	if err != nil {
		t.Fatalf("ListRuns page2: %v", err)
	}
	if len(page2.Items) != 2 {
		t.Fatalf("expected 2 remaining items, got %d", len(page2.Items))
	}
	if page2.Items[0].RunID != "run-1" || page2.Items[1].RunID != "run-0" {
		t.Fatalf("unexpected page2 order: %s, %s", page2.Items[0].RunID, page2.Items[1].RunID)
	}
}

func TestEvalRunListByDataset(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	base := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	for i, ds := range []string{"ds-a", "ds-b", "ds-a", "ds-c"} {
		r := sampleRun(fmt.Sprintf("run-%d", i), "acme", ds, base.Add(time.Duration(i)*time.Second), 100.0, 0)
		if err := s.CreateRun(ctx, r); err != nil {
			t.Fatalf("CreateRun %d: %v", i, err)
		}
	}

	page, err := s.ListRuns(ctx, "acme", RunFilter{DatasetID: "ds-a"}, "", 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 ds-a runs, got %d", len(page.Items))
	}
	for _, r := range page.Items {
		if r.DatasetID != "ds-a" {
			t.Fatalf("unexpected dataset: %s", r.DatasetID)
		}
	}
}

func TestEvalRunListRegressionFilter(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	if err := s.CreateRun(ctx, sampleRun("clean", "acme", "ds-1", time.Now().UTC(), 100.0, 0)); err != nil {
		t.Fatalf("create clean: %v", err)
	}
	if err := s.CreateRun(ctx, sampleRun("regressed", "acme", "ds-1", time.Now().UTC().Add(time.Second), 90.0, 1)); err != nil {
		t.Fatalf("create regressed: %v", err)
	}

	page, err := s.ListRuns(ctx, "acme", RunFilter{HasRegression: true}, "", 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].RunID != "regressed" {
		t.Fatalf("expected only regressed run, got %+v", page.Items)
	}
}

func TestEvalRunListMinScoreFilter(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	_ = s.CreateRun(ctx, sampleRun("high", "acme", "ds-1", base, 95.5, 0))
	_ = s.CreateRun(ctx, sampleRun("mid", "acme", "ds-1", base.Add(time.Second), 80.0, 0))
	_ = s.CreateRun(ctx, sampleRun("low", "acme", "ds-1", base.Add(2*time.Second), 50.0, 0))

	page, err := s.ListRuns(ctx, "acme", RunFilter{MinScore: 75.0, MinScoreSet: true}, "", 10)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected 2 runs above 75%%, got %d", len(page.Items))
	}
	for _, r := range page.Items {
		if r.Summary.ScorePercent == nil || *r.Summary.ScorePercent < 75.0 {
			t.Fatalf("min-score filter leaked: %+v", r)
		}
	}
}

func TestEvalRunDelete(t *testing.T) {
	s, srv := newTestEvalRunStore(t)
	ctx := context.Background()
	_ = srv

	r := sampleRun("run-del", "acme", "ds-1", time.Now().UTC(), 100.0, 0)
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if err := s.DeleteRun(ctx, "acme", "run-del"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if _, err := s.GetRun(ctx, "acme", "run-del"); !errors.Is(err, ErrEvalRunNotFound) {
		t.Fatalf("expected not-found post-delete, got %v", err)
	}
	// Idempotent: deleting a gone run is a no-op.
	if err := s.DeleteRun(ctx, "acme", "run-del"); err != nil {
		t.Fatalf("expected idempotent delete, got %v", err)
	}
	// Index members should be gone too.
	if n, _ := s.client.ZCard(ctx, evalRunIndexKey("acme")).Result(); n != 0 {
		t.Fatalf("primary index should be empty, has %d", n)
	}
}

func TestEvalRunTenantIsolation(t *testing.T) {
	s, _ := newTestEvalRunStore(t)
	ctx := context.Background()

	if err := s.CreateRun(ctx, sampleRun("run-a", "tenant-a", "ds-1", time.Now().UTC(), 100.0, 0)); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if err := s.CreateRun(ctx, sampleRun("run-b", "tenant-b", "ds-1", time.Now().UTC(), 100.0, 0)); err != nil {
		t.Fatalf("create b: %v", err)
	}

	pageA, _ := s.ListRuns(ctx, "tenant-a", RunFilter{}, "", 10)
	if len(pageA.Items) != 1 || pageA.Items[0].RunID != "run-a" {
		t.Fatalf("tenant-a leak: %+v", pageA.Items)
	}
	// Cross-tenant get must return not-found.
	if _, err := s.GetRun(ctx, "tenant-b", "run-a"); !errors.Is(err, ErrEvalRunNotFound) {
		t.Fatalf("cross-tenant get should return not-found, got %v", err)
	}
}

func TestEvalRunGCExpired(t *testing.T) {
	s, srv := newTestEvalRunStore(t)
	ctx := context.Background()

	r := sampleRun("run-gc", "acme", "ds-1", time.Now().UTC(), 100.0, 0)
	if err := s.CreateRun(ctx, r); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	// Simulate TTL expiry by manually deleting the record hash, leaving
	// the index member dangling.
	srv.Del(evalRunRecordKey("acme", "run-gc"))

	pruned, err := s.GCExpired(ctx, "acme")
	if err != nil {
		t.Fatalf("GCExpired: %v", err)
	}
	if pruned != 1 {
		t.Fatalf("expected 1 pruned, got %d", pruned)
	}
	if n, _ := s.client.ZCard(ctx, evalRunIndexKey("acme")).Result(); n != 0 {
		t.Fatalf("primary index should be empty post-GC, has %d", n)
	}
}

func TestEvalRunCursorSerialization(t *testing.T) {
	enc := encodeEvalRunCursor(1234567890, "run-id-x")
	ms, id, err := decodeEvalRunCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ms != 1234567890 || id != "run-id-x" {
		t.Fatalf("round-trip mismatch: ms=%d id=%q", ms, id)
	}
	for _, bad := range []string{"", ":id", "notnumeric:id", "trailing:", "12345"} {
		if _, _, err := decodeEvalRunCursor(bad); err == nil {
			t.Fatalf("expected error for malformed cursor %q", bad)
		}
	}
}

func TestEvalRunRoundTripMarshal(t *testing.T) {
	// The store uses encoding/json; round-trip must preserve the
	// nil/value distinction on ScorePercent so the wire encoding of
	// empty datasets stays null rather than 0.0.
	zero := 0.0
	cases := []runner.RunResult{
		{RunID: "no-score", Tenant: "acme", DatasetID: "ds", Summary: runner.RunSummary{Total: 0, ScorePercent: nil}},
		{RunID: "zero-score", Tenant: "acme", DatasetID: "ds", Summary: runner.RunSummary{Total: 5, ScorePercent: &zero}},
	}
	for _, r := range cases {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("marshal %s: %v", r.RunID, err)
		}
		var back runner.RunResult
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", r.RunID, err)
		}
		if (r.Summary.ScorePercent == nil) != (back.Summary.ScorePercent == nil) {
			t.Fatalf("%s: nil-vs-value mismatch after round trip", r.RunID)
		}
	}
}
