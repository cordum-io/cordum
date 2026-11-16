package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

// These tests pin that an Edge session's owner (principal_id) is only a
// principal a credential vouches for (task-cf94ebba). A principal named only by
// X-Principal-Id, on a key with no bound principal, is kept as the session's
// principal_display_name and never owns a session. Both profiles run, through
// the real BasicAuthProvider and middleware chain.

const edgeSessionOwnerCreateBody = `{"agent_product":"claude-code","mode":"local-dev","policy_snapshot":"snap-edge-owner","policy_mode":"observe"}`

// edgeSessionOwnerFixture is the asserted-authority Edge fixture with a policy
// stub that allows every action, so evaluate answers 200 once access passes.
// The stub records each policy request.
func edgeSessionOwnerFixture(t *testing.T, env map[string]string) (assertedAuthorityEdge, *edgeEvaluateStubSafetyClient) {
	t.Helper()
	f := assertedAuthorityEdgeFixture(t, env)
	stub := &edgeEvaluateStubSafetyClient{
		response: &pb.PolicyCheckResponse{Decision: pb.DecisionType_DECISION_TYPE_ALLOW, Reason: "edge session owner test"},
	}
	f.s.safetyClient = stub
	return f, stub
}

func (f assertedAuthorityEdge) createSession(t *testing.T, body, apiKey, headerPrincipal string) edgeSessionCreateResponseJSON {
	t.Helper()
	rec := f.request(t, http.MethodPost, "/api/v1/edge/sessions", body, apiKey, headerPrincipal)
	assertedAuthorityStatus(t, rec, http.StatusCreated)
	var created edgeSessionCreateResponseJSON
	decodeEdgeRouteJSON(t, rec, &created)
	return created
}

func (f assertedAuthorityEdge) evaluate(t *testing.T, session edgeSessionCreateResponseJSON, apiKey, headerPrincipal string) *httptest.ResponseRecorder {
	t.Helper()
	body := edgeEvaluateBody(session.SessionID, session.ExecutionID, edgeRouteTenant, "Bash", map[string]any{"command": "npm test"})
	return f.request(t, http.MethodPost, "/api/v1/edge/evaluate", body, apiKey, headerPrincipal)
}

func TestEdgeSessionOwner_AssertedPrincipalCannotEvaluateBoundSession(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f, _ := edgeSessionOwnerFixture(t, env)
		session := f.createSession(t, edgeSessionOwnerCreateBody, assertedAuthorityEdgeBoundUserKey, "")
		if got := session.Session; got.PrincipalID != "principal-edge-user" || got.PrincipalDisplayName != "" {
			t.Fatalf("bound session principal_id=%q principal_display_name=%q, want %q and empty", got.PrincipalID, got.PrincipalDisplayName, "principal-edge-user")
		}

		// A key with no bound principal that names the owner is refused.
		rec := f.evaluate(t, session, assertedAuthorityEdgeUserKey, "principal-edge-user")
		assertEdgeErrorShape(t, rec, http.StatusForbidden, edgeErrCodeAccessDenied)

		// The owner's own key still evaluates.
		assertedAuthorityStatus(t, f.evaluate(t, session, assertedAuthorityEdgeBoundUserKey, ""), http.StatusOK)
	})
}

func TestEdgeSessionOwner_HeaderOnlySessionEvaluatesForItsCreator(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f, stub := edgeSessionOwnerFixture(t, env)
		session := f.createSession(t, edgeSessionOwnerCreateBody, assertedAuthorityEdgeUserKey, "dev-alice")
		assertedAuthorityStatus(t, f.evaluate(t, session, assertedAuthorityEdgeUserKey, "dev-alice"), http.StatusOK)

		// Policy input still names the asserted principal: attribution, not ownership.
		reqs := stub.capturedRequests()
		if n := len(reqs); n == 0 || reqs[n-1].GetPrincipalId() != "dev-alice" {
			t.Fatalf("policy requests = %d, want the last one for principal %q", n, "dev-alice")
		}
	})
}

func TestEdgeSessionOwner_HeaderOnlySessionKeepsAssertedNameAsDisplayLabel(t *testing.T) {
	assertedAuthorityProfiles(t, func(t *testing.T, env map[string]string) {
		f, _ := edgeSessionOwnerFixture(t, env)
		session := f.createSession(t, edgeSessionOwnerCreateBody, assertedAuthorityEdgeUserKey, "dev-alice")
		if got := session.Session; got.PrincipalID != "" || got.PrincipalDisplayName != "dev-alice" {
			t.Fatalf("response principal_id=%q principal_display_name=%q, want empty and %q", got.PrincipalID, got.PrincipalDisplayName, "dev-alice")
		}
		stored, found, err := f.s.edgeStore.GetSession(context.Background(), edgeRouteTenant, session.SessionID)
		if err != nil || !found || stored == nil {
			t.Fatalf("GetSession() = (%v, %v, %v), want the stored session", stored, found, err)
		}
		if stored.PrincipalID != "" || stored.PrincipalDisplayName != "dev-alice" {
			t.Fatalf("stored principal_id=%q principal_display_name=%q, want empty and %q", stored.PrincipalID, stored.PrincipalDisplayName, "dev-alice")
		}

		// The session has no owner, so the principal filter does not match it.
		rec := f.request(t, http.MethodGet, "/api/v1/edge/sessions?principal_id=dev-alice&limit=10", "", assertedAuthorityEdgeUserKey, "dev-alice")
		assertedAuthorityStatus(t, rec, http.StatusOK)
		var page edgeSessionPageJSON
		decodeEdgeRouteJSON(t, rec, &page)
		if len(page.Items) != 0 {
			t.Fatalf("?principal_id=dev-alice listed %d sessions, want 0", len(page.Items))
		}

		// A display label the client sets is kept as sent.
		labelled := f.createSession(t, `{"agent_product":"claude-code","mode":"local-dev","policy_mode":"observe","principal_display_name":"Alice Example"}`, assertedAuthorityEdgeUserKey, "dev-alice")
		if got := labelled.Session; got.PrincipalID != "" || got.PrincipalDisplayName != "Alice Example" {
			t.Fatalf("labelled principal_id=%q principal_display_name=%q, want empty and %q", got.PrincipalID, got.PrincipalDisplayName, "Alice Example")
		}
	})
}
