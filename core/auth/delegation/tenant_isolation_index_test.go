package delegation

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// delegationKeyspace renders the value of every key matching the pattern.
// TTLs are left out: time passing is not a write by the caller.
func delegationKeyspace(t *testing.T, client redis.UniversalClient, pattern string) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := client.Keys(ctx, pattern).Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		var value any
		switch kind := client.Type(ctx, key).Val(); kind {
		case "string":
			value, err = client.Get(ctx, key).Result()
		case "hash":
			value, err = client.HGetAll(ctx, key).Result()
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
	return out
}

func delegationJTIs(page DelegationListPage, err error) ([]string, error) {
	jtis := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		jtis = append(jtis, item.JTI)
	}
	return jtis, err
}

// Each tenant holds a root token and a child for the same agent id.
func TestTenantIsolationDelegationIndexes(t *testing.T) {
	revocations, _ := newTestRevocationStore(t)
	listStore := NewRedisListStoreFromClient(revocations.client)
	ctx, now := context.Background(), time.Now().UTC()
	tenants := []string{"tenant-a", "tenant-b"}
	for _, tenant := range tenants {
		root, child := "dlg-root-"+tenant, "dlg-child-"+tenant
		recordDelegationToken(t, listStore, revocations, DelegationView{JTI: root, Tenant: tenant, Issuer: "agent-shared", Subject: "agent-shared", IssuedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(time.Hour)})
		recordDelegationToken(t, listStore, revocations, DelegationView{JTI: child, Tenant: tenant, Issuer: "agent-shared", Subject: "agent-shared", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), ParentJTI: root})
	}
	before := delegationKeyspace(t, revocations.client, "*")
	for _, tenant := range tenants {
		want := []string{"dlg-child-" + tenant, "dlg-root-" + tenant}
		if got, err := delegationJTIs(listStore.ListByAgent(ctx, tenant, "agent-shared", DelegationListFilter{}, "", 0)); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s by agent = %v, %v; want %v", tenant, got, err, want)
		}
		if got, err := delegationJTIs(listStore.ListAll(ctx, tenant, DelegationListFilter{}, "", 0)); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s all = %v, %v; want %v", tenant, got, err, want)
		}
	}
	if after := delegationKeyspace(t, revocations.client, "*"); !reflect.DeepEqual(before, after) {
		t.Errorf("keyspace changed by reads")
	}
	// The children walk and the active-index cleanup run without a tenant
	// argument; A's cascade must stay inside A's tree and A's active index.
	beforeB := delegationKeyspace(t, revocations.client, "*tenant-b*")
	result, err := revocations.CascadeRevoke(ctx, "dlg-root-tenant-a", "compromised", now, true)
	if want := []string{"dlg-root-tenant-a", "dlg-child-tenant-a"}; err != nil || !slices.Equal(result.RevokedJTIs, want) {
		t.Fatalf("A cascade = %v, %v; want %v", result.RevokedJTIs, err, want)
	}
	if n := revocations.client.ZCard(ctx, delegationActiveKey("tenant-a")).Val(); n != 0 {
		t.Errorf("A active index holds %d tokens after A's cascade", n)
	}
	for _, jti := range []string{"dlg-root-tenant-b", "dlg-child-tenant-b"} {
		if revoked, err := revocations.IsRevoked(ctx, jti); err != nil || revoked {
			t.Errorf("B token %s revoked by A's cascade: %v", jti, err)
		}
	}
	if afterB := delegationKeyspace(t, revocations.client, "*tenant-b*"); !reflect.DeepEqual(beforeB, afterB) {
		t.Errorf("B keys changed by A's cascade")
	}
}
