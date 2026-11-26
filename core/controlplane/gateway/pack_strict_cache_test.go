package gateway

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func packStrictTestClient(t *testing.T) (*miniredis.Miniredis, redis.UniversalClient) {
	t.Helper()
	t.Setenv(gatewayPackStrictEnv, "")
	resetGatewayPackStrictCache()
	t.Cleanup(resetGatewayPackStrictCache)
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

// A failed read returns the default for that call only. It must not be
// cached, or a transient Redis error would hide strict mode from every caller
// for the cache TTL after Redis recovers.
func TestResolveGatewayPackStrict_ReadErrorIsNotCached(t *testing.T) {
	mr, client := packStrictTestClient(t)
	ctx := context.Background()
	if err := client.Set(ctx, gatewayStrictRedisKey, "true", 0).Err(); err != nil {
		t.Fatalf("seed strict flag: %v", err)
	}

	mr.SetError("simulated outage")
	if resolveGatewayPackStrict(ctx, client) {
		t.Fatal("a failed read resolved strict=true; want the default false for that call")
	}

	mr.SetError("")
	if !resolveGatewayPackStrict(ctx, client) {
		t.Fatal("strict stayed false after Redis recovered: the failed read was cached")
	}
}

// An unset key is a real answer (the default), so it is cached for the TTL.
func TestResolveGatewayPackStrict_UnsetKeyIsCached(t *testing.T) {
	_, client := packStrictTestClient(t)
	ctx := context.Background()

	if resolveGatewayPackStrict(ctx, client) {
		t.Fatal("unset key resolved strict=true, want false")
	}
	if err := client.Set(ctx, gatewayStrictRedisKey, "true", 0).Err(); err != nil {
		t.Fatalf("set strict flag: %v", err)
	}
	if resolveGatewayPackStrict(ctx, client) {
		t.Fatal("the unset-key default was not cached for the TTL")
	}
}
