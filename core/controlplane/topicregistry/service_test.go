package topicregistry

import (
	"context"
	"errors"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
	"github.com/redis/go-redis/v9"
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

func seedLegacyPoolTopic(t *testing.T, cfg *configsvc.Service, topic string) {
	t.Helper()
	if err := cfg.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: scopeIDDefault,
		Data: map[string]any{"pools": map[string]any{
			"topics": map[string]any{topic: "pool-a"},
			"pools":  map[string]any{"pool-a": map[string]any{}},
		}},
	}); err != nil {
		t.Fatalf("seed legacy pool topic: %v", err)
	}
}

// storedTopics returns the stored system/topics document, or nil when it was never written.
func storedTopics(t *testing.T, cfg *configsvc.Service) *configsvc.Document {
	t.Helper()
	doc, err := cfg.Get(context.Background(), configsvc.ScopeSystem, scopeIDTopics)
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		t.Fatalf("get system/topics: %v", err)
	}
	return doc
}

func TestStoredEmptyRegistryIsAuthoritative(t *testing.T) {
	registry, cfg, cleanup := newTopicRegistryTestService(t)
	defer cleanup()
	ctx := context.Background()
	seedLegacyPoolTopic(t, cfg, "job.legacy")
	if err := registry.Set(ctx, Registration{Name: "job.legacy", TenantID: "tenant-a", Pool: "pool-a", Status: StatusActive}); err != nil {
		t.Fatalf("set registration: %v", err)
	}
	if err := registry.DeleteForTenant(ctx, "tenant-a", []string{"job.legacy"}); err != nil {
		t.Fatalf("delete last registration: %v", err)
	}
	stored := storedTopics(t, cfg)
	if stored == nil || len(stored.Data) != 0 {
		t.Fatalf("emptied registry should be stored with no records: %+v", stored)
	}

	all, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got, getEmpty, err := registry.Get(ctx, "job.legacy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	tenantGot, tenantEmpty, err := registry.GetForTenant(ctx, "tenant-a", "job.legacy")
	if err != nil {
		t.Fatalf("get for tenant: %v", err)
	}
	tenantList, err := registry.ListForTenant(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("list for tenant: %v", err)
	}
	if len(all.Items) != 0 || all.RegistryEmpty || got != nil || getEmpty ||
		tenantGot != nil || tenantEmpty || len(tenantList.Items) != 0 || tenantList.RegistryEmpty {
		t.Errorf("stored empty registry: list=%+v get=%+v/%v tenant get=%+v/%v tenant list=%+v",
			all, got, getEmpty, tenantGot, tenantEmpty, tenantList)
	}
	if after := storedTopics(t, cfg); after == nil || after.Revision != stored.Revision || len(after.Data) != 0 {
		t.Errorf("reads changed system/topics: before revision %d, after %+v", stored.Revision, after)
	}
}

func TestUnwrittenRegistryStaysOpen(t *testing.T) {
	for _, tc := range []struct {
		name   string
		delete func(context.Context, *Service) error
	}{
		{"DeleteForTenant", func(ctx context.Context, s *Service) error {
			return s.DeleteForTenant(ctx, "tenant-a", []string{"job.unknown"})
		}},
		{"DeleteMany", func(ctx context.Context, s *Service) error {
			return s.DeleteMany(ctx, []string{"job.unknown"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry, cfg, cleanup := newTopicRegistryTestService(t)
			defer cleanup()
			ctx := context.Background()
			if err := tc.delete(ctx, registry); err != nil {
				t.Fatalf("delete: %v", err)
			}
			if doc := storedTopics(t, cfg); doc != nil {
				t.Errorf("delete of an unknown name stored system/topics: %+v", doc)
			}
			snap, err := registry.List(ctx)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			_, getEmpty, err := registry.Get(ctx, "job.unknown")
			if err != nil {
				t.Fatalf("get: %v", err)
			}
			if !snap.RegistryEmpty || len(snap.Items) != 0 || !getEmpty {
				t.Errorf("unwritten registry should stay open: list=%+v get empty=%v", snap, getEmpty)
			}
		})
	}
}

func TestLegacyPoolTopicsMigrateIntoUnwrittenRegistry(t *testing.T) {
	registry, cfg, cleanup := newTopicRegistryTestService(t)
	defer cleanup()
	ctx := context.Background()
	seedLegacyPoolTopic(t, cfg, "job.legacy")

	snap, err := registry.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if snap.RegistryEmpty || len(snap.Items) != 1 || snap.Items[0].Name != "job.legacy" ||
		snap.Items[0].TenantID != "" || snap.Items[0].Pool != "pool-a" {
		t.Fatalf("legacy mapping should migrate as a record with no tenant: %+v", snap)
	}
	stored := storedTopics(t, cfg)
	if stored == nil || len(stored.Data) != 1 {
		t.Fatalf("migration should store the record: %+v", stored)
	}
	if _, err := registry.List(ctx); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if after := storedTopics(t, cfg); after == nil || after.Revision != stored.Revision {
		t.Errorf("second read rewrote system/topics: before revision %d, after %+v", stored.Revision, after)
	}
}

func TestMigrationSkipsStoredRegistry(t *testing.T) {
	registry, cfg, cleanup := newTopicRegistryTestService(t)
	defer cleanup()
	ctx := context.Background()
	seedLegacyPoolTopic(t, cfg, "job.legacy")
	if err := cfg.Set(ctx, &configsvc.Document{Scope: configsvc.ScopeSystem, ScopeID: scopeIDTopics, Data: map[string]any{}}); err != nil {
		t.Fatalf("store empty registry: %v", err)
	}
	before := storedTopics(t, cfg)

	if _, err := registry.migrateFromLegacyPools(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if after := storedTopics(t, cfg); after == nil || after.Revision != before.Revision || len(after.Data) != 0 {
		t.Errorf("migration wrote into a stored registry: before revision %d, after %+v", before.Revision, after)
	}
}
