package auth

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

func authObjectRecords(t *testing.T, client *redis.Client, keys ...string) []string {
	t.Helper()
	values := make([]string, len(keys))
	for i, key := range keys {
		value, err := client.Get(context.Background(), key).Result()
		if err != nil {
			t.Fatalf("read seeded record %s: %v", key, err)
		}
		values[i] = value
	}
	return values
}

func TestTenantIsolationAuthManagedKeyObject(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()
	const id = "private-key-b"
	seedManagedKey(ctx, t, store, "tenant-b", id)
	owner, err := store.List(ctx, "tenant-b")
	if err != nil || len(owner) != 1 || owner[0].ID != id {
		t.Fatalf("owner control: %v, %v", owner, err)
	}
	before := authObjectRecords(t, store.client, keyRecordKey(id))
	// Revoke is the managed-key API's tenant-checked lookup by record id.
	if err := store.Revoke(ctx, id, "tenant-a"); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("foreign key id was not refused: %v", err)
	}
	got, err := store.List(ctx, "tenant-a")
	if err != nil || len(got) != 0 {
		t.Errorf("tenant A received key records: %v, %v", got, err)
	}
	if after := authObjectRecords(t, store.client, keyRecordKey(id)); !reflect.DeepEqual(before, after) {
		t.Error("foreign key lookup changed tenant B's key")
	}
	after, err := store.List(ctx, "tenant-b")
	if err != nil || !reflect.DeepEqual(owner, after) {
		t.Errorf("tenant B's key is no longer visible unchanged: %v, %v", after, err)
	}
}

func TestTenantIsolationAuthUserAndSCIMMetaObjects(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	svc, err := NewSCIMService(store, "tenant-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.createSCIMUser(ctx, scimUserResource{UserName: "shared-name", DisplayName: "private-user-b", ExternalID: "private-external-b"})
	if err != nil {
		t.Fatal(err)
	}
	a := &User{Username: "shared-name", Tenant: "tenant-a", Role: "viewer", DisplayName: "own-user-a"}
	if err := store.Create(ctx, a, "TestPassword123!"); err != nil {
		t.Fatal(err)
	}
	owner, err := svc.getSCIMUser(ctx, "tenant-b", b.ID)
	if err != nil || owner.ExternalID != "private-external-b" {
		t.Fatalf("owner did not receive user and SCIM metadata: %v, %v", owner, err)
	}
	keys := []string{userKey("tenant-b", "shared-name"), userIDKey(b.ID), scimUserMetaPrefix + b.ID}
	before := authObjectRecords(t, store.client, keys...)
	// GetByID itself is internal and unscoped; this is its tenant-checked reader.
	foreign, err := svc.getSCIMUser(ctx, "tenant-a", b.ID)
	if !errors.Is(err, ErrUserNotFound) || !reflect.DeepEqual(foreign, scimUserResource{}) {
		t.Errorf("foreign id released user or SCIM metadata: %v, %v", foreign, err)
	}
	byName, err := store.GetByUsername(ctx, "shared-name", "tenant-a")
	if err != nil || byName == nil || byName.ID != a.ID || byName.Tenant != "tenant-a" || byName.DisplayName != "own-user-a" {
		t.Errorf("same-named user lookup crossed tenant: %v, %v", byName, err)
	}
	if after := authObjectRecords(t, store.client, keys...); !reflect.DeepEqual(before, after) {
		t.Error("foreign user lookup changed tenant B's records")
	}
}

func TestTenantIsolationAuthSCIMGroupObject(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	svc, err := NewSCIMService(store, "tenant-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.createSCIMGroup(ctx, "tenant-b", scimGroupResource{DisplayName: "viewer", ExternalID: "private-group-b"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.getSCIMGroup(ctx, "tenant-b", b.ID)
	if err != nil || owner.ExternalID != "private-group-b" {
		t.Fatalf("owner did not read group: %v, %v", owner, err)
	}
	before := authObjectRecords(t, store.client, scimGroupKeyPrefix+b.ID)
	foreign, err := svc.getSCIMGroup(ctx, "tenant-a", b.ID)
	if !errors.Is(err, ErrUserNotFound) || !reflect.DeepEqual(foreign, scimGroupResource{}) {
		t.Errorf("foreign group id released data: %v, %v", foreign, err)
	}
	if after := authObjectRecords(t, store.client, scimGroupKeyPrefix+b.ID); !reflect.DeepEqual(before, after) {
		t.Error("foreign group lookup changed tenant B's record")
	}
}
