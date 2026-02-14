package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/model"
	"github.com/cordum/cordum/core/infra/config"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/cordum/cordum/core/protocol/pb/v1"
)

const policyContent = `rules:
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`

const outputPolicyContent = `output_rules:
  - id: out-secret
    enabled: false
    description: "Detect secret leaks"
    severity: high
    decision: quarantine
    reason: "secret matched in output"
    match:
      topics:
        - job.*
      scanners:
        - secret
      content_patterns:
        - AKIA[0-9A-Z]{16}
  - id: out-pii
    enabled: true
    description: "Redact PII"
    severity: medium
    decision: redact
    reason: "pii found"
    match:
      detectors:
        - pii
`

const legacyOutputPolicyContentWithNulls = `output_policy:
  enabled: true
  fail_mode: ""
default_decision: ""
output_rules:
  - id: out-secret
    enabled: false
    description: "Detect secret leaks"
    severity: high
    decision: quarantine
    reason: "secret matched in output"
    match:
      topics:
        - job.*
      scanners:
        - secret
      has_error: null
`

type policySimAuth struct{}

func (a *policySimAuth) AuthenticateHTTP(r *http.Request) (*AuthContext, error) {
	return authFromRequest(r), nil
}

func (a *policySimAuth) AuthenticateGRPC(ctx context.Context) (*AuthContext, error) {
	return authFromContext(ctx), nil
}

func (a *policySimAuth) RequireRole(r *http.Request, roles ...string) error {
	auth := authFromRequest(r)
	if auth == nil {
		return errors.New("unauthorized")
	}
	role := normalizeRole(auth.Role)
	if role == "" {
		return errors.New("role required")
	}
	for _, candidate := range roles {
		if normalizeRole(candidate) == role {
			return nil
		}
	}
	return errors.New("forbidden")
}

func (a *policySimAuth) ResolveTenant(r *http.Request, requested, _ string) (string, error) {
	auth := authFromRequest(r)
	if auth == nil {
		return "", errors.New("unauthorized")
	}
	requested = strings.TrimSpace(requested)
	authTenant := strings.TrimSpace(auth.Tenant)
	if requested != "" && !auth.AllowCrossTenant && authTenant != "" && requested != authTenant {
		return "", errors.New("tenant access denied")
	}
	if requested == "" {
		if authTenant == "" {
			return "", errors.New("tenant required")
		}
		return authTenant, nil
	}
	return requested, nil
}

func (a *policySimAuth) RequireTenantAccess(r *http.Request, tenant string) error {
	auth := authFromRequest(r)
	if auth == nil {
		return errors.New("unauthorized")
	}
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return errors.New("tenant required")
	}
	if auth.AllowCrossTenant {
		return nil
	}
	if strings.TrimSpace(auth.Tenant) != tenant {
		return errors.New("tenant access denied")
	}
	return nil
}

func (a *policySimAuth) ResolvePrincipal(r *http.Request, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, nil
	}
	auth := authFromRequest(r)
	if auth == nil || strings.TrimSpace(auth.PrincipalID) == "" {
		return "", errors.New("principal required")
	}
	return strings.TrimSpace(auth.PrincipalID), nil
}

func TestPolicyBundleHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	body, _ := json.Marshal(map[string]any{
		"content": policyContent,
		"enabled": true,
		"author":  "tester",
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", bytes.NewReader(body))
	putReq.Header.Set("X-Tenant-ID", "default")
	putReq.SetPathValue("id", "secops/test")
	putReq.Header.Set("X-Principal-Role", "admin")
	putRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put policy bundle: %d %s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/secops/test", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.SetPathValue("id", "secops/test")
	getReq.Header.Set("X-Principal-Role", "admin")
	getRec := httptest.NewRecorder()
	s.handleGetPolicyBundle(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get policy bundle: %d %s", getRec.Code, getRec.Body.String())
	}
	var detail map[string]any
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode bundle detail: %v", err)
	}
	if detail["id"] != "secops/test" {
		t.Fatalf("unexpected bundle id")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles", nil)
	listReq.Header.Set("X-Tenant-ID", "default")
	listReq.Header.Set("X-Principal-Role", "admin")
	listRec := httptest.NewRecorder()
	s.handlePolicyBundles(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list policy bundles: %d %s", listRec.Code, listRec.Body.String())
	}

	rulesReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/rules", nil)
	rulesReq.Header.Set("X-Tenant-ID", "default")
	rulesReq.Header.Set("X-Principal-Role", "admin")
	rulesRec := httptest.NewRecorder()
	s.handlePolicyRules(rulesRec, rulesReq)
	if rulesRec.Code != http.StatusOK {
		t.Fatalf("policy rules: %d %s", rulesRec.Code, rulesRec.Body.String())
	}

	simBody, _ := json.Marshal(map[string]any{
		"request": map[string]any{
			"topic":  "job.test",
			"tenant": "default",
		},
	})
	simReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/secops/test/simulate", bytes.NewReader(simBody))
	simReq.Header.Set("X-Tenant-ID", "default")
	simReq.SetPathValue("id", "secops/test")
	simReq.Header.Set("X-Principal-Role", "admin")
	simRec := httptest.NewRecorder()
	s.handleSimulatePolicyBundle(simRec, simReq)
	if simRec.Code != http.StatusOK {
		t.Fatalf("simulate bundle: %d %s", simRec.Code, simRec.Body.String())
	}
	var resp pb.PolicyCheckResponse
	if err := protojson.Unmarshal(simRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode simulate response: %v", err)
	}
	if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
		t.Fatalf("unexpected decision: %v", resp.GetDecision())
	}
}

func TestPolicyBundleSimulateAuthAndTenant(t *testing.T) {
	s, _, _ := newTestGateway(t)
	s.auth = &policySimAuth{}

	tenantPolicy := `rules:
  - id: deny-tenant-b
    match:
      tenants:
        - tenant-b
      topics:
        - job.*
    decision: deny
  - id: allow-all
    match:
      topics:
        - job.*
    decision: allow
`
	seed, _ := json.Marshal(map[string]any{
		"content": tenantPolicy,
		"enabled": true,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", bytes.NewReader(seed))
	putReq.Header.Set("X-Tenant-ID", "tenant-a")
	putReq.SetPathValue("id", "secops/test")
	putReq = withAuth(putReq, &AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-1"})
	putRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("seed bundle: %d %s", putRec.Code, putRec.Body.String())
	}

	simulate := func(auth *AuthContext, requestedTenant string) (*httptest.ResponseRecorder, *pb.PolicyCheckResponse) {
		simBody, _ := json.Marshal(map[string]any{
			"request": map[string]any{
				"topic":  "job.test",
				"tenant": requestedTenant,
			},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/secops/test/simulate", bytes.NewReader(simBody))
		headerTenant := requestedTenant
		if headerTenant == "" {
			headerTenant = auth.Tenant
		}
		req.Header.Set("X-Tenant-ID", headerTenant)
		req.SetPathValue("id", "secops/test")
		req = withAuth(req, auth)
		rec := httptest.NewRecorder()
		s.handleSimulatePolicyBundle(rec, req)
		if rec.Code != http.StatusOK {
			return rec, nil
		}
		var resp pb.PolicyCheckResponse
		if err := protojson.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode simulate response: %v", err)
		}
		return rec, &resp
	}

	t.Run("non-admin forbidden", func(t *testing.T) {
		rec, _ := simulate(&AuthContext{Tenant: "tenant-a", Role: "viewer", PrincipalID: "user-1"}, "tenant-a")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("cross-tenant denied", func(t *testing.T) {
		rec, _ := simulate(&AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-1"}, "tenant-b")
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("cross-tenant allowed uses requested tenant", func(t *testing.T) {
		rec, resp := simulate(&AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-1", AllowCrossTenant: true}, "tenant-b")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_DENY {
			t.Fatalf("expected deny for tenant-b, got %v", resp.GetDecision())
		}
	})

	t.Run("admin success uses resolved tenant", func(t *testing.T) {
		rec, resp := simulate(&AuthContext{Tenant: "tenant-a", Role: "admin", PrincipalID: "admin-1"}, "tenant-a")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if resp.GetDecision() != pb.DecisionType_DECISION_TYPE_ALLOW {
			t.Fatalf("expected allow for tenant-a, got %v", resp.GetDecision())
		}
	})
}

func TestPolicyBundlePublishRollbackAndAudit(t *testing.T) {
	s, _, _ := newTestGateway(t)

	seed, _ := json.Marshal(map[string]any{
		"content": policyContent,
		"enabled": false,
	})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/test", bytes.NewReader(seed))
	putReq.Header.Set("X-Tenant-ID", "default")
	putReq.SetPathValue("id", "secops/test")
	putReq.Header.Set("X-Principal-Role", "admin")
	putReq.Header.Set("X-Principal-Id", "user1")
	putRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("seed bundle: %d %s", putRec.Code, putRec.Body.String())
	}

	pubReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/publish", bytes.NewReader([]byte(`{}`)))
	pubReq.Header.Set("X-Tenant-ID", "default")
	pubReq.Header.Set("X-Principal-Role", "admin")
	pubReq.Header.Set("X-Principal-Id", "user1")
	pubRec := httptest.NewRecorder()
	s.handlePublishPolicyBundles(pubRec, pubReq)
	if pubRec.Code != http.StatusOK {
		t.Fatalf("publish bundles: %d %s", pubRec.Code, pubRec.Body.String())
	}
	var publishResp map[string]any
	if err := json.NewDecoder(pubRec.Body).Decode(&publishResp); err != nil {
		t.Fatalf("decode publish: %v", err)
	}
	rollbackID, _ := publishResp["snapshot_after"].(string)
	if rollbackID == "" {
		t.Fatalf("expected snapshot_after id")
	}

	snapReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/snapshots", nil)
	snapReq.Header.Set("X-Tenant-ID", "default")
	snapReq.Header.Set("X-Principal-Role", "admin")
	snapRec := httptest.NewRecorder()
	s.handleListPolicyBundleSnapshots(snapRec, snapReq)
	if snapRec.Code != http.StatusOK {
		t.Fatalf("list snapshots: %d %s", snapRec.Code, snapRec.Body.String())
	}

	rbBody, _ := json.Marshal(map[string]any{"snapshot_id": rollbackID})
	rbReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/rollback", bytes.NewReader(rbBody))
	rbReq.Header.Set("X-Tenant-ID", "default")
	rbReq.Header.Set("X-Principal-Role", "admin")
	rbReq.Header.Set("X-Principal-Id", "user1")
	rbRec := httptest.NewRecorder()
	s.handleRollbackPolicyBundles(rbRec, rbReq)
	if rbRec.Code != http.StatusOK {
		t.Fatalf("rollback bundles: %d %s", rbRec.Code, rbRec.Body.String())
	}

	auditReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit", nil)
	auditReq.Header.Set("X-Tenant-ID", "default")
	auditReq.Header.Set("X-Principal-Role", "admin")
	auditRec := httptest.NewRecorder()
	s.handleListPolicyAudit(auditRec, auditReq)
	if auditRec.Code != http.StatusOK {
		t.Fatalf("audit list: %d %s", auditRec.Code, auditRec.Body.String())
	}
	var auditResp map[string]any
	if err := json.NewDecoder(auditRec.Body).Decode(&auditResp); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	items, _ := auditResp["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected audit entries")
	}
}

func TestPolicyBundleSnapshotHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	captureReq := httptest.NewRequest(http.MethodPost, "/api/v1/policy/bundles/snapshots", bytes.NewReader([]byte(`{"note":"snapshot-test"}`)))
	captureReq.Header.Set("X-Tenant-ID", "default")
	captureReq.Header.Set("X-Principal-Role", "admin")
	captureRec := httptest.NewRecorder()
	s.handleCapturePolicyBundleSnapshot(captureRec, captureReq)
	if captureRec.Code != http.StatusOK {
		t.Fatalf("capture snapshot: %d %s", captureRec.Code, captureRec.Body.String())
	}
	var snap policyBundleSnapshot
	if err := json.NewDecoder(captureRec.Body).Decode(&snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snap.ID == "" {
		t.Fatalf("expected snapshot id")
	}
	if snap.Note != "snapshot-test" {
		t.Fatalf("expected snapshot note")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/snapshots/"+snap.ID, nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.Header.Set("X-Principal-Role", "admin")
	getReq.SetPathValue("id", snap.ID)
	getRec := httptest.NewRecorder()
	s.handleGetPolicyBundleSnapshot(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get snapshot: %d %s", getRec.Code, getRec.Body.String())
	}

	missingReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/snapshots/missing", nil)
	missingReq.Header.Set("X-Tenant-ID", "default")
	missingReq.Header.Set("X-Principal-Role", "admin")
	missingReq.SetPathValue("id", "missing")
	missingRec := httptest.NewRecorder()
	s.handleGetPolicyBundleSnapshot(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("expected not found for missing snapshot")
	}
}

func TestPolicyOutputRulesHandlers(t *testing.T) {
	s, _, _ := newTestGateway(t)

	seedBody, _ := json.Marshal(map[string]any{
		"content": outputPolicyContent,
		"enabled": true,
		"author":  "tester",
	})
	putBundleReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/output", bytes.NewReader(seedBody))
	putBundleReq.Header.Set("X-Tenant-ID", "default")
	putBundleReq.Header.Set("X-Principal-Role", "admin")
	putBundleReq.SetPathValue("id", "secops/output")
	putBundleRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putBundleRec, putBundleReq)
	if putBundleRec.Code != http.StatusOK {
		t.Fatalf("seed output policy bundle: %d %s", putBundleRec.Code, putBundleRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/output/rules", nil)
	listReq.Header.Set("X-Tenant-ID", "default")
	listReq.Header.Set("X-Principal-Role", "admin")
	listRec := httptest.NewRecorder()
	s.handlePolicyOutputRules(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list output policy rules: %d %s", listRec.Code, listRec.Body.String())
	}

	var listResp map[string]any
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode output rules: %v", err)
	}
	items, _ := listResp["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 output rules, got %d", len(items))
	}

	findRule := func(ruleID string) map[string]any {
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			if strings.TrimSpace(anyString(item["id"])) == ruleID {
				return item
			}
		}
		return nil
	}
	secretRule := findRule("out-secret")
	if secretRule == nil {
		t.Fatalf("expected out-secret rule in response")
	}
	if enabled, ok := secretRule["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected out-secret enabled=false, got %v", secretRule["enabled"])
	}

	toggleBody, _ := json.Marshal(map[string]any{"enabled": true})
	toggleReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/output/rules/out-secret", bytes.NewReader(toggleBody))
	toggleReq.Header.Set("X-Tenant-ID", "default")
	toggleReq.Header.Set("X-Principal-Role", "admin")
	toggleReq.SetPathValue("id", "out-secret")
	toggleRec := httptest.NewRecorder()
	s.handlePutPolicyOutputRule(toggleRec, toggleReq)
	if toggleRec.Code != http.StatusOK {
		t.Fatalf("toggle output rule: %d %s", toggleRec.Code, toggleRec.Body.String())
	}
	var toggleResp map[string]any
	if err := json.NewDecoder(toggleRec.Body).Decode(&toggleResp); err != nil {
		t.Fatalf("decode toggle response: %v", err)
	}
	if enabled, _ := toggleResp["enabled"].(bool); !enabled {
		t.Fatalf("expected enabled=true in toggle response")
	}
	if strings.TrimSpace(anyString(toggleResp["bundle_id"])) != "secops/output" {
		t.Fatalf("expected bundle_id secops/output, got %v", toggleResp["bundle_id"])
	}

	verifyReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/output/rules", nil)
	verifyReq.Header.Set("X-Tenant-ID", "default")
	verifyReq.Header.Set("X-Principal-Role", "admin")
	verifyRec := httptest.NewRecorder()
	s.handlePolicyOutputRules(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify output rules: %d %s", verifyRec.Code, verifyRec.Body.String())
	}
	var verifyResp map[string]any
	if err := json.NewDecoder(verifyRec.Body).Decode(&verifyResp); err != nil {
		t.Fatalf("decode verify response: %v", err)
	}
	verifyItems, _ := verifyResp["items"].([]any)
	verified := false
	for _, raw := range verifyItems {
		item, _ := raw.(map[string]any)
		if strings.TrimSpace(anyString(item["id"])) == "out-secret" {
			enabled, _ := item["enabled"].(bool)
			if !enabled {
				t.Fatalf("expected out-secret enabled after toggle")
			}
			verified = true
		}
	}
	if !verified {
		t.Fatalf("expected out-secret in verify response")
	}
}

func TestPolicyOutputRuleToggleErrors(t *testing.T) {
	s, _, _ := newTestGateway(t)

	seedBody, _ := json.Marshal(map[string]any{
		"content": outputPolicyContent,
		"enabled": true,
	})
	putBundleReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/output", bytes.NewReader(seedBody))
	putBundleReq.Header.Set("X-Tenant-ID", "default")
	putBundleReq.Header.Set("X-Principal-Role", "admin")
	putBundleReq.SetPathValue("id", "secops/output")
	putBundleRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putBundleRec, putBundleReq)
	if putBundleRec.Code != http.StatusOK {
		t.Fatalf("seed output policy bundle: %d %s", putBundleRec.Code, putBundleRec.Body.String())
	}

	noEnabledReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/output/rules/out-secret", bytes.NewReader([]byte(`{}`)))
	noEnabledReq.Header.Set("X-Tenant-ID", "default")
	noEnabledReq.Header.Set("X-Principal-Role", "admin")
	noEnabledReq.SetPathValue("id", "out-secret")
	noEnabledRec := httptest.NewRecorder()
	s.handlePutPolicyOutputRule(noEnabledRec, noEnabledReq)
	if noEnabledRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when enabled missing, got %d", noEnabledRec.Code)
	}

	notFoundReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/output/rules/does-not-exist", bytes.NewReader([]byte(`{"enabled":true}`)))
	notFoundReq.Header.Set("X-Tenant-ID", "default")
	notFoundReq.Header.Set("X-Principal-Role", "admin")
	notFoundReq.SetPathValue("id", "does-not-exist")
	notFoundRec := httptest.NewRecorder()
	s.handlePutPolicyOutputRule(notFoundRec, notFoundReq)
	if notFoundRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing rule, got %d", notFoundRec.Code)
	}
}

func TestPolicyOutputRuleToggleSanitizesLegacyNulls(t *testing.T) {
	s, _, _ := newTestGateway(t)

	seedBody, _ := json.Marshal(map[string]any{
		"content": legacyOutputPolicyContentWithNulls,
		"enabled": true,
	})
	putBundleReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/bundles/secops/output", bytes.NewReader(seedBody))
	putBundleReq.Header.Set("X-Tenant-ID", "default")
	putBundleReq.Header.Set("X-Principal-Role", "admin")
	putBundleReq.SetPathValue("id", "secops/output")
	putBundleRec := httptest.NewRecorder()
	s.handlePutPolicyBundle(putBundleRec, putBundleReq)
	if putBundleRec.Code != http.StatusOK {
		t.Fatalf("seed output policy bundle: %d %s", putBundleRec.Code, putBundleRec.Body.String())
	}

	toggleReq := httptest.NewRequest(http.MethodPut, "/api/v1/policy/output/rules/out-secret", bytes.NewReader([]byte(`{"enabled":true}`)))
	toggleReq.Header.Set("X-Tenant-ID", "default")
	toggleReq.Header.Set("X-Principal-Role", "admin")
	toggleReq.SetPathValue("id", "out-secret")
	toggleRec := httptest.NewRecorder()
	s.handlePutPolicyOutputRule(toggleRec, toggleReq)
	if toggleRec.Code != http.StatusOK {
		t.Fatalf("toggle output rule should succeed for sanitized legacy bundle: %d %s", toggleRec.Code, toggleRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/policy/bundles/secops/output", nil)
	getReq.Header.Set("X-Tenant-ID", "default")
	getReq.Header.Set("X-Principal-Role", "admin")
	getReq.SetPathValue("id", "secops/output")
	getRec := httptest.NewRecorder()
	s.handleGetPolicyBundle(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get policy bundle: %d %s", getRec.Code, getRec.Body.String())
	}

	var detail map[string]any
	if err := json.NewDecoder(getRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode bundle detail: %v", err)
	}
	content := strings.TrimSpace(anyString(detail["content"]))
	if strings.Contains(content, "has_error: null") {
		t.Fatalf("expected sanitized bundle to omit has_error: null, got content:\n%s", content)
	}
	if strings.Contains(content, `fail_mode: ""`) {
		t.Fatalf("expected sanitized bundle to omit empty fail_mode, got content:\n%s", content)
	}
	if _, err := config.ParseSafetyPolicy([]byte(content)); err != nil {
		t.Fatalf("sanitized bundle must stay schema-valid: %v", err)
	}
}

func TestMergeSafetyPoliciesPreservesDefaultsAndOutputRules(t *testing.T) {
	enabled := true
	hasError := false
	base := &config.SafetyPolicy{
		DefaultDecision: "allow",
		OutputPolicy: config.OutputPolicyConfig{
			Enabled:  true,
			FailMode: "closed",
		},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "base-output",
				Enabled:  &enabled,
				Severity: "high",
				Decision: "quarantine",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.base"},
					Scanners: []string{"secret"},
					HasError: &hasError,
				},
			},
		},
	}
	extra := &config.SafetyPolicy{
		Rules: []config.PolicyRule{
			{
				ID:       "allow-extra",
				Decision: "allow",
			},
		},
		OutputRules: []config.OutputPolicyRule{
			{
				ID:       "extra-output",
				Severity: "medium",
				Decision: "redact",
				Match: config.OutputPolicyMatch{
					Topics:   []string{"job.extra"},
					Scanners: []string{"pii"},
				},
			},
		},
	}

	merged := mergeSafetyPolicies(base, extra)
	if merged == nil {
		t.Fatal("expected merged policy")
	}
	if merged.DefaultDecision != "allow" {
		t.Fatalf("expected default_decision=allow, got %q", merged.DefaultDecision)
	}
	if !merged.OutputPolicy.Enabled || merged.OutputPolicy.FailMode != "closed" {
		t.Fatalf("expected output policy config preserved, got %#v", merged.OutputPolicy)
	}
	if len(merged.OutputRules) != 2 {
		t.Fatalf("expected 2 output rules, got %d", len(merged.OutputRules))
	}
	if merged.OutputRules[0].ID != "base-output" || merged.OutputRules[1].ID != "extra-output" {
		t.Fatalf("unexpected output rule order: %#v", []string{merged.OutputRules[0].ID, merged.OutputRules[1].ID})
	}
}

func TestPolicyOutputStatsHandler(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()
	now := time.Now().UTC()

	seed := func(jobID string, state model.JobState, decision model.OutputDecision, checkedAt int64, latencyMs int64) {
		req := &pb.JobRequest{
			JobId:    jobID,
			Topic:    "job.test",
			TenantId: "default",
		}
		if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
			t.Fatalf("set job meta %s: %v", jobID, err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
			t.Fatalf("set pending %s: %v", jobID, err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
			t.Fatalf("set scheduled %s: %v", jobID, err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
			t.Fatalf("set running %s: %v", jobID, err)
		}
		if err := s.jobStore.SetState(ctx, jobID, state); err != nil {
			t.Fatalf("set state %s: %v", jobID, err)
		}
		record := model.OutputSafetyRecord{
			Decision:        decision,
			RuleID:          "out-secret",
			Reason:          "matched output policy rule",
			CheckedAt:       checkedAt,
			CheckDurationMs: latencyMs,
			Phase:           "sync",
		}
		if err := s.jobStore.SetOutputDecision(ctx, jobID, record); err != nil {
			t.Fatalf("set output decision %s: %v", jobID, err)
		}
	}

	seed("job-stats-recent-quarantine", model.JobStateQuarantined, model.OutputQuarantine, now.Add(-10*time.Minute).UnixMicro(), 15)
	seed("job-stats-recent-allow", model.JobStateSucceeded, model.OutputAllow, now.Add(-5*time.Minute).UnixMicro(), 5)
	seed("job-stats-old", model.JobStateQuarantined, model.OutputQuarantine, now.Add(-26*time.Hour).UnixMicro(), 99)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/output/stats?limit=100", nil)
	req.Header.Set("X-Tenant-ID", "default")
	rec := httptest.NewRecorder()
	s.handlePolicyOutputStats(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("output stats: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		TotalChecks24h int64  `json:"total_checks_24h"`
		Quarantined24h int64  `json:"quarantined_24h"`
		AvgLatencyMs   int64  `json:"avg_latency_ms"`
		LastCheckAt    string `json:"last_check_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode output stats: %v", err)
	}
	if payload.TotalChecks24h != 2 {
		t.Fatalf("expected total_checks_24h=2, got %d", payload.TotalChecks24h)
	}
	if payload.Quarantined24h != 1 {
		t.Fatalf("expected quarantined_24h=1, got %d", payload.Quarantined24h)
	}
	if payload.AvgLatencyMs != 10 {
		t.Fatalf("expected avg_latency_ms=10, got %d", payload.AvgLatencyMs)
	}
	if strings.TrimSpace(payload.LastCheckAt) == "" {
		t.Fatalf("expected non-empty last_check_at")
	}
}

func TestPolicyAuditOutputFilterByRuleID(t *testing.T) {
	s, _, _ := newTestGateway(t)
	ctx := context.Background()

	seedOutputJob := func(jobID, ruleID string) {
		req := &pb.JobRequest{
			JobId:    jobID,
			Topic:    "job.test",
			TenantId: "default",
		}
		if err := s.jobStore.SetJobMeta(ctx, req); err != nil {
			t.Fatalf("set job meta: %v", err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStatePending); err != nil {
			t.Fatalf("set pending: %v", err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStateScheduled); err != nil {
			t.Fatalf("set scheduled: %v", err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStateRunning); err != nil {
			t.Fatalf("set running: %v", err)
		}
		if err := s.jobStore.SetState(ctx, jobID, model.JobStateQuarantined); err != nil {
			t.Fatalf("set quarantined: %v", err)
		}
		record := model.OutputSafetyRecord{
			Decision:        model.OutputQuarantine,
			RuleID:          ruleID,
			Reason:          "matched output policy rule",
			CheckedAt:       time.Now().UTC().UnixMicro(),
			CheckDurationMs: 7,
			Phase:           "sync",
			OriginalPtr:     "redis://res:" + jobID,
			Findings: []model.OutputFinding{{
				Type:           "secret_leak",
				Severity:       "critical",
				Detail:         "aws_access_key_id",
				Scanner:        "regex",
				Confidence:     0.99,
				MatchedPattern: "AKIA[0-9A-Z]{16}",
			}},
		}
		if err := s.jobStore.SetOutputDecision(ctx, jobID, record); err != nil {
			t.Fatalf("set output decision: %v", err)
		}
	}

	seedOutputJob("job-output-1", "out-secret")
	seedOutputJob("job-output-2", "out-other")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/policy/audit?type=output&rule_id=out-secret&limit=10", nil)
	req.Header.Set("X-Tenant-ID", "default")
	req.Header.Set("X-Principal-Role", "admin")
	rec := httptest.NewRecorder()
	s.handleListPolicyAudit(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("policy audit output filter: %d %s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode audit output filter response: %v", err)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 output audit item for out-secret, got %d", len(items))
	}
	item, _ := items[0].(map[string]any)
	if strings.TrimSpace(anyString(item["rule_id"])) != "out-secret" {
		t.Fatalf("expected rule_id out-secret, got %v", item["rule_id"])
	}
	if strings.TrimSpace(anyString(item["job_id"])) != "job-output-1" {
		t.Fatalf("expected job_id job-output-1, got %v", item["job_id"])
	}
	if strings.TrimSpace(anyString(item["decision"])) != "quarantine" {
		t.Fatalf("expected decision quarantine, got %v", item["decision"])
	}
}

func anyString(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	default:
		return ""
	}
}
