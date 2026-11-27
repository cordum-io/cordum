package audit

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// scanHoldSeed writes hold records with single-key SETs, because CreateHold's
// transaction spans hash slots. It returns the ids in ascending order.
func scanHoldSeed(t *testing.T, client redis.UniversalClient) []string {
	t.Helper()
	var ids []string
	for i := range 12 {
		hold := LegalHold{ID: fmt.Sprintf("hold-%02d", i), TenantID: fmt.Sprintf("tenant-%d", i%3)}
		data, err := json.Marshal(hold)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(t.Context(), legalHoldKeyPrefix+hold.ID, data, 0).Err(); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, hold.ID)
	}
	return ids
}

func TestListHoldsAllTenantsTwoMasters(t *testing.T) {
	a, b := miniredis.RunT(t), miniredis.RunT(t)
	client := testredis.NewTwoMasterClusterClient(t, a.Addr(), b.Addr())
	want := scanHoldSeed(t, client)
	if len(a.Keys()) == 0 || len(b.Keys()) == 0 {
		t.Fatal("test requires holds on both masters")
	}

	holds, err := NewLegalHoldStoreFromClient(client).ListHolds(t.Context(), "")
	if err != nil {
		t.Fatalf("ListHolds: %v", err)
	}
	got := make([]string, 0, len(holds))
	for _, hold := range holds {
		got = append(got, hold.ID)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("hold ids = %v, want %v exactly once", got, want)
	}
}
