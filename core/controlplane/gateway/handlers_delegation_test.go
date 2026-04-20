package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	created, err := s.agentIdentityStore.Create(context.Background(), store.AgentIdentity{
		ID:            id,
		TenantID:      tenant,
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

	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "other", "agent-x", []string{"read"}, []string{"job.alpha"})

	req := adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-a/delegate", strings.NewReader(`{"target_agent_id":"agent-x","allowed_actions":["read"],"allowed_topics":["job.alpha"]}`)))
	req.SetPathValue("id", "agent-a")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.handleDelegateAgent(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant status=%d body=%s", rec.Code, rec.Body.String())
	}

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
	revokeReq = adminCtx(httptest.NewRequest(http.MethodPost, "/api/v1/agents/revoke-delegation", strings.NewReader(`{"jti":"`+verified.JTI+`"}`)))
	revokeReq.Header.Set("Content-Type", "application/json")
	revokeRec := httptest.NewRecorder()
	s.handleRevokeDelegation(revokeRec, revokeReq)
	if revokeRec.Code != http.StatusNoContent {
		t.Fatalf("revoke status=%d body=%s", revokeRec.Code, revokeRec.Body.String())
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
