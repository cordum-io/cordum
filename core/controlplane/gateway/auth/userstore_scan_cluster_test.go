package auth

import (
	"fmt"
	"slices"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// scanUserSeed writes user:id refs with single-key SETs, because the user
// store's own writes span hash slots. It returns each tenant's ids in
// ascending order.
func scanUserSeed(t *testing.T, client redis.UniversalClient) map[string][]string {
	t.Helper()
	ids := map[string][]string{}
	for i := range 12 {
		id := fmt.Sprintf("user-%02d", i)
		tenant := fmt.Sprintf("tenant-%d", i%3)
		if err := client.Set(t.Context(), userIDKey(id), tenant+":name-"+id, 0).Err(); err != nil {
			t.Fatal(err)
		}
		ids[tenant] = append(ids[tenant], id)
	}
	return ids
}

func TestBackfillTenantIndexTwoMasters(t *testing.T) {
	a, b := miniredis.RunT(t), miniredis.RunT(t)
	client := testredis.NewTwoMasterClusterClient(t, a.Addr(), b.Addr())
	want := scanUserSeed(t, client)
	if len(a.Keys()) == 0 || len(b.Keys()) == 0 {
		t.Fatal("test requires users on both masters")
	}

	if err := NewRedisUserStoreFromClient(client).backfillTenantIndex(t.Context()); err != nil {
		t.Fatalf("backfillTenantIndex: %v", err)
	}
	for tenant, ids := range want {
		got, err := client.SMembers(t.Context(), userTenantIndexPrefix+tenant).Result()
		if err != nil {
			t.Fatal(err)
		}
		slices.Sort(got)
		if !slices.Equal(got, ids) {
			t.Fatalf("%s index = %v, want %v", tenant, got, ids)
		}
	}
}
