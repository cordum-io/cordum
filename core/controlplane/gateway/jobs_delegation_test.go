package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/golang-jwt/jwt/v5"
)

func TestHandleSubmitJobHTTPInjectsDelegationContextIntoPolicyCheck(t *testing.T) {
	s, _, safetyClient := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"`+token+`"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if safetyClient.lastReq == nil {
		t.Fatal("expected policy check request to be captured")
	}
	if got := safetyClient.lastReq.GetLabels()["agent_id"]; got != "agent-b" {
		t.Fatalf("agent_id label = %q, want agent-b", got)
	}
	if got := safetyClient.lastReq.GetLabels()["_delegation.depth"]; got != "1" {
		t.Fatalf("delegation depth label = %q, want 1", got)
	}
	if got := safetyClient.lastReq.GetLabels()["_delegation.issuer"]; got != "agent-a" {
		t.Fatalf("delegation issuer label = %q, want agent-a", got)
	}
	if got := safetyClient.lastReq.GetLabels()["_delegation.issuer_chain"]; got != "agent-a" {
		t.Fatalf("delegation issuer_chain label = %q, want agent-a", got)
	}
	if got := safetyClient.lastReq.GetLabels()["_delegation.parent_issuer"]; got != "agent-a" {
		t.Fatalf("delegation parent_issuer label = %q, want agent-a", got)
	}
	if got := safetyClient.lastReq.GetLabels()["_delegation.scope"]; got != "read" {
		t.Fatalf("delegation scope label = %q, want read", got)
	}
	if got := safetyClient.lastReq.GetLabels()["_delegation.jti"]; got == "" {
		t.Fatal("delegation jti label should be present")
	}
}

func TestHandleSubmitJobHTTPPersistsDelegationDispatchToken(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"`+token+`"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got, err := s.jobStore.GetDelegationDispatchToken(context.Background(), resp.JobID)
	if err != nil {
		t.Fatalf("GetDelegationDispatchToken() error = %v", err)
	}
	if got.Token != token {
		t.Fatalf("stored delegation token mismatch")
	}
	if got.Audience != "agent-b" {
		t.Fatalf("audience = %q, want agent-b", got.Audience)
	}
}

func TestHandleSubmitJobHTTPEmitsDelegationExtrasOnSafetyDecisionAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, verifiedClaims, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}
	verified, err := service.VerifyDelegationToken(context.Background(), token, "agent-b")
	if err != nil {
		t.Fatalf("VerifyDelegationToken() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"`+token+`"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var decisionEvent *audit.SIEMEvent
	for i := range sink.events {
		ev := &sink.events[i]
		if ev.EventType == audit.EventSafetyDecision && ev.Action == "submit" {
			decisionEvent = ev
			break
		}
	}
	if decisionEvent == nil {
		t.Fatalf("expected safety.decision audit event, got %#v", sink.events)
	}
	if decisionEvent.Decision != "allow" {
		t.Fatalf("decision = %q, want allow", decisionEvent.Decision)
	}
	if decisionEvent.Extra["topic"] != "job.test" {
		t.Fatalf("topic extra = %q, want job.test", decisionEvent.Extra["topic"])
	}
	wantExtras := map[string]string{
		"delegation.depth":         "1",
		"delegation.root_issuer":   "agent-a",
		"delegation.parent_issuer": "agent-a",
		"delegation.audience":      "agent-b",
		"delegation.expires_at":    verified.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	for key, want := range wantExtras {
		if got := decisionEvent.Extra[key]; got != want {
			t.Fatalf("extra[%q] = %q, want %q", key, got, want)
		}
	}
	if got := decisionEvent.Extra["delegation.jti"]; got == "" {
		t.Fatal("delegation.jti should be present on safety decision audit event")
	}
	if got := decisionEvent.Extra["delegation.chain"]; got != "" {
		t.Fatalf("delegation.chain = %q, want omitted from safety decision audit event", got)
	}
	if verifiedClaims.ExpiresAt == nil {
		t.Fatal("expected issued claims to carry an expiration")
	}
}

func TestHandleSubmitJobHTTPEmitsDelegationRejectedAuditOnVerifyFailure(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)
	sink := &testAuditSender{}
	s.auditExporter = sink

	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"not-a-jwt"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sink.Len() != 1 {
		t.Fatalf("expected 1 audit event, got %d", sink.Len())
	}
	event := sink.Get(0)
	if event.EventType != audit.EventDelegationRejected {
		t.Fatalf("event_type = %q, want %q", event.EventType, audit.EventDelegationRejected)
	}
	if event.Action != "submit" {
		t.Fatalf("action = %q, want submit", event.Action)
	}
	if event.Reason != "malformed" {
		t.Fatalf("reason = %q, want malformed", event.Reason)
	}
	if event.Extra["topic"] != "job.test" {
		t.Fatalf("topic extra = %q, want job.test", event.Extra["topic"])
	}
}

func TestHandleSubmitJobHTTPRejectsDelegationAudienceMismatch(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-c", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-c", "worker-c"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"`+token+`"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-c",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "audience_mismatch") {
		t.Fatalf("expected audience_mismatch body, got %s", rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPWithoutDelegationLeavesContextUnset(t *testing.T) {
	s, _, safetyClient := newTestGateway(t)
	enableTestAuth(s)

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if safetyClient.lastReq == nil {
		t.Fatal("expected policy check request to be captured")
	}
	for _, key := range []string{
		"_delegation.depth",
		"_delegation.issuer",
		"_delegation.issuer_chain",
		"_delegation.parent_issuer",
		"_delegation.scope",
		"_delegation.jti",
	} {
		if got := safetyClient.lastReq.GetLabels()[key]; got != "" {
			t.Fatalf("%s = %q, want empty for direct call", key, got)
		}
	}
}

func TestHandleSubmitJobHTTPRejectsMalformedDelegationToken(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"not-a-jwt"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "malformed") {
		t.Fatalf("expected malformed body, got %s", rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPRejectsRevokedDelegationToken(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, claims, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-b",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}
	if err := delegation.NewRedisRevocationStoreFromClient(s.jobStore.Client()).Revoke(context.Background(), claims.ID, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"`+token+`"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "revoked") {
		t.Fatalf("expected revoked body, got %s", rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPRejectsExpiredDelegationToken(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	signingKey := setDelegationKeys(t)

	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	now := time.Now().UTC().Add(-2 * time.Hour)
	expiresAt := now.Add(-15 * time.Minute)
	token := signDelegationToken(t, signingKey, delegation.DelegationClaims{
		Tenant:         "default",
		AllowedActions: []string{"read"},
		AllowedTopics:  []string{"job.test"},
		DelegationChain: []delegation.ChainLink{{
			AgentID:   "agent-a",
			IssuedAt:  now.Format(time.RFC3339Nano),
			ExpiresAt: expiresAt.Format(time.RFC3339Nano),
			JTI:       "expired-link",
			IssuedBy:  "cordum",
		}},
		ChainDepth: 1,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "cordum",
			Subject:   "agent-a",
			Audience:  jwt.ClaimStrings{"agent-b"},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now.Add(-time.Minute)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        "expired-link",
		},
	})

	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(`{"prompt":"hello","topic":"job.test","delegation_token":"`+token+`"}`)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "admin",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "expired") {
		t.Fatalf("expected expired body, got %s", rec.Body.String())
	}
}

// TestHandleSubmitJobHTTPRejectsAudienceOverrideWithoutImpersonatePermission
// verifies the delegation_audience_agent_id field cannot be used to widen
// identity at token-verification time. A caller whose auth-derived agent
// identity differs from the requested audience must hold
// PermDelegationImpersonate (or the admin legacy role); otherwise the
// submit is rejected 403 and an audit event is recorded.
func TestHandleSubmitJobHTTPRejectsAudienceOverrideWithoutImpersonatePermission(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(entitlements *licensing.Entitlements) {
		entitlements.RBAC = true
		entitlements.AgentIdentity = true
	})

	// Relay worker is bound to agent-b, but will try to submit with
	// delegation_audience_agent_id=agent-c (impersonation attempt).
	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-c", []string{"read"}, []string{"job.test"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}

	// Role holds jobs.write (so the outer gate passes) but NOT
	// delegation.impersonate, so the audience override gate must fire.
	putTestRole(t, s, "relay", auth.PermJobsWrite)

	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService() error = %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant:            "default",
		DelegatingAgentID: "agent-a",
		TargetAgentID:     "agent-c",
		AllowedActions:    []string{"read"},
		AllowedTopics:     []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken() error = %v", err)
	}

	body := `{"prompt":"hello","topic":"job.test","delegation_token":"` + token + `","delegation_audience_agent_id":"agent-c"}`
	req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body)), &auth.AuthContext{
		Tenant:      "default",
		PrincipalID: "worker-b",
		Role:        "relay",
	})
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s want 403", rec.Code, rec.Body.String())
	}
}

func TestHandleSubmitJobHTTPIgnoresOtherTenantsWorkerLink(t *testing.T) {
	for _, clientLabel := range []bool{true, false} {
		name := "no_label"
		if clientLabel {
			name = "client_label"
		}
		t.Run(name, func(t *testing.T) {
			s, bus, safetyClient := newTestGateway(t)
			enableTestAuth(s)
			createDelegationAgent(t, s, "tenant-b", "agent-foreign", []string{"read"}, []string{"job.test"})
			if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-foreign", "p"); err != nil {
				t.Fatalf("link worker: %v", err)
			}
			body := `{"prompt":"hello","topic":"job.test"}`
			if clientLabel {
				body = `{"prompt":"hello","topic":"job.test","labels":{"agent_id":"agent-foreign"}}`
			}
			req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body)),
				&auth.AuthContext{Tenant: "default", PrincipalID: "p", Role: "admin"})
			rr := httptest.NewRecorder()
			s.handleSubmitJobHTTP(rr, req)
			if clientLabel {
				if rr.Code != http.StatusForbidden || len(bus.published) != 0 {
					t.Fatalf("expected 403 and no publish, got %d, publishes=%d: %s", rr.Code, len(bus.published), rr.Body.String())
				}
				return
			}
			if rr.Code != http.StatusOK || safetyClient.lastReq == nil {
				t.Fatalf("expected 200 and policy check, got %d: %s", rr.Code, rr.Body.String())
			}
			if got := safetyClient.lastReq.GetLabels()["agent_id"]; got != "" {
				t.Fatalf("agent_id = %q, want no injected identity", got)
			}
		})
	}
}

func TestHandleDelegateAgentRefusesOtherTenantsWorkerLink(t *testing.T) {
	for _, tenant := range []string{"tenant-b", "default"} {
		t.Run(tenant, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			// Exercise the principal-binding gate directly: the outer legacy
			// role gate otherwise admits only admins, even with RBAC enabled.
			setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
				e.AgentIdentity, e.RBAC = true, true
			})
			setDelegationKeys(t)
			putTestRole(t, s, "delegator", auth.PermAgentsDelegate)
			createDelegationAgent(t, s, tenant, "agent-source", []string{"read"}, []string{"job.alpha"})
			createDelegationAgent(t, s, "default", "agent-target", []string{"read"}, []string{"job.alpha"})
			if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-source", "p"); err != nil {
				t.Fatalf("link worker: %v", err)
			}
			body := `{"target_agent_id":"agent-target","allowed_actions":["read"],"allowed_topics":["job.alpha"]}`
			req := withAuth(httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-source/delegate", strings.NewReader(body)),
				&auth.AuthContext{Tenant: "default", PrincipalID: "p", Role: "delegator"})
			req.SetPathValue("id", "agent-source")
			rr := httptest.NewRecorder()
			s.handleDelegateAgent(rr, req)
			want := http.StatusCreated
			if tenant == "tenant-b" {
				want = http.StatusForbidden
			}
			if rr.Code != want {
				t.Fatalf("expected %d, got %d: %s", want, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestSubmitJobGRPCIgnoresOtherTenantsWorkerLink(t *testing.T) {
	for _, tenant := range []string{"tenant-b", "default"} {
		t.Run(tenant, func(t *testing.T) {
			s, _, safetyClient := newTestGateway(t)
			s.tenant = "default"
			createDelegationAgent(t, s, tenant, "agent-source", []string{"read"}, []string{"job.default"})
			if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-source", "p"); err != nil {
				t.Fatalf("link worker: %v", err)
			}
			_, err := s.SubmitJob(context.Background(), &pb.SubmitJobRequest{
				Prompt: "hello", Topic: "job.default", OrgId: "default", PrincipalId: "p",
			})
			if err != nil {
				t.Fatalf("submit job: %v", err)
			}
			if safetyClient.lastReq == nil {
				t.Fatal("expected policy check")
			}
			want := "agent-source"
			if tenant == "tenant-b" {
				want = ""
			}
			if got := safetyClient.lastReq.GetLabels()["agent_id"]; got != want {
				t.Fatalf("agent_id = %q, want %q", got, want)
			}
		})
	}
}

func signDelegationToken(t *testing.T, signingKey delegation.SigningKey, claims delegation.DelegationClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = signingKey.KID
	signed, err := token.SignedString(signingKey.PrivateKey)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}
	return signed
}
