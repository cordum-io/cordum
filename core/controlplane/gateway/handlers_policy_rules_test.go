package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

// newRuleStoreOnTestGateway wires a real RuleRedisStore into the test
// server using the same miniredis instance newTestGateway already
// created. We use the real store (not a mock) so the handler tests
// exercise the same Lua CAS path production traffic hits.
func newRuleStoreOnTestGateway(t *testing.T, s *server) *policy.RuleRedisStore {
	t.Helper()
	rs := policy.NewRedisRuleStoreFromClient(s.jobStore.Client())
	s.policyRuleStore = rs
	return rs
}

// authedAdmin returns an *http.Request with an admin AuthContext bound,
// the JSON body, and the Content-Type header set. Mirrors the pattern
// used elsewhere in the package (cf. unifiedPolicyEvaluate tests).
func authedAdminPolicyRule(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-acme")
	return withAuth(req, &auth.AuthContext{
		Tenant:      "tenant-acme",
		Role:        "admin",
		PrincipalID: "alice",
	})
}

func samplePolicyRuleCreateBody(id string) string {
	// Server fills version/audit/status; client only supplies the
	// authoring shape (id, name, type, scope, match, decide).
	return `{
	  "id": "` + id + `",
	  "name": "Sample rule ` + id + `",
	  "type": "input",
	  "scope": {"kind":"tenant","value":"tenant-acme"},
	  "match": {"topics":["job.acme.evaluate"]},
	  "decide": {"decision":"deny","reason":"test"}
	}`
}

// --- POST /api/v1/policy/rules -----------------------------------------

func TestCreatePolicyRuleReturnsCreatedWithServerSetMetadata(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", samplePolicyRuleCreateBody("rule-h1"))
	rec := httptest.NewRecorder()

	s.handleCreatePolicyRule(rec, req)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "/api/v1/policy/rules/rule-h1", rec.Header().Get("Location"))
	var got policy.Rule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "rule-h1", got.ID)
	require.Equal(t, "v1", got.Version, "Version is server-set on create")
	require.False(t, got.Audit.CreatedAt.IsZero(), "CreatedAt is server-set on create")
	require.False(t, got.Audit.UpdatedAt.IsZero(), "UpdatedAt is server-set on create")
	require.Equal(t, policy.RuleStatusDraft, got.Status, "Status defaults to draft")
}

func TestCreatePolicyRuleRejectsClientVersion(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	body := `{"id":"rule-fakeversion","name":"x","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"version":"v999","match":{},"decide":{"decision":"deny"}}`
	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", body)
	rec := httptest.NewRecorder()

	s.handleCreatePolicyRule(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "version")
}

func TestCreatePolicyRuleRejectsClientAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	body := `{"id":"rule-fakeaudit","name":"x","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"audit":{"created_by":"imposter"},"match":{},"decide":{"decision":"deny"}}`
	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", body)
	rec := httptest.NewRecorder()

	s.handleCreatePolicyRule(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "audit")
}

func TestCreatePolicyRuleRejectsMissingID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	body := `{"name":"x","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"match":{},"decide":{"decision":"deny"}}`
	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", body)
	rec := httptest.NewRecorder()

	s.handleCreatePolicyRule(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestCreatePolicyRuleDuplicateIDReturns409(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	body := samplePolicyRuleCreateBody("rule-dup")
	first := httptest.NewRecorder()
	s.handleCreatePolicyRule(first, authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", body))
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	second := httptest.NewRecorder()
	s.handleCreatePolicyRule(second, authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", body))
	require.Equal(t, http.StatusConflict, second.Code, second.Body.String())
}

func TestCreatePolicyRuleRejectsUnauthenticated(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/rules", strings.NewReader(samplePolicyRuleCreateBody("rule-noauth")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	s.handleCreatePolicyRule(rec, req)
	require.NotEqual(t, http.StatusCreated, rec.Code, "creation must require auth")
}

// --- PUT /api/v1/policy/rules/{id} -------------------------------------

func createPolicyRuleViaHandler(t *testing.T, s *server, id string) policy.Rule {
	t.Helper()
	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", samplePolicyRuleCreateBody(id))
	rec := httptest.NewRecorder()
	s.handleCreatePolicyRule(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var got policy.Rule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	return got
}

// putPolicyRuleRequest builds a PUT request that hits the handler's
// path-parameter contract. We use http.NewRequestWithContext + SetPathValue
// because httptest.NewRequest doesn't populate r.PathValue() the way mux
// would in production.
func putPolicyRuleRequest(t *testing.T, id, ifMatch, body string) *http.Request {
	t.Helper()
	req := authedAdminPolicyRule(http.MethodPut, "/api/v1/policy/rules/"+id, body)
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	req.SetPathValue("id", id)
	return req
}

func TestUpdatePolicyRuleBumpsVersionWithIfMatch(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)
	created := createPolicyRuleViaHandler(t, s, "rule-update-1")

	updateBody := `{"id":"rule-update-1","name":"Updated name","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"match":{"topics":["job.acme.evaluate"]},"decide":{"decision":"allow"}}`
	req := putPolicyRuleRequest(t, "rule-update-1", created.Version, updateBody)
	rec := httptest.NewRecorder()

	s.handleUpdatePolicyRule(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got policy.Rule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "v2", got.Version, "Update must bump version v1 -> v2")
	require.Equal(t, "Updated name", got.Name)
}

func TestUpdatePolicyRuleMissingIfMatchReturns428(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)
	createPolicyRuleViaHandler(t, s, "rule-update-2")

	updateBody := samplePolicyRuleCreateBody("rule-update-2")
	req := putPolicyRuleRequest(t, "rule-update-2", "", updateBody) // no If-Match
	rec := httptest.NewRecorder()

	s.handleUpdatePolicyRule(rec, req)

	require.Equal(t, http.StatusPreconditionRequired, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "If-Match")
}

func TestUpdatePolicyRuleStaleVersionReturns409WithCurrentMetadata(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)
	createPolicyRuleViaHandler(t, s, "rule-update-3")

	// First update succeeds — bumps to v2.
	body := `{"id":"rule-update-3","name":"v2","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"match":{},"decide":{"decision":"deny"}}`
	rec1 := httptest.NewRecorder()
	s.handleUpdatePolicyRule(rec1, putPolicyRuleRequest(t, "rule-update-3", "v1", body))
	require.Equal(t, http.StatusOK, rec1.Code, rec1.Body.String())

	// Second update with stale If-Match: v1 must 409.
	body2 := `{"id":"rule-update-3","name":"v3","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"match":{},"decide":{"decision":"deny"}}`
	rec2 := httptest.NewRecorder()
	s.handleUpdatePolicyRule(rec2, putPolicyRuleRequest(t, "rule-update-3", "v1", body2))

	require.Equal(t, http.StatusConflict, rec2.Code, rec2.Body.String())
	var conflictBody map[string]any
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &conflictBody))
	require.Equal(t, "stale_version", conflictBody["error"], "stale 409 must include error=stale_version")
	require.Equal(t, "v2", conflictBody["current_version"], "stale 409 must include current_version")
	require.NotEmpty(t, conflictBody["current_audit_hash"], "stale 409 must include current_audit_hash")
}

func TestUpdatePolicyRuleNotFoundReturns404(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)

	body := `{"id":"missing","name":"x","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"match":{},"decide":{"decision":"deny"}}`
	req := putPolicyRuleRequest(t, "missing", "v1", body)
	rec := httptest.NewRecorder()

	s.handleUpdatePolicyRule(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

func TestUpdatePolicyRulePathIDOverridesBodyID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)
	createPolicyRuleViaHandler(t, s, "rule-path-1")

	// Body claims ID=rule-path-X; path says rule-path-1. Path wins.
	body := `{"id":"rule-path-X","name":"path-wins","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"match":{},"decide":{"decision":"deny"}}`
	req := putPolicyRuleRequest(t, "rule-path-1", "v1", body)
	rec := httptest.NewRecorder()

	s.handleUpdatePolicyRule(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got policy.Rule
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "rule-path-1", got.ID, "path id must win over body id")
}
