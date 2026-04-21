package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
)

func TestHandleSubmitJobHTTPInjectsDelegationContextIntoPolicyCheck(t *testing.T) {
	s, _, safetyClient := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "worker-b"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}
	createDelegationAgent(t, s, "default", "agent-a", []string{"read", "write"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})

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

func TestHandleSubmitJobHTTPRejectsDelegationAudienceMismatch(t *testing.T) {
	s, _, _ := newTestGateway(t)
	enableTestAuth(s)
	setDelegationKeys(t)

	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-c", "worker-c"); err != nil {
		t.Fatalf("LinkWorker() error = %v", err)
	}
	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.test"})
	createDelegationAgent(t, s, "default", "agent-c", []string{"read"}, []string{"job.test"})

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
