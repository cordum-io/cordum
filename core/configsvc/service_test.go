package configsvc

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
)

func newSvc(t *testing.T) *Service {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	svc, err := New("redis://" + srv.Addr())
	if err != nil {
		t.Fatalf("svc init: %v", err)
	}
	return svc
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
	if eff["timeout"] != 30 {
		t.Fatalf("expected timeout 30, got %v", eff["timeout"])
	}
	if eff["model"] != "gpt-4" {
		t.Fatalf("expected inherited model, got %v", eff["model"])
	}
	if eff["budget"] != 100 {
		t.Fatalf("expected team budget, got %v", eff["budget"])
	}
}
