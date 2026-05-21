package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/redis/go-redis/v9"
)

// handlers_shadow_results.go — REST projections over the shadow_eval
// audit stream.
//
// Three endpoints under /api/v1/policy/bundles/{id}/shadow/results/:
//
//   - /summary       — counts aggregated over a time range (60 s cache)
//   - /comparisons   — individual decisions paginated via stream cursor
//   - /timeseries    — zero-filled bucket counts for chart rendering
//
// All three share the scanShadowEvents helper below so the filter logic
// (EventType == shadow_eval && Extra[bundle_id] == target) is defined
// in exactly one place. Tenant scoping is enforced by keying on
// audit.NewChainer(...).StreamKey(tenant) — the chainer's per-tenant
// stream is already the invariant; callers cannot read events for a
// tenant they do not have access to.

// shadowResultsStreamField is the Redis Stream field holding the
// canonical SIEMEvent JSON payload. Mirrors the unexported constant in
// core/audit/chain.go; we use the literal here rather than exporting
// it from the audit package — the schema is stable, duplicating one
// constant is less coupling than widening the audit API surface.
const shadowResultsStreamField = "event"

// scanShadowEvents walks the given tenant's audit stream between the
// `since` and `until` unix-ms bounds and returns every shadow_eval
// event whose Extra[bundle_id] matches bundleID.
//
// Returns:
//   - events: in stream-arrival order (oldest first)
//   - truncatedAtMax: true when the scan terminated because it hit the
//     caller's `limit` before reaching `until`. Callers expose this on
//     the response so UIs can surface "older data may be missing".
//   - err: any Redis failure; the caller maps to 500.
//
// The limit is clamped to [1, audit.MaxVerifyLimit]. Passing limit=0
// yields audit.DefaultVerifyLimit so handlers can accept "no limit
// specified" without a special-case in each one.
func scanShadowEvents(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	bundleID string,
	since, until int64,
	limit int64,
) ([]audit.SIEMEvent, bool, error) {
	events, _, truncated, err := scanShadowEventsPaged(ctx, client, streamKey, bundleID, since, until, "", limit)
	return events, truncated, err
}

// scanShadowEventsPaged is the cursor-aware variant used by the
// comparisons handler. startCursor is an exclusive Redis Stream ID
// (e.g. "1700000000000-3"); empty falls back to the `since` lower
// bound. Returns (events, lastStreamID, truncatedAtMax, err) — the
// lastStreamID is empty when the caller is at the end of the range.
func scanShadowEventsPaged(
	ctx context.Context,
	client redis.UniversalClient,
	streamKey string,
	bundleID string,
	since, until int64,
	startCursor string,
	limit int64,
) ([]audit.SIEMEvent, string, bool, error) {
	if client == nil {
		return nil, "", false, fmt.Errorf("shadow results: nil redis client")
	}
	if limit <= 0 {
		limit = audit.DefaultVerifyLimit
	}
	if limit > audit.MaxVerifyLimit {
		limit = audit.MaxVerifyLimit
	}

	// Stream IDs are "<ms>-<seq>"; Redis accepts a bare "<ms>" as the
	// start of that millisecond and "<ms>-18446744073709551615" (or
	// "<ms>+1 ms") as the end. We pass "-" / "+" sentinels when the
	// caller omits since/until so the whole stream is walked.
	startID := "-"
	endID := "+"
	if since > 0 {
		startID = strconv.FormatInt(since, 10) + "-0"
	}
	if until > 0 {
		// Use the maximum seq within the millisecond so inclusive-upper
		// semantics match XRANGE's `end` interpretation.
		endID = strconv.FormatInt(until, 10) + "-18446744073709551615"
	}
	// Cursor wins over `since` — a paginated follow-up call uses the
	// prior page's lastStreamID exclusively, so `(<id>` skips past any
	// duplicate from the prior response.
	if startCursor != "" {
		startID = "(" + startCursor
	}

	// We paginate XRANGE by tracking the last-seen stream ID and
	// advancing with exclusive start markers ("(<id>") so a single huge
	// COUNT call doesn't OOM the Redis response buffer. Chunk size is
	// modest (1000) — tuning further is premature; measure first.
	const chunkSize = int64(1000)
	var (
		out       []audit.SIEMEvent
		cursor    = startID
		remaining = limit
		truncated = false
		lastID    string
	)
	for remaining > 0 {
		pull := chunkSize
		if pull > remaining {
			pull = remaining
		}
		entries, err := client.XRangeN(ctx, streamKey, cursor, endID, pull).Result()
		if err != nil {
			return nil, "", false, fmt.Errorf("shadow results: xrange %s: %w", streamKey, err)
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			raw, ok := entry.Values[shadowResultsStreamField].(string)
			if !ok {
				continue
			}
			var ev audit.SIEMEvent
			if err := json.Unmarshal([]byte(raw), &ev); err != nil {
				// Malformed entry — skip rather than abort. A single
				// garbled row shouldn't poison the whole projection.
				continue
			}
			if ev.EventType != audit.EventShadowEval {
				continue
			}
			if gotBundle := ev.Extra["bundle_id"]; gotBundle != bundleID {
				continue
			}
			out = append(out, ev)
			lastID = entry.ID
			if int64(len(out)) >= limit {
				truncated = true
				return out, lastID, truncated, nil
			}
		}
		// Advance cursor past the last entry we read.
		cursor = "(" + entries[len(entries)-1].ID
		remaining -= int64(len(entries))
	}
	return out, lastID, truncated, nil
}

// ---------------------------------------------------------------------------
// Summary endpoint
// ---------------------------------------------------------------------------

// ShadowResultsSummary is the JSON shape returned by
// GET /api/v1/policy/bundles/{id}/shadow/results/summary. Kept public
// so the SDK + OpenAPI spec can pin the exact field names.
type ShadowResultsSummary struct {
	BundleID            string `json:"bundle_id"`
	ShadowBundleID      string `json:"shadow_bundle_id,omitempty"`
	FromMs              int64  `json:"from_ms"`
	ToMs                int64  `json:"to_ms"`
	TotalEvaluated      int64  `json:"total_evaluated"`
	EscalatedCount      int64  `json:"escalated_count"`
	RelaxedCount        int64  `json:"relaxed_count"`
	ApprovalDifferCount int64  `json:"approval_differ_count"`
	UnchangedCount      int64  `json:"unchanged_count"`
	TruncatedAtMax      bool   `json:"truncated_at_max"`
}

// shadowSummaryCacheEntry is the micro-cache value. 60s TTL chosen so
// a 5s-polling dashboard pays the stream scan once per minute instead
// of 12× — reduces Redis load on busy tenants without noticeably
// affecting perceived freshness (shadow_eval counts rarely surprise
// operators at 60s resolution).
type shadowSummaryCacheEntry struct {
	summary   ShadowResultsSummary
	expiresAt time.Time
}

var (
	shadowSummaryCacheMu sync.RWMutex
	shadowSummaryCache   = map[string]shadowSummaryCacheEntry{}
)

const shadowSummaryCacheTTL = 60 * time.Second

// shadowSummaryCacheKey composes the cache lookup key from the scope
// dimensions that uniquely identify a (tenant, bundle, window) scan.
// Any change in any of them warrants a fresh scan — don't collapse.
func shadowSummaryCacheKey(tenant, bundleID string, from, to int64) string {
	return tenant + "|" + bundleID + "|" + strconv.FormatInt(from, 10) + "|" + strconv.FormatInt(to, 10)
}

// lookupShadowSummaryCache returns a cached summary if fresh, else
// (zero-value, false). The lookup + store use a single RWMutex; for
// heavily concurrent callers the write-serialising behaviour here is
// acceptable — the critical section is trivial (map probe).
func lookupShadowSummaryCache(key string, now time.Time) (ShadowResultsSummary, bool) {
	shadowSummaryCacheMu.RLock()
	entry, ok := shadowSummaryCache[key]
	shadowSummaryCacheMu.RUnlock()
	if !ok || now.After(entry.expiresAt) {
		return ShadowResultsSummary{}, false
	}
	return entry.summary, true
}

// storeShadowSummaryCache writes into the cache with the configured TTL.
// Stale entries are evicted opportunistically to bound memory — anything
// older than 5 minutes (5× the TTL) is removed on every write.
func storeShadowSummaryCache(key string, s ShadowResultsSummary, now time.Time) {
	shadowSummaryCacheMu.Lock()
	defer shadowSummaryCacheMu.Unlock()
	shadowSummaryCache[key] = shadowSummaryCacheEntry{
		summary:   s,
		expiresAt: now.Add(shadowSummaryCacheTTL),
	}
	evictBefore := now.Add(-5 * shadowSummaryCacheTTL)
	for k, e := range shadowSummaryCache {
		if e.expiresAt.Before(evictBefore) {
			delete(shadowSummaryCache, k)
		}
	}
}

// resetShadowSummaryCache clears the in-memory cache. Exposed for tests
// that need a clean slate; production callers have no reason to drop it.
func resetShadowSummaryCache() {
	shadowSummaryCacheMu.Lock()
	defer shadowSummaryCacheMu.Unlock()
	shadowSummaryCache = map[string]shadowSummaryCacheEntry{}
}

// handleShadowResultsSummary implements
// GET /api/v1/policy/bundles/{id}/shadow/results/summary.
//
// Returns aggregate counts for each diff class over the [from, to]
// window. Admin-only + tenant-scoped. Memoised for 60 s per
// (tenant, bundle, window) so dashboard polling is cheap.
func (s *server) handleShadowResultsSummary(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, client) {
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	bundleID, ok := extractBundleIDFromPath(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery, "bundle id required in path")
		return
	}

	fromMs, toMs, httpErr := parseShadowResultsRange(r)
	if httpErr != nil {
		writeJSONError(w, httpErr.status, errorCodePolicyShadowQuery, httpErr.message)
		return
	}

	now := time.Now()
	cacheKey := shadowSummaryCacheKey(tenant, bundleID, fromMs, toMs)
	if cached, hit := lookupShadowSummaryCache(cacheKey, now); hit {
		writeJSON(w, cached)
		return
	}

	events, truncated, err := scanShadowEvents(
		r.Context(), client, shadowStreamKey(tenant), bundleID, fromMs, toMs, 0,
	)
	if err != nil {
		writeInternalError(w, r, "shadow results summary: scan", err)
		return
	}
	summary := reduceShadowSummary(bundleID, fromMs, toMs, events, truncated)
	storeShadowSummaryCache(cacheKey, summary, now)
	writeJSON(w, summary)
}

// reduceShadowSummary folds the scanned events into the aggregate.
// Separated from the HTTP handler so unit tests can assert the
// reducer directly against a fabricated event slice.
func reduceShadowSummary(bundleID string, fromMs, toMs int64, events []audit.SIEMEvent, truncated bool) ShadowResultsSummary {
	s := ShadowResultsSummary{
		BundleID:       bundleID,
		FromMs:         fromMs,
		ToMs:           toMs,
		TruncatedAtMax: truncated,
	}
	var latestShadowMs int64
	for _, ev := range events {
		s.TotalEvaluated++
		switch ev.Extra["diff"] {
		case "escalated":
			s.EscalatedCount++
		case "relaxed":
			s.RelaxedCount++
		case "approval_differ":
			s.ApprovalDifferCount++
		case "unchanged":
			s.UnchangedCount++
		}
		// Most-recent shadow_bundle_id. Events are arrival-ordered
		// (oldest first) so we update on every pass rather than track
		// indices — simpler and immune to out-of-order fixtures.
		if id := ev.Extra["shadow_bundle_id"]; id != "" {
			if ms := ev.Timestamp.UnixMilli(); ms >= latestShadowMs {
				latestShadowMs = ms
				s.ShadowBundleID = id
			}
		}
	}
	return s
}

// ---------------------------------------------------------------------------
// Comparisons endpoint
// ---------------------------------------------------------------------------

// ShadowComparisonEntry is a single row returned by the comparisons
// handler. JSON names are frozen — SDK + dashboard rely on them.
type ShadowComparisonEntry struct {
	TsMs           int64  `json:"ts_ms"`
	JobID          string `json:"job_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	ShadowBundleID string `json:"shadow_bundle_id,omitempty"`
	ActiveVerdict  string `json:"active_verdict,omitempty"`
	ShadowVerdict  string `json:"shadow_verdict,omitempty"`
	Diff           string `json:"diff,omitempty"`
	ActiveRuleID   string `json:"active_rule_id,omitempty"`
	ShadowRuleID   string `json:"shadow_rule_id,omitempty"`
	LatencyMs      string `json:"latency_ms,omitempty"`
	Seq            int64  `json:"seq,omitempty"`
}

// ShadowComparisonsResponse is the wrapper for paginated comparison
// entries. NextCursor is the Redis Stream ID of the last entry in the
// page — clients pass it back as ?cursor= to fetch the next page.
// Empty when the caller has reached the end of the range.
type ShadowComparisonsResponse struct {
	Entries        []ShadowComparisonEntry `json:"entries"`
	NextCursor     string                  `json:"next_cursor,omitempty"`
	TruncatedAtMax bool                    `json:"truncated_at_max"`
}

const (
	shadowComparisonsDefaultLimit = 50
	shadowComparisonsMaxLimit     = 500
	// shadowComparisonsScanFactor bounds the over-scan budget when a
	// diff filter is set. Requesting 50 `escalated` rows may require
	// scanning several hundred events if most are `unchanged`. 5× is a
	// reasonable guardrail — a busy tenant with mostly-unchanged
	// diffs still gets a responsive page, and the caller can paginate
	// past a dense "unchanged" region via the cursor.
	shadowComparisonsScanFactor = 5
)

var validShadowDiffFilters = map[string]struct{}{
	"":                {}, // default = no filter
	"all":             {},
	"escalated":       {},
	"relaxed":         {},
	"approval_differ": {},
	"unchanged":       {},
}

// handleShadowResultsComparisons implements
// GET /api/v1/policy/bundles/{id}/shadow/results/comparisons.
//
// Query params:
//
//	from, to  — required unix ms bounds (range ≤ 30 days)
//	diff      — optional filter in {escalated, relaxed, approval_differ, unchanged, all}
//	cursor    — optional Redis stream ID (exclusive start for pagination)
//	limit     — optional page size (default 50, max 500)
//
// Admin + tenant-scoped. Never cached (cursor-bound responses drift
// once new events arrive, caching would serve stale pages).
func (s *server) handleShadowResultsComparisons(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, client) {
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	bundleID, ok := extractBundleIDFromPath(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery, "bundle id required in path")
		return
	}
	fromMs, toMs, httpErr := parseShadowResultsRange(r)
	if httpErr != nil {
		writeJSONError(w, httpErr.status, errorCodePolicyShadowQuery, httpErr.message)
		return
	}

	q := r.URL.Query()
	diffFilter := strings.ToLower(strings.TrimSpace(q.Get("diff")))
	if _, okDiff := validShadowDiffFilters[diffFilter]; !okDiff {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery,
			"diff must be one of: escalated, relaxed, approval_differ, unchanged, all")
		return
	}
	if diffFilter == "all" {
		diffFilter = ""
	}

	limit := int64(shadowComparisonsDefaultLimit)
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery, "limit must be a positive integer")
			return
		}
		if v > shadowComparisonsMaxLimit {
			writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery,
				fmt.Sprintf("limit exceeds maximum (%d)", shadowComparisonsMaxLimit))
			return
		}
		limit = v
	}
	cursor := strings.TrimSpace(q.Get("cursor"))
	if cursor != "" && !isValidStreamID(cursor) {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery, "cursor must be a Redis Stream ID (<ms>-<seq>)")
		return
	}

	// When a diff filter is set, over-scan by shadowComparisonsScanFactor
	// so the page isn't half-empty after filtering. The scanner caps at
	// MaxVerifyLimit, so runaway over-scans are still bounded.
	scanLimit := limit
	if diffFilter != "" {
		scanLimit = limit * shadowComparisonsScanFactor
	}
	events, lastID, truncated, err := scanShadowEventsPaged(
		r.Context(), client, shadowStreamKey(tenant), bundleID, fromMs, toMs, cursor, scanLimit,
	)
	if err != nil {
		writeInternalError(w, r, "shadow results comparisons: scan", err)
		return
	}

	entries := make([]ShadowComparisonEntry, 0, limit)
	var lastKeptID string
	for _, ev := range events {
		if diffFilter != "" && ev.Extra["diff"] != diffFilter {
			continue
		}
		entries = append(entries, shadowEventToComparisonEntry(ev))
		lastKeptID = streamIDForEvent(ev)
		if int64(len(entries)) >= limit {
			break
		}
	}
	// NextCursor reflects the last entry we emitted, not the last
	// scanner ID — if we truncated mid-scan during filtering, callers
	// should resume from the last visible row, not from a row they
	// never saw.
	resp := ShadowComparisonsResponse{
		Entries:        entries,
		NextCursor:     lastKeptID,
		TruncatedAtMax: truncated,
	}
	// Nothing more to fetch when the scan exhausted the range AND we
	// kept all of it. Surface NextCursor="" in that case.
	if !truncated && int64(len(entries)) < limit {
		resp.NextCursor = ""
	}
	// Also blank NextCursor when lastID is empty (no matching events).
	if lastID == "" {
		resp.NextCursor = ""
	}
	writeJSON(w, resp)
}

// shadowEventToComparisonEntry lifts the subset of fields a comparison
// row exposes out of the SIEMEvent Extra map + top-level identity.
func shadowEventToComparisonEntry(ev audit.SIEMEvent) ShadowComparisonEntry {
	return ShadowComparisonEntry{
		TsMs:           ev.Timestamp.UnixMilli(),
		JobID:          ev.JobID,
		AgentID:        ev.AgentID,
		ShadowBundleID: ev.Extra["shadow_bundle_id"],
		ActiveVerdict:  ev.Extra["active_verdict"],
		ShadowVerdict:  ev.Extra["shadow_verdict"],
		Diff:           ev.Extra["diff"],
		ActiveRuleID:   ev.Extra["active_rule_id"],
		ShadowRuleID:   ev.Extra["shadow_rule_id"],
		LatencyMs:      ev.Extra["latency_ms"],
		Seq:            ev.Seq,
	}
}

// streamIDForEvent rebuilds the Redis Stream ID from the event's
// timestamp + seq. Matches the ID the producer wrote via chain.go
// (<unixms>-<seq>) so the returned cursor round-trips through the
// next XRANGE call without loss.
func streamIDForEvent(ev audit.SIEMEvent) string {
	return fmt.Sprintf("%d-%d", ev.Timestamp.UnixMilli(), ev.Seq)
}

// isValidStreamID sanity-checks a caller-supplied cursor so a typo
// doesn't turn into a Redis protocol error. Accepts "<ms>-<seq>" or
// "<ms>" (Redis treats the latter as <ms>-0). Anything else 400s.
func isValidStreamID(id string) bool {
	if id == "" {
		return false
	}
	parts := strings.SplitN(id, "-", 2)
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return false
	}
	if len(parts) == 2 {
		if _, err := strconv.ParseUint(parts[1], 10, 64); err != nil {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Timeseries endpoint
// ---------------------------------------------------------------------------

// ShadowTimeseriesBucket is one row of the timeseries response — a
// zero-filled bucket carries zero counts with a ts_ms on the bucket
// boundary.
type ShadowTimeseriesBucket struct {
	TsMs           int64 `json:"ts_ms"`
	Escalated      int64 `json:"escalated"`
	Relaxed        int64 `json:"relaxed"`
	ApprovalDiffer int64 `json:"approval_differ"`
	Unchanged      int64 `json:"unchanged"`
	Total          int64 `json:"total"`
}

// ShadowTimeseriesResponse is the wrapper for timeseries payloads.
type ShadowTimeseriesResponse struct {
	Bucket         string                   `json:"bucket"`
	FromMs         int64                    `json:"from_ms"`
	ToMs           int64                    `json:"to_ms"`
	Buckets        []ShadowTimeseriesBucket `json:"buckets"`
	TruncatedAtMax bool                     `json:"truncated_at_max"`
}

// shadowBucketDurations maps the whitelisted bucket strings to their
// millisecond widths. Explicitly not derived from time.ParseDuration
// so a caller cannot inject "2h30m" or "0s" to sneak past the whitelist.
var shadowBucketDurations = map[string]int64{
	"1m":  int64(time.Minute / time.Millisecond),
	"5m":  int64(5 * time.Minute / time.Millisecond),
	"15m": int64(15 * time.Minute / time.Millisecond),
	"1h":  int64(time.Hour / time.Millisecond),
	"1d":  int64(24 * time.Hour / time.Millisecond),
}

// shadowTimeseriesMaxBuckets caps the payload size: a 1-minute bucket
// over the 30-day range ceiling already yields 43,200 buckets, so we
// must reject overly-fine resolutions up front. 2000 is what the
// plan froze; it covers a 1m bucket over 33h, 5m over 7d, 15m over
// 20d, 1h over 83d, etc.
const shadowTimeseriesMaxBuckets = 2000

// handleShadowResultsTimeseries implements
// GET /api/v1/policy/bundles/{id}/shadow/results/timeseries.
//
// Query params:
//
//	from, to — required unix ms bounds (range ≤ 30 days)
//	bucket   — required, one of {1m, 5m, 15m, 1h, 1d}
//
// Returns a zero-filled series so the dashboard chart shows gaps
// rather than stretching the last observed bucket. Admin + tenant-
// scoped. Not cached — the bucket resolution is fine-grained enough
// that callers re-query frequently, and the scan is bounded already.
func (s *server) handleShadowResultsTimeseries(w http.ResponseWriter, r *http.Request) {
	client := s.redisClient()
	if !s.requireStoreAndPermissionOrRole(w, r, auth.PermPolicyRead, []string{"admin"}, client) {
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}
	if err := s.requireTenantAccess(r, tenant); err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	bundleID, ok := extractBundleIDFromPath(r)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery, "bundle id required in path")
		return
	}
	fromMs, toMs, httpErr := parseShadowResultsRange(r)
	if httpErr != nil {
		writeJSONError(w, httpErr.status, errorCodePolicyShadowQuery, httpErr.message)
		return
	}

	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	bucketMs, okBucket := shadowBucketDurations[bucket]
	if !okBucket {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery,
			"bucket must be one of: 1m, 5m, 15m, 1h, 1d")
		return
	}
	// Quantise the start DOWN to the bucket boundary so the first row
	// on every caller's chart has a stable X-axis origin regardless of
	// whether `from` lands mid-bucket.
	alignedFromMs := (fromMs / bucketMs) * bucketMs
	bucketCount := (toMs - alignedFromMs) / bucketMs
	if (toMs-alignedFromMs)%bucketMs != 0 {
		bucketCount++
	}
	if bucketCount > shadowTimeseriesMaxBuckets {
		writeJSONError(w, http.StatusBadRequest, errorCodePolicyShadowQuery,
			fmt.Sprintf("bucket resolution too fine for range: %d buckets exceeds %d max", bucketCount, shadowTimeseriesMaxBuckets))
		return
	}

	events, truncated, err := scanShadowEvents(
		r.Context(), client, shadowStreamKey(tenant), bundleID, fromMs, toMs, 0,
	)
	if err != nil {
		writeInternalError(w, r, "shadow results timeseries: scan", err)
		return
	}

	// Bucket the observations.
	counts := make(map[int64]ShadowTimeseriesBucket, bucketCount)
	for _, ev := range events {
		ts := ev.Timestamp.UnixMilli()
		bucketStart := (ts / bucketMs) * bucketMs
		b := counts[bucketStart]
		b.TsMs = bucketStart
		b.Total++
		switch ev.Extra["diff"] {
		case "escalated":
			b.Escalated++
		case "relaxed":
			b.Relaxed++
		case "approval_differ":
			b.ApprovalDiffer++
		case "unchanged":
			b.Unchanged++
		}
		counts[bucketStart] = b
	}

	// Emit zero-filled slice from alignedFromMs to toMs.
	buckets := make([]ShadowTimeseriesBucket, 0, bucketCount)
	for i := int64(0); i < bucketCount; i++ {
		tsMs := alignedFromMs + i*bucketMs
		if b, ok := counts[tsMs]; ok {
			b.TsMs = tsMs
			buckets = append(buckets, b)
		} else {
			buckets = append(buckets, ShadowTimeseriesBucket{TsMs: tsMs})
		}
	}

	writeJSON(w, ShadowTimeseriesResponse{
		Bucket:         bucket,
		FromMs:         alignedFromMs,
		ToMs:           toMs,
		Buckets:        buckets,
		TruncatedAtMax: truncated,
	})
}

// ---------------------------------------------------------------------------
// Shared helpers for the three endpoints
// ---------------------------------------------------------------------------

// shadowResultsHTTPError is a narrow verbatim copy of verifyHTTPError —
// we keep them separate so a future refactor to one endpoint doesn't
// accidentally change the other's response phrasing.
type shadowResultsHTTPError struct {
	status  int
	message string
}

// parseShadowResultsRange enforces the common `from` / `to` contract
// for all three endpoints: both required, non-negative, from < to, and
// the window cannot exceed 30 days.
func parseShadowResultsRange(r *http.Request) (int64, int64, *shadowResultsHTTPError) {
	q := r.URL.Query()
	fromRaw := strings.TrimSpace(q.Get("from"))
	toRaw := strings.TrimSpace(q.Get("to"))
	if fromRaw == "" || toRaw == "" {
		return 0, 0, &shadowResultsHTTPError{http.StatusBadRequest, "from and to query params are required (unix ms)"}
	}
	from, err := strconv.ParseInt(fromRaw, 10, 64)
	if err != nil || from < 0 {
		return 0, 0, &shadowResultsHTTPError{http.StatusBadRequest, "from must be a non-negative unix millisecond"}
	}
	to, err := strconv.ParseInt(toRaw, 10, 64)
	if err != nil || to < 0 {
		return 0, 0, &shadowResultsHTTPError{http.StatusBadRequest, "to must be a non-negative unix millisecond"}
	}
	if to <= from {
		return 0, 0, &shadowResultsHTTPError{http.StatusBadRequest, "to must be strictly greater than from"}
	}
	if to-from > maxVerifySinceUntilSpread.Milliseconds() {
		return 0, 0, &shadowResultsHTTPError{http.StatusBadRequest, "from/to range exceeds 30 days"}
	}
	return from, to, nil
}

// extractBundleIDFromPath resolves the {id} path segment in the
// /api/v1/policy/bundles/{id}/shadow/results/* routes. Returns ("",
// false) for the empty / whitespace case so the handler can 400.
// Logical bundle IDs contain "/" (e.g. "secops/safety") and are
// tilde-encoded on the wire by the dashboard (bundleIDForPath). The
// decode here must match policybundles.BundleIDFromRequest so both
// the /shadow and /shadow/results/* handlers resolve the same
// canonical ID for the same wire path.
func extractBundleIDFromPath(r *http.Request) (string, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		return "", false
	}
	return strings.ReplaceAll(id, "~", "/"), true
}

// shadowStreamKey is the tenant's audit-chain stream key — the same
// key the chainer uses to append every SIEMEvent. We route through
// audit.NewChainer rather than hard-coding the prefix so a future
// change to ChainKeyPrefix automatically flows through.
func shadowStreamKey(tenant string) string {
	return audit.NewChainer(nil, "").StreamKey(tenant)
}

// writeJSONShadow is a tiny wrapper over the package's writeJSON so
// future refactors can add a content-type or cache-control header in
// one place without touching every call site. Currently just delegates.
func writeJSONShadow(w http.ResponseWriter, v any) {
	raw, err := json.Marshal(v)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "marshal response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprint(w, string(raw))
}

// Compile-time assertion that the timeseries helper contract is stable.
var _ = writeJSONShadow
