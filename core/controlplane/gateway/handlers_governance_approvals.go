package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/timeutil"
	"github.com/cordum/cordum/core/model"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Approval analytics — Phase 2 governance widget (task-10eee0ca).
// Consumes the Policy Decision Log (sibling task-24f3b7e0) as the
// source of truth: filter to require_approval verdicts, pair each
// decision with its ApprovalRecord via jobStore.GetApprovalRecord,
// aggregate counts + time-to-approve percentiles + auto-vs-manual
// ratios. Cached per-tenant for 30s to smooth dashboard polling
// without masking new approvals for long.

const (
	approvalAnalyticsDefaultLimit        = 10
	approvalAnalyticsMaxLimit            = 50
	approvalAnalyticsCandidateMultiplier = 50 // Limit*50 candidate scan cap
	approvalAnalyticsCacheTTL            = 30 * time.Second
	approvalAnalyticsCacheMaxEntries     = 512
	approvalAnalyticsMaxWindowDays       = 30
)

var approvalAnalyticsQueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_governance_approval_analytics_query_total",
	Help: "Total approval analytics queries by group_by.",
}, []string{"group_by"})

var approvalAnalyticsHandlerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "cordum_governance_approval_analytics_handler_latency_seconds",
	Help:    "Latency of the approval analytics handler in seconds.",
	Buckets: prometheus.DefBuckets,
}, []string{"group_by"})

var approvalAnalyticsCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_governance_approval_analytics_cache_total",
	Help: "Approval analytics in-memory cache hit/miss counts.",
}, []string{"outcome"})

// approvalAnalyticsResponse is the wire shape of the endpoint body.
// Field names use snake_case so SDKs normalise to camelCase during
// their transform layer (dashboard/src/api/transform.ts).
type approvalAnalyticsResponse struct {
	Window  approvalAnalyticsWindow  `json:"window"`
	Summary approvalAnalyticsSummary `json:"summary"`
	Groups  []approvalAnalyticsGroup `json:"groups,omitempty"`
}

type approvalAnalyticsWindow struct {
	Since string `json:"since"`
	Until string `json:"until"`
}

// approvalAnalyticsSummary carries window-wide KPIs. Percentile
// fields are pointers so "no approvals in window" distinguishes
// from "all approvals resolved in 0s".
type approvalAnalyticsSummary struct {
	Total                   int      `json:"total"`
	Approved                int      `json:"approved"`
	Rejected                int      `json:"rejected"`
	Expired                 int      `json:"expired"`
	AutoResolved            int      `json:"auto_resolved"`
	ManualResolved          int      `json:"manual_resolved"`
	AvgTimeToApproveSeconds *float64 `json:"avg_time_to_approve_seconds"`
	P50                     *float64 `json:"p50"`
	P90                     *float64 `json:"p90"`
	P99                     *float64 `json:"p99"`
}

type approvalAnalyticsGroup struct {
	Key            string   `json:"key"`
	Label          string   `json:"label"`
	Total          int      `json:"total"`
	Approved       int      `json:"approved"`
	Rejected       int      `json:"rejected"`
	Expired        int      `json:"expired"`
	AutoCount      int      `json:"auto_count"`
	ManualCount    int      `json:"manual_count"`
	AvgTTARSeconds *float64 `json:"avg_ttar_seconds"`
	P90Seconds     *float64 `json:"p90_seconds"`
}

// approvalAnalyticsQuery is the parsed + validated URL query.
type approvalAnalyticsQuery struct {
	Since   int64
	Until   int64
	GroupBy string // one of: rule, agent, topic, overall
	Limit   int
}

func (s *server) handleApprovalAnalytics(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	if s.decisionLogStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "decision log store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermGovernanceRead) {
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	query, err := parseApprovalAnalyticsQuery(r, time.Now().UTC())
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	approvalAnalyticsQueryTotal.WithLabelValues(query.GroupBy).Inc()
	defer func() {
		approvalAnalyticsHandlerLatency.WithLabelValues(query.GroupBy).Observe(time.Since(start).Seconds())
	}()

	resp, err := s.approvalAnalytics(r.Context(), tenant, query)
	if err != nil {
		writeInternalError(w, r, "approval analytics", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// approvalAnalytics is the pure compute layer: given a validated
// query + a tenant, produce the response. Split from handler so
// unit tests can drive it directly without building an HTTP request.
func (s *server) approvalAnalytics(ctx context.Context, tenant string, q approvalAnalyticsQuery) (approvalAnalyticsResponse, error) {
	cacheKey := approvalAnalyticsCacheKey(tenant, q)
	if cached, ok := s.approvalAnalyticsCache.get(cacheKey); ok {
		approvalAnalyticsCacheHits.WithLabelValues("hit").Inc()
		return cached, nil
	}
	approvalAnalyticsCacheHits.WithLabelValues("miss").Inc()

	page, err := s.decisionLogStore.QueryDecisions(ctx, model.DecisionQuery{
		Tenant:  tenant,
		Since:   q.Since,
		Until:   q.Until,
		Verdict: model.SafetyRequireApproval,
		Limit:   q.Limit * approvalAnalyticsCandidateMultiplier,
	})
	if err != nil {
		return approvalAnalyticsResponse{}, fmt.Errorf("query decisions: %w", err)
	}

	samples := make([]approvalSample, 0, len(page.Items))
	for _, rec := range page.Items {
		if rec.JobID == "" {
			continue
		}
		appr, aerr := s.jobStore.GetApprovalRecord(ctx, rec.JobID)
		// A missing ApprovalRecord is a still-pending approval — we
		// count the total but cannot contribute a TTAR sample.
		sample := approvalSample{
			record:      rec,
			hasApproval: aerr == nil,
		}
		if aerr == nil {
			sample.approval = appr
		}
		samples = append(samples, sample)
	}

	resp := approvalAnalyticsResponse{
		Window:  approvalAnalyticsWindow{Since: millisToRFC3339(q.Since), Until: millisToRFC3339(q.Until)},
		Summary: summariseApprovalSamples(samples),
	}
	if q.GroupBy != "overall" {
		resp.Groups = groupApprovalSamples(samples, q.GroupBy, q.Limit)
	}

	s.approvalAnalyticsCache.set(cacheKey, resp)
	return resp, nil
}

// approvalSample pairs a decision-log record with its matching
// ApprovalRecord (when one exists yet).
type approvalSample struct {
	record      model.DecisionLogRecord
	approval    model.ApprovalRecord
	hasApproval bool
}

// ttarSeconds returns the time-to-approve in seconds, or (0, false)
// when the approval is still pending.
func (s approvalSample) ttarSeconds() (float64, bool) {
	if !s.hasApproval || s.approval.ApprovedAt == 0 || s.record.Timestamp == 0 {
		return 0, false
	}
	ms := s.approval.ApprovedAt - s.record.Timestamp
	if ms <= 0 {
		return 0, false
	}
	return float64(ms) / 1000.0, true
}

// isApproved / isRejected / isExpired — outcome classification.
// Auto-resolved: the approval fired without a human decision
// (ApprovalDecision is empty but Status is terminal, or
// EffectiveOwnerKind distinguishes system-driven resolutions). The
// plan's discriminator is approval.Decision == approve|reject ⇒
// manual; anything else terminal ⇒ auto.
func (s approvalSample) classify() (approved, rejected, expired bool, auto bool, resolved bool) {
	if !s.hasApproval {
		return false, false, false, false, false
	}
	switch s.approval.Decision {
	case model.ApprovalDecisionApprove:
		return true, false, false, false, true
	case model.ApprovalDecisionReject:
		return false, true, false, false, true
	case model.ApprovalDecisionExpire:
		return false, false, true, true, true
	case model.ApprovalDecisionInvalidate, model.ApprovalDecisionRepair:
		// Auto-resolved lifecycle events — counted but not approved.
		return false, false, false, true, true
	}
	// No explicit decision yet.
	return false, false, false, false, false
}

// groupKey extracts the per-group bucket key.
func (s approvalSample) groupKey(groupBy string) (key, label string) {
	switch groupBy {
	case "rule":
		return s.record.RuleID, s.record.RuleID
	case "agent":
		return s.record.AgentID, s.record.AgentID
	case "topic":
		return s.record.Topic, s.record.Topic
	}
	return "", ""
}

// summariseApprovalSamples computes the window-wide summary.
func summariseApprovalSamples(samples []approvalSample) approvalAnalyticsSummary {
	out := approvalAnalyticsSummary{Total: len(samples)}
	ttars := make([]float64, 0, len(samples))
	for _, sample := range samples {
		approved, rejected, expired, auto, resolved := sample.classify()
		if !resolved {
			continue
		}
		if approved {
			out.Approved++
		}
		if rejected {
			out.Rejected++
		}
		if expired {
			out.Expired++
		}
		if auto {
			out.AutoResolved++
		} else {
			out.ManualResolved++
		}
		if v, ok := sample.ttarSeconds(); ok {
			ttars = append(ttars, v)
		}
	}
	if len(ttars) > 0 {
		sort.Float64s(ttars)
		avg := floatSum(ttars) / float64(len(ttars))
		out.AvgTimeToApproveSeconds = &avg
		p50 := ttarPercentile(ttars, 0.50)
		p90 := ttarPercentile(ttars, 0.90)
		p99 := ttarPercentile(ttars, 0.99)
		out.P50, out.P90, out.P99 = &p50, &p90, &p99
	}
	return out
}

// groupApprovalSamples buckets samples by key, computes per-group
// KPIs, and returns the top-N sorted by avg_ttar_seconds desc so
// the worst bottlenecks surface first.
func groupApprovalSamples(samples []approvalSample, groupBy string, limit int) []approvalAnalyticsGroup {
	type bucket struct {
		samples []approvalSample
	}
	buckets := map[string]*bucket{}
	for _, sample := range samples {
		key, _ := sample.groupKey(groupBy)
		if key == "" {
			continue
		}
		b, ok := buckets[key]
		if !ok {
			b = &bucket{}
			buckets[key] = b
		}
		b.samples = append(b.samples, sample)
	}
	out := make([]approvalAnalyticsGroup, 0, len(buckets))
	for key, b := range buckets {
		group := approvalAnalyticsGroup{Key: key, Label: key, Total: len(b.samples)}
		ttars := make([]float64, 0, len(b.samples))
		for _, sample := range b.samples {
			approved, rejected, expired, auto, resolved := sample.classify()
			if !resolved {
				continue
			}
			if approved {
				group.Approved++
			}
			if rejected {
				group.Rejected++
			}
			if expired {
				group.Expired++
			}
			if auto {
				group.AutoCount++
			} else {
				group.ManualCount++
			}
			if v, ok := sample.ttarSeconds(); ok {
				ttars = append(ttars, v)
			}
		}
		if len(ttars) > 0 {
			sort.Float64s(ttars)
			avg := floatSum(ttars) / float64(len(ttars))
			p90 := ttarPercentile(ttars, 0.90)
			group.AvgTTARSeconds, group.P90Seconds = &avg, &p90
		}
		out = append(out, group)
	}
	sort.Slice(out, func(i, j int) bool {
		li, ri := safeDeref(out[i].AvgTTARSeconds), safeDeref(out[j].AvgTTARSeconds)
		if li == ri {
			return out[i].Key < out[j].Key
		}
		return li > ri
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ttarPercentile returns the p-th percentile of a sorted slice using
// nearest-rank (enough for dashboard precision; full HDR histogram
// would be overkill at the Limit*50 candidate cap).
func ttarPercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p*float64(len(sorted)-1) + 0.5)
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	if rank < 0 {
		rank = 0
	}
	return sorted[rank]
}

func floatSum(values []float64) float64 {
	var sum float64
	for _, v := range values {
		sum += v
	}
	return sum
}

func safeDeref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// millisToRFC3339 forwards to timeutil.FromMillis. See task-e396a874.
func millisToRFC3339(ms int64) string {
	return timeutil.FromMillis(ms)
}

// parseApprovalAnalyticsQuery validates + normalises URL params.
func parseApprovalAnalyticsQuery(r *http.Request, now time.Time) (approvalAnalyticsQuery, error) {
	q := approvalAnalyticsQuery{
		GroupBy: strings.TrimSpace(strings.ToLower(r.URL.Query().Get("group_by"))),
	}
	if q.GroupBy == "" {
		q.GroupBy = "overall"
	}
	switch q.GroupBy {
	case "overall", "rule", "agent", "topic":
	default:
		return q, fmt.Errorf("invalid group_by (allowed: overall|rule|agent|topic)")
	}

	// window= shorthand wins over since/until when present.
	if shorthand := strings.TrimSpace(r.URL.Query().Get("window")); shorthand != "" {
		dur, err := parseWindowShorthand(shorthand)
		if err != nil {
			return q, err
		}
		q.Until = now.UnixMilli()
		q.Since = now.Add(-dur).UnixMilli()
	} else {
		since, err := parseGovernanceDecisionTime(r.URL.Query().Get("since"))
		if err != nil {
			return q, fmt.Errorf("invalid since")
		}
		until, err := parseGovernanceDecisionTime(r.URL.Query().Get("until"))
		if err != nil {
			return q, fmt.Errorf("invalid until")
		}
		if since == 0 && until == 0 {
			q.Until = now.UnixMilli()
			q.Since = now.Add(-24 * time.Hour).UnixMilli()
		} else {
			q.Since, q.Until = since, until
			if q.Until == 0 {
				q.Until = now.UnixMilli()
			}
			if q.Since == 0 {
				q.Since = time.UnixMilli(q.Until).Add(-24 * time.Hour).UnixMilli()
			}
		}
	}
	if q.Until < q.Since {
		return q, fmt.Errorf("until must be >= since")
	}
	if spread := time.UnixMilli(q.Until).Sub(time.UnixMilli(q.Since)); spread > approvalAnalyticsMaxWindowDays*24*time.Hour {
		return q, fmt.Errorf("window must be <= %d days", approvalAnalyticsMaxWindowDays)
	}

	q.Limit = approvalAnalyticsDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return q, fmt.Errorf("invalid limit")
		}
		if n > approvalAnalyticsMaxLimit {
			return q, fmt.Errorf("limit must be <= %d", approvalAnalyticsMaxLimit)
		}
		q.Limit = n
	}
	return q, nil
}

// parseWindowShorthand accepts 24h / 7d / 30d shorthand values.
func parseWindowShorthand(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, fmt.Errorf("empty window")
	}
	switch raw {
	case "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid window (allowed: 24h|7d|30d)")
}

// approvalAnalyticsCacheKey is a deterministic cache key covering
// tenant + window + group_by + limit. Using a hash over the stable
// key avoids whitespace/casing drift.
func approvalAnalyticsCacheKey(tenant string, q approvalAnalyticsQuery) string {
	h := sha256.New()
	h.Write([]byte(tenant))
	h.Write([]byte{0})
	h.Write([]byte(q.GroupBy))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(q.Since, 10)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(q.Until, 10)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.Itoa(q.Limit)))
	return hex.EncodeToString(h.Sum(nil))
}

// approvalAnalyticsMemCache is a simple per-server TTL cache keyed by
// a sha256 of (tenant, window, group_by, limit). In-memory rather
// than Redis because the payload is small + the TTL is short + this
// tier of caching is strictly a polling-smoother, not a source of
// truth. A burst of identical requests from the same dashboard tab
// collapses to one decision-log query.
type approvalAnalyticsMemCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]approvalAnalyticsCacheEntry
}

type approvalAnalyticsCacheEntry struct {
	response approvalAnalyticsResponse
	expires  time.Time
}

// newApprovalAnalyticsCache returns a fresh cache with the package's
// default TTL. The server wires this up at construction time.
func newApprovalAnalyticsCache() *approvalAnalyticsMemCache {
	return &approvalAnalyticsMemCache{
		ttl:        approvalAnalyticsCacheTTL,
		maxEntries: approvalAnalyticsCacheMaxEntries,
		entries:    map[string]approvalAnalyticsCacheEntry{},
	}
}

func (c *approvalAnalyticsMemCache) get(key string) (approvalAnalyticsResponse, bool) {
	if c == nil {
		return approvalAnalyticsResponse{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return approvalAnalyticsResponse{}, false
	}
	if time.Now().After(entry.expires) {
		delete(c.entries, key)
		return approvalAnalyticsResponse{}, false
	}
	return entry.response, true
}

func (c *approvalAnalyticsMemCache) set(key string, resp approvalAnalyticsResponse) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.evictExpiredLocked(now)
	if c.maxEntries <= 0 {
		c.maxEntries = approvalAnalyticsCacheMaxEntries
	}
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		c.evictOneLocked()
	}
	c.entries[key] = approvalAnalyticsCacheEntry{response: resp, expires: now.Add(c.ttl)}
}

func (c *approvalAnalyticsMemCache) evictExpiredLocked(now time.Time) {
	for key, entry := range c.entries {
		if now.After(entry.expires) {
			delete(c.entries, key)
		}
	}
}

func (c *approvalAnalyticsMemCache) evictOneLocked() {
	for key := range c.entries {
		delete(c.entries, key)
		return
	}
}
