package gateway

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var governanceDecisionsQueryTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "cordum_governance_decisions_query_total",
	Help: "Total governance decision-log queries by verdict filter.",
}, []string{"verdict"})

var governanceDecisionsHandlerLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "cordum_governance_decisions_handler_latency_seconds",
	Help:    "Latency of governance decision-log handlers.",
	Buckets: prometheus.DefBuckets,
}, []string{"route"})

type governanceDecisionView struct {
	JobID            string                 `json:"job_id"`
	Topic            string                 `json:"topic"`
	MatchedRule      string                 `json:"matched_rule,omitempty"`
	Verdict          string                 `json:"verdict"`
	Reason           string                 `json:"reason,omitempty"`
	Constraints      *pb.PolicyConstraints  `json:"constraints,omitempty"`
	ApprovalStatus   model.ApprovalStatus   `json:"approval_status,omitempty"`
	ApprovalDecision model.ApprovalDecision `json:"approval_decision,omitempty"`
	AgentID          string                 `json:"agent_id,omitempty"`
	PolicyVersion    string                 `json:"policy_version,omitempty"`
	Timestamp        string                 `json:"timestamp"`
}

type governanceDecisionsResponse struct {
	Items      []governanceDecisionView `json:"items"`
	NextCursor string                   `json:"next_cursor,omitempty"`
}

func (s *server) handleListGovernanceDecisions(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		governanceDecisionsHandlerLatency.WithLabelValues("governance.decisions").Observe(time.Since(start).Seconds())
	}()

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

	query, verdictLabel, err := parseGovernanceDecisionQuery(r, tenant)
	if err != nil {
		writeErrorJSON(w, http.StatusBadRequest, err.Error())
		return
	}
	governanceDecisionsQueryTotal.WithLabelValues(verdictLabel).Inc()

	page, err := s.decisionLogStore.QueryDecisions(r.Context(), query)
	if err != nil {
		writeInternalError(w, r, "list governance decisions", err)
		return
	}

	items := make([]governanceDecisionView, 0, len(page.Items))
	for _, record := range page.Items {
		verdict, err := record.Verdict.DecisionLogWireValue()
		if err != nil {
			writeInternalError(w, r, "encode governance decision verdict", err)
			return
		}
		items = append(items, governanceDecisionView{
			JobID:            record.JobID,
			Topic:            record.Topic,
			MatchedRule:      record.RuleID,
			Verdict:          verdict,
			Reason:           record.Reason,
			Constraints:      record.Constraints,
			ApprovalStatus:   record.ApprovalStatus,
			ApprovalDecision: record.ApprovalDecision,
			AgentID:          record.AgentID,
			PolicyVersion:    record.PolicyVersion,
			Timestamp:        governanceTimestamp(record.Timestamp),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, governanceDecisionsResponse{Items: items, NextCursor: page.NextCursor})
}

func parseGovernanceDecisionQuery(r *http.Request, tenant string) (model.DecisionQuery, string, error) {
	query := model.DecisionQuery{
		Tenant:  strings.TrimSpace(tenant),
		Topic:   strings.TrimSpace(r.URL.Query().Get("topic")),
		RuleID:  strings.TrimSpace(r.URL.Query().Get("rule_id")),
		AgentID: strings.TrimSpace(r.URL.Query().Get("agent_id")),
		Cursor:  strings.TrimSpace(r.URL.Query().Get("cursor")),
	}

	since, err := parseGovernanceDecisionTime(r.URL.Query().Get("since"))
	if err != nil {
		return model.DecisionQuery{}, "", fmt.Errorf("invalid since timestamp")
	}
	query.Since = since

	until, err := parseGovernanceDecisionTime(r.URL.Query().Get("until"))
	if err != nil {
		return model.DecisionQuery{}, "", fmt.Errorf("invalid until timestamp")
	}
	query.Until = until

	verdictLabel := "all"
	rawVerdict := strings.TrimSpace(r.URL.Query().Get("verdict"))
	if rawVerdict != "" {
		verdict, err := model.ParseDecisionLogVerdict(rawVerdict)
		if err != nil {
			return model.DecisionQuery{}, "", fmt.Errorf("invalid verdict")
		}
		query.Verdict = verdict
		verdictLabel = strings.ToLower(rawVerdict)
	}

	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return model.DecisionQuery{}, "", fmt.Errorf("invalid limit")
		}
		if limit < 0 {
			return model.DecisionQuery{}, "", fmt.Errorf("invalid limit")
		}
		query.Limit = limit
	}

	normalized, err := query.Normalize(time.Now().UTC())
	if err != nil {
		return model.DecisionQuery{}, "", err
	}
	if normalized.Limit > model.MaxDecisionQueryLimit {
		return model.DecisionQuery{}, "", fmt.Errorf("limit must be <= %d", model.MaxDecisionQueryLimit)
	}
	return normalized, verdictLabel, nil
}

func parseGovernanceDecisionTime(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if value <= 0 {
			return 0, fmt.Errorf("timestamp must be positive")
		}
		return value, nil
	}
	for _, format := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(format, raw); err == nil {
			return parsed.UTC().UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("invalid timestamp")
}

func governanceTimestamp(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.UnixMilli(ts).UTC().Format(time.RFC3339)
}
