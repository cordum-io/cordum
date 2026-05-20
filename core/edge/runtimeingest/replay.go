package runtimeingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	ReplayWindowKeyPrefix      = "edge:rt:nonce:"
	ReplayWindowTTL            = time.Hour
	MaxReplayWindowCardinality = int64(10000)
)

var ErrReplayWindowFull = errors.New("runtime replay window cap exhausted")

// reserveScript performs the entire Reserve sequence (membership check,
// cap check, SADD, EXPIRE) as one atomic Lua call. The earlier sequence
// of separate go-redis round trips (SCARD → SISMEMBER → SADD → EXPIRE)
// was a TOCTOU: concurrent callers all observed count<maxCard before any
// of them ran SADD, so the cap was approximate rather than enforced;
// and an Expire that failed after SAdd already added a NEW member left
// the key without a TTL, growing memory unboundedly.
//
// Return codes (Redis Lua integer):
//
//	0  → first-seen, member added with TTL applied
//	1  → already a member (replayed), no state change
//	-1 → cap exhausted and member not present (refusal)
const reserveScript = `
if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then
  return 1
end
local sc = redis.call('SCARD', KEYS[1])
if sc >= tonumber(ARGV[3]) then
  return -1
end
redis.call('SADD', KEYS[1], ARGV[1])
redis.call('EXPIRE', KEYS[1], ARGV[2])
return 0
`

type ReplayWindow struct {
	client  redis.Cmdable
	ttl     time.Duration
	maxCard int64
}

func NewReplayWindow(client redis.Cmdable, ttl time.Duration, maxCard int64) *ReplayWindow {
	if ttl <= 0 {
		ttl = ReplayWindowTTL
	}
	if maxCard <= 0 {
		maxCard = MaxReplayWindowCardinality
	}
	return &ReplayWindow{client: client, ttl: ttl, maxCard: maxCard}
}

func (r *ReplayWindow) Reserve(ctx context.Context, tenantID, collectorID, nonce string) (bool, error) {
	key, value, err := r.keyAndValue(tenantID, collectorID, nonce)
	if err != nil {
		return false, err
	}
	ttlSeconds := int64(r.ttl / time.Second)
	if ttlSeconds <= 0 {
		ttlSeconds = 1
	}
	res, err := r.client.Eval(ctx, reserveScript, []string{key}, value, ttlSeconds, r.maxCard).Int64()
	if err != nil {
		return false, err
	}
	switch res {
	case 0:
		return true, nil
	case 1:
		return false, nil
	case -1:
		return false, ErrReplayWindowFull
	default:
		return false, fmt.Errorf("runtime replay window: unexpected script result %d", res)
	}
}

func (r *ReplayWindow) Release(ctx context.Context, tenantID, collectorID, nonce string) error {
	key, value, err := r.keyAndValue(tenantID, collectorID, nonce)
	if err != nil {
		return err
	}
	return r.client.SRem(ctx, key, value).Err()
}

func (r *ReplayWindow) keyAndValue(tenantID, collectorID, nonce string) (string, string, error) {
	if r == nil || r.client == nil {
		return "", "", errors.New("runtime replay window redis client unavailable")
	}
	tenantID = strings.TrimSpace(tenantID)
	collectorID = strings.TrimSpace(collectorID)
	nonce = strings.TrimSpace(nonce)
	if tenantID == "" || collectorID == "" || nonce == "" {
		return "", "", fmt.Errorf("runtime replay window requires tenant_id, collector_id, and nonce")
	}
	key := ReplayWindowKeyPrefix + replayKeyComponentDigest(tenantID) + ":" + replayKeyComponentDigest(collectorID)
	return key, replayNonceDigest(nonce), nil
}

func replayKeyComponentDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func replayNonceDigest(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return hex.EncodeToString(sum[:])
}
