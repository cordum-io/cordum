package replay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	capsdk "github.com/cordum-io/cap/v2/sdk/go"
	"github.com/redis/go-redis/v9"
)

// claimScript runs Claim's decision atomically on Admit's single key. A claim
// record is v2|<state>|<digest hex>|<token>|<lease-until unix ms>, with state
// p (processing), s (succeeded) or f (failed). A plain digest is an Admit
// record and counts as completed. Rewrites keep the key's TTL.
//
// KEYS[1]   replay entry key
// ARGV[1]   hex-encoded signed-body digest
// ARGV[2]   TTL in milliseconds for a new record
// ARGV[3]   now, unix ms
// ARGV[4]   lease end for this claim, unix ms
// ARGV[5]   fresh fencing token
var claimScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
local claim = 'v2|p|' .. ARGV[1] .. '|' .. ARGV[5] .. '|' .. ARGV[4]
if not v then
  redis.call('SET', KEYS[1], claim, 'PX', ARGV[2])
  return {'acquired'}
end
if string.sub(v, 1, 3) ~= 'v2|' then
  if v == ARGV[1] then return {'completed'} end
  return {'conflict'}
end
local state, digest, leaseUntil = string.match(v, '^v2|(%a)|(%x+)|[^|]+|(%d+)$')
if digest ~= ARGV[1] then return {'conflict'} end
if state ~= 'p' then return {'completed'} end
local remaining = tonumber(leaseUntil) - tonumber(ARGV[3])
if remaining > 0 then return {'busy', remaining} end
redis.call('SET', KEYS[1], claim, 'KEEPTTL')
return {'acquired'}
`)

// settleScript writes a claim's outcome only while the record is still
// processing under the claim's token, which fences out a taken-over delivery.
//
// KEYS[1]   replay entry key
// ARGV[1]   the claim's token
// ARGV[2]   new state; p releases the claim with an expired lease
var settleScript = redis.NewScript(`
local v = redis.call('GET', KEYS[1])
if not v then return 'lost' end
local digest, token = string.match(v, '^v2|p|(%x+)|([^|]+)|%d+$')
if token ~= ARGV[1] then return 'lost' end
redis.call('SET', KEYS[1], 'v2|' .. ARGV[2] .. '|' .. digest .. '|' .. token .. '|0', 'KEEPTTL')
return 'ok'
`)

// ClaimState is the replay record's answer to Claim.
type ClaimState uint8

const (
	// ClaimAcquired means this delivery owns the message and must Settle it.
	ClaimAcquired ClaimState = iota + 1
	// ClaimCompleted means an earlier delivery succeeded or failed for good.
	ClaimCompleted
	// ClaimBusy means another delivery holds a live lease; retry after RetryAfter.
	ClaimBusy
)

// Claim is one delivery's answer from Claim. An acquired Claim carries the
// record key and fencing token that Settle needs.
type Claim struct {
	State      ClaimState
	RetryAfter time.Duration
	key, token string
}

// SettleOutcome is how an acquired delivery ended.
type SettleOutcome uint8

const (
	// SettleSucceeded completes the message.
	SettleSucceeded SettleOutcome = iota + 1
	// SettleFailed completes the message with a terminal failure.
	SettleFailed
	// SettleReleased gives the message up so the next delivery takes it over.
	SettleReleased
)

var settleStates = map[SettleOutcome]string{SettleSucceeded: "s", SettleFailed: "f", SettleReleased: "p"}

// ErrClaimLost reports a Settle by a delivery that no longer owns the record:
// its lease ran out and another delivery took the message over.
var ErrClaimLost = errors.New("replay: claim lost to another delivery")

// Claim takes a lease on a message for one delivery. Unlike Admit, admission
// does not complete the message: only Settle with SettleSucceeded or
// SettleFailed does. A later delivery takes the message over once the lease
// ends (the owner stopped) or the owner released it (a retry), and is told to
// wait while the lease is live. lease must be positive. Errors match Admit's.
func (s *RedisReplayStore) Claim(
	tenant, audience, sender string,
	messageID, digest []byte,
	expires time.Time,
	lease time.Duration,
) (Claim, error) {
	key, now, ttl, err := s.entry(tenant, audience, sender, messageID, digest, expires)
	if err != nil {
		return Claim{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	token := rand.Text()
	res, err := claimScript.Run(ctx, s.client, []string{key}, hex.EncodeToString(digest),
		ttl.Milliseconds(), now.UnixMilli(), now.Add(lease).UnixMilli(), token).Slice()
	if err != nil || len(res) == 0 {
		return Claim{}, capsdk.ErrReplayStoreUnavailable
	}
	switch res[0] {
	case "acquired":
		return Claim{State: ClaimAcquired, key: key, token: token}, nil
	case "completed":
		return Claim{State: ClaimCompleted}, nil
	case "busy":
		remaining, _ := res[len(res)-1].(int64)
		return Claim{State: ClaimBusy, RetryAfter: time.Duration(remaining) * time.Millisecond}, nil
	case "conflict":
		return Claim{}, capsdk.ErrReplayConflict
	default:
		return Claim{}, capsdk.ErrReplayStoreUnavailable
	}
}

// Settle records how the delivery holding c ended. It writes nothing and
// returns ErrClaimLost once another delivery has taken the message over.
// SettleReleased keeps the record processing with an expired lease, so the
// next delivery takes the message over at once.
func (s *RedisReplayStore) Settle(c Claim, outcome SettleOutcome) error {
	state, ok := settleStates[outcome]
	if !ok {
		return fmt.Errorf("replay: unknown settle outcome %d", outcome)
	}
	if s == nil || s.client == nil {
		return capsdk.ErrReplayStoreUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	res, err := settleScript.Run(ctx, s.client, []string{c.key}, c.token, state).Text()
	if err != nil {
		return capsdk.ErrReplayStoreUnavailable
	}
	if res != "ok" {
		return ErrClaimLost
	}
	return nil
}
