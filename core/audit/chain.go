package audit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Chain keyspace layout:
//
//	audit:chain:<tenant>        — Redis Stream; one entry per audit event.
//	                              Fields: "seq" (int64 as string) and
//	                              "event" (canonical JSON payload).
//	audit:chain:head:<tenant>   — Plain key holding "seq:event_hash" for
//	                              the tenant's current chain head. Empty
//	                              or missing means the tenant has no
//	                              events yet (next append is genesis).
const (
	// ChainKeyPrefix is the default namespace for chain Redis keys.
	ChainKeyPrefix = "audit:chain:"

	// EnvHMACKey is the environment variable that supplies the hex-encoded
	// HMAC-SHA256 signing key. When set, every appended audit event is
	// signed with this key. Generate with: openssl rand -hex 32
	EnvHMACKey = "CORDUM_AUDIT_HMAC_KEY"

	chainHeadInfix = "head:"
	// chainMaxCASRetries caps how many times a single Append will retry
	// on a contended head pointer. Under 100 concurrent writers on one
	// tenant the total attempt count is ≈ producers² / 2, so budget
	// 1024 comfortably handles the soak workload while still catching
	// pathological head-key corruption that no amount of retrying can
	// recover from.
	chainMaxCASRetries = 1024
	// chainCASBackoffMin / chainCASBackoffMax bound the jittered
	// backoff between CAS retries. Without backoff, contending
	// producers retry in lockstep and waste work — a randomized pause
	// spreads them so throughput stays smooth under burst load.
	chainCASBackoffMin = 50 * time.Microsecond
	chainCASBackoffMax = 4 * time.Millisecond
	chainHashHexLen    = 64 // sha256 hex

	// chainStreamFieldEvent is the Redis Stream field holding the canonical
	// event JSON. Matches the literal used by chainAppendScript.
	chainStreamFieldEvent = "event"
)

// Sentinel errors returned by Chainer. Exported so callers can distinguish
// operational failures (retryable / loggable) from malformed inputs.
var (
	// ErrTenantRequired is returned when an event missing TenantID is
	// passed to Append — the chain is per-tenant so every event needs one.
	ErrTenantRequired = errors.New("audit chain: event TenantID is required")
	// ErrCASExhausted is returned when CAS retries on the head pointer hit
	// the budget without committing. Indicates catastrophic contention
	// (e.g. head key corruption or a runaway producer) — the caller should
	// fail the pipeline rather than silently drop the event.
	ErrCASExhausted = errors.New("audit chain: CAS retry budget exhausted")
	// ErrNilEvent guards against accidental nil derefs from wrapped call
	// sites; surfaces as a distinct sentinel so test assertions are clean.
	ErrNilEvent = errors.New("audit chain: nil event")
)

// Chainer builds and persists a per-tenant append-only SHA-256 hash chain of
// audit events in Redis. One Chainer is safe to share across goroutines; local
// per-tenant locks avoid self-contention while the CAS-based Lua append still
// protects the head pointer across processes.
//
// When an HMAC key is configured (via WithHMACKey), every appended event also
// receives an HMAC-SHA256 tag computed over the canonical event payload. The
// HMAC proves the event was produced by a process holding the signing key,
// closing the threat model gap where an attacker with Redis write access could
// forge a valid SHA-256 chain by recomputing hashes from an arbitrary point.
type Chainer struct {
	client          redis.UniversalClient
	keyPrefix       string
	hmacKey         []byte // nil = HMAC disabled
	headCacheMu     sync.Mutex
	headCacheValid  bool
	headCacheTenant string
	headCacheRaw    string
	tenantLocksMu   sync.Mutex
	tenantLocks     map[string]*sync.Mutex
}

// ChainerOption configures optional Chainer behaviour.
type ChainerOption func(*Chainer)

// WithHMACKey enables HMAC-SHA256 event authentication. The key must be at
// least 32 bytes (256 bits); shorter keys are logged as an error and
// silently ignored (HMAC stays disabled) rather than crashing the process.
// A nil or empty key disables HMAC (the default).
//
// Key rotation: deploy the new key via CORDUM_AUDIT_HMAC_KEY on all
// replicas. Events signed with the old key will show hmac_mismatch
// during verification — operators note the seq boundary where the
// key changed. Pre-HMAC events (no tag) are hmac_skipped, not failed.
func WithHMACKey(key []byte) ChainerOption {
	return func(c *Chainer) {
		if len(key) == 0 {
			c.hmacKey = nil
			return
		}
		if len(key) < 32 {
			slog.Error("audit chain: HMAC key must be at least 32 bytes — HMAC disabled",
				"got_bytes", len(key),
				"hint", "generate a valid key with: openssl rand -hex 32",
			)
			c.hmacKey = nil
			return
		}
		c.hmacKey = make([]byte, len(key))
		copy(c.hmacKey, key)
	}
}

// NewChainer wires a Chainer around the given client. An empty keyPrefix
// falls back to the default "audit:chain:" namespace; tests and multi-env
// deployments can override so their chains do not collide.
func NewChainer(client redis.UniversalClient, keyPrefix string, opts ...ChainerOption) *Chainer {
	if keyPrefix == "" {
		keyPrefix = ChainKeyPrefix
	}
	c := &Chainer{client: client, keyPrefix: keyPrefix}
	for _, o := range opts {
		o(c)
	}
	return c
}

// HMACEnabled reports whether this Chainer signs events with HMAC-SHA256.
// Exposed so the gateway boot log and the consumer can include the state.
func (c *Chainer) HMACEnabled() bool {
	return len(c.hmacKey) > 0
}

// HMACKeyForVerify returns a copy of the HMAC key for use by the verify
// handler. Returns nil when HMAC is disabled. The returned slice is a
// defensive copy so the caller cannot mutate the Chainer's internal key.
func (c *Chainer) HMACKeyForVerify() []byte {
	if len(c.hmacKey) == 0 {
		return nil
	}
	out := make([]byte, len(c.hmacKey))
	copy(out, c.hmacKey)
	return out
}

// StreamKey returns the Redis Stream key that holds this tenant's chain.
// Exported so the verify handler and tests can reach the same key without
// duplicating the prefix math.
func (c *Chainer) StreamKey(tenant string) string {
	return c.keyPrefix + tenant
}

// HeadKey returns the Redis key holding "seq:hash" of the tenant's head.
func (c *Chainer) HeadKey(tenant string) string {
	return c.keyPrefix + chainHeadInfix + tenant
}

func (c *Chainer) cachedHead(tenant string) (string, bool) {
	c.headCacheMu.Lock()
	defer c.headCacheMu.Unlock()
	if !c.headCacheValid || c.headCacheTenant != tenant {
		return "", false
	}
	return c.headCacheRaw, true
}

func (c *Chainer) storeCachedHead(tenant, rawHead string) {
	c.headCacheMu.Lock()
	defer c.headCacheMu.Unlock()
	c.headCacheValid = true
	c.headCacheTenant = tenant
	c.headCacheRaw = rawHead
}

func (c *Chainer) invalidateCachedHead(tenant, rawHead string) {
	c.headCacheMu.Lock()
	defer c.headCacheMu.Unlock()
	if c.headCacheValid && c.headCacheTenant == tenant && c.headCacheRaw == rawHead {
		c.headCacheValid = false
		c.headCacheTenant = ""
		c.headCacheRaw = ""
	}
}

func (c *Chainer) lockTenant(tenant string) func() {
	c.tenantLocksMu.Lock()
	if c.tenantLocks == nil {
		c.tenantLocks = make(map[string]*sync.Mutex)
	}
	mu := c.tenantLocks[tenant]
	if mu == nil {
		mu = &sync.Mutex{}
		c.tenantLocks[tenant] = mu
	}
	c.tenantLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// chainAppendScript is a CAS (check-and-set) Lua append. Go precomputes the
// event_hash using the just-read head as PrevHash input; the script only
// commits if the head has not shifted between read and write.
//
// KEYS[1] = head key      ARGV[1] = expected_head ("seq:hash" or empty)
// KEYS[2] = stream key    ARGV[2] = new_seq (string int)
//
//	ARGV[3] = new_hash (64-char hex)
//	ARGV[4] = event JSON payload
//
// Returns 1 on commit, 0 on CAS miss (caller re-reads head and retries).
// Using a Lua script for the critical section keeps the check-XADD-update
// trio atomic under Redis single-threaded command execution, so two racing
// producers cannot both see the same head and both commit at the same seq.
// chainAppendScript commits a new chain entry under the tenant's head
// pointer. The Go side passes its observed head via ARGV[1]; the
// script re-reads head under Redis's single-threaded execution and
// refuses the commit when it shifted (CAS miss -> return 0).
//
// Guard against a head-poison attack where an operator DELs the head
// key while the stream still carries entries: if the caller claims
// head is empty (genesis) we additionally require the stream to be
// genuinely empty via XLEN. Otherwise a malicious DEL would let seq=1
// collide with an existing seq=1, corrupting the chain without the
// CAS check firing.
var chainAppendScript = redis.NewScript(`
local head = redis.call('GET', KEYS[1])
if not head then head = '' end
if head ~= ARGV[1] then return 0 end
if ARGV[1] == '' then
  local streamLen = redis.call('XLEN', KEYS[2])
  if streamLen ~= 0 then return 0 end
end
redis.call('XADD', KEYS[2], '*', 'seq', ARGV[2], 'event', ARGV[4])
redis.call('SET', KEYS[1], ARGV[2] .. ':' .. ARGV[3])
return 1
`)

// Append links event into its tenant's chain. On success event.Seq,
// event.PrevHash, and event.EventHash are populated in place.
//
// The event_hash is SHA-256 of the event's canonical JSON encoding with
// Seq and EventHash cleared. PrevHash is part of the hashed bytes so any
// tampering with a predecessor (direct mutation or reordering) invalidates
// every descendant hash — this is what gives the chain its tamper-evidence.
func (c *Chainer) Append(ctx context.Context, event *SIEMEvent) error {
	if event == nil {
		return ErrNilEvent
	}
	if event.TenantID == "" {
		return ErrTenantRequired
	}
	unlockTenant := c.lockTenant(event.TenantID)
	defer unlockTenant()

	headKey := c.HeadKey(event.TenantID)
	streamKey := c.StreamKey(event.TenantID)

	for attempt := 0; attempt < chainMaxCASRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		rawHead, ok := c.cachedHead(event.TenantID)
		if !ok {
			var err error
			rawHead, err = c.client.Get(ctx, headKey).Result()
			if err != nil && !errors.Is(err, redis.Nil) {
				return fmt.Errorf("audit chain: read head: %w", err)
			}
			if errors.Is(err, redis.Nil) {
				rawHead = ""
			}
		}

		headSeq, headHash, err := parseChainHead(rawHead)
		if err != nil {
			return fmt.Errorf("audit chain: parse head: %w", err)
		}

		event.PrevHash = headHash
		event.Seq = headSeq + 1
		eventHash, err := computeEventHash(event)
		if err != nil {
			return fmt.Errorf("audit chain: compute hash: %w", err)
		}
		event.EventHash = eventHash

		// Compute HMAC after the event hash so the HMAC covers the full
		// canonical payload including PrevHash (forward tamper propagation).
		if len(c.hmacKey) > 0 {
			macTag, macErr := computeEventHMAC(event, c.hmacKey)
			if macErr != nil {
				return fmt.Errorf("audit chain: compute hmac: %w", macErr)
			}
			event.HMAC = macTag
		}

		payload, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("audit chain: marshal event: %w", err)
		}

		res, err := chainAppendScript.Run(ctx, c.client,
			[]string{headKey, streamKey},
			rawHead,
			strconv.FormatInt(event.Seq, 10),
			eventHash,
			string(payload),
		).Int()
		if err != nil {
			return fmt.Errorf("audit chain: script run: %w", err)
		}
		if res == 1 {
			c.storeCachedHead(event.TenantID, strconv.FormatInt(event.Seq, 10)+":"+eventHash)
			return nil
		}
		// CAS miss: another writer beat us. Clear the in-place
		// mutations so a retry does not carry stale state if the
		// subsequent read errors, then back off with jitter so
		// contending producers stop retrying in lockstep.
		c.invalidateCachedHead(event.TenantID, rawHead)
		event.Seq = 0
		event.PrevHash = ""
		event.EventHash = ""
		event.HMAC = ""
		if err := sleepCASBackoff(ctx, attempt); err != nil {
			return err
		}
	}

	return ErrCASExhausted
}

// sleepCASBackoff pauses for a jittered duration that grows with
// attempt count (capped at chainCASBackoffMax). Cancelled by ctx —
// returning ctx.Err() so callers surface cancellation rather than
// spinning the retry loop to exhaustion.
func sleepCASBackoff(ctx context.Context, attempt int) error {
	//nolint:gosec // rand.Int64N is non-crypto; jitter only.
	base := chainCASBackoffMin << attempt //nolint:gosec
	if base <= 0 || base > chainCASBackoffMax {
		base = chainCASBackoffMax
	}
	jitter := time.Duration(rand.Int64N(int64(base))) // #nosec G404 -- CAS retry jitter is not security-sensitive.
	d := jitter + chainCASBackoffMin
	if d > chainCASBackoffMax {
		d = chainCASBackoffMax
	}
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// parseChainHead decodes the "seq:hash" representation written by the Lua
// append. An empty string (fresh tenant) resolves to (0, "") — the next
// event becomes the genesis for that tenant.
func parseChainHead(raw string) (int64, string, error) {
	if raw == "" {
		return 0, "", nil
	}
	colon := strings.IndexByte(raw, ':')
	if colon < 0 {
		return 0, "", fmt.Errorf("head missing separator: %q", raw)
	}
	seq, err := strconv.ParseInt(raw[:colon], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("parse seq: %w", err)
	}
	if seq < 0 {
		return 0, "", fmt.Errorf("negative seq: %d", seq)
	}
	hash := raw[colon+1:]
	if hash != "" && len(hash) != chainHashHexLen {
		return 0, "", fmt.Errorf("hash wrong length: got %d want %d", len(hash), chainHashHexLen)
	}
	return seq, hash, nil
}

// computeEventHash returns the canonical SHA-256 (hex) of the event with
// its Seq and EventHash fields cleared. PrevHash is intentionally included
// in the hashed bytes so the chain has forward-cascading tamper evidence.
//
// Determinism note: Go's encoding/json emits struct fields in declaration
// order and sorts map keys alphabetically, so marshalling the same event
// twice produces identical bytes. That is load-bearing — the verify path
// re-computes this hash and must reach the same value.
func computeEventHash(event *SIEMEvent) (string, error) {
	clone := *event
	clone.Seq = 0
	clone.EventHash = ""
	clone.HMAC = ""
	b, err := json.Marshal(&clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// computeEventHMAC returns the HMAC-SHA256 (hex) of the canonical event
// payload. The same fields are cleared as for computeEventHash (Seq,
// EventHash, HMAC) so the HMAC is deterministic across re-computation.
// PrevHash is included so the HMAC inherits the forward tamper propagation
// property of the hash chain.
func computeEventHMAC(event *SIEMEvent, key []byte) (string, error) {
	clone := *event
	clone.Seq = 0
	clone.EventHash = ""
	clone.HMAC = ""
	b, err := json.Marshal(&clone)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifyEventHash recomputes the canonical event hash and returns true when
// it matches the stored EventHash. Used by the verify handler and tests to
// detect mutation of a persisted event's payload.
func VerifyEventHash(event *SIEMEvent) (bool, error) {
	want := event.EventHash
	got, err := computeEventHash(event)
	if err != nil {
		return false, err
	}
	return want == got, nil
}

// VerifyEventHMAC recomputes the HMAC-SHA256 of the canonical event payload
// using the provided key, and returns true when it matches the stored HMAC.
// Returns (true, nil) when the event carries no HMAC (backward compat with
// events appended before HMAC was enabled) or when the key is nil/empty.
// Uses constant-time comparison to prevent timing side-channels.
func VerifyEventHMAC(event *SIEMEvent, key []byte) (bool, error) {
	if event.HMAC == "" || len(key) == 0 {
		return true, nil // HMAC not applicable
	}
	got, err := computeEventHMAC(event, key)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(event.HMAC), []byte(got)) == 1, nil
}
