package audit

import (
	"context"
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

// retentionTrimScript atomically deletes up to ARGV[3] chain entries older
// than the cutoff and records the newest deleted entry as the tenant's
// retention anchor, so verify can still link the oldest retained event and
// tell retention apart from deletion. It never deletes the newest entry (the
// chain head), which keeps chainAppendScript's genesis guard (XLEN == 0)
// meaningful. A malformed anchor candidate aborts the batch: evidence of
// tampering is kept, not trimmed.
//
// KEYS[1] = chain stream   ARGV[1] = newest stream ID older than the cutoff
// KEYS[2] = anchor key     ARGV[2] = cutoff as an XTRIM MINID
//
//	ARGV[3] = max entries to delete
//
// Returns the number of entries deleted; fewer than ARGV[3] means done.
var retentionTrimScript = redis.NewScript(`
local batch = tonumber(ARGV[3])
local old = redis.call('XRANGE', KEYS[1], '-', ARGV[1], 'COUNT', batch + 1)
local n = math.min(#old, batch)
if n == 0 then return 0 end
local head = redis.call('XREVRANGE', KEYS[1], '+', '-', 'COUNT', 1)
if old[n][1] == head[1][1] then n = n - 1 end
if n == 0 then return 0 end
local seq, payload
local fields = old[n][2]
for i = 1, #fields, 2 do
  if fields[i] == 'seq' then seq = fields[i + 1] elseif fields[i] == 'event' then payload = fields[i + 1] end
end
local ok, ev = pcall(cjson.decode, payload or '')
local hash = ok and type(ev) == 'table' and ev['event_hash'] or nil
if type(seq) ~= 'string' or not string.match(seq, '^%d+$') or type(hash) ~= 'string' or #hash ~= 64 or not string.match(hash, '^%x+$') then
  return redis.error_reply('malformed chain entry ' .. old[n][1])
end
redis.call('SET', KEYS[2], seq .. ':' .. hash)
local minid = ARGV[2]
if #old > n then minid = old[n + 1][1] end
return redis.call('XTRIM', KEYS[1], 'MINID', minid)
`)

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
	keys := []string{s.chainer.StreamKey(tenant), s.chainer.AnchorKey(tenant)}
	newestOld := strconv.FormatInt(cutoffMs-1, 10) + "-18446744073709551615"
	minID := strconv.FormatInt(cutoffMs, 10) + "-0"
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		held, err := s.holds.IsUnderHold(ctx, tenant)
		if err != nil {
			auditRetentionSkipped.WithLabelValues("hold_check_error").Inc()
			return total, fmt.Errorf("legal hold check: %w", err)
		}
		if held {
			auditRetentionSkipped.WithLabelValues("legal_hold").Inc()
			slog.Info("audit retention: tenant under legal hold, not trimmed", "tenant", tenant)
			return total, nil
		}
		n, err := retentionTrimScript.Run(ctx, s.client, keys, newestOld, minID, retentionTrimBatch).Int64()
		if err != nil {
			auditRetentionSkipped.WithLabelValues("trim_error").Inc()
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

// tenants lists every tenant with a chain head.
func (s *RetentionSweeper) tenants(ctx context.Context) ([]string, error) {
	prefix := s.chainer.HeadKey("")
	var tenants []string
	iter := s.client.Scan(ctx, 0, prefix+"*", 100).Iterator()
	for iter.Next(ctx) {
		if tenant := strings.TrimPrefix(iter.Val(), prefix); tenant != "" {
			tenants = append(tenants, tenant)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("audit retention: scan chain heads: %w", err)
	}
	return tenants, nil
}
