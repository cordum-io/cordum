package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

var authIsolationTenants = []string{"tenant-a", "tenant-b"}

// authKeyspace renders the value of every key matching the patterns. TTLs are
// left out: time passing is not a write by the caller.
func authKeyspace(t *testing.T, client *redis.Client, patterns ...string) map[string]string {
	t.Helper()
	ctx := context.Background()
	out := map[string]string{}
	for _, pattern := range patterns {
		keys, err := client.Keys(ctx, pattern).Result()
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			var value any
			switch kind := client.Type(ctx, key).Val(); kind {
			case "string":
				value, err = client.Get(ctx, key).Result()
			case "hash":
				value, err = client.HGetAll(ctx, key).Result()
			case "list":
				value, err = client.LRange(ctx, key, 0, -1).Result()
			case "set":
				var members []string
				members, err = client.SMembers(ctx, key).Result()
				sort.Strings(members)
				value = members
			case "zset":
				value, err = client.ZRangeWithScores(ctx, key, 0, -1).Result()
			default:
				t.Fatalf("key %s has type %q", key, kind)
			}
			if err != nil {
				t.Fatalf("read %s: %v", key, err)
			}
			out[key] = fmt.Sprint(value)
		}
	}
	return out
}

func assertAuthTenantReads(t *testing.T, ctx context.Context, store *RedisUserStore, svc *SCIMService, tenant, user, group string) {
	t.Helper()
	if got, err := store.GetByEmail(ctx, "shared@example.com", tenant); err != nil || got.ID != user {
		t.Errorf("%s by email = %+v, %v; want %s", tenant, got, err, user)
	}
	listed, err := store.List(ctx, tenant)
	if err != nil || len(listed) != 1 || listed[0].ID != user {
		t.Errorf("%s users = %+v, %v; want %s", tenant, listed, err, user)
	}
	scimUsers, err := svc.listSCIMUsers(ctx, tenant)
	if err != nil || len(scimUsers) != 1 || scimUsers[0].ID != user {
		t.Errorf("%s SCIM users = %+v, %v; want %s", tenant, scimUsers, err, user)
	}
	groups, err := svc.listSCIMGroups(ctx, tenant)
	if err != nil || len(groups) != 1 || groups[0].ID != group {
		t.Errorf("%s SCIM groups = %+v, %v; want %s", tenant, groups, err, group)
	}
}

// Both tenants hold a SCIM user with the same name and email, a session, and
// a group with the same name.
func TestTenantIsolationAuthUserSessionAndSCIMIndexes(t *testing.T) {
	store, _ := newTestUserStore(t)
	ctx := context.Background()
	services, users, groups := map[string]*SCIMService{}, map[string]string{}, map[string]string{}
	for _, tenant := range authIsolationTenants {
		svc, err := NewSCIMService(store, tenant, nil)
		if err != nil {
			t.Fatal(err)
		}
		u, err := svc.createSCIMUser(ctx, scimUserResource{UserName: "shared-name", Emails: []scimMultiValued{{Value: "shared@example.com", Primary: true}}})
		if err != nil {
			t.Fatal(err)
		}
		g, err := svc.createSCIMGroup(ctx, tenant, scimGroupResource{DisplayName: "admin"})
		if err != nil {
			t.Fatal(err)
		}
		user, err := store.GetByID(ctx, u.ID)
		if err != nil || user.Role != "viewer" {
			t.Fatalf("seed %s user: %+v, %v", tenant, user, err)
		}
		if err := store.StoreSession(ctx, "token-"+tenant, user, time.Hour); err != nil {
			t.Fatal(err)
		}
		services[tenant], users[tenant], groups[tenant] = svc, u.ID, g.ID
	}
	before := authKeyspace(t, store.client, "*")
	for _, tenant := range authIsolationTenants {
		assertAuthTenantReads(t, ctx, store, services[tenant], tenant, users[tenant], groups[tenant])
	}
	if after := authKeyspace(t, store.client, "*"); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads")
	}
	b := users["tenant-b"]
	bKeys := []string{"*tenant-b*", userIDKey(b), scimUserMetaPrefix + b, scimGroupKeyPrefix + groups["tenant-b"], sessionUserIdxPrefix + b, sessionKeyPrefix + "token-tenant-b"}
	beforeB := authKeyspace(t, store.client, bKeys...)
	// A's group names B's user as a member; membership must skip B's user.
	members := []scimMultiValued{{Value: users["tenant-a"]}, {Value: b}}
	if _, err := services["tenant-a"].replaceSCIMGroup(ctx, "tenant-a", groups["tenant-a"], scimGroupResource{DisplayName: "admin", Members: members}); err != nil {
		t.Fatal(err)
	}
	if a, err := store.GetByID(ctx, users["tenant-a"]); err != nil || a.Role != "admin" {
		t.Fatalf("A membership did not apply: %+v, %v", a, err)
	}
	// session:user is global; A's password change must revoke only A's tokens.
	if err := store.UpdatePassword(ctx, users["tenant-a"], "NewPassword123!"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ValidateSession(ctx, "token-tenant-a"); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("A session survived A's password change: %+v, %v", got, err)
	}
	if got, err := store.ValidateSession(ctx, "token-tenant-b"); err != nil || got.Tenant != "tenant-b" || got.PrincipalID != b {
		t.Errorf("B session = %+v, %v", got, err)
	}
	if afterB := authKeyspace(t, store.client, bKeys...); !reflect.DeepEqual(beforeB, afterB) {
		t.Errorf("B keys changed by A's group and password writes")
	}
	assertAuthTenantReads(t, ctx, store, services["tenant-b"], "tenant-b", b, groups["tenant-b"])
}

// apikey:prefix is global: both tenants' keys share one prefix set entry.
func TestTenantIsolationAuthAPIKeyIndexes(t *testing.T) {
	store, _ := newTestKeyStore(t)
	ctx := context.Background()
	raw, ids := map[string]string{}, map[string]string{}
	for _, tenant := range authIsolationTenants {
		raw[tenant] = "ck_0123abcd-secret-" + tenant
		key := &ManagedKey{Name: "shared", Tenant: tenant}
		if err := store.Create(ctx, key, raw[tenant]); err != nil {
			t.Fatal(err)
		}
		ids[tenant] = key.ID
	}
	prefixKey := keyPrefixIndexKey("ck_0123abcd")
	if n := store.client.SCard(ctx, prefixKey).Val(); n != 2 {
		t.Fatalf("seed prefix set holds %d ids, want both tenants", n)
	}
	before := authKeyspace(t, store.client, "*")
	for _, tenant := range authIsolationTenants {
		if got, err := store.ValidateKey(ctx, raw[tenant]); err != nil || got.ID != ids[tenant] || got.Tenant != tenant {
			t.Errorf("%s validate = %+v, %v", tenant, got, err)
		}
		listed, err := store.List(ctx, tenant)
		if err != nil || len(listed) != 1 || listed[0].ID != ids[tenant] {
			t.Errorf("%s keys = %+v, %v", tenant, listed, err)
		}
	}
	if err := store.Revoke(ctx, ids["tenant-b"], "tenant-a"); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("A revoked B's key: %v", err)
	}
	if after := authKeyspace(t, store.client, "*"); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads and a refused revoke")
	}
	if err := store.Revoke(ctx, ids["tenant-a"], "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.ValidateKey(ctx, raw["tenant-a"]); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("A key valid after A's revoke: %+v, %v", got, err)
	}
	members, err := store.client.SMembers(ctx, prefixKey).Result()
	if err != nil || !slices.Equal(members, []string{ids["tenant-b"]}) {
		t.Errorf("prefix set after A's revoke = %v, %v", members, err)
	}
	if got, err := store.ValidateKey(ctx, raw["tenant-b"]); err != nil || got.ID != ids["tenant-b"] || got.Revoked {
		t.Errorf("B key after A's revoke = %+v, %v", got, err)
	}
	if got := authKeyspace(t, store.client, keyRecordKey(ids["tenant-b"]), keyTenantKey("tenant-b")); !reflect.DeepEqual(got, map[string]string{
		keyRecordKey(ids["tenant-b"]): before[keyRecordKey(ids["tenant-b"])],
		keyTenantKey("tenant-b"):      before[keyTenantKey("tenant-b")],
	}) {
		t.Errorf("B key records changed by A's revoke")
	}
}
