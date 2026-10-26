//go:build integration

package redisutil

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

// A cluster client's SCAN reaches one node; ScanEach reaches every master.
// Meaningful against a cluster with more than one master (see
// core/audit/cluster_integration_test.go for a local setup).
func TestClusterIntegration_ScanEachReachesEveryMaster(t *testing.T) {
	client := testredis.NewClusterClient(t)
	ctx := context.Background()
	prefix := "it:" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":scan:"
	const n = 60
	for i := 0; i < n; i++ {
		if err := client.Set(ctx, prefix+strconv.Itoa(i), "v", time.Minute).Err(); err != nil {
			t.Fatalf("set: %v", err)
		}
		t.Cleanup(func() { client.Del(context.Background(), prefix+strconv.Itoa(i)) })
	}

	var mu sync.Mutex
	perMaster := map[string]int{}
	if err := client.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
		keys, err := node.Keys(ctx, prefix+"*").Result()
		mu.Lock()
		defer mu.Unlock()
		perMaster[node.Options().Addr] = len(keys)
		return err
	}); err != nil {
		t.Fatalf("count keys per master: %v", err)
	}

	seen := map[string]bool{}
	if err := ScanEach(ctx, client, prefix+"*", 10, func(keys []string) error {
		for _, k := range keys {
			seen[k] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("ScanEach: %v", err)
	}
	if len(seen) != n {
		t.Fatalf("ScanEach saw %d keys, want %d (per master: %v)", len(seen), n, perMaster)
	}
	t.Logf("keys per master: %v", perMaster)
}
