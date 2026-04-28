package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

const (
	// envAuditChainRetentionHours configures how far back in time the
	// verify endpoint treats an absent seq as "trimmed by retention" vs
	// "tampered / dropped". Default: 168h (7 days). Exposed via
	// CORDUM_AUDIT_RETENTION_HOURS so operators can tighten/loosen it
	// without a code change.
	envAuditChainRetentionHours = "CORDUM_AUDIT_RETENTION_HOURS"
	defaultAuditChainRetention  = 168 * time.Hour

	// maxVerifySinceUntilSpread caps the range a single call can cover.
	// Keeps the verify endpoint from scanning months of stream data in
	// one go — callers paginate by since/until if they need more.
	maxVerifySinceUntilSpread = 30 * 24 * time.Hour
)

var auditVerifyChainFn = audit.VerifyChain

var auditVerifyDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "cordum",
	Subsystem: "audit_verify",
	Name:      "duration_seconds",
	Help:      "Seconds spent walking the audit hash chain for /api/v1/audit/verify leader calls.",
	Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
}, []string{"status"})

var auditVerifyEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "cordum",
	Subsystem: "audit_verify",
	Name:      "events_total",
	Help:      "Number of audit-chain events scanned by /api/v1/audit/verify leader calls.",
}, []string{"status"})

var auditVerifyInflight = promauto.NewGauge(prometheus.GaugeOpts{
	Namespace: "cordum",
	Subsystem: "audit_verify",
	Name:      "inflight",
	Help:      "Number of active audit-chain verify walks.",
})

var auditVerifyCoalescedTotal = promauto.NewCounter(prometheus.CounterOpts{
	Namespace: "cordum",
	Subsystem: "audit_verify",
	Name:      "coalesced_total",
	Help:      "Number of /api/v1/audit/verify callers served by another in-flight identical verify walk.",
})

// handleAuditVerify implements GET /api/v1/audit/verify.
//
// Query parameters:
//
//	tenant  (required) — tenant to verify, must match caller's scope
//	since   (optional) — unix ms (inclusive lower bound on stream IDs)
//	until   (optional) — unix ms (inclusive upper bound on stream IDs)
//	limit   (optional) — max events to read (default 10000, max 100000)
//
// Response: audit.VerifyResult (status + gap summary). NEVER includes raw
// event bodies — the endpoint is admin-only and for integrity reporting,
// not event retrieval.
//
// Retention boundary: the lowest seq currently present in the stream.
// Gaps strictly below it are classified retention_trimmed; at or above
// are reported as missing (suspected tampering). This is the control
// that distinguishes routine log-expiry from real integrity failures.
func (s *server) handleAuditVerify(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermAuditVerify, []string{"admin"}, client) {
		return
	}

	tenant, err := s.resolveTenant(r, strings.TrimSpace(r.URL.Query().Get("tenant")))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	opts, httpErr := parseVerifyQuery(r)
	if httpErr != nil {
		writeErrorJSON(w, httpErr.status, httpErr.message)
		return
	}

	chainer := audit.NewChainer(client, "")
	streamKey := chainer.StreamKey(tenant)

	boundary, err := readRetentionBoundary(r.Context(), client, streamKey)
	if err != nil {
		writeInternalError(w, r, "audit verify: read boundary", err)
		return
	}
	// Fail loud when the chainer isn't installed AND no events have
	// ever been chained for this tenant. Without this guard the
	// endpoint happily returns status=ok, total_events=0 on a
	// misconfigured deploy — a false-green that would sail through a
	// compliance review. Only fail when both conditions hold so a
	// correctly-configured tenant with genuinely no activity still
	// sees status=ok.
	if s.auditChainer == nil && boundary == 0 {
		writeErrorJSON(w, http.StatusServiceUnavailable,
			"audit chainer not installed; verify cannot attest integrity. "+
				"Check gateway boot logs for 'audit chain enabled' and confirm CORDUM_AUDIT_CHAIN_FAIL is set.")
		return
	}
	opts.RetentionBoundarySeq = boundary

	// Wire the HMAC key from the server's chainer (sourced from
	// CORDUM_AUDIT_HMAC_KEY at boot) so the verify endpoint checks
	// HMAC tags when HMAC is enabled. The key is NOT accepted as a
	// query parameter — URLs are routinely logged and cached, making
	// them unsafe for secret material.
	if s.auditChainer != nil && s.auditChainer.HMACEnabled() {
		opts.HMACKey = s.auditChainer.HMACKeyForVerify()
	}

	key := auditVerifySingleflightKey(tenant, opts)
	leader := false
	resultCh := s.auditVerifyCoalesce.DoChan(key, func() (any, error) {
		leader = true
		auditVerifyInflight.Inc()
		start := time.Now()
		status := "error"
		defer func() {
			auditVerifyDuration.WithLabelValues(status).Observe(time.Since(start).Seconds())
			auditVerifyInflight.Dec()
		}()

		verifyCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		if s.shutdownCh != nil {
			go func() {
				select {
				case <-s.shutdownCh:
					cancel()
				case <-verifyCtx.Done():
				}
			}()
		}
		defer cancel()

		result, err := auditVerifyChainFn(verifyCtx, client, streamKey, opts)
		if err != nil {
			return nil, err
		}

		// Fail-closed safety net: if the scanned range contains HMAC-tagged
		// events but this process has no HMAC key configured, the HMAC
		// verification branch was skipped entirely. Surface this as a
		// degraded result so operators don't see a false-green. Uses
		// result.HMACSeen (set by the verification loop) instead of a
		// separate Redis probe, so it catches all mixed-rollout scenarios.
		if len(opts.HMACKey) == 0 && result.HMACSeen {
			slog.Warn("audit verify: chain contains HMAC-tagged events but CORDUM_AUDIT_HMAC_KEY is not configured — HMAC verification skipped",
				"tenant", tenant,
				"total_events", result.TotalEvents,
			)
			// Don't override a compromised status, but downgrade ok → partial
			// so the caller knows verification was incomplete.
			if result.Status == audit.VerifyStatusOK {
				result.Status = audit.VerifyStatusPartial
			}
		}

		// Surface the operator-configured retention window so the caller
		// can decide whether the oldest present event's timestamp is
		// within policy — the boundary seq alone is not enough context.
		result.RetentionWindowHours = auditChainRetention().Hours()
		status = string(result.Status)
		auditVerifyEventsTotal.WithLabelValues(status).Add(float64(result.TotalEvents))
		return result, nil
	})

	var sfResult singleflight.Result
	select {
	case sfResult = <-resultCh:
	case <-r.Context().Done():
		return
	}
	value, err, shared := sfResult.Val, sfResult.Err, sfResult.Shared
	if shared && !leader {
		auditVerifyCoalescedTotal.Inc()
	}
	if err != nil {
		writeInternalError(w, r, "audit verify: walk chain", err)
		return
	}

	result, ok := value.(*audit.VerifyResult)
	if !ok || result == nil {
		writeInternalError(w, r, "audit verify: walk chain", fmt.Errorf("singleflight returned %T", value))
		return
	}
	writeJSON(w, result)
}

func auditVerifySingleflightKey(tenant string, opts audit.VerifyOptions) string {
	limit := opts.Limit
	if limit <= 0 {
		limit = audit.DefaultVerifyLimit
	}
	if limit > audit.MaxVerifyLimit {
		limit = audit.MaxVerifyLimit
	}
	hmacFingerprint := "none"
	if len(opts.HMACKey) > 0 {
		sum := sha256.Sum256(opts.HMACKey)
		hmacFingerprint = hex.EncodeToString(sum[:])
	}
	return tenant +
		"|" + strconv.FormatInt(opts.SinceMs, 10) +
		"|" + strconv.FormatInt(opts.UntilMs, 10) +
		"|" + strconv.FormatInt(limit, 10) +
		"|" + strconv.FormatInt(opts.RetentionBoundarySeq, 10) +
		"|" + hmacFingerprint
}

// verifyHTTPError pairs a status code with a message so parseVerifyQuery
// can return one value on error.
type verifyHTTPError struct {
	status  int
	message string
}

// parseVerifyQuery decodes and validates ?since=, ?until=, ?limit=.
func parseVerifyQuery(r *http.Request) (audit.VerifyOptions, *verifyHTTPError) {
	q := r.URL.Query()
	opts := audit.VerifyOptions{}

	if raw := strings.TrimSpace(q.Get("since")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return opts, &verifyHTTPError{http.StatusBadRequest, "since must be a non-negative unix millisecond"}
		}
		opts.SinceMs = v
	}
	if raw := strings.TrimSpace(q.Get("until")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			return opts, &verifyHTTPError{http.StatusBadRequest, "until must be a non-negative unix millisecond"}
		}
		opts.UntilMs = v
	}
	if opts.SinceMs > 0 && opts.UntilMs > 0 {
		if opts.UntilMs < opts.SinceMs {
			return opts, &verifyHTTPError{http.StatusBadRequest, "until must be >= since"}
		}
		if opts.UntilMs-opts.SinceMs > maxVerifySinceUntilSpread.Milliseconds() {
			return opts, &verifyHTTPError{http.StatusBadRequest, "since/until range exceeds 30 days"}
		}
	}
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			return opts, &verifyHTTPError{http.StatusBadRequest, "limit must be a positive integer"}
		}
		if v > audit.MaxVerifyLimit {
			return opts, &verifyHTTPError{http.StatusBadRequest, "limit exceeds maximum"}
		}
		opts.Limit = v
	}
	return opts, nil
}

// readRetentionBoundary returns the lowest seq currently present in the
// tenant's stream. Callers pass the shared Redis client directly; the
// helper does nothing smarter than XRANGE stream - + COUNT 1.
func readRetentionBoundary(ctx context.Context, client redis.UniversalClient, streamKey string) (int64, error) {
	entries, err := client.XRangeN(ctx, streamKey, "-", "+", 1).Result()
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	raw, ok := entries[0].Values["seq"].(string)
	if !ok {
		return 0, nil // stream has non-chain entries; report no boundary
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// auditChainRetentionParseWarn guards the once-per-process WARN log so
// a misconfigured CORDUM_AUDIT_RETENTION_HOURS doesn't flood the
// handler log on every /api/v1/audit/verify request.
var auditChainRetentionParseWarn sync.Once

// auditChainRetention reads CORDUM_AUDIT_RETENTION_HOURS and returns the
// duration. Invalid values fall back to the 168h default and emit
// exactly one WARN log per process so operators notice the misconfig
// without drowning in repeated messages.
func auditChainRetention() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envAuditChainRetentionHours))
	if raw == "" {
		return defaultAuditChainRetention
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		auditChainRetentionParseWarn.Do(func() {
			slog.Warn("CORDUM_AUDIT_RETENTION_HOURS unparseable, falling back to default",
				"value", raw,
				"default_hours", defaultAuditChainRetention.Hours(),
			)
		})
		return defaultAuditChainRetention
	}
	return time.Duration(v * float64(time.Hour))
}
