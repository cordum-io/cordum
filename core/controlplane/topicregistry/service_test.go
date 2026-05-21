package topicregistry

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
)

func newTopicRegistryTestService(t *testing.T) (*Service, *configsvc.Service, func()) {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Skipf("miniredis unavailable: %v", err)
	}
	cfg, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		srv.Close()
		t.Fatalf("config service init: %v", err)
	}
	cleanup := func() {
		_ = cfg.Close()
		srv.Close()
	}
	return NewService(cfg), cfg, cleanup
}

func TestServiceAllowsSameTopicNameAcrossTenants(t *testing.T) {
	registry, _, cleanup := newTopicRegistryTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := registry.SetMany(ctx, []Registration{
		{Name: "job.shared", Pool: "pool-global", Status: StatusActive},
		{Name: "job.shared", TenantID: "tenant-a", Pool: "pool-a", Status: StatusActive},
		{Name: "job.shared", TenantID: "tenant-b", Pool: "pool-b", Status: StatusActive},
	}); err != nil {
		t.Fatalf("set registrations: %v", err)
	}

	reg, empty, err := registry.GetForTenant(ctx, "tenant-a", "job.shared")
	if err != nil {
		t.Fatalf("get tenant-a: %v", err)
	}
	if empty || reg == nil || reg.Pool != "pool-a" || reg.TenantID != "tenant-a" {
		t.Fatalf("tenant-a should see tenant-specific registration, got empty=%v reg=%+v", empty, reg)
	}

	reg, empty, err = registry.GetForTenant(ctx, "tenant-b", "job.shared")
	if err != nil {
		t.Fatalf("get tenant-b: %v", err)
	}
	if empty || reg == nil || reg.Pool != "pool-b" || reg.TenantID != "tenant-b" {
		t.Fatalf("tenant-b should see tenant-specific registration, got empty=%v reg=%+v", empty, reg)
	}

	reg, empty, err = registry.GetForTenant(ctx, "tenant-c", "job.shared")
	if err != nil {
		t.Fatalf("get tenant-c: %v", err)
	}
	if empty || reg == nil || reg.Pool != "pool-global" || reg.TenantID != "" {
		t.Fatalf("tenant-c should fall back to global registration, got empty=%v reg=%+v", empty, reg)
	}

	snap, err := registry.ListForTenant(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("list tenant-a: %v", err)
	}
	if len(snap.Items) != 1 || snap.Items[0].Name != "job.shared" || snap.Items[0].Pool != "pool-a" {
		t.Fatalf("tenant-a list should prefer tenant-specific record over global fallback: %+v", snap.Items)
	}

	all, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all.Items) != 3 {
		t.Fatalf("all registrations should retain all tenant variants, got %+v", all.Items)
	}
}

func TestLegacyTenantIDRecordIsRekeyedWithoutOverwritingOtherTenants(t *testing.T) {
	registry, cfg, cleanup := newTopicRegistryTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := cfg.Set(ctx, &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: scopeIDTopics,
		Data: map[string]any{
			"job.shared": map[string]any{
				"name":      "job.shared",
				"tenant_id": "tenant-a",
				"pool":      "pool-a",
				"status":    StatusActive,
			},
		},
	}); err != nil {
		t.Fatalf("seed legacy tenant record: %v", err)
	}

	if err := registry.Set(ctx, Registration{
		Name:     "job.shared",
		TenantID: "tenant-b",
		Pool:     "pool-b",
		Status:   StatusActive,
	}); err != nil {
		t.Fatalf("set tenant-b: %v", err)
	}

	doc, err := cfg.Get(ctx, configsvc.ScopeSystem, scopeIDTopics)
	if err != nil {
		t.Fatalf("reload topics doc: %v", err)
	}
	if _, ok := doc.Data["job.shared"]; ok {
		t.Fatalf("legacy single-tenant key should be migrated to tenant-aware storage: %+v", doc.Data)
	}
	if _, ok := doc.Data[topicStorageKey("tenant-a", "job.shared")]; !ok {
		t.Fatalf("tenant-a record missing after rekey: %+v", doc.Data)
	}
	if _, ok := doc.Data[topicStorageKey("tenant-b", "job.shared")]; !ok {
		t.Fatalf("tenant-b record missing after set: %+v", doc.Data)
	}
}

// TestServiceDeleteForTenant_ScopedDelete verifies that DeleteForTenant
// removes only the caller tenant's registrations and leaves other tenants'
// records intact. Regression for PR #276 audit finding (MEDIUM,
// topicregistry/service.go:192-228) — legacy Delete/DeleteMany match purely
// on Name and remove every tenant's record sharing that name.
func TestServiceDeleteForTenant_ScopedDelete(t *testing.T) {
	registry, _, cleanup := newTopicRegistryTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := registry.SetMany(ctx, []Registration{
		{Name: "job.shared", TenantID: "tenant-a", Pool: "pool-a", Status: StatusActive},
		{Name: "job.shared", TenantID: "tenant-b", Pool: "pool-b", Status: StatusActive},
		{Name: "job.shared", Pool: "pool-global", Status: StatusActive},
	}); err != nil {
		t.Fatalf("seed registrations: %v", err)
	}

	if err := registry.DeleteForTenant(ctx, "tenant-a", []string{"job.shared"}); err != nil {
		t.Fatalf("DeleteForTenant tenant-a: %v", err)
	}

	// tenant-a record must be gone.
	regA, _, err := registry.GetForTenant(ctx, "tenant-a", "job.shared")
	if err != nil {
		t.Fatalf("GetForTenant tenant-a: %v", err)
	}
	if regA == nil || regA.TenantID != "" {
		// After deleting tenant-a's record, the global record remains as fallback.
		if regA != nil && regA.TenantID == "" {
			// expected fallback to global
		} else {
			t.Fatalf("tenant-a should fall back to global after DeleteForTenant, got %+v", regA)
		}
	}

	// tenant-b record MUST be intact (cross-tenant isolation).
	regB, _, err := registry.GetForTenant(ctx, "tenant-b", "job.shared")
	if err != nil {
		t.Fatalf("GetForTenant tenant-b: %v", err)
	}
	if regB == nil || regB.Pool != "pool-b" || regB.TenantID != "tenant-b" {
		t.Fatalf("CROSS-TENANT DELETE: DeleteForTenant(tenant-a) destroyed tenant-b's record: %+v", regB)
	}

	// Global record MUST be intact.
	all, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawGlobal, sawB bool
	for _, item := range all.Items {
		if item.Name == "job.shared" && item.TenantID == "" {
			sawGlobal = true
		}
		if item.Name == "job.shared" && item.TenantID == "tenant-b" {
			sawB = true
		}
	}
	if !sawGlobal {
		t.Fatalf("global job.shared record destroyed by tenant-a delete: %+v", all.Items)
	}
	if !sawB {
		t.Fatalf("tenant-b job.shared record destroyed by tenant-a delete: %+v", all.Items)
	}
}
