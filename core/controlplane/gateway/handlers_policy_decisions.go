package gateway

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/policy"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	defaultPolicyDecisionsLimit = 100
	maxPolicyDecisionsLimit     = 500
)

var policyDecisionsQueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_policy_decisions_query_total",
	Help: "Total /api/v1/policy/decisions queries by source/type filter.",
}, []string{"source", "type"})

var policyDecisionsHandlerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "cordum_policy_decisions_handler_latency_seconds",
	Help:    "Latency of /api/v1/policy/decisions handlers.",
	Buckets: prometheus.DefBuckets,
}, []string{"route"})

// policyDecisionsResponse is the JSON shape returned by /api/v1/policy/decisions.
type policyDecisionsResponse struct {
	Items      []policy.Decision `json:"items"`
	HasMore    bool              `json:"has_more"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// policyDecisionBroker exposes the shared in-process broker for tests + WS
// handler. The accessor lazily creates a broker if none was wired by the
// constructor (defensive path; production gateway always installs one).
func (s *server) policyDecisionBroker() *policy.DecisionBroker {
	if s == nil {
		return nil
	}
	if s.policyDecisions == nil {
		s.policyDecisions = policy.NewDecisionBroker()
	}
	return s.policyDecisions
}

// handleListPolicyDecisions serves GET /api/v1/policy/decisions. The handler
// reads the extended DecisionLogRecord and projects the unified policy fields
// onto the response. Legacy verdict/approval fields are NOT exposed on this
// surface — /api/v1/governance/decisions stays as the legacy view.
func (s *server) handleListPolicyDecisions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		policyDecisionsHandlerLatency.WithLabelValues("policy.decisions").Observe(time.Since(start).Seconds())
	}()

	if s.decisionLogStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "decision log store unavailable")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermPolicyRead) {
		return
	}

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	query, sourceLabel, typeLabel, err := parsePolicyDecisionsQuery(r, tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	policyDecisionsQueryTotal.WithLabelValues(sourceLabel, typeLabel).Inc()

	page, err := s.decisionLogStore.QueryDecisions(r.Context(), query)
	if err != nil {
		writeInternalError(w, r, "list policy decisions", err)
		return
	}

	items := make([]policy.Decision, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, decisionFromLogRecord(record))
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, policyDecisionsResponse{
		Items:      items,
		HasMore:    page.NextCursor != "",
		NextCursor: page.NextCursor,
	})
}

// parsePolicyDecisionsQuery validates query params and builds a normalized
// model.DecisionQuery. The label returns are for the prometheus counter.
func parsePolicyDecisionsQuery(r *http.Request, tenant string) (model.DecisionQuery, string, string, error) {
	q := model.DecisionQuery{
		Tenant:  strings.TrimSpace(tenant),
		Topic:   strings.TrimSpace(r.URL.Query().Get("topic")),
		RuleID:  strings.TrimSpace(r.URL.Query().Get("rule_id")),
		AgentID: strings.TrimSpace(r.URL.Query().Get("agent_id")),
		Cursor:  strings.TrimSpace(r.URL.Query().Get("cursor")),
	}

	since, err := parseGovernanceDecisionTime(r.URL.Query().Get("since"))
	if err != nil {
		return model.DecisionQuery{}, "", "", fmt.Errorf("invalid since timestamp")
	}
	q.Since = since

	until, err := parseGovernanceDecisionTime(r.URL.Query().Get("until"))
	if err != nil {
		return model.DecisionQuery{}, "", "", fmt.Errorf("invalid until timestamp")
	}
	q.Until = until

	sourceLabel := "all"
	if raw := strings.TrimSpace(r.URL.Query().Get("source")); raw != "" {
		src, err := policy.ParseDecisionSource(raw)
		if err != nil {
			return model.DecisionQuery{}, "", "", fmt.Errorf("invalid source")
		}
		q.Source = src
		sourceLabel = string(src)
	}

	typeLabel := "all"
	if raw := strings.TrimSpace(r.URL.Query().Get("type")); raw != "" {
		dt, err := policy.ParseDecisionType(raw)
		if err != nil {
			return model.DecisionQuery{}, "", "", fmt.Errorf("invalid type")
		}
		q.Type = dt
		typeLabel = string(dt)
	}

	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return model.DecisionQuery{}, "", "", fmt.Errorf("invalid limit")
		}
		if limit < 0 {
			return model.DecisionQuery{}, "", "", fmt.Errorf("invalid limit")
		}
		q.Limit = limit
	} else if q.Limit == 0 {
		q.Limit = defaultPolicyDecisionsLimit
	}

	normalized, err := q.Normalize(time.Now().UTC())
	if err != nil {
		return model.DecisionQuery{}, "", "", err
	}
	if normalized.Limit > maxPolicyDecisionsLimit {
		return model.DecisionQuery{}, "", "", fmt.Errorf("limit must be <= %d", maxPolicyDecisionsLimit)
	}
	return normalized, sourceLabel, typeLabel, nil
}

// decisionFromLogRecord projects the extended DecisionLogRecord onto a
// unified policy.Decision. Pre-amendment records (with empty unified fields)
// fall back to deriving Type from the legacy Verdict so the response stays
// stable across the migration window. Source defaults to job because
// pre-amendment records were all written by the scheduler.
func decisionFromLogRecord(record model.DecisionLogRecord) policy.Decision {
	source := record.Source
	if source == "" {
		source = policy.DecisionSourceJob
	}
	dt := record.Type
	if dt == "" {
		dt = legacyVerdictToDecisionType(record.Verdict)
	}
	timestamp := time.UnixMilli(record.Timestamp).UTC()
	trace := record.Trace
	if len(trace) == 0 && dt != "" {
		trace = []policy.TraceStep{{
			RuleID:       strings.TrimSpace(record.RuleID),
			BundleID:     record.BundleID,
			DecisionType: dt,
			Reason:       strings.TrimSpace(record.Reason),
			Timestamp:    timestamp,
		}}
	}
	return policy.Decision{
		Source:        source,
		RuleID:        strings.TrimSpace(record.RuleID),
		BundleID:      record.BundleID,
		BundleVersion: record.BundleVersion,
		Type:          dt,
		Trace:         trace,
		InputRef:      record.InputRef,
		OutputRef:     record.OutputRef,
		AuditHash:     record.AuditHash,
		Timestamp:     timestamp,
	}
}

// legacyVerdictToDecisionType bridges pre-amendment records that have only
// the legacy Verdict field. Empty input yields empty output so callers can
// detect "no decision recorded" without a panic.
func legacyVerdictToDecisionType(v model.SafetyDecision) policy.DecisionType {
	switch v {
	case model.SafetyAllow:
		return policy.DecisionAllow
	case model.SafetyDeny:
		return policy.DecisionDeny
	case model.SafetyAllowWithConstraints:
		return policy.DecisionAllowWithConstraints
	case model.SafetyRequireApproval:
		return policy.DecisionRequireHuman
	case model.SafetyThrottle:
		return policy.DecisionThrottle
	default:
		return ""
	}
}

