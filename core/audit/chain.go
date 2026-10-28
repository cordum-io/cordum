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

	"github.com/cordum/cordum/core/infra/redisutil"
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
//	                              events yet (next append is genesis). On
//	                              Redis Cluster it is a hint written after
//	                              each append (see appendCluster).
//	audit:chain:anchor:<tenant> — "seq:event_hash:stream_id[:hmac]" of the
//	                              newest event the retention sweeper trimmed
//	                              (retentionAnchor). Missing means the chain
//	                              was never trimmed.
const (
	// ChainKeyPrefix is the default namespace for chain Redis keys.
	ChainKeyPrefix = "audit:chain:"

	// EnvHMACKey is the environment variable that supplies the hex-encoded
	// HMAC-SHA256 signing key. When set, every appended audit event is
	// signed with this key. Generate with: openssl rand -hex 32
	EnvHMACKey = "CORDUM_AUDIT_HMAC_KEY"

	// EnvHMACOptional, when set to true, allows production boot without
	// CORDUM_AUDIT_HMAC_KEY (escape hatch; logs a loud warning). Required
	// because the audit chain underwrites ProvenanceGate audit_chain_compromised
	// — unsigned chains weaken that defense.
	EnvHMACOptional = "CORDUM_AUDIT_HMAC_OPTIONAL"

	chainHeadInfix   = "head:"
	chainAnchorInfix = "anchor:"
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
	cluster         bool // client is a Redis Cluster client: see appendCluster
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
// least 32 bytes (256 bits); a non-empty shorter key PANICS — the caller
// cannot recover and silent downgrade-to-no-HMAC would weaken the chain
// invisibly (BUG-013). A nil or empty key disables HMAC (the default).
// Callers wiring this from env should pre-validate the length and emit a
// clean startup error instead of letting this option panic — see
// initAuditPipeline at gateway.go:1032 for the canonical pattern.
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
			panic(fmt.Sprintf("audit chain: HMAC key must be at least 32 bytes (got %d); generate one with: openssl rand -hex 32", len(key)))
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
	c := &Chainer{client: client, cluster: redisutil.IsCluster(client), keyPrefix: keyPrefix}
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

// AnchorKey returns the Redis key holding the retention anchor: the newest
// event the retention sweeper trimmed from the tenant's chain. Verify callers
// pass it as VerifyOptions.RetentionAnchorKey.
func (c *Chainer) AnchorKey(tenant string) string {
	return c.keyPrefix + chainAnchorInfix + tenant
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
	if c.cluster {
		return c.appendCluster(ctx, event)
	}

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
		payload, err := c.seal(event)
		if err != nil {
			return err
		}
		eventHash := event.EventHash

		res, err := chainAppendScript.Run(ctx, c.client,
			[]string{headKey, streamKey},
			rawHead,
			strconv.FormatInt(event.Seq, 10),
			eventHash,
			payload,
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

// seal sets the event's EventHash and, with an HMAC key, its HMAC for the Seq
// and PrevHash already on it, and returns the payload to store. The HMAC is
// computed after the hash so it covers the full canonical payload including
// PrevHash (forward tamper propagation).
func (c *Chainer) seal(event *SIEMEvent) (string, error) {
	eventHash, err := computeEventHash(event)
	if err != nil {
		return "", fmt.Errorf("audit chain: compute hash: %w", err)
	}
	event.EventHash = eventHash
	if len(c.hmacKey) > 0 {
		macTag, macErr := computeEventHMAC(event, c.hmacKey)
		if macErr != nil {
			return "", fmt.Errorf("audit chain: compute hmac: %w", macErr)
		}
		event.HMAC = macTag
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("audit chain: marshal event: %w", err)
	}
	return string(payload), nil
}

// clusterAppendScript is chainAppendScript for Redis Cluster, where a
// tenant's head key and stream hash to different slots and one script cannot
// touch both. It appends only while the stream's last entry is still the one
// the caller linked to, so concurrent producers serialize on the stream.
//
// KEYS[1] = stream key   ARGV[1] = expected last entry ID, empty for an empty stream
//
//	ARGV[2] = new_seq
//	ARGV[3] = event JSON payload
//
// Returns 1 on commit, 0 when the tail moved (caller re-reads and retries).
var clusterAppendScript = redis.NewScript(`
local last = redis.call('XREVRANGE', KEYS[1], '+', '-', 'COUNT', 1)
local tail = ''
if #last > 0 then tail = last[1][1] end
if tail ~= ARGV[1] then return 0 end
redis.call('XADD', KEYS[1], '*', 'seq', ARGV[2], 'event', ARGV[3])
return 1
`)

// clusterHeadScript records "seq:event_hash" (ARGV[2]) as the head hint
// (KEYS[1]) unless the hint already holds seq ARGV[1] or later: a slow
// producer must not move it back, which would let a later tail deletion
// reuse seqs unnoticed.
var clusterHeadScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur then
  local seq = tonumber(string.match(cur, '^(%d+):'))
  if seq and seq >= tonumber(ARGV[1]) then return 0 end
end
redis.call('SET', KEYS[1], ARGV[2])
return 1
`)

// appendCluster is Append on Redis Cluster. The stream is the source of
// truth: the event links to the stream's last entry and commits with a
// single-key compare-and-append (clusterAppendScript). The head key is then
// moved forward as a hint for tenant discovery; a crash before that leaves it
// behind the stream, which is harmless. It is never used for linkage, only
// as a floor for the next seq: a head ahead of the stream means entries were
// deleted from the tail, and numbering past it keeps that deletion visible as
// missing seqs, as chainAppendScript does on a single node.
func (c *Chainer) appendCluster(ctx context.Context, event *SIEMEvent) error {
	headKey := c.HeadKey(event.TenantID)
	streamKey := c.StreamKey(event.TenantID)
	for attempt := 0; attempt < chainMaxCASRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		headSeq, tail, err := c.readClusterTail(ctx, headKey, streamKey)
		if err != nil {
			return err
		}
		event.PrevHash = tail.Hash
		event.Seq = max(tail.Seq, headSeq) + 1
		payload, err := c.seal(event)
		if err != nil {
			return err
		}
		seq := strconv.FormatInt(event.Seq, 10)
		res, err := clusterAppendScript.Run(ctx, c.client, []string{streamKey}, tail.StreamID, seq, payload).Int()
		if err != nil {
			return fmt.Errorf("audit chain: script run: %w", err)
		}
		if res == 1 {
			if err := clusterHeadScript.Run(ctx, c.client, []string{headKey}, seq, seq+":"+event.EventHash).Err(); err != nil {
				// The event is committed; the next append moves the hint.
				slog.Warn("audit chain: head hint not updated", "tenant", event.TenantID, "seq", event.Seq, "error", err)
			}
			return nil
		}
		event.Seq, event.PrevHash, event.EventHash, event.HMAC = 0, "", "", ""
		if err := sleepCASBackoff(ctx, attempt); err != nil {
			return err
		}
	}
	return ErrCASExhausted
}

// readClusterTail returns the head hint's seq and the stream's last entry
// (zero for an empty stream), read in one round trip: the keys live on
// different nodes.
func (c *Chainer) readClusterTail(ctx context.Context, headKey, streamKey string) (int64, retentionAnchor, error) {
	pipe := c.client.Pipeline()
	headCmd := pipe.Get(ctx, headKey)
	tailCmd := pipe.XRevRangeN(ctx, streamKey, "+", "-", 1)
	_, _ = pipe.Exec(ctx) // per-command errors are checked below
	rawHead, err := headCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, retentionAnchor{}, fmt.Errorf("audit chain: read head: %w", err)
	}
	headSeq, _, err := parseChainHead(rawHead)
	if err != nil {
		return 0, retentionAnchor{}, fmt.Errorf("audit chain: parse head: %w", err)
	}
	entries, err := tailCmd.Result()
	if err != nil {
		return 0, retentionAnchor{}, fmt.Errorf("audit chain: read tail: %w", err)
	}
	if len(entries) == 0 {
		return headSeq, retentionAnchor{}, nil
	}
	tail, err := anchorForEntry(entries[0])
	if err != nil {
		return 0, retentionAnchor{}, fmt.Errorf("audit chain: read tail: %w", err)
	}
	return headSeq, tail, nil
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
