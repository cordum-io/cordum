package gateway

import (
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/cordum/cordum/core/mcp"
	"github.com/cordum/cordum/core/policy/actiongates"
	"github.com/redis/go-redis/v9"
)

// newMiniRedisClientForDedupe spins up a miniredis backend and a
// go-redis client wired to it. Cleanup tears both down so -count=N
// runs see isolated state per iteration. Shared by the four backend-
// selection tests below so a single helper change covers them all.
func newMiniRedisClientForDedupe(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), PoolSize: 2})
	t.Cleanup(func() { _ = client.Close(); mr.Close() })
	return client
}

// TestBuildMCPPolicyDeps_DefaultRedisWithClient asserts the unset-env
// default selects the Redis backend when a Redis client is wired
// (multi-instance HA deploy). This is the production-shape: no
// operator override, gateway booted with a JobStore client → cross-
// process retry-dedupe should be active.
func TestBuildMCPPolicyDeps_DefaultRedisWithClient(t *testing.T) {
	t.Setenv(mcp.DedupeBackendEnvVar, "")
	client := newMiniRedisClientForDedupe(t)
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, client)
	if deps.DedupeState == nil {
		t.Fatal("expected DedupeState non-nil; got nil")
	}
	if _, ok := deps.DedupeState.(*mcp.RedisDedupeStore); !ok {
		t.Fatalf("DedupeState = %T; want *mcp.RedisDedupeStore (unset env + non-nil client → Redis backend)", deps.DedupeState)
	}
}

// TestBuildMCPPolicyDeps_DefaultMemoryWithoutClient asserts the
// unset-env default falls back to the in-process backend when no
// Redis client is available. This is the dev/test boot path where
// the gateway runs without a JobStore.
func TestBuildMCPPolicyDeps_DefaultMemoryWithoutClient(t *testing.T) {
	t.Setenv(mcp.DedupeBackendEnvVar, "")
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, nil)
	if deps.DedupeState == nil {
		t.Fatal("expected DedupeState non-nil; got nil")
	}
	if _, ok := deps.DedupeState.(*mcp.RedisDedupeStore); ok {
		t.Fatalf("DedupeState = *mcp.RedisDedupeStore; want in-process (unset env + nil client → memory backend)")
	}
}

// TestBuildMCPPolicyDeps_EnvForcedMemoryWithClient asserts the
// operator opt-out: CORDUM_MCP_DEDUPE_BACKEND=memory MUST select the
// in-process backend even when a Redis client is available. This
// covers the rollback scenario where an operator suspects Redis-side
// problems and wants to flip back to per-instance dedupe without
// re-deploying.
func TestBuildMCPPolicyDeps_EnvForcedMemoryWithClient(t *testing.T) {
	t.Setenv(mcp.DedupeBackendEnvVar, "memory")
	client := newMiniRedisClientForDedupe(t)
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, client)
	if _, ok := deps.DedupeState.(*mcp.RedisDedupeStore); ok {
		t.Fatalf("DedupeState = *mcp.RedisDedupeStore; want in-process (env=memory must override the Redis client)")
	}
}

// TestBuildMCPPolicyDeps_EnvForcedRedisFallsBackWhenNoClient asserts
// the env-set-but-unsatisfiable case: CORDUM_MCP_DEDUPE_BACKEND=redis
// with no Redis client → in-process fallback, NOT a panic and NOT a
// fail-closed empty deps. The boot must continue with degraded (per-
// instance) dedupe rather than blocking gate startup entirely.
func TestBuildMCPPolicyDeps_EnvForcedRedisFallsBackWhenNoClient(t *testing.T) {
	t.Setenv(mcp.DedupeBackendEnvVar, "redis")
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, nil)
	if deps.DedupeState == nil {
		t.Fatal("env=redis + nil client must NOT zero the deps; want in-process fallback")
	}
	if _, ok := deps.DedupeState.(*mcp.RedisDedupeStore); ok {
		t.Fatalf("DedupeState = *mcp.RedisDedupeStore with nil client; want in-process (selector fallback)")
	}
}

// TestBuildMCPPolicyDeps_UnknownEnvValueFallsBackToMemory asserts the
// defensive matrix entry: an unknown env value (typo, future setting)
// MUST NOT panic and MUST select in-process. Without this guard, a
// misformatted production env would crash the gateway boot.
func TestBuildMCPPolicyDeps_UnknownEnvValueFallsBackToMemory(t *testing.T) {
	t.Setenv(mcp.DedupeBackendEnvVar, "etcd-someday")
	client := newMiniRedisClientForDedupe(t)
	deps := BuildMCPPolicyDeps(&actiongates.Pipeline{}, &gatewayApprovalGate{}, fakeMCPEventEmitter{}, fakeMCPArtifactStore{}, client)
	if deps.DedupeState == nil {
		t.Fatal("unknown env value must NOT zero the deps; want in-process fallback")
	}
	if _, ok := deps.DedupeState.(*mcp.RedisDedupeStore); ok {
		t.Fatalf("DedupeState = *mcp.RedisDedupeStore; want in-process (unknown env value must NOT honor Redis)")
	}
}
