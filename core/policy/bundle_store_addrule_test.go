package policy

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func newTestStorePair(t *testing.T) (*BundleRedisStore, *RuleRedisStore) {
	t.Helper()
	bundleStore := newTestBundleStore(t)
	ruleStore := NewRedisRuleStoreFromClient(bundleStore.client)
	return bundleStore, ruleStore
}

func ruleExistsFn(rs *RuleRedisStore) func(context.Context, string) (bool, error) {
	return func(ctx context.Context, ruleID string) (bool, error) {
		_, err := rs.GetRule(ctx, ruleID)
		if errors.Is(err, ErrRuleNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	}
}

func TestAddRuleToBundle(t *testing.T) {
	bundleStore, ruleStore := newTestStorePair(t)
	ctx := context.Background()

	mustCreateBundle(t, bundleStore, &Bundle{
		ID:           "bundle-1",
		Name:         "Test bundle",
		ScopeBinding: RuleScope{Kind: RuleScopeTenant, Value: "acme"},
	})
	if _, err := ruleStore.CreateRule(ctx, newSampleRule("rule-1")); err != nil {
		t.Fatalf("create rule: %v", err)
	}

	got, err := bundleStore.AddRuleToBundle(ctx, "bundle-1", "rule-1", ruleExistsFn(ruleStore))
	if err != nil {
		t.Fatalf("add rule to bundle: %v", err)
	}
	if len(got.RuleIDs) != 1 || got.RuleIDs[0] != "rule-1" {
		t.Errorf("RuleIDs = %v, want [rule-1]", got.RuleIDs)
	}
}

func TestAddRuleToBundleIdempotent(t *testing.T) {
	bundleStore, ruleStore := newTestStorePair(t)
	ctx := context.Background()
	mustCreateBundle(t, bundleStore, &Bundle{ID: "bundle-1", Name: "Test", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	if _, err := ruleStore.CreateRule(ctx, newSampleRule("rule-1")); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	for i := 0; i < 3; i++ {
		got, err := bundleStore.AddRuleToBundle(ctx, "bundle-1", "rule-1", ruleExistsFn(ruleStore))
		if err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		if len(got.RuleIDs) != 1 {
			t.Errorf("after add #%d: RuleIDs = %v, want exactly [rule-1] (idempotent)", i, got.RuleIDs)
		}
	}
}

func TestAddRuleToBundleRuleNotFound(t *testing.T) {
	bundleStore, ruleStore := newTestStorePair(t)
	mustCreateBundle(t, bundleStore, &Bundle{ID: "bundle-1", Name: "Test", ScopeBinding: RuleScope{Kind: RuleScopeGlobal}})
	_, err := bundleStore.AddRuleToBundle(context.Background(), "bundle-1", "missing", ruleExistsFn(ruleStore))
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("err = %v, want ErrRuleNotFound", err)
	}
}

func TestAddRuleToBundleBundleNotFound(t *testing.T) {
	bundleStore, ruleStore := newTestStorePair(t)
	ctx := context.Background()
	if _, err := ruleStore.CreateRule(ctx, newSampleRule("rule-1")); err != nil {
		t.Fatalf("create rule: %v", err)
	}
	_, err := bundleStore.AddRuleToBundle(ctx, "missing-bundle", "rule-1", ruleExistsFn(ruleStore))
	if !errors.Is(err, ErrBundleNotFound) {
		t.Errorf("err = %v, want ErrBundleNotFound", err)
	}
}

// TestAddRuleToBundleConcurrentDistinctIDs is the architect's load-bearing
// race test (msg-f38f9aff "Phase 8(f) AddRuleToBundle race test is the
// load-bearing one — do it before complete_task"). N goroutines each add
// a distinct ruleID to the same bundle; all N must succeed and the final
// RuleIDs list must contain all N entries with no lost writes. The Lua
// CAS retry inside AddRuleToBundle is what makes this safe.
func TestAddRuleToBundleConcurrentDistinctIDs(t *testing.T) {
	bundleStore, ruleStore := newTestStorePair(t)
	ctx := context.Background()
	mustCreateBundle(t, bundleStore, &Bundle{
		ID:           "bundle-race",
		Name:         "Concurrent bundle",
		ScopeBinding: RuleScope{Kind: RuleScopeGlobal},
	})
	const N = 10
	for i := 0; i < N; i++ {
		id := fmtRuleID(i)
		if _, err := ruleStore.CreateRule(ctx, newSampleRule(id)); err != nil {
			t.Fatalf("create rule %s: %v", id, err)
		}
	}

	var success, fail int32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			id := fmtRuleID(idx)
			_, err := bundleStore.AddRuleToBundle(ctx, "bundle-race", id, ruleExistsFn(ruleStore))
			if err == nil {
				atomic.AddInt32(&success, 1)
			} else {
				atomic.AddInt32(&fail, 1)
				t.Errorf("add %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&success); got != N {
		t.Errorf("successes = %d, want %d (every distinct ruleID add must succeed)", got, N)
	}
	if got := atomic.LoadInt32(&fail); got != 0 {
		t.Errorf("failures = %d, want 0 (Lua CAS-retry must converge)", got)
	}

	final, err := bundleStore.GetBundle(ctx, "bundle-race")
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if len(final.RuleIDs) != N {
		t.Errorf(
			"final RuleIDs has %d entries (got %v), want exactly %d (no lost writes)",
			len(final.RuleIDs), final.RuleIDs, N,
		)
	}
	seen := make(map[string]bool, N)
	for _, id := range final.RuleIDs {
		if seen[id] {
			t.Errorf("duplicate rule_id in final state: %s", id)
		}
		seen[id] = true
	}
}

func fmtRuleID(idx int) string {
	return "rule-race-" + ruleIndexLabel(idx)
}

func ruleIndexLabel(idx int) string {
	if idx < 10 {
		return "0" + string(rune('0'+idx))
	}
	tens := idx / 10
	ones := idx % 10
	return string(rune('0'+tens)) + string(rune('0'+ones))
}
