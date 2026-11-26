package topicregistry

import (
	"context"
	"reflect"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
)

func TestTenantIsolationTopicRegistryReadOnly(t *testing.T) {
	s, cfg, cleanup := newTopicRegistryTestService(t)
	defer cleanup()
	ctx := context.Background()
	if err := s.Set(ctx, Registration{Name: "job.private-b", TenantID: "tenant-b", Pool: "private-pool-b", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	before, empty, err := s.GetForTenant(ctx, "tenant-b", "job.private-b")
	if err != nil || empty || before == nil || before.Pool != "private-pool-b" {
		t.Fatalf("seed read: %v, %v", before, err)
	}
	doc, err := cfg.Get(ctx, configsvc.ScopeSystem, scopeIDTopics)
	if err != nil || doc == nil {
		t.Fatalf("seed document: %v", err)
	}
	for _, tenant := range []string{"tenant-a", ""} {
		got, _, err := s.GetForTenant(ctx, tenant, "job.private-b")
		if err != nil || got != nil {
			t.Errorf("foreign topic for %q: %v, %v", tenant, got, err)
		}
		page, err := s.ListForTenant(ctx, tenant)
		if err != nil || len(page.Items) != 0 {
			t.Errorf("foreign topic list: %v, %v", page, err)
		}
	}
	after, _, err := s.GetForTenant(ctx, "tenant-b", "job.private-b")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B registration changed: %v", err)
	}
	afterDoc, err := cfg.Get(ctx, configsvc.ScopeSystem, scopeIDTopics)
	if err != nil || !reflect.DeepEqual(doc, afterDoc) {
		t.Errorf("topics document changed: %v", err)
	}
}
