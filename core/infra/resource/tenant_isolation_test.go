package resource

import (
	"context"
	"errors"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// TestTenantIsolationResourceRedisResolver seeds tenant B's cap:resource key
// directly, because no writer exists in the repository, and resolves it
// through Registry.Resolve with a Redis resolver that uses the same prefix and
// authority as the safety kernel.
func TestTenantIsolationResourceRedisResolver(t *testing.T) {
	const (
		tenantA, tenantB = "tenant-iso-a", "tenant-iso-b"
		jobA, jobB       = "job-iso-a", "job-iso-b"
		key              = "cap:resource:" + tenantB + ":" + jobB + ":item"
	)
	now := time.Unix(1_800_000_000, 0)
	server, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close(); server.Close() })
	resolver, err := NewRedisResolver(RedisResolverConfig{
		ID: "cache", Client: client, Scheme: "redis", Authority: "resources",
		KeyPrefix: "cap:resource:", AllowedMediaTypes: []string{"application/json"}, MaxBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewRedisResolver: %v", err)
	}
	registry, err := NewRegistry(func() time.Time { return now }, resolver)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	body := []byte(`{"input":"ISO-resource-` + uuid.NewString() + `"}`)
	if err := server.Set(key, string(body)); err != nil {
		t.Fatalf("seed tenant B resource: %v", err)
	}
	ref := redisRef(now, "redis://resources/"+key, "application/json", body)

	// Control: the owner's trusted context resolves the same reference.
	owned, err := registry.Resolve(context.Background(), ref, TrustedContext{TenantID: tenantB, JobID: jobB})
	if err != nil || string(owned.Content) != string(body) {
		t.Fatalf("tenant B own resolve = %q, %v", owned.Content, err)
	}

	for name, trusted := range map[string]TrustedContext{
		"tenant A with its own job id":    {TenantID: tenantA, JobID: jobA},
		"tenant A with tenant B's job id": {TenantID: tenantA, JobID: jobB},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := registry.Resolve(context.Background(), ref, trusted)
			if !errors.Is(err, ErrPolicyViolation) || len(got.Content) != 0 {
				t.Fatalf("Resolve = %q, %v; want ErrPolicyViolation and no content", got.Content, err)
			}
		})
	}

	stored, err := server.Get(key)
	if err != nil || stored != string(body) {
		t.Fatalf("tenant B resource changed: %q, %v", stored, err)
	}
}
