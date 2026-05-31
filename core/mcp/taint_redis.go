package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// TaintBackendEnvVar names the operator-facing backend selector, mirroring
// DedupeBackendEnvVar. Values: redis|memory (case-insensitive, trimmed);
// unset/unknown default to Redis-when-a-shared-client-is-present.
const TaintBackendEnvVar = "CORDUM_MCP_TAINT_BACKEND"

// TaintTTLEnvVar names the operator-facing env var (integer SECONDS) controlling
// how long a session taint survives in Redis. It MUST exceed a live demo/agent
// session — an expiry mid-session would drop the taint and let the destructive
// call through (a false negative). Unset/invalid falls back to defaultTaintTTL.
const TaintTTLEnvVar = "CORDUM_MCP_TAINT_TTL"

// defaultTaintTTL is the fallback taint lifetime. 1h comfortably exceeds a demo
// session while still bounding stale-taint growth in Redis.
const defaultTaintTTL = 3600 * time.Second

// taintCommandTimeout bounds every Redis command so a partition cannot block the
// gate hot path; mirrors redisDedupeCommandTimeout.
const taintCommandTimeout = 500 * time.Millisecond

// RedisTaintStore is the cross-process TaintStore backed by Redis. Two gateway
// instances behind a load balancer sharing one Redis backend observe the same
// taint, so a read that taints on instance A denies the delete that lands on
// instance B.
//
// Write-side fail-soft mirrors RedisDedupeStore: on a nil client or any
// Redis/encode error, Taint routes through the in-process fallback so a Redis
// blip degrades to per-instance taint instead of crashing the gate. Read-side
// behavior is stricter: GetTaint only treats redis.Nil as a clean session.
// Backend and decode failures are returned to the caller so the policy descriptor
// can record TaintLookupFailed instead of silently masking an unavailable or
// corrupted shared taint store as untainted.
type RedisTaintStore struct {
	client   redis.Cmdable
	ttl      time.Duration
	fallback TaintStore
}

// NewRedisTaintStore wires the SHARED gateway redis client into a cross-process
// TaintStore (epic rail "no parallel subsystems" — reuse the one client; a fresh
// connection here would double the pool and hide the backend from metrics). ttl
// is explicit for testability (tests pass a short ttl / use miniredis
// FastForward); a non-positive ttl is coerced to defaultTaintTTL. A nil client
// routes every call through the in-process fallback.
func NewRedisTaintStore(client redis.Cmdable, ttl time.Duration) *RedisTaintStore {
	if ttl <= 0 {
		ttl = defaultTaintTTL
	}
	return &RedisTaintStore{client: client, ttl: ttl, fallback: NewInProcessTaintStore()}
}

func (s *RedisTaintStore) Taint(ctx context.Context, tenant, session string, t SessionTaint) error {
	if s.client == nil {
		return s.fallback.Taint(ctx, tenant, session, t)
	}
	encoded, err := json.Marshal(t)
	if err != nil {
		return s.fallback.Taint(ctx, tenant, session, t)
	}
	cctx, cancel := context.WithTimeout(ctx, taintCommandTimeout)
	defer cancel()
	if err := s.client.Set(cctx, MCPTaintKeyPrefix+taintKey(tenant, session), encoded, s.ttl).Err(); err != nil {
		return s.fallback.Taint(ctx, tenant, session, t)
	}
	return nil
}

func (s *RedisTaintStore) GetTaint(ctx context.Context, tenant, session string) (*SessionTaint, bool, error) {
	if s.client == nil {
		return s.fallback.GetTaint(ctx, tenant, session)
	}
	cctx, cancel := context.WithTimeout(ctx, taintCommandTimeout)
	defer cancel()
	raw, err := s.client.Get(cctx, MCPTaintKeyPrefix+taintKey(tenant, session)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil // no taint (clean session) — the common case, not an error
	}
	if err != nil {
		return nil, false, err
	}
	var t SessionTaint
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, false, err
	}
	return &t, true, nil
}

// SelectTaintStore picks the TaintStore the gateway wires into ToolCallDeps,
// mirroring SelectDedupeStore's matrix:
//
//	hint=="memory"      → in-process (operator opt-out of cross-process)
//	hint=="redis"       → Redis if client != nil, else in-process
//	hint==""    (unset) → Redis if client != nil, else in-process
//	hint == anything else (typo / future value) → in-process (no panic)
//
// Comparison is case-insensitive with surrounding whitespace trimmed. The Redis
// store's TTL is resolved from TaintTTLEnvVar.
func SelectTaintStore(hint string, client redis.Cmdable) TaintStore {
	normalized := strings.ToLower(strings.TrimSpace(hint))
	switch normalized {
	case "memory":
		return NewInProcessTaintStore()
	case "redis", "":
		if client != nil {
			return NewRedisTaintStore(client, resolveTaintTTL())
		}
		return NewInProcessTaintStore()
	default:
		return NewInProcessTaintStore()
	}
}

// resolveTaintTTL reads TaintTTLEnvVar as integer seconds, falling back to
// defaultTaintTTL when unset, non-numeric, or non-positive.
func resolveTaintTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv(TaintTTLEnvVar))
	if raw == "" {
		return defaultTaintTTL
	}
	secs, err := strconv.Atoi(raw)
	if err != nil || secs <= 0 {
		return defaultTaintTTL
	}
	return time.Duration(secs) * time.Second
}
