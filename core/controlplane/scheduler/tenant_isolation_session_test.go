package scheduler

import (
	"context"
	"reflect"
	"testing"
)

func TestTenantIsolationSchedulerBoundSessionReadOnly(t *testing.T) {
	i, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	bindingB := boundTestSession("tenant-b")
	_, claimsB, err := i.IssueBound(ctx, bindingB)
	if err != nil {
		t.Fatal(err)
	}
	before := readActiveRecordForTest(t, mr, boundWorkerKey(bindingB.Tenant, bindingB.WorkerID))
	if before.JTI != claimsB.JTI || before.Tenant != "tenant-b" {
		t.Fatal("B positive control missing")
	}
	// The real store lookup must miss when A knows B's worker id.
	foreign := claimsB
	foreign.Tenant = "tenant-a"
	got, err := i.loadActiveForClaims(ctx, foreign)
	if err != nil || got != nil {
		t.Errorf("foreign session: %v, %v", got, err)
	}
	after := readActiveRecordForTest(t, mr, boundWorkerKey(bindingB.Tenant, bindingB.WorkerID))
	if !reflect.DeepEqual(before, after) {
		t.Error("B active record changed")
	}
}

func TestTenantIsolationSchedulerRevocationReadOnly(t *testing.T) {
	i, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	token, claims, err := i.IssueBound(ctx, boundTestSession("tenant-a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := i.Revoke(ctx, "tenant-b", claims.JTI, claims.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	key := revokedKey("tenant-b", claims.JTI)
	before, err := mr.Get(key)
	if err != nil || before != "1" {
		t.Fatalf("B revocation not persisted: %q, %v", before, err)
	}
	got, err := i.VerifyBound(ctx, token, true)
	if err != nil || got.Tenant != "tenant-a" || got.JTI != claims.JTI {
		t.Errorf("B revocation affected A: %v, %v", got, err)
	}
	after, err := mr.Get(key)
	if err != nil || before != after {
		t.Errorf("B revocation changed: %q, %v", after, err)
	}
}

func TestTenantIsolationSchedulerLegacySessionReadOnly(t *testing.T) {
	i, mr, _, cleanup := newTestIssuer(t, SessionTokenIssuerOptions{})
	defer cleanup()
	ctx := context.Background()
	_, claims, err := i.Issue(ctx, "legacy-worker", "tenant-b", "v2.9.0")
	if err != nil {
		t.Fatal(err)
	}
	key := workerKey("legacy-worker")
	before := readActiveRecordForTest(t, mr, key)
	if before.JTI != claims.JTI || before.Tenant != "tenant-b" {
		t.Fatal("B positive control missing")
	}
	got, err := i.loadRevocationTargets(ctx, "tenant-a", "legacy-worker")
	if err != nil || len(got) != 0 {
		t.Errorf("foreign legacy targets: %v, %v", got, err)
	}
	after := readActiveRecordForTest(t, mr, key)
	if !reflect.DeepEqual(before, after) {
		t.Error("B legacy record changed")
	}
}
