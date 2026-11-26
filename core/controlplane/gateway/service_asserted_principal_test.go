package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/controlplane/copilot"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	edgecore "github.com/cordum/cordum/core/edge"
)

const assertedServiceUnboundKey = "asserted-service-unbound-static-key-0000"

const assertedServiceBoundKey = "asserted-service-bound-static-key-000000"

const assertedServiceCopilotSessionID = "sess-abc123"

const assertedServiceCopilotTitle = "assertedService session"

// assertedServiceEachProfile runs fn in a "production" and a "development"
// subtest. Each builds a fresh gateway first, then a BasicAuthProvider from a
// pinned environment, so only the provider sees the profile. Production sets
// CORDUM_ALLOW_HEADER_PRINCIPAL=true; development leaves the flag unset, since
// it accepts header principals by default. The static keys carry the tenant and
// role, and are one unbound key and one key bound to boundPrincipal.
func assertedServiceEachProfile(t *testing.T, tenant, role, boundPrincipal string, fn func(t *testing.T, s *server, p *auth.BasicAuthProvider)) {
	t.Helper()
	for _, profile := range []string{"production", "development"} {
		t.Run(profile, func(t *testing.T) {
			s, _, _ := newTestGateway(t)
			flag := ""
			if profile == "production" {
				flag = "true"
			}
			keys, err := json.Marshal([]map[string]string{
				{"key": assertedServiceUnboundKey, "tenant": tenant, "role": role},
				{"key": assertedServiceBoundKey, "tenant": tenant, "role": role, "principal_id": boundPrincipal},
			})
			if err != nil {
				t.Fatalf("marshal api keys: %v", err)
			}
			s.auth = newBasicAuthForTest(t, map[string]string{
				"CORDUM_ENV":                    profile,
				"CORDUM_ALLOW_HEADER_PRINCIPAL": flag,
				"CORDUM_API_KEYS":               string(keys),
			})
			fn(t, s, s.auth.(*auth.BasicAuthProvider))
		})
	}
}

// assertedServiceRequest is the request a caller sends to path with key and an
// X-Principal-Id header.
func assertedServiceRequest(method, path, key, principal string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-Principal-Id", principal)
	return req
}

// assertedServiceExpectContext checks the context the provider built: the
// role, the principal, whether the caller only asserted it, and what
// AuthenticatedPrincipal reports for it.
func assertedServiceExpectContext(t *testing.T, ac *auth.AuthContext, wantRole, wantPrincipal string, wantAsserted bool) {
	t.Helper()
	if ac.Role != wantRole {
		t.Fatalf("Role = %q, want %q", ac.Role, wantRole)
	}
	if ac.PrincipalID != wantPrincipal {
		t.Fatalf("PrincipalID = %q, want %q", ac.PrincipalID, wantPrincipal)
	}
	if ac.PrincipalAsserted != wantAsserted {
		t.Fatalf("PrincipalAsserted = %v, want %v", ac.PrincipalAsserted, wantAsserted)
	}
	wantAuthenticated := wantPrincipal
	if wantAsserted {
		wantAuthenticated = ""
	}
	if got := ac.AuthenticatedPrincipal(); got != wantAuthenticated {
		t.Fatalf("AuthenticatedPrincipal() = %q, want %q", got, wantAuthenticated)
	}
}

func TestServiceAssertedPrincipal_LLMIngest(t *testing.T) {
	const path = "/api/v1/edge/llm/events"
	assertedServiceEachProfile(t, edgeRouteTenant, testLLMProxyRole, "llm-proxy-1", func(t *testing.T, s *server, p *auth.BasicAuthProvider) {
		enableLLMIngest(t)
		s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
		setupLLMIngestRBAC(t, s)
		session, execution := createLLMIngestParents(t, s, "asserted", edgecore.AdapterLLMProxy)

		// An unbound key whose caller names the proxy in X-Principal-Id is
		// refused like a missing principal, and nothing is stored.
		ac, err := p.AuthenticateHTTP(assertedServiceRequest(http.MethodPost, path, assertedServiceUnboundKey, "llm-proxy-1"))
		if err != nil {
			t.Fatalf("AuthenticateHTTP(unbound key) error = %v, want nil", err)
		}
		assertedServiceExpectContext(t, ac, testLLMProxyRole, "llm-proxy-1", true)
		rr := postLLMIngestWithAuth(t, s, ac, llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-asserted-1", "hello"))
		assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
		if got := len(readEdgeEventsFromStore(t, s, execution.ExecutionID).Items); got != 0 {
			t.Fatalf("stored events after the asserted principal = %d, want 0", got)
		}

		// The same role on a key bound to the proxy ingests; the header's
		// other name is ignored.
		ac, err = p.AuthenticateHTTP(assertedServiceRequest(http.MethodPost, path, assertedServiceBoundKey, "llm-proxy-2"))
		if err != nil {
			t.Fatalf("AuthenticateHTTP(bound key) error = %v, want nil", err)
		}
		assertedServiceExpectContext(t, ac, testLLMProxyRole, "llm-proxy-1", false)
		rr = postLLMIngestWithAuth(t, s, ac, llmIngestBody("llm-proxy-1", session.SessionID, execution.ExecutionID, "evt-asserted-2", "hello"))
		if rr.Code != http.StatusCreated {
			t.Fatalf("bound key status = %d, want 201; body=%s", rr.Code, rr.Body.String())
		}
		if got := decodeLLMIngestResponse(t, rr).AcceptedCount; got != 1 {
			t.Fatalf("bound key accepted_count = %d, want 1", got)
		}
		if got := len(readEdgeEventsFromStore(t, s, execution.ExecutionID).Items); got != 1 {
			t.Fatalf("stored events after the bound key = %d, want 1", got)
		}
	})
}

func TestServiceAssertedPrincipal_RuntimeIngest(t *testing.T) {
	const path = "/api/v1/edge/runtime/events"
	assertedServiceEachProfile(t, edgeRouteTenant, testRuntimeCollectorRole, "collector-a", func(t *testing.T, s *server, p *auth.BasicAuthProvider) {
		enableRuntimeIngest(t)
		s.edgeStore = edgecore.NewRedisStoreFromClient(s.jobStore.Client())
		setupRuntimeIngestRBAC(t, s)
		session, execution := createRuntimeIngestBoundParents(t, s, "asserted", "collector-a")

		// An unbound key whose caller names the collector in X-Principal-Id
		// is refused like a missing principal, and nothing is stored.
		ac, err := p.AuthenticateHTTP(assertedServiceRequest(http.MethodPost, path, assertedServiceUnboundKey, "collector-a"))
		if err != nil {
			t.Fatalf("AuthenticateHTTP(unbound key) error = %v, want nil", err)
		}
		assertedServiceExpectContext(t, ac, testRuntimeCollectorRole, "collector-a", true)
		rr := postRuntimeIngestWithAuth(t, s, ac, runtimeIngestBodyForSource("collector-a", session.SessionID, execution.ExecutionID, edgeRouteTenant, "evt-asserted-1"))
		assertEdgeErrorShape(t, rr, http.StatusForbidden, edgeErrCodeAccessDenied)
		if got := len(readEdgeEventsFromStore(t, s, execution.ExecutionID).Items); got != 0 {
			t.Fatalf("stored events after the asserted principal = %d, want 0", got)
		}

		// The same role on a key bound to the collector ingests; the
		// header's other name is ignored.
		ac, err = p.AuthenticateHTTP(assertedServiceRequest(http.MethodPost, path, assertedServiceBoundKey, "collector-b"))
		if err != nil {
			t.Fatalf("AuthenticateHTTP(bound key) error = %v, want nil", err)
		}
		assertedServiceExpectContext(t, ac, testRuntimeCollectorRole, "collector-a", false)
		rr = postRuntimeIngestWithAuth(t, s, ac, runtimeIngestBodyForSource("collector-a", session.SessionID, execution.ExecutionID, edgeRouteTenant, "evt-asserted-2"))
		if rr.Code != http.StatusCreated {
			t.Fatalf("bound key status = %d, want 201; body=%s", rr.Code, rr.Body.String())
		}
		var resp runtimeIngestResponseShape
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode bound key response: %v body=%s", err, rr.Body.String())
		}
		if resp.AcceptedCount != 1 || resp.DroppedCount != 0 {
			t.Fatalf("bound key accepted/dropped = %d/%d, want 1/0", resp.AcceptedCount, resp.DroppedCount)
		}
		if got := len(readEdgeEventsFromStore(t, s, execution.ExecutionID).Items); got != 1 {
			t.Fatalf("stored events after the bound key = %d, want 1", got)
		}
	})
}

func TestServiceAssertedPrincipal_CopilotSessionOwner(t *testing.T) {
	path := "/api/v1/copilot/sessions/" + assertedServiceCopilotSessionID
	assertedServiceEachProfile(t, "tenant-a", "viewer", "alice", func(t *testing.T, s *server, p *auth.BasicAuthProvider) {
		now := time.Now().UTC()
		store := &stubCopilotSessionStore{session: &copilot.CopilotSession{
			ID:        assertedServiceCopilotSessionID,
			Title:     assertedServiceCopilotTitle,
			UserID:    "alice",
			CreatedAt: now.Add(-time.Hour),
			UpdatedAt: now,
		}}
		s.copilotStore = store
		get := func(key, principal string) (*auth.AuthContext, *httptest.ResponseRecorder) {
			t.Helper()
			req := assertedServiceRequest(http.MethodGet, path, key, principal)
			req.Header.Set("X-Tenant-ID", "tenant-a")
			ac, err := p.AuthenticateHTTP(req)
			if err != nil {
				t.Fatalf("AuthenticateHTTP(%s) error = %v, want nil", principal, err)
			}
			req = withAuth(req, ac)
			req.SetPathValue("sessionId", assertedServiceCopilotSessionID)
			rr := httptest.NewRecorder()
			s.handleGetCopilotSession(rr, req)
			return ac, rr
		}

		// An unbound key whose caller names the owner in X-Principal-Id gets
		// 401 and no session data, and the store is never asked.
		ac, rr := get(assertedServiceUnboundKey, "alice")
		assertedServiceExpectContext(t, ac, "viewer", "alice", true)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("asserted principal status = %d, want 401; body=%s", rr.Code, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), assertedServiceCopilotTitle) {
			t.Fatalf("asserted principal body = %q, want a refusal without the session", rr.Body.String())
		}
		if store.lastSessionID != "" || store.lastUserID != "" {
			t.Fatalf("store asked for session %q as user %q, want it never asked", store.lastSessionID, store.lastUserID)
		}

		// The same role on a key bound to the owner reads the session; the
		// header's other name is ignored.
		ac, rr = get(assertedServiceBoundKey, "mallory")
		assertedServiceExpectContext(t, ac, "viewer", "alice", false)
		if rr.Code != http.StatusOK {
			t.Fatalf("bound key status = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if store.lastUserID != "alice" {
			t.Fatalf("store user = %q, want alice", store.lastUserID)
		}
		if !strings.Contains(rr.Body.String(), assertedServiceCopilotTitle) {
			t.Fatalf("bound key body = %s, want it to carry %q", rr.Body.String(), assertedServiceCopilotTitle)
		}
	})
}
