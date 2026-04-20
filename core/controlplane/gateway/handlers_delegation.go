package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
)

const delegationIssueLimitPerMinute = 60

type delegateTokenRequest struct {
	TargetAgentID  string   `json:"target_agent_id"`
	AllowedActions []string `json:"allowed_actions,omitempty"`
	AllowedTopics  []string `json:"allowed_topics,omitempty"`
	TTLSeconds     int64    `json:"ttl_seconds,omitempty"`
	ParentToken    string   `json:"parent_token,omitempty"`
}

type delegateTokenResponse struct {
	Token      string `json:"token"`
	KID        string `json:"kid"`
	ExpiresAt  string `json:"expires_at"`
	ChainDepth int    `json:"chain_depth"`
	JTI        string `json:"jti"`
}

type verifyDelegationRequest struct {
	Token            string `json:"token"`
	ExpectedAudience string `json:"expected_audience"`
}

type verifyDelegationResponse struct {
	Valid           bool                   `json:"valid"`
	Sub             string                 `json:"sub,omitempty"`
	Aud             string                 `json:"aud,omitempty"`
	AllowedActions  []string               `json:"allowed_actions,omitempty"`
	AllowedTopics   []string               `json:"allowed_topics,omitempty"`
	ChainDepth      int                    `json:"chain_depth,omitempty"`
	DelegationChain []delegation.ChainLink `json:"delegation_chain,omitempty"`
	ErrorCode       string                 `json:"error_code,omitempty"`
}

type revokeDelegationRequest struct {
	JTI    string `json:"jti"`
	Reason string `json:"reason,omitempty"`
}

type gatewayDelegationPermissionsResolver struct {
	store *store.AgentIdentityStore
}

func (r gatewayDelegationPermissionsResolver) ResolveAgentPermissions(ctx context.Context, agentID string) (delegation.AgentPermissions, error) {
	if r.store == nil {
		return delegation.AgentPermissions{}, fmt.Errorf("agent identity store unavailable")
	}
	identity, err := r.store.Get(ctx, agentID)
	if err != nil {
		return delegation.AgentPermissions{}, err
	}
	if identity == nil {
		return delegation.AgentPermissions{}, fmt.Errorf("agent identity not found")
	}
	return delegation.AgentPermissions{
		AllowedActions: identity.AllowedTools,
		AllowedTopics:  identity.AllowedTopics,
	}, nil
}

func (s *server) handleDelegateAgent(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsDelegate, "admin") {
		s.emitDelegationAudit(r, "issue", tenantFromRequest(r), "", "", "", 0, "denied", errors.New("access denied"))
		return
	}
	authCtx := auth.FromRequest(r)
	if authCtx == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "authentication required")
		return
	}

	delegatingAgentID, ok := requirePathParam(w, r, "id")
	if !ok {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(authCtx.Role), "admin") && strings.TrimSpace(authCtx.PrincipalID) != delegatingAgentID {
		writeForbidden(w, r, errors.New("principal access denied"))
		s.emitDelegationAudit(r, "issue", tenantFromRequest(r), delegatingAgentID, "", "", 0, "denied", errors.New("principal access denied"))
		return
	}

	var req delegateTokenRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	req.TargetAgentID = strings.TrimSpace(req.TargetAgentID)
	if req.TargetAgentID == "" {
		writeErrorJSON(w, http.StatusBadRequest, "target_agent_id required")
		return
	}
	if req.TTLSeconds < 0 {
		writeErrorJSON(w, http.StatusBadRequest, "ttl_seconds must be non-negative")
		return
	}

	tenant := tenantFromRequest(r)
	if tenant == "" {
		writeErrorJSON(w, http.StatusBadRequest, "tenant required")
		return
	}
	if _, ok := s.loadDelegationAgent(w, r, delegatingAgentID, tenant); !ok {
		s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, "", 0, "denied", errors.New("delegating agent unavailable"))
		return
	}
	if _, ok := s.loadDelegationAgent(w, r, req.TargetAgentID, tenant); !ok {
		s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, "", 0, "denied", errors.New("target agent unavailable"))
		return
	}
	if !s.allowDelegationIssue(r.Context(), tenant, delegatingAgentID) {
		writeErrorJSON(w, http.StatusTooManyRequests, "rate limited")
		s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, "", 0, "rate_limited", errors.New("rate limited"))
		return
	}

	service, err := s.delegationTokenService()
	if err != nil {
		writeServiceUnavailable(w, r, "delegation token service", err)
		s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, "", 0, "error", err)
		return
	}

	ttl := time.Duration(req.TTLSeconds) * time.Second
	token, claims, err := service.IssueDelegationToken(r.Context(), delegation.IssueRequest{
		Tenant:            tenant,
		DelegatingAgentID: delegatingAgentID,
		TargetAgentID:     req.TargetAgentID,
		AllowedActions:    req.AllowedActions,
		AllowedTopics:     req.AllowedTopics,
		TTL:               ttl,
		ParentToken:       req.ParentToken,
	})
	if err != nil {
		status := delegationIssueStatus(err)
		if status >= 500 {
			writeInternalError(w, r, "issue delegation token", err)
		} else {
			writeErrorJSON(w, status, delegationIssueMessage(err))
		}
		s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, claims.ID, claims.ChainDepth, "denied", err)
		return
	}

	resp := delegateTokenResponse{
		Token:      token,
		KID:        service.KeyID(),
		ExpiresAt:  claims.ExpiresAt.Time.UTC().Format(time.RFC3339Nano),
		ChainDepth: claims.ChainDepth,
		JTI:        claims.ID,
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, resp)
	s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, claims.ID, claims.ChainDepth, "ok", nil)
}

func (s *server) handleVerifyDelegation(w http.ResponseWriter, r *http.Request) {
	if auth.FromRequest(r) == nil {
		writeErrorJSON(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req verifyDelegationRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	if strings.TrimSpace(req.Token) == "" {
		writeErrorJSON(w, http.StatusBadRequest, "token required")
		return
	}

	tenant := tenantFromRequest(r)
	service, err := s.delegationTokenService()
	if err != nil {
		writeServiceUnavailable(w, r, "delegation token service", err)
		s.emitDelegationAudit(r, "verify", tenant, "", strings.TrimSpace(req.ExpectedAudience), "", 0, "error", err)
		return
	}
	verified, err := service.VerifyDelegationToken(r.Context(), req.Token, strings.TrimSpace(req.ExpectedAudience))
	if err != nil {
		code := delegation.ErrorCode(err)
		if code == "" {
			writeInternalError(w, r, "verify delegation token", err)
			s.emitDelegationAudit(r, "verify", tenant, "", strings.TrimSpace(req.ExpectedAudience), "", 0, "error", err)
			return
		}
		writeJSON(w, verifyDelegationResponse{
			Valid:     false,
			ErrorCode: code,
		})
		s.emitDelegationAudit(r, "verify", tenant, "", strings.TrimSpace(req.ExpectedAudience), "", 0, "denied", err)
		return
	}
	writeJSON(w, verifyDelegationResponse{
		Valid:           true,
		Sub:             verified.Subject,
		Aud:             verified.Audience,
		AllowedActions:  verified.AllowedActions,
		AllowedTopics:   verified.AllowedTopics,
		ChainDepth:      verified.ChainDepth,
		DelegationChain: verified.DelegationChain,
	})
	s.emitDelegationAudit(r, "verify", tenant, verified.Subject, verified.Audience, verified.JTI, verified.ChainDepth, "ok", nil)
}

func (s *server) handleRevokeDelegation(w http.ResponseWriter, r *http.Request) {
	if !s.requirePermissionOrRole(w, r, auth.PermAgentsDelegate, "admin") {
		s.emitDelegationAudit(r, "revoke", tenantFromRequest(r), "", "", "", 0, "denied", errors.New("access denied"))
		return
	}
	var req revokeDelegationRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeJSONDecodeError(w, err, "invalid json")
		return
	}
	req.JTI = strings.TrimSpace(req.JTI)
	if req.JTI == "" {
		writeErrorJSON(w, http.StatusBadRequest, "jti required")
		return
	}
	if s == nil || s.jobStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	revocations := delegation.NewRedisRevocationStoreFromClient(s.jobStore.Client())
	if err := revocations.Revoke(r.Context(), req.JTI, time.Now().UTC().Add(24*time.Hour)); err != nil {
		writeInternalError(w, r, "revoke delegation token", err)
		s.emitDelegationAudit(r, "revoke", tenantFromRequest(r), "", "", req.JTI, 0, "error", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
	s.emitDelegationAudit(r, "revoke", tenantFromRequest(r), "", "", req.JTI, 0, "ok", nil)
}

func (s *server) delegationTokenService() (*delegation.TokenService, error) {
	if s == nil || s.jobStore == nil || s.agentIdentityStore == nil {
		return nil, fmt.Errorf("delegation token service unavailable")
	}
	signingKey, err := delegation.LoadSigningKeyFromEnv()
	if err != nil {
		return nil, err
	}
	keyring, err := delegation.LoadVerificationKeysFromEnv()
	if err != nil {
		return nil, err
	}
	return delegation.NewTokenService(
		signingKey,
		keyring,
		gatewayDelegationPermissionsResolver{store: s.agentIdentityStore},
		delegation.NewRedisRevocationStoreFromClient(s.jobStore.Client()),
	), nil
}

func (s *server) loadDelegationAgent(w http.ResponseWriter, r *http.Request, agentID, tenant string) (*store.AgentIdentity, bool) {
	if s == nil || s.agentIdentityStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
		return nil, false
	}
	identity, err := s.agentIdentityStore.Get(r.Context(), agentID)
	if err != nil {
		writeInternalError(w, r, "load delegation agent", err)
		return nil, false
	}
	if identity == nil {
		writeErrorJSON(w, http.StatusNotFound, "agent identity not found")
		return nil, false
	}
	if tenant != "" && identity.TenantID != tenant {
		writeForbidden(w, r, errors.New("cross-tenant delegation denied"))
		return nil, false
	}
	return identity, true
}

func (s *server) allowDelegationIssue(ctx context.Context, tenant, agentID string) bool {
	if s == nil || s.jobStore == nil || s.jobStore.Client() == nil {
		return true
	}
	key := fmt.Sprintf("delegation:issue:%s:%s:%s", strings.TrimSpace(tenant), strings.TrimSpace(agentID), time.Now().UTC().Format("200601021504"))
	count, err := s.jobStore.Client().Incr(ctx, key).Result()
	if err != nil {
		return false
	}
	if count == 1 {
		_ = s.jobStore.Client().Expire(ctx, key, 2*time.Minute).Err()
	}
	return count <= delegationIssueLimitPerMinute
}

func delegationIssueStatus(err error) int {
	switch {
	case errors.Is(err, delegation.ErrMalformed),
		errors.Is(err, delegation.ErrExpired),
		errors.Is(err, delegation.ErrNotYetValid),
		errors.Is(err, delegation.ErrAudienceMismatch),
		errors.Is(err, delegation.ErrChainTooDeep),
		errors.Is(err, delegation.ErrScopeExceeded),
		errors.Is(err, delegation.ErrRevoked),
		errors.Is(err, delegation.ErrUnknownKeyId),
		errors.Is(err, delegation.ErrBadSignature):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func delegationIssueMessage(err error) string {
	code := delegation.ErrorCode(err)
	if code != "" {
		return code
	}
	return "delegation issue failed"
}

func (s *server) emitDelegationAudit(r *http.Request, action, tenant, agentID, target, jti string, chainDepth int, outcome string, err error) {
	if s == nil || s.auditExporter == nil {
		return
	}
	extra := map[string]string{
		"outcome": outcome,
	}
	if target != "" {
		extra["target"] = target
	}
	if jti != "" {
		extra["jti"] = jti
	}
	if chainDepth > 0 {
		extra["chain_depth"] = strconv.Itoa(chainDepth)
	}
	if code := delegation.ErrorCode(err); code != "" {
		extra["error_code"] = code
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSystemAuth,
		Severity:  delegationAuditSeverity(outcome),
		TenantID:  tenant,
		AgentID:   agentID,
		Action:    "delegation." + action,
		Reason:    delegationAuditReason(action, outcome, err),
		Identity:  policyActorID(r),
		Extra:     extra,
	})
}

func delegationAuditSeverity(outcome string) string {
	if outcome == "ok" {
		return audit.SeverityInfo
	}
	return audit.SeverityMedium
}

func delegationAuditReason(action, outcome string, err error) string {
	if err != nil {
		return err.Error()
	}
	return "delegation " + action + " " + outcome
}
