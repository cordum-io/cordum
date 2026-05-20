package edge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisMCPUpstreamCreateEnforcesTenantCap(t *testing.T) {
	ctx, registry, _ := newConcreteMCPUpstreamRegistryForTest(t, 1)
	registry.createMaxPerTenant = 2

	for _, name := range []string{"tenant-cap-1", "tenant-cap-2"} {
		upstream := validMCPUpstream("tenant-cap", name)
		if err := registry.Create(ctx, &upstream); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}

	duplicate := validMCPUpstream("tenant-cap", "tenant-cap-2")
	if err := registry.Create(ctx, &duplicate); !errors.Is(err, ErrUpstreamAlreadyExists) {
		t.Fatalf("duplicate Create error = %v, want ErrUpstreamAlreadyExists", err)
	}

	overflow := validMCPUpstream("tenant-cap", "tenant-cap-3")
	if err := registry.Create(ctx, &overflow); !errors.Is(err, ErrUpstreamLimitExceeded) {
		t.Fatalf("overflow Create error = %v, want ErrUpstreamLimitExceeded", err)
	}
}

func TestRedisMCPUpstreamCreateTenantCapConcurrent(t *testing.T) {
	ctx, registry, client := newConcreteMCPUpstreamRegistryForTest(t, 16)
	registry.createMaxPerTenant = 2

	const racers = 8
	start := make(chan struct{})
	errs := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			upstream := validMCPUpstream("tenant-race", fmt.Sprintf("race-%02d", i))
			errs <- registry.Create(ctx, &upstream)
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes int
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, ErrUpstreamLimitExceeded) {
			t.Fatalf("concurrent Create error = %v, want ErrUpstreamLimitExceeded for losers", err)
		}
	}
	if successes != int(registry.createMaxPerTenant) {
		t.Fatalf("successful creates = %d, want %d", successes, registry.createMaxPerTenant)
	}
	got, err := client.SCard(ctx, mcpUpstreamTenantIndexKey("tenant-race")).Result()
	if err != nil {
		t.Fatalf("SCard tenant-race: %v", err)
	}
	if got != registry.createMaxPerTenant {
		t.Fatalf("tenant index cardinality = %d, want %d", got, registry.createMaxPerTenant)
	}
}

func TestRedisMCPUpstreamRegistryEnsureReadyDoesNotMutateClock(t *testing.T) {
	_, _, client := newConcreteMCPUpstreamRegistryForTest(t, 1)
	registry := &RedisMCPUpstreamRegistry{client: client}

	if err := registry.ensureReady(); err == nil {
		t.Fatal("ensureReady() with nil clock succeeded; direct construction must fail closed")
	}
	if registry.now != nil {
		t.Fatal("ensureReady() mutated r.now; the registry clock must be constructor-owned")
	}
}

func TestRedisMCPUpstreamListUsesBoundedScan(t *testing.T) {
	ctx, registry, _ := newConcreteMCPUpstreamRegistryForTest(t, 1)
	registry.listScanBatchSize = 4
	scanned := make(map[string][]int64)
	var mu sync.Mutex
	registry.listScanHook = func(scope string, count int64) {
		mu.Lock()
		defer mu.Unlock()
		scanned[scope] = append(scanned[scope], count)
	}

	for _, upstream := range []UpstreamServer{
		validMCPUpstream("*", "system-a"),
		validMCPUpstream("*", "system-b"),
		validMCPUpstream("tenant-a", "tenant-a-a"),
		validMCPUpstream("tenant-a", "tenant-a-b"),
		validMCPUpstream("tenant-b", "tenant-b-only"),
	} {
		if err := registry.Create(ctx, &upstream); err != nil {
			t.Fatalf("Create %s/%s: %v", upstream.TenantID, upstream.Name, err)
		}
	}

	got, err := registry.List(ctx, "tenant-a")
	if err != nil {
		t.Fatalf("List tenant-a: %v", err)
	}
	if names := mcpUpstreamNames(got); !sameStringSet(names, []string{"system-a", "system-b", "tenant-a-a", "tenant-a-b"}) {
		t.Fatalf("List tenant-a names = %v", names)
	}
	assertScanHookCount(t, scanned, "*", registry.listScanBatchSize)
	assertScanHookCount(t, scanned, "tenant-a", registry.listScanBatchSize)
	if _, ok := scanned["tenant-b"]; ok {
		t.Fatalf("List tenant-a scanned tenant-b scope: %v", scanned)
	}
}

func TestRedisMCPUpstreamUpdateKeepsSingleTenantIndexMember(t *testing.T) {
	ctx, registry, client := newConcreteMCPUpstreamRegistryForTest(t, 1)
	upstream := validMCPUpstream("tenant-index", "stable-index")
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, endpoint := range []string{"https://one.example/mcp", "https://two.example/mcp"} {
		upstream.Endpoint = endpoint
		if err := registry.Update(ctx, &upstream); err != nil {
			t.Fatalf("Update %s: %v", endpoint, err)
		}
	}

	card, err := client.SCard(ctx, mcpUpstreamTenantIndexKey("tenant-index")).Result()
	if err != nil {
		t.Fatalf("SCard tenant index: %v", err)
	}
	if card != 1 {
		t.Fatalf("tenant index cardinality = %d, want 1", card)
	}
	got, err := registry.List(ctx, "tenant-index")
	if err != nil {
		t.Fatalf("List tenant-index: %v", err)
	}
	if names := mcpUpstreamNames(got); !sameStringSet(names, []string{"stable-index"}) {
		t.Fatalf("List tenant-index names = %v, want [stable-index]", names)
	}
}

func TestRedisMCPUpstreamListEmptyTenantDoesNotScan(t *testing.T) {
	ctx, registry, _ := newConcreteMCPUpstreamRegistryForTest(t, 1)
	var scans int
	registry.listScanHook = func(string, int64) { scans++ }

	got, err := registry.List(ctx, " ")
	if err != nil {
		t.Fatalf("List empty tenant: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List empty tenant = %+v, want empty", got)
	}
	if scans != 0 {
		t.Fatalf("empty tenant triggered %d scans, want 0", scans)
	}
}

func TestRegistryUpdateCreatesUniqueBackupsForRapidUpdates(t *testing.T) {
	ctx, registry, client := newMCPUpstreamRegistryForTest(t)
	upstream := validMCPUpstream("tenant-a", "rapid-update")
	upstream.Endpoint = "https://one.example/mcp"
	if err := registry.Create(ctx, &upstream); err != nil {
		t.Fatalf("Create: %v", err)
	}

	upstream.Endpoint = "https://two.example/mcp"
	if err := registry.Update(ctx, &upstream); err != nil {
		t.Fatalf("Update #1: %v", err)
	}
	upstream.Endpoint = "https://three.example/mcp"
	if err := registry.Update(ctx, &upstream); err != nil {
		t.Fatalf("Update #2: %v", err)
	}

	keys, err := client.Keys(ctx, "edge:mcp:upstream:bak:tenant-a:rapid-update:*").Result()
	if err != nil {
		t.Fatalf("Keys backup: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("backup keys = %v, want two unique backups", keys)
	}
	payloads := make([]string, 0, len(keys))
	for _, key := range keys {
		payload, err := client.Get(ctx, key).Result()
		if err != nil {
			t.Fatalf("Get backup %s: %v", key, err)
		}
		payloads = append(payloads, payload)
	}
	joined := strings.Join(payloads, "\n")
	if !strings.Contains(joined, "https://one.example/mcp") || !strings.Contains(joined, "https://two.example/mcp") {
		t.Fatalf("backup payloads missing prior versions: %s", joined)
	}
}

func newConcreteMCPUpstreamRegistryForTest(t *testing.T, poolSize int) (context.Context, *RedisMCPUpstreamRegistry, *redis.Client) {
	t.Helper()
	if poolSize <= 0 {
		poolSize = 1
	}
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: poolSize})
	t.Cleanup(func() { _ = client.Close() })
	return context.Background(), NewRedisMCPUpstreamRegistryFromClient(client), client
}

func assertScanHookCount(t *testing.T, scanned map[string][]int64, scope string, max int64) {
	t.Helper()
	counts := scanned[scope]
	if len(counts) == 0 {
		t.Fatalf("scope %q did not use bounded scan; scanned=%v", scope, scanned)
	}
	for _, count := range counts {
		if count <= 0 || count > max {
			t.Fatalf("scope %q scan count = %d, want 1..%d; scanned=%v", scope, count, max, scanned)
		}
	}
}
