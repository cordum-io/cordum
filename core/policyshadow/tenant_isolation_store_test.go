package policyshadow

import (
	"context"
	"reflect"
	"testing"
)

func TestTenantIsolationPolicyShadowReadOnly(t *testing.T) {
	s, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	if _, err := s.Put(ctx, ShadowPolicy{TenantID: "tenant-b", BundleID: "bundle-b", Content: "private B policy"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Get(ctx, "tenant-b", "bundle-b")
	if err != nil || before == nil || before.Content != "private B policy" {
		t.Fatalf("seed read: %v, %v", before, err)
	}
	got, err := s.Get(ctx, "tenant-a", "bundle-b")
	if err != nil || got != nil {
		t.Errorf("foreign policy: %v, %v", got, err)
	}
	items, err := s.List(ctx, "tenant-a")
	if err != nil || len(items) != 0 {
		t.Errorf("foreign policy list: %v, %v", items, err)
	}
	after, err := s.Get(ctx, "tenant-b", "bundle-b")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Errorf("B policy changed: %v", err)
	}
}
