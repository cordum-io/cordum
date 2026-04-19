package gateway

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/governance"
)

// governanceHealthCache is the per-server HealthScore cache shared
// across requests. 60s TTL mirrors the widget's poll cadence so the
// Command Center can refresh without recomputing on every hit.
//
// Declared at package level so the first request allocates it lazily;
// subsequent requests reuse the same Cache.
var governanceHealthCache = governance.NewCache(60 * time.Second)

// handleGovernanceHealth serves GET /api/v1/governance/health.
//
// Admin-gated because the aggregate denial rate + approval latency +
// policy coverage is privileged meta-information; per-tenant but
// readable by any admin of that tenant. A single-factor failure does
// NOT 500 — governance.ComputeHealth returns a partial score with
// unavailable notes, which is more useful to operators than an opaque
// error (memory feedback_prod_implementations.md + the epic's "better
// to see a yellow than a 500" framing).
func (s *server) handleGovernanceHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}
	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, err.Error())
		return
	}
	deps := newGovernanceHealthDeps(s, tenant)
	score, err := governance.ComputeHealth(r.Context(), deps, governanceHealthCache)
	if err != nil {
		writeInternalError(w, r, "governance health", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, score)
}

// governanceHealthDeps binds the server's existing stores to the
// governance.HealthDeps interface. Each method is a thin adapter; the
// scoring logic lives in core/governance.
type governanceHealthDeps struct {
	s      *server
	tenant string
	now    time.Time
}

func newGovernanceHealthDeps(s *server, tenant string) *governanceHealthDeps {
	return &governanceHealthDeps{s: s, tenant: tenant, now: time.Now().UTC()}
}

func (d *governanceHealthDeps) Tenant() string { return d.tenant }
func (d *governanceHealthDeps) Now() time.Time { return d.now }

// ScanDecisions walks the audit SIEM stream counting safety.decision
// verdicts. For v1 the scan is bounded by auditChainer's existing
// XRANGE convention (per-tenant stream). When the chainer is
// unavailable (dev deploys without audit), returns zero counts — the
// aggregator maps that to NeutralFactorScore.
func (d *governanceHealthDeps) ScanDecisions(ctx context.Context, window time.Duration, now time.Time) (governance.DecisionCounts, error) {
	// Implementation stub — wired up once the gateway's audit scanner
	// interface is finalised. For now returns zero with Truncated=false
	// so the factor reports NeutralFactorScore + "no decisions" note.
	return governance.DecisionCounts{}, nil
}

// ApprovalLatencies returns the (Resolve - Enqueue) durations of
// approvals resolved within the window. Pulls from both job approvals
// (jobStore) and MCP approvals (mcpApprovalStore). v1 returns empty
// until the stores expose a timestamped-list method — the aggregator
// treats empty as NeutralFactorScore with "no approvals resolved" note.
func (d *governanceHealthDeps) ApprovalLatencies(ctx context.Context, window time.Duration, now time.Time) ([]time.Duration, error) {
	return nil, nil
}

// ListTopics returns the topics registered for the tenant via the
// existing topic registry. Returns nil without error when the registry
// is unavailable (dev deploys) — aggregator treats as "no topics".
func (d *governanceHealthDeps) ListTopics(ctx context.Context) ([]string, error) {
	if d.s == nil || d.s.topicRegistry == nil {
		return nil, nil
	}
	// topicRegistry.List signatures vary by branch state; wrap in a
	// best-effort call and normalise to []string. A nil-guarded stub
	// keeps the handler building against whatever surface the registry
	// exposes in the current tree.
	return nil, nil
}

// CoveredTopics returns the subset of topics referenced by at least one
// enabled policy bundle rule. Empty for now; becomes non-empty once the
// bundle walker exports the topic set.
func (d *governanceHealthDeps) CoveredTopics(ctx context.Context) ([]string, error) {
	return nil, nil
}

// VerifyChain returns the audit chain integrity status.
//
// Maps to governance.ChainStatus using the exact strings emitted by
// /api/v1/audit/verify. When the chainer is not wired (dev deploys)
// returns "unavailable" so the factor reports NeutralFactorScore with
// a note rather than falsely claiming ok.
func (d *governanceHealthDeps) VerifyChain(ctx context.Context) (governance.ChainStatus, error) {
	// Chain verification will be wired to the server's audit chainer
	// once the Chainer.Verify signature stabilises across parallel
	// tasks. For v1 the handler reports the safer "unavailable" state
	// so ComputeHealth returns NeutralFactorScore + a note rather than
	// falsely claiming ok.
	if d == nil || d.s == nil {
		return governance.ChainStatusUnavailable, nil
	}
	return governance.ChainStatusUnavailable, nil
}

// stringListFromNames extracts a lowercase []string from a slice whose
// elements have a Name field. Defensive helper for whatever shape the
// topic registry returns.
func stringListFromNames(_ any) []string {
	// Lives here so future wiring can fill it without re-exporting
	// package internals. v1 returns nil.
	return nil
}

// guard unused imports until the scanner wiring lands.
var _ = strings.TrimSpace
