package outbound

import (
	"context"
	"slices"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/redis/go-redis/v9"
)

// TestRetentionProofDIC34 is the docs/contract/deletion-proofs.md proof for
// the mcp:outbound:nonce:<nonce> family of DI-C34. The verifier records each
// nonce for 2*clockSkew. Exports: none; the store writes only this Redis key.
func TestRetentionProofDIC34(t *testing.T) {
	ctx := context.Background()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 1})
	t.Cleanup(func() { _ = client.Close() })
	const marker = "pr2-08-dic34-outbound-nonce"
	ttl := 2 * DefaultClockSkew

	if seen, err := NewRedisNonceStore(client, "").SeenAndRecord(ctx, marker, ttl); err != nil || seen {
		t.Fatalf("SeenAndRecord = %v, %v; want first sight recorded", seen, err)
	}
	requireNonceTrace(t, client, marker, "mcp:outbound:nonce:"+marker)

	mr.FastForward(ttl + time.Second)
	requireNonceTrace(t, client, marker)
}

func requireNonceTrace(t *testing.T, client *redis.Client, marker string, want ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if !slices.Equal(hits, want) {
		t.Fatalf("trace of %q = %v, want %v", marker, hits, want)
	}
}
