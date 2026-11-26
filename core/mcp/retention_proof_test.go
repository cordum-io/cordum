package mcp

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/cordum/cordum/core/infra/tracescan"
	"github.com/redis/go-redis/v9"
)

// TestRetentionProofDIC34 is the docs/contract/deletion-proofs.md proof for
// the mcp:dedupe:<key> (fixed MCPDedupeTTL, 10m) and mcp:taint:<tenant>:<session>
// (CORDUM_MCP_TAINT_TTL, default 1h) families of DI-C34. Exports: none; both
// stores write only these Redis keys.
func TestRetentionProofDIC34(t *testing.T) {
	t.Setenv(TaintTTLEnvVar, "")
	ctx := context.Background()
	client, mr := newMiniRedisDedupeBackend(t)
	const dedupeMarker, taintMarker = "pr2-08-dic34-dedupe", "pr2-08-dic34-taint"

	dedupe := NewRedisDedupeStore(client)
	pending := &redisDedupeRecord{State: redisDedupeStatePending, Owner: dedupeMarker + "-owner"}
	if _, loaded := dedupe.LoadOrStore(dedupeMarker, pending); loaded {
		t.Fatal("LoadOrStore loaded an existing record; want the reservation")
	}
	completed := &redisDedupeRecord{State: redisDedupeStateCompleted, Result: &redisDedupeResultMetadata{ResultSHA256: dedupeMarker}}
	if !dedupe.CompareAndSwap(dedupeMarker, pending, completed) {
		t.Fatal("CompareAndSwap did not publish the completed record")
	}
	taints := SelectTaintStore("redis", client)
	taint := SessionTaint{Tool: "board.read", Pattern: "ignore_previous", Snippet: taintMarker, Severity: "high",
		DetectedAt: time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)}
	if err := taints.Taint(ctx, "tenant-dic34", taintMarker+"-session", taint); err != nil {
		t.Fatalf("Taint: %v", err)
	}
	dedupeKey := MCPDedupeKeyPrefix + dedupeMarker
	taintRedisKey := MCPTaintKeyPrefix + taintKey("tenant-dic34", taintMarker+"-session")
	requireDIC34Trace(t, client, dedupeMarker, dedupeKey)
	requireDIC34Trace(t, client, taintMarker, taintRedisKey)

	mr.FastForward(MCPDedupeTTL + time.Second)
	requireDIC34Trace(t, client, dedupeMarker)
	requireDIC34Trace(t, client, taintMarker, taintRedisKey)

	mr.FastForward(defaultTaintTTL)
	requireDIC34Trace(t, client, taintMarker)
}

// requireDIC34Trace requires tracescan to find marker in exactly want.
func requireDIC34Trace(t *testing.T, client *redis.Client, marker string, want ...string) {
	t.Helper()
	hits, err := tracescan.Find(context.Background(), client, marker)
	if err != nil {
		t.Fatalf("tracescan.Find: %v", err)
	}
	if !slices.Equal(hits, want) {
		t.Fatalf("trace of %q = %v, want %v", marker, hits, want)
	}
}
