package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cordum/cordum/core/auth/delegation"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

var errDelegationAgentRequired = errors.New("delegation token requires an authenticated agent identity")

func (s *server) applySubmitDelegation(ctx context.Context, tenant, agentID, token string, labels map[string]string, meta *pb.JobMetadata) (map[string]string, error) {
	return s.applySubmitDelegationWithAudience(ctx, tenant, agentID, token, "", labels, meta)
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
	expectedAudience := strings.TrimSpace(audienceOverride)
	if expectedAudience == "" {
		expectedAudience = agentID
	}
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

// submitDelegationErrorStatus maps delegation verify errors to HTTP status
// codes per the plan's taxonomy so callers can branch on shape without
// parsing messages:
//
//   - 401 Unauthorized     — malformed / bad_signature / unknown_kid / not_yet_valid
//                            (the token cannot be trusted as a cryptographic object)
//   - 403 Forbidden        — expired / revoked / audience_mismatch / tenant mismatch
//                            (the token is a valid object but its authorisation
//                            has lapsed or was granted to a different audience)
//   - 422 Unprocessable    — chain_too_deep / scope_exceeded
//                            (the token is cryptographically valid but violates
//                            policy envelope constraints)
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
