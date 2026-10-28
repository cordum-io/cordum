package redisutil

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/internal/testredis"
	"github.com/redis/go-redis/v9"
)

func TestScanEachReadsEveryMatchingKey(t *testing.T) {
	mr := miniredis.RunT(t)
	for i := 0; i < 250; i++ {
		_ = mr.Set(fmt.Sprintf("k:%03d", i), "v")
	}
	_ = mr.Set("other", "v")
	ctx := context.Background()

	for name, client := range map[string]redis.UniversalClient{
		"single node": testredis.NewClient(t, mr.Addr()),
		"cluster":     testredis.NewMiniClusterClient(t, mr.Addr()),
	} {
		seen := map[string]bool{}
		err := ScanEach(ctx, client, "k:*", 100, func(keys []string) error {
			for _, k := range keys {
				seen[k] = true
			}
			return nil
		})
		if err != nil || len(seen) != 250 {
			t.Fatalf("%s: ScanEach saw %d keys, err %v; want 250", name, len(seen), err)
		}
	}

	stop := errors.New("stop")
	calls := 0
	err := ScanEach(ctx, testredis.NewClient(t, mr.Addr()), "k:*", 10, func([]string) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("ScanEach = %v after %d calls, want the callback's error after 1", err, calls)
	}
}
