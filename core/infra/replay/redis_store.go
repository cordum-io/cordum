// Package replay provides the shared, atomic replay-defense store required by
// the CAP-PRODUCTION profile (task-a13f83fa, step 12).
//
// The CAP SDK ships only an in-process replay store. That is insufficient for
// a real deployment: with more than one control-plane replica an attacker can
// replay a captured packet to a replica that has never seen its message ID and
// have it admitted. This store moves the first/duplicate/conflict decision into
// a single atomic Redis script so all replicas share one view.
package replay

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/redis/go-redis/v9"
)

// DefaultKeyPrefix namespaces replay entries inside the shared Redis instance.
const DefaultKeyPrefix = "cap:replay:"

// admitScript performs the whole first/duplicate/conflict decision atomically.
//
// It touches exactly ONE key, which also keeps it safe under Redis Cluster:
// a multi-key script without a common hash tag would fail with CROSSSLOT.
//
// KEYS[1]   replay entry key
// ARGV[1]   hex-encoded signed-body digest
// ARGV[2]   TTL in milliseconds
var admitScript = redis.NewScript(`
local existing = redis.call('GET', KEYS[1])
if existing then
  if existing == ARGV[1] then
    return 'duplicate'
  end
  return 'conflict'
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 'first'
`)

// RedisReplayStore is a shared capsdk.ReplayStore backed by Redis.
type RedisReplayStore struct {
	client    redis.UniversalClient
	keyPrefix string
	timeout   time.Duration
	now       func() time.Time
}

// Option customises a RedisReplayStore.
type Option func(*RedisReplayStore)

// WithKeyPrefix overrides the Redis key namespace.
func WithKeyPrefix(prefix string) Option {
	return func(s *RedisReplayStore) {
		if prefix != "" {
			s.keyPrefix = prefix
		}
	}
}

// WithTimeout bounds each Redis round trip. A hung store must surface as
// unavailable (and therefore rejection) rather than blocking admission.
func WithTimeout(d time.Duration) Option {
	return func(s *RedisReplayStore) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// WithClock overrides the time source, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *RedisReplayStore) {
		if now != nil {
			s.now = now
		}
	}
}

// NewRedisReplayStore builds a store over client. A nil client yields a store
// that reports ErrReplayStoreUnavailable rather than panicking, so a
// misconfigured deployment fails closed instead of crashing mid-admission.
func NewRedisReplayStore(client redis.UniversalClient, opts ...Option) *RedisReplayStore {
	s := &RedisReplayStore{
		client:    client,
		keyPrefix: DefaultKeyPrefix,
		timeout:   2 * time.Second,
		now:       time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ErrIncompleteReplayIdentity reports a replay lookup missing a tuple
// component. It is deliberately distinct from ErrReplayStoreUnavailable: the
// packet is malformed, so retrying it can never help.
var ErrIncompleteReplayIdentity = errors.New("replay: incomplete replay identity")

// Admit implements capsdk.ReplayStore.
//
// It returns ReplayOutcomeFirst for a newly seen message, ReplayOutcomeDuplicate
// for an identical redelivery (safe to ACK and drop), capsdk.ErrReplayConflict
// when the same message ID arrives with a different signed body, and
// capsdk.ErrReplayStoreUnavailable when the decision cannot be made — which
// callers MUST treat as rejection under CAP-PRODUCTION.
func (s *RedisReplayStore) Admit(
	tenant, audience, sender string,
	messageID, digest []byte,
	expires time.Time,
) (capsdk.ReplayOutcome, error) {
	if s == nil || s.client == nil {
		return 0, capsdk.ErrReplayStoreUnavailable
	}
	if tenant == "" || audience == "" || sender == "" || len(messageID) == 0 || len(digest) == 0 {
		return 0, ErrIncompleteReplayIdentity
	}
	ttl := expires.Sub(s.now())
	if ttl <= 0 {
		return 0, fmt.Errorf("%w: packet already expired", ErrIncompleteReplayIdentity)
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	key := s.entryKey(tenant, audience, sender, messageID)
	raw, err := admitScript.Run(ctx, s.client, []string{key},
		hex.EncodeToString(digest), ttl.Milliseconds()).Text()
	if err != nil {
		// Any transport/script failure is unavailability. Never downgrade to
		// "first" — that would admit an unverified replay.
		return 0, capsdk.ErrReplayStoreUnavailable
	}

	switch raw {
	case "first":
		return capsdk.ReplayOutcomeFirst, nil
	case "duplicate":
		return capsdk.ReplayOutcomeDuplicate, nil
	case "conflict":
		return 0, capsdk.ErrReplayConflict
	default:
		return 0, capsdk.ErrReplayStoreUnavailable
	}
}

// entryKey derives a delimiter-safe, bounded-length key from the full replay
// tuple.
//
// Identifiers may contain ':' (tenant paths, subject tokens, sender URNs), so a
// plain join is ambiguous: {tenant "a:b", sender "c"} and {tenant "a", sender
// "b:c"} would produce the same key and let one identity suppress another's
// messages. Each component is therefore length-prefixed before hashing, which
// makes the encoding injective, and the digest bounds the key length regardless
// of how long the identifiers are.
func (s *RedisReplayStore) entryKey(tenant, audience, sender string, messageID []byte) string {
	h := sha256.New()
	writeLengthPrefixed(h, []byte(tenant))
	writeLengthPrefixed(h, []byte(audience))
	writeLengthPrefixed(h, []byte(sender))
	writeLengthPrefixed(h, messageID)
	return s.keyPrefix + hex.EncodeToString(h.Sum(nil))
}

func writeLengthPrefixed(h interface{ Write([]byte) (int, error) }, b []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(b)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(b)
}
