package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cordum/cordum/core/configsvc"
	"github.com/cordum/cordum/core/controlplane/gateway/auth"
	"github.com/cordum/cordum/core/licensing"
	"github.com/cordum/cordum/core/telemetry"
	"github.com/redis/go-redis/v9"
)

func TestDeploymentSettingsNeedPlatformAuthority(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T, *crossTenantSweep, crossTenantSweepTenant)
	}{
		{"oidc-mapping", platformSettingsOIDC},
		{"admin-locks", platformSettingsLocks},
		{"license-reload", platformSettingsLicense},
		{"telemetry-consent", platformSettingsConsent},
	}
	for _, tc := range cases {
		for _, principal := range []string{"platform", "spanning"} {
			t.Run(tc.name+"/"+principal, func(t *testing.T) {
				sw, who := platformSettingsFixture(t, principal)
				tc.run(t, sw, who)
			})
		}
	}
	for _, path := range []string{"status", "usage", "inspect", "export"} {
		for _, principal := range []string{"platform", "spanning"} {
			t.Run("telemetry-"+path+"/"+principal, func(t *testing.T) {
				sw, who := platformSettingsFixture(t, principal)
				platformSettingsTelemetryRead(t, sw, who, path)
			})
		}
	}
	for _, route := range []struct{ method, path string }{{"GET", "/api/v1/scim/settings"}, {"POST", "/api/v1/scim/settings/token"}} {
		t.Run("scim-"+route.method, func(t *testing.T) { platformSettingsSCIM(t, route.method, route.path) })
	}
	t.Run("scim-tenant-boundaries", platformSettingsSCIMTenants)
}

func platformSettingsFixture(t *testing.T, principal string) (*crossTenantSweep, crossTenantSweepTenant) {
	t.Helper()
	sw := newCrossTenantSweep(t)
	setTestEntitlements(t, sw.server, licensing.PlanEnterprise, nil)
	p := crossTenantSweepTenant{"default", "platform-settings-key", "platform-settings-admin"}
	if principal == "spanning" {
		p.tenantID = "operator-tenant"
	}
	keys := fmt.Sprintf(`[{"key":%q,"tenant":%q,"role":"admin"},{"key":%q,"tenant":%q,"role":"admin"},{"key":%q,"tenant":%q,"role":"admin","allow_cross_tenant":%t}]`,
		sw.tenantA.apiKey, sw.tenantA.tenantID, sw.tenantB.apiKey, sw.tenantB.tenantID, p.apiKey, p.tenantID, principal == "spanning")
	basic := newBasicAuthForTest(t, map[string]string{"CORDUM_API_KEYS": keys})
	client, ok := sw.server.jobStore.Client().(*redis.Client)
	if !ok {
		t.Fatal("expected redis client")
	}
	scim, err := auth.NewSCIMService(auth.NewRedisUserStoreFromClient(client), sw.tenantB.tenantID, sw.server.entitlements)
	if err != nil {
		t.Fatal(err)
	}
	oidc := auth.NewOIDCAuthAdapter(&auth.OIDCProvider{}, "default")
	composite, err := auth.NewCompositeAuthProvider(basic, oidc, scim)
	if err != nil {
		t.Fatal(err)
	}
	sw.server.auth = composite
	sw.mux = http.NewServeMux()
	if err := sw.server.registerRoutes(sw.mux); err != nil {
		t.Fatal(err)
	}
	sw.handler = apiKeyMiddleware(composite, tenantMiddleware(composite, maxBodyMiddleware(sw.mux, sw.server.entitlements)))
	return sw, p
}

func platformSettingsStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rr.Code != want {
		t.Errorf("status=%d, want %d; body=%s", rr.Code, want, rr.Body.String())
	}
}

func platformSettingsOIDC(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	const path = "/api/v1/auth/oidc/group-role-mapping"
	const seed = `{"oidc_groups_claim":"platform_groups","oidc_group_role_mapping":{"operators":"admin"}}`
	platformSettingsStatus(t, sw.as(t, who, "PUT", path, seed), http.StatusOK)
	before, err := sw.server.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatal(err)
	}
	provider := sw.server.auth.(auth.AuthConfigProvider)
	live, mark := provider.AuthConfig(), sw.publishMark()
	platformSettingsStatus(t, sw.as(t, sw.tenantA, "PUT", path, `{"oidc_groups_claim":"tenant_groups","oidc_group_role_mapping":{"team":"viewer"}}`), http.StatusForbidden)
	after, err := sw.server.configSvc.Get(context.Background(), configsvc.ScopeSystem, "default")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(live, provider.AuthConfig()) || sw.publishMark() != mark {
		t.Error("mapping document, live config or bus changed")
	}
	platformSettingsStatus(t, sw.as(t, who, "PUT", path, seed), http.StatusOK)
}

func platformSettingsLocks(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	id, holder := seedCrossTenantJob(t, sw)
	key, ctx := "cordum:scheduler:job:"+id, context.Background()
	rdb := sw.server.jobStore.Client()
	if err := rdb.Set(ctx, key, holder, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	before := crossTenantJobMeta(t, sw, id)
	ttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatal(err)
	}
	rr := sw.as(t, sw.tenantA, "GET", "/api/v1/admin/locks", "")
	platformSettingsStatus(t, rr, http.StatusForbidden)
	for _, marker := range []string{holder, id, sw.tenantB.tenantID} {
		if strings.Contains(rr.Body.String(), marker) {
			t.Error("response contains job lock data")
		}
	}
	value, err := rdb.Get(ctx, key).Result()
	afterTTL, ttlErr := rdb.PTTL(ctx, key).Result()
	if err != nil || ttlErr != nil || value != holder || afterTTL != ttl || !reflect.DeepEqual(before, crossTenantJobMeta(t, sw, id)) {
		t.Fatal("job or lock changed")
	}
	rr = sw.as(t, who, "GET", "/api/v1/admin/locks", "")
	platformSettingsStatus(t, rr, http.StatusOK)
	if !strings.Contains(rr.Body.String(), holder) || !strings.Contains(rr.Body.String(), id) {
		t.Fatal("platform response missing seeded lock")
	}
}

func platformSettingsLicense(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	before := sw.server.currentLicenseInfo()
	installReloadableLicense(t, licensing.Claims{OrgID: "platform-org", LicenseID: "platform-license", Plan: "enterprise", ExpiresAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339)})
	platformSettingsStatus(t, sw.as(t, sw.tenantA, "POST", "/api/v1/license/reload", ""), http.StatusForbidden)
	if !reflect.DeepEqual(before, sw.server.currentLicenseInfo()) {
		t.Error("license changed")
	}
	rr := sw.as(t, who, "POST", "/api/v1/license/reload", "")
	platformSettingsStatus(t, rr, http.StatusOK)
	if !strings.Contains(rr.Body.String(), "platform-license") {
		t.Fatal("platform license not loaded")
	}
}

func platformSettingsConsent(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant) {
	seedTelemetryCollector(t, sw.server)
	sw.server.telemetryState = telemetry.NewStoreWithClient(sw.server.jobStore.Client())
	ctx := context.Background()
	if err := sw.server.telemetryState.SetConsentMode(ctx, "local_only"); err != nil {
		t.Fatal(err)
	}
	platformSettingsStatus(t, sw.as(t, sw.tenantA, "POST", "/api/v1/telemetry/consent", `{"mode":"off"}`), http.StatusForbidden)
	mode, err := sw.server.telemetryState.GetConsentMode(ctx)
	if err != nil || mode != "local_only" || sw.server.telemetry.Mode() != telemetry.ModeLocalOnly {
		t.Error("telemetry consent changed")
	}
	platformSettingsStatus(t, sw.as(t, who, "POST", "/api/v1/telemetry/consent", `{"mode":"off"}`), http.StatusOK)
	mode, err = sw.server.telemetryState.GetConsentMode(ctx)
	if err != nil || mode != "off" || sw.server.telemetry.Mode() != telemetry.ModeOff {
		t.Fatal("platform consent was not applied")
	}
}

func platformSettingsTelemetryRead(t *testing.T, sw *crossTenantSweep, who crossTenantSweepTenant, route string) {
	seedTelemetryCollector(t, sw.server)
	path := "/api/v1/telemetry/" + route
	owner := sw.as(t, who, "GET", path, "")
	platformSettingsStatus(t, owner, http.StatusOK)
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(owner.Body.Bytes(), &payload); err != nil || len(payload) == 0 {
		t.Fatalf("missing telemetry payload: %v", err)
	}
	rr := sw.as(t, sw.tenantA, "GET", path, "")
	platformSettingsStatus(t, rr, http.StatusForbidden)
	var denied map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &denied); err != nil {
		t.Fatal(err)
	}
	for field := range payload {
		if _, exists := denied[field]; exists {
			t.Errorf("response contains telemetry field %s", field)
		}
	}
}

func platformSettingsSCIMToken(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	platformSettingsStatus(t, rr, http.StatusOK)
	var response struct {
		BearerToken string `json:"bearerToken"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil || response.BearerToken == "" {
		t.Fatalf("missing SCIM token: %v", err)
	}
	return response.BearerToken
}

func platformSettingsSCIM(t *testing.T, method, path string) {
	sw, operator := platformSettingsFixture(t, "spanning")
	token := platformSettingsSCIMToken(t, sw.as(t, sw.tenantB, "POST", "/api/v1/scim/settings/token", ""))
	do := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, auth.SCIMUsersPath, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		sw.handler.ServeHTTP(rr, req)
		return rr
	}
	platformSettingsStatus(t, do("POST", `{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"platform-settings-user","active":true}`), http.StatusCreated)
	before := do("GET", "").Body.String()
	for _, tenant := range []string{sw.tenantA.tenantID, strings.ToUpper(sw.tenantB.tenantID), "", "   "} {
		req := withAuth(httptest.NewRequest(method, path, nil), &auth.AuthContext{Role: "admin", Tenant: tenant})
		rr := httptest.NewRecorder()
		if tenant == sw.tenantA.tenantID {
			rr = sw.as(t, sw.tenantA, method, path, "")
		} else {
			sw.mux.ServeHTTP(rr, req)
		}
		platformSettingsStatus(t, rr, http.StatusForbidden)
		if strings.Contains(rr.Body.String(), token) || strings.Contains(rr.Body.String(), "platform-settings-user") {
			t.Error("response contains SCIM settings")
		}
		after := do("GET", "")
		if after.Code != http.StatusOK || after.Body.String() != before {
			t.Error("SCIM token or users changed")
		}
	}
	platformSettingsStatus(t, sw.as(t, sw.tenantB, method, path, ""), http.StatusOK)
	platformSettingsStatus(t, sw.as(t, operator, method, path, ""), http.StatusOK)
}

func platformSettingsSCIMTenants(t *testing.T) {
	sw, _ := platformSettingsFixture(t, "platform")
	client := sw.server.jobStore.Client().(*redis.Client)
	for _, tenant := range []string{" Team ", ""} {
		svc, err := auth.NewSCIMService(auth.NewRedisUserStoreFromClient(client), tenant, sw.server.entitlements)
		if err != nil {
			t.Fatal(err)
		}
		caller := " Team "
		if tenant == "" {
			caller = " default "
		}
		mux := http.NewServeMux()
		svc.RegisterRoutes(mux, nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, withAuth(httptest.NewRequest("GET", "/api/v1/scim/settings", nil), &auth.AuthContext{Role: "admin", Tenant: caller}))
		platformSettingsStatus(t, rr, http.StatusOK)
	}
}
