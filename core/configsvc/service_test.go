package configsvc

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func newSvc(t *testing.T) *Service {
	t.Helper()
	svc, _ := newSvcWithServer(t)
	return svc
}

func newSvcWithServer(t *testing.T) (*Service, *miniredis.Miniredis) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	svc, err := New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("svc init: %v", err)
	}
	return svc, srv
}

func TestSetGetEffective(t *testing.T) {
	svc := newSvc(t)
	defer func() { _ = svc.Close() }()

	ctx := context.Background()
	// system
	_ = svc.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"timeout": 60, "model": "gpt-4"},
	})
	// org override
	_ = svc.Set(ctx, &Document{
		Scope:   ScopeOrg,
		ScopeID: "org-1",
		Data:    map[string]any{"timeout": 30},
	})
	// team override
	_ = svc.Set(ctx, &Document{
		Scope:   ScopeTeam,
		ScopeID: "team-1",
		Data:    map[string]any{"budget": 100},
	})

	eff, err := svc.Effective(ctx, "org-1", "team-1", "", "")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}
	if timeout, ok := asInt(eff["timeout"]); !ok || timeout != 30 {
		t.Fatalf("expected timeout 30, got %v", eff["timeout"])
	}
	if eff["model"] != "gpt-4" {
		t.Fatalf("expected inherited model, got %v", eff["model"])
	}
	if budget, ok := asInt(eff["budget"]); !ok || budget != 100 {
		t.Fatalf("expected team budget, got %v", eff["budget"])
	}
}

func TestEnsureDefault_CreatesIfMissing(t *testing.T) {
	svc := newSvc(t)
	defer func() { _ = svc.Close() }()

	ctx := context.Background()

	// Should create the default config.
	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("ensure default: %v", err)
	}

	// Verify document exists with expected fields.
	doc, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get after ensure: %v", err)
	}
	if doc.Meta["source"] != "auto-bootstrap" {
		t.Fatalf("expected meta source=auto-bootstrap, got %v", doc.Meta["source"])
	}
	safety, ok := doc.Data["safety"].(map[string]any)
	if !ok {
		t.Fatalf("expected safety map, got %T", doc.Data["safety"])
	}
	if safety["enabled"] != true {
		t.Fatalf("expected safety.enabled=true, got %v", safety["enabled"])
	}
}

func TestEnsureDefault_NoOpIfExists(t *testing.T) {
	svc := newSvc(t)
	defer func() { _ = svc.Close() }()

	ctx := context.Background()

	// Seed a custom config.
	if err := svc.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"custom": "value"},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// EnsureDefault should be a no-op.
	if err := svc.EnsureDefault(ctx); err != nil {
		t.Fatalf("ensure default: %v", err)
	}

	// Verify the custom config is preserved (not overwritten).
	doc, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if doc.Data["custom"] != "value" {
		t.Fatalf("expected custom=value preserved, got %v", doc.Data["custom"])
	}
	// Should NOT have auto-bootstrap meta since original was preserved.
	if doc.Meta != nil && doc.Meta["source"] == "auto-bootstrap" {
		t.Fatalf("expected existing doc meta to be preserved, not overwritten with auto-bootstrap")
	}
}

func TestEffectiveSnapshot_RedisError(t *testing.T) {
	svc, srv := newSvcWithServer(t)
	defer func() { _ = svc.Close() }()

	ctx := context.Background()

	// Seed system config so there's something to read.
	if err := svc.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"safety": true},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Stop miniredis to simulate Redis outage.
	srv.Close()

	// EffectiveSnapshot must return an error — not silently return empty config.
	snap, err := svc.EffectiveSnapshot(ctx, "", "", "", "")
	if err == nil {
		t.Fatalf("expected error on Redis outage, got snapshot: %+v", snap)
	}
}

func TestSetOptimisticLocking(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	// Seed initial document
	doc := &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"key": "v1"},
	}
	if err := svc.Set(ctx, doc); err != nil {
		t.Fatalf("initial set: %v", err)
	}
	if doc.Revision != 1 {
		t.Fatalf("expected revision 1 after first set, got %d", doc.Revision)
	}

	// Two callers read the same revision
	docA, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get A: %v", err)
	}
	docB, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get B: %v", err)
	}

	// A writes first — should succeed
	docA.Data["key"] = "v2-from-A"
	if err := svc.Set(ctx, docA); err != nil {
		t.Fatalf("set A: %v", err)
	}
	if docA.Revision != 2 {
		t.Fatalf("expected revision 2 after A's set, got %d", docA.Revision)
	}

	// B writes with stale revision — should get conflict
	docB.Data["key"] = "v2-from-B"
	err = svc.Set(ctx, docB)
	if err == nil {
		t.Fatal("expected ErrRevisionConflict for stale write, got nil")
	}
	if err != ErrRevisionConflict {
		t.Fatalf("expected ErrRevisionConflict, got: %v", err)
	}
	// B's revision should not have changed
	if docB.Revision != 1 {
		t.Fatalf("expected B's revision to stay at 1 after conflict, got %d", docB.Revision)
	}

	// Verify A's write persisted
	final, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("final get: %v", err)
	}
	if final.Data["key"] != "v2-from-A" {
		t.Errorf("expected key=v2-from-A, got %v", final.Data["key"])
	}
}

func TestSetRevisionOnlyIncrementsOnSuccess(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	doc := &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"x": 1},
	}
	if err := svc.Set(ctx, doc); err != nil {
		t.Fatalf("set: %v", err)
	}
	if doc.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", doc.Revision)
	}

	// Second set should go to 2
	doc.Data["x"] = 2
	if err := svc.Set(ctx, doc); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	if doc.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", doc.Revision)
	}
}

func TestSetWithRetry(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	// Seed initial doc
	if err := svc.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"counter": float64(0)},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// SetWithRetry should handle conflicts gracefully
	err := svc.SetWithRetry(ctx, ScopeSystem, "default", 3, func(doc *Document) error {
		doc.Data["counter"] = float64(42)
		return nil
	})
	if err != nil {
		t.Fatalf("SetWithRetry: %v", err)
	}

	// Verify
	final, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v, ok := final.Data["counter"].(float64); !ok || v != 42 {
		t.Errorf("expected counter=42, got %v", final.Data["counter"])
	}
}

func TestSetWithRetryPreservesMissingSystemDefaultKeys(t *testing.T) {
	svc := newSvc(t)
	defer func() { _ = svc.Close() }()

	ctx := context.Background()
	if err := svc.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{
					"job.existing": "default",
				},
				"pools": map[string]any{
					"default": map[string]any{"requires": []any{}},
				},
			},
			"bundles": map[string]any{
				"pack/default": map[string]any{"content": "package policy"},
			},
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.SetWithRetry(ctx, ScopeSystem, "default", 3, func(doc *Document) error {
		doc.Data = map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{
					"job.new": "new-pool",
				},
			},
		}
		return nil
	}); err != nil {
		t.Fatalf("SetWithRetry: %v", err)
	}

	final, err := svc.Get(ctx, ScopeSystem, "default")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	bundles, ok := final.Data["bundles"].(map[string]any)
	if !ok || bundles["pack/default"] == nil {
		t.Fatalf("expected bundles preserved, got %v", final.Data["bundles"])
	}
	pools, ok := final.Data["pools"].(map[string]any)
	if !ok {
		t.Fatalf("expected pools map, got %T", final.Data["pools"])
	}
	topics, ok := pools["topics"].(map[string]any)
	if !ok {
		t.Fatalf("expected pools.topics map, got %T", pools["topics"])
	}
	if topics["job.existing"] != "default" {
		t.Fatalf("expected existing topic preserved, got %v", topics["job.existing"])
	}
	if topics["job.new"] != "new-pool" {
		t.Fatalf("expected new topic merged, got %v", topics["job.new"])
	}
	if _, ok := pools["pools"].(map[string]any); !ok {
		t.Fatalf("expected sibling pools.pools preserved, got %v", pools["pools"])
	}
}

func TestSetNewDocument(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	// Set a brand new document (no existing key in Redis)
	doc := &Document{
		Scope:   ScopeSystem,
		ScopeID: "fresh",
		Data:    map[string]any{"new": true},
	}
	if err := svc.Set(ctx, doc); err != nil {
		t.Fatalf("set new doc: %v", err)
	}
	if doc.Revision != 1 {
		t.Fatalf("expected revision 1 for new doc, got %d", doc.Revision)
	}
}

func TestMergeDeep_NestedMaps(t *testing.T) {
	dst := map[string]any{"a": map[string]any{"b": 1, "c": 2}}
	src := map[string]any{"a": map[string]any{"c": 3, "d": 4}}
	mergeDeep(dst, src)

	a, ok := dst["a"].(map[string]any)
	if !ok {
		t.Fatal("expected a to be map")
	}
	if v, _ := asInt(a["b"]); v != 1 {
		t.Errorf("expected a.b=1, got %v", a["b"])
	}
	if v, _ := asInt(a["c"]); v != 3 {
		t.Errorf("expected a.c=3, got %v", a["c"])
	}
	if v, _ := asInt(a["d"]); v != 4 {
		t.Errorf("expected a.d=4, got %v", a["d"])
	}
}

func TestMergeDeep_NonMapOverwrite(t *testing.T) {
	dst := map[string]any{"a": map[string]any{"b": 1}}
	src := map[string]any{"a": "string"}
	mergeDeep(dst, src)
	if dst["a"] != "string" {
		t.Errorf("expected a to be overwritten to 'string', got %v", dst["a"])
	}
}

func TestMergeDeep_DeeplyNested(t *testing.T) {
	dst := map[string]any{
		"l1": map[string]any{
			"l2": map[string]any{
				"l3": map[string]any{"keep": true, "old": "val"},
			},
		},
	}
	src := map[string]any{
		"l1": map[string]any{
			"l2": map[string]any{
				"l3": map[string]any{"new": "added", "old": "updated"},
			},
		},
	}
	mergeDeep(dst, src)

	l3 := dst["l1"].(map[string]any)["l2"].(map[string]any)["l3"].(map[string]any)
	if l3["keep"] != true {
		t.Error("expected keep=true preserved")
	}
	if l3["new"] != "added" {
		t.Error("expected new=added")
	}
	if l3["old"] != "updated" {
		t.Error("expected old=updated")
	}
}

func TestMergeDeep_NilValue(t *testing.T) {
	dst := map[string]any{"a": "existing", "b": "keep"}
	src := map[string]any{"a": nil}
	mergeDeep(dst, src)
	if dst["a"] != nil {
		t.Errorf("expected a=nil, got %v", dst["a"])
	}
	if dst["b"] != "keep" {
		t.Error("expected b=keep preserved")
	}
}

func TestEffective_PreservesSiblingKeys(t *testing.T) {
	svc := newSvc(t)
	ctx := context.Background()

	// System config with nested pools
	if err := svc.Set(ctx, &Document{
		Scope:   ScopeSystem,
		ScopeID: "default",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.default": "default"},
				"pools":  map[string]any{"default": map[string]any{}},
			},
		},
	}); err != nil {
		t.Fatalf("set system: %v", err)
	}

	// Org config adds a topic without losing pools.pools
	if err := svc.Set(ctx, &Document{
		Scope:   ScopeOrg,
		ScopeID: "org-1",
		Data: map[string]any{
			"pools": map[string]any{
				"topics": map[string]any{"job.visa": "visa-pool"},
			},
		},
	}); err != nil {
		t.Fatalf("set org: %v", err)
	}

	result, err := svc.Effective(ctx, "org-1", "", "", "")
	if err != nil {
		t.Fatalf("effective: %v", err)
	}

	pools, ok := result["pools"].(map[string]any)
	if !ok {
		t.Fatal("expected pools to be map")
	}
	topics, ok := pools["topics"].(map[string]any)
	if !ok {
		t.Fatal("expected pools.topics to be map")
	}
	// System topic preserved
	if topics["job.default"] != "default" {
		t.Error("expected system topic job.default preserved")
	}
	// Org topic added
	if topics["job.visa"] != "visa-pool" {
		t.Error("expected org topic job.visa added")
	}
	// System pools key preserved (sibling of topics)
	poolsDef, ok := pools["pools"].(map[string]any)
	if !ok {
		t.Fatal("expected pools.pools preserved from system config (sibling key)")
	}
	if _, ok := poolsDef["default"]; !ok {
		t.Error("expected pools.pools.default preserved")
	}
}
