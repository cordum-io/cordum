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
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/infra/env"
	"github.com/cordum/cordum/core/infra/redisutil"
	"github.com/cordum/cordum/core/licensing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
)

const (
	// EnvRetentionHours narrows the plan's audit retention entitlement to a
	// shorter window, in hours. Unset, non-positive or invalid values leave
	// the entitlement window unchanged.
	EnvRetentionHours = "CORDUM_AUDIT_RETENTION_HOURS"
	// EnvRetentionEnforce turns audit chain trimming on or off. Unset means
	// DefaultRetentionEnforce.
	EnvRetentionEnforce = "CORDUM_AUDIT_RETENTION_ENFORCE"

	// DefaultRetentionEnforce applies when CORDUM_AUDIT_RETENTION_ENFORCE is
	// unset. Trimming deletes compliance data that existing deployments have
	// always kept, so it stays opt-in; flip this to enforce by default.
	DefaultRetentionEnforce = false

	// DefaultRetentionSweepInterval is how often the sweeper runs. Only one
	// replica sweeps per interval (see sweepIfLeader).
	DefaultRetentionSweepInterval = time.Hour

	retentionTrimBatch = 1000
	retentionLockKey   = "audit:retention:lock"
	maxRetentionHours  = float64(math.MaxInt64 / int64(time.Hour))
)

var (
	auditRetentionTrimmed = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cordum_audit_retention_trimmed_total",
		Help: "Audit chain events deleted by the retention sweeper.",
	})
	auditRetentionSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "cordum_audit_retention_skipped_total",
		Help: "Tenant chain sweeps the retention sweeper skipped, by reason (legal_hold, hold_check_error, trim_error).",
	}, []string{"reason"})

	errLegalHoldStoreUnavailable = errors.New("audit retention: legal hold store unavailable; nothing trimmed")

	// retentionHoursWarn keeps a bad CORDUM_AUDIT_RETENTION_HOURS from
	// logging on every call.
	retentionHoursWarn sync.Once
)

// RetentionWindow returns the effective audit chain retention window: the
// plan's audit retention entitlement, narrowed by CORDUM_AUDIT_RETENTION_HOURS
// when that is set to a shorter positive number of hours. Zero means
// unlimited: nothing is ever trimmed.
func RetentionWindow(entitlements licensing.Entitlements) time.Duration {
	window := RetentionTTLFromEntitlements(entitlements)
	if narrowed := retentionHoursFromEnv(); narrowed > 0 && (window == 0 || narrowed < window) {
		window = narrowed
	}
	return window
}

func retentionHoursFromEnv() time.Duration {
	raw := strings.TrimSpace(os.Getenv(EnvRetentionHours))
	if raw == "" {
		return 0
	}
	hours, err := strconv.ParseFloat(raw, 64)
	if err != nil || !(hours > 0) || hours > maxRetentionHours {
		retentionHoursWarn.Do(func() {
			slog.Warn("CORDUM_AUDIT_RETENTION_HOURS must be a positive number of hours; using the plan retention entitlement",
				"value", raw)
		})
		return 0
	}
	return time.Duration(hours * float64(time.Hour))
}

// RetentionEnforced reports whether the sweeper may delete audit events:
// CORDUM_AUDIT_RETENTION_ENFORCE when set, otherwise DefaultRetentionEnforce.
func RetentionEnforced() bool {
	if strings.TrimSpace(os.Getenv(EnvRetentionEnforce)) == "" {
		return DefaultRetentionEnforce
	}
	return env.Bool(EnvRetentionEnforce)
}

// retentionAnchor is the value of a tenant's retention anchor key
// (Chainer.AnchorKey): the newest chain event the sweeper deleted, stored as
// "<seq>:<event_hash>:<stream_id>", plus ":<mac>" when the chain has an HMAC
// key. Verify links the oldest retained event to it.
//
// On Redis Cluster the anchor is written before the stream trim it records
// (see trimBatchCluster), as a pending anchor that also carries the anchor
// it replaces, signed together:
//
//	<seq>:<event_hash>:<stream_id>:<prev_seq>:<prev_event_hash>:<prev_stream_id>[:<mac>]
//
// prev_seq 0, with an empty hash and ID, means no earlier trim. Until the
// trim is confirmed, verify links the stream to the previous anchor (linked).
//
// Anchors written before anchors carried a stream ID are "<seq>:<event_hash>"
// and parse as unsigned anchors with an empty StreamID: an HMAC verify reports
// them as hmac_mismatch, and without HMAC they still anchor the chain, but a
// window cannot tell whether it starts after the trim they record (see
// walkChain). The next trim replaces them.
type retentionAnchor struct {
	Seq      int64
	Hash     string
	StreamID string

	Pending      bool
	PrevSeq      int64
	PrevHash     string
	PrevStreamID string

	MAC string
}

// retentionAnchorMACDomain keeps anchor MACs apart from event MACs.
const retentionAnchorMACDomain = "cordum.audit.retention_anchor.v1"

// value is the anchor without its MAC: what the MAC signs.
func (a retentionAnchor) value() string {
	v := strconv.FormatInt(a.Seq, 10) + ":" + a.Hash + ":" + a.StreamID
	if a.Pending {
		v += ":" + strconv.FormatInt(a.PrevSeq, 10) + ":" + a.PrevHash + ":" + a.PrevStreamID
	}
	return v
}

func (a retentionAnchor) String() string {
	if a.MAC != "" {
		return a.value() + ":" + a.MAC
	}
	return a.value()
}

// linked is the anchor the oldest retained event links to: a pending
// anchor's trim is unconfirmed, so the stream still answers to the anchor it
// replaces.
func (a retentionAnchor) linked() retentionAnchor {
	if !a.Pending {
		return a
	}
	return retentionAnchor{Seq: a.PrevSeq, Hash: a.PrevHash, StreamID: a.PrevStreamID}
}

// mac is the HMAC-SHA256 (hex) of the anchor bound to its Redis key, so an
// anchor cannot be forged, or copied from another tenant, without the chain
// HMAC key.
func (a retentionAnchor) mac(anchorKey string, key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(retentionAnchorMACDomain + "\n" + anchorKey + "\n" + a.value()))
	return hex.EncodeToString(m.Sum(nil))
}

// validMAC reports whether the anchor carries the MAC key would give it.
func (a retentionAnchor) validMAC(anchorKey string, key []byte) bool {
	return subtle.ConstantTimeCompare([]byte(a.MAC), []byte(a.mac(anchorKey, key))) == 1
}

// streamMs is the millisecond part of the anchor's stream ID.
func (a retentionAnchor) streamMs() int64 {
	ms, _, _ := strings.Cut(a.StreamID, "-")
	v, _ := strconv.ParseInt(ms, 10, 64)
	return v
}

// parseRetentionAnchor decodes an anchor value; "" (never trimmed) is the
// zero anchor.
func parseRetentionAnchor(raw string) (retentionAnchor, error) {
	if raw == "" {
		return retentionAnchor{}, nil
	}
	malformed := fmt.Errorf("malformed retention anchor %q", raw)
	parts := strings.Split(raw, ":")
	if len(parts) == 2 {
		parts = append(parts, "") // legacy "<seq>:<event_hash>"
	}
	var a retentionAnchor
	if n := len(parts); n == 4 || n == 7 {
		a.MAC, parts = parts[n-1], parts[:n-1]
		if !isHexHash(a.MAC) {
			return retentionAnchor{}, malformed
		}
	}
	if len(parts) != 3 && len(parts) != 6 {
		return retentionAnchor{}, malformed
	}
	pos, ok := parseAnchorPosition(parts[:3])
	if !ok || pos.Seq == 0 || (len(parts) == 6 && pos.StreamID == "") {
		return retentionAnchor{}, malformed
	}
	a.Seq, a.Hash, a.StreamID = pos.Seq, pos.Hash, pos.StreamID
	if len(parts) == 6 {
		prev, ok := parseAnchorPosition(parts[3:])
		if !ok || prev.Seq >= a.Seq {
			return retentionAnchor{}, malformed
		}
		a.Pending, a.PrevSeq, a.PrevHash, a.PrevStreamID = true, prev.Seq, prev.Hash, prev.StreamID
	}
	return a, nil
}

// parseAnchorPosition decodes "<seq>:<event_hash>:<stream_id>". Seq 0 is the
// position before the first event and comes with an empty hash and ID. An
// empty ID with seq > 0 is a legacy anchor's position, or one a pending
// anchor carries over.
func parseAnchorPosition(f []string) (retentionAnchor, bool) {
	seq, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil || seq < 0 {
		return retentionAnchor{}, false
	}
	if seq == 0 {
		return retentionAnchor{}, f[1] == "" && f[2] == ""
	}
	if f[2] == "" {
		return retentionAnchor{Seq: seq, Hash: f[1]}, isHexHash(f[1])
	}
	ms, idSeq, _ := strings.Cut(f[2], "-")
	_, msErr := strconv.ParseUint(ms, 10, 64)
	_, idErr := strconv.ParseUint(idSeq, 10, 64)
	if !isHexHash(f[1]) || msErr != nil || idErr != nil {
		return retentionAnchor{}, false
	}
	return retentionAnchor{Seq: seq, Hash: f[1], StreamID: f[2]}, true
}

func isHexHash(s string) bool {
	if len(s) != chainHashHexLen {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// anchorForEntry builds the anchor for a chain entry about to be trimmed. A
// malformed entry is an error: evidence of tampering is kept, not trimmed.
func anchorForEntry(entry redis.XMessage) (retentionAnchor, error) {
	rawSeq, _ := entry.Values["seq"].(string)
	payload, _ := entry.Values[chainStreamFieldEvent].(string)
	var ev struct {
		EventHash string `json:"event_hash"`
	}
	seq, err := strconv.ParseInt(rawSeq, 10, 64)
	if err != nil || seq <= 0 || json.Unmarshal([]byte(payload), &ev) != nil || !isHexHash(ev.EventHash) {
		return retentionAnchor{}, fmt.Errorf("malformed chain entry %s", entry.ID)
	}
	return retentionAnchor{Seq: seq, Hash: ev.EventHash, StreamID: entry.ID}, nil
}

// retentionTrimScript deletes every chain entry up to and including ARGV[1]
// and records ARGV[2] as the tenant's retention anchor, atomically, so
// verify always sees the anchor that matches the trimmed stream. It deletes
// nothing when ARGV[1] is already gone (another sweep got there first) or is
// the chain head, which is never trimmed so chainAppendScript's genesis
// guard (XLEN == 0) stays meaningful.
//
// KEYS[1] = chain stream   ARGV[1] = stream ID of the newest entry to delete
// KEYS[2] = anchor key     ARGV[2] = anchor value (retentionAnchor.String)
//
// Returns the number of entries deleted.
var retentionTrimScript = redis.NewScript(`
if #redis.call('XRANGE', KEYS[1], ARGV[1], ARGV[1]) == 0 then return 0 end
local nxt = redis.call('XRANGE', KEYS[1], '(' .. ARGV[1], '+', 'COUNT', 1)
if #nxt == 0 then return 0 end
redis.call('SET', KEYS[2], ARGV[2])
return redis.call('XTRIM', KEYS[1], 'MINID', nxt[1][1])
`)

// clusterTrimScript is retentionTrimScript without the anchor write, for
// Redis Cluster, where the anchor key hashes to another slot than the stream:
// the anchor moves in separate steps around it (see trimBatchCluster).
//
// KEYS[1] = chain stream   ARGV[1] = stream ID of the newest entry to delete
var clusterTrimScript = redis.NewScript(`
if #redis.call('XRANGE', KEYS[1], ARGV[1], ARGV[1]) == 0 then return 0 end
local nxt = redis.call('XRANGE', KEYS[1], '(' .. ARGV[1], '+', 'COUNT', 1)
if #nxt == 0 then return 0 end
return redis.call('XTRIM', KEYS[1], 'MINID', nxt[1][1])
`)

// anchorSwapScript replaces a retention anchor (KEYS[1]) with ARGV[2] only
// while it still holds ARGV[1], the value the caller read (empty when none).
var anchorSwapScript = redis.NewScript(`
if (redis.call('GET', KEYS[1]) or '') ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2])
return 1
`)

// errAnchorMoved is a retention anchor that changed between read and write.
var errAnchorMoved = errors.New("retention anchor changed concurrently")

// RetentionSweeper trims every tenant's audit chain stream to the effective
// retention window (RetentionWindow). It deletes nothing unless
// RetentionEnforced, never trims a tenant under an active legal hold, and
// skips a tenant whose hold check fails (fail safe: keep the data).
type RetentionSweeper struct {
	client   redis.UniversalClient
	chainer  *Chainer
	holds    *LegalHoldStore
	resolver *licensing.EntitlementResolver
	interval time.Duration
}

// NewRetentionSweeper wires a sweeper. A nil holds store trims nothing; a
// nil resolver means Community entitlements.
func NewRetentionSweeper(client redis.UniversalClient, chainer *Chainer, holds *LegalHoldStore, resolver *licensing.EntitlementResolver) *RetentionSweeper {
	return &RetentionSweeper{
		client:   client,
		chainer:  chainer,
		holds:    holds,
		resolver: resolver,
		interval: DefaultRetentionSweepInterval,
	}
}

// Run sweeps at startup and then every interval until ctx is cancelled. When
// retention is not enforced it logs that once and returns.
func (s *RetentionSweeper) Run(ctx context.Context) {
	if s == nil || s.client == nil || s.chainer == nil {
		return
	}
	window := RetentionWindow(currentEntitlements(s.resolver))
	if !RetentionEnforced() {
		slog.Warn("audit retention not enforced: audit chain streams are never trimmed",
			"enable_with", EnvRetentionEnforce+"=true",
			"window_hours", window.Hours(),
			"unlimited", window == 0,
		)
		return
	}
	slog.Info("audit retention enforced",
		"window_hours", window.Hours(),
		"unlimited", window == 0,
		"interval", s.interval.String(),
	)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		s.sweepIfLeader(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweepIfLeader runs one sweep, bounded by the interval, when this replica
// wins the cycle lock. The lock is never released: it expires with the sweep
// deadline, so replicas do not sweep concurrently and a crashed leader is
// replaced next cycle. Overlapping sweeps would still be safe (every trim
// batch is atomic); the lock only avoids duplicate work.
func (s *RetentionSweeper) sweepIfLeader(ctx context.Context) {
	won, err := s.client.SetNX(ctx, retentionLockKey, "1", s.interval).Result()
	if err != nil || !won {
		if err != nil && ctx.Err() == nil {
			slog.Warn("audit retention: acquire sweep lock failed", "error", err)
		}
		return
	}
	sweepCtx, cancel := context.WithTimeout(ctx, s.interval)
	defer cancel()
	if _, err := s.SweepOnce(sweepCtx); err != nil && ctx.Err() == nil {
		slog.Warn("audit retention sweep incomplete", "error", err)
	}
}

// SweepOnce trims every tenant's chain once and returns how many events it
// deleted. It is a no-op when retention is not enforced or unlimited. A
// tenant whose hold check or trim fails is logged, counted and skipped.
func (s *RetentionSweeper) SweepOnce(ctx context.Context) (int64, error) {
	if !RetentionEnforced() {
		return 0, nil
	}
	window := RetentionWindow(currentEntitlements(s.resolver))
	if window <= 0 {
		return 0, nil
	}
	if s.holds == nil {
		return 0, errLegalHoldStoreUnavailable
	}
	// Cut off by the Redis clock: it stamped the stream IDs, and every
	// replica shares it.
	now, err := s.client.Time(ctx).Result()
	if err != nil {
		return 0, fmt.Errorf("audit retention: redis time: %w", err)
	}
	cutoffMs := now.Add(-window).UnixMilli()
	if cutoffMs <= 0 {
		return 0, nil
	}
	tenants, err := s.tenants(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, tenant := range tenants {
		n, err := s.sweepTenant(ctx, tenant, cutoffMs)
		total += n
		if err != nil {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			slog.Warn("audit retention: tenant skipped", "tenant", tenant, "error", err)
		}
	}
	return total, nil
}

// sweepTenant trims one tenant's chain in batches, re-checking the legal
// hold before every batch so a hold placed mid-sweep stops deletion.
func (s *RetentionSweeper) sweepTenant(ctx context.Context, tenant string, cutoffMs int64) (int64, error) {
	newestOld := strconv.FormatInt(cutoffMs-1, 10) + "-18446744073709551615"
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		if blocked, err := s.holdBlocks(ctx, tenant); blocked {
			return total, err
		}
		n, err := s.trimBatch(ctx, tenant, newestOld)
		if err != nil {
			if !errors.As(err, new(holdCheckError)) {
				auditRetentionSkipped.WithLabelValues("trim_error").Inc()
			}
			return total, fmt.Errorf("trim: %w", err)
		}
		total += n
		auditRetentionTrimmed.Add(float64(n))
		if n < retentionTrimBatch {
			if total > 0 {
				slog.Info("audit retention trimmed tenant chain", "tenant", tenant, "events", total)
			}
			return total, nil
		}
	}
}

// holdBlocks reports whether tenant must not be trimmed now: it is under an
// active legal hold, or the hold check failed (fail safe: keep the data, and
// return the failure).
func (s *RetentionSweeper) holdBlocks(ctx context.Context, tenant string) (bool, error) {
	held, err := s.holds.IsUnderHold(ctx, tenant)
	if err != nil {
		auditRetentionSkipped.WithLabelValues("hold_check_error").Inc()
		return true, holdCheckError{err}
	}
	if held {
		auditRetentionSkipped.WithLabelValues("legal_hold").Inc()
		slog.Info("audit retention: tenant under legal hold, not trimmed", "tenant", tenant)
	}
	return held, nil
}

// holdCheckError is a failed legal hold check, counted as hold_check_error
// rather than trim_error.
type holdCheckError struct{ err error }

func (e holdCheckError) Error() string { return "legal hold check: " + e.err.Error() }
func (e holdCheckError) Unwrap() error { return e.err }

// trimBatch deletes up to retentionTrimBatch of the tenant's oldest entries
// with stream IDs up to newestOld, never the chain head, and moves the
// anchor to the newest one deleted (signed when the chain has an HMAC key).
func (s *RetentionSweeper) trimBatch(ctx context.Context, tenant, newestOld string) (int64, error) {
	if redisutil.IsCluster(s.client) {
		return s.trimBatchCluster(ctx, tenant, newestOld)
	}
	streamKey := s.chainer.StreamKey(tenant)
	pipe := s.client.Pipeline()
	oldCmd := pipe.XRangeN(ctx, streamKey, "-", newestOld, retentionTrimBatch)
	headCmd := pipe.XRevRangeN(ctx, streamKey, "+", "-", 1)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	old := oldCmd.Val()
	if head := headCmd.Val(); len(old) > 0 && len(head) > 0 && old[len(old)-1].ID == head[0].ID {
		old = old[:len(old)-1]
	}
	if len(old) == 0 {
		return 0, nil
	}
	newest := old[len(old)-1]
	anchor, err := anchorForEntry(newest)
	if err != nil {
		return 0, err
	}
	anchorKey := s.chainer.AnchorKey(tenant)
	if len(s.chainer.hmacKey) > 0 {
		anchor.MAC = anchor.mac(anchorKey, s.chainer.hmacKey)
	}
	return retentionTrimScript.Run(ctx, s.client, []string{streamKey, anchorKey}, newest.ID, anchor.String()).Int64()
}

// trimBatchCluster is trimBatch on Redis Cluster, where the anchor key and
// the stream hash to different slots and no script can update both. The
// anchor moves in three single-key steps around the trim:
//
//  1. write a pending anchor: the newest entry to delete plus the anchor it
//     replaces, signed together;
//  2. re-check the legal hold, then trim the stream (clusterTrimScript);
//  3. confirm: rewrite the anchor without the one it replaced.
//
// Until step 3, verify links the stream to the replaced anchor, so the events
// a crash or a new legal hold leaves in place still verify end to end, and
// deleting any of them is reported. A trim whose confirmation is lost reads
// as missing events until the next sweep confirms it: an unconfirmed trim
// looks the same as someone else deleting those events. A sweep finishes a
// pending trim before starting a new one.
func (s *RetentionSweeper) trimBatchCluster(ctx context.Context, tenant, newestOld string) (int64, error) {
	streamKey, anchorKey := s.chainer.StreamKey(tenant), s.chainer.AnchorKey(tenant)
	pipe := s.client.Pipeline()
	anchorCmd := pipe.Get(ctx, anchorKey)
	oldestCmd := pipe.XRangeN(ctx, streamKey, "-", "+", 1)
	oldCmd := pipe.XRangeN(ctx, streamKey, "-", newestOld, retentionTrimBatch)
	headCmd := pipe.XRevRangeN(ctx, streamKey, "+", "-", 1)
	_, _ = pipe.Exec(ctx) // per-command errors are checked below
	raw, err := anchorCmd.Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	for _, cmd := range []*redis.XMessageSliceCmd{oldestCmd, oldCmd, headCmd} {
		if err := cmd.Err(); err != nil {
			return 0, err
		}
	}
	cur, err := parseRetentionAnchor(raw)
	if err != nil {
		return 0, err
	}
	// Never finish, or sign over, an anchor the chain's HMAC key did not
	// sign: confirming a forged pending anchor would sign the deletion it
	// names. The tenant is skipped, its data kept, and verify reports the
	// anchor as hmac_mismatch.
	if raw != "" && len(s.chainer.hmacKey) > 0 && !cur.validMAC(anchorKey, s.chainer.hmacKey) {
		return 0, errors.New("retention anchor MAC does not verify; not trimming over it")
	}
	if cur.Pending {
		return s.finishPendingTrim(ctx, tenant, raw, cur, oldestCmd.Val())
	}
	old := oldCmd.Val()
	if head := headCmd.Val(); len(old) > 0 && len(head) > 0 && old[len(old)-1].ID == head[0].ID {
		old = old[:len(old)-1]
	}
	if len(old) == 0 {
		return 0, nil
	}
	pending, err := anchorForEntry(old[len(old)-1])
	if err != nil {
		return 0, err
	}
	if pending.Seq <= cur.Seq {
		return 0, fmt.Errorf("retention anchor seq %d is not behind the stream", cur.Seq)
	}
	pending.Pending, pending.PrevSeq, pending.PrevHash, pending.PrevStreamID = true, cur.Seq, cur.Hash, cur.StreamID
	pendingRaw := s.signAnchor(anchorKey, pending)
	if err := s.swapAnchor(ctx, anchorKey, raw, pendingRaw); err != nil {
		return 0, err
	}
	return s.trimToPending(ctx, tenant, pendingRaw, pending)
}

// finishPendingTrim completes a cluster trim that a crash or a legal hold
// interrupted after its pending anchor was written.
func (s *RetentionSweeper) finishPendingTrim(ctx context.Context, tenant, pendingRaw string, pending retentionAnchor, oldest []redis.XMessage) (int64, error) {
	if len(oldest) == 0 {
		// The sweeper never trims the last entry: the stream was deleted.
		return 0, errors.New("chain stream is empty under a pending retention anchor")
	}
	first, err := anchorForEntry(oldest[0])
	if err != nil {
		return 0, err
	}
	if first.Seq > pending.Seq {
		// Trimmed, then the confirmation was lost. The tenant is not under
		// hold now, and those events were past retention when the anchor
		// was written.
		return 0, s.confirmAnchor(ctx, tenant, pendingRaw, pending)
	}
	return s.trimToPending(ctx, tenant, pendingRaw, pending)
}

// trimToPending re-checks the legal hold, trims the stream through the
// pending anchor and confirms the anchor.
func (s *RetentionSweeper) trimToPending(ctx context.Context, tenant, pendingRaw string, pending retentionAnchor) (int64, error) {
	if blocked, err := s.holdBlocks(ctx, tenant); blocked {
		return 0, err
	}
	n, err := clusterTrimScript.Run(ctx, s.client, []string{s.chainer.StreamKey(tenant)}, pending.StreamID).Int64()
	if err != nil || n == 0 {
		return n, err
	}
	return n, s.confirmAnchor(ctx, tenant, pendingRaw, pending)
}

func (s *RetentionSweeper) confirmAnchor(ctx context.Context, tenant, pendingRaw string, pending retentionAnchor) error {
	anchorKey := s.chainer.AnchorKey(tenant)
	done := retentionAnchor{Seq: pending.Seq, Hash: pending.Hash, StreamID: pending.StreamID}
	return s.swapAnchor(ctx, anchorKey, pendingRaw, s.signAnchor(anchorKey, done))
}

// swapAnchor replaces the anchor with next only while it still holds want.
func (s *RetentionSweeper) swapAnchor(ctx context.Context, anchorKey, want, next string) error {
	swapped, err := anchorSwapScript.Run(ctx, s.client, []string{anchorKey}, want, next).Int()
	if err != nil {
		return err
	}
	if swapped != 1 {
		return errAnchorMoved
	}
	return nil
}

// signAnchor renders the anchor, signed when the chain has an HMAC key.
func (s *RetentionSweeper) signAnchor(anchorKey string, a retentionAnchor) string {
	a.MAC = ""
	if len(s.chainer.hmacKey) > 0 {
		a.MAC = a.mac(anchorKey, s.chainer.hmacKey)
	}
	return a.String()
}

// tenants lists every tenant with a chain head.
func (s *RetentionSweeper) tenants(ctx context.Context) ([]string, error) {
	prefix := s.chainer.HeadKey("")
	var tenants []string
	err := redisutil.ScanEach(ctx, s.client, prefix+"*", 100, func(keys []string) error {
		for _, key := range keys {
			if tenant := strings.TrimPrefix(key, prefix); tenant != "" {
				tenants = append(tenants, tenant)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("audit retention: scan chain heads: %w", err)
	}
	return tenants, nil
}
