package gateway

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/governance"
	"github.com/cordum/cordum/core/infra/config"
	"github.com/cordum/cordum/core/model"
)

func (s *server) handleGovernanceHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.requireRole(r, "admin"); err != nil {
		writeForbidden(w, r, err)
		return
	}

	tenant, err := s.resolveTenant(r, strings.TrimSpace(r.URL.Query().Get("tenant")))
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	cache := (*governance.Cache)(nil)
	if s != nil {
		cache = s.governanceHealthCache
	}
	if cache == nil {
		cache = governance.NewCache(60 * time.Second)
	}

	score, err := governance.ComputeHealth(r.Context(), newGovernanceHealthDeps(s, tenant), cache)
	if err != nil {
		writeInternalError(w, r, "governance health", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, score)
}

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

func (d *governanceHealthDeps) ScanDecisions(ctx context.Context, window time.Duration, now time.Time) (governance.DecisionCounts, error) {
	if d == nil || d.s == nil || d.s.decisionLogStore == nil {
		return governance.DecisionCounts{}, nil
	}

	query := model.DecisionQuery{
		Tenant: d.tenant,
		Since:  now.Add(-window).UnixMilli(),
		Until:  now.UnixMilli(),
		Limit:  model.MaxDecisionQueryLimit,
	}
	query, err := query.Normalize(now)
	if err != nil {
		return governance.DecisionCounts{}, err
	}

	page, err := d.s.decisionLogStore.QueryDecisions(ctx, query)
	if err != nil {
		return governance.DecisionCounts{}, err
	}

	counts := governance.DecisionCounts{
		ScannedEvents: len(page.Items),
		Truncated:     strings.TrimSpace(page.NextCursor) != "",
	}
	for _, item := range page.Items {
		switch item.Verdict {
		case model.SafetyAllow:
			counts.Allow++
		case model.SafetyDeny:
			counts.Deny++
		case model.SafetyRequireApproval:
			counts.RequireApproval++
		default:
			counts.Other++
		}
	}
	return counts, nil
}

func (d *governanceHealthDeps) ApprovalLatencies(ctx context.Context, window time.Duration, now time.Time) ([]time.Duration, bool, error) {
	if d == nil || d.s == nil || d.s.decisionLogStore == nil || d.s.jobStore == nil {
		return nil, false, nil
	}

	query := model.DecisionQuery{
		Tenant:  d.tenant,
		Since:   now.Add(-window).UnixMilli(),
		Until:   now.UnixMilli(),
		Verdict: model.SafetyRequireApproval,
		Limit:   model.MaxDecisionQueryLimit,
	}
	query, err := query.Normalize(now)
	if err != nil {
		return nil, false, err
	}

	page, err := d.s.decisionLogStore.QueryDecisions(ctx, query)
	if err != nil {
		return nil, false, err
	}
	truncated := strings.TrimSpace(page.NextCursor) != ""

	samples := make([]time.Duration, 0, len(page.Items))
	for _, item := range page.Items {
		if item.JobID == "" || item.Timestamp <= 0 {
			continue
		}
		record, err := d.s.jobStore.GetApprovalRecord(ctx, item.JobID)
		if err != nil {
			return nil, truncated, fmt.Errorf("approval latency lookup %s: %w", item.JobID, err)
		}
		// Missing or still-pending approvals have no terminal resolution
		// timestamp yet. They should not make the health endpoint unavailable;
		// real store lookup errors above do. ApprovedAt is the legacy
		// resolution timestamp field for both approved and rejected approvals.
		resolvedAt, resolved := approvalResolutionTimestamp(record)
		if !resolved || resolvedAt <= item.Timestamp {
			continue
		}
		samples = append(samples, time.Duration(resolvedAt-item.Timestamp)*time.Millisecond)
	}
	return samples, truncated, nil
}

func approvalResolutionTimestamp(record model.ApprovalRecord) (int64, bool) {
	if record.ApprovedAt <= 0 {
		return 0, false
	}
	switch record.Status {
	case "", model.ApprovalStatusApproved, model.ApprovalStatusRejected, model.ApprovalStatusExpired, model.ApprovalStatusInvalidated, model.ApprovalStatusRepaired:
		return record.ApprovedAt, true
	default:
		return 0, false
	}
}

func (d *governanceHealthDeps) ListTopics(ctx context.Context) ([]string, error) {
	if d == nil || d.s == nil || d.s.topicRegistry == nil {
		return nil, nil
	}
	snap, err := d.s.topicRegistry.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(snap.Items))
	for _, item := range snap.Items {
		if name := strings.TrimSpace(item.Name); name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (d *governanceHealthDeps) CoveredTopics(ctx context.Context) ([]string, error) {
	if d == nil || d.s == nil || d.s.configSvc == nil {
		return nil, nil
	}

	bundles, _, err := d.s.loadPolicyBundles(ctx)
	if err != nil {
		return nil, err
	}

	covered := map[string]struct{}{}
	for _, raw := range bundles {
		content, ok := policybundles.PolicyBundleContent(raw)
		if !ok || strings.TrimSpace(content) == "" {
			continue
		}
		policy, err := config.ParseSafetyPolicy([]byte(policybundles.SanitizePolicyBundleYAML(content)))
		if err != nil || policy == nil {
			continue
		}
		for _, rule := range policy.EffectiveRules() {
			for _, topic := range rule.Match.Topics {
				topic = strings.TrimSpace(topic)
				if topic != "" {
					covered[topic] = struct{}{}
				}
			}
		}
	}

	out := make([]string, 0, len(covered))
	for topic := range covered {
		out = append(out, topic)
	}
	sort.Strings(out)
	return out, nil
}

func (d *governanceHealthDeps) VerifyChain(ctx context.Context) (governance.ChainStatus, error) {
	if d == nil || d.s == nil {
		return governance.ChainStatusUnavailable, nil
	}

	client := d.s.redisClient()
	if client == nil {
		return governance.ChainStatusUnavailable, nil
	}

	streamKey := audit.NewChainer(client, "").StreamKey(d.tenant)
	boundary, err := readRetentionBoundary(ctx, client, streamKey)
	if err != nil {
		return governance.ChainStatusUnavailable, err
	}

	opts := audit.VerifyOptions{
		Limit:                audit.DefaultVerifyLimit,
		RetentionBoundarySeq: boundary,
	}
	// Verify HMAC tags when HMAC is enabled; the key comes only from the
	// server's configured chainer (never request input). Mirrors
	// /api/v1/audit/verify and the compliance-export fix (task-33485ac3).
	if d.s.auditChainer != nil && d.s.auditChainer.HMACEnabled() {
		opts.HMACKey = d.s.auditChainer.HMACKeyForVerify()
	}

	result, err := audit.VerifyChain(ctx, client, streamKey, opts)
	if err != nil {
		return governance.ChainStatusUnavailable, err
	}

	// Fail-closed: HMAC tags seen but no key configured means HMAC was not
	// verified while Status stayed ok — downgrade ok->partial so the
	// governance health surface is not a false-green.
	if len(opts.HMACKey) == 0 && result.HMACSeen && result.Status == audit.VerifyStatusOK {
		result.Status = audit.VerifyStatusPartial
	}

	switch result.Status {
	case audit.VerifyStatusOK:
		return governance.ChainStatusOK, nil
	case audit.VerifyStatusPartial:
		return governance.ChainStatusPartial, nil
	case audit.VerifyStatusCompromised:
		return governance.ChainStatusCompromised, nil
	default:
		return governance.ChainStatusUnavailable, nil
	}
}
