package resource

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRedisResolverConcurrentResolve(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	registry, server := newRedisRegistry(t, &now, []string{"application/octet-stream"}, 1024)
	body := []byte("immutable-content")
	if err := server.Set("blob:tenant-a:job-a:item", string(body)); err != nil {
		t.Fatalf("seed miniredis: %v", err)
	}
	ref := redisRef(now, "redis://resources/blob:tenant-a:job-a:item", "application/octet-stream", body)
	trusted := TrustedContext{TenantID: "tenant-a", JobID: "job-a"}
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			resolved, err := registry.Resolve(context.Background(), ref, trusted)
			if err != nil || string(resolved.Content) != string(body) {
				t.Errorf("Resolve = %q, %v", resolved.Content, err)
			}
		}()
	}
	wait.Wait()
}
