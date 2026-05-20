package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis wire-state constants. Persisted as the `state` field on every
// stored `redisDedupeRecord`. The pending → completed transition is the
// signal the polling waiter in dedupeBegin watches for.
const (
	redisDedupeStatePending   = "pending"
	redisDedupeStateCompleted = "completed"
)

// maxRedisDedupeRecordBytes caps the serialized record size we are
// willing to write to Redis. Larger payloads are dropped from the
// cross-process cache (LoadOrStore returns the supplied value with
// loaded=false so the caller proceeds as if uncached) rather than
// risking an oversized Redis value that wastes memory across every
// gateway instance for one-off jumbo responses. Epic rail #7: no
// large raw transcripts/tool payloads in Redis events.
const maxRedisDedupeRecordBytes = 32 * 1024

// redisDedupeCommandTimeout bounds every Redis command issued by the
// store so a network partition cannot livelock a caller. The wider
// MCP retry-dedupe path tolerates a fail-soft fallback to in-process
// (taskRail #3), so a short command timeout that flips an unresponsive
// Redis to fallback is preferable to letting a slow command block the
// gate hot path.
const redisDedupeCommandTimeout = 500 * time.Millisecond

// redisDedupePollInterval is the polling cadence dedupeBegin uses when
// it observes a `pending` wire record and must wait for the cross-
// process winner to publish a `completed` record or the TTL to expire.
// Exported only inside the package; the constant lives here because
// the polling behavior is a contract of the Redis store, not of
// dedupeBegin in isolation.
const redisDedupePollInterval = 50 * time.Millisecond

// redisDedupeRecord is the JSON wire format persisted at every
// `mcp:dedupe:<key>` Redis row. Completed rows store only bounded
// metadata about the ToolCallResult; Redis never stores raw content
// text/data, structuredContent, MIME values, or other tool-result bodies.
type redisDedupeRecord struct {
	State      string                     `json:"state"`
	Result     *redisDedupeResultMetadata `json:"result,omitempty"`
	ErrorClass string                     `json:"error_class,omitempty"`
}

type redisDedupeResultMetadata struct {
	IsError              bool   `json:"is_error"`
	ContentCount         int    `json:"content_count"`
	HasStructuredContent bool   `json:"has_structured_content"`
	ResultSHA256         string `json:"result_sha256"`
}

// RedisDedupeStore is the cross-process DedupeStore backed by Redis
// SET NX EX semantics. Two gateway instances behind a load balancer
// sharing one Redis backend collapse identical retries into a single
// upstream call via the atomic SETNX winner selection.
//
// Fail-soft contract (taskRail #3): on ANY Redis command error or
// decode failure, the operation falls back to the in-process store so
// a Redis outage degrades to per-instance dedupe instead of blocking
// gate traffic. The fallback store is shared across the lifetime of
// this instance so two callers on the same gateway still collapse
// locally during the outage window.
type RedisDedupeStore struct {
	client   redis.Cmdable
	fallback DedupeStore
}

// NewRedisDedupeStore wires the Redis client into a cross-process
// DedupeStore implementation. The caller MUST reuse the gateway's
// existing go-redis client (epic rail "no parallel subsystems"); a
// fresh client connection here would double the connection pool and
// hide the Redis backend type from operator-visible metrics.
//
// A nil client is allowed — the store routes every call through the
// in-process fallback. This is the boot path when the gateway has no
// Redis connectivity at all (test fixtures, no-Redis dev runs).
func NewRedisDedupeStore(client redis.Cmdable) *RedisDedupeStore {
	return &RedisDedupeStore{
		client:   client,
		fallback: NewInProcessDedupeStore(),
	}
}

// maxRedisDedupeNxRaceRetries bounds how many times LoadOrStore re-attempts
// the SETNX→GET dance when it observes the benign NX-miss-then-Get-Nil race:
// another gateway instance called Delete on the key between our SETNX-miss
// and our GET. A small bound is sufficient — the race window is one Redis
// round-trip wide, so multiple consecutive losses indicate sustained
// contention rather than a single slipped step.
const maxRedisDedupeNxRaceRetries = 3

// LoadOrStore performs the cross-process winner selection. The first
// caller across all gateway instances wins via `SET NX EX`; subsequent
// callers see SET return zero rows (NX miss) and fall through to GET +
// decode of the stored record.
//
// Fail-soft on Redis OUTAGE: any non-Nil Redis error → in-process
// fallback. The caller cannot distinguish a Redis-backed entry from a
// fallback entry, which is intentional: the dedupe semantics are the
// same and the gate must not surface backend health as a tool-call error.
//
// Fail-CLOSED on NX-miss-then-Nil RACE: another gateway process Deleted
// the key between our SETNX-miss and our GET. Falling back to the per-
// instance map here would promote BOTH instances to local-winner and
// produce duplicate upstream side effects (double tool invocation,
// double audit). Instead we retry SETNX a bounded number of times; if
// retries exhaust we return a pending wire record with loaded=true so
// the caller enters waitForRedisDedupe polling and a downstream cycle
// observes the real cross-process state. The trade-off vs the outage
// path: a transient race adds at most one polling-interval of latency,
// while the outage path keeps the gate non-blocking by absorbing the
// per-instance dedupe loss.
func (s *RedisDedupeStore) LoadOrStore(key string, value any) (any, bool) {
	if s.client == nil {
		return s.fallback.LoadOrStore(key, value)
	}
	encoded, ok := s.encode(value)
	if !ok {
		return s.fallback.LoadOrStore(key, value)
	}
	fullKey := MCPDedupeKeyPrefix + key
	for range maxRedisDedupeNxRaceRetries {
		ctx, cancel := context.WithTimeout(context.Background(), redisDedupeCommandTimeout)
		stored, setErr := s.client.SetNX(ctx, fullKey, encoded, MCPDedupeTTL).Result()
		if setErr != nil {
			cancel()
			return s.fallback.LoadOrStore(key, value)
		}
		if stored {
			cancel()
			return value, false
		}
		raw, getErr := s.client.Get(ctx, fullKey).Bytes()
		cancel()
		if errors.Is(getErr, redis.Nil) {
			// Benign race: entry vanished between our SETNX-miss and GET.
			// Re-attempt SETNX — if it wins, we are the new legitimate
			// cross-process winner; if it loses again, the next Get
			// observes the new tenant of the slot.
			continue
		}
		if getErr != nil {
			return s.fallback.LoadOrStore(key, value)
		}
		decoded, decodeErr := decodeRedisDedupeRecord(raw)
		if decodeErr != nil {
			return s.fallback.LoadOrStore(key, value)
		}
		return decoded, true
	}
	// Race retried out without resolving. Returning a pending wire record
	// with loaded=true routes the caller into waitForRedisDedupe polling
	// (see policy_evaluate.go dedupeBegin's type switch) instead of
	// firing a duplicate upstream call via the fallback's local winner
	// path. Polling re-reads the cross-process state and either observes
	// the winner's completed record or eventually takes the slot via the
	// deadline-breaker — both are safe outcomes.
	return &redisDedupeRecord{State: redisDedupeStatePending}, true
}

// Store publishes a completed record (or any updated wire value) with
// the standard MCPDedupeTTL — every write reapplies the TTL so a
// completed record cannot outlive a pending one and so a slow-Store
// after a near-expiry LoadOrStore still gives waiters the full TTL
// budget to observe it.
//
// On any encode/Redis error: fall back to in-process Store. The
// in-process store cannot disagree with itself across processes, but
// it preserves the per-instance dedupe invariant the gate relies on
// for at-least-one-collapse-during-outage behavior.
func (s *RedisDedupeStore) Store(key string, value any) {
	if s.client == nil {
		s.fallback.Store(key, value)
		return
	}
	encoded, ok := s.encode(value)
	if !ok {
		s.fallback.Store(key, value)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisDedupeCommandTimeout)
	defer cancel()
	if err := s.client.Set(ctx, MCPDedupeKeyPrefix+key, encoded, MCPDedupeTTL).Err(); err != nil {
		s.fallback.Store(key, value)
	}
}

// Delete removes the prefixed key from Redis and the in-process
// fallback. We delete from BOTH unconditionally so a fail-soft write
// that landed in the fallback during a transient Redis outage is also
// cleaned up — leaving a stale fallback entry would cause the next
// retry on this instance to short-circuit on an error record while
// other instances correctly fire fresh upstream calls.
func (s *RedisDedupeStore) Delete(key string) {
	s.fallback.Delete(key)
	if s.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), redisDedupeCommandTimeout)
	defer cancel()
	_ = s.client.Del(ctx, MCPDedupeKeyPrefix+key).Err()
}

// encode serializes the caller's value to the wire format the Redis
// store persists. Two preconditions enforce the epic rail #7 "no large
// raw payloads in Redis" contract:
//
//  1. The value MUST be a *redisDedupeRecord. Any other type indicates
//     a caller bug or a future wire-format drift; we refuse to coerce
//     so the fail-soft fallback catches the mistake without writing a
//     corrupt row.
//  2. The serialized payload MUST be ≤ maxRedisDedupeRecordBytes.
//     Larger payloads (e.g. a jumbo tool response) are dropped from
//     the cross-process cache via the fallback path; per-instance
//     dedupe still works, but the cross-process write is skipped
//     rather than persisting a multi-MB row.
func (s *RedisDedupeStore) encode(value any) ([]byte, bool) {
	rec, ok := value.(*redisDedupeRecord)
	if !ok || rec == nil {
		return nil, false
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		return nil, false
	}
	if len(encoded) > maxRedisDedupeRecordBytes {
		return nil, false
	}
	return encoded, true
}

// DedupeBackendEnvVar names the operator-facing env var controlling
// the cross-process dedupe backend selection. The constant is exposed
// so docs/tests can reference the same literal as the gateway boot
// code that reads it.
const DedupeBackendEnvVar = "CORDUM_MCP_DEDUPE_BACKEND"

// SelectDedupeStore picks the DedupeStore implementation the gateway
// wires into ToolCallDeps based on the caller-supplied backend hint
// (typically `os.Getenv(DedupeBackendEnvVar)`) and an optional shared
// Redis client. The selection matrix mirrors taskRail #3 fail-soft +
// epic rail #3 reuse-existing-client + the task plan step 7 contract:
//
//	hint=="memory"      → in-process (operator opt-out of cross-process)
//	hint=="redis"       → Redis if client != nil, else in-process
//	hint==""    (unset) → Redis if client != nil, else in-process
//	hint == anything else (typo / future value) → in-process (no panic)
//
// Comparison is case-insensitive with surrounding whitespace trimmed
// so a misformatted env var (`CORDUM_MCP_DEDUPE_BACKEND= Redis `) is
// honored as `redis`.
func SelectDedupeStore(hint string, client redis.Cmdable) DedupeStore {
	normalized := strings.ToLower(strings.TrimSpace(hint))
	switch normalized {
	case "memory":
		return NewInProcessDedupeStore()
	case "redis", "":
		if client != nil {
			return NewRedisDedupeStore(client)
		}
		return NewInProcessDedupeStore()
	default:
		return NewInProcessDedupeStore()
	}
}

// decodeRedisDedupeRecord is the inverse of encode. A defensive helper
// rather than inline json.Unmarshal so the State-field shape can be
// validated in one place: an empty State indicates either corrupt wire
// data or a future-version record we should not silently treat as
// pending; we surface the error so the caller falls back rather than
// looping forever on an unrecognizable record.
func decodeRedisDedupeRecord(raw []byte) (*redisDedupeRecord, error) {
	if len(raw) == 0 {
		return nil, errors.New("empty redis dedupe payload")
	}
	var rec redisDedupeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	if rec.State != redisDedupeStatePending && rec.State != redisDedupeStateCompleted {
		return nil, errors.New("unknown redis dedupe state: " + rec.State)
	}
	return &rec, nil
}
