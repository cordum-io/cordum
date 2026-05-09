package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordum/cordum/core/audit"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/policy"
	pb "github.com/cordum/cordum/core/protocol/pb/v1"
	"github.com/stretchr/testify/require"
)

func TestPolicyEvaluateUnifiedJobDispatchesToSafetyKernelAndReturnsDecision(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	auditSender := &testAuditSender{}
	s.auditExporter = auditSender
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_DENY,
		Reason:         "blocked input token",
		RuleId:         "job-input-deny",
		PolicySnapshot: "snap-job",
	})

	rec := postUnifiedPolicyEvaluate(t, s, unifiedJobEvaluateBody("job-input-deny"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeUnifiedPolicyEvaluateResponse(t, rec)
	require.Equal(t, policy.DecisionSourceJob, got.Decision.Source)
	require.Equal(t, policy.DecisionDeny, got.Decision.Type)
	require.Equal(t, "job-input-deny", got.Decision.RuleID)
	require.NotEmpty(t, got.Decision.Trace)
	require.Equal(t, "blocked input token", got.Decision.Trace[0].Reason)

	safety.mu.Lock()
	defer safety.mu.Unlock()
	require.NotNil(t, safety.lastReq)
	require.Equal(t, "job.acme.evaluate", safety.lastReq.GetTopic())
	require.Equal(t, "tenant-acme", safety.lastReq.GetTenant())
	require.Equal(t, []byte("contains blocked-token"), safety.lastReq.GetInputContent())
	requirePolicyDecisionAuditOrder(t, auditSender)
}

func TestPolicyEvaluateUnifiedEdgeDispatchesToEdgeClassifierAndEmitsAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	auditSender := &testAuditSender{}
	s.auditExporter = auditSender

	rec := postUnifiedPolicyEvaluate(t, s, unifiedEdgeEvaluateBody("edge-deny"))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeUnifiedPolicyEvaluateResponse(t, rec)
	require.Equal(t, policy.DecisionSourceEdge, got.Decision.Source)
	require.Equal(t, policy.DecisionDeny, got.Decision.Type)
	require.Equal(t, "edge-deny", got.Decision.RuleID)
	require.NotEmpty(t, got.Decision.Trace)
	require.Equal(t, policy.DecisionDeny, got.Decision.Trace[0].DecisionType)
	requirePolicyDecisionAuditOrder(t, auditSender)
}

func TestPolicyEvaluateUnifiedBundleResolvesActiveDeployment(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision: pb.DecisionType_DECISION_TYPE_DENY,
		Reason:   "active bundle rule denied",
		RuleId:   "bundle-job-deny",
	})
	store := newMemoryPolicyBundleStore()
	seedActivePolicyBundle(t, store, policy.RuleTypeInput)
	s.policyBundleStore = store

	rec := postUnifiedPolicyEvaluate(t, s, unifiedBundleEvaluateBody())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := decodeUnifiedPolicyEvaluateResponse(t, rec)
	require.Equal(t, policy.DecisionSourceJob, got.Decision.Source)
	require.Equal(t, "bundle-active", got.Decision.BundleID)
	require.Equal(t, "v2", got.Decision.BundleVersion)
	require.Equal(t, "bundle-job-deny", got.Decision.RuleID)
	require.Equal(t, 1, store.getActiveCalls())
}

func TestPolicyEvaluateUnifiedRejectsTypeConfusionBeforeDispatch(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)

	rec := postUnifiedPolicyEvaluate(t, s, unifiedEdgeRuleWithJobContextBody())

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	requireJSONError(t, rec, "rule type edge requires edge_context")
	safety.mu.Lock()
	defer safety.mu.Unlock()
	require.Nil(t, safety.lastReq)
}

func TestPolicyEvaluateUnifiedRejectsUnknownRuleType(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)

	rec := postUnifiedPolicyEvaluate(t, s, unifiedUnknownRuleTypeBody())

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	requireJSONError(t, rec, "unsupported rule type unknown")
	safety.mu.Lock()
	defer safety.mu.Unlock()
	require.Nil(t, safety.lastReq)
}

func TestPolicyEvaluateUnifiedAuthRunsBeforeBundleStoreLookup(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	store := newMemoryPolicyBundleStore()
	seedActivePolicyBundle(t, store, policy.RuleTypeInput)
	s.policyBundleStore = store

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(unifiedBundleEvaluateBody()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-acme")
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Equal(t, 0, store.getActiveCalls(), "bundle store must not be consulted before auth/RBAC succeeds")
}

func TestPolicyEvaluateLegacyEndpointResponseStaysCompatible(t *testing.T) {
	s, _, safety := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, nil)
	safety.setResponse(&pb.PolicyCheckResponse{
		Decision:       pb.DecisionType_DECISION_TYPE_ALLOW,
		Reason:         "legacy-ok",
		PolicySnapshot: "snap-legacy",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(`{"tenant":"default","topic":"job.default"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	req = withAuth(req, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-user"})
	rec := httptest.NewRecorder()

	s.handlePolicyEvaluate(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	requireDeprecatedEndpointHeaders(t, rec, "/api/v1/policy/evaluate")
	var legacy map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &legacy))
	require.Contains(t, legacy, "decision")
	require.NotContains(t, legacy, "decision_source")
	require.NotContains(t, legacy, "trace")
}

func TestPolicySimulateExplainDeprecatedEndpointsKeepLegacyResponseShape(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    string
		handler func(*server, http.ResponseWriter, *http.Request)
	}{
		{name: "simulate", path: "/api/v1/policy/simulate", handler: (*server).handlePolicySimulate},
		{name: "explain", path: "/api/v1/policy/explain", handler: (*server).handlePolicyExplain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, safety := newTestGateway(t)
			s.auth = newBasicAuthForTest(t, nil)
			safety.setResponse(&pb.PolicyCheckResponse{
				Decision:       pb.DecisionType_DECISION_TYPE_ALLOW,
				Reason:         "legacy " + tc.name,
				PolicySnapshot: "snap-" + tc.name,
			})
			req := legacyPolicyCheckRequest(t, tc.path)
			rec := httptest.NewRecorder()

			tc.handler(s, rec, req)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			requireDeprecatedEndpointHeaders(t, rec, "/api/v1/policy/evaluate")
			var legacy map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &legacy))
			require.Equal(t, "DECISION_TYPE_ALLOW", legacy["decision"])
			require.NotContains(t, legacy, "source")
			require.NotContains(t, legacy, "trace")
		})
	}
}

func TestPolicyBundleLifecycleRoutesUseRealBundleStore(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = newBasicAuthForTest(t, map[string]string{
		"CORDUM_API_KEYS": `[{"key":"test-api-key","role":"admin","principal_id":"alice","tenant":"tenant-acme"}]`,
	})
	store := newMemoryPolicyBundleStore()
	require.NoError(t, store.CreateBundle(context.Background(), &policy.Bundle{
		ID:           "bundle-route",
		Name:         "Route bundle",
		ScopeBinding: policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"},
	}))
	s.policyBundleStore = store
	handler := policyRouteHandler(t, s)

	version := policyBundleLifecyclePOST(t, handler, "/api/v1/policy/bundles/bundle-route/versions", `{
		"version":"v1",
		"rule_snapshot":[`+unifiedJobRuleJSON("bundle-route-rule")+`]
	}`)
	require.Equal(t, http.StatusCreated, version.Code, version.Body.String())

	deploy := policyBundleLifecyclePOST(t, handler, "/api/v1/policy/bundles/bundle-route/deploy", `{
		"version":"v1",
		"scope":{"kind":"tenant","value":"tenant-acme"}
	}`)
	require.Equal(t, http.StatusOK, deploy.Code, deploy.Body.String())

	version2 := policyBundleLifecyclePOST(t, handler, "/api/v1/policy/bundles/bundle-route/versions", `{
		"version":"v2",
		"rule_snapshot":[`+unifiedJobRuleJSON("bundle-route-rule-v2")+`]
	}`)
	require.Equal(t, http.StatusCreated, version2.Code, version2.Body.String())

	deploy2 := policyBundleLifecyclePOST(t, handler, "/api/v1/policy/bundles/bundle-route/deploy", `{
		"version":"v2",
		"scope":{"kind":"tenant","value":"tenant-acme"}
	}`)
	require.Equal(t, http.StatusOK, deploy2.Code, deploy2.Body.String())

	rollback := policyBundleLifecyclePOST(t, handler, "/api/v1/policy/bundles/deployments/rollback", `{
		"scope":{"kind":"tenant","value":"tenant-acme"}
	}`)
	require.Equal(t, http.StatusOK, rollback.Code, rollback.Body.String())
	require.Contains(t, rollback.Body.String(), `"action":"rollback"`)
	require.Contains(t, rollback.Body.String(), `"version":"v1"`)

	history := policyBundleLifecycleGET(t, handler, "/api/v1/policy/bundles/deployments?scope_kind=tenant&scope_value=tenant-acme")
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	require.Contains(t, history.Body.String(), `"bundle_id":"bundle-route"`)
}

type unifiedPolicyEvaluateResponseJSON struct {
	Decision policy.Decision `json:"decision"`
}

func postUnifiedPolicyEvaluate(t *testing.T, s *server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/evaluate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-acme")
	req = withAuth(req, &auth.AuthContext{Tenant: "tenant-acme", Role: "admin", PrincipalID: "alice"})
	rec := httptest.NewRecorder()
	s.handlePolicyEvaluate(rec, req)
	return rec
}

func decodeUnifiedPolicyEvaluateResponse(t *testing.T, rec *httptest.ResponseRecorder) unifiedPolicyEvaluateResponseJSON {
	t.Helper()
	var got unifiedPolicyEvaluateResponseJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got), rec.Body.String())
	return got
}

func requireJSONError(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got), rec.Body.String())
	require.Equal(t, want, got["error"])
}

func requirePolicyDecisionAuditOrder(t *testing.T, sender *testAuditSender) {
	t.Helper()
	require.GreaterOrEqual(t, sender.Len(), 2, "expected legacy + unified policy decision events")
	legacy := sender.Get(sender.Len() - 2)
	unified := sender.Get(sender.Len() - 1)
	if legacy.EventType != audit.EventSafetyDecision || unified.EventType != audit.EventPolicyDecisionV2 {
		t.Fatalf("audit event order = [%q,%q], want [%q,%q]",
			legacy.EventType,
			unified.EventType,
			audit.EventSafetyDecision,
			audit.EventPolicyDecisionV2,
		)
	}
}

func requireDeprecatedEndpointHeaders(t *testing.T, rec *httptest.ResponseRecorder, successor string) {
	t.Helper()
	require.Equal(t, "true", rec.Header().Get("Deprecation"))
	require.Contains(t, rec.Header().Values("Link"), "<"+successor+">; rel=\"successor-version\"")
}

func legacyPolicyCheckRequest(t *testing.T, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"tenant":"default","topic":"job.default"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "default")
	return withAuth(req, &auth.AuthContext{Tenant: "default", Role: "admin", PrincipalID: "admin-user"})
}

func policyRouteHandler(t *testing.T, s *server) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	require.NoError(t, s.registerRoutes(mux))
	return apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(mux, s.entitlements)))
}

func policyBundleLifecyclePOST(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant-ID", "tenant-acme")
	req.Header.Set("X-API-Key", "test-api-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func policyBundleLifecycleGET(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Tenant-ID", "tenant-acme")
	req.Header.Set("X-API-Key", "test-api-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func unifiedJobEvaluateBody(ruleID string) string {
	return `{"rule":` + unifiedJobRuleJSON(ruleID) + `,"job_context":{"tenant_id":"tenant-acme","job_id":"job-123","workflow_id":"wf-1","topic":"job.acme.evaluate","principal_id":"alice","input":{"content":"contains blocked-token","content_type":"text/plain"}}}`
}

func unifiedEdgeEvaluateBody(ruleID string) string {
	return `{"rule":` + unifiedEdgeRuleJSON(ruleID) + `,"edge_context":{"tenant_id":"tenant-acme","principal_id":"alice","session_id":"sess-1","execution_id":"exec-1","agent_product":"claude-code","tool_name":"Bash","tool_input_redacted":{"command":"rm -rf build"},"labels":{"edge.fleet_id":"fleet-a"}}}`
}

func unifiedBundleEvaluateBody() string {
	return `{"bundle_id":"bundle-active","scope":{"kind":"tenant","value":"tenant-acme"},"job_context":{"tenant_id":"tenant-acme","job_id":"job-bundle","workflow_id":"wf-1","topic":"job.acme.evaluate","principal_id":"alice","input":{"content":"contains blocked-token","content_type":"text/plain"}}}`
}

func unifiedEdgeRuleWithJobContextBody() string {
	return `{"rule":` + unifiedEdgeRuleJSON("edge-confused") + `,"job_context":{"tenant_id":"tenant-acme","job_id":"job-123","topic":"job.acme.evaluate"}}`
}

func unifiedUnknownRuleTypeBody() string {
	return `{"rule":{"id":"unknown-rule","name":"unknown","type":"unknown","scope":{"kind":"tenant","value":"tenant-acme"},"status":"published","version":"v1","audit":{"created_at":"2026-05-09T00:00:00Z","created_by":"alice"},"match":{},"decide":{"decision":"deny"}},"job_context":{"tenant_id":"tenant-acme","job_id":"job-123","topic":"job.acme.evaluate"}}`
}

func unifiedJobRuleJSON(ruleID string) string {
	return `{"id":"` + ruleID + `","name":"Block input","type":"input","scope":{"kind":"tenant","value":"tenant-acme"},"status":"published","version":"v1","audit":{"created_at":"2026-05-09T00:00:00Z","created_by":"alice"},"match":{"tenants":["tenant-acme"],"topics":["job.acme.evaluate"],"keywords":["blocked-token"],"content_types":["text/plain"]},"decide":{"decision":"deny","reason":"blocked input token","severity":"high"}}`
}

func unifiedEdgeRuleJSON(ruleID string) string {
	return `{"id":"` + ruleID + `","name":"Deny destructive shell","type":"edge","scope":{"kind":"tenant","value":"tenant-acme"},"status":"published","version":"v1","audit":{"created_at":"2026-05-09T00:00:00Z","created_by":"alice"},"match":{"topics":["edge.agent_action"],"capabilities":["exec.shell"],"risk_tags":["destructive"]},"decide":{"decision":"deny","reason":"destructive shell denied"}}`
}

func seedActivePolicyBundle(t *testing.T, store *memoryPolicyBundleStore, ruleType policy.RuleType) {
	t.Helper()
	rule := policy.Rule{ID: "bundle-job-deny", Name: "Bundle job deny", Type: ruleType, Scope: policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"}, Status: policy.RuleStatusPublished, Version: "v2", Match: json.RawMessage(`{"topics":["job.acme.evaluate"],"keywords":["blocked-token"]}`), Decide: json.RawMessage(`{"decision":"deny","reason":"active bundle rule denied"}`)}
	bundle := &policy.Bundle{ID: "bundle-active", Name: "Active bundle", ScopeBinding: policy.RuleScope{Kind: policy.RuleScopeTenant, Value: "tenant-acme"}}
	require.NoError(t, store.CreateBundle(context.Background(), bundle))
	require.NoError(t, store.CreateBundleVersion(context.Background(), bundle.ID, &policy.BundleVersion{Version: "v2", RuleSnapshot: []policy.Rule{rule}, DeployedAt: time.Now().UTC()}))
	_, err := store.DeployVersionToScope(context.Background(), bundle.ID, "v2", bundle.ScopeBinding)
	require.NoError(t, err)
}

type memoryPolicyBundleStore struct {
	mu             sync.Mutex
	bundles        map[string]*policy.Bundle
	versions       map[string]map[string]*policy.BundleVersion
	deployments    map[policy.RuleScope]*policy.Deployment
	history        map[policy.RuleScope][]*policy.Deployment
	getActiveCount int
}

func newMemoryPolicyBundleStore() *memoryPolicyBundleStore {
	return &memoryPolicyBundleStore{bundles: map[string]*policy.Bundle{}, versions: map[string]map[string]*policy.BundleVersion{}, deployments: map[policy.RuleScope]*policy.Deployment{}, history: map[policy.RuleScope][]*policy.Deployment{}}
}

func (s *memoryPolicyBundleStore) CreateBundle(ctx context.Context, b *policy.Bundle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.bundles[b.ID]; exists {
		return policy.ErrBundleExists
	}
	cp := *b
	s.bundles[b.ID] = &cp
	return nil
}

func (s *memoryPolicyBundleStore) GetBundle(ctx context.Context, id string) (*policy.Bundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bundles[id]
	if !ok {
		return nil, policy.ErrBundleNotFound
	}
	cp := *b
	return &cp, nil
}

func (s *memoryPolicyBundleStore) ListBundlesByScope(ctx context.Context, scope policy.RuleScope) ([]*policy.Bundle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*policy.Bundle
	for _, b := range s.bundles {
		if b.ScopeBinding == scope {
			cp := *b
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (s *memoryPolicyBundleStore) CreateBundleVersion(ctx context.Context, bundleID string, v *policy.BundleVersion) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.bundles[bundleID]; !ok {
		return policy.ErrBundleNotFound
	}
	if s.versions[bundleID] == nil {
		s.versions[bundleID] = map[string]*policy.BundleVersion{}
	}
	cp := *v
	s.versions[bundleID][v.Version] = &cp
	return nil
}

func (s *memoryPolicyBundleStore) ListBundleVersions(ctx context.Context, bundleID string) ([]*policy.BundleVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*policy.BundleVersion
	for _, v := range s.versions[bundleID] {
		cp := *v
		out = append(out, &cp)
	}
	return out, nil
}

func (s *memoryPolicyBundleStore) GetBundleVersion(ctx context.Context, bundleID, version string) (*policy.BundleVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[bundleID][version]
	if !ok {
		return nil, policy.ErrBundleVersionNotFound
	}
	cp := *v
	return &cp, nil
}

func (s *memoryPolicyBundleStore) DeployVersionToScope(ctx context.Context, bundleID, version string, scope policy.RuleScope) (*policy.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.versions[bundleID][version]; !ok {
		return nil, policy.ErrBundleVersionNotFound
	}
	dep := &policy.Deployment{BundleID: bundleID, Version: version, Scope: scope, DeployedAt: time.Now().UTC(), Action: policy.DeploymentActionDeploy}
	s.deployments[scope] = dep
	s.history[scope] = append([]*policy.Deployment{dep}, s.history[scope]...)
	return dep, nil
}

func (s *memoryPolicyBundleStore) RollbackDeployment(ctx context.Context, scope policy.RuleScope) (*policy.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.history[scope]
	if len(history) < 2 {
		return nil, policy.ErrNoRollbackTarget
	}
	prev := history[1]
	dep := &policy.Deployment{BundleID: prev.BundleID, Version: prev.Version, Scope: scope, DeployedAt: time.Now().UTC(), Action: policy.DeploymentActionRollback}
	s.deployments[scope] = dep
	s.history[scope] = append([]*policy.Deployment{dep}, history...)
	return dep, nil
}

func (s *memoryPolicyBundleStore) GetActiveDeployment(ctx context.Context, scope policy.RuleScope) (*policy.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getActiveCount++
	dep, ok := s.deployments[scope]
	if !ok {
		return nil, policy.ErrNoDeploymentForScope
	}
	cp := *dep
	return &cp, nil
}

func (s *memoryPolicyBundleStore) ListDeploymentHistory(ctx context.Context, scope policy.RuleScope, limit int) ([]*policy.Deployment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.history[scope]
	if limit > 0 && len(history) > limit {
		history = history[:limit]
	}
	out := make([]*policy.Deployment, 0, len(history))
	for _, dep := range history {
		cp := *dep
		out = append(out, &cp)
	}
	return out, nil
}

func (s *memoryPolicyBundleStore) getActiveCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getActiveCount
}

var _ policy.BundleStore = (*memoryPolicyBundleStore)(nil)
