// Package governance holds cross-cutting metrics derived from Cordum's
// audit, approval, and policy subsystems.
//
// The HealthScore type is the composite metric surfaced on the Command
// Center home page. It is deliberately NOT a real-time gauge — the
// underlying data sources (audit stream scan, approval latency
// histogram, topic-coverage count, chain verification) are too expensive
// to recompute per request. A ComputeHealth call caches per-tenant
// results for 60 seconds so the Command Center can poll on a ~15 s
// cadence without hammering Redis.
package governance

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Weight constants. Sum to 100 so the weighted total maps directly to
// the 0-100 score dimension. Operators can override via the
// (currently-unused) Weights field on HealthDeps when we wire the
// per-tenant tunable knob; v1 ships the defaults.
const (
	WeightDenialRate         = 25
	WeightApprovalLatencyP95 = 25
	WeightPolicyCoverage     = 25
	WeightChainIntegrity     = 25

	// FloorCompromisedChain is the maximum overall score a tenant with a
	// compromised audit chain can receive. Forces the widget into the
	// F-grade band regardless of how clean the other three factors are —
	// a compromised chain is a compliance-stop condition.
	FloorCompromisedChain = 55

	// NeutralFactorScore is returned when a factor's input is missing or
	// undefined (e.g. zero jobs in the window for denial_rate). Better
	// than a zero — a tenant that has done no work should not be marked
	// failing — but below the 90-band cutoff so "no data" never earns an
	// A.
	NeutralFactorScore = 70

	defaultCacheTTL        = 60 * time.Second
	defaultCacheMaxEntries = 1024
)

// Factor names. Exported so tests and the HTTP handler can reference
// them without stringly-typed drift.
const (
	FactorDenialRate         = "denial_rate"
	FactorApprovalLatencyP95 = "approval_latency_p95"
	FactorPolicyCoverage     = "policy_coverage"
	FactorChainIntegrity     = "chain_integrity"
)

// HealthScore is the user-facing aggregate.
//
// Score is the weighted sum of every factor's (Score * Weight / 100),
// capped at FloorCompromisedChain when the chain-integrity factor
// reports compromised. Grade is the letter mapping (A-F).
// TruncatedAtMax is true when any factor's underlying scan hit its
// hard cap (e.g. 100k audit events) and the raw value is approximate.
type HealthScore struct {
	Score          int                     `json:"score"`
	Grade          string                  `json:"grade"`
	GeneratedAt    time.Time               `json:"generated_at"`
	Factors        map[string]HealthFactor `json:"factors"`
	TruncatedAtMax bool                    `json:"truncated_at_max,omitempty"`
}

// HealthFactor is one of the four inputs that roll up into Score.
// Raw carries the source measurement (a rate, p95 duration, ratio, or
// enum) so the dashboard tooltip can render the "why" behind the score.
// Notes is operator-facing text — if a factor is unavailable (dep
// failure), Notes carries the reason and Score stays at NeutralFactorScore.
type HealthFactor struct {
	Score  int     `json:"score"`
	Weight float64 `json:"weight"`
	Raw    any     `json:"raw,omitempty"`
	Notes  string  `json:"notes,omitempty"`
}

// HealthDeps abstracts the data sources ComputeHealth needs. Gateway
// wiring implements each interface against its existing stores; unit
// tests inject fakes.
type HealthDeps interface {
	AuditScanner
	ApprovalsLister
	TopicsLister
	PolicyBundleLister
	ChainVerifier

	// Tenant is the scope every factor is computed within.
	Tenant() string

	// Now lets tests inject a deterministic clock. Production wiring
	// returns time.Now().UTC().
	Now() time.Time
}

// AuditScanner walks the safety-decision audit stream within a window.
// The implementation is expected to honour MaxEvents and return the
// counts + a Truncated flag when the cap was hit.
type AuditScanner interface {
	// ScanDecisions returns counts {allow, deny, require_approval, other}
	// over the window ending at `now`.
	ScanDecisions(ctx context.Context, window time.Duration, now time.Time) (DecisionCounts, error)
}

// DecisionCounts is the shape ScanDecisions returns.
type DecisionCounts struct {
	Allow           int
	Deny            int
	RequireApproval int
	Other           int
	Truncated       bool
	ScannedEvents   int
}

// Total returns allow+deny+require_approval+other.
func (d DecisionCounts) Total() int {
	return d.Allow + d.Deny + d.RequireApproval + d.Other
}

// ApprovalsLister returns timing samples for approvals resolved within
// the window. Only APPROVED/REJECTED records count — still-pending
// approvals don't have a resolution latency.
type ApprovalsLister interface {
	// ApprovalLatencies returns resolution durations (Resolve - Enqueue)
	// in milliseconds for approvals that hit a terminal state inside
	// the window. Truncated is true when the backing query hit its scan
	// limit and the sample set is approximate.
	ApprovalLatencies(ctx context.Context, window time.Duration, now time.Time) ([]time.Duration, bool, error)
}

// TopicsLister returns all topics the tenant can publish to.
type TopicsLister interface {
	ListTopics(ctx context.Context) ([]string, error)
}

// PolicyBundleLister returns the set of topics that have at least one
// rule in an enabled bundle.
type PolicyBundleLister interface {
	CoveredTopics(ctx context.Context) ([]string, error)
}

// ChainVerifier returns the audit chain's current integrity status.
type ChainVerifier interface {
	VerifyChain(ctx context.Context) (ChainStatus, error)
}

// ChainStatus is the enum for the chain-integrity factor. Matches the
// string values emitted by /api/v1/audit/verify so UIs that already
// read that endpoint stay consistent.
type ChainStatus string

const (
	ChainStatusOK          ChainStatus = "ok"
	ChainStatusPartial     ChainStatus = "partial"
	ChainStatusCompromised ChainStatus = "compromised"
	ChainStatusUnavailable ChainStatus = "unavailable"
)

// factorThresholds documents the linear-scoring endpoints so tests and
// docs reference one source of truth.
const (
	// denial_rate: 0 → 100, 50%+ → 0 (linear).
	denialBest  = 0.0
	denialWorst = 0.5

	// approval latency p95: <=30s → 100, >=5min → 0 (linear).
	latencyBest  = 30 * time.Second
	latencyWorst = 5 * time.Minute

	// policy coverage: 100% → 100, 0% → 0 (linear).
	coverageBest  = 1.0
	coverageWorst = 0.0
)

// cachedEntry is one tenant's memoised HealthScore.
type cachedEntry struct {
	value *HealthScore
	at    time.Time
}

type cacheInflight struct {
	done  chan struct{}
	value *HealthScore
	err   error
}

// Cache is a tiny concurrency-safe per-tenant TTL cache of HealthScore values.
// The dashboard polls at ~15s; 60s TTL keeps the recompute rate ≤ 1/m
// per tenant. Exported so the gateway can wire one up as a server field
// and share it across handlers.
type Cache struct {
	mu         sync.RWMutex
	ttl        time.Duration
	maxEntries int
	data       map[string]cachedEntry
	inflight   map[string]*cacheInflight
}

// NewCache returns a cache with the given TTL. Zero or negative TTL
// uses the package default (60s).
func NewCache(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = defaultCacheTTL
	}
	return &Cache{
		ttl:        ttl,
		maxEntries: defaultCacheMaxEntries,
		data:       map[string]cachedEntry{},
		inflight:   map[string]*cacheInflight{},
	}
}

// Get returns the cached value for tenant if fresh, else (nil, false).
func (c *Cache) Get(tenant string, now time.Time) (*HealthScore, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	entry, ok := c.data[tenant]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if now.Sub(entry.at) > c.ttl {
		c.mu.Lock()
		if current, exists := c.data[tenant]; exists && current.at.Equal(entry.at) {
			delete(c.data, tenant)
		}
		c.mu.Unlock()
		return nil, false
	}
	// Return a copy so callers can't mutate the cached value.
	return cloneHealthScore(entry.value), true
}

// Put stores value under tenant keyed at now.
func (c *Cache) Put(tenant string, value *HealthScore, now time.Time) {
	if c == nil || value == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[tenant] = cachedEntry{value: cloneHealthScore(value), at: now}
	c.purgeExpiredLocked(now)
	c.enforceMaxEntriesLocked()
}

// Invalidate drops the tenant's cached entry. Used after operator-driven
// state changes (policy bundle publish, chain repair).
func (c *Cache) Invalidate(tenant string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, tenant)
}

func (c *Cache) purgeExpiredLocked(now time.Time) {
	for tenant, entry := range c.data {
		if now.Sub(entry.at) > c.ttl {
			delete(c.data, tenant)
		}
	}
}

func (c *Cache) enforceMaxEntriesLocked() {
	if c.maxEntries <= 0 {
		return
	}
	for len(c.data) > c.maxEntries {
		var oldestTenant string
		var oldestAt time.Time
		for tenant, entry := range c.data {
			if oldestTenant == "" || entry.at.Before(oldestAt) {
				oldestTenant = tenant
				oldestAt = entry.at
			}
		}
		if oldestTenant == "" {
			return
		}
		delete(c.data, oldestTenant)
	}
}

func (c *Cache) computeOnce(ctx context.Context, tenant string, fn func() (*HealthScore, error)) (*HealthScore, error) {
	if c == nil {
		return fn()
	}
	c.mu.Lock()
	if c.inflight == nil {
		c.inflight = map[string]*cacheInflight{}
	}
	if call, ok := c.inflight[tenant]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			if call.err != nil {
				return nil, call.err
			}
			return cloneHealthScore(call.value), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &cacheInflight{done: make(chan struct{})}
	c.inflight[tenant] = call
	c.mu.Unlock()

	value, err := fn()
	call.value = cloneHealthScore(value)
	call.err = err
	close(call.done)

	c.mu.Lock()
	delete(c.inflight, tenant)
	c.mu.Unlock()

	return value, err
}

// ComputeHealth builds a HealthScore for deps.Tenant() from the four
// factor inputs. Per-factor failures are captured as Notes on the
// factor rather than aborting the whole call — a partial score is more
// useful than a 500.
//
// When cache is non-nil and a fresh entry exists for the tenant, it is
// returned without recomputing. Otherwise the fresh result is stored
// before returning.
func ComputeHealth(ctx context.Context, deps HealthDeps, cache *Cache) (*HealthScore, error) {
	if deps == nil {
		return nil, errors.New("governance: deps is nil")
	}
	tenant := deps.Tenant()
	now := deps.Now()

	if cached, ok := cache.Get(tenant, now); ok {
		return cached, nil
	}

	if cache != nil {
		return cache.computeOnce(ctx, tenant, func() (*HealthScore, error) {
			if cached, ok := cache.Get(tenant, now); ok {
				return cached, nil
			}
			score, err := computeHealthScore(ctx, deps, now)
			if err != nil {
				return nil, err
			}
			cache.Put(tenant, score, now)
			return score, nil
		})
	}

	return computeHealthScore(ctx, deps, now)
}

func computeHealthScore(ctx context.Context, deps HealthDeps, now time.Time) (*HealthScore, error) {
	score := &HealthScore{
		GeneratedAt: now,
		Factors:     make(map[string]HealthFactor, 4),
	}

	// Denial-rate factor: last 24h.
	score.Factors[FactorDenialRate] = computeDenialRate(ctx, deps, now)
	// Approval latency p95: last 24h.
	score.Factors[FactorApprovalLatencyP95] = computeApprovalLatency(ctx, deps, now)
	// Policy coverage: current snapshot.
	score.Factors[FactorPolicyCoverage] = computePolicyCoverage(ctx, deps)
	// Chain integrity: current snapshot.
	score.Factors[FactorChainIntegrity] = computeChainIntegrity(ctx, deps)

	// Propagate per-factor truncation up to the aggregate.
	for _, f := range score.Factors {
		if raw, ok := f.Raw.(DecisionCounts); ok && raw.Truncated {
			score.TruncatedAtMax = true
		}
		if raw, ok := f.Raw.(map[string]any); ok {
			if truncated, ok := raw["truncated"].(bool); ok && truncated {
				score.TruncatedAtMax = true
			}
		}
	}

	// Weighted sum. Weights are additive (sum to 100) so the total sits
	// naturally in 0-100.
	total := 0.0
	for _, f := range score.Factors {
		total += float64(f.Score) * f.Weight / 100.0
	}
	score.Score = clamp(int(total+0.5), 0, 100)

	// Compromised-chain floor. Drop the total to F-band regardless of
	// other factor scores so compliance teams see the stop-condition
	// immediately.
	if raw, ok := score.Factors[FactorChainIntegrity].Raw.(ChainStatus); ok && raw == ChainStatusCompromised {
		if score.Score > FloorCompromisedChain {
			score.Score = FloorCompromisedChain
		}
	}
	score.Grade = gradeFor(score.Score)

	return score, nil
}

// computeDenialRate is the last-24h denial-rate factor.
func computeDenialRate(ctx context.Context, deps HealthDeps, now time.Time) HealthFactor {
	window := 24 * time.Hour
	counts, err := deps.ScanDecisions(ctx, window, now)
	if err != nil {
		slog.Warn("governance: ScanDecisions failed", "err", err)
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightDenialRate,
			Notes:  "unavailable: " + err.Error(),
		}
	}
	total := counts.Total()
	if total == 0 {
		// No jobs — return a neutral score with a note so the UI can
		// render "no recent decisions" rather than a green A for doing
		// nothing.
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightDenialRate,
			Raw:    counts,
			Notes:  "no decisions in the last 24h",
		}
	}
	rate := float64(counts.Deny) / float64(total)
	return HealthFactor{
		Score:  linearScore(rate, denialBest, denialWorst),
		Weight: WeightDenialRate,
		Raw:    counts,
	}
}

// computeApprovalLatency is the last-24h approval-latency-p95 factor.
func computeApprovalLatency(ctx context.Context, deps HealthDeps, now time.Time) HealthFactor {
	window := 24 * time.Hour
	samples, truncated, err := deps.ApprovalLatencies(ctx, window, now)
	if err != nil {
		slog.Warn("governance: ApprovalLatencies failed", "err", err)
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightApprovalLatencyP95,
			Notes:  "unavailable: " + err.Error(),
		}
	}
	if len(samples) == 0 {
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightApprovalLatencyP95,
			Raw: map[string]any{
				"samples":   0,
				"truncated": truncated,
			},
			Notes: "no approvals resolved in the last 24h",
		}
	}
	p95 := percentileDuration(samples, 95)
	return HealthFactor{
		Score:  linearDurationScore(p95, latencyBest, latencyWorst),
		Weight: WeightApprovalLatencyP95,
		Raw: map[string]any{
			"p95_ms":    p95.Milliseconds(),
			"samples":   len(samples),
			"truncated": truncated,
		},
	}
}

// computePolicyCoverage is the ratio of topics with ≥1 active rule.
func computePolicyCoverage(ctx context.Context, deps HealthDeps) HealthFactor {
	topics, err := deps.ListTopics(ctx)
	if err != nil {
		slog.Warn("governance: ListTopics failed", "err", err)
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightPolicyCoverage,
			Notes:  "unavailable: " + err.Error(),
		}
	}
	if len(topics) == 0 {
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightPolicyCoverage,
			Raw:    map[string]any{"topics": 0, "covered": 0},
			Notes:  "no topics registered",
		}
	}
	covered, err := deps.CoveredTopics(ctx)
	if err != nil {
		slog.Warn("governance: CoveredTopics failed", "err", err)
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightPolicyCoverage,
			Notes:  "unavailable: " + err.Error(),
		}
	}
	coveredSet := make(map[string]struct{}, len(covered))
	for _, c := range covered {
		coveredSet[c] = struct{}{}
	}
	hits := 0
	for _, t := range topics {
		if _, ok := coveredSet[t]; ok {
			hits++
		}
	}
	ratio := float64(hits) / float64(len(topics))
	// For coverage, higher is better — pass best=1, worst=0 so
	// linearScore normalises the right direction.
	return HealthFactor{
		Score:  linearScore(ratio, coverageBest, coverageWorst),
		Weight: WeightPolicyCoverage,
		Raw: map[string]any{
			"topics":  len(topics),
			"covered": hits,
			"ratio":   ratio,
		},
	}
}

// computeChainIntegrity is the current audit-chain status factor.
func computeChainIntegrity(ctx context.Context, deps HealthDeps) HealthFactor {
	status, err := deps.VerifyChain(ctx)
	if err != nil {
		slog.Warn("governance: VerifyChain failed", "err", err)
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightChainIntegrity,
			Raw:    ChainStatusUnavailable,
			Notes:  "unavailable: " + err.Error(),
		}
	}
	var s int
	switch status {
	case ChainStatusOK:
		s = 100
	case ChainStatusPartial:
		s = 85
	case ChainStatusCompromised:
		s = 0
	case ChainStatusUnavailable:
		return HealthFactor{
			Score:  NeutralFactorScore,
			Weight: WeightChainIntegrity,
			Raw:    status,
			Notes:  "audit chain unavailable",
		}
	default:
		s = NeutralFactorScore
	}
	return HealthFactor{
		Score:  s,
		Weight: WeightChainIntegrity,
		Raw:    status,
	}
}

// linearScore maps rate in [best, worst] to [100, 0] linearly.
// best < worst: higher-is-worse (denial rate). best > worst: swap.
// Out-of-range values clamp to [0, 100].
func linearScore(rate, best, worst float64) int {
	if best == worst {
		return 100
	}
	// Normalise so best=0, worst=1 regardless of direction.
	normalised := (rate - best) / (worst - best)
	if normalised < 0 {
		normalised = 0
	}
	if normalised > 1 {
		normalised = 1
	}
	return clamp(int((1-normalised)*100+0.5), 0, 100)
}

// linearDurationScore is linearScore for time.Duration values.
func linearDurationScore(val, best, worst time.Duration) int {
	return linearScore(float64(val), float64(best), float64(worst))
}

// percentileDuration returns the nth percentile of samples using the
// nearest-rank method. Empty samples return 0. p is in [0, 100].
func percentileDuration(samples []time.Duration, p int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	// Nearest-rank: index = ceil(p/100 * N) - 1, clamped.
	idx := (p*len(sorted)+99)/100 - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// gradeFor maps score → letter grade.
func gradeFor(score int) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 80:
		return "B"
	case score >= 70:
		return "C"
	case score >= 60:
		return "D"
	default:
		return "F"
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func cloneHealthScore(src *HealthScore) *HealthScore {
	if src == nil {
		return nil
	}
	cloned := *src
	if src.Factors != nil {
		cloned.Factors = make(map[string]HealthFactor, len(src.Factors))
		for name, factor := range src.Factors {
			cloned.Factors[name] = cloneHealthFactor(factor)
		}
	}
	return &cloned
}

func cloneHealthFactor(src HealthFactor) HealthFactor {
	cloned := src
	cloned.Raw = cloneRawValue(src.Raw)
	return cloned
}

func cloneRawValue(src any) any {
	switch v := src.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(v))
		for key, value := range v {
			cloned[key] = cloneRawValue(value)
		}
		return cloned
	case []any:
		cloned := make([]any, len(v))
		for i, value := range v {
			cloned[i] = cloneRawValue(value)
		}
		return cloned
	default:
		return v
	}
}
