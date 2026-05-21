package outbound

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// NonceStoreCommandTimeout bounds every NonceStore SeenAndRecord call.
// A wedged Redis must not freeze the verifier indefinitely — pre-fix,
// RedisNonceStore captured ctx=context.Background() at construction
// and every verifier inherited it, turning a slow Redis into a DoS
// amplifier on the signed-call verify endpoint.
const NonceStoreCommandTimeout = 500 * time.Millisecond

// NonceStore is the replay-protection substrate. SeenAndRecord returns
// true when the nonce has already been observed within its TTL,
// atomically recording it for future calls when not seen. The ctx
// parameter bounds the underlying store call so a wedged backend
// surfaces as ctx.Err() instead of a stuck goroutine.
type NonceStore interface {
	SeenAndRecord(ctx context.Context, nonce string, ttl time.Duration) (bool, error)
}

// InMemoryNonceStore is a mutex-protected map keyed on nonce. Suitable
// for single-replica dev deploys; loses state on restart and does not
// replicate across HA instances — use RedisNonceStore in production.
type InMemoryNonceStore struct {
	mu   sync.Mutex
	seen map[string]time.Time
	now  func() time.Time
}

// NewInMemoryNonceStore returns an empty store. The optional now
// argument lets tests inject a deterministic clock.
func NewInMemoryNonceStore() *InMemoryNonceStore {
	return &InMemoryNonceStore{seen: make(map[string]time.Time), now: func() time.Time { return time.Now().UTC() }}
}

// SeenAndRecord satisfies NonceStore. ctx is accepted for interface
// uniformity with RedisNonceStore but ignored here — the in-memory
// path has no I/O, so cancellation only matters when the call would
// otherwise block. Expired entries are opportunistically garbage-
// collected on every call so long-running processes don't leak
// memory — cheap compared to a dedicated reaper goroutine.
func (s *InMemoryNonceStore) SeenAndRecord(_ context.Context, nonce string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = DefaultClockSkew
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if expiry, ok := s.seen[nonce]; ok && now.Before(expiry) {
		return true, nil
	}
	s.seen[nonce] = now.Add(ttl)
	// Opportunistic GC — drop every entry past its TTL. O(n) on a
	// process with thousands of live nonces, but live-nonce-count is
	// bounded by outbound QPS × TTL which stays small in practice.
	for k, v := range s.seen {
		if now.After(v) {
			delete(s.seen, k)
		}
	}
	return false, nil
}

// Size returns the current number of tracked nonces. Exposed for
// observability + tests.
func (s *InMemoryNonceStore) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}

// RedisNonceStore uses SETNX-with-EX for cross-replica replay
// protection. TTL on the key gives automatic cleanup; no reaper
// goroutine needed.
type RedisNonceStore struct {
	client redis.UniversalClient
	prefix string
}

// NewRedisNonceStore wires a store. prefix is prepended to each nonce
// before the SET NX EX call so dev/test/prod deploys sharing the same
// Redis don't collide with each other's nonce spaces.
func NewRedisNonceStore(client redis.UniversalClient, prefix string) *RedisNonceStore {
	if prefix == "" {
		prefix = "mcp:outbound:nonce:"
	}
	return &RedisNonceStore{client: client, prefix: prefix}
}

// SeenAndRecord satisfies NonceStore via Redis SET NX EX. First write
// wins; subsequent writes within the TTL return (true, nil). The
// caller-supplied ctx is wrapped with NonceStoreCommandTimeout so a
// wedged Redis surfaces as a bounded error instead of a stuck verifier
// goroutine; if the caller already attached a tighter deadline, that
// deadline still wins.
func (s *RedisNonceStore) SeenAndRecord(ctx context.Context, nonce string, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = DefaultClockSkew
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cmdCtx, cancel := context.WithTimeout(ctx, NonceStoreCommandTimeout)
	defer cancel()
	ok, err := s.client.SetNX(cmdCtx, s.prefix+nonce, "1", ttl).Result()
	if err != nil {
		return false, err
	}
	// ok==true means the write succeeded, i.e. nonce was NOT seen.
	return !ok, nil
}
