package policy

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newTestBundleStore(t *testing.T) *BundleRedisStore {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	store, err := NewRedisBundleStore("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("store init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(); srv.Close() })
	return store
}

func mustCreateBundle(t *testing.T, s *BundleRedisStore, b *Bundle) {
	t.Helper()
	if err := s.CreateBundle(context.Background(), b); err != nil {
		t.Fatalf("create bundle: %v", err)
	}
}

func mustCreateVersion(t *testing.T, s *BundleRedisStore, bundleID string, v *BundleVersion) {
	t.Helper()
	if err := s.CreateBundleVersion(context.Background(), bundleID, v); err != nil {
		t.Fatalf("create version: %v", err)
	}
}

func TestCreateGetBundle(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()

	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}
	b := &Bundle{ID: "b1", Name: "Acme baseline", ScopeBinding: scope}
	mustCreateBundle(t, store, b)

	got, err := store.GetBundle(ctx, "b1")
	if err != nil {
		t.Fatalf("get bundle: %v", err)
	}
	if got.ID != "b1" || got.Name != "Acme baseline" {
		t.Errorf("got bundle = %+v, want id=b1 name='Acme baseline'", got)
	}
	if got.ScopeBinding.Kind != RuleScopeTenant || got.ScopeBinding.Value != "acme" {
		t.Errorf("scope binding mismatch: %+v", got.ScopeBinding)
	}

	// Duplicate create returns ErrBundleExists.
	err = store.CreateBundle(ctx, b)
	if !errors.Is(err, ErrBundleExists) {
		t.Errorf("duplicate create should return ErrBundleExists, got %v", err)
	}
}

func TestGetBundle_NotFound(t *testing.T) {
	store := newTestBundleStore(t)
	_, err := store.GetBundle(context.Background(), "missing")
	if !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("expected ErrBundleNotFound, got %v", err)
	}
}

func TestListBundlesByScope(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()

	scopeA := RuleScope{Kind: RuleScopeTenant, Value: "acme"}
	scopeB := RuleScope{Kind: RuleScopeTenant, Value: "beta"}
	scopeGlobal := RuleScope{Kind: RuleScopeGlobal}

	mustCreateBundle(t, store, &Bundle{ID: "b-a1", Name: "Acme one", ScopeBinding: scopeA})
	mustCreateBundle(t, store, &Bundle{ID: "b-a2", Name: "Acme two", ScopeBinding: scopeA})
	mustCreateBundle(t, store, &Bundle{ID: "b-b1", Name: "Beta one", ScopeBinding: scopeB})
	mustCreateBundle(t, store, &Bundle{ID: "b-g", Name: "Global", ScopeBinding: scopeGlobal})

	got, err := store.ListBundlesByScope(ctx, scopeA)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 bundles for acme, got %d", len(got))
	}

	got, err = store.ListBundlesByScope(ctx, scopeGlobal)
	if err != nil {
		t.Fatalf("list global: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 global bundle, got %d", len(got))
	}
}

func TestCreateBundleVersion_Idempotent(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	v := &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()}
	mustCreateVersion(t, store, "b1", v)

	err := store.CreateBundleVersion(ctx, "b1", v)
	if !errors.Is(err, ErrBundleVersionExists) {
		t.Errorf("duplicate version should return ErrBundleVersionExists, got %v", err)
	}
}

func TestListBundleVersions_OrderByDeployedAt(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	t0 := time.Now().UTC()
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v3", DeployedAt: t0.Add(3 * time.Second)})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: t0})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: t0.Add(time.Second)})

	got, err := store.ListBundleVersions(ctx, "b1")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(got))
	}
	want := []string{"v1", "v2", "v3"}
	for i, v := range got {
		if v.Version != want[i] {
			t.Errorf("position %d: got %q, want %q", i, v.Version, want[i])
		}
	}
}

func TestGetBundleVersion_NotFound(t *testing.T) {
	store := newTestBundleStore(t)
	_, err := store.GetBundleVersion(context.Background(), "b1", "v1")
	if !errors.Is(err, ErrBundleVersionNotFound) {
		t.Errorf("expected ErrBundleVersionNotFound, got %v", err)
	}
}

func TestDeployVersionToScope_FirstDeploy(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})

	dep, err := store.DeployVersionToScope(ctx, "b1", "v1", scope)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if dep.BundleID != "b1" || dep.Version != "v1" || dep.Action != DeploymentActionDeploy {
		t.Errorf("got deployment = %+v", dep)
	}

	active, err := store.GetActiveDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.BundleID != "b1" || active.Version != "v1" {
		t.Errorf("active = %+v, want b1:v1", active)
	}

	hist, err := store.ListDeploymentHistory(ctx, scope, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(hist))
	}
}

func TestDeployVersionToScope_OverwritesActive(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: time.Now().UTC().Add(time.Second)})

	if _, err := store.DeployVersionToScope(ctx, "b1", "v1", scope); err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	if _, err := store.DeployVersionToScope(ctx, "b1", "v2", scope); err != nil {
		t.Fatalf("deploy v2: %v", err)
	}

	active, err := store.GetActiveDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.Version != "v2" {
		t.Errorf("active version = %q, want v2", active.Version)
	}

	hist, err := store.ListDeploymentHistory(ctx, scope, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(hist))
	}
	// Newest first.
	if hist[0].Version != "v2" || hist[1].Version != "v1" {
		t.Errorf("history order wrong: %+v", hist)
	}
}

func TestDeployVersionToScope_VersionNotFound(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	_, err := store.DeployVersionToScope(ctx, "b1", "ghost", scope)
	if !errors.Is(err, ErrBundleVersionNotFound) {
		t.Errorf("expected ErrBundleVersionNotFound, got %v", err)
	}
}

func TestRollbackDeployment(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: time.Now().UTC().Add(time.Second)})

	if _, err := store.DeployVersionToScope(ctx, "b1", "v1", scope); err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	if _, err := store.DeployVersionToScope(ctx, "b1", "v2", scope); err != nil {
		t.Fatalf("deploy v2: %v", err)
	}

	rb, err := store.RollbackDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if rb.Version != "v1" || rb.Action != DeploymentActionRollback {
		t.Errorf("rollback record = %+v, want v1 action=rollback", rb)
	}

	active, err := store.GetActiveDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("get active after rollback: %v", err)
	}
	if active.Version != "v1" {
		t.Errorf("active after rollback = %q, want v1", active.Version)
	}

	hist, err := store.ListDeploymentHistory(ctx, scope, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 3 {
		t.Errorf("expected 3 history entries (deploy v1, deploy v2, rollback to v1), got %d", len(hist))
	}
	if hist[0].Action != DeploymentActionRollback {
		t.Errorf("newest entry should be rollback, got %s", hist[0].Action)
	}
}

func TestRollbackDeployment_NoHistory(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	if _, err := store.DeployVersionToScope(ctx, "b1", "v1", scope); err != nil {
		t.Fatalf("deploy: %v", err)
	}

	_, err := store.RollbackDeployment(ctx, scope)
	if !errors.Is(err, ErrNoRollbackTarget) {
		t.Errorf("expected ErrNoRollbackTarget, got %v", err)
	}
}

func TestGetActiveDeployment_NoDeployment(t *testing.T) {
	store := newTestBundleStore(t)
	scope := RuleScope{Kind: RuleScopeTenant, Value: "ghost"}
	_, err := store.GetActiveDeployment(context.Background(), scope)
	if !errors.Is(err, ErrNoDeploymentForScope) {
		t.Errorf("expected ErrNoDeploymentForScope, got %v", err)
	}
}

func TestScopeIsolation(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scopeA := RuleScope{Kind: RuleScopeTenant, Value: "acme"}
	scopeB := RuleScope{Kind: RuleScopeTenant, Value: "beta"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: time.Now().UTC().Add(time.Second)})

	// Same bundle deployed to two scopes; rollback on A should not touch B.
	if _, err := store.DeployVersionToScope(ctx, "b1", "v1", scopeA); err != nil {
		t.Fatalf("A v1: %v", err)
	}
	if _, err := store.DeployVersionToScope(ctx, "b1", "v2", scopeA); err != nil {
		t.Fatalf("A v2: %v", err)
	}
	if _, err := store.DeployVersionToScope(ctx, "b1", "v2", scopeB); err != nil {
		t.Fatalf("B v2: %v", err)
	}

	if _, err := store.RollbackDeployment(ctx, scopeA); err != nil {
		t.Fatalf("rollback A: %v", err)
	}

	activeA, _ := store.GetActiveDeployment(ctx, scopeA)
	activeB, _ := store.GetActiveDeployment(ctx, scopeB)
	if activeA.Version != "v1" {
		t.Errorf("scope A after rollback = %q, want v1", activeA.Version)
	}
	if activeB.Version != "v2" {
		t.Errorf("scope B should be untouched at v2, got %q", activeB.Version)
	}
}

func TestEmptyIDInputs(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()

	if err := store.CreateBundle(ctx, nil); err == nil {
		t.Errorf("CreateBundle(nil) should error")
	}
	if err := store.CreateBundle(ctx, &Bundle{ID: ""}); err == nil {
		t.Errorf("CreateBundle empty id should error")
	}
	if _, err := store.GetBundle(ctx, ""); err == nil {
		t.Errorf("GetBundle empty id should error")
	}
	if err := store.CreateBundleVersion(ctx, "", &BundleVersion{Version: "v1"}); err == nil {
		t.Errorf("CreateBundleVersion empty bundle id should error")
	}
	if err := store.CreateBundleVersion(ctx, "b1", nil); err == nil {
		t.Errorf("CreateBundleVersion nil should error")
	}
	if _, err := store.GetBundleVersion(ctx, "", "v1"); err == nil {
		t.Errorf("GetBundleVersion empty bundle id should error")
	}
	if _, err := store.ListBundleVersions(ctx, ""); err == nil {
		t.Errorf("ListBundleVersions empty id should error")
	}
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}
	if _, err := store.DeployVersionToScope(ctx, "", "v1", scope); err == nil {
		t.Errorf("DeployVersionToScope empty bundle id should error")
	}
}

func TestListBundleVersions_EmptyBundle(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	got, err := store.ListBundleVersions(ctx, "b1")
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected zero versions, got %d", len(got))
	}
}

func TestListBundlesByScope_NoMatches(t *testing.T) {
	store := newTestBundleStore(t)
	got, err := store.ListBundlesByScope(context.Background(), RuleScope{Kind: RuleScopeTenant, Value: "ghost"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected zero bundles for ghost tenant, got %d", len(got))
	}
}

func TestRollbackChain(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: time.Now().UTC().Add(time.Second)})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v3", DeployedAt: time.Now().UTC().Add(2 * time.Second)})

	if _, err := store.DeployVersionToScope(ctx, "b1", "v1", scope); err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	if _, err := store.DeployVersionToScope(ctx, "b1", "v2", scope); err != nil {
		t.Fatalf("deploy v2: %v", err)
	}
	if _, err := store.DeployVersionToScope(ctx, "b1", "v3", scope); err != nil {
		t.Fatalf("deploy v3: %v", err)
	}

	// Rollback: v3 → v2.
	rb1, err := store.RollbackDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("rollback 1: %v", err)
	}
	if rb1.Version != "v2" {
		t.Errorf("rollback 1 → %q, want v2", rb1.Version)
	}

	// Rollback again: should walk past the rollback marker to find v1.
	rb2, err := store.RollbackDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("rollback 2: %v", err)
	}
	if rb2.Version != "v1" {
		t.Errorf("rollback 2 → %q, want v1 (skipping the rollback marker)", rb2.Version)
	}
}

// TestDeployAfterRollback locks in the regression that originally
// shipped to QA in PR #252 reopen #1 (task-b349524a). Sequence:
//
//	deploy v1, deploy v2, rollback (-> v1), deploy v3, rollback
//
// The final rollback must restore v1 — the active state immediately
// before v3 was deployed — NOT v2 (the second-most-recent deploy in
// raw event order). The original implementation walked history for
// "next deploy after the matching deploy" and returned v2; the fix
// stores prev_bundle_id + prev_version on each deploy event inside
// Lua so rollback restores from the matching deploy's prev fields.
func TestDeployAfterRollback(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: time.Now().UTC().Add(time.Second)})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v3", DeployedAt: time.Now().UTC().Add(2 * time.Second)})

	// Step 1+2: deploy v1, deploy v2.
	d1, err := store.DeployVersionToScope(ctx, "b1", "v1", scope)
	if err != nil {
		t.Fatalf("deploy v1: %v", err)
	}
	if d1.PrevBundleID != "" || d1.PrevVersion != "" {
		t.Errorf("first deploy should record empty prev pair, got %s:%s", d1.PrevBundleID, d1.PrevVersion)
	}
	d2, err := store.DeployVersionToScope(ctx, "b1", "v2", scope)
	if err != nil {
		t.Fatalf("deploy v2: %v", err)
	}
	if d2.PrevBundleID != "b1" || d2.PrevVersion != "v1" {
		t.Errorf("v2 deploy should record prev=b1:v1, got %s:%s", d2.PrevBundleID, d2.PrevVersion)
	}

	// Step 3: rollback v2 → v1.
	rb1, err := store.RollbackDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("rollback after v2: %v", err)
	}
	if rb1.Version != "v1" {
		t.Fatalf("rollback after v2 should restore v1, got %q", rb1.Version)
	}
	active, err := store.GetActiveDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("active after rb1: %v", err)
	}
	if active.Version != "v1" {
		t.Fatalf("active after rb1 = %q, want v1", active.Version)
	}

	// Step 4: deploy v3 (active state immediately before this deploy is v1).
	d3, err := store.DeployVersionToScope(ctx, "b1", "v3", scope)
	if err != nil {
		t.Fatalf("deploy v3: %v", err)
	}
	if d3.PrevBundleID != "b1" || d3.PrevVersion != "v1" {
		t.Errorf("v3 deploy should record prev=b1:v1 (post-rollback active), got %s:%s", d3.PrevBundleID, d3.PrevVersion)
	}

	// Step 5: final rollback must restore v1, NOT v2 (the prior deploy
	// in raw history order).
	rb2, err := store.RollbackDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("rollback after v3: %v", err)
	}
	if rb2.Version != "v1" {
		t.Fatalf("rollback after deploy-after-rollback should restore v1, got %q (regression: returns the second-most-recent raw deploy v2 instead of the active state before v3)", rb2.Version)
	}
	active, err = store.GetActiveDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("active after rb2: %v", err)
	}
	if active.Version != "v1" {
		t.Fatalf("active after rb2 = %q, want v1", active.Version)
	}
}

// TestCreateBundleVersion_OrphanRejected verifies CreateBundleVersion
// refuses to write a version for a non-existent parent bundle. Without
// this check the store could accumulate orphan version blobs +
// deployment records that point at a bundle envelope nothing owns.
func TestCreateBundleVersion_OrphanRejected(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()

	// No CreateBundle call — parent is absent.
	err := store.CreateBundleVersion(ctx, "ghost", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	if !errors.Is(err, ErrBundleNotFound) {
		t.Fatalf("orphan version creation should return ErrBundleNotFound, got %v", err)
	}

	// And the version blob must NOT have been written. Reading it back
	// is the cleanest proof; ListBundleVersions on the same bundle ID
	// must return an empty slice (the index ZSET also stays empty).
	got, err := store.ListBundleVersions(ctx, "ghost")
	if err != nil {
		t.Fatalf("list versions for orphan parent: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("orphan parent should have no versions, got %d", len(got))
	}
}

// TestDeploymentHistoryCapEnforced verifies the LTRIM-based history
// cap inside the deploy Lua script. After 105 deploys to the same
// scope, history must hold exactly 100 entries (the cap from
// deploymentHistoryCap) and the 5 oldest deploy events must have been
// dropped.
func TestDeploymentHistoryCapEnforced(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})

	const total = 105
	for i := range total {
		if _, err := store.DeployVersionToScope(ctx, "b1", "v1", scope); err != nil {
			t.Fatalf("deploy %d: %v", i, err)
		}
	}

	hist, err := store.ListDeploymentHistory(ctx, scope, deploymentHistoryCap+50)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if len(hist) != deploymentHistoryCap {
		t.Fatalf("history len = %d, want %d (LTRIM cap)", len(hist), deploymentHistoryCap)
	}
	// Newest 100 entries are kept; the 5 oldest dropped. Every entry
	// is a deploy of v1, so we can't distinguish "oldest" by version,
	// but len(hist) == cap is the binding assertion that LTRIM ran.
	for i, dep := range hist {
		if dep.Action != DeploymentActionDeploy {
			t.Errorf("history[%d].Action = %s, want deploy", i, dep.Action)
		}
		if dep.Version != "v1" {
			t.Errorf("history[%d].Version = %s, want v1", i, dep.Version)
		}
	}
}

func TestConcurrentDeploy(t *testing.T) {
	store := newTestBundleStore(t)
	ctx := context.Background()
	scope := RuleScope{Kind: RuleScopeTenant, Value: "acme"}

	mustCreateBundle(t, store, &Bundle{ID: "b1", Name: "B", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v1", DeployedAt: time.Now().UTC()})
	mustCreateVersion(t, store, "b1", &BundleVersion{Version: "v2", DeployedAt: time.Now().UTC().Add(time.Second)})

	// Two goroutines deploy v1 + v2 to the same scope concurrently. Lua
	// EVAL serializes Redis-side, so one fully completes before the other.
	// Both deploys must land in history; the active pointer is whichever
	// goroutine's SET ran last.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = store.DeployVersionToScope(ctx, "b1", "v1", scope)
	}()
	go func() {
		defer wg.Done()
		_, _ = store.DeployVersionToScope(ctx, "b1", "v2", scope)
	}()
	wg.Wait()

	hist, err := store.ListDeploymentHistory(ctx, scope, 100)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 {
		t.Errorf("expected both deploys in history, got %d", len(hist))
	}

	active, err := store.GetActiveDeployment(ctx, scope)
	if err != nil {
		t.Fatalf("get active: %v", err)
	}
	if active.Version != "v1" && active.Version != "v2" {
		t.Errorf("active version should be v1 or v2, got %q", active.Version)
	}
}
