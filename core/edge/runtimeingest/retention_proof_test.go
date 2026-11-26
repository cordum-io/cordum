package runtimeingest

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/redis/go-redis/v9"
)

// TestRetentionProofDIC34 is the docs/contract/deletion-proofs.md proof for
// the edge:rt:nonce:<tenant_hash>:<collector_hash> family of DI-C34. The window
// stores digests only, so the markers are the digests it writes: the tenant
// digest in the key name and the nonce digest as the set member. Exports:
// none; the window writes only this Redis key.
func TestRetentionProofDIC34(t *testing.T) {
	ctx := context.Background()
	client, mr := newReplayWindowTestClient(t)
	const tenant, collector, nonce = "tenant-dic34", "collector-dic34", "pr2-08-dic34-runtime-nonce"
	window := NewReplayWindow(client, ReplayWindowTTL, 0)
	if fresh, err := window.Reserve(ctx, tenant, collector, nonce); err != nil || !fresh {
		t.Fatalf("Reserve = %v, %v; want first sight recorded", fresh, err)
	}
	key := ReplayWindowKeyPrefix + replayKeyComponentDigest(tenant) + ":" + replayKeyComponentDigest(collector)
	tenantDigest, nonceDigest := replayKeyComponentDigest(tenant), replayNonceDigest(nonce)
	requireReplayTrace(t, client, tenantDigest, key)
	requireReplayTrace(t, client, nonceDigest, key+":"+nonceDigest)
	requireReplayTrace(t, client, nonce)

	mr.FastForward(ReplayWindowTTL + time.Second)
	requireReplayTrace(t, client, tenantDigest)
	requireReplayTrace(t, client, nonceDigest)
}

func requireReplayTrace(t *testing.T, client *redis.Client, marker string, want ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if !slices.Equal(hits, want) {
		t.Fatalf("trace of %q = %v, want %v", marker, hits, want)
	}
}
