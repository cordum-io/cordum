package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/policy"
	"github.com/stretchr/testify/require"
)

func setupAddRuleToBundleTest(t *testing.T) (*server, *memoryPolicyBundleStore) {
	t.Helper()
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	newRuleStoreOnTestGateway(t, s)
	bundleStore := newMemoryPolicyBundleStore()
	require.NoError(t, bundleStore.CreateBundle(context.Background(), &policy.Bundle{
		ID:           "bundle-1",
		Name:         "Test bundle",
		ScopeBinding: policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"},
	}))
	s.policyBundleStore = bundleStore
	return s, bundleStore
}

func mustCreateRuleViaHandler(t *testing.T, s *server, id string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handleCreatePolicyRule(rec, authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/rules", samplePolicyRuleCreateBody(id)))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func newAddRuleToBundleRequest(t *testing.T, bundleID, ruleID string) *http.Request {
	t.Helper()
	body := `{"rule_id":"` + ruleID + `"}`
	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/bundles/"+bundleID+"/rules", body)
	req.SetPathValue("id", bundleID)
	return req
}

func TestAddRuleToBundleHandlerSuccess(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)
	mustCreateRuleViaHandler(t, s, "rule-bound-1")

	rec := httptest.NewRecorder()
	s.handleAddRuleToBundle(rec, newAddRuleToBundleRequest(t, "bundle-1", "rule-bound-1"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got policy.Bundle
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"rule-bound-1"}, got.RuleIDs)
}

func TestAddRuleToBundleHandlerIdempotent(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)
	mustCreateRuleViaHandler(t, s, "rule-idemp")

	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		s.handleAddRuleToBundle(rec, newAddRuleToBundleRequest(t, "bundle-1", "rule-idemp"))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var got policy.Bundle
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Equal(t, []string{"rule-idemp"}, got.RuleIDs, "idempotent: RuleIDs unchanged on repeat add")
	}
}

func TestAddRuleToBundleHandlerRuleNotFoundDisambiguates(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)

	rec := httptest.NewRecorder()
	s.handleAddRuleToBundle(rec, newAddRuleToBundleRequest(t, "bundle-1", "rule-missing"))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "rule_not_found", body["error"], "missing rule must surface rule_not_found, not bundle_not_found")
}

func TestAddRuleToBundleHandlerBundleNotFoundDisambiguates(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)
	mustCreateRuleViaHandler(t, s, "rule-orphan")

	rec := httptest.NewRecorder()
	s.handleAddRuleToBundle(rec, newAddRuleToBundleRequest(t, "bundle-missing", "rule-orphan"))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "bundle_not_found", body["error"], "missing bundle must surface bundle_not_found, not rule_not_found")
}

func TestAddRuleToBundleHandlerRejectsEmptyRuleID(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)

	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/bundles/bundle-1/rules", `{"rule_id":""}`)
	req.SetPathValue("id", "bundle-1")
	rec := httptest.NewRecorder()

	s.handleAddRuleToBundle(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "rule_id")
}

func TestAddRuleToBundleHandlerRejectsEmptyBundleID(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)
	mustCreateRuleViaHandler(t, s, "rule-x")

	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/bundles//rules", `{"rule_id":"rule-x"}`)
	req.SetPathValue("id", "") // explicitly empty
	rec := httptest.NewRecorder()

	s.handleAddRuleToBundle(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestAddRuleToBundleHandlerRejectsInvalidJSON(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)

	req := authedAdminPolicyRule(http.MethodPost, "/api/v1/policy/bundles/bundle-1/rules", `not-json`)
	req.SetPathValue("id", "bundle-1")
	rec := httptest.NewRecorder()

	s.handleAddRuleToBundle(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestAddRuleToBundleHandlerRejectsUnauthenticated(t *testing.T) {
	s, _ := setupAddRuleToBundleTest(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/bundle-1/rules", strings.NewReader(`{"rule_id":"rule-x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", "bundle-1")
	rec := httptest.NewRecorder()

	s.handleAddRuleToBundle(rec, req)
	require.NotEqual(t, http.StatusOK, rec.Code, "binding must require auth")
}
