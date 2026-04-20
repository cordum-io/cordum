package policyshadow

import (
	"context"
	"fmt"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/configsvc"
)

func newTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	svc, err := configsvc.New("redis://" + srv.Addr())
	if err != nil {
		srv.Close()
		t.Fatalf("configsvc: %v", err)
	}
	store := NewStore(svc, WithClock(func() time.Time {
		return time.Unix(1_700_000_000, 0).UTC()
	}))
	cleanup := func() {
		_ = svc.Close()
		srv.Close()
	}
	return store, cleanup
}

func TestStore_GetMissingReturnsNil(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	got, err := store.Get(context.Background(), "tenant-a", "bundle-x")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil, got %+v", got)
	}
}

func TestStore_PutReadbackRoundTrip(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	sp := ShadowPolicy{
		BundleID:  "foo~default",
		TenantID:  "tenant-a",
		Content:   "version: 1",
		CreatedBy: "op1",
	}
	stored, err := store.Put(ctx, sp)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if stored.ShadowBundleID == "" {
		t.Fatal("ShadowBundleID was not assigned")
	}
	if stored.CreatedAt.IsZero() || stored.ActivatedAt.IsZero() {
		t.Fatalf("timestamps not stamped: %+v", stored)
	}

	got, err := store.Get(ctx, "tenant-a", "foo~default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil after Put")
	}
	if got.ShadowBundleID != stored.ShadowBundleID {
		t.Fatalf("ShadowBundleID mismatch: got %q, want %q", got.ShadowBundleID, stored.ShadowBundleID)
	}
	if got.Content != sp.Content {
		t.Fatalf("Content mismatch: got %q", got.Content)
	}
	if got.CreatedBy != sp.CreatedBy {
		t.Fatalf("CreatedBy mismatch: got %q", got.CreatedBy)
	}
}

func TestStore_PutReplacesExistingShadow(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()

	first, err := store.Put(ctx, ShadowPolicy{
		BundleID: "foo~default",
		TenantID: "tenant-a",
		Content:  "version: 1",
	})
	if err != nil {
		t.Fatalf("first Put: %v", err)
	}

	second, err := store.Put(ctx, ShadowPolicy{
		BundleID: "foo~default",
		TenantID: "tenant-a",
		Content:  "version: 2",
	})
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if second.ShadowBundleID == first.ShadowBundleID {
		t.Fatalf("expected fresh ShadowBundleID on replace, got same %q", second.ShadowBundleID)
	}

	got, err := store.Get(ctx, "tenant-a", "foo~default")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ShadowBundleID != second.ShadowBundleID {
		t.Fatalf("Get returned stale ShadowBundleID %q, want %q", got.ShadowBundleID, second.ShadowBundleID)
	}
	if got.Content != "version: 2" {
		t.Fatalf("Content did not update: got %q", got.Content)
	}
	// CreatedAt must be preserved across replace (first-seen stays put,
	// ActivatedAt bumps on each Put).
	if !got.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("CreatedAt should persist across replace: got %v, want %v", got.CreatedAt, first.CreatedAt)
	}
}

func TestStore_DeleteAbsentIsNotAnError(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	removed, err := store.Delete(context.Background(), "tenant-a", "does-not-exist")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if removed {
		t.Fatal("removed flag should be false for absent entry")
	}
}

func TestStore_DeleteRemovesEntry(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := store.Put(ctx, ShadowPolicy{
		BundleID: "foo~default",
		TenantID: "tenant-a",
		Content:  "version: 1",
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	removed, err := store.Delete(ctx, "tenant-a", "foo~default")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !removed {
		t.Fatal("expected removed=true for live entry")
	}

	got, err := store.Get(ctx, "tenant-a", "foo~default")
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Fatalf("Get returned %+v after Delete", got)
	}
}

func TestStore_ListReturnsAllTenantShadows(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	for _, bundle := range []string{"a~ns", "b~ns", "c~ns"} {
		if _, err := store.Put(ctx, ShadowPolicy{
			BundleID: bundle,
			TenantID: "tenant-a",
			Content:  "version: 1",
		}); err != nil {
			t.Fatalf("Put %s: %v", bundle, err)
		}
	}
	// Another tenant's shadows must not leak into tenant-a's list.
	if _, err := store.Put(ctx, ShadowPolicy{
		BundleID: "other~ns",
		TenantID: "tenant-b",
		Content:  "version: 1",
	}); err != nil {
		t.Fatalf("Put tenant-b: %v", err)
	}

	got, err := store.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("List tenant-a len = %d, want 3", len(got))
	}
	seen := map[string]bool{}
	for _, sp := range got {
		seen[sp.BundleID] = true
		if sp.TenantID != "tenant-a" {
			t.Errorf("List returned cross-tenant shadow: %+v", sp)
		}
	}
	for _, want := range []string{"a~ns", "b~ns", "c~ns"} {
		if !seen[want] {
			t.Errorf("List missing %q: got %+v", want, got)
		}
	}
}

func TestStore_ListMissingTenantReturnsEmpty(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	got, err := store.List(context.Background(), "tenant-nowhere")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %+v", got)
	}
}

func TestStore_PutRequiresFields(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	cases := []struct {
		name string
		sp   ShadowPolicy
	}{
		{"no_tenant", ShadowPolicy{BundleID: "b", Content: "c"}},
		{"no_bundle", ShadowPolicy{TenantID: "t", Content: "c"}},
		{"no_content", ShadowPolicy{TenantID: "t", BundleID: "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.Put(ctx, tc.sp); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestStore_ConcurrentPutSameBundle(t *testing.T) {
	t.Parallel()
	store, cleanup := newTestStore(t)
	defer cleanup()

	// Sanity check: 8 concurrent Puts against the same tenant-bundle
	// pair all succeed (ETag retry) and the final state contains
	// exactly one shadow for the bundle (one-shadow-per-bundle).
	ctx := context.Background()
	const goroutines = 8
	done := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			_, err := store.Put(ctx, ShadowPolicy{
				BundleID: "hot~bundle",
				TenantID: "tenant-a",
				Content:  fmt.Sprintf("version: %d", i),
			})
			done <- err
		}()
	}
	for i := 0; i < goroutines; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent Put %d failed: %v", i, err)
		}
	}
	list, err := store.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("one-shadow-per-bundle violated: got %d entries", len(list))
	}
}
