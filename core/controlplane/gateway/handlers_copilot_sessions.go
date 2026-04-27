package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/model"
)

const copilotSessionAggregateLimit = 500

var copilotSessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type copilotSessionDetailResponse struct {
	Session   copilot.CopilotSession       `json:"session"`
	Jobs      []copilotSessionJobView      `json:"jobs"`
	Decisions []copilotSessionDecisionView `json:"decisions"`
	Truncated bool                         `json:"truncated"`
}

type copilotSessionJobView struct {
	ID           string            `json:"id"`
	Type         string            `json:"type,omitempty"`
	Topic        string            `json:"topic,omitempty"`
	Status       string            `json:"status"`
	Pool         string            `json:"pool,omitempty"`
	Capabilities []string          `json:"capabilities"`
	RiskTags     []string          `json:"riskTags"`
	Metadata     map[string]string `json:"metadata"`
	CreatedAt    string            `json:"createdAt,omitempty"`
	UpdatedAt    string            `json:"updatedAt,omitempty"`
}

type copilotSessionDecisionView struct {
	JobID            string                 `json:"jobId"`
	Topic            string                 `json:"topic,omitempty"`
	MatchedRule      string                 `json:"matchedRule,omitempty"`
	RuleName         string                 `json:"ruleName,omitempty"`
	Verdict          string                 `json:"verdict"`
	Reason           string                 `json:"reason,omitempty"`
	Constraints      any                    `json:"constraints,omitempty"`
	ApprovalStatus   model.ApprovalStatus   `json:"approvalStatus,omitempty"`
	ApprovalDecision model.ApprovalDecision `json:"approvalDecision,omitempty"`
	AgentID          string                 `json:"agentId,omitempty"`
	PolicyVersion    string                 `json:"policyVersion,omitempty"`
	Timestamp        string                 `json:"timestamp"`
}

func (s *server) handleGetCopilotSession(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	sessionID := strings.TrimSpace(r.PathValue("sessionId"))
	if !copilotSessionIDPattern.MatchString(sessionID) {
		writeErrorJSON(w, http.StatusBadRequest, "invalid session id")
		return
	}

	authCtx := auth.FromRequest(r)
	if authCtx == nil || strings.TrimSpace(authCtx.PrincipalID) == "" {
		writeErrorJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !s.requirePermissionOrRole(w, r, auth.PermJobsRead) {
		return
	}
	// Custom RBAC roles can grant jobs.read without governance.read. Without a
	// separate gate the response would leak DecisionLogRecord data behind only
	// the jobs.read permission, bypassing the existing /api/v1/governance/decisions
	// gate (auth.PermGovernanceRead). Compute the gate once here; jobs.read-only
	// callers still get the timeline + linked jobs but with decisions:[].
	canReadGovernance := s.hasPermissionSilent(r, auth.PermGovernanceRead)

	tenant, err := s.resolveTenant(r, "")
	if err != nil {
		writeErrorJSON(w, http.StatusForbidden, "tenant access denied")
		return
	}

	userID := strings.TrimSpace(authCtx.PrincipalID)
	slog.Info("copilot session detail start",
		"session_id", sessionID,
		"principal", copilotLogPrincipal(userID),
		"tenant", tenant,
		"governance_read", canReadGovernance,
	)

	store := s.copilotStore
	if store == nil {
		store = copilot.NotImplementedStore{}
	}
	sess, err := store.GetSession(r.Context(), sessionID, userID)
	if err != nil {
		s.handleCopilotSessionStoreError(w, r, sessionID, userID, tenant, started, err)
		return
	}
	if sess == nil {
		s.handleCopilotSessionStoreError(w, r, sessionID, userID, tenant, started, copilot.ErrNotFound)
		return
	}

	jobs, jobIDs, jobsTruncated, err := s.collectCopilotSessionJobs(r.Context(), sessionID, tenant, sess)
	if err != nil {
		writeInternalError(w, r, "get copilot session jobs", err)
		return
	}
	var (
		decisions          []copilotSessionDecisionView
		decisionsTruncated bool
	)
	if canReadGovernance {
		decisions, decisionsTruncated, err = s.collectCopilotSessionDecisions(r.Context(), tenant, jobIDs, sess.CreatedAt)
		if err != nil {
			writeInternalError(w, r, "get copilot session decisions", err)
			return
		}
	} else {
		decisions = []copilotSessionDecisionView{}
		slog.Info("copilot session decisions omitted",
			"session_id", sessionID,
			"principal", copilotLogPrincipal(userID),
			"tenant", tenant,
			"reason", "missing governance.read",
		)
	}
	truncated := jobsTruncated || decisionsTruncated
	if truncated {
		slog.Warn("copilot session detail truncated",
			"session_id", sessionID,
			"principal", copilotLogPrincipal(userID),
			"tenant", tenant,
			"limit", copilotSessionAggregateLimit,
		)
	}

	writeJSON(w, copilotSessionDetailResponse{
		Session:   *sess,
		Jobs:      jobs,
		Decisions: decisions,
		Truncated: truncated,
	})
	slog.Info("copilot session detail end",
		"session_id", sessionID,
		"principal", copilotLogPrincipal(userID),
		"tenant", tenant,
		"latency_ms", time.Since(started).Milliseconds(),
	)
}

func (s *server) handleCopilotSessionStoreError(w http.ResponseWriter, r *http.Request, sessionID, userID, tenant string, started time.Time, err error) {
	slog.Info("copilot session detail end",
		"session_id", sessionID,
		"principal", copilotLogPrincipal(userID),
		"tenant", tenant,
		"latency_ms", time.Since(started).Milliseconds(),
		"error", err,
	)
	switch {
	case errors.Is(err, copilot.ErrNotFound):
		writeErrorJSON(w, http.StatusNotFound, "copilot session not found")
	case errors.Is(err, copilot.ErrCrossTenant):
		writeErrorJSON(w, http.StatusForbidden, "access denied")
	case errors.Is(err, copilot.ErrNotImplemented):
		writeErrorJSON(w, http.StatusNotImplemented, "copilot_store_not_ready")
	default:
		writeInternalError(w, r, "get copilot session", err)
	}
}

func (s *server) collectCopilotSessionJobs(ctx context.Context, sessionID, tenant string, sess *copilot.CopilotSession) ([]copilotSessionJobView, map[string]struct{}, bool, error) {
	jobIDs := orderedCopilotSessionJobIDs(sess)
	jobSet := make(map[string]struct{}, len(jobIDs))
	for _, id := range jobIDs {
		jobSet[id] = struct{}{}
	}

	if s.jobStore == nil {
		return []copilotSessionJobView{}, jobSet, false, nil
	}

	recent, err := s.jobStore.ListRecentJobs(ctx, copilotSessionAggregateLimit)
	if err != nil {
		return nil, nil, false, err
	}
	for _, record := range recent {
		if record.ID == "" || record.Tenant != tenant {
			continue
		}
		if _, ok := jobSet[record.ID]; ok {
			continue
		}
		meta, err := s.jobStore.GetAllMeta(ctx, record.ID)
		if err != nil || len(meta) == 0 {
			continue
		}
		labels := parseCopilotLabels(meta["labels"])
		if labels["session_id"] == sessionID || labels["copilot_session_id"] == sessionID {
			jobSet[record.ID] = struct{}{}
			jobIDs = append(jobIDs, record.ID)
		}
	}

	truncated := false
	if len(jobIDs) > copilotSessionAggregateLimit {
		jobIDs = jobIDs[:copilotSessionAggregateLimit]
		truncated = true
	}

	metas, err := s.jobStore.GetJobMetas(ctx, jobIDs)
	if err != nil {
		return nil, nil, false, err
	}

	jobs := make([]copilotSessionJobView, 0, len(jobIDs))
	// decisionJobSet must include every job id the session references, even
	// those whose enriched metadata has expired or been evicted from the job
	// store. Without that, governance decisions for missing-meta jobs are
	// silently dropped from the response. Cross-tenant jobs are still
	// excluded — both for security and because QueryDecisions filters by
	// tenant, so they cannot match anyway.
	decisionJobSet := make(map[string]struct{}, len(jobIDs))
	for _, id := range jobIDs {
		meta := metas[id]
		if tenant != "" && meta["tenant"] != "" && meta["tenant"] != tenant {
			continue
		}
		decisionJobSet[id] = struct{}{}
		if len(meta) == 0 {
			slog.Warn("copilot session job reference missing",
				"session_id", sessionID,
				"tenant", tenant,
				"job_id", id,
			)
			continue
		}
		jobs = append(jobs, copilotJobViewFromMeta(id, meta))
	}
	return jobs, decisionJobSet, truncated, nil
}

func (s *server) collectCopilotSessionDecisions(ctx context.Context, tenant string, jobIDs map[string]struct{}, sessionStartedAt time.Time) ([]copilotSessionDecisionView, bool, error) {
	if s.decisionLogStore == nil || len(jobIDs) == 0 {
		return []copilotSessionDecisionView{}, false, nil
	}

	// Bound the decision-log scan to the session's lifetime, with a 7-day
	// minimum lookback for sessions that started within the last week. A
	// `Since: 1` (epoch) query otherwise scans every historical decision
	// for the tenant, which is unnecessary work and a latency tail risk on
	// tenants with extensive decision history.
	now := time.Now().UTC()
	weekAgoMillis := now.Add(-7 * 24 * time.Hour).UnixMilli()
	sinceMillis := weekAgoMillis
	if !sessionStartedAt.IsZero() {
		startedMillis := sessionStartedAt.UTC().UnixMilli()
		if startedMillis < sinceMillis {
			sinceMillis = startedMillis
		}
	}
	if sinceMillis < 1 {
		sinceMillis = 1
	}
	var (
		decisions []copilotSessionDecisionView
		cursor    string
		until     = now.Add(24 * time.Hour).UnixMilli()
	)
	for {
		page, err := s.decisionLogStore.QueryDecisions(ctx, model.DecisionQuery{
			Tenant: tenant,
			Since:  sinceMillis,
			Until:  until,
			Limit:  copilotSessionAggregateLimit,
			Cursor: cursor,
		})
		if err != nil {
			return nil, false, err
		}

		for _, record := range page.Items {
			if _, ok := jobIDs[record.JobID]; !ok {
				continue
			}
			view, err := copilotDecisionViewFromRecord(record)
			if err != nil {
				return nil, false, err
			}
			decisions = append(decisions, view)
			if len(decisions) == copilotSessionAggregateLimit {
				return decisions, true, nil
			}
		}
		if page.NextCursor == "" {
			return decisions, false, nil
		}
		cursor = page.NextCursor
	}
}

func orderedCopilotSessionJobIDs(sess *copilot.CopilotSession) []string {
	if sess == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, msg := range sess.Messages {
		for _, id := range msg.JobIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func copilotJobViewFromMeta(id string, meta map[string]string) copilotSessionJobView {
	topic := meta["topic"]
	// Pool is its own metadata key; falling back to topic preserves the
	// previous behavior for jobs whose meta predates the pool field, but
	// real pool metadata must take precedence when present.
	pool := strings.TrimSpace(meta["pool"])
	if pool == "" {
		pool = topic
	}
	labels := parseCopilotLabels(meta["labels"])
	status := strings.ToLower(strings.TrimSpace(meta["state"]))
	if status == "" {
		status = "unknown"
	}
	capabilities := []string{}
	if capability := strings.TrimSpace(meta["capability"]); capability != "" {
		capabilities = append(capabilities, capability)
	}
	return copilotSessionJobView{
		ID:           id,
		Type:         topic,
		Topic:        topic,
		Status:       status,
		Pool:         pool,
		Capabilities: capabilities,
		RiskTags:     parseCopilotStringSlice(meta["risk_tags"]),
		Metadata:     labels,
		CreatedAt:    formatCopilotUnixMicros(meta["created_at"]),
		UpdatedAt:    formatCopilotUnixMicros(meta["updated_at"]),
	}
}

func parseCopilotLabels(raw string) map[string]string {
	labels := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return labels
	}
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return map[string]string{}
	}
	return labels
}

func parseCopilotStringSlice(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return []string{}
	}
	return values
}

func formatCopilotUnixMicros(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	micros, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || micros <= 0 {
		return ""
	}
	return time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
}

func copilotLogPrincipal(principal string) string {
	principal = strings.TrimSpace(principal)
	if len(principal) <= 8 {
		return principal
	}
	return principal[:8]
}

func copilotDecisionViewFromRecord(record model.DecisionLogRecord) (copilotSessionDecisionView, error) {
	verdict, err := record.Verdict.DecisionLogWireValue()
	if err != nil {
		return copilotSessionDecisionView{}, err
	}
	return copilotSessionDecisionView{
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
	}, nil
}
