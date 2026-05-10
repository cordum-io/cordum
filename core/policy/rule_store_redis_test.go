package policy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newTestRuleStore(t *testing.T) *RuleRedisStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisRuleStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("rule store init: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		srv.Close()
	})
	// Lock the clock so audit timestamps are deterministic.
	store.WithNow(func() time.Time {
		return time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	})
	store.WithActor(func() string { return "test-actor" })
	return store
}

func newSampleRule(id string) *Rule {
	return &Rule{
		ID:          id,
		Name:        "Sample rule " + id,
		Type:        RuleTypeInput,
		Scope:       RuleScope{Kind: RuleScopeTenant, Value: "acme"},
		Match:       json.RawMessage(`{"topics":["*"]}`),
		Decide:      json.RawMessage(`{"type":"deny","reason":"test"}`),
		Description: "Sample rule for unit tests",
	}
}

func TestCreateRuleSetsServerMetadata(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	src := newSampleRule("rule-1")
	src.Version = "v999"            // Client tries to fake history.
	src.Audit.CreatedBy = "imposter" // Client tries to fake actor.

	got, err := store.CreateRule(ctx, src)
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	if got.ID != "rule-1" {
		t.Errorf("ID = %q, want rule-1", got.ID)
	}
	if got.Version != "v1" {
		t.Errorf("Version = %q, want v1 (server-set)", got.Version)
	}
	if got.Audit.CreatedBy != "test-actor" {
		t.Errorf("CreatedBy = %q, want test-actor (client-set value rejected)", got.Audit.CreatedBy)
	}
	if got.Audit.CreatedAt.IsZero() {
		t.Error("CreatedAt should be server-populated")
	}
	if got.Audit.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should be server-populated")
	}
	if got.Status != RuleStatusDraft {
		t.Errorf("Status = %q, want draft (server default)", got.Status)
	}
}

func TestCreateRuleDuplicateIDReturnsErrRuleExists(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	if _, err := store.CreateRule(ctx, newSampleRule("rule-1")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := store.CreateRule(ctx, newSampleRule("rule-1"))
	if !errors.Is(err, ErrRuleExists) {
		t.Errorf("err = %v, want ErrRuleExists", err)
	}
}

func TestGetRuleNotFound(t *testing.T) {
	store := newTestRuleStore(t)
	_, err := store.GetRule(context.Background(), "missing")
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("err = %v, want ErrRuleNotFound", err)
	}
}

func TestGetRuleReturnsPersisted(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	created, err := store.CreateRule(ctx, newSampleRule("rule-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetRule(ctx, "rule-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != created.Version {
		t.Errorf("Version = %q, want %q", got.Version, created.Version)
	}
	if got.Name != created.Name {
		t.Errorf("Name = %q, want %q", got.Name, created.Name)
	}
}

func TestUpdateRuleBumpsVersion(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	created, err := store.CreateRule(ctx, newSampleRule("rule-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated := *created
	updated.Name = "Updated name"
	updated.Description = "Updated description"
	got, err := store.UpdateRule(ctx, &updated, "v1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Version != "v2" {
		t.Errorf("Version = %q, want v2", got.Version)
	}
	if got.Name != "Updated name" {
		t.Errorf("Name not propagated: got %q", got.Name)
	}
	if !got.Audit.UpdatedAt.After(created.Audit.UpdatedAt) &&
		!got.Audit.UpdatedAt.Equal(created.Audit.UpdatedAt) {
		// With deterministic clock both should be equal — accept either.
		t.Errorf("UpdatedAt regressed: %v vs %v", got.Audit.UpdatedAt, created.Audit.UpdatedAt)
	}
	if got.Audit.CreatedAt != created.Audit.CreatedAt {
		t.Errorf("CreatedAt should be preserved across updates: got %v vs %v",
			got.Audit.CreatedAt, created.Audit.CreatedAt)
	}
}

func TestUpdateRuleStaleVersionReturnsTypedError(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	created, err := store.CreateRule(ctx, newSampleRule("rule-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	updated := *created
	updated.Name = "First update"
	if _, err := store.UpdateRule(ctx, &updated, "v1"); err != nil {
		t.Fatalf("first update: %v", err)
	}
	// Caller still thinks version is v1 — they're stale.
	_, err = store.UpdateRule(ctx, &updated, "v1")
	if err == nil {
		t.Fatal("expected stale-version error, got nil")
	}
	stale, ok := IsStaleVersionError(err)
	if !ok {
		t.Fatalf("err = %v, want *ErrRuleStaleVersion", err)
	}
	if stale.CurrentVersion != "v2" {
		t.Errorf("CurrentVersion = %q, want v2", stale.CurrentVersion)
	}
	if stale.CurrentAuditHash == "" {
		t.Error("CurrentAuditHash should be populated for the reload-banner")
	}
}

func TestUpdateRuleNotFound(t *testing.T) {
	store := newTestRuleStore(t)
	r := newSampleRule("rule-missing")
	r.Version = "v1"
	_, err := store.UpdateRule(context.Background(), r, "v1")
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("err = %v, want ErrRuleNotFound", err)
	}
}

func TestDeleteRule(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	if _, err := store.CreateRule(ctx, newSampleRule("rule-1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.DeleteRule(ctx, "rule-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetRule(ctx, "rule-1"); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("post-delete get err = %v, want ErrRuleNotFound", err)
	}
}

func TestDeleteRuleNotFound(t *testing.T) {
	store := newTestRuleStore(t)
	err := store.DeleteRule(context.Background(), "missing")
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("err = %v, want ErrRuleNotFound", err)
	}
}

func TestListRulesByScope(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	scopeAcme := RuleScope{Kind: RuleScopeTenant, Value: "acme"}
	scopeOther := RuleScope{Kind: RuleScopeTenant, Value: "other"}

	for _, id := range []string{"rule-c", "rule-a", "rule-b"} {
		r := newSampleRule(id)
		r.Scope = scopeAcme
		if _, err := store.CreateRule(ctx, r); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	other := newSampleRule("rule-z")
	other.Scope = scopeOther
	if _, err := store.CreateRule(ctx, other); err != nil {
		t.Fatalf("create other: %v", err)
	}

	got, err := store.ListRulesByScope(ctx, scopeAcme)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	wantIDs := []string{"rule-a", "rule-b", "rule-c"}
	for i, r := range got {
		if r.ID != wantIDs[i] {
			t.Errorf("got[%d].ID = %q, want %q (sorted)", i, r.ID, wantIDs[i])
		}
	}
}

func TestConcurrentCreateOnlyOneSucceeds(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	const N = 8
	var success, exists int32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, err := store.CreateRule(ctx, newSampleRule("rule-race"))
			if err == nil {
				atomic.AddInt32(&success, 1)
			} else if errors.Is(err, ErrRuleExists) {
				atomic.AddInt32(&exists, 1)
			}
		}()
	}
	wg.Wait()
	if got := atomic.LoadInt32(&success); got != 1 {
		t.Errorf("successes = %d, want exactly 1 (Lua atomicity)", got)
	}
	if got := atomic.LoadInt32(&exists); got != N-1 {
		t.Errorf("exists errors = %d, want %d", got, N-1)
	}
}

func TestConcurrentUpdateOnlyOneVersionWins(t *testing.T) {
	store := newTestRuleStore(t)
	ctx := context.Background()
	if _, err := store.CreateRule(ctx, newSampleRule("rule-1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	const N = 6
	var success, stale, otherErr int32
	var firstOther error
	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			r := newSampleRule("rule-1")
			r.Name = "Concurrent update"
			_, err := store.UpdateRule(ctx, r, "v1")
			if err == nil {
				atomic.AddInt32(&success, 1)
			} else if _, ok := IsStaleVersionError(err); ok {
				atomic.AddInt32(&stale, 1)
			} else {
				atomic.AddInt32(&otherErr, 1)
				mu.Lock()
				if firstOther == nil {
					firstOther = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstOther != nil {
		t.Logf("first non-stale error: %v", firstOther)
	}
	t.Logf("counters: success=%d stale=%d other=%d",
		atomic.LoadInt32(&success), atomic.LoadInt32(&stale), atomic.LoadInt32(&otherErr))
	if got := atomic.LoadInt32(&success); got != 1 {
		t.Errorf("successful updates = %d, want exactly 1 (CAS atomicity)", got)
	}
	if got := atomic.LoadInt32(&stale); got != N-1 {
		t.Errorf("stale errors = %d, want %d", got, N-1)
	}
	final, err := store.GetRule(ctx, "rule-1")
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if final.Version != "v2" {
		t.Errorf("final Version = %q, want v2", final.Version)
	}
}
