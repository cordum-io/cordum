package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/auth/delegation"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/metadata"
)

func newDelegationAudienceScopeFixture(t *testing.T, scope string) (edgeScopeFixture, *auth.ManagedKey, string) {
	t.Helper()
	f := newEdgeScopeFixture(t)
	setTestEntitlements(t, f.s, licensing.PlanEnterprise, func(e *licensing.Entitlements) {
		e.RBAC = false
	})
	if auth.RBACEntitled(f.s.currentEntitlements()) {
		t.Fatal("expected RBAC entitlement off")
	}
	for _, id := range []string{"agent-a", "agent-b", "agent-c"} {
		createDelegationAgent(t, f.s, "default", id, []string{"read"}, []string{"job.test"})
	}
	key, secret := f.mint(t, scope)
	if err := f.s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", key.ID); err != nil {
		t.Fatalf("LinkWorker: %v", err)
	}
	return f, key, secret
}

func delegationAudienceScopeRequest(t *testing.T, s *server, audience string) (*http.Request, string) {
	t.Helper()
	setDelegationKeys(t)
	service, err := s.delegationTokenService()
	if err != nil {
		t.Fatalf("delegationTokenService: %v", err)
	}
	token, _, err := service.IssueDelegationToken(context.Background(), delegation.IssueRequest{
		Tenant: "default", DelegatingAgentID: "agent-a", TargetAgentID: audience,
		AllowedActions: []string{"read"}, AllowedTopics: []string{"job.test"},
	})
	if err != nil {
		t.Fatalf("IssueDelegationToken: %v", err)
	}
	if _, err := service.VerifyDelegationToken(context.Background(), token, audience); err != nil {
		t.Fatalf("VerifyDelegationToken: %v", err)
	}
	body := `{"prompt":"hello","topic":"job.test","delegation_token":"` + token + `","delegation_audience_agent_id":"` + audience + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	return req, token
}

func assertDelegationAudienceResult(t *testing.T, f edgeScopeFixture, rec *httptest.ResponseRecorder, token, audience string, allowed bool) {
	t.Helper()
	wantCode, wantJobs, wantDenials := http.StatusForbidden, 0, 1
	if allowed {
		wantCode, wantJobs, wantDenials = http.StatusOK, 1, 0
	}
	jobs, err := f.s.jobStore.ListRecentJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListRecentJobs: %v", err)
	}
	denials := (linkInjectionFixture{s: f.s}).audienceDenials(t)
	if rec.Code != wantCode || denials != wantDenials || len(jobs) != wantJobs {
		t.Errorf("status=%d audience_denials=%d stored_jobs=%d, want %d/%d/%d; body=%s", rec.Code, denials, len(jobs), wantCode, wantDenials, wantJobs, rec.Body.String())
	}
	for _, job := range jobs {
		stored, err := f.s.jobStore.GetDelegationDispatchToken(context.Background(), job.ID)
		if err != nil {
			t.Fatalf("GetDelegationDispatchToken: %v", err)
		}
		if stored.Audience != audience || stored.Token != token {
			t.Error("stored delegation audience/token does not match the authorized request")
		}
	}
	for _, pattern := range []string{"job:req:*", "ctx:*"} {
		if count := f.records(t, pattern); count != wantJobs {
			t.Errorf("stored %s records=%d, want %d", pattern, count, wantJobs)
		}
	}
}

func TestDelegationAudienceOverrideHTTPManaged(t *testing.T) {
	for _, tc := range []struct {
		name, scope, audience      string
		rbac, impersonate, allowed bool
	}{
		{"jobs_write", "jobs:write", "agent-c", false, false, false},
		{"jobs_wildcard", "jobs:*", "agent-c", false, false, false},
		{"bare_write", "write", "agent-c", false, false, false},
		{"admin", "admin", "agent-c", false, false, true},
		{"own_agent", "jobs:write", "agent-b", false, false, true},
		{"rbac_allowed", "jobs:write", "agent-c", true, true, true},
		{"rbac_denied", "jobs:write", "agent-c", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _, secret := newDelegationAudienceScopeFixture(t, tc.scope)
			setTestEntitlements(t, f.s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.RBAC = tc.rbac })
			if auth.RBACEntitled(f.s.currentEntitlements()) != tc.rbac || f.s.permChecker == nil {
				t.Fatal("wrong RBAC fixture configuration")
			}
			if tc.rbac {
				permissions := []string{auth.PermJobsWrite}
				if tc.impersonate {
					permissions = append(permissions, auth.PermDelegationImpersonate)
				}
				putTestRole(t, f.s, "operator", permissions...)
			}
			req, token := delegationAudienceScopeRequest(t, f.s, tc.audience)
			req.Header.Set("X-API-Key", secret)
			rec := httptest.NewRecorder()
			f.handler.ServeHTTP(rec, req)
			f.provider.DrainUsage()
			assertDelegationAudienceResult(t, f, rec, token, tc.audience, tc.allowed)
		})
	}
}

func TestDelegationAudienceOverrideHTTPStaticOperator(t *testing.T) {
	for _, tc := range []struct {
		name, role, audience string
		allowed              bool
	}{
		{"operator_other_agent", "operator", "agent-c", false},
		{"operator_own_agent", "operator", "agent-b", true},
		{"admin_other_agent", "admin", "agent-c", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _, _ := newDelegationAudienceScopeFixture(t, "jobs:write")
			provider := newBasicAuthForTest(t, map[string]string{
				"CORDUM_ENV":      "production",
				"CORDUM_API_KEYS": `[{"key":"` + edgeScopeStaticKey + `","tenant":"default","role":"` + tc.role + `","principal_id":"static-caller"}]`,
			})
			f.s.auth = provider
			handler, err := newHTTPHandler(f.s)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", "static-caller"); err != nil {
				t.Fatal(err)
			}
			req, token := delegationAudienceScopeRequest(t, f.s, tc.audience)
			req.Header.Set("X-API-Key", edgeScopeStaticKey)
			caller, err := provider.AuthenticateHTTP(req)
			if err != nil || caller == nil || caller.AuthSource != auth.AuthSourceAPIKey || caller.Role != tc.role {
				t.Fatalf("static key must authenticate as %s: %v", tc.role, err)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			assertDelegationAudienceResult(t, f, rec, token, tc.audience, tc.allowed)
			if !tc.allowed {
				for _, pattern := range []string{"job:state:*", "job:meta:*", "delegation:*"} {
					if count := f.records(t, pattern); count != 0 {
						t.Errorf("stored %s records=%d, want 0", pattern, count)
					}
				}
			}
		})
	}
}

// Without the RBAC entitlement an operator session holds only the built-in operator
// permissions, which lack delegation.impersonate, so an audience override to another
// agent answers 403 and stores nothing.
func TestDelegationAudienceOverrideHTTPSessionOperatorDeniedWithoutRBAC(t *testing.T) {
	f, _, _ := newDelegationAudienceScopeFixture(t, "jobs:write")
	client, ok := f.s.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatal("expected Redis client")
	}
	users := auth.NewRedisUserStoreFromClient(client)
	f.provider.SetUserStore(users)
	user := &auth.User{ID: "session-operator", Username: "operator", Tenant: "default", Role: "operator"}
	if err := users.StoreSession(context.Background(), "session-audience", user, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := f.s.agentIdentityStore.LinkWorker(context.Background(), "agent-b", user.ID); err != nil {
		t.Fatal(err)
	}
	req, token := delegationAudienceScopeRequest(t, f.s, "agent-c")
	req.Header.Set("Authorization", "Bearer session-audience")
	caller, err := f.provider.AuthenticateHTTP(req)
	if err != nil || caller == nil || caller.AuthSource != auth.AuthSourceSession || caller.Role != "operator" {
		t.Fatalf("session must authenticate with the stored operator role: %v", err)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	assertDelegationAudienceResult(t, f, rec, token, "agent-c", false)
	for _, pattern := range []string{"job:state:*", "job:meta:*", "delegation:*"} {
		if count := f.records(t, pattern); count != 0 {
			t.Errorf("stored %s records=%d, want 0", pattern, count)
		}
	}
}

func TestDelegationAudienceOverrideHTTPEmptyAPIKeyRole(t *testing.T) {
	f, key, _ := newDelegationAudienceScopeFixture(t, "jobs:write")
	req, _ := delegationAudienceScopeRequest(t, f.s, "agent-c")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", PrincipalID: key.ID, AuthSource: auth.AuthSourceAPIKey})
	rec := httptest.NewRecorder()
	f.s.handleSubmitJobHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403: %s", rec.Code, rec.Body.String())
	}
	for _, pattern := range []string{"job:state:*", "job:req:*", "ctx:*"} {
		if count := f.records(t, pattern); count != 0 {
			t.Errorf("stored %s records=%d, want 0", pattern, count)
		}
	}
}

func TestDelegationAudienceOverrideGRPCStripsLabels(t *testing.T) {
	f, key, secret := newDelegationAudienceScopeFixture(t, "jobs:write")
	ctx := metadata.NewIncomingContext(withGRPCMethod(pb.CordumApi_SubmitJob_FullMethodName), metadata.Pairs("x-api-key", secret))
	caller, err := f.provider.AuthenticateGRPC(ctx)
	f.provider.DrainUsage()
	if err != nil {
		t.Fatalf("AuthenticateGRPC: %v", err)
	}
	if caller.AuthSource != auth.AuthSourceAPIKey || caller.Role != "operator" || caller.AuthenticatedPrincipal() != key.ID {
		t.Fatal("expected the managed jobs:write credential's authenticated operator principal")
	}
	resp, err := f.s.SubmitJob(context.WithValue(ctx, auth.ContextKey{}, caller), &pb.SubmitJobRequest{
		Prompt: "hello", Topic: "job.test", OrgId: "default",
		Labels: map[string]string{"_delegation.audience": "agent-c", "_delegation.subject": "agent-a", "team": "scope-test"},
	})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}
	stored, err := f.s.jobStore.GetJobRequest(ctx, resp.GetJobId())
	if err != nil {
		t.Fatalf("GetJobRequest: %v", err)
	}
	labels := stored.GetMeta().GetLabels()
	if labels["team"] != "scope-test" || labels["agent_id"] != "agent-b" {
		t.Fatalf("stored labels=%v, want ordinary label and authenticated agent", labels)
	}
	for label := range labels {
		if strings.HasPrefix(label, "_delegation.") {
			t.Errorf("stored reserved delegation label %q", label)
		}
	}
}
