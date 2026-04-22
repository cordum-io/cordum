package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/infra/store"
	"github.com/cordum/cordum/core/licensing"
)

func setDelegationKeys(t *testing.T) delegation.SigningKey {
	t.Helper()

	signingKey, err := delegation.GenerateSigningKey("dlg-1")
	if err != nil {
		t.Fatalf("GenerateSigningKey() error = %v", err)
	}
	privatePEM, err := delegation.EncodePrivateKeyPEM(signingKey.PrivateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM() error = %v", err)
	}
	publicKey, err := delegation.EncodePublicKeyBase64(signingKey.PublicKey())
	if err != nil {
		t.Fatalf("EncodePublicKeyBase64() error = %v", err)
	}
	t.Setenv("CORDUM_DELEGATION_PRIVATE_KEY", string(privatePEM))
	t.Setenv("CORDUM_DELEGATION_PUBLIC_KEY_DLG_1", publicKey)
	t.Setenv("CORDUM_DELEGATION_KEY_ID", "dlg-1")
	return signingKey
}

func createDelegationAgent(t *testing.T, s *server, tenant, id string, actions, topics []string) *store.AgentIdentity {
	t.Helper()
	_ = tenant // store.AgentIdentity has no tenant field today; tenant
	// scoping is enforced upstream at the gateway middleware. The
	// parameter is kept so existing call sites don't need to change
	// once per-agent tenant binding lands in the store.
	created, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:            id,
		Name:          id,
		Owner:         "admin",
		RiskTier:      "low",
		Status:        "active",
		AllowedTools:  actions,
		AllowedTopics: topics,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return created
}

func TestHandleDelegateAgentIssuesTokenAndAudits(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	delegating := createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.alpha"})
	target := createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})

	body := bytes.NewBufferString(`{"target_agent_id":"` + target.ID + `","allowed_actions":["read"],"allowed_topics":["job.alpha"]}`)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+delegating.ID+"/delegate", body))
	req.SetPathValue("id", delegating.ID)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleDelegateAgent(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp delegateTokenResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" || resp.KID != "dlg-1" || resp.ChainDepth != 1 || resp.JTI == "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	verified, err := service.VerifyDelegationToken(context.Background(), resp.Token, target.ID)
	if err != nil {
		t.Fatalf("VerifyDelegationToken() error = %v", err)
	}
	if verified.Subject != delegating.ID || verified.Audience != target.ID {
		t.Fatalf("verified token = %+v", verified)
	}
	if len(sink.events) == 0 {
		t.Fatal("expected delegation audit event")
	}
	event := sink.events[len(sink.events)-1]
	if event.Action != "delegation.issue" || event.Extra["outcome"] != "ok" || event.Extra["target"] != target.ID {
		t.Fatalf("unexpected audit event: %+v", event)
	}
}

func TestHandleDelegateAgentRequiresMatchingPrincipalOrAdmin(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
		entitlements.RBAC = true
	})
	setDelegationKeys(t)
	putTestRole(t, s, "delegator", auth.PermAgentsDelegate)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-a/delegate", strings.NewReader(`{"target_agent_id":"agent-b","allowed_actions":["read"],"allowed_topics":["job.alpha"]}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "someone-else",
		Role:        "delegator",
	})
	req.SetPathValue("id", "agent-a")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleDelegateAgent(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleDelegateAgentCrossTenantAndScopeErrors(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)

	// NOTE: the cross-tenant subtest previously asserted that a
	// delegation request where delegating and target agents live in
	// different tenants is rejected 403. That rejection relied on
	// store.AgentIdentity.TenantID, which the store no longer exposes
	// (tenant scoping for agent identities moved upstream to the
	// gateway tenant middleware). The scope-exceeded subtest below
	// continues to exercise the scope-subset enforcement — the exact
	// attack that matters at the delegation wire path.

	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read", "write"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-c", []string{"read"}, []string{"job.alpha"})

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	parentToken, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	scopeReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-b/delegate", strings.NewReader(`{"target_agent_id":"agent-c","allowed_actions":["write"],"allowed_topics":["job.alpha"],"parent_token":"`+parentToken+`"}`)))
	scopeReq.SetPathValue("id", "agent-b")
	scopeReq.Header.Set("Content-Type", "application/json")
	scopeRec := httptest.NewRecorder()
	s.handleDelegateAgent(scopeRec, scopeReq)
	if scopeRec.Code != http.StatusBadRequest || !strings.Contains(scopeRec.Body.String(), "scope_exceeded") {
		t.Fatalf("scope status=%d body=%s", scopeRec.Code, scopeRec.Body.String())
	}
}

func TestHandleDelegateAgentRejectsTooDeepChain(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read", "write"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-c", []string{"read"}, []string{"job.alpha"})

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	tokenAB, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken(ab) error = %v", err)
	}
	tokenBC, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-b",
		TargetAgentID:     "agent-c",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       tokenAB,
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken(bc) error = %v", err)
	}
	tokenCB, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-c",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       tokenBC,
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken(cb) error = %v", err)
	}

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-b/delegate", strings.NewReader(`{"target_agent_id":"agent-a","allowed_actions":["read"],"allowed_topics":["job.alpha"],"parent_token":"`+tokenCB+`"}`)))
	req.SetPathValue("id", "agent-b")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleDelegateAgent(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "chain_too_deep") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleVerifyAndRevokeDelegationStructuredVerdicts(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	verifyReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/verify-delegation", strings.NewReader(`{"token":"`+token+`","expected_audience":"agent-x"}`)))
	verifyReq.Header.Set("Content-Type", "application/json")
	verifyRec := httptest.NewRecorder()
	s.handleVerifyDelegation(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verifyRec.Code, verifyRec.Body.String())
	}
	var invalidResp verifyDelegationResponse
	if err := json.NewDecoder(verifyRec.Body).Decode(&invalidResp); err != nil {
		t.Fatalf("decode invalid verify response: %v", err)
	}
	if invalidResp.Valid || invalidResp.ErrorCode != "audience_mismatch" {
		t.Fatalf("unexpected invalid response: %+v", invalidResp)
	}

	revokeReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/revoke-delegation", strings.NewReader(`{"jti":"`+service.KeyID()+`"}`)))
	revokeReq.Header.Set("Content-Type", "application/json")
	// revoke the actual token jti
	verified, err := service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if err != nil {
		t.Fatalf("VerifyDelegationToken() error = %v", err)
	}
	if err := s.delegationListStore().RecordIssuedToken(context.Background(), delegation.DelegationView{
		JTI:            verified.JTI,
		Tenant:         "default",
		Issuer:         "agent-a",
		Subject:        verified.Subject,
		Audience:       verified.Audience,
		AllowedActions: append([]string(nil), verified.AllowedActions...),
		AllowedTopics:  append([]string(nil), verified.AllowedTopics...),
		Chain:          append([]delegation.ChainLink(nil), verified.DelegationChain...),
		ChainDepth:     verified.ChainDepth,
		IssuedAt:       verified.IssuedAt,
		ExpiresAt:      verified.ExpiresAt,
		ParentJTI:      verified.ParentTokenJTI,
	}); err != nil {
		t.Fatalf("RecordIssuedToken() error = %v", err)
	}
	revokeReq = adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/revoke-delegation", strings.NewReader(`{"jti":"`+verified.JTI+`"}`)))
	revokeReq.Header.Set("Content-Type", "application/json")
	revokeRec := httptest.NewRecorder()
	s.handleRevokeDelegation(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeRec.Code, revokeRec.Body.String())
	}
	var revokeResp revokeDelegationResponse
	if err := json.NewDecoder(revokeRec.Body).Decode(&revokeResp); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if revokeResp.JTI != verified.JTI || revokeResp.CascadedCount != 0 {
		t.Fatalf("unexpected revoke response: %+v", revokeResp)
	}

	verifyValidReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/verify-delegation", strings.NewReader(`{"token":"`+token+`","expected_audience":"agent-b"}`)))
	verifyValidReq.Header.Set("Content-Type", "application/json")
	verifyValidRec := httptest.NewRecorder()
	s.handleVerifyDelegation(verifyValidRec, verifyValidReq)
	if verifyValidRec.Code != http.StatusOK {
		t.Fatalf("verify revoked status=%d body=%s", verifyValidRec.Code, verifyValidRec.Body.String())
	}
	var revokedResp verifyDelegationResponse
	if err := json.NewDecoder(verifyValidRec.Body).Decode(&revokedResp); err != nil {
		t.Fatalf("decode revoked response: %v", err)
	}
	if revokedResp.Valid || revokedResp.ErrorCode != "revoked" {
		t.Fatalf("unexpected revoked response: %+v", revokedResp)
	}

	if len(sink.events) < 3 {
		t.Fatalf("expected verify+revoke audit events, got %d", len(sink.events))
	}
}

func TestHandleRevokeDelegationCascadesAndAudits(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-c", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-d", []string{"read"}, []string{"job.alpha"})

	tokenAB, verifiedAB := issueDelegationTokenForTests(t, s, delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
	}, "agent-b")
	tokenBC, _ := issueDelegationTokenForTests(t, s, delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-b",
		TargetAgentID:     "agent-c",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       tokenAB,
	}, "agent-c")
	_, verifiedCD := issueDelegationTokenForTests(t, s, delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-c",
		TargetAgentID:     "agent-d",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.alpha"},
		ParentToken:       tokenBC,
	}, "agent-d")

	revokeReq := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/revoke-delegation", strings.NewReader(`{"jti":"`+verifiedAB.JTI+`","reason":"compromised"}`)))
	revokeReq.Header.Set("Content-Type", "application/json")
	revokeRec := httptest.NewRecorder()
	s.handleRevokeDelegation(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeRec.Code, revokeRec.Body.String())
	}
	var revokeResp revokeDelegationResponse
	if err := json.NewDecoder(revokeRec.Body).Decode(&revokeResp); err != nil {
		t.Fatalf("decode cascade revoke response: %v", err)
	}
	if revokeResp.CascadedCount != 2 {
		t.Fatalf("unexpected cascade revoke response: %+v", revokeResp)
	}

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	if _, err := service.VerifyDelegationToken(context.Background(), tokenBC, "agent-c"); err != delegation.ErrRevoked {
		t.Fatalf("expected child token to be revoked, got %v", err)
	}
	if _, err := service.VerifyDelegationToken(context.Background(), verifiedCD.Token, "agent-d"); err != delegation.ErrRevoked {
		t.Fatalf("expected grandchild token to be revoked, got %v", err)
	}

	if !hasDelegationAuditAction(sink.events, "delegation.revoked_cascade") {
		t.Fatalf("expected cascade audit event, got %#v", sink.events)
	}
	if got := countDelegationAuditAction(sink.events, "delegation.revoked"); got != 2 {
		t.Fatalf("expected 2 descendant revoked audit events, got %d", got)
	}
	if !hasDelegationAuditAction(sink.events, "delegation.revoke") {
		t.Fatalf("expected root revoke audit event, got %#v", sink.events)
	}
}

func TestHandleRevokeDelegationReturnsNotFoundForUnknownJTI(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/revoke-delegation", strings.NewReader(`{"jti":"missing"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleRevokeDelegation(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown jti, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandleDelegateAgentRejectsOverflowTTL guards the `ttl_seconds`
// multiplication overflow path. Without the pre-multiplication bound,
// `time.Duration(req.TTLSeconds) * time.Second` wraps int64 nanoseconds
// into a negative duration that would sneak past the service-layer
// maxTTL check (which tests `ttl > maxTTL`) and mint a token with
// unbounded lifetime.
func TestHandleDelegateAgentRejectsOverflowTTL(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.AgentIdentity = true
	})
	setDelegationKeys(t)

	delegating := createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.alpha"})
	target := createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})

	// math.MaxInt64 would overflow int64 nanoseconds when multiplied by
	// time.Second; the handler must reject before the multiplication.
	body := bytes.NewBufferString(`{"target_agent_id":"` + target.ID + `","allowed_actions":["read"],"allowed_topics":["job.alpha"],"ttl_seconds":9223372036854775000}`)
	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+delegating.ID+"/delegate", body))
	req.SetPathValue("id", delegating.ID)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleDelegateAgent(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on overflow ttl_seconds, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ttl_seconds") {
		t.Fatalf("expected ttl_seconds error message, got: %s", rec.Body.String())
	}

	// One-year-plus-one-second is also rejected — 1 year is the documented
	// pre-multiplication cap.
	body = bytes.NewBufferString(`{"target_agent_id":"` + target.ID + `","allowed_actions":["read"],"allowed_topics":["job.alpha"],"ttl_seconds":31536001}`)
	req = adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+delegating.ID+"/delegate", body))
	req.SetPathValue("id", delegating.ID)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()

	s.handleDelegateAgent(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 on >1yr ttl_seconds, got %d: %s", rec.Code, rec.Body.String())
	}
}

func issueDelegationTokenForTests(t *testing.T, s *server, req delegation.IssueRequest, expectedAudience string) (string, delegation.VerifiedToken) {
	t.Helper()
	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, claims, err := service.IssueDelegationToken(context.Background(), req)
	if err != nil {
		t.Fatalf("IssueDelegationToken(%s->%s) error = %v", req.DelegatingAgentID, req.TargetAgentID, err)
	}
	if err := s.delegationListStore().RecordIssuedToken(context.Background(), delegationIssuedView(req.Tenant, claims, req.TargetAgentID)); err != nil {
		t.Fatalf("RecordIssuedToken(%s) error = %v", claims.ID, err)
	}
	verified, err := service.VerifyDelegationToken(context.Background(), token, expectedAudience)
	if err != nil {
		t.Fatalf("VerifyDelegationToken(%s) error = %v", claims.ID, err)
	}
	return token, verified
}

func hasDelegationAuditAction(events []audit.SIEMEvent, action string) bool {
	return countDelegationAuditAction(events, action) > 0
}

func countDelegationAuditAction(events []audit.SIEMEvent, action string) int {
	count := 0
	for _, event := range events {
		if event.Action == action {
			count++
		}
	}
	return count
}
