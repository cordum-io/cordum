package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
	"github.com/cordum/cordum/core/licensing"
)

// These tests pin that a principal named only by the X-Principal-Id header
// (an unbound static or managed API key) never acts as the caller's identity
// in delegation issuance or in Edge approval access and resolution. Every
// auth context comes from a real BasicAuthProvider, in both profiles.

const assertedAuthorityBoundAdminKey = "assertedauthority-bound-admin-key-0000001"

const assertedAuthorityUnboundViewerKey = "assertedauthority-unbound-viewer-key-00001"

// assertedAuthorityManagedKey is the raw key regressionScopedKeyStore accepts.
const assertedAuthorityManagedKey = "scoped-test-secret"

const assertedAuthorityEdgeUserKey = "assertedauthority-edge-unbound-user-key-001"

const assertedAuthorityEdgeBoundUserKey = "assertedauthority-edge-bound-user-key-00001"

const assertedAuthorityEdgeAdminKey = "assertedauthority-edge-unbound-admin-key-01"

const assertedAuthorityEdgeBoundAdminKey = "assertedauthority-edge-bound-admin-key-0001"

const assertedAuthorityDelegateBody = `{"target_agent_id":"agent-b","allowed_actions":["read"],"allowed_topics":["job.alpha"]}`

const assertedAuthorityApprovals = "/api/v1/edge/approvals/"

// assertedAuthorityProfiles runs fn in production, where the header principal
// is honored (as asserted) only because CORDUM_ALLOW_HEADER_PRINCIPAL is on,
// and in development, where it is honored by default.
func assertedAuthorityProfiles(t *testing.T, fn func(t *testing.T, env map[string]string)) {
	t.Helper()
	t.Run("production", func(t *testing.T) {
		fn(t, map[string]string{"CORDUM_ENV": "production", "CORDUM_ALLOW_HEADER_PRINCIPAL": "true"})
	})
	t.Run("development", func(t *testing.T) {
		fn(t, map[string]string{"CORDUM_ENV": "development"})
	})
}

func assertedAuthorityContext(t *testing.T, ac *auth.AuthContext, role, principal string, asserted bool, authenticated string) {
	t.Helper()
	if ac.Role != role || ac.PrincipalID != principal || ac.PrincipalAsserted != asserted || ac.AuthenticatedPrincipal() != authenticated {
		t.Fatalf("auth context = role:%q principal:%q asserted:%v authenticated:%q, want role:%q principal:%q asserted:%v authenticated:%q",
			ac.Role, ac.PrincipalID, ac.PrincipalAsserted, ac.AuthenticatedPrincipal(), role, principal, asserted, authenticated)
	}
}

func assertedAuthorityStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, want, rec.Body.String())
	}
}

type assertedAuthorityDelegation struct {
	s        *server
	provider *auth.BasicAuthProvider
	sink     *recordingAuditSender
}

func assertedAuthorityDelegationFixture(t *testing.T, env map[string]string) assertedAuthorityDelegation {
	t.Helper()
	s, _, _ := newTestGateway(t)
	env["CORDUM_API_KEYS"] = `[` +
		`{"key":"` + assertedAuthorityBoundAdminKey + `","tenant":"default","role":"admin","principal_id":"worker-a"},` +
		`{"key":"` + assertedAuthorityUnboundViewerKey + `","tenant":"default","role":"viewer"}` +
		`]`
	provider := newBasicAuthForTest(t, env)
	provider.SetKeyStore(&regressionScopedKeyStore{key: &auth.ManagedKey{
		ID: "assertedauthority-operator", Tenant: "default", Scopes: []string{"operator"},
	}})
	s.auth = provider
	setTestEntitlements(t, s, licensing.PlanEnterprise, func(e *licensing.Entitlements) { e.AgentIdentity = true })
	// Enterprise RBAC: the built-in operator role lacks agents.delegate, so
	// grant it, as a deployment that lets operators delegate does. Otherwise
	// the outer gate refuses an operator key before the per-agent check.
	putTestRole(t, s, "operator", auth.PermAgentsDelegate)
	setDelegationKeys(t)
	sink := &recordingAuditSender{}
	s.auditExporter = sink
	createDelegationAgent(t, s, "default", "agent-a", []string{"read"}, []string{"job.alpha"})
	createDelegationAgent(t, s, "default", "agent-b", []string{"read"}, []string{"job.alpha"})
	if err := s.agentIdentityStore.LinkWorker(context.Background(), "agent-a", "worker-a"); err != nil {
		t.Fatalf("LinkWorker(agent-a, worker-a) error = %v, want nil", err)
	}
	return assertedAuthorityDelegation{s: s, provider: provider, sink: sink}
}

// delegate authenticates the request with the real provider, then calls the
// handler with that context, as apiKeyMiddleware would.
func (f assertedAuthorityDelegation) delegate(t *testing.T, apiKey, headerPrincipal string) (*auth.AuthContext, *httptest.ResponseRecorder) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/agent-a/delegate", strings.NewReader(assertedAuthorityDelegateBody))
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Principal-Id", headerPrincipal)
	ac, err := f.provider.AuthenticateHTTP(req)
	if err != nil || ac == nil {
		t.Fatalf("AuthenticateHTTP() = (%v, %v), want an auth context and nil error", ac, err)
	}
	f.provider.DrainUsage()
	req = withAuth(req, ac)
	req.SetPathValue("id", "agent-a")
	rec := httptest.NewRecorder()
	f.s.handleDelegateAgent(rec, req)
	return ac, rec
}

func TestAuthorityAssertedPrincipal_DelegationRefusesAssertedPrincipal(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f := assertedAuthorityDelegationFixture(t, env)
		ac, rec := f.delegate(t, assertedAuthorityManagedKey, "worker-a")
		// An operator-scoped managed key keeps the raw role "operator". It passes
		// the admin gate (operator normalizes to admin) and so reaches the
		// per-agent check. Static keys and JWTs normalize their role at
		// authentication and take the admin path instead.
		assertedAuthorityContext(t, ac, "operator", "worker-a", true, "")
		assertedAuthorityStatus(t, rec, http.StatusForbidden)
		if strings.Contains(rec.Body.String(), "token") {
			t.Fatalf("body = %s, want no token", rec.Body.String())
		}
		if len(f.sink.events) == 0 {
			t.Fatal("audit events = 0, want a denied delegation.issue event")
		}
		last := f.sink.events[len(f.sink.events)-1]
		if last.Action != "delegation.issue" || last.Extra["outcome"] != "denied" || last.Reason != "principal access denied" || last.AgentID != "agent-a" {
			t.Fatalf("last audit = action:%q outcome:%q reason:%q agent:%q, want delegation.issue, denied, principal access denied, agent-a",
				last.Action, last.Extra["outcome"], last.Reason, last.AgentID)
		}
		for _, ev := range f.sink.events {
			if ev.Extra["outcome"] == "ok" {
				t.Fatalf("audit event %q has outcome ok, want no ok outcome", ev.Action)
			}
		}
	})
}

func TestAuthorityAssertedPrincipal_DelegationBoundKeyIssues(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f := assertedAuthorityDelegationFixture(t, env)
		// Static-key roles normalize to admin at authentication, so this is the
		// admin path. The authenticated non-admin path is pinned by
		// TestHandleDelegateAgentRefusesOtherTenantsWorkerLink/default.
		ac, rec := f.delegate(t, assertedAuthorityBoundAdminKey, "worker-b")
		assertedAuthorityContext(t, ac, "admin", "worker-a", false, "worker-a")
		assertedAuthorityStatus(t, rec, http.StatusCreated)
		var resp delegateTokenResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response error = %v, want nil", err)
		}
		service, err := f.s.delegationTokenService()
		if err != nil {
			t.Fatalf("delegationTokenService() error = %v, want nil", err)
		}
		verified, err := service.VerifyDelegationToken(context.Background(), resp.Token, "agent-b")
		if err != nil || verified.Subject != "agent-a" {
			t.Fatalf("VerifyDelegationToken() = (%+v, %v), want subject agent-a and nil error", verified, err)
		}
		if n := len(f.sink.events); n == 0 || f.sink.events[n-1].Extra["outcome"] != "ok" {
			t.Fatalf("audit events = %+v, want a last event with outcome ok", f.sink.events)
		}
	})
}

type assertedAuthorityEdge struct {
	s        *server
	provider *auth.BasicAuthProvider
	handler  http.Handler
}

func assertedAuthorityEdgeFixture(t *testing.T, env map[string]string) assertedAuthorityEdge {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
	tenant := `","tenant":"` + edgeRouteTenant
	env["CORDUM_API_KEYS"] = `[` +
		`{"key":"` + assertedAuthorityEdgeUserKey + tenant + `","role":"user"},` +
		`{"key":"` + assertedAuthorityEdgeBoundUserKey + tenant + `","role":"user","principal_id":"principal-edge-user"},` +
		`{"key":"` + assertedAuthorityEdgeAdminKey + tenant + `","role":"admin"},` +
		`{"key":"` + assertedAuthorityEdgeBoundAdminKey + tenant + `","role":"admin","principal_id":"principal-reviewer"}` +
		`]`
	provider := newBasicAuthForTest(t, env)
	s.auth = provider
	mux := http.NewServeMux()
	if err := s.registerRoutes(mux); err != nil {
		t.Fatalf("registerRoutes() error = %v, want nil", err)
	}
	handler := apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
	return assertedAuthorityEdge{s: s, provider: provider, handler: handler}
}

// assertedAuthorityEdgeKeyPrincipal is the principal a test key is bound to,
// or "" for an unbound key.
func assertedAuthorityEdgeKeyPrincipal(apiKey string) string {
	switch apiKey {
	case assertedAuthorityEdgeBoundUserKey:
		return "principal-edge-user"
	case assertedAuthorityEdgeBoundAdminKey:
		return "principal-reviewer"
	default:
		return ""
	}
}

// request serves one request through the real middleware chain. When it sends
// X-Principal-Id, a twin request authenticated by the same provider first
// proves the header is asserted for an unbound key and ignored for a bound one.
func (f assertedAuthorityEdge) request(t *testing.T, method, path, body, apiKey, headerPrincipal string) *httptest.ResponseRecorder {
	t.Helper()
	build := func() *http.Request {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-API-Key", apiKey)
		req.Header.Set("X-Tenant-ID", edgeRouteTenant)
		req.Header.Set("Content-Type", "application/json")
		if headerPrincipal != "" {
			req.Header.Set("X-Principal-Id", headerPrincipal)
		}
		return req
	}
	if headerPrincipal != "" {
		bound := assertedAuthorityEdgeKeyPrincipal(apiKey)
		want := bound
		if bound == "" {
			want = headerPrincipal
		}
		ac, err := f.provider.AuthenticateHTTP(build())
		if err != nil || ac == nil {
			t.Fatalf("AuthenticateHTTP() = (%v, %v), want an auth context and nil error", ac, err)
		}
		if ac.PrincipalID != want || ac.PrincipalAsserted != (bound == "") || ac.AuthenticatedPrincipal() != bound {
			t.Fatalf("twin auth context = principal:%q asserted:%v authenticated:%q, want principal:%q asserted:%v authenticated:%q",
				ac.PrincipalID, ac.PrincipalAsserted, ac.AuthenticatedPrincipal(), want, bound == "", bound)
		}
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, build())
	return rec
}

func TestAuthorityAssertedPrincipal_EdgeApprovalAccessIgnoresAssertedPrincipal(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f := assertedAuthorityEdgeFixture(t, env)
		own := seedGatewayEdgeApproval(t, f.s, edgeRouteTenant, "principal-edge-user", "own")
		other := seedGatewayEdgeApproval(t, f.s, edgeRouteTenant, "principal-other-member", "other")
		list := "/api/v1/edge/approvals?status=pending&limit=10"

		// An unbound key naming the owner gets no own-approval access.
		rec := f.request(t, http.MethodGet, list, "", assertedAuthorityEdgeUserKey, "principal-edge-user")
		assertedAuthorityStatus(t, rec, http.StatusOK)
		if page := decodeEdgeApprovalPage(t, rec); len(page.Items) != 0 || page.NextCursor != "" {
			t.Fatalf("asserted list = %d items cursor %q, want 0 items and an empty cursor", len(page.Items), page.NextCursor)
		}
		rec = f.request(t, http.MethodGet, assertedAuthorityApprovals+own.ApprovalRef, "", assertedAuthorityEdgeUserKey, "principal-edge-user")
		assertEdgeErrorShape(t, rec, http.StatusNotFound, edgeErrCodeNotFound)
		rec = f.request(t, http.MethodPost, assertedAuthorityApprovals+own.ApprovalRef+"/wait", `{"timeout_ms":1}`, assertedAuthorityEdgeUserKey, "principal-edge-user")
		assertEdgeErrorShape(t, rec, http.StatusNotFound, edgeErrCodeNotFound)

		// A key bound to the owner keeps exactly its own approvals, whatever the header names.
		rec = f.request(t, http.MethodGet, list, "", assertedAuthorityEdgeBoundUserKey, "principal-other-member")
		assertedAuthorityStatus(t, rec, http.StatusOK)
		assertEdgeApprovalPageRefs(t, decodeEdgeApprovalPage(t, rec), own.ApprovalRef)
		rec = f.request(t, http.MethodGet, assertedAuthorityApprovals+own.ApprovalRef, "", assertedAuthorityEdgeBoundUserKey, "principal-other-member")
		assertEdgeApprovalResponseRef(t, rec, own.ApprovalRef)
		rec = f.request(t, http.MethodGet, assertedAuthorityApprovals+other.ApprovalRef, "", assertedAuthorityEdgeBoundUserKey, "principal-other-member")
		assertEdgeErrorShape(t, rec, http.StatusNotFound, edgeErrCodeNotFound)
	})
}

// approve seeds an approval requested by principal-edge-a and approves it.
func (f assertedAuthorityEdge) approve(t *testing.T, suffix, apiKey, headerPrincipal string) edgecore.EdgeApproval {
	t.Helper()
	approval := seedGatewayEdgeApproval(t, f.s, edgeRouteTenant, "principal-edge-a", suffix)
	rec := f.request(t, http.MethodPost, assertedAuthorityApprovals+approval.ApprovalRef+"/approve", `{"reason":"assertedauthority"}`, apiKey, headerPrincipal)
	assertedAuthorityStatus(t, rec, http.StatusOK)
	var approved edgecore.EdgeApproval
	decodeEdgeRouteJSON(t, rec, &approved)
	if approved.Status != edgecore.ApprovalStatusApproved {
		t.Fatalf("%s approval status = %q, want %q", suffix, approved.Status, edgecore.ApprovalStatusApproved)
	}
	return approved
}

func TestAuthorityAssertedPrincipal_EdgeApprovalResolverIgnoresAssertedPrincipal(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f := assertedAuthorityEdgeFixture(t, env)
		base := f.approve(t, "base", assertedAuthorityEdgeAdminKey, "")
		if !strings.HasPrefix(base.ResolverID, "apikey:") || base.ResolvedBy != "role:admin" {
			t.Fatalf("base resolver = id:%q by:%q, want id apikey:<hash> and by role:admin", base.ResolverID, base.ResolvedBy)
		}
		asserted := f.approve(t, "asserted", assertedAuthorityEdgeAdminKey, "principal-asserted-approver")
		if asserted.ResolverID != base.ResolverID || asserted.ResolvedBy != base.ResolvedBy {
			t.Fatalf("asserted resolver = id:%q by:%q, want the headerless id:%q by:%q",
				asserted.ResolverID, asserted.ResolvedBy, base.ResolverID, base.ResolvedBy)
		}
		if strings.Contains(asserted.ResolverID+"|"+asserted.ResolvedBy, "principal-asserted-approver") {
			t.Fatalf("asserted resolver = id:%q by:%q, want no principal-asserted-approver", asserted.ResolverID, asserted.ResolvedBy)
		}
		bound := f.approve(t, "bound", assertedAuthorityEdgeBoundAdminKey, "principal-asserted-approver")
		if bound.ResolverID != "principal-reviewer" || bound.ResolvedBy != "principal:principal-reviewer|role:admin" {
			t.Fatalf("bound resolver = id:%q by:%q, want id principal-reviewer and by principal:principal-reviewer|role:admin",
				bound.ResolverID, bound.ResolvedBy)
		}

		// An asserted principal equal to the requester is still refused as self-approval.
		self := seedGatewayEdgeApproval(t, f.s, edgeRouteTenant, "principal-edge-a", "self")
		rec := f.request(t, http.MethodPost, assertedAuthorityApprovals+self.ApprovalRef+"/approve", `{"reason":"assertedauthority"}`,
			assertedAuthorityEdgeAdminKey, "principal-edge-a")
		assertEdgeErrorShape(t, rec, http.StatusForbidden, "self_approval_denied")
		rec = f.request(t, http.MethodGet, assertedAuthorityApprovals+self.ApprovalRef, "", assertedAuthorityEdgeBoundAdminKey, "")
		assertEdgeApprovalResponseRef(t, rec, self.ApprovalRef)
		var stored edgecore.EdgeApproval
		decodeEdgeRouteJSON(t, rec, &stored)
		if stored.Status != edgecore.ApprovalStatusPending {
			t.Fatalf("self-refused approval status = %q, want %q", stored.Status, edgecore.ApprovalStatusPending)
		}
	})
}
