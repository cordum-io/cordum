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
	defer svc.Close()

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
	defer svc.Close()

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
	defer svc.Close()

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
	defer svc.Close()

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
