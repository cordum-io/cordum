package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/model"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var errDelegationAgentRequired = errors.New("delegation token requires an authenticated agent identity")

func (s *server) applySubmitDelegation(ctx context.Context, tenant, agentID, token string, labels map[string]string, meta *pb.JobMetadata) (map[string]string, error) {
	return s.applySubmitDelegationWithAudience(ctx, tenant, agentID, token, "", labels, meta)
}

func submitDelegationExpectedAudience(agentID, audienceOverride string) string {
	expectedAudience := strings.TrimSpace(audienceOverride)
	if expectedAudience == "" {
		expectedAudience = strings.TrimSpace(agentID)
	}
	return expectedAudience
}

// applySubmitDelegationWithAudience allows the caller to pass an
// explicit audience agent id that overrides the submitting-agent
// default. When audienceOverride is empty, the authenticated
// submitting agent id is used (the common case — the token was
// issued TO this caller).
func (s *server) applySubmitDelegationWithAudience(ctx context.Context, tenant, agentID, token, audienceOverride string, labels map[string]string, meta *pb.JobMetadata) (map[string]string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return labels, nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, errDelegationAgentRequired
	}
	expectedAudience := submitDelegationExpectedAudience(agentID, audienceOverride)
	service, err := s.delegationTokenService()
	if err != nil {
		return nil, fmt.Errorf("delegation token service unavailable: %w", err)
	}
	verified, err := service.VerifyDelegationToken(ctx, token, expectedAudience)
	if err != nil {
		return nil, err
	}
	if tenant != "" && verified.Tenant != "" && !strings.EqualFold(verified.Tenant, tenant) {
		return nil, fmt.Errorf("delegation token tenant mismatch")
	}
	delegationCtx := projectVerifiedDelegationContext(verified)
	labels = applyDelegationContextLabels(labels, delegationCtx, verified.Subject)
	if meta != nil {
		if meta.Labels == nil {
			meta.Labels = map[string]string{}
		}
		for key, value := range labels {
			if strings.HasPrefix(key, "_delegation.") && strings.TrimSpace(value) != "" {
				meta.Labels[key] = value
			}
		}
	}
	return labels, nil
}

// persistSubmitDelegationToken stores the raw delegation bearer token
// on the job-metadata hash so the scheduler can re-verify it at
// dispatch time (defense-in-depth against submit→dispatch revocation
// races).
//
// The raw token IS sensitive material — it is wiped as soon as the
// scheduler finishes dispatch verification via the companion call in
// core/controlplane/scheduler/delegation_dispatch.go
// (ClearDelegationDispatchToken). That companion lives in
// split/platform; this branch provides the Clear method on the store
// interface + RedisJobStore implementation, and the scheduler wire-up
// on platform invokes it. After the wipe, only the non-sensitive
// DelegationLineage remains on the job record (JTI, issuer chain,
// scope) for audit + read-side APIs — see Blocker 4 from the #198
// review.
func (s *server) persistSubmitDelegationToken(ctx context.Context, jobID, token, audience string) error {
	token = strings.TrimSpace(token)
	if token == "" || s == nil || s.jobStore == nil {
		return nil
	}
	dispatchStore, ok := any(s.jobStore).(model.DelegationDispatchTokenStore)
	if !ok {
		return fmt.Errorf("delegation dispatch token store unavailable")
	}
	return dispatchStore.SetDelegationDispatchToken(ctx, jobID, model.DelegationDispatchToken{
		Token:    token,
		Audience: strings.TrimSpace(audience),
	})
}

func submitDelegationAuditReason(err error) string {
	if err == nil {
		return ""
	}
	if code := delegation.ErrorCode(err); code != "" {
		return code
	}
	if errors.Is(err, errDelegationAgentRequired) {
		return err.Error()
	}
	if strings.Contains(strings.ToLower(err.Error()), "tenant mismatch") {
		return "delegation token tenant mismatch"
	}
	return "delegation token service unavailable"
}

func (s *server) emitSubmitDelegationRejectedAudit(r *http.Request, jobID, topic, agentID string, err error) {
	if s == nil || s.auditExporter == nil || err == nil {
		return
	}
	extra := map[string]string{}
	if topic = strings.TrimSpace(topic); topic != "" {
		extra["topic"] = topic
	}
	if len(extra) == 0 {
		extra = nil
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventDelegationRejected,
		Severity:  audit.SeverityMedium,
		TenantID:  strings.TrimSpace(tenantFromRequest(r)),
		AgentID:   strings.TrimSpace(agentID),
		JobID:     strings.TrimSpace(jobID),
		Action:    "submit",
		Reason:    submitDelegationAuditReason(err),
		Identity:  strings.TrimSpace(policyActorID(r)),
		Extra:     extra,
	})
}

// submitDelegationErrorStatus maps delegation verify errors to HTTP status
// codes per the plan's taxonomy so callers can branch on shape without
// parsing messages:
//
//   - 401 Unauthorized     — malformed / bad_signature / unknown_kid / not_yet_valid
//     (the token cannot be trusted as a cryptographic object)
//   - 403 Forbidden        — expired / revoked / audience_mismatch / tenant mismatch
//     (the token is a valid object but its authorisation
//     has lapsed or was granted to a different audience)
//   - 422 Unprocessable    — chain_too_deep / scope_exceeded
//     (the token is cryptographically valid but violates
//     policy envelope constraints)
//   - 400 Bad Request      — missing authenticated agent identity
//   - 503 Service Unavail  — delegation service not configured / unreachable
//
// Returns (status, errorCode). The errorCode string is the delegation
// taxonomy keyword (e.g. "expired") so clients can branch on it
// without scraping human-readable messages.
func submitDelegationErrorStatus(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, errDelegationAgentRequired):
		return http.StatusBadRequest, err.Error()
	}
	if code := delegation.ErrorCode(err); code != "" {
		switch code {
		case "malformed", "bad_signature", "unknown_kid", "not_yet_valid":
			return http.StatusUnauthorized, code
		case "expired", "revoked", "audience_mismatch":
			return http.StatusForbidden, code
		case "chain_too_deep", "scope_exceeded":
			return http.StatusUnprocessableEntity, code
		default:
			return http.StatusForbidden, code
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "tenant mismatch") {
		return http.StatusForbidden, "delegation token tenant mismatch"
	}
	return http.StatusServiceUnavailable, "delegation token service unavailable"
}

func writeDelegationSubmitErrorJSON(w http.ResponseWriter, status int, code string) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "delegation token service unavailable"
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]any{
		"error":      code,
		"error_code": code,
		"status":     status,
	})
}
