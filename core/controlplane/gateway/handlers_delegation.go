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

// maxDelegationTTLSeconds caps `ttl_seconds` on the delegation-issue handler
// before it is multiplied into time.Duration. Without this bound a caller
// could submit `ttl_seconds=math.MaxInt64` and wrap the `int64 * time.Second`
// multiplication past the int64 nanosecond range — the resulting negative
// duration would silently bypass the service-layer `maxTTL` check (which
// tests `ttl > maxTTL`) and mint an already-expired-or-far-future token. We
// cap at one year here; the service layer's configured maxTTL still applies
// and will reject anything smaller than that bound with its own 400 error.
const maxDelegationTTLSeconds int64 = 365 * 24 * 60 * 60

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
	JTI     string `json:"jti"`
	Reason  string `json:"reason,omitempty"`
	Cascade *bool  `json:"cascade,omitempty"`
}

type revokeDelegationResponse struct {
	JTI           string `json:"jti"`
	CascadedCount int    `json:"cascaded_count"`
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
	// Bind the caller's authoritative agent from the authenticated worker
	// credential, not from authCtx.PrincipalID (which is the worker ID and
	// has a distinct namespace from agent IDs in this codebase). Admins
	// bypass the per-agent check; everyone else must prove they are the
	// delegating agent via their linked credential.
	if !strings.EqualFold(strings.TrimSpace(authCtx.Role), "admin") {
		callerAgentID := ""
		if s.agentIdentityStore != nil {
			if agent, err := s.agentIdentityStore.GetByWorkerID(r.Context(), strings.TrimSpace(authCtx.PrincipalID)); err == nil && agent != nil {
				callerAgentID = agent.ID
			}
		}
		if callerAgentID == "" || callerAgentID != delegatingAgentID {
			writeForbidden(w, r, errors.New("principal access denied"))
			s.emitDelegationAudit(r, "issue", tenantFromRequest(r), delegatingAgentID, "", "", 0, "denied", errors.New("principal access denied"))
			return
		}
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
	if req.TTLSeconds > maxDelegationTTLSeconds {
		// Enforce the pre-multiplication bound so `time.Duration(foo) *
		// time.Second` cannot overflow int64 nanoseconds and return a
		// negative duration that would sneak past the service-layer
		// maxTTL guard.
		writeErrorJSON(w, http.StatusBadRequest, "ttl_seconds exceeds maximum (1 year)")
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
	if allowed, quotaErr := s.allowDelegationIssue(r.Context(), tenant, delegatingAgentID); !allowed {
		if quotaErr != nil {
			// Rate-limit backend is broken — surface as 503 so operators
			// see the actual failure class instead of a misleading 429.
			writeServiceUnavailable(w, r, "delegation rate limiter", quotaErr)
			s.emitDelegationAudit(r, "issue", tenant, delegatingAgentID, req.TargetAgentID, "", 0, "error", quotaErr)
			return
		}
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
	if listStore := s.delegationListStore(); listStore != nil {
		if err := listStore.RecordIssuedToken(r.Context(), delegationIssuedView(tenant, claims, req.TargetAgentID)); err != nil {
			writeInternalError(w, r, "record delegation token", err)
			return
		}
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
	tenant := tenantFromRequest(r)
	listStore := s.delegationListStore()
	if listStore == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	rootView, ok, err := listStore.Get(r.Context(), req.JTI)
	if err != nil {
		writeInternalError(w, r, "load delegation token", err)
		s.emitDelegationAudit(r, "revoke", tenant, "", "", req.JTI, 0, "error", err)
		return
	}
	if !ok || !strings.EqualFold(strings.TrimSpace(rootView.Tenant), tenant) {
		writeErrorJSON(w, http.StatusNotFound, "delegation token not found")
		s.emitDelegationAudit(r, "revoke", tenant, "", "", req.JTI, 0, "denied", delegation.ErrNotFound)
		return
	}
	revocations := delegation.NewRedisRevocationStoreFromClient(s.jobStore.Client())
	revokedAt := time.Now().UTC()
	cascade := req.Cascade == nil || *req.Cascade
	result, err := revocations.CascadeRevoke(r.Context(), req.JTI, strings.TrimSpace(req.Reason), revokedAt, cascade)
	if err != nil {
		switch {
		case errors.Is(err, delegation.ErrNotFound):
			writeErrorJSON(w, http.StatusNotFound, "delegation token not found")
		case errors.Is(err, delegation.ErrCascadeTooDeep):
			writeErrorJSON(w, http.StatusUnprocessableEntity, "delegation cascade too deep")
		default:
			writeInternalError(w, r, "revoke delegation token", err)
		}
		s.emitDelegationAudit(r, "revoke", tenant, "", "", req.JTI, rootView.ChainDepth, "error", err)
		return
	}
	writeJSON(w, revokeDelegationResponse{
		JTI:           req.JTI,
		CascadedCount: result.CascadedCount,
	})
	s.emitDelegationAudit(r, "revoke", tenant, "", "", req.JTI, rootView.ChainDepth, "ok", nil)
	if result.CascadedCount > 0 {
		s.emitDelegationCascadeAudit(r, tenant, req.JTI, result.CascadedCount, strings.TrimSpace(req.Reason))
	}
	for _, revokedJTI := range result.RevokedJTIs {
		if revokedJTI == req.JTI {
			continue
		}
		s.emitDelegationRevokedAudit(r, tenant, req.JTI, revokedJTI, strings.TrimSpace(req.Reason))
	}
}

func (s *server) emitDelegationCascadeAudit(r *http.Request, tenant, rootJTI string, cascadedCount int, reason string) {
	if s == nil || s.auditExporter == nil || cascadedCount <= 0 {
		return
	}
	extra := map[string]string{
		"root_jti":       strings.TrimSpace(rootJTI),
		"cascaded_count": strconv.Itoa(cascadedCount),
	}
	if trim := strings.TrimSpace(reason); trim != "" {
		extra["reason"] = trim
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSystemAuth,
		Severity:  audit.SeverityInfo,
		TenantID:  tenant,
		Action:    "delegation.revoked_cascade",
		Reason:    "delegation revoke cascaded",
		Identity:  policyActorID(r),
		Extra:     extra,
	})
}

func (s *server) emitDelegationRevokedAudit(r *http.Request, tenant, rootJTI, jti, reason string) {
	if s == nil || s.auditExporter == nil {
		return
	}
	extra := map[string]string{
		"jti":      strings.TrimSpace(jti),
		"root_jti": strings.TrimSpace(rootJTI),
		"outcome":  "ok",
	}
	if trim := strings.TrimSpace(reason); trim != "" {
		extra["reason"] = trim
	}
	s.auditExporter.Send(audit.SIEMEvent{
		Timestamp: time.Now().UTC(),
		EventType: audit.EventSystemAuth,
		Severity:  audit.SeverityInfo,
		TenantID:  tenant,
		Action:    "delegation.revoked",
		Reason:    "delegation revoked",
		Identity:  policyActorID(r),
		Extra:     extra,
	})
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
	// Tenant scoping: store.AgentIdentity does not currently carry a
	// tenant field — agent IDs live in a flat `agent:identity:<id>`
	// keyspace. Cross-tenant isolation for this endpoint relies on the
	// upstream tenant middleware (tenantFromRequest + auth context)
	// already pinning the caller to their tenant. Adding per-agent
	// tenant binding to the store is tracked separately; until it
	// lands, this helper treats the tenant argument as advisory.
	_ = tenant
	return identity, true
}

// allowDelegationIssue returns (allowed, err). allowed=true means the
// caller is under the per-minute issuance quota. allowed=false means
// either the quota is exhausted (err==nil — caller should 429) or the
// rate-limit backend itself is unavailable (err!=nil — caller should
// 503). Splitting the two lets operators distinguish a noisy client
// from a Redis outage in logs + responses; previously both collapsed
// to a single 429 which masked infrastructure failures.
func (s *server) allowDelegationIssue(ctx context.Context, tenant, agentID string) (bool, error) {
	if s == nil || s.jobStore == nil || s.jobStore.Client() == nil {
		return true, nil
	}
	key := fmt.Sprintf("delegation:issue:%s:%s:%s", strings.TrimSpace(tenant), strings.TrimSpace(agentID), time.Now().UTC().Format("200601021504"))
	count, err := s.jobStore.Client().Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("delegation rate limiter unavailable: %w", err)
	}
	if count == 1 {
		_ = s.jobStore.Client().Expire(ctx, key, 2*time.Minute).Err()
	}
	return count <= delegationIssueLimitPerMinute, nil
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

func (s *server) delegationListStore() *delegation.RedisListStore {
	if s == nil || s.jobStore == nil {
		return nil
	}
	return delegation.NewRedisListStoreFromClient(s.jobStore.Client())
}

func delegationIssuedView(tenant string, claims delegation.DelegationClaims, audience string) delegation.DelegationView {
	rootIssuer := strings.TrimSpace(claims.Subject)
	if len(claims.DelegationChain) > 0 {
		if first := strings.TrimSpace(claims.DelegationChain[0].AgentID); first != "" {
			rootIssuer = first
		}
	}
	return delegation.DelegationView{
		JTI:            strings.TrimSpace(claims.ID),
		Tenant:         strings.TrimSpace(tenant),
		Issuer:         rootIssuer,
		Subject:        strings.TrimSpace(claims.Subject),
		Audience:       strings.TrimSpace(audience),
		AllowedActions: append([]string(nil), claims.AllowedActions...),
		AllowedTopics:  append([]string(nil), claims.AllowedTopics...),
		Chain:          append([]delegation.ChainLink(nil), claims.DelegationChain...),
		ChainDepth:     claims.ChainDepth,
		IssuedAt:       claims.IssuedAt.Time.UTC(),
		ExpiresAt:      claims.ExpiresAt.Time.UTC(),
		ParentJTI:      strings.TrimSpace(claims.ParentTokenJTI),
	}
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
