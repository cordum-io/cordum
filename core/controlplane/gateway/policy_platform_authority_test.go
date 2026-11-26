package gateway

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/controlplane/gateway/packs"
	"github.com/cordum/cordum/core/controlplane/gateway/policybundles"
	"github.com/cordum/cordum/core/licensing"
	"github.com/redis/go-redis/v9"
)

const (
	systemPolicyBundleID    = "secops/platform-authority"
	systemPolicyVelocityID  = "platform-authority"
	systemPolicyPackID      = "platform-authority"
	systemPolicyInstallPath = "/api/v1/packs/install"
	systemPolicyVelocity    = "/api/v1/policy/velocity-rules"
	systemPolicyConfig      = `{"scope":"system","scope_id":"policy","data":{"platform_authority":"set"}}`
)

var (
	systemPolicyPlatform = crossTenantSweepTenant{"tenant-platform", "system-policy-platform-key", "principal-system-policy-platform"}
	systemPolicySpanning = crossTenantSweepTenant{edgeRouteTenant, "system-policy-spanning-key", "principal-system-policy-spanning"}
	systemPolicyBundle   = "/api/v1/policy/bundles/" + url.PathEscape(systemPolicyBundleID)
	systemPolicyEdit     = fmt.Sprintf(`{"content":%q,"enabled":false}`, shadowTestReplacementYAML)
	systemPolicyGlobal   = fmt.Sprintf(`{"sections":{"input_rules":{"content":%q}}}`, globalInputContent)
)

// systemPolicyWrite is one route that writes the system policy document. body
// prepares the fixture with the platform key and returns the request body. part
// is the systemPolicyState entry the platform key's write changes ("" when the
// fixture has no catalog to install from).
type systemPolicyWrite struct {
	name, method, path, part string
	body                     func(t *testing.T, sw *crossTenantSweep) string
}

func fixedBody(body string) func(*testing.T, *crossTenantSweep) string {
	return func(*testing.T, *crossTenantSweep) string { return body }
}

var systemPolicyWrites = []systemPolicyWrite{
	{"put-bundle", http.MethodPut, systemPolicyBundle, "policy", fixedBody(systemPolicyEdit)},
	{"delete-bundle", http.MethodDelete, systemPolicyBundle, "policy", fixedBody("")},
	{"publish", http.MethodPost, "/api/v1/policy/publish", "policy", func(t *testing.T, sw *crossTenantSweep) string {
		systemPolicyPrepare(t, sw, http.MethodPut, systemPolicyBundle, systemPolicyEdit)
		return fmt.Sprintf(`{"bundle_ids":[%q]}`, systemPolicyBundleID)
	}},
	{"rollback", http.MethodPost, "/api/v1/policy/rollback", "policy", func(t *testing.T, sw *crossTenantSweep) string {
		snapshots, _, err := sw.server.loadPolicySnapshots(context.Background())
		if err != nil || len(snapshots) == 0 {
			t.Fatalf("read snapshots: %d, %v", len(snapshots), err)
		}
		systemPolicyPrepare(t, sw, http.MethodPut, systemPolicyBundle, systemPolicyEdit)
		return fmt.Sprintf(`{"snapshot_id":%q}`, snapshots[0].ID)
	}},
	{"capture-snapshot", http.MethodPost, "/api/v1/policy/bundles/snapshots", "policy", fixedBody(`{"note":"platform authority"}`)},
	{"put-global", http.MethodPut, "/api/v1/policy/global", "policy", fixedBody(systemPolicyGlobal)},
	{"put-output-rule", http.MethodPut, "/api/v1/policy/output/rules/out-secret", "policy", func(t *testing.T, sw *crossTenantSweep) string {
		systemPolicyPrepare(t, sw, http.MethodPut, "/api/v1/policy/bundles/secops%2Foutput", fmt.Sprintf(`{"content":%q,"enabled":true}`, outputPolicyContent))
		return `{"enabled":true}`
	}},
	{"create-velocity", http.MethodPost, systemPolicyVelocity, "policy", func(t *testing.T, sw *crossTenantSweep) string {
		return systemPolicyVelocityRule(t, sw, false, 3)
	}},
	{"put-velocity", http.MethodPut, systemPolicyVelocity + "/" + systemPolicyVelocityID, "policy", func(t *testing.T, sw *crossTenantSweep) string {
		return systemPolicyVelocityRule(t, sw, true, 7)
	}},
	{"delete-velocity", http.MethodDelete, systemPolicyVelocity + "/" + systemPolicyVelocityID, "policy", func(t *testing.T, sw *crossTenantSweep) string {
		systemPolicyVelocityRule(t, sw, true, 3)
		return ""
	}},
	{"config-put", http.MethodPut, "/api/v1/config", "policy", fixedBody(systemPolicyConfig)},
	{"config-post", http.MethodPost, "/api/v1/config", "policy", fixedBody(systemPolicyConfig)},
	{"pack-install", http.MethodPost, systemPolicyInstallPath, "packs", systemPolicyPack},
	{"pack-uninstall", http.MethodPost, "/api/v1/packs/" + systemPolicyPackID + "/uninstall", "packs", func(t *testing.T, sw *crossTenantSweep) string {
		systemPolicyPrepare(t, sw, http.MethodPost, systemPolicyInstallPath, systemPolicyPack(t, sw))
		return `{}`
	}},
	{"marketplace-install", http.MethodPost, "/api/v1/marketplace/install", "", fixedBody(`{"catalog_id":"platform-authority","pack_id":"` + systemPolicyPackID + `"}`)},
}

func TestSystemPolicyPlatformAuthority(t *testing.T) {
	for _, tc := range systemPolicyWrites {
		t.Run(tc.name, func(t *testing.T) {
			sw := systemPolicySweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
			body := tc.body(t, sw)
			before, mark := systemPolicyState(t, sw), sw.publishMark()
			rr := systemPolicySend(t, sw, sw.tenantA, tc.method, tc.path, body)
			if rr.Code != http.StatusForbidden {
				t.Errorf("A: status=%d, want 403; body=%s", rr.Code, rr.Body.String())
			}
			systemPolicyUnchanged(t, sw, before, mark)
		})
		if tc.part == "" {
			continue
		}
		t.Run("platform/"+tc.name, func(t *testing.T) {
			sw := systemPolicySweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
			body := tc.body(t, sw)
			before := systemPolicyState(t, sw)[tc.part]
			systemPolicyOK(t, systemPolicySend(t, sw, systemPolicyPlatform, tc.method, tc.path, body), "platform")
			if reflect.DeepEqual(before, systemPolicyState(t, sw)[tc.part]) {
				t.Errorf("platform: %s unchanged", tc.part)
			}
		})
	}
	t.Run("spanning/put-global", func(t *testing.T) {
		systemPolicyGlobalAllowed(t, systemPolicySweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform), systemPolicySpanning)
	})
	t.Run("default-fallback/put-global", func(t *testing.T) {
		platform := crossTenantSweepTenant{"default", systemPolicyPlatform.apiKey, systemPolicyPlatform.principalID}
		systemPolicyGlobalAllowed(t, systemPolicySweep(t, "", platform), platform)
	})
	t.Run("empty-tenant/put-global", func(t *testing.T) {
		sw := systemPolicySweep(t, systemPolicyPlatform.tenantID, systemPolicyPlatform)
		before, mark := systemPolicyState(t, sw), sw.publishMark()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/policy/global", strings.NewReader(systemPolicyGlobal))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		sw.server.handlePutPolicyGlobal(rr, withAuth(req, &auth.AuthContext{Role: "admin", PrincipalID: "principal-system-policy-tenantless"}))
		if rr.Code != http.StatusForbidden {
			t.Errorf("tenantless admin: status=%d, want 403; body=%s", rr.Code, rr.Body.String())
		}
		systemPolicyUnchanged(t, sw, before, mark)
	})
}

// systemPolicySweep is the sweep server whose own tenant is gatewayTenant, with
// two more admin keys: platform, and spanning, an admin of A's tenant whose
// access spans all tenants. It seeds system/default with empty topic and pool
// maps, which pack install reads. An admin with that access seeds an enabled
// system bundle and one snapshot; B's key seeds B's shadow of that bundle.
func systemPolicySweep(t *testing.T, gatewayTenant string, platform crossTenantSweepTenant) *crossTenantSweep {
	t.Helper()
	sw := newCrossTenantSweep(t)
	s := sw.server
	s.tenant = gatewayTenant
	var keys []string
	for _, who := range []crossTenantSweepTenant{sw.tenantA, sw.tenantB, platform} {
		keys = append(keys, fmt.Sprintf(`{"key":%q,"tenant":%q,"role":"admin","principal_id":%q}`, who.apiKey, who.tenantID, who.principalID))
	}
	sp := systemPolicySpanning
	keys = append(keys, fmt.Sprintf(`{"key":%q,"tenant":%q,"role":"admin","principal_id":%q,"allow_cross_tenant":true}`, sp.apiKey, sp.tenantID, sp.principalID))
	s.auth = newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS": "[" + strings.Join(keys, ",") + "]"})
	sw.handler = apiKeyMiddleware(s.auth, tenantMiddleware(s.auth, maxBodyMiddleware(sw.mux, s.entitlements)))
	setTestSigningEnv(t, "warn")
	if err := s.configSvc.Set(context.Background(), &configsvc.Document{
		Scope:   configsvc.ScopeSystem,
		ScopeID: "default",
		Data:    map[string]any{"pools": map[string]any{"topics": map[string]any{}, "pools": map[string]any{}}},
	}); err != nil {
		t.Fatalf("seed system/default: %v", err)
	}
	admin := &auth.AuthContext{Role: "admin", Tenant: sw.tenantB.tenantID, AllowCrossTenant: true}
	for _, seed := range []struct{ method, path, body string }{
		{http.MethodPut, systemPolicyBundle, fmt.Sprintf(`{"content":%q,"enabled":true}`, shadowTestValidYAML)},
		{http.MethodPost, "/api/v1/policy/bundles/snapshots", `{}`},
	} {
		req := httptest.NewRequest(seed.method, seed.path, strings.NewReader(seed.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Tenant-ID", sw.tenantB.tenantID)
		rr := httptest.NewRecorder()
		sw.mux.ServeHTTP(rr, withAuth(req, admin))
		systemPolicyOK(t, rr, "seed "+seed.path)
	}
	shadow := "/api/v1/policy/shadows/" + url.PathEscape(systemPolicyBundleID)
	systemPolicyOK(t, sw.as(t, sw.tenantB, http.MethodPut, shadow, fmt.Sprintf(`{"content":%q}`, shadowTestValidYAML)), "seed B's shadow")
	return sw
}

// systemPolicySend sends body with who's key and tenant header. The pack
// install route takes body as the bundle of a multipart upload.
func systemPolicySend(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	if path != systemPolicyInstallPath {
		return sw.as(t, who, method, path, body)
	}
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("bundle", systemPolicyPackID+".tgz")
	if err == nil {
		_, err = part.Write([]byte(body))
	}
	if err == nil {
		err = writer.Close()
	}
	if err != nil {
		t.Fatalf("build pack upload: %v", err)
	}
	req := httptest.NewRequest(method, path, &form)
	req.Header.Set("X-API-Key", who.apiKey)
	req.Header.Set("X-Tenant-ID", who.tenantID)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rr := httptest.NewRecorder()
	sw.handler.ServeHTTP(rr, req)
	return rr
}

func systemPolicyPrepare(t *testing.T, sw *crossTenantSweep, method, path, body string) {
	t.Helper()
	systemPolicyOK(t, systemPolicySend(t, sw, systemPolicyPlatform, method, path, body), "prepare "+method+" "+path)
}

func systemPolicyOK(t *testing.T, rr *httptest.ResponseRecorder, label string) {
	t.Helper()
	if rr.Code < 200 || rr.Code >= 300 {
		t.Fatalf("%s: status=%d, want 2xx; body=%s", label, rr.Code, rr.Body.String())
	}
}

// systemPolicyVelocityRule turns velocity rules on and returns the body of a
// rule with the threshold given, creating the rule first when existing is set.
func systemPolicyVelocityRule(t *testing.T, sw *crossTenantSweep, existing bool, threshold int) string {
	t.Helper()
	setTestEntitlements(t, sw.server, licensing.PlanTeam, func(e *licensing.Entitlements) { e.VelocityRules = true })
	rule := velocityRulePayload(systemPolicyVelocityID)
	if existing {
		systemPolicyPrepare(t, sw, http.MethodPost, systemPolicyVelocity, mustJSON(rule))
	}
	rule["threshold"] = threshold
	return mustJSON(rule)
}

// systemPolicyPack returns a minimal unsigned pack as a .tgz, with strict pack
// verification off so that the pack installs.
func systemPolicyPack(t *testing.T, _ *crossTenantSweep) string {
	t.Helper()
	resetGatewayPackStrictCache()
	t.Setenv(gatewayPackStrictEnv, "")
	return string(buildTarGz(t, map[string]string{
		"pack.yaml": `apiVersion: cordum.io/v1alpha1
kind: Pack
metadata:
  id: ` + systemPolicyPackID + `
  version: 0.1.0
compatibility:
  protocolVersion: 1
resources:
  schemas:
    - id: ` + systemPolicyPackID + `/In
      path: schemas/In.json
`,
		"schemas/In.json": `{"type":"object"}`,
	}))
}

// systemPolicyState reads what a refused write must leave alone. "policy" is
// the system policy document with its snapshots.
func systemPolicyState(t *testing.T, sw *crossTenantSweep) map[string]any {
	t.Helper()
	ctx := context.Background()
	doc := func(scope, id string) *configsvc.Document {
		d, err := sw.server.configSvc.Get(ctx, configsvc.Scope(scope), id)
		if err != nil && !errors.Is(err, redis.Nil) {
			t.Fatalf("read %s/%s: %v", scope, id, err)
		}
		return d
	}
	shadow, err := sw.server.policyShadowStore.Get(ctx, sw.tenantB.tenantID, systemPolicyBundleID)
	if err != nil {
		t.Fatalf("read B's shadow: %v", err)
	}
	policy := [2]*configsvc.Document{doc(packs.PolicyConfigScope, packs.PolicyConfigID), doc(policybundles.PolicySnapshotsScope, policybundles.PolicySnapshotsID)}
	return map[string]any{
		"policy":         policy,
		"system/default": doc(string(configsvc.ScopeSystem), "default"),
		"packs":          doc(packs.PackRegistryScope, packs.PackRegistryID),
		"shadow":         shadow,
	}
}

func systemPolicyUnchanged(t *testing.T, sw *crossTenantSweep, before map[string]any, mark int) {
	t.Helper()
	after := systemPolicyState(t, sw)
	for part, value := range before {
		if !reflect.DeepEqual(value, after[part]) {
			t.Errorf("%s changed", part)
		}
	}
	if got := sw.publishedSince(mark); len(got) != 0 {
		t.Errorf("published %d messages, want none: %v", len(got), got)
	}
}

// systemPolicyGlobalAllowed checks that who's global policy write succeeds and
// changes the system policy document.
func systemPolicyGlobalAllowed(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	t.Helper()
	before := systemPolicyState(t, sw)["policy"]
	systemPolicyOK(t, sw.as(t, who, http.MethodPut, "/api/v1/policy/global", systemPolicyGlobal), "global policy as "+who.tenantID)
	if reflect.DeepEqual(before, systemPolicyState(t, sw)["policy"]) {
		t.Error("system policy document unchanged")
	}
}
